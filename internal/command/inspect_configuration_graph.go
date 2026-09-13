package command

import (
	"fmt"
	"io"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func inspectConfigurationGraph(evidence resolutionevidence.Evidence) (diagnosticschema.GraphResult, error) {
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return diagnosticschema.GraphResult{}, fmt.Errorf("resolution evidence omits selected configuration provenance")
	}

	modules := evidence.Modules()
	moduleNodes := make(map[string]string, len(modules))
	nodes := make([]diagnosticschema.GraphNode, 0, len(modules)+evidence.ConfigurationFieldCount()+1)
	currentModuleNode := ""
	currentSourceModule := ""
	for _, module := range modules {
		nodeID := inspectGraphNodeID("module", module.Path())
		sourceModule := module.Source().Module()
		if _, duplicate := moduleNodes[sourceModule]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("configuration source module %s appears more than once", sourceModule)
		}
		moduleNodes[sourceModule] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID:      nodeID,
			Kind:    "module",
			Label:   module.Path(),
			Sources: []diagnosticjson.Source{explainSource(module.Source())},
		})
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			currentModuleNode = nodeID
			currentSourceModule = sourceModule
		}
	}
	if currentModuleNode == "" || currentSourceModule == "" {
		return diagnosticschema.GraphResult{}, fmt.Errorf("resolution evidence omits the current Project module")
	}

	selectionNode := inspectGraphNodeID("configuration-selection", string(selection.Mode()))
	selectionSource := diagnosticjson.Source{
		Module: currentSourceModule,
		Path:   selection.SelectedPath(),
		Kind:   "configuration-selection",
		Line:   1,
		Column: 1,
	}
	nodes = append(nodes, diagnosticschema.GraphNode{
		ID:      selectionNode,
		Kind:    "configuration-selection",
		Label:   configurationGraphSelectionLabel(selection),
		Sources: []diagnosticjson.Source{selectionSource},
	})

	edges := make(inspectGraphEdges)
	edges.add("selects-configuration", currentModuleNode, selectionNode, string(selection.Mode()), []diagnosticjson.Source{selectionSource})
	for _, module := range modules {
		if module.Role() != resolutionevidence.ModuleRoleDependency {
			continue
		}
		edges.add(
			"composes-configuration",
			inspectGraphNodeID("module", module.Path()),
			selectionNode,
			"dependency-baseline",
			[]diagnosticjson.Source{explainSource(module.Source())},
		)
	}

	fields := evidence.ConfigurationFields()
	fieldNodes := make(map[string]string, len(fields))
	for _, field := range fields {
		nodeID := inspectGraphNodeID("configuration-field", field.Path())
		fieldNodes[field.Path()] = nodeID
		var sources []diagnosticjson.Source
		for _, contribution := range field.Contributors() {
			for _, source := range contribution.Sources() {
				sources = append(sources, explainSource(source))
			}
		}
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID:      nodeID,
			Kind:    "configuration-field",
			Label:   field.Path(),
			Sources: sources,
		})
	}

	for _, field := range fields {
		fieldNode := fieldNodes[field.Path()]
		for _, contribution := range field.Contributors() {
			groups, err := configurationGraphSourceGroups(contribution.Sources(), moduleNodes)
			if err != nil {
				return diagnosticschema.GraphResult{}, fmt.Errorf("configuration field %s: %w", field.Path(), err)
			}
			for moduleNode, sources := range groups {
				edges.add("contributes-configuration", moduleNode, fieldNode, string(contribution.Owner()), sources)
				if !contribution.Effective() {
					continue
				}
				kind := diagnosticschema.GraphEdgeKind("sets-configuration")
				if contribution.Removed() {
					kind = "removes-configuration"
				}
				edges.add(kind, moduleNode, fieldNode, string(contribution.Owner()), sources)
			}
		}
		if field.Effective() {
			continue
		}
		suppressor, found := suppressingConfigurationField(evidence, field.Path())
		if !found {
			return diagnosticschema.GraphResult{}, fmt.Errorf("suppressed configuration field %s omits its effective ancestor", field.Path())
		}
		contribution, found := effectiveConfigurationContribution(suppressor)
		if !found {
			return diagnosticschema.GraphResult{}, fmt.Errorf("suppressing configuration field %s omits its winning contribution", suppressor.Path())
		}
		reason := "ancestor-replacement"
		if suppressor.Removed() {
			reason = "ancestor-removal"
		}
		sources := make([]diagnosticjson.Source, 0, len(contribution.Sources()))
		for _, source := range contribution.Sources() {
			sources = append(sources, explainSource(source))
		}
		edges.add("suppresses-configuration", fieldNodes[suppressor.Path()], fieldNode, reason, sources)
	}

	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: evidence,
		Type:     diagnosticschema.GraphTypeConfiguration,
		Nodes:    nodes,
		Edges:    edges.values(),
	})
}

