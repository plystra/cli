package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

var (
	errExplainInvocation     = errors.New("invalid plystra explain invocation")
	errExplainSubject        = errors.New("invalid plystra explain subject")
	errExplainTargetNotFound = errors.New("explanation target not found")
)

type explainResolver func(context.Context, applicationresolve.Options) (applicationresolve.Result, error)

type explainDependencies struct {
	resolve      explainResolver
	invocationID invocationIDGenerator
}

type explainResultEncoder struct {
	invocationID     string
	emptyEffects     commandschema.Effects
	buildResult      func(commandschema.ResultInput) (commandschema.Result, error)
	internalFailures map[string]commandschema.Result
}

type explainFailureInput struct {
	operation string
	err       error
	snapshot  *commandschema.SelectorSnapshot
	arguments *explainArguments
	context   recoveryContext
}

type explainFailureClassification struct {
	status    commandschema.Status
	code      string
	message   string
	locations []diagnosticjson.Source
}

type explainTargetNotFoundError struct {
	kind    diagnosticschema.ExplainSubjectKind
	subject string
}

func (e *explainTargetNotFoundError) Error() string {
	if e == nil {
		return errExplainTargetNotFound.Error()
	}
	return fmt.Sprintf("%s %q is not present in the selected application model", e.kind, e.subject)
}

func (*explainTargetNotFoundError) Unwrap() error { return errExplainTargetNotFound }

func newExplainTargetNotFound(kind diagnosticschema.ExplainSubjectKind, subject string) error {
	return &explainTargetNotFoundError{kind: kind, subject: subject}
}

func defaultExplainDependencies() explainDependencies {
	return explainDependencies{
		resolve:      applicationresolve.Resolve,
		invocationID: generateInvocationID,
	}
}

var explainOperations = []string{
	"explain",
	"explain.alias",
	"explain.capability",
	"explain.config",
	"explain.exposure",
	"explain.plugin",
}

