package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

var (
	errDoctorInvocation = errors.New("invalid plystra doctor invocation")
	errDoctorFailed     = errors.New("run plystra doctor")
)

type doctorArguments struct {
	offline           bool
	format            commandFormat
	configurationPath string
	environmentName   string
}

type doctorResultEncoder struct {
	invocationID string
	emptyEffects commandschema.Effects
}

func runDoctor(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	format := doctorFormatIntent(arguments)
	output, err := newCommandOutput(format, stdout, stderr)
	if err != nil {
		return 2
	}
	encoder, initialized := initializeDoctorResultEncoder(output.diagnosticWriter())
	if !initialized {
		return 8
	}
	parsed, valid := parseDoctorArguments(arguments)
	if !valid {
		result, buildErr := encoder.failure(errDoctorInvocation, commandschema.DoctorPayload{})
		if buildErr != nil {
			return 8
		}
		return writeDoctorResult(output, result, func() error { _, writeErr := io.WriteString(output.diagnosticWriter(), doctorUsage); return writeErr })
	}
	if parsed.configurationPath != "" && parsed.environmentName != "" {
		err = fmt.Errorf("%w: --config and --env cannot be used together", applicationresolve.ErrConfigurationSelection)
		result, buildErr := encoder.failure(err, commandschema.DoctorPayload{})
		if buildErr != nil {
			return 8
		}
		return writeDoctorResult(output, result, func() error {
			writeCommandFailure(output.diagnosticWriter(), "", err, commandRecoveryContext(parsed.configurationPath, parsed.environmentName, environment))
			return nil
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	payload, doctorErr := executeDoctor(ctx, parsed, workingDirectory, environment)
	if doctorErr != nil {
		result, buildErr := encoder.failure(doctorErr, payload)
		if buildErr != nil {
			return 8
		}
		return writeDoctorResult(output, result, func() error {
			writeCommandFailure(output.diagnosticWriter(), "", doctorErr, commandRecoveryContext(parsed.configurationPath, parsed.environmentName, environment))
			return nil
		})
	}
	status := commandschema.StatusSuccess
	for _, check := range payload.Checks() {
		if check.Status() == commandschema.SupportNo {
			status = commandschema.StatusPrerequisiteMissing
			break
		}
	}
	result, buildErr := encoder.success(payload, status)
	if buildErr != nil {
		return 8
	}
	return writeDoctorResult(output, result, func() error { return writeHumanDoctor(output.resultWriter(), payload) })
}

func parseDoctorArguments(arguments []string) (doctorArguments, bool) {
	if len(arguments) == 0 || arguments[0] != "doctor" {
		return doctorArguments{}, false
	}
	result := doctorArguments{format: commandFormatHuman}
	formatSet, configurationSet, environmentSet := false, false, false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--offline":
			if result.offline {
				return doctorArguments{}, false
			}
			result.offline = true
		case "--format":
			if formatSet || index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return doctorArguments{}, false
			}
			formatSet = true
			index++
			if arguments[index] != string(commandFormatHuman) && arguments[index] != string(commandFormatJSON) {
				return doctorArguments{}, false
			}
			result.format = commandFormat(arguments[index])
		case "--config":
			if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return doctorArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return doctorArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return doctorArguments{}, false
		}
	}
	return result, true
}

func doctorFormatIntent(arguments []string) commandFormat {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--format" && arguments[index+1] == string(commandFormatJSON) {
			return commandFormatJSON
		}
	}
	return commandFormatHuman
}

