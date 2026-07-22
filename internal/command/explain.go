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
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/resolutionevidence"
)

type explainArguments struct {
	subjectKind       diagnosticschema.ExplainSubjectKind
	subject           string
	format            commandFormat
	verbose           bool
	configurationPath string
	environmentName   string
}

type capabilityExplanation struct {
	result    diagnosticschema.ExplainResult
	required  bool
	intrinsic bool
	provider  string
}

func runExplain(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	parsed, ok := parseExplainArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, explainCapabilityUsage)
		return 2
	}
	output, err := newCommandOutput(parsed.format, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configure explain output: %v\n", err)
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
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "explain selected application: %v\n", err)
		return 1
	}

	var explanation capabilityExplanation
	switch parsed.subjectKind {
	case diagnosticschema.ExplainSubjectCapability:
		explanation, err = explainCapability(resolved, parsed.subject)
	default:
		err = fmt.Errorf("explanation subject kind %q is not implemented", parsed.subjectKind)
	}
	if err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "explain %s %s: %v\n", parsed.subjectKind, parsed.subject, err)
		return 1
	}
	if parsed.format == commandFormatJSON {
		_, _ = output.resultWriter().Write(explanation.result.Envelope().CanonicalJSON())
		_, _ = io.WriteString(output.resultWriter(), "\n")
		return 0
	}
	if err := writeHumanCapabilityExplanation(output.resultWriter(), explanation, parsed.verbose); err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "render capability explanation: %v\n", err)
		return 1
	}
	return 0
}

func parseExplainArguments(arguments []string) (explainArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "explain" || arguments[1] != string(diagnosticschema.ExplainSubjectCapability) || strings.TrimSpace(arguments[2]) == "" || strings.HasPrefix(arguments[2], "--") {
		return explainArguments{}, false
	}
	result := explainArguments{
		subjectKind: diagnosticschema.ExplainSubjectCapability,
		subject:     arguments[2],
		format:      commandFormatHuman,
	}
	formatSet := false
	configurationSet := false
	environmentSet := false
	for index := 3; index < len(arguments); index++ {
		switch arguments[index] {
		case "--verbose":
			if result.verbose {
				return explainArguments{}, false
			}
			result.verbose = true
		case "--format":
			if formatSet || index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return explainArguments{}, false
			}
			formatSet = true
			index++
			switch commandFormat(arguments[index]) {
			case commandFormatHuman, commandFormatJSON:
				result.format = commandFormat(arguments[index])
			default:
				return explainArguments{}, false
			}
		case "--config":
			if configurationSet || environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return explainArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return explainArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return explainArguments{}, false
		}
	}
	return result, true
}

func explainCapability(resolved applicationresolve.Result, subject string) (capabilityExplanation, error) {
	capabilityID, err := generation.ParseCapabilityID(subject)
	if err != nil {
		return capabilityExplanation{}, err
	}
	context := resolved.Resolution().Context()
	capability, visible := context.Capability(capabilityID)
	if !visible {
		return capabilityExplanation{}, fmt.Errorf("capability %q is not visible in the selected application model", subject)
	}

	evidence := resolved.ResolutionEvidence()
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return capabilityExplanation{}, fmt.Errorf("selected application evidence omits configuration provenance")
	}
	currentModule := ""
	for _, module := range evidence.Modules() {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			currentModule = module.Path()
			break
		}
	}
	if currentModule == "" {
		return capabilityExplanation{}, fmt.Errorf("selected application evidence omits the current Project")
	}

	required := false
	for _, requirement := range evidence.Requirements() {
		if requirement.Capability() == subject {
			required = true
			break
		}
	}
	input := diagnosticschema.ExplainInput{
		Evidence:    evidence,
		SubjectKind: diagnosticschema.ExplainSubjectCapability,
		Subject:     subject,
	}
	explanation := capabilityExplanation{required: required, intrinsic: capability.Intrinsic()}
	if !required {
		input.Outcome = "available"
		input.Reason = string(resolutionevidence.ProviderRejectionCapabilityNotRequired)
		input.PrimarySources = []diagnosticjson.Source{{
			Module: currentModule,
			Path:   selection.SelectedPath(),
			Kind:   "configuration-selection",
		}}
		input.Change = diagnosticschema.ExplainChange{
			Kind:   diagnosticschema.ExplainChangeFile,
			Module: currentModule,
			Path:   selection.SelectedPath(),
			Field:  fmt.Sprintf("capabilities.require[%q]", subject),
		}
	} else {
		provider, found := selectedCapabilityProvider(evidence, subject)
		if !found {
			return capabilityExplanation{}, fmt.Errorf("selected application evidence omits the required Capability Provider decision")
		}
		explanation.intrinsic = provider.Intrinsic()
		explanation.provider = provider.PluginID()
		input.Outcome = "required"
		input.Reason = string(provider.SelectionReason())
		selectionSources := provider.SelectionSources()
		if len(selectionSources) == 0 {
			input.PrimarySources = []diagnosticjson.Source{explainSource(provider.ProviderSource())}
		} else {
			input.PrimarySources = make([]diagnosticjson.Source, len(selectionSources))
			for index, source := range selectionSources {
				input.PrimarySources[index] = explainSource(source.Source())
			}
		}
		input.Change = requiredCapabilityChange(evidence, selection, currentModule, subject, provider)
	}

	result, err := diagnosticschema.NewExplain(input)
	if err != nil {
		return capabilityExplanation{}, err
	}
	explanation.result = result
	return explanation, nil
}