func runExplainWithDependencies(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string, dependencies explainDependencies) int {
	format := explainFormatIntent(arguments)
	output, err := newCommandOutput(format, stdout, stderr)
	if err != nil {
		writeExplainInitializationFailure(stderr)
		return 8
	}
	encoder, initialized := initializeExplainResultEncoder(output.diagnosticWriter(), dependencies.invocationID)
	if !initialized {
		return 8
	}

	operation := explainOperationIntent(arguments)
	parsed, valid := parseExplainArguments(arguments)
	if !valid {
		return emitExplainFailure(output, encoder, explainFailureInput{
			operation: operation,
			err:       errExplainInvocation,
		}, func() error { return writeHumanExplainInvalidInvocation(output.diagnosticWriter()) })
	}
	operation = explainOperation(parsed.subjectKind)
	recovery := commandRecoveryContext(parsed.configurationPath, parsed.environmentName, environment)
	recovery.operation = operation
	if subjectErr := diagnosticschema.ValidateExplainSubject(parsed.subjectKind, parsed.subject); subjectErr != nil {
		err = fmt.Errorf("%w: %v", errExplainSubject, subjectErr)
		return emitExplainFailure(output, encoder, explainFailureInput{
			operation: operation,
			err:       err,
			arguments: &parsed,
			context:   recovery,
		}, func() error { return writeHumanExplainFailure(output.diagnosticWriter(), "", err, recovery) })
	}
	if parsed.configurationPath != "" && parsed.environmentName != "" {
		err = fmt.Errorf("%w: --config and --env cannot be used together", applicationresolve.ErrConfigurationSelection)
		return emitExplainFailure(output, encoder, explainFailureInput{
			operation: operation,
			err:       err,
			arguments: &parsed,
			context:   recovery,
		}, func() error { return writeHumanExplainFailure(output.diagnosticWriter(), "", err, recovery) })
	}

	if format == commandFormatHuman {
		_, _ = io.WriteString(output.diagnosticWriter(), "Resolving selected application model...\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	if dependencies.resolve == nil {
		err = errors.New("explain resolver is unavailable")
	} else {
		var resolved applicationresolve.Result
		resolved, err = dependencies.resolve(ctx, applicationresolve.Options{
			Start:             workingDirectory,
			ConfigurationPath: parsed.configurationPath,
			EnvironmentName:   parsed.environmentName,
			Environment:       environment,
		})
		if err == nil {
			return emitResolvedExplanation(output, encoder, operation, parsed, recovery, resolved)
		}
	}
	return emitExplainFailure(output, encoder, explainFailureInput{
		operation: operation,
		err:       err,
		arguments: &parsed,
		context:   recovery,
	}, func() error {
		return writeHumanExplainFailure(output.diagnosticWriter(), "explain selected application", err, recovery)
	})
}

func emitResolvedExplanation(output commandOutput, encoder explainResultEncoder, operation string, parsed explainArguments, recovery recoveryContext, resolved applicationresolve.Result) int {
	snapshot, err := explainSelectorSnapshot(resolved.ResolutionEvidence())
	if err != nil {
		return emitExplainFailure(output, encoder, explainFailureInput{
			operation: operation,
			err:       err,
			arguments: &parsed,
			context:   recovery,
		}, func() error {
			return writeHumanExplainFailure(output.diagnosticWriter(), "build explanation snapshot", err, recovery)
		})
	}

	explanation, err := buildExplanation(resolved, parsed)
	if err != nil {
		return emitExplainFailure(output, encoder, explainFailureInput{
			operation: operation,
			err:       err,
			snapshot:  &snapshot,
			arguments: &parsed,
			context:   recovery,
		}, func() error {
			return writeHumanExplainFailure(output.diagnosticWriter(), fmt.Sprintf("explain %s %s", parsed.subjectKind, parsed.subject), err, recovery)
		})
	}
	result, err := encoder.success(operation, explanation, snapshot, resolved.ResolutionEvidence())
	if err != nil {
		return emitExplainInternalFailure(output, encoder, operation, &snapshot)
	}
	return writeExplainResult(output, result, func() error {
		if err := writeHumanExplanation(output.resultWriter(), explanation, parsed.verbose); err != nil {
			_, _ = fmt.Fprintf(output.diagnosticWriter(), "render %s explanation: %v\n", parsed.subjectKind, err)
			return err
		}
		return nil
	})
}

func buildExplanation(resolved applicationresolve.Result, parsed explainArguments) (commandExplanation, error) {
	switch parsed.subjectKind {
	case diagnosticschema.ExplainSubjectCapability:
		return explainCapability(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectPlugin:
		return explainPlugin(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectConfiguration:
		return explainConfiguration(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectAlias:
		return explainAlias(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectExposure:
		return explainPublicExposure(resolved, parsed.subject)
	default:
		return commandExplanation{}, fmt.Errorf("explanation subject kind %q is not implemented", parsed.subjectKind)
	}
}

func explainFormatIntent(arguments []string) commandFormat {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--format" && arguments[index+1] == string(commandFormatJSON) {
			return commandFormatJSON
		}
	}
	return commandFormatHuman
}

func explainOperationIntent(arguments []string) string {
	if len(arguments) >= 2 {
		switch arguments[1] {
		case "capability":
			return "explain.capability"
		case "plugin":
			return "explain.plugin"
		case "config":
			return "explain.config"
		case "alias":
			return "explain.alias"
		case "exposure":
			return "explain.exposure"
		}
	}
	return "explain"
}

func explainOperation(kind diagnosticschema.ExplainSubjectKind) string {
	switch kind {
	case diagnosticschema.ExplainSubjectCapability:
		return "explain.capability"
	case diagnosticschema.ExplainSubjectPlugin:
		return "explain.plugin"
	case diagnosticschema.ExplainSubjectConfiguration:
		return "explain.config"
	case diagnosticschema.ExplainSubjectAlias:
		return "explain.alias"
	case diagnosticschema.ExplainSubjectExposure:
		return "explain.exposure"
	default:
		return "explain"
	}
}

func initializeExplainResultEncoder(stderr io.Writer, generateID invocationIDGenerator) (explainResultEncoder, bool) {
	if generateID == nil {
		writeExplainInitializationFailure(stderr)
		return explainResultEncoder{}, false
	}
	invocationID, err := generateID()
	if err != nil {
		writeExplainInitializationFailure(stderr)
		return explainResultEncoder{}, false
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		writeExplainInitializationFailure(stderr)
		return explainResultEncoder{}, false
	}
	encoder := explainResultEncoder{
		invocationID:     invocationID,
		emptyEffects:     effects,
		buildResult:      commandschema.NewResult,
		internalFailures: make(map[string]commandschema.Result, len(explainOperations)),
	}
	for _, operation := range explainOperations {
		result, err := newExplainInternalFailureResult(invocationID, effects, operation, nil)
		if err != nil {
			writeExplainInitializationFailure(stderr)
			return explainResultEncoder{}, false
		}
		encoder.internalFailures[operation] = result
	}
	return encoder, true
}

func writeExplainInitializationFailure(stderr io.Writer) {
	if stderr != nil {
		_, _ = io.WriteString(stderr, "initialize plystra explain result: internal failure\n")
	}
}

func (e explainResultEncoder) success(operation string, explanation commandExplanation, snapshot commandschema.SelectorSnapshot, evidence resolutionevidence.Evidence) (commandschema.Result, error) {
	payload, err := commandschema.NewDiagnosticPayload(explanation.result.Envelope())
	if err != nil {
		return commandschema.Result{}, err
	}
	recovery, err := explainSuccessRecovery(explanation.result, evidence)
	if err != nil {
		return commandschema.Result{}, err
	}
	return e.buildResult(commandschema.ResultInput{
		Operation:    operation,
		InvocationID: e.invocationID,
		Snapshot:     &snapshot,
		Status:       commandschema.StatusSuccess,
		Recovery:     []commandschema.Recovery{recovery},
		Effects:      e.emptyEffects,
		Payload:      payload,
	})
}

func (e explainResultEncoder) failure(input explainFailureInput) (commandschema.Result, error) {
	classification := classifyExplainFailure(input.err, input.context)
	diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{
		Code:      classification.code,
		Severity:  diagnosticjson.SeverityError,
		Message:   classification.message,
		Locations: classification.locations,
	})
	if err != nil {
		return commandschema.Result{}, err
	}
	recovery, err := explainFailureRecovery(input, classification)
	if err != nil {
		return commandschema.Result{}, err
	}
	return e.buildResult(commandschema.ResultInput{
		Operation:    input.operation,
		InvocationID: e.invocationID,
		Snapshot:     input.snapshot,
		Status:       classification.status,
		Diagnostics:  []commandschema.Diagnostic{diagnostic},
		Recovery:     []commandschema.Recovery{recovery},
		Effects:      e.emptyEffects,
	})
}

func classifyExplainFailure(err error, recovery recoveryContext) explainFailureClassification {
	switch {
	case errors.Is(err, errExplainInvocation):
		return explainFailureClassification{
			status:  commandschema.StatusInvalidInvocation,
			code:    diagnosticcode.ExplainInvocationInvalid,
			message: "The plystra explain invocation is invalid.",
		}
	case errors.Is(err, errExplainSubject):
		return explainFailureClassification{
			status:  commandschema.StatusInvalidInvocation,
			code:    diagnosticcode.ExplainSubjectInvalid,
			message: "The requested explanation subject is invalid.",
		}
	case errors.Is(err, errExplainTargetNotFound):
		return explainFailureClassification{
			status:  commandschema.StatusValidationFailed,
			code:    diagnosticcode.ExplainTargetNotFound,
			message: "The requested explanation target is not present in the selected application model.",
		}
	}
	actionable, found := primaryActionableDiagnostic(err, recovery)
	if !found {
		return explainFailureClassification{
			status:  commandschema.StatusExecutionFailed,
			code:    diagnosticcode.ExplainFailed,
			message: "Plystra could not complete the requested explanation.",
		}
	}
	status := commandschema.StatusValidationFailed
	switch actionable.code {
	case diagnosticProviderAmbiguous, diagnosticResolveMultipleImplementations, diagnosticConfigurationOwnershipAmbiguous:
		status = commandschema.StatusDecisionRequired
	case diagnosticGoModuleUnavailable, diagnosticGoCommandFailed:
		status = commandschema.StatusPrerequisiteMissing
	}
	return explainFailureClassification{
		status:    status,
		code:      actionable.code,
		message:   explainFailureMessage(status),
		locations: actionableDiagnosticSources(err, actionable.code),
	}
}

func explainFailureMessage(status commandschema.Status) string {
	switch status {
	case commandschema.StatusDecisionRequired:
		return "The selected Project requires an explicit decision before this explanation can complete."
	case commandschema.StatusPrerequisiteMissing:
		return "A required application-resolution prerequisite is unavailable."
	default:
		return "The selected Project is invalid for this explanation."
	}
}

func explainSuccessRecovery(result diagnosticschema.ExplainResult, evidence resolutionevidence.Evidence) (commandschema.Recovery, error) {
	selection, currentModule, err := explanationProjectContext(evidence)
	if err != nil {
		return commandschema.Recovery{}, err
	}
	selector := explainRecoverySelectorFromSelection(selection)
	verification := commandschema.Verification{
		WorkingDirectory: ".",
		Argv:             explainVerificationArgv(result.SubjectKind(), result.Subject(), explainSelectorArgv(selection)),
	}
	change := result.Change()
	input := commandschema.RecoveryInput{
		ID:            "change-explained-decision",
		Target:        commandschema.RecoveryTarget{Kind: string(result.SubjectKind()), ID: result.Subject()},
		Provenance:    explainRecoverySources(result.PrimarySources()),
		Selector:      selector,
		Preconditions: []commandschema.RecoveryFact{{Kind: "selected_snapshot_current"}},
		Effects:       []commandschema.RecoveryFact{{Kind: "decision_changed", Value: result.Subject()}},
		Verification:  verification,
	}
	switch change.Kind {
	case diagnosticschema.ExplainChangeCommand:
		input.Kind = commandschema.RecoveryExecute
		input.Owner = &commandschema.Owner{Module: currentModule, Path: "."}
		input.WorkingDirectory = "."
		input.Argv = change.Argv
	case diagnosticschema.ExplainChangeFile:
		input.Kind = commandschema.RecoveryEditSource
		input.Target = commandschema.RecoveryTarget{Kind: "configuration_field", ID: change.Field}
		input.Owner = &commandschema.Owner{Module: change.Module, Path: change.Path}
	default:
		return commandschema.Recovery{}, fmt.Errorf("unsupported explanation change kind %q", change.Kind)
	}
	return commandschema.NewRecovery(input)
}

func explainFailureRecovery(input explainFailureInput, classification explainFailureClassification) (commandschema.Recovery, error) {
	verification, selector, selectorArguments, selectorValid := explainFailureVerification(input.arguments, input.context)
	base := commandschema.RecoveryInput{
		ID:            "resolve-explain-failure",
		Kind:          commandschema.RecoveryManual,
		Target:        commandschema.RecoveryTarget{Kind: "operation", ID: input.operation},
		Provenance:    explainRecoverySources(classification.locations),
		Selector:      selector,
		Preconditions: []commandschema.RecoveryFact{{Kind: "failure_resolved"}},
		Verification:  verification,
	}
	switch classification.code {
	case diagnosticcode.ExplainInvocationInvalid:
		base.ID = "correct-explain-invocation"
		base.Target = commandschema.RecoveryTarget{Kind: "command", ID: input.operation}
		base.Preconditions = []commandschema.RecoveryFact{{Kind: "valid_invocation"}}
	case diagnosticcode.ExplainSubjectInvalid:
		base.ID = "correct-explain-subject"
		base.Target = commandschema.RecoveryTarget{Kind: "subject", ID: input.operation}
		base.Preconditions = []commandschema.RecoveryFact{{Kind: "canonical_subject"}}
	case diagnosticcode.ExplainTargetNotFound:
		base.ID = "select-visible-explain-target"
		base.Target = explainFailureTarget(input.arguments, input.operation)
		base.Preconditions = []commandschema.RecoveryFact{{Kind: "target_visible"}}
	case diagnosticConfigurationSelectionInvalid:
		base.ID = "select-explain-configuration"
		base.Target = commandschema.RecoveryTarget{Kind: "configuration_selector", ID: input.operation}
		base.Preconditions = []commandschema.RecoveryFact{{Kind: "exactly_one_selector"}}
	case diagnosticProviderAmbiguous:
		if selectorValid {
			var ambiguous *providerresolution.AmbiguousProviderError
			if errors.As(input.err, &ambiguous) && ambiguous != nil {
				options := make([]commandschema.RecoveryOption, 0, len(ambiguous.Providers()))
				for _, provider := range ambiguous.Providers() {
					options = append(options, commandschema.RecoveryOption{Value: provider.PluginID()})
				}
				base.ID = "choose-capability-provider"
				base.Kind = commandschema.RecoveryChoose
				base.Target = commandschema.RecoveryTarget{Kind: "configuration_field", ID: fmt.Sprintf("capabilities.use[%q]", ambiguous.Capability().String())}
				base.Preconditions = []commandschema.RecoveryFact{{Kind: "compatible_provider_selected"}}
				base.Effects = []commandschema.RecoveryFact{{Kind: "provider_selection_changed"}}
				base.Options = options
			}
		}
	case diagnosticResolveMultipleImplementations:
		if selectorValid {
			var ambiguous *interfaceresolution.AmbiguousImplementationError
			if errors.As(input.err, &ambiguous) && ambiguous != nil {
				options := make([]commandschema.RecoveryOption, 0, len(ambiguous.Candidates()))
				for _, candidate := range ambiguous.Candidates() {
					value := candidate.Constructor().String()
					argv := append([]string{"plystra", "use", ambiguous.InterfaceID().String(), value}, selectorArguments...)
					options = append(options, commandschema.RecoveryOption{Value: value, WorkingDirectory: ".", Argv: argv})
				}
				base.ID = "choose-interface-implementation"
				base.Kind = commandschema.RecoveryChoose
				base.Target = commandschema.RecoveryTarget{Kind: "interface_implementation", ID: ambiguous.InterfaceID().String()}
				base.Preconditions = []commandschema.RecoveryFact{{Kind: "compatible_implementation_selected"}}
				base.Effects = []commandschema.RecoveryFact{{Kind: "implementation_selection_changed"}}
				base.Options = options
			}
		}
	default:
		switch classification.status {
		case commandschema.StatusPrerequisiteMissing:
			base.ID = "satisfy-explain-prerequisite"
			base.Kind = commandschema.RecoverySatisfyPrerequisite
			base.Target = commandschema.RecoveryTarget{Kind: "prerequisite", ID: "application-resolution"}
			base.Preconditions = []commandschema.RecoveryFact{{Kind: "resolution_prerequisites_available"}}
		case commandschema.StatusValidationFailed:
			if first, editable := exactExplainEditableSource(classification.code, classification.locations); editable {
				base.ID = "correct-explain-project-source"
				base.Kind = commandschema.RecoveryEditSource
				base.Target = commandschema.RecoveryTarget{Kind: "source", ID: first.Path}
				base.Owner = &commandschema.Owner{Module: first.Module, Path: first.Path}
				base.Preconditions = []commandschema.RecoveryFact{{Kind: "source_valid"}}
				base.Effects = []commandschema.RecoveryFact{{Kind: "application_resolves"}}
			} else {
				base.ID = "correct-explain-project"
				base.Target = commandschema.RecoveryTarget{Kind: "project", ID: "selected"}
				base.Preconditions = []commandschema.RecoveryFact{{Kind: "project_valid"}}
			}
		}
	}
	return commandschema.NewRecovery(base)
}

func exactExplainEditableSource(code string, locations []diagnosticjson.Source) (diagnosticjson.Source, bool) {
	if len(locations) != 1 || locations[0].Module == "" || locations[0].Path == "" {
		return diagnosticjson.Source{}, false
	}
	switch code {
	case diagnosticProjectManifestInvalid,
		diagnosticEnvironmentOverlayInvalid,
		diagnosticConfigurationInvalid,
		diagnosticApplicationDependencyDrift,
		diagnosticGoModuleInvalid,
		diagnosticProviderSelectionInvalid,
		diagnosticResolveUnknownImplementation,
		diagnosticResolveIncompatibleImplementation,
		diagnosticResolveIntrinsicInterfaceSelection,
		diagnosticImplementationDeclarationInvalid,
		diagnosticImplementationConfigInvalid,
		diagnosticImplementationRequiredInvalid,
		diagnosticImplementationOptionalInvalid,
		diagnosticImplementationResultInvalid,
		diagnosticImplementationConformanceInvalid,
		diagnosticInterfaceDeclarationInvalid,
		diagnosticInterfaceContractInvalid,
		diagnosticInterfaceMetadataInvalid,
		diagnosticAuthoredPackageInvalid,
		diagnosticProtobufIdentityCollision,
		diagnosticProtobufOperationKindUnsupported,
		diagnosticProtobufPointerProjectionUnsupported,
		diagnosticCapabilityManifestInvalid,
		diagnosticConstructorConfigurationSchemaInvalid,
		diagnosticConstructorConfigurationValuesInvalid:
		return locations[0], true
	default:
		return diagnosticjson.Source{}, false
	}
}

func explainFailureTarget(arguments *explainArguments, operation string) commandschema.RecoveryTarget {
	if arguments == nil {
		return commandschema.RecoveryTarget{Kind: "operation", ID: operation}
	}
	return commandschema.RecoveryTarget{Kind: string(arguments.subjectKind), ID: arguments.subject}
}

func explainFailureVerification(arguments *explainArguments, recovery recoveryContext) (commandschema.Verification, *commandschema.RecoverySelector, []string, bool) {
	selector, selectorArguments, valid := explainRecoverySelectorFromContext(recovery)
	if arguments == nil || diagnosticschema.ValidateExplainSubject(arguments.subjectKind, arguments.subject) != nil || !valid {
		return commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "explain", "--help"}}, nil, nil, false
	}
	return commandschema.Verification{
		WorkingDirectory: ".",
		Argv:             explainVerificationArgv(arguments.subjectKind, arguments.subject, selectorArguments),
	}, selector, selectorArguments, true
}