func configurationGraphSourceGroups(sources []resolutionevidence.Source, moduleNodes map[string]string) (map[string][]diagnosticjson.Source, error) {
	groups := make(map[string][]diagnosticjson.Source)
	for _, source := range sources {
		moduleNode, exists := moduleNodes[source.Module()]
		if !exists {
			return nil, fmt.Errorf("source module %s is absent from resolution evidence", source.Module())
		}
		groups[moduleNode] = append(groups[moduleNode], explainSource(source))
	}
	return groups, nil
}

func configurationGraphSelectionLabel(selection resolutionevidence.ConfigurationSelection) string {
	switch selection.Mode() {
	case generation.ConfigurationModeDefault:
		return fmt.Sprintf("default: %s over dependency composition", selection.SelectedPath())
	case generation.ConfigurationModeEnvironment:
		return fmt.Sprintf("environment %q: %s over %s and dependency composition", selection.Environment(), selection.SelectedPath(), selection.RootPath())
	case generation.ConfigurationModeExplicit:
		return fmt.Sprintf("explicit-config: %s over dependency composition; %s is Project marker only", selection.SelectedPath(), selection.RootPath())
	default:
		return string(selection.Mode())
	}
}

func writeHumanConfigurationGraph(writer io.Writer, result diagnosticschema.GraphResult, evidence resolutionevidence.Evidence, verbose bool) error {
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return fmt.Errorf("resolution evidence omits selected configuration provenance")
	}
	fields := evidence.ConfigurationFields()
	effectiveValues := 0
	removals := 0
	suppressed := 0
	contributions := 0
	for _, field := range fields {
		contributions += len(field.Contributors())
		switch {
		case !field.Effective():
			suppressed++
		case field.Removed():
			removals++
		default:
			effectiveValues++
		}
	}

	var content strings.Builder
	fmt.Fprintf(
		&content,
		"Configuration graph: %d fields, %d effective values, %d removals, %d suppressed, %d contributions, %d relationships\n",
		len(fields),
		effectiveValues,
		removals,
		suppressed,
		contributions,
		result.EdgeCount(),
	)
	fmt.Fprintf(&content, "Selection: %s\n", configurationGraphSelectionLabel(selection))
	for _, field := range fields {
		state, err := humanConfigurationFieldState(evidence, field)
		if err != nil {
			return err
		}
		fmt.Fprintf(&content, "Field: %s (%s)\n", field.Path(), state)
		for _, contribution := range field.Contributors() {
			contributionState := "overridden"
			if contribution.Effective() {
				contributionState = "effective"
			} else if !field.Effective() {
				contributionState = "suppressed"
			}
			fmt.Fprintf(
				&content,
				"  Contribution: %s (precedence %d, %s, %s)\n",
				contribution.Owner(),
				contribution.Precedence(),
				contributionState,
				contribution.Summary(),
			)
			for _, source := range contribution.Sources() {
				fmt.Fprintf(&content, "    Source: %s\n", explainSourceSummary(explainSource(source)))
			}
		}
	}
	if verbose {
		if err := appendHumanGraphEvidence(&content, result); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, content.String())
	return err
}

func humanConfigurationFieldState(evidence resolutionevidence.Evidence, field resolutionevidence.ConfigurationField) (string, error) {
	if field.Effective() {
		if field.Removed() {
			return fmt.Sprintf("removed by %s", field.Owner()), nil
		}
		return fmt.Sprintf("effective %s from %s", field.Summary(), field.Owner()), nil
	}
	suppressor, found := suppressingConfigurationField(evidence, field.Path())
	if !found {
		return "", fmt.Errorf("suppressed configuration field %s omits its effective ancestor", field.Path())
	}
	reason := "ancestor replacement"
	if suppressor.Removed() {
		reason = "ancestor removal"
	}
	return fmt.Sprintf("suppressed by %s at %s through %s", suppressor.Owner(), suppressor.Path(), reason), nil
}
