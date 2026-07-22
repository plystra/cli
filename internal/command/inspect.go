package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/diagnosticschema"
)

type inspectArguments struct {
	format            commandFormat
	verbose           bool
	configurationPath string
	environmentName   string
}

func runInspect(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	parsed, ok := parseInspectArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, inspectUsage)
		return 2
	}
	output, err := newCommandOutput(parsed.format, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configure inspect output: %v\n", err)
		return 2
	}

	_, _ = io.WriteString(output.progressWriter(), "Resolving selected application model...\n")
	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	resolved, err := applicationresolve.Resolve(ctx, applicationresolve.Options{
		Start:             workingDirectory,
		ConfigurationPath: parsed.configurationPath,
		EnvironmentName:   parsed.environmentName,
		Environment:       environment,
	})
	if err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "inspect selected application: %v\n", err)
		return 1
	}

	result, err := diagnosticschema.NewInspect(diagnosticschema.InspectInput{
		Evidence:   resolved.ResolutionEvidence(),
		NextAction: inspectNextAction(resolved.ConfigurationSelection()),
	})
	if err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "inspect selected application: %v\n", err)
		return 1
	}
	if parsed.format == commandFormatJSON {
		_, _ = output.resultWriter().Write(result.Envelope().CanonicalJSON())
		_, _ = io.WriteString(output.resultWriter(), "\n")
		return 0
	}
	if err := writeHumanInspect(output.resultWriter(), result, parsed.verbose); err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "render inspect result: %v\n", err)
		return 1
	}
	return 0
}

func parseInspectArguments(arguments []string) (inspectArguments, bool) {
	if len(arguments) == 0 || arguments[0] != "inspect" {
		return inspectArguments{}, false
	}
	result := inspectArguments{format: commandFormatHuman}
	formatSet := false
	configurationSet := false
	environmentSet := false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--verbose":
			if result.verbose {
				return inspectArguments{}, false
			}
			result.verbose = true
		case "--format":
			if formatSet || index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return inspectArguments{}, false
			}
			formatSet = true
			index++
			switch commandFormat(arguments[index]) {
			case commandFormatHuman, commandFormatJSON:
				result.format = commandFormat(arguments[index])
			default:
				return inspectArguments{}, false
			}
		case "--config":
			if configurationSet || environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return inspectArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return inspectArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return inspectArguments{}, false
		}
	}
	return result, true
}

func inspectNextAction(selection applicationresolve.ConfigurationSelection) string {
	command := "plystra check"
	switch selection.Mode() {
	case string(generation.ConfigurationModeEnvironment):
		command += " --env " + strconv.Quote(selection.Environment())
	case string(generation.ConfigurationModeExplicit):
		command += " --config " + strconv.Quote(selection.Path())
	}
	return "Run " + command + " to validate the selected model."
}

func writeHumanInspect(writer io.Writer, result diagnosticschema.InspectResult, verbose bool) error {
	var content strings.Builder
	fmt.Fprintf(&content, "Project: %s\n", result.ProjectModule())
	fmt.Fprintf(&content, "Configuration: %s\n", inspectConfigurationSummary(result))
	fmt.Fprintf(&content, "Plugins: %d selected\n", result.SelectedPluginCount())
	fmt.Fprintf(
		&content,
		"Capabilities: %d available, %d required, %d exposed, %d aliases\n",
		result.AvailableCapabilityCount(),
		result.RequiredCapabilityCount(),
		result.ExposedCapabilityCount(),
		result.CapabilityAliasCount(),
	)
	fmt.Fprintf(&content, "AuthN: %s\n", inspectActivation(result.AuthNActive()))
	fmt.Fprintf(&content, "AuthZ: %s\n", inspectActivation(result.AuthZActive()))
	fmt.Fprintf(&content, "Transports: %s\n", inspectTransports(result.Transports()))
	fmt.Fprintf(&content, "Readiness: %s (%d problems)\n", result.Readiness(), result.ProblemCount())
	fmt.Fprintf(&content, "Next action: %s\n", result.NextAction())
	if verbose {
		var evidence bytes.Buffer
		if err := json.Indent(&evidence, result.ResolutionEvidenceJSON(), "", "  "); err != nil {
			return fmt.Errorf("format resolution evidence: %w", err)
		}
		content.WriteString("Resolution evidence:\n")
		for _, line := range strings.Split(evidence.String(), "\n") {
			content.WriteString("  ")
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	_, err := io.WriteString(writer, content.String())
	return err
}

func inspectConfigurationSummary(result diagnosticschema.InspectResult) string {
	switch result.ConfigurationMode() {
	case generation.ConfigurationModeEnvironment:
		return fmt.Sprintf("environment %q (%s)", result.ConfigurationEnvironment(), result.ConfigurationPath())
	default:
		return fmt.Sprintf("%s (%s)", result.ConfigurationMode(), result.ConfigurationPath())
	}
}

func inspectActivation(active bool) string {
	if active {
		return "active"
	}
	return "inactive"
}

func inspectTransports(transports []diagnosticschema.Transport) string {
	if len(transports) == 0 {
		return "none"
	}
	values := make([]string, len(transports))
	for index, transport := range transports {
		values[index] = string(transport)
	}
	return strings.Join(values, ", ")
}