func explainVerificationArgv(kind diagnosticschema.ExplainSubjectKind, subject string, selectorArguments []string) []string {
	argv := []string{"plystra", "explain", explainSubjectArgument(kind), subject, "--format", "json"}
	return append(argv, selectorArguments...)
}

func explainSubjectArgument(kind diagnosticschema.ExplainSubjectKind) string {
	if kind == diagnosticschema.ExplainSubjectConfiguration {
		return "config"
	}
	return string(kind)
}

func explainSelectorSnapshot(evidence resolutionevidence.Evidence) (commandschema.SelectorSnapshot, error) {
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return commandschema.SelectorSnapshot{}, errors.New("resolved explanation evidence omits the selected configuration")
	}
	input := commandschema.SelectorSnapshotInput{Mode: selection.Mode()}
	switch selection.Mode() {
	case generation.ConfigurationModeEnvironment:
		input.Name = selection.Environment()
	case generation.ConfigurationModeExplicit:
		input.Path = selection.SelectedPath()
	}
	return commandschema.NewSelectorSnapshot(input)
}

func explainRecoverySelectorFromSelection(selection resolutionevidence.ConfigurationSelection) *commandschema.RecoverySelector {
	selector := &commandschema.RecoverySelector{Mode: string(selection.Mode())}
	switch selection.Mode() {
	case generation.ConfigurationModeEnvironment:
		selector.Name = selection.Environment()
	case generation.ConfigurationModeExplicit:
		selector.Path = selection.SelectedPath()
	}
	return selector
}

