package command

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/installedcapabilities"
)

var (
	errInspectCapabilitiesInvocation = errors.New("invalid plystra inspect capabilities invocation")
	errInspectCapabilities           = errors.New("inspect installed Plystra capabilities")
)

type inspectCapabilitiesArguments struct {
	format commandFormat
}

type installedCapabilitiesProvider func() (commandschema.Capabilities, error)

type inspectCapabilitiesDependencies struct {
	current      installedCapabilitiesProvider
	invocationID invocationIDGenerator
}

type inspectCapabilitiesResultEncoder struct {
	invocationID string
	emptyEffects commandschema.Effects
}

func defaultInspectCapabilitiesDependencies() inspectCapabilitiesDependencies {
	return inspectCapabilitiesDependencies{
		current:      installedcapabilities.Current,
		invocationID: generateInvocationID,
	}
}

func isInspectCapabilitiesCommand(arguments []string) bool {
	return len(arguments) >= 2 && arguments[0] == "inspect" && arguments[1] == "capabilities"
}

func runInspectCapabilities(arguments []string, stdout, stderr io.Writer, dependencies inspectCapabilitiesDependencies) int {
	format := inspectCapabilitiesFormatIntent(arguments)
	output, err := newCommandOutput(format, stdout, stderr)
	if err != nil {
		return 2
	}
	encoder, initialized := initializeInspectCapabilitiesResultEncoder(output.diagnosticWriter(), dependencies.invocationID)
	if !initialized {
		return 8
	}
	if _, valid := parseInspectCapabilitiesArguments(arguments); !valid {
		result, buildErr := encoder.failure(errInspectCapabilitiesInvocation)
		if buildErr != nil {
			return 8
		}
		return writeInspectCapabilitiesResult(output, result, func() error {
			return writeInspectCapabilitiesInvalidInvocation(output.diagnosticWriter())
		})
	}
	if dependencies.current == nil {
		err = fmt.Errorf("%w: installed capability provider is unavailable", errInspectCapabilities)
	} else {
		var capabilities commandschema.Capabilities
		capabilities, err = dependencies.current()
		if err == nil {
			result, buildErr := encoder.success(capabilities)
			if buildErr != nil {
				return 8
			}
			return writeInspectCapabilitiesResult(output, result, func() error {
				renderErr := writeHumanInspectCapabilities(output.resultWriter(), capabilities)
				if renderErr != nil {
					_, _ = fmt.Fprintf(output.diagnosticWriter(), "render installed capabilities: %v\n", renderErr)
				}
				return renderErr
			})
		}
		err = fmt.Errorf("%w: %v", errInspectCapabilities, err)
	}
	result, buildErr := encoder.failure(err)
	if buildErr != nil {
		return 8
	}
	return writeInspectCapabilitiesResult(output, result, func() error {
		return writeInspectCapabilitiesFailure(output.diagnosticWriter())
	})
}

func parseInspectCapabilitiesArguments(arguments []string) (inspectCapabilitiesArguments, bool) {
	if !isInspectCapabilitiesCommand(arguments) {
		return inspectCapabilitiesArguments{}, false
	}
	result := inspectCapabilitiesArguments{format: commandFormatHuman}
	formatSet := false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--format":
			if formatSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return inspectCapabilitiesArguments{}, false
			}
			formatSet = true
			index++
			switch commandFormat(arguments[index]) {
			case commandFormatHuman, commandFormatJSON:
				result.format = commandFormat(arguments[index])
			default:
				return inspectCapabilitiesArguments{}, false
			}
		default:
			return inspectCapabilitiesArguments{}, false
		}
	}
	return result, true
}

func inspectCapabilitiesFormatIntent(arguments []string) commandFormat {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--format" && arguments[index+1] == string(commandFormatJSON) {
			return commandFormatJSON
		}
	}
	return commandFormatHuman
}

func initializeInspectCapabilitiesResultEncoder(stderr io.Writer, generateID invocationIDGenerator) (inspectCapabilitiesResultEncoder, bool) {
	if generateID == nil {
		writeInspectCapabilitiesInitializationFailure(stderr)
		return inspectCapabilitiesResultEncoder{}, false
	}
	invocationID, err := generateID()
	if err != nil {
		writeInspectCapabilitiesInitializationFailure(stderr)
		return inspectCapabilitiesResultEncoder{}, false
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		writeInspectCapabilitiesInitializationFailure(stderr)
		return inspectCapabilitiesResultEncoder{}, false
	}
	encoder := inspectCapabilitiesResultEncoder{invocationID: invocationID, emptyEffects: effects}
	if _, err := commandschema.NewResult(commandschema.ResultInput{
		Operation:    "inspect.capabilities",
		InvocationID: invocationID,
		Status:       commandschema.StatusSuccess,
		Effects:      effects,
	}); err != nil {
		writeInspectCapabilitiesInitializationFailure(stderr)
		return inspectCapabilitiesResultEncoder{}, false
	}
	return encoder, true
}