func selectedCapabilityProvider(evidence resolutionevidence.Evidence, capability string) (resolutionevidence.SelectedProvider, bool) {
	for _, provider := range evidence.SelectedProviders() {
		if provider.Capability() == capability {
			return provider, true
		}
	}
	return resolutionevidence.SelectedProvider{}, false
}

func requiredCapabilityChange(evidence resolutionevidence.Evidence, selection resolutionevidence.ConfigurationSelection, currentModule, capability string, selected resolutionevidence.SelectedProvider) diagnosticschema.ExplainChange {
	if !selected.Intrinsic() {
		for _, candidate := range evidence.ProviderCandidates() {
			if candidate.Capability() != capability || candidate.PluginID() == selected.PluginID() {
				continue
			}
			return diagnosticschema.ExplainChange{
				Kind:    diagnosticschema.ExplainChangeCommand,
				Command: "plystra use " + capability + " " + candidate.PluginID() + explainSelectorSuffix(selection),
			}
		}
		return diagnosticschema.ExplainChange{
			Kind:   diagnosticschema.ExplainChangeFile,
			Module: currentModule,
			Path:   selection.SelectedPath(),
			Field:  fmt.Sprintf("capabilities.use[%q]", capability),
		}
	}
	return diagnosticschema.ExplainChange{
		Kind:   diagnosticschema.ExplainChangeFile,
		Module: currentModule,
		Path:   selection.SelectedPath(),
		Field:  fmt.Sprintf("capabilities.require[%q]", capability),
	}
}

func explainSelectorSuffix(selection resolutionevidence.ConfigurationSelection) string {
	switch selection.Mode() {
	case generation.ConfigurationModeEnvironment:
		return " --env " + strconv.Quote(selection.Environment())
	case generation.ConfigurationModeExplicit:
		return " --config " + strconv.Quote(selection.SelectedPath())
	default:
		return ""
	}
}

func explainSource(source resolutionevidence.Source) diagnosticjson.Source {
	return diagnosticjson.Source{
		Module: source.Module(),
		Path:   source.Path(),
		Kind:   source.Kind(),
		Line:   source.Line(),
		Column: source.Column(),
	}
}

func writeHumanCapabilityExplanation(writer io.Writer, explanation capabilityExplanation, verbose bool) error {
	result := explanation.result
	var content strings.Builder
	fmt.Fprintf(&content, "Capability: %s\n", result.Subject())
	switch {
	case !explanation.required:
		content.WriteString("Decision: available but not required\n")
	case explanation.intrinsic:
		content.WriteString("Decision: required; Kernel intrinsic is selected\n")
	default:
		fmt.Fprintf(&content, "Decision: required; Provider %s is selected\n", explanation.provider)
	}
	fmt.Fprintf(&content, "Reason: %s\n", result.Reason())
	for _, source := range result.PrimarySources() {
		fmt.Fprintf(&content, "Source: %s\n", explainSourceSummary(source))
	}
	change := result.Change()
	switch change.Kind {
	case diagnosticschema.ExplainChangeCommand:
		fmt.Fprintf(&content, "Change: %s\n", change.Command)
	case diagnosticschema.ExplainChangeFile:
		fmt.Fprintf(&content, "Change: edit %s at %s\n", change.Path, change.Field)
	}
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

func explainSourceSummary(source diagnosticjson.Source) string {
	location := source.Module + ":" + source.Path
	if source.Line > 0 {
		location += fmt.Sprintf(":%d:%d", source.Line, source.Column)
	}
	return location + " (" + source.Kind + ")"
}