func explainRecoverySelectorFromContext(recovery recoveryContext) (*commandschema.RecoverySelector, []string, bool) {
	mode, value, valid := recovery.selector()
	if !valid {
		return nil, nil, false
	}
	switch mode {
	case "default":
		return &commandschema.RecoverySelector{Mode: string(generation.ConfigurationModeDefault)}, nil, true
	case "environment":
		if !safeEnvironmentHint(value) {
			return nil, nil, false
		}
		return &commandschema.RecoverySelector{Mode: string(generation.ConfigurationModeEnvironment), Name: value}, []string{"--env", value}, true
	case "config":
		path, safe := safeConfigurationHint(value)
		if !safe {
			return nil, nil, false
		}
		return &commandschema.RecoverySelector{Mode: string(generation.ConfigurationModeExplicit), Path: path}, []string{"--config", path}, true
	default:
		return nil, nil, false
	}
}

func explainRecoverySources(values []diagnosticjson.Source) []commandschema.RecoverySource {
	result := make([]commandschema.RecoverySource, len(values))
	for index, value := range values {
		result[index] = commandschema.RecoverySource{
			Module: value.Module,
			Path:   value.Path,
			Kind:   value.Kind,
			Line:   value.Line,
			Column: value.Column,
		}
	}
	return result
}