func writeInspectCapabilitiesInitializationFailure(stderr io.Writer) {
	_, _ = io.WriteString(stderr, "initialize plystra inspect capabilities result: internal failure\n")
}

func (e inspectCapabilitiesResultEncoder) success(capabilities commandschema.Capabilities) (commandschema.Result, error) {
	return commandschema.NewResult(commandschema.ResultInput{
		Operation:    "inspect.capabilities",
		InvocationID: e.invocationID,
		Status:       commandschema.StatusSuccess,
		Effects:      e.emptyEffects,
		Payload:      capabilities,
	})
}

func (e inspectCapabilitiesResultEncoder) failure(err error) (commandschema.Result, error) {
	status := commandschema.StatusExecutionFailed
	code := diagnosticcode.InspectCapabilitiesFailed
	message := "Installed Plystra capability inspection failed."
	recoveryTarget := commandschema.RecoveryTarget{Kind: "installation", ID: "plystra"}
	recoveryID := "repair-plystra-installation"
	precondition := "installed_distribution_consistent"
	if errors.Is(err, errInspectCapabilitiesInvocation) {
		status = commandschema.StatusInvalidInvocation
		code = diagnosticcode.InspectCapabilitiesInvocationInvalid
		message = "The plystra inspect capabilities invocation is invalid."
		recoveryTarget = commandschema.RecoveryTarget{Kind: "command", ID: "inspect.capabilities"}
		recoveryID = "correct-inspect-capabilities-invocation"
		precondition = "valid_invocation"
	}
	recovery, recoveryErr := commandschema.NewRecovery(commandschema.RecoveryInput{
		ID:            recoveryID,
		Kind:          commandschema.RecoveryManual,
		Target:        recoveryTarget,
		Preconditions: []commandschema.RecoveryFact{{Kind: precondition}},
		Verification: commandschema.Verification{
			WorkingDirectory: ".",
			Argv:             []string{"plystra", "inspect", "capabilities", "--format", "json"},
		},
	})
	if recoveryErr != nil {
		return commandschema.Result{}, recoveryErr
	}
	return commandschema.NewResult(commandschema.ResultInput{
		Operation:    "inspect.capabilities",
		InvocationID: e.invocationID,
		Status:       status,
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     code,
			Severity: diagnosticjson.SeverityError,
			Message:  message,
		}},
		Recovery: []commandschema.Recovery{recovery},
		Effects:  e.emptyEffects,
	})
}

func writeInspectCapabilitiesResult(output commandOutput, result commandschema.Result, human func() error) int {
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

func writeHumanInspectCapabilities(writer io.Writer, capabilities commandschema.Capabilities) error {
	toolchain := capabilities.TransportToolchain()
	if _, err := fmt.Fprintf(
		writer,
		"Installed Plystra capabilities\nCLI: %s\nKernel: %s\nSpecification: %s\nGo requirement: %s\nPlatform: %s/%s\nPublic schemas:\n",
		capabilities.CLIVersion(),
		capabilities.KernelVersion(),
		capabilities.SpecificationRevision(),
		capabilities.GoRequirement(),
		capabilities.GOOS(),
		capabilities.GOARCH(),
	); err != nil {
		return err
	}
	for _, schema := range capabilities.Schemas() {
		if schema.Available() {
			if _, err := fmt.Fprintf(writer, "  %s: %s/v%d\n", schema.Role(), schema.Name(), schema.Version()); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(writer, "  %s: unavailable\n", schema.Role()); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(
		writer,
		"Project document limit: %d bytes\nDefaults: startup %s, invocation %s\nTransport toolchain: %s (%d components)\nSupport stages:\n",
		capabilities.ProjectDocumentBytes(),
		capabilities.StartupTimeoutText(),
		capabilities.InvocationTimeoutText(),
		toolchain.Digest(),
		len(toolchain.Components()),
	); err != nil {
		return err
	}
	for _, support := range capabilities.Support() {
		if _, err := fmt.Fprintf(
			writer,
			"  %s: specified=%s parsed=%s generated=%s executed=%s accepted=%s\n",
			support.ID(),
			support.Specified(),
			support.Parsed(),
			support.Generated(),
			support.Executed(),
			support.Accepted(),
		); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "Transport component details are omitted from human output; use --format json for the complete installed payload.\n")
	return err
}

func writeInspectCapabilitiesInvalidInvocation(writer io.Writer) error {
	if _, err := io.WriteString(writer, inspectUsage); err != nil {
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"\nRecovery:\nReview `plystra inspect capabilities --help`, then rerun with only `--format human|json`.\n\nDiagnostic: %s\n",
		diagnosticcode.InspectCapabilitiesInvocationInvalid,
	)
	return err
}

func writeInspectCapabilitiesFailure(writer io.Writer) error {
	_, err := fmt.Fprintf(
		writer,
		"inspect installed Plystra capabilities: internal failure\n\nRecovery:\nInstall a Plystra CLI build with consistent release metadata, then rerun `plystra inspect capabilities --format json`.\n\nDiagnostic: %s\n",
		diagnosticcode.InspectCapabilitiesFailed,
	)
	return err
}
