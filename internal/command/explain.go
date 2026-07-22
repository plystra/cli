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
	"github.com/plystra/cli/internal/pluginid"
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

type commandExplanation struct {
	result       diagnosticschema.ExplainResult
	subjectLabel string
	decision     string
}

func runExplain(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	parsed, ok := parseExplainArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, explainUsage)
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

	var explanation commandExplanation
	switch parsed.subjectKind {
	case diagnosticschema.ExplainSubjectCapability:
		explanation, err = explainCapability(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectPlugin:
		explanation, err = explainPlugin(resolved, parsed.subject)
	case diagnosticschema.ExplainSubjectConfiguration:
		explanation, err = explainConfiguration(resolved, parsed.subject)
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
	if err := writeHumanExplanation(output.resultWriter(), explanation, parsed.verbose); err != nil {
		_, _ = fmt.Fprintf(output.diagnosticWriter(), "render %s explanation: %v\n", parsed.subjectKind, err)
		return 1
	}
	return 0
}

func parseExplainArguments(arguments []string) (explainArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "explain" || strings.TrimSpace(arguments[2]) == "" || strings.HasPrefix(arguments[2], "--") {
		return explainArguments{}, false
	}
	var subjectKind diagnosticschema.ExplainSubjectKind
	switch diagnosticschema.ExplainSubjectKind(arguments[1]) {
	case diagnosticschema.ExplainSubjectCapability, diagnosticschema.ExplainSubjectPlugin:
		subjectKind = diagnosticschema.ExplainSubjectKind(arguments[1])
	default:
		if arguments[1] != "config" {
			return explainArguments{}, false
		}
		subjectKind = diagnosticschema.ExplainSubjectConfiguration
	}
	result := explainArguments{
		subjectKind: subjectKind,
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

func explainCapability(resolved applicationresolve.Result, subject string) (commandExplanation, error) {
	capabilityID, err := generation.ParseCapabilityID(subject)
	if err != nil {
		return commandExplanation{}, err
	}
	context := resolved.Resolution().Context()
	_, visible := context.Capability(capabilityID)
	if !visible {
		return commandExplanation{}, fmt.Errorf("capability %q is not visible in the selected application model", subject)
	}

	evidence := resolved.ResolutionEvidence()
	selection, currentModule, err := explanationProjectContext(evidence)
	if err != nil {
		return commandExplanation{}, err
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
	explanation := commandExplanation{subjectLabel: "Capability"}
	if !required {
		explanation.decision = "available but not required"
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
			return commandExplanation{}, fmt.Errorf("selected application evidence omits the required Capability Provider decision")
		}
		if provider.Intrinsic() {
			explanation.decision = "required; Kernel intrinsic is selected"
		} else {
			explanation.decision = fmt.Sprintf("required; Provider %s is selected", provider.PluginID())
		}
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
		return commandExplanation{}, err
	}
	explanation.result = result
	return explanation, nil
}

func explainPlugin(resolved applicationresolve.Result, subject string) (commandExplanation, error) {
	if err := pluginid.Validate(subject); err != nil {
		return commandExplanation{}, err
	}
	evidence := resolved.ResolutionEvidence()
	selection, currentModule, err := explanationProjectContext(evidence)
	if err != nil {
		return commandExplanation{}, err
	}
	candidate, visible := visiblePlugin(evidence, subject)
	if !visible {
		return commandExplanation{}, fmt.Errorf("plugin %q is not visible in the selected application model", subject)
	}

	input := diagnosticschema.ExplainInput{
		Evidence:    evidence,
		SubjectKind: diagnosticschema.ExplainSubjectPlugin,
		Subject:     subject,
	}
	explanation := commandExplanation{subjectLabel: "Plugin"}
	selected, isSelected := selectedPlugin(evidence, subject)
	if isSelected {
		providerCapabilities := make([]string, 0, len(selected.Reasons()))
		for _, reason := range selected.Reasons() {
			if reason.Kind() == resolutionevidence.PluginSelectionCurrentProject {
				explanation.decision = "selected from the current Project"
				input.Outcome = "selected"
				input.Reason = string(reason.Kind())
				input.PrimarySources = []diagnosticjson.Source{explainSource(selected.Source())}
				input.Change = diagnosticschema.ExplainChange{
					Kind:   diagnosticschema.ExplainChangeFile,
					Module: currentModule,
					Path:   selected.Source().Path(),
					Field:  "id",
				}
				break
			}
			if reason.Kind() != resolutionevidence.PluginSelectionProvider {
				return commandExplanation{}, fmt.Errorf("selected Plugin evidence contains unsupported reason %q", reason.Kind())
			}
			provider, found := selectedCapabilityProvider(evidence, reason.Capability())
			if !found || provider.PluginID() != subject {
				return commandExplanation{}, fmt.Errorf("selected Plugin evidence omits Provider decision for %s", reason.Capability())
			}
			providerCapabilities = append(providerCapabilities, reason.Capability())
			selectionSources := provider.SelectionSources()
			if len(selectionSources) == 0 {
				input.PrimarySources = append(input.PrimarySources, explainSource(provider.ProviderSource()))
				continue
			}
			for _, source := range selectionSources {
				input.PrimarySources = append(input.PrimarySources, explainSource(source.Source()))
			}
		}
		if input.Reason == "" {
			if len(providerCapabilities) == 0 {
				return commandExplanation{}, fmt.Errorf("selected dependency Plugin evidence omits Provider reasons")
			}
			explanation.decision = "selected as a Provider for " + strings.Join(providerCapabilities, ", ")
			input.Outcome = "selected"
			input.Reason = string(resolutionevidence.PluginSelectionProvider)
			input.Change = selectedPluginChange(evidence, selection, currentModule, subject, providerCapabilities)
		}
	} else {
		explanation.decision = "visible but not selected"
		input.Outcome = "available"
		providerCandidates := pluginProviderCandidates(evidence, subject)
		input.Reason, input.PrimarySources, input.Change = unselectedPluginDecision(evidence, selection, currentModule, candidate, providerCandidates)
	}

	result, err := diagnosticschema.NewExplain(input)
	if err != nil {
		return commandExplanation{}, err
	}
	explanation.result = result
	return explanation, nil
}

func explainConfiguration(resolved applicationresolve.Result, subject string) (commandExplanation, error) {
	evidence := resolved.ResolutionEvidence()
	selection, currentModule, err := explanationProjectContext(evidence)
	if err != nil {
		return commandExplanation{}, err
	}
	field, canonicalSubject, exists := selectedConfigurationField(evidence, subject)
	if !exists {
		return commandExplanation{}, fmt.Errorf("configuration field %q is not present in the selected application model", subject)
	}

	input := diagnosticschema.ExplainInput{
		Evidence:    evidence,
		SubjectKind: diagnosticschema.ExplainSubjectConfiguration,
		Subject:     canonicalSubject,
	}
	explanation := commandExplanation{subjectLabel: "Configuration"}
	changePath := field.Path()
	var owner resolutionevidence.ConfigurationOwner
	var sources []resolutionevidence.Source
	if field.Effective() {
		contribution, found := effectiveConfigurationContribution(field)
		if !found {
			return commandExplanation{}, fmt.Errorf("effective configuration field %q omits its winning contribution", canonicalSubject)
		}
		owner = field.Owner()
		sources = contribution.Sources()
		input.Reason = string(owner)
		if field.Removed() {
			input.Outcome = "removed"
			explanation.decision = fmt.Sprintf("removed%s by %s", configurationPluginDescription(field.Path(), evidence), owner)
		} else {
			input.Outcome = "effective"
			explanation.decision = fmt.Sprintf("effective %s%s from %s", field.Summary(), configurationPluginDescription(field.Path(), evidence), owner)
		}
	} else {
		suppressor, found := suppressingConfigurationField(evidence, field.Path())
		if !found {
			return commandExplanation{}, fmt.Errorf("suppressed configuration field %q omits its effective ancestor", canonicalSubject)
		}
		contribution, found := effectiveConfigurationContribution(suppressor)
		if !found {
			return commandExplanation{}, fmt.Errorf("suppressing configuration field %q omits its winning contribution", suppressor.Path())
		}
		owner = suppressor.Owner()
		sources = contribution.Sources()
		changePath = suppressor.Path()
		input.Outcome = "suppressed"
		if suppressor.Removed() {
			input.Reason = "ancestor-removal"
		} else {
			input.Reason = "ancestor-replacement"
		}
		explanation.decision = fmt.Sprintf("suppressed%s by %s at %s", configurationPluginDescription(field.Path(), evidence), owner, suppressor.Path())
	}
	if len(sources) == 0 {
		return commandExplanation{}, fmt.Errorf("configuration field %q omits causal source provenance", canonicalSubject)
	}
	input.PrimarySources = make([]diagnosticjson.Source, len(sources))
	for index, source := range sources {
		input.PrimarySources[index] = explainSource(source)
	}
	input.Change = configurationFieldChange(selection, currentModule, changePath, owner, sources)

	result, err := diagnosticschema.NewExplain(input)
	if err != nil {
		return commandExplanation{}, err
	}
	explanation.result = result
	return explanation, nil
}

func selectedConfigurationField(evidence resolutionevidence.Evidence, subject string) (resolutionevidence.ConfigurationField, string, bool) {
	fields := evidence.ConfigurationFields()
	byPath := make(map[string]resolutionevidence.ConfigurationField, len(fields))
	for _, field := range fields {
		byPath[field.Path()] = field
	}
	if field, exists := byPath[subject]; exists {
		return field, subject, true
	}
	if !strings.HasPrefix(subject, "config.") {
		return resolutionevidence.ConfigurationField{}, "", false
	}
	pluginID := ""
	for _, candidate := range evidence.PluginCandidates() {
		prefix := "config." + candidate.ID()
		if subject != prefix && !strings.HasPrefix(subject, prefix+".") {
			continue
		}
		if len(candidate.ID()) > len(pluginID) {
			pluginID = candidate.ID()
		}
	}
	if pluginID == "" {
		return resolutionevidence.ConfigurationField{}, "", false
	}
	canonical := "config[" + strconv.Quote(pluginID) + "]"
	remainder := strings.TrimPrefix(subject, "config."+pluginID)
	if remainder != "" {
		for _, segment := range strings.Split(strings.TrimPrefix(remainder, "."), ".") {
			if segment == "" {
				return resolutionevidence.ConfigurationField{}, "", false
			}
			canonical += "[" + strconv.Quote(segment) + "]"
		}
	}
	field, exists := byPath[canonical]
	return field, canonical, exists
}

func effectiveConfigurationContribution(field resolutionevidence.ConfigurationField) (resolutionevidence.ConfigurationContribution, bool) {
	for _, contribution := range field.Contributors() {
		if contribution.Effective() {
			return contribution, true
		}
	}
	return resolutionevidence.ConfigurationContribution{}, false
}

func suppressingConfigurationField(evidence resolutionevidence.Evidence, path string) (resolutionevidence.ConfigurationField, bool) {
	fields := evidence.ConfigurationFields()
	byPath := make(map[string]resolutionevidence.ConfigurationField, len(fields))
	for _, field := range fields {
		byPath[field.Path()] = field
	}
	ancestors := explainConfigurationPathAncestors(path)
	for index := len(ancestors) - 1; index >= 0; index-- {
		ancestor, exists := byPath[ancestors[index]]
		if !exists || !ancestor.Effective() || configurationFieldIsObject(ancestor, fields) {
			continue
		}
		return ancestor, true
	}
	return resolutionevidence.ConfigurationField{}, false
}

func explainConfigurationPathAncestors(value string) []string {
	if strings.HasPrefix(value, "config[") {
		var result []string
		for index := strings.IndexByte(value, '['); index >= 0; {
			close := strings.IndexByte(value[index:], ']')
			if close < 0 {
				break
			}
			close += index
			result = append(result, value[:close+1])
			next := close + 1
			if next >= len(value) || value[next] != '[' {
				break
			}
			index = next
		}
		if len(result) > 0 {
			result = result[:len(result)-1]
		}
		return result
	}
	parts := strings.Split(value, ".")
	result := make([]string, 0, len(parts)-1)
	for index := 1; index < len(parts); index++ {
		result = append(result, strings.Join(parts[:index], "."))
	}
	return result
}

func configurationFieldIsObject(field resolutionevidence.ConfigurationField, fields []resolutionevidence.ConfigurationField) bool {
	if field.Summary() == "object" {
		return true
	}
	if field.Summary() != "redacted" {
		return false
	}
	for _, candidate := range fields {
		if candidate.Path() != field.Path() && strings.HasPrefix(candidate.Path(), field.Path()+"[") {
			return true
		}
	}
	return false
}

func configurationPluginDescription(path string, evidence resolutionevidence.Evidence) string {
	for _, candidate := range evidence.PluginCandidates() {
		if strings.HasPrefix(path, "config["+strconv.Quote(candidate.ID())+"]") {
			return " for Plugin " + candidate.ID()
		}
	}
	return ""
}

func configurationFieldChange(selection resolutionevidence.ConfigurationSelection, currentModule, field string, owner resolutionevidence.ConfigurationOwner, sources []resolutionevidence.Source) diagnosticschema.ExplainChange {
	path := selection.SelectedPath()
	if owner != resolutionevidence.ConfigurationOwnerDependency {
		for _, source := range sources {
			if source.Module() == currentModule {
				path = source.Path()
				break
			}
		}
	}
	return diagnosticschema.ExplainChange{
		Kind:   diagnosticschema.ExplainChangeFile,
		Module: currentModule,
		Path:   path,
		Field:  field,
	}
}

func explanationProjectContext(evidence resolutionevidence.Evidence) (resolutionevidence.ConfigurationSelection, string, error) {
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return resolutionevidence.ConfigurationSelection{}, "", fmt.Errorf("selected application evidence omits configuration provenance")
	}
	for _, module := range evidence.Modules() {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			return selection, module.Path(), nil
		}
	}
	return resolutionevidence.ConfigurationSelection{}, "", fmt.Errorf("selected application evidence omits the current Project")
}

func visiblePlugin(evidence resolutionevidence.Evidence, pluginID string) (resolutionevidence.PluginCandidate, bool) {
	for _, candidate := range evidence.PluginCandidates() {
		if candidate.ID() == pluginID {
			return candidate, true
		}
	}
	return resolutionevidence.PluginCandidate{}, false
}

func selectedPlugin(evidence resolutionevidence.Evidence, pluginID string) (resolutionevidence.SelectedPlugin, bool) {
	for _, plugin := range evidence.SelectedPlugins() {
		if plugin.ID() == pluginID {
			return plugin, true
		}
	}
	return resolutionevidence.SelectedPlugin{}, false
}

func pluginProviderCandidates(evidence resolutionevidence.Evidence, pluginID string) []resolutionevidence.ProviderCandidate {
	var result []resolutionevidence.ProviderCandidate
	for _, candidate := range evidence.ProviderCandidates() {
		if candidate.PluginID() == pluginID {
			result = append(result, candidate)
		}
	}
	return result
}

func selectedPluginChange(evidence resolutionevidence.Evidence, selection resolutionevidence.ConfigurationSelection, currentModule, pluginID string, capabilities []string) diagnosticschema.ExplainChange {
	for _, capability := range capabilities {
		for _, candidate := range evidence.ProviderCandidates() {
			if candidate.Capability() == capability && candidate.PluginID() != pluginID {
				return diagnosticschema.ExplainChange{
					Kind:    diagnosticschema.ExplainChangeCommand,
					Command: "plystra use " + capability + " " + candidate.PluginID() + explainSelectorSuffix(selection),
				}
			}
		}
	}
	return diagnosticschema.ExplainChange{
		Kind:   diagnosticschema.ExplainChangeFile,
		Module: currentModule,
		Path:   selection.SelectedPath(),
		Field:  fmt.Sprintf("capabilities.require[%q]", capabilities[0]),
	}
}

func unselectedPluginDecision(evidence resolutionevidence.Evidence, selection resolutionevidence.ConfigurationSelection, currentModule string, plugin resolutionevidence.PluginCandidate, candidates []resolutionevidence.ProviderCandidate) (string, []diagnosticjson.Source, diagnosticschema.ExplainChange) {
	for _, candidate := range candidates {
		if candidate.RejectionReason() != resolutionevidence.ProviderRejectionAnotherProviderSelected {
			continue
		}
		return string(candidate.RejectionReason()), []diagnosticjson.Source{explainSource(candidate.Source())}, diagnosticschema.ExplainChange{
			Kind:    diagnosticschema.ExplainChangeCommand,
			Command: "plystra use " + candidate.Capability() + " " + plugin.ID() + explainSelectorSuffix(selection),
		}
	}
	if len(candidates) > 0 {
		sources := make([]diagnosticjson.Source, len(candidates))
		for index, candidate := range candidates {
			sources[index] = explainSource(candidate.Source())
		}
		return string(resolutionevidence.ProviderRejectionCapabilityNotRequired), sources, diagnosticschema.ExplainChange{
			Kind:   diagnosticschema.ExplainChangeFile,
			Module: currentModule,
			Path:   selection.SelectedPath(),
			Field:  fmt.Sprintf("capabilities.require[%q]", candidates[0].Capability()),
		}
	}
	change := diagnosticschema.ExplainChange{
		Kind:   diagnosticschema.ExplainChangeFile,
		Module: currentModule,
		Path:   "go.mod",
		Field:  fmt.Sprintf("require[%q]", plugin.ModulePath()),
	}
	for _, module := range evidence.Modules() {
		if module.Path() == plugin.ModulePath() && module.Direct() {
			change = diagnosticschema.ExplainChange{
				Kind:    diagnosticschema.ExplainChangeCommand,
				Command: "plystra remove " + plugin.ModulePath(),
			}
			break
		}
	}
	return "no-required-provider", []diagnosticjson.Source{explainSource(plugin.Source())}, change
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

func writeHumanExplanation(writer io.Writer, explanation commandExplanation, verbose bool) error {
	result := explanation.result
	var content strings.Builder
	fmt.Fprintf(&content, "%s: %s\n", explanation.subjectLabel, result.Subject())
	fmt.Fprintf(&content, "Decision: %s\n", explanation.decision)
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