func emitExplainFailure(output commandOutput, encoder explainResultEncoder, input explainFailureInput, human func() error) int {
	result, err := encoder.failure(input)
	if err != nil {
		return emitExplainInternalFailure(output, encoder, input.operation, input.snapshot)
	}
	return writeExplainResult(output, result, human)
}

func emitExplainInternalFailure(output commandOutput, encoder explainResultEncoder, operation string, snapshot *commandschema.SelectorSnapshot) int {
	result := encoder.internalFailure(operation, snapshot)
	return writeExplainResult(output, result, func() error {
		return writeHumanExplainInternalFailure(output.diagnosticWriter())
	})
}

func (e explainResultEncoder) internalFailure(operation string, snapshot *commandschema.SelectorSnapshot) commandschema.Result {
	if result, err := newExplainInternalFailureResult(e.invocationID, e.emptyEffects, operation, snapshot); err == nil {
		return result
	}
	if result, exists := e.internalFailures[operation]; exists {
		return result
	}
	return e.internalFailures["explain"]
}

func newExplainInternalFailureResult(invocationID string, effects commandschema.Effects, operation string, snapshot *commandschema.SelectorSnapshot) (commandschema.Result, error) {
	diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{
		Code:     diagnosticcode.ExplainFailed,
		Severity: diagnosticjson.SeverityError,
		Message:  "Plystra could not complete the requested explanation.",
	})
	if err != nil {
		return commandschema.Result{}, err
	}
	recovery, err := commandschema.NewRecovery(commandschema.RecoveryInput{
		ID:            "resolve-explain-internal-failure",
		Kind:          commandschema.RecoveryManual,
		Target:        commandschema.RecoveryTarget{Kind: "operation", ID: operation},
		Preconditions: []commandschema.RecoveryFact{{Kind: "failure_resolved"}},
		Verification:  commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "explain", "--help"}},
	})
	if err != nil {
		return commandschema.Result{}, err
	}
	return commandschema.NewResult(commandschema.ResultInput{
		Operation:    operation,
		InvocationID: invocationID,
		Snapshot:     snapshot,
		Status:       commandschema.StatusExecutionFailed,
		Diagnostics:  []commandschema.Diagnostic{diagnostic},
		Recovery:     []commandschema.Recovery{recovery},
		Effects:      effects,
	})
}

