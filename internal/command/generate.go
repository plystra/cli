package command

import (
	"context"
	"errors"
	"fmt"
	"io"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/version"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
)

var (
	errGenerateInvocation = errors.New("invalid plystra generate invocation")
	errGenerateInternal   = errors.New("internal plystra generate result failure")
)

type generateResultEncoder struct {
	invocationID string
	emptyEffects commandschema.Effects
}

type generateFailureInput struct {
	err          error
	result       applicationgenerate.Result
	observations []datacompiler.Observation
	context      recoveryContext
	report       *generatedfiles.Report
}

type generateFailureClassification struct {
	status    commandschema.Status
	code      string
	message   string
	recovery  string
	locations []diagnosticjson.Source
}

func runGenerate(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if len(arguments) == 2 && isHelp(arguments[1]) {
		_, _ = io.WriteString(stdout, generateUsage)
		return 0
	}
	format := generateFormatIntent(arguments)
	output, err := newCommandOutput(format, stdout, stderr)
	if err != nil {
		return 8
	}
	encoder, initialized := initializeGenerateResultEncoder(output.diagnosticWriter())
	if !initialized {
		return 8
	}
	parsed, valid := parseGenerateArguments(arguments)
	if !valid {
		return emitGenerateFailure(output, encoder, generateFailureInput{err: errGenerateInvocation}, func() error {
			_, writeErr := io.WriteString(output.diagnosticWriter(), generateUsage)
			return writeErr
		})
	}
	recovery := commandRecoveryContext(parsed.configurationPath, parsed.environmentName, environment)
	if parsed.configurationPath != "" && parsed.environmentName != "" {
		return emitGenerateFailure(output, encoder, generateFailureInput{
			err:     fmt.Errorf("%w: --config and --env cannot be used together", applicationresolve.ErrConfigurationSelection),
			context: recovery,
		}, func() error {
			writeCommandFailure(output.diagnosticWriter(), "", fmt.Errorf("%w: --config and --env cannot be used together", applicationresolve.ErrConfigurationSelection), recovery)
			return nil
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	result, observations, generateErr := executeGenerate(ctx, parsed, workingDirectory, environment)
	if generateErr != nil {
		return emitGenerateFailure(output, encoder, generateFailureInput{err: generateErr, result: result, observations: observations, context: recovery}, func() error {
			writeCommandFailure(output.diagnosticWriter(), "", generateErr, recovery)
			return nil
		})
	}
	if !result.Report().Clean() {
		return emitGenerateFailure(output, encoder, generateFailureInput{
			result:       result,
			observations: observations,
			context:      recovery,
			report:       reportPointer(result.Report()),
		}, func() error {
			heading := "generated output remains inconsistent after installation"
			if result.Checked() {
				heading = "generated output is not current"
			}
			writeGenerationReport(output.diagnosticWriter(), heading, result.Module().ModulePath(), result.Report(), recovery)
			return nil
		})
	}

	structured, buildErr := encoder.success(result, observations)
	if buildErr != nil {
		return emitGenerateInternalFailure(output, encoder, result, buildErr)
	}
	return writeGenerateResult(output, structured, func() error {
		if result.Checked() {
			_, err := fmt.Fprintf(output.resultWriter(), "generated output is current for %s in %s\n", result.Module().ModulePath(), result.Module().Path())
			return err
		}
		_, err := fmt.Fprintf(output.resultWriter(), "generated %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		return err
	})
}

func executeGenerate(ctx context.Context, arguments generateArguments, workingDirectory string, environment []string) (applicationgenerate.Result, []datacompiler.Observation, error) {
	options := applicationgenerate.Options{
		Start:             workingDirectory,
		Check:             arguments.check,
		Offline:           arguments.offline,
		ConfigurationPath: arguments.configurationPath,
		EnvironmentName:   arguments.environmentName,
		Environment:       environment,
	}
	if arguments.check || arguments.offline {
		result, err := applicationgenerate.Generate(ctx, options)
		return result, result.DataCompilerObservations(), err
	}
	project, err := projectlocate.Find(workingDirectory)
	if err != nil {
		return applicationgenerate.Result{}, nil, fmt.Errorf("locate Project: %w", err)
	}
	var result applicationgenerate.Result
	var observations []datacompiler.Observation
	generateWithMutation := func(mutate applicationgenerate.ModuleMutation) error {
		options.MutateModule = mutate
		var generateErr error
		result, generateErr = applicationgenerate.Generate(ctx, options)
		observations = append(observations, result.DataCompilerObservations()...)
		return generateErr
	}
	err = modulemutation.Tidy(ctx, project.Path(), options.GoCommand, environment, generateWithMutation)
	var dependencySource *applicationgenerate.DependencySourceError
	if errors.Is(err, applicationgenerate.ErrKernelDependency) && errors.As(err, &dependencySource) {
		err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
			GoCommand:          options.GoCommand,
			Environment:        environment,
			Arguments:          []string{"get", kernelintrinsic.ModulePath + "@" + version.KernelVersion},
			DirectRequirements: []string{kernelintrinsic.ModulePath},
		}, generateWithMutation)
	}
	return result, observations, err
}

func initializeGenerateResultEncoder(stderr io.Writer) (generateResultEncoder, bool) {
	invocationID, err := generateInvocationID()
	if err != nil {
		_, _ = io.WriteString(stderr, "initialize plystra generate result: internal failure\n")
		return generateResultEncoder{}, false
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		_, _ = io.WriteString(stderr, "initialize plystra generate result: internal failure\n")
		return generateResultEncoder{}, false
	}
	return generateResultEncoder{invocationID: invocationID, emptyEffects: effects}, true
}

func (e generateResultEncoder) success(result applicationgenerate.Result, observations []datacompiler.Observation) (commandschema.Result, error) {
	snapshot, err := generateSnapshot(result)
	if err != nil {
		return commandschema.Result{}, err
	}
	payload, err := generatePayload(result)
	if err != nil {
		return commandschema.Result{}, err
	}
	effects, err := generateEffects(result, observations, nil, result.Installed())
	if err != nil {
		return commandschema.Result{}, err
	}
	status := commandschema.StatusSuccess
	if result.Checked() {
		status = commandschema.StatusNoOp
		effects = e.emptyEffects
	}
	return commandschema.NewResult(commandschema.ResultInput{
		Operation:    "generate",
		InvocationID: e.invocationID,
		Snapshot:     snapshot,
		Status:       status,
		Effects:      effects,
		Payload:      payload,
	})
}

func (e generateResultEncoder) failure(input generateFailureInput) (commandschema.Result, error) {
	classification := classifyGenerateFailure(input)
	diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{
		Code:      classification.code,
		Severity:  diagnosticjson.SeverityError,
		Message:   classification.message,
		Locations: classification.locations,
	})
	if err != nil {
		return commandschema.Result{}, err
	}
	recovery, err := generateFailureRecovery(input, classification)
	if err != nil {
		return commandschema.Result{}, err
	}
	effects, err := generateEffects(input.result, input.observations, input.err, false)
	if err != nil {
		return commandschema.Result{}, err
	}
	return commandschema.NewResult(commandschema.ResultInput{
		Operation:    "generate",
		InvocationID: e.invocationID,
		Snapshot:     generateSnapshotOrNil(input.result),
		Status:       classification.status,
		Diagnostics:  []commandschema.Diagnostic{diagnostic},
		Recovery:     []commandschema.Recovery{recovery},
		Effects:      effects,
	})
}

func classifyGenerateFailure(input generateFailureInput) generateFailureClassification {
	if input.report != nil {
		code := diagnosticcode.GeneratedDrift
		message := "Generated output is not current."
		recovery := "Run `plystra generate" + input.context.selectorSuffix() + "` to restore the selected generated output."
		var sources []diagnosticjson.Source
		for _, change := range input.report.Changes() {
			sources = append(sources, diagnosticjson.Source{Module: input.result.Module().ModulePath(), Path: change.Path(), Kind: "generated-artifact"})
		}
		if len(input.report.Unexpected()) != 0 {
			code = diagnosticcode.GeneratedUnexpectedOutput
			message = "Generated output contains unexpected unowned paths."
			recovery = "Move every unexpected unowned path outside generated/, then run `plystra generate" + input.context.selectorSuffix() + "`."
			sources = sources[:0]
			for _, path := range input.report.Unexpected() {
				sources = append(sources, diagnosticjson.Source{Module: input.result.Module().ModulePath(), Path: path, Kind: "generated-artifact"})
			}
		}
		return generateFailureClassification{status: commandschema.StatusValidationFailed, code: code, message: message, recovery: recovery, locations: canonicalSources(sources)}
	}
	if errors.Is(input.err, errGenerateInvocation) {
		return generateFailureClassification{
			status:    commandschema.StatusInvalidInvocation,
			code:      diagnosticcode.GenerateInvocationInvalid,
			message:   "The plystra generate invocation is invalid.",
			recovery:  "Review `plystra generate --help`, then rerun one complete generation command.",
			locations: nil,
		}
	}
	if errors.Is(input.err, errGenerateInternal) {
		return generateFailureClassification{
			status:    commandschema.StatusExecutionFailed,
			code:      diagnosticcode.GenerateFailed,
			message:   "Plystra could not construct the generation result.",
			recovery:  "Retry `plystra generate" + input.context.selectorSuffix() + "`; report the failure if it persists.",
			locations: nil,
		}
	}
	if errors.Is(input.err, context.Canceled) || errors.Is(input.err, context.DeadlineExceeded) {
		return generateFailureClassification{
			status:    commandschema.StatusCancelled,
			code:      diagnosticcode.GenerateFailed,
			message:   "Generation was cancelled before it completed.",
			recovery:  "Rerun `plystra generate" + input.context.selectorSuffix() + "` after the cancellation condition is resolved.",
			locations: nil,
		}
	}
	if actionable, found := primaryActionableDiagnostic(input.err, input.context); found {
		status := generateStatusForDiagnostic(actionable.code)
		message := generateFailureMessage(status)
		return generateFailureClassification{
			status:    status,
			code:      actionable.code,
			message:   message,
			recovery:  actionable.recovery,
			locations: canonicalSources(actionableDiagnosticSources(input.err, actionable.code)),
		}
	}
	return generateFailureClassification{
		status:    commandschema.StatusExecutionFailed,
		code:      diagnosticcode.GenerateFailed,
		message:   "Plystra could not complete generation.",
		recovery:  "Inspect the generation failure, then rerun `plystra generate" + input.context.selectorSuffix() + "`.",
		locations: nil,
	}
}

func generateStatusForDiagnostic(code string) commandschema.Status {
	switch code {
	case diagnosticcode.DataCompilerUnavailable, diagnosticcode.GoModuleUnavailable, diagnosticcode.GoCommandFailed:
		return commandschema.StatusPrerequisiteMissing
	case diagnosticcode.ProviderAmbiguous, diagnosticcode.ResolveMultipleImplementations, diagnosticcode.ResourceBindingAmbiguous:
		return commandschema.StatusDecisionRequired
	case diagnosticcode.GenerationCompileFailed, diagnosticcode.GenerationExecutionFailed, diagnosticcode.GenerationExtensionFailed, diagnosticcode.GenerationCrashed, diagnosticcode.GenerationTimeout, diagnosticcode.GenerationRequestTooLarge, diagnosticcode.GenerationOutputTooLarge, diagnosticcode.GenerationOutputMalformed, diagnosticcode.GenerationOutputInvalid, diagnosticcode.GenerationExtensionDiagnostic:
		return commandschema.StatusExecutionFailed
	default:
		return commandschema.StatusValidationFailed
	}
}

func generateFailureMessage(status commandschema.Status) string {
	switch status {
	case commandschema.StatusDecisionRequired:
		return "The selected Project requires an explicit decision before generation can complete."
	case commandschema.StatusPrerequisiteMissing:
		return "A required generation prerequisite is unavailable."
	case commandschema.StatusExecutionFailed:
		return "Plystra could not complete generation."
	default:
		return "The selected Project is invalid for generation."
	}
}

func generateFailureRecovery(failureInput generateFailureInput, classification generateFailureClassification) (commandschema.Recovery, error) {
	selector, selectorArguments, valid := explainRecoverySelectorFromContext(failureInput.context)
	verificationArguments := append([]string{"plystra", "generate"}, selectorArguments...)
	kind := commandschema.RecoveryManual
	targetKind, targetID := "operation", "generate"
	if classification.code == diagnosticcode.DataCompilerUnavailable {
		kind = commandschema.RecoverySatisfyPrerequisite
		targetKind, targetID = "tool", "data-compiler"
	}
	recoveryInput := commandschema.RecoveryInput{
		ID:            "resolve-generate-failure",
		Kind:          kind,
		Target:        commandschema.RecoveryTarget{Kind: targetKind, ID: targetID},
		Provenance:    recoverySources(classification.locations),
		Preconditions: []commandschema.RecoveryFact{{Kind: "failure_resolved"}},
		Verification:  commandschema.Verification{WorkingDirectory: ".", Argv: verificationArguments},
	}
	if classification.code == diagnosticcode.GenerateInvocationInvalid {
		recoveryInput.ID = "correct-generate-invocation"
		recoveryInput.Target = commandschema.RecoveryTarget{Kind: "command", ID: "generate"}
		recoveryInput.Preconditions = []commandschema.RecoveryFact{{Kind: "valid_invocation"}}
	}
	if classification.code == diagnosticcode.ProviderAmbiguous && valid {
		var ambiguous *providerresolution.AmbiguousProviderError
		if errors.As(failureInput.err, &ambiguous) && ambiguous != nil {
			options := make([]commandschema.RecoveryOption, 0, len(ambiguous.Providers()))
			for _, provider := range ambiguous.Providers() {
				options = append(options, commandschema.RecoveryOption{Value: provider.PluginID()})
			}
			recoveryInput.ID = "choose-capability-provider"
			recoveryInput.Kind = commandschema.RecoveryChoose
			recoveryInput.Target = commandschema.RecoveryTarget{Kind: "configuration_field", ID: fmt.Sprintf("capabilities.use[%q]", ambiguous.Capability().String())}
			recoveryInput.Preconditions = []commandschema.RecoveryFact{{Kind: "compatible_provider_selected"}}
			recoveryInput.Effects = []commandschema.RecoveryFact{{Kind: "provider_selection_changed"}}
			recoveryInput.Options = options
		}
	}
	if classification.code == diagnosticcode.ResolveMultipleImplementations && valid {
		var ambiguous *interfaceresolution.AmbiguousImplementationError
		if errors.As(failureInput.err, &ambiguous) && ambiguous != nil {
			options := make([]commandschema.RecoveryOption, 0, len(ambiguous.Candidates()))
			for _, candidate := range ambiguous.Candidates() {
				value := candidate.Constructor().String()
				argv := append([]string{"plystra", "use", ambiguous.InterfaceID().String(), value}, selectorArguments...)
				options = append(options, commandschema.RecoveryOption{Value: value, WorkingDirectory: ".", Argv: argv})
			}
			recoveryInput.ID = "choose-interface-implementation"
			recoveryInput.Kind = commandschema.RecoveryChoose
			recoveryInput.Target = commandschema.RecoveryTarget{Kind: "interface_implementation", ID: ambiguous.InterfaceID().String()}
			recoveryInput.Preconditions = []commandschema.RecoveryFact{{Kind: "compatible_implementation_selected"}}
			recoveryInput.Effects = []commandschema.RecoveryFact{{Kind: "implementation_selection_changed"}}
			recoveryInput.Options = options
		}
	}
	if classification.status == commandschema.StatusValidationFailed {
		if first, editable := exactExplainEditableSource(classification.code, classification.locations); editable {
			recoveryInput.ID = "correct-generate-project-source"
			recoveryInput.Kind = commandschema.RecoveryEditSource
			recoveryInput.Target = commandschema.RecoveryTarget{Kind: "source", ID: first.Path}
			recoveryInput.Owner = &commandschema.Owner{Module: first.Module, Path: first.Path}
			recoveryInput.Preconditions = []commandschema.RecoveryFact{{Kind: "source_valid"}}
			recoveryInput.Effects = []commandschema.RecoveryFact{{Kind: "generation_succeeds"}}
		}
	}
	if valid {
		recoveryInput.Selector = selector
	}
	return commandschema.NewRecovery(recoveryInput)
}

func generateEffects(result applicationgenerate.Result, observations []datacompiler.Observation, failure error, installation bool) (commandschema.Effects, error) {
	modulePath := result.Module().ModulePath()
	if modulePath == "" {
		var unavailable *applicationresolve.DataCompilerUnavailableError
		if errors.As(failure, &unavailable) && unavailable != nil {
			modulePath = unavailable.Source().ModulePath()
		}
	}
	if modulePath == "" {
		return commandschema.NewEffects(commandschema.EffectsInput{})
	}
	observed := make([]commandschema.Effect, 0, len(observations)+1)
	occurrences := make(map[string]int)
	seen := make(map[string]struct{})
	for _, observation := range observations {
		occurrences[observation.ID]++
		effectID := observation.ID
		if occurrences[observation.ID] > 1 {
			effectID = fmt.Sprintf("%s-%d", observation.ID, occurrences[observation.ID])
		}
		class, ok := generateEffectClass(observation.Class)
		if !ok {
			return commandschema.Effects{}, fmt.Errorf("unsupported Data compiler observation class %q", observation.Class)
		}
		effect, err := commandschema.NewEffect(commandschema.EffectInput{
			ID:            effectID,
			Class:         class,
			Phase:         observation.Phase,
			Target:        observation.Target,
			Owner:         commandschema.Owner{Module: modulePath, Path: "."},
			Reason:        observation.Reason,
			Reversibility: observation.Reversibility,
			Verification:  observation.Verification,
		})
		if err != nil {
			return commandschema.Effects{}, err
		}
		seen[effectID] = struct{}{}
		observed = append(observed, effect)
	}
	if installation && failure == nil && !result.Checked() {
		effectID := "generate-project"
		for suffix := 2; ; suffix++ {
			if _, exists := seen[effectID]; !exists {
				break
			}
			effectID = fmt.Sprintf("generate-project-%d", suffix)
		}
		effect, err := commandschema.NewEffect(commandschema.EffectInput{
			ID:            effectID,
			Class:         commandschema.EffectProjectWrite,
			Phase:         "commit",
			Target:        ".",
			Owner:         commandschema.Owner{Module: modulePath, Path: "."},
			Reason:        "generated_output_installation",
			Reversibility: "automatic",
			Verification:  []string{"plystra", "generate", "--check"},
		})
		if err != nil {
			return commandschema.Effects{}, err
		}
		observed = append(observed, effect)
	}
	return commandschema.NewEffects(commandschema.EffectsInput{Observed: observed})
}

func generateEffectClass(class datacompiler.ObservationClass) (commandschema.EffectClass, bool) {
	switch class {
	case datacompiler.ObservationCacheMaterialization:
		return commandschema.EffectCacheMaterialization, true
	case datacompiler.ObservationTemporaryFile:
		return commandschema.EffectTemporaryFile, true
	case datacompiler.ObservationTrustedExecution:
		return commandschema.EffectTrustedCodeExecution, true
	default:
		return "", false
	}
}

func generatePayload(result applicationgenerate.Result) (commandschema.GeneratePayload, error) {
	input := commandschema.GeneratePayloadInput{
		ModulePath: result.Module().ModulePath(),
		Mode:       "install",
	}
	if result.Checked() {
		input.Mode = "check"
	}
	if acquisition, ok := result.DataCompilerAcquisition(); ok {
		input.DataCompiler = &commandschema.GenerateDataCompilerInput{
			ModulePath:     acquisition.ModulePath(),
			ModuleVersion:  acquisition.ModuleVersion(),
			ModuleChecksum: acquisition.ModuleChecksum(),
			ManifestDigest: acquisition.ManifestDigest(),
			BinaryDigest:   acquisition.BinaryDigest(),
			GoToolchain:    acquisition.GoToolchain(),
			GOOS:           acquisition.GOOS(),
			GOARCH:         acquisition.GOARCH(),
			CacheHit:       acquisition.CacheHit(),
			Offline:        acquisition.Offline(),
		}
	}
	return commandschema.NewGeneratePayload(input)
}

func generateSnapshot(result applicationgenerate.Result) (*commandschema.SelectorSnapshot, error) {
	selection, exists := result.ConfigurationSelection()
	if !exists {
		return nil, nil
	}
	input := commandschema.SelectorSnapshotInput{Mode: generation.ConfigurationMode(selection.Mode())}
	switch input.Mode {
	case generation.ConfigurationModeEnvironment:
		input.Name = selection.Environment()
	case generation.ConfigurationModeExplicit:
		input.Path = selection.Path()
	}
	snapshot, err := commandschema.NewSelectorSnapshot(input)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func generateSnapshotOrNil(result applicationgenerate.Result) *commandschema.SelectorSnapshot {
	snapshot, err := generateSnapshot(result)
	if err != nil {
		return nil
	}
	return snapshot
}

func emitGenerateFailure(output commandOutput, encoder generateResultEncoder, input generateFailureInput, human func() error) int {
	result, err := encoder.failure(input)
	if err != nil {
		return emitGenerateInternalFailure(output, encoder, input.result, err)
	}
	exitCode := writeGenerateResult(output, result, human)
	if output.format == commandFormatHuman {
		if errors.Is(input.err, errGenerateInvocation) {
			return 2
		}
		return 1
	}
	return exitCode
}

func emitGenerateInternalFailure(output commandOutput, encoder generateResultEncoder, applicationResult applicationgenerate.Result, cause error) int {
	result, err := encoder.failure(generateFailureInput{err: fmt.Errorf("%w: %v", errGenerateInternal, cause), result: applicationResult})
	if err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "build generate result: %v\n", cause)
		return 8
	}
	return writeGenerateResult(output, result, func() error {
		_, writeErr := io.WriteString(output.diagnosticWriter(), "build generate result: internal failure\n")
		return writeErr
	})
}

func writeGenerateResult(output commandOutput, result commandschema.Result, human func() error) int {
	if output.format == commandFormatJSON {
		if _, err := output.resultWriter().Write(append(result.CanonicalJSON(), '\n')); err != nil {
			return 8
		}
		return result.ExitClass()
	}
	if human != nil {
		if err := human(); err != nil {
			return 8
		}
	}
	return result.ExitClass()
}

func generateFormatIntent(arguments []string) commandFormat {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--format" && arguments[index+1] == string(commandFormatJSON) {
			return commandFormatJSON
		}
	}
	return commandFormatHuman
}

func canonicalSources(values []diagnosticjson.Source) []diagnosticjson.Source {
	canonical, err := diagnosticjson.CanonicalizeSources(values)
	if err != nil {
		return nil
	}
	return canonical
}

func recoverySources(values []diagnosticjson.Source) []commandschema.RecoverySource {
	result := make([]commandschema.RecoverySource, len(values))
	for index, value := range values {
		result[index] = commandschema.RecoverySource{Module: value.Module, Path: value.Path, Kind: value.Kind, Line: value.Line, Column: value.Column}
	}
	return result
}

func reportPointer(report generatedfiles.Report) *generatedfiles.Report { return &report }