func executeDoctor(ctx context.Context, arguments doctorArguments, workingDirectory string, environment []string) (commandschema.DoctorPayload, error) {
	inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		Start: workingDirectory, ConfigurationPath: arguments.configurationPath, EnvironmentName: arguments.environmentName,
		Environment: environment, Offline: arguments.offline,
	})
	if err != nil {
		return commandschema.DoctorPayload{}, fmt.Errorf("%w: discover selected Project: %v", errDoctorFailed, err)
	}
	selection := inputs.ConfigurationSelection()
	payloadInput := commandschema.DoctorPayloadInput{
		ModulePath: inputs.Module().ModulePath(), ConfigurationPath: selection.Path(), Environment: selection.Environment(), Offline: arguments.offline,
		Checks: []commandschema.DoctorCheckInput{
			{ID: "go-toolchain", Status: commandschema.SupportYes, Reason: "The selected Project was discovered with the configured Go toolchain.", Verification: []string{"plystra", "doctor"}},
			{ID: "module-resolution", Status: commandschema.SupportYes, Reason: "The selected Project module graph was resolved without mutation.", Verification: []string{"plystra", "doctor"}},
		},
	}
	composition, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
	if err != nil {
		return commandschema.DoctorPayload{}, fmt.Errorf("%w: compose selected configuration: %v", errDoctorFailed, err)
	}
	if len(composition.Manifest().DataMembers()) == 0 {
		payloadInput.Checks = append(payloadInput.Checks,
			commandschema.DoctorCheckInput{ID: "data-compiler-selection", Status: commandschema.SupportNotApplicable, Reason: "The selected Project has no active Data member.", Verification: []string{"plystra", "doctor"}},
			commandschema.DoctorCheckInput{ID: "data-compiler-cache", Status: commandschema.SupportNotApplicable, Reason: "No selected Data compiler requires a cache check.", Verification: []string{"plystra", "doctor"}},
		)
		return commandschema.NewDoctorPayload(payloadInput)
	}

	compilerSelection, err := inputs.DataCompilerSelection()
	if err != nil {
		payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{
			ID: "data-compiler-selection", Status: commandschema.SupportNo,
			Reason:       "The selected Project does not have an exact verified Data compiler distribution.",
			Verification: []string{"plystra", "generate", "--check"},
		})
		return commandschema.NewDoctorPayload(payloadInput)
	}
	payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{
		ID: "data-compiler-selection", Status: commandschema.SupportYes,
		Reason:       "The selected Project compiler module, checksum, and distribution manifest are verified.",
		Verification: []string{"plystra", "doctor"},
	})
	compiler := commandschema.DoctorDataCompilerInput{
		ModulePath: compilerSelection.ModulePath, ModuleVersion: compilerSelection.ModuleVersion, ModuleChecksum: compilerSelection.ModuleChecksum,
		DistributionSchema: compilerSelection.Manifest.Schema, ManifestDigest: compilerSelection.ManifestDigest,
		CommandImportPath: compilerSelection.Manifest.CommandImportPath, AnalyzeProtocol: compilerSelection.Manifest.AnalyzeProtocol,
		EmitProtocol: compilerSelection.Manifest.EmitProtocol, DeclarationLanguage: compilerSelection.Manifest.DeclarationLanguage,
		GoToolchain: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Source: commandschema.SupportYes, Build: commandschema.SupportYes,
	}
	payloadInput.DataCompiler = &compiler
	cacheArtifact, cacheErr := inputs.AcquireDataCompiler(ctx, applicationresolve.Options{Environment: environment, Offline: true})
	if cacheErr == nil {
		compiler.Cache, compiler.Offline, compiler.BinaryDigest, compiler.GoToolchain = commandschema.SupportYes, commandschema.SupportYes, cacheArtifact.BinaryDigest, cacheArtifact.GoToolchain
		payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{ID: "data-compiler-cache", Status: commandschema.SupportYes, Reason: "A verified compiler executable is available in the private cache.", Verification: []string{"plystra", "generate", "--offline", "--check"}})
	} else if errors.Is(cacheErr, datacompiler.ErrOfflineUnavailable) {
		compiler.Cache, compiler.Offline = commandschema.SupportNo, commandschema.SupportNo
		payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{ID: "data-compiler-cache", Status: commandschema.SupportNo, Reason: "The exact selected compiler is not present in the verified private cache.", Verification: []string{"plystra", "generate", "--check"}})
	} else {
		compiler.Cache, compiler.Offline = commandschema.SupportUnknown, commandschema.SupportUnknown
		payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{ID: "data-compiler-cache", Status: commandschema.SupportUnknown, Reason: "The selected compiler cache could not be inspected.", Verification: []string{"plystra", "doctor"}})
	}
	payloadInput.Checks = append(payloadInput.Checks, commandschema.DoctorCheckInput{ID: "data-compiler-build", Status: commandschema.SupportYes, Reason: "The selected compiler source and Go toolchain are available for a future cache materialization.", Verification: []string{"plystra", "generate", "--check"}})
	return commandschema.NewDoctorPayload(payloadInput)
}

func initializeDoctorResultEncoder(stderr io.Writer) (doctorResultEncoder, bool) {
	invocationID, err := generateInvocationID()
	if err != nil {
		_, _ = io.WriteString(stderr, "initialize plystra doctor result: internal failure\n")
		return doctorResultEncoder{}, false
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		_, _ = io.WriteString(stderr, "initialize plystra doctor result: internal failure\n")
		return doctorResultEncoder{}, false
	}
	return doctorResultEncoder{invocationID: invocationID, emptyEffects: effects}, true
}