func writeExplainResult(output commandOutput, result commandschema.Result, human func() error) int {
	if output.format == commandFormatJSON {
		canonical := append(result.CanonicalJSON(), '\n')
		if _, err := output.resultWriter().Write(canonical); err != nil {
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

func writeHumanExplainInvalidInvocation(writer io.Writer) error {
	if _, err := io.WriteString(writer, explainUsage); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "\nRecovery:\nReview `plystra explain --help`, then rerun one complete explanation command.\n\nDiagnostic: %s\n", diagnosticcode.ExplainInvocationInvalid)
	return err
}

func writeHumanExplainFailure(writer io.Writer, prefix string, err error, recovery recoveryContext) error {
	if diagnostic, found := primaryActionableDiagnostic(err, recovery); found && diagnostic.code != diagnosticProviderAmbiguous {
		writeCommandFailure(writer, prefix, err, recovery)
		return nil
	}
	classification := classifyExplainFailure(err, recovery)
	message := classification.message
	if prefix == "" {
		if _, writeErr := fmt.Fprintln(writer, message); writeErr != nil {
			return writeErr
		}
	} else if _, writeErr := fmt.Fprintf(writer, "%s: %s\n", prefix, message); writeErr != nil {
		return writeErr
	}
	for _, source := range classification.locations {
		if _, writeErr := fmt.Fprintf(writer, "\nSource: %s\n", explainSourceSummary(source)); writeErr != nil {
			return writeErr
		}
	}
	recoveryText := "Resolve the reported explanation failure, then rerun the command."
	switch classification.code {
	case diagnosticcode.ExplainSubjectInvalid:
		recoveryText = "Review `plystra explain --help`, then use one canonical exact explanation subject."
	case diagnosticcode.ExplainTargetNotFound:
		recoveryText = "Select one exact target present in the selected application model, then rerun the command."
	case diagnosticProviderAmbiguous:
		var ambiguous *providerresolution.AmbiguousProviderError
		if errors.As(err, &ambiguous) && ambiguous != nil {
			values := make([]string, 0, len(ambiguous.Providers()))
			for _, provider := range ambiguous.Providers() {
				values = append(values, provider.PluginID())
			}
			recoveryText = fmt.Sprintf("Set capabilities.use[%q] in the selected Project configuration to one of: %s. Then rerun the same explanation.", ambiguous.Capability().String(), strings.Join(values, ", "))
		}
	}
	_, writeErr := fmt.Fprintf(writer, "\nRecovery:\n%s\n\nDiagnostic: %s\n", recoveryText, classification.code)
	return writeErr
}

func writeHumanExplainInternalFailure(writer io.Writer) error {
	_, err := fmt.Fprintf(writer, "Plystra could not complete the requested explanation.\n\nRecovery:\nResolve the internal failure, then rerun the command.\n\nDiagnostic: %s\n", diagnosticcode.ExplainFailed)
	return err
}