func (e doctorResultEncoder) success(payload commandschema.DoctorPayload, status commandschema.Status) (commandschema.Result, error) {
	snapshot := doctorSnapshot(payload)
	input := commandschema.ResultInput{Operation: "doctor", InvocationID: e.invocationID, Snapshot: snapshot, Status: status, Effects: e.emptyEffects, Payload: payload}
	if status == commandschema.StatusPrerequisiteMissing {
		diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{
			Code:     diagnosticcode.DoctorPrerequisiteMissing,
			Severity: diagnosticjson.SeverityError,
			Message:  "A local Project or Data compiler prerequisite is unavailable.",
		})
		if err != nil {
			return commandschema.Result{}, err
		}
		input.Diagnostics = []commandschema.Diagnostic{diagnostic}
	}
	return commandschema.NewResult(input)
}

func (e doctorResultEncoder) failure(err error, payload commandschema.DoctorPayload) (commandschema.Result, error) {
	status, code, message := commandschema.StatusExecutionFailed, diagnosticcode.DoctorFailed, "Plystra doctor could not complete."
	if errors.Is(err, errDoctorInvocation) {
		status, code, message = commandschema.StatusInvalidInvocation, diagnosticcode.DoctorInvocationInvalid, "The plystra doctor invocation is invalid."
	} else if errors.Is(err, applicationresolve.ErrConfigurationSelection) {
		status, code, message = commandschema.StatusValidationFailed, diagnosticcode.DoctorFailed, "The selected configuration cannot be inspected."
	} else {
		status, code, message = commandschema.StatusPrerequisiteMissing, diagnosticcode.DoctorPrerequisiteMissing, "A local Project or Data compiler prerequisite is unavailable."
	}
	diagnostic, diagnosticErr := commandschema.NewDiagnostic(commandschema.DiagnosticInput{Code: code, Severity: diagnosticjson.SeverityError, Message: message})
	if diagnosticErr != nil {
		return commandschema.Result{}, diagnosticErr
	}
	recovery, recoveryErr := commandschema.NewRecovery(commandschema.RecoveryInput{ID: "resolve-doctor-failure", Kind: commandschema.RecoveryManual, Target: commandschema.RecoveryTarget{Kind: "operation", ID: "doctor"}, Preconditions: []commandschema.RecoveryFact{{Kind: "prerequisite_available"}}, Verification: commandschema.Verification{WorkingDirectory: ".", Argv: []string{"plystra", "doctor", "--format", "json"}}})
	if recoveryErr != nil {
		return commandschema.Result{}, recoveryErr
	}
	input := commandschema.ResultInput{Operation: "doctor", InvocationID: e.invocationID, Status: status, Diagnostics: []commandschema.Diagnostic{diagnostic}, Recovery: []commandschema.Recovery{recovery}, Effects: e.emptyEffects}
	if payload.Valid() {
		input.Snapshot = doctorSnapshot(payload)
		input.Payload = payload
	}
	return commandschema.NewResult(input)
}

func doctorSnapshot(payload commandschema.DoctorPayload) *commandschema.SelectorSnapshot {
	if !payload.Valid() {
		return nil
	}
	input := commandschema.SelectorSnapshotInput{Mode: generation.ConfigurationModeDefault}
	if payload.Environment() != "" {
		input.Mode, input.Name = generation.ConfigurationModeEnvironment, payload.Environment()
	} else if payload.ConfigurationPath() != "plystra.yaml" {
		input.Mode, input.Path = generation.ConfigurationModeExplicit, payload.ConfigurationPath()
	}
	snapshot, err := commandschema.NewSelectorSnapshot(input)
	if err != nil {
		return nil
	}
	return &snapshot
}

func writeDoctorResult(output commandOutput, result commandschema.Result, human func() error) int {
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

func writeHumanDoctor(writer io.Writer, payload commandschema.DoctorPayload) error {
	if _, err := fmt.Fprintf(writer, "Plystra doctor\nProject: %s\nConfiguration: %s\nOffline: %t\n", payload.ModulePath(), payload.ConfigurationPath(), payload.Offline()); err != nil {
		return err
	}
	if compiler, ok := payload.DataCompiler(); ok {
		if _, err := fmt.Fprintf(writer, "Data compiler: %s@%s\nChecksum: %s\nManifest: %s\nSource/cache/build/offline: %s/%s/%s/%s\n", compiler.ModulePath(), compiler.ModuleVersion(), compiler.ModuleChecksum(), compiler.ManifestDigest(), compiler.Source(), compiler.Cache(), compiler.Build(), compiler.Offline()); err != nil {
			return err
		}
	}
	for _, check := range payload.Checks() {
		if _, err := fmt.Fprintf(writer, "  %s: %s (%s)\n", check.ID(), check.Status(), check.Reason()); err != nil {
			return err
		}
	}
	return nil
}
