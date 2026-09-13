package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const intrinsicKernelModulePath = "github.com/plystra/kernel"

func inspectInterfacesGraph(resolved applicationresolve.Result) (diagnosticschema.GraphResult, error) {
	evidence := resolved.ResolutionEvidence()
	moduleNodes := make(map[string]string)
	nodes := make([]diagnosticschema.GraphNode, 0, len(evidence.Modules())+len(resolved.Interfaces().Interfaces())+len(resolved.InterfaceResolution().IntrinsicRequirements())+1)
	for _, module := range evidence.Modules() {
		nodeID := interfaceGraphModuleNodeID(module.Path())
		moduleNodes[module.Path()] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID:      nodeID,
			Kind:    "module",
			Label:   module.Path(),
			Sources: []diagnosticjson.Source{explainSource(module.Source())},
		})
	}
	currentModule := resolved.Module().ModulePath()
	if currentModule == "" {
		return diagnosticschema.GraphResult{}, fmt.Errorf("selected Project has no module identity")
	}
	if _, exists := moduleNodes[currentModule]; !exists {
		return diagnosticschema.GraphResult{}, fmt.Errorf("resolution evidence omits the current Project module %s", currentModule)
	}
	if _, exists := moduleNodes[intrinsicKernelModulePath]; !exists {
		nodeID := interfaceGraphModuleNodeID(intrinsicKernelModulePath)
		moduleNodes[intrinsicKernelModulePath] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{ID: nodeID, Kind: "module", Label: intrinsicKernelModulePath})
	}

	edges := make(interfaceGraphEdges)
	interfaceNodes := make(map[string]string)
	for _, definition := range resolved.Interfaces().Interfaces() {
		identifier := definition.ID()
		nodeID := interfaceGraphInterfaceNodeID(identifier)
		if _, duplicate := interfaceNodes[identifier]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("visible Interface %s appears more than once", identifier)
		}
		ownerNode, exists := moduleNodes[definition.ModulePath()]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("visible Interface %s owner module %s is absent from resolution evidence", identifier, definition.ModulePath())
		}
		position := definition.Declaration().Position()
		declarationSource := diagnosticjson.Source{
			Module: definition.ModulePath(),
			Path:   definition.SourcePath(),
			Kind:   "interface-declaration",
			Line:   position.Line,
			Column: position.Column,
		}
		sources := []diagnosticjson.Source{declarationSource}
		if metadata, present := definition.Metadata(); present {
			sources = append(sources, diagnosticjson.Source{
				Module: definition.ModulePath(),
				Path:   metadata.Path(),
				Kind:   "interface-metadata",
				Line:   1,
				Column: 1,
			})
		}
		interfaceNodes[identifier] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{ID: nodeID, Kind: "interface", Label: definition.PackagePath(), Sources: sources})
		edges.add("defines-interface", ownerNode, nodeID, "authored", []diagnosticjson.Source{declarationSource})
	}

	for _, requirement := range resolved.InterfaceResolution().IntrinsicRequirements() {
		identifier := requirement.InterfaceID().String()
		nodeID := interfaceGraphInterfaceNodeID(identifier)
		if _, duplicate := interfaceNodes[identifier]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("intrinsic Interface %s conflicts with a visible authored Interface", identifier)
		}
		source, err := intrinsicInterfaceGraphSource(requirement.PackagePath())
		if err != nil {
			return diagnosticschema.GraphResult{}, fmt.Errorf("intrinsic Interface %s provenance: %w", identifier, err)
		}
		interfaceNodes[identifier] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{ID: nodeID, Kind: "interface", Label: requirement.PackagePath(), Sources: []diagnosticjson.Source{source}})
		edges.add("defines-interface", moduleNodes[intrinsicKernelModulePath], nodeID, "intrinsic", []diagnosticjson.Source{source})
		edges.add("requires-interface", moduleNodes[intrinsicKernelModulePath], nodeID, "intrinsic", []diagnosticjson.Source{source})
		for _, requirementSource := range requirement.RequirementSources() {
			owner, exists := moduleNodes[requirementSource.ModulePath]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("intrinsic Interface %s requirement source module %s is absent from resolution evidence", identifier, requirementSource.ModulePath)
			}
			edges.add("requires-interface", owner, nodeID, string(requirementSource.Kind), []diagnosticjson.Source{interfaceRequirementGraphSource(requirementSource)})
		}
	}

	graph := resolved.InterfaceResolution().Graph()
	for _, root := range graph.Roots() {
		target, exists := interfaceNodes[root.InterfaceID().String()]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("required Interface %s is absent from the visible graph", root.InterfaceID())
		}
		for _, source := range root.RequirementSources() {
			owner, exists := moduleNodes[source.ModulePath]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("interface %s requirement source module %s is absent from resolution evidence", root.InterfaceID(), source.ModulePath)
			}
			edges.add("requires-interface", owner, target, string(source.Kind), []diagnosticjson.Source{{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			}})
		}
	}

	constructorNodes := make(map[string]string)
	constructorSources := make(map[string]diagnosticjson.Source)
	for _, constructor := range graph.ConstructionOrder() {
		symbol := constructor.Symbol().String()
		if _, duplicate := constructorNodes[symbol]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("reachable constructor %s appears more than once", constructor.Symbol())
		}
		implementation := constructor.Implementation()
		position := implementation.Declaration().Position()
		source := diagnosticjson.Source{
			Module: implementation.ModulePath(),
			Path:   implementation.SourcePath(),
			Kind:   "implementation-constructor",
			Line:   position.Line,
			Column: position.Column,
		}
		nodeID := interfaceGraphConstructorNodeID(symbol)
		constructorNodes[symbol] = nodeID
		constructorSources[symbol] = source
		nodes = append(nodes, diagnosticschema.GraphNode{ID: nodeID, Kind: "constructor", Label: symbol, Sources: []diagnosticjson.Source{source}})
	}
	for _, binding := range graph.Bindings() {
		interfaceNode, exists := interfaceNodes[binding.InterfaceID().String()]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("bound Interface %s is absent from the visible graph", binding.InterfaceID())
		}
		constructor := binding.Constructor().String()
		constructorNode, exists := constructorNodes[constructor]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("bound Interface %s selects constructor %s outside the reachable graph", binding.InterfaceID(), binding.Constructor())
		}
		sources, err := interfaceSelectionGraphSources(evidence, binding.InterfaceID().String(), binding.Reason(), constructorSources[constructor])
		if err != nil {
			return diagnosticschema.GraphResult{}, err
		}
		edges.add("selects-constructor", interfaceNode, constructorNode, string(binding.Reason()), sources)
	}
	for _, constructor := range graph.ConstructionOrder() {
		constructorNode := constructorNodes[constructor.Symbol().String()]
		source := constructorSources[constructor.Symbol().String()]
		for _, dependency := range constructor.Dependencies() {
			target, exists := interfaceNodes[dependency.InterfaceID().String()]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s dependency Interface %s is absent from the visible graph", constructor.Symbol(), dependency.InterfaceID())
			}
			reason := interfaceDependencyGraphReason(dependency.Optional(), dependency.Available())
			edges.add("depends-on-interface", constructorNode, target, reason, []diagnosticjson.Source{source})
		}
	}

	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: evidence,
		Type:     diagnosticschema.GraphTypeInterfaces,
		Nodes:    nodes,
		Edges:    edges.values(),
	})
}

func interfaceRequirementGraphSource(source constructorgraph.RequirementSource) diagnosticjson.Source {
	return diagnosticjson.Source{
		Module: source.ModulePath,
		Path:   source.Path,
		Kind:   string(source.Kind),
		Line:   source.Line,
		Column: source.Column,
	}
}

func interfaceSelectionGraphSources(evidence resolutionevidence.Evidence, interfaceID string, reason constructorgraph.SelectionReason, constructorSource diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	switch reason {
	case constructorgraph.SelectionUnique:
		return []diagnosticjson.Source{constructorSource}, nil
	case constructorgraph.SelectionExplicit:
		path := fmt.Sprintf("interfaces.use[%q]", interfaceID)
		for _, field := range evidence.ConfigurationFields() {
			if field.Path() != path {
				continue
			}
			if !field.Effective() || field.Removed() {
				return nil, fmt.Errorf("explicit Interface selection %s has no effective configuration field", interfaceID)
			}
			contribution, found := effectiveConfigurationContribution(field)
			if !found {
				return nil, fmt.Errorf("explicit Interface selection %s has no effective configuration contribution", interfaceID)
			}
			sources := contribution.Sources()
			if len(sources) == 0 {
				return nil, fmt.Errorf("explicit Interface selection %s has no configuration source", interfaceID)
			}
			result := make([]diagnosticjson.Source, len(sources))
			for index, source := range sources {
				result[index] = explainSource(source)
				result[index].Kind = "implementation-selection"
			}
			return result, nil
		}
		return nil, fmt.Errorf("explicit Interface selection %s is absent from configuration evidence", interfaceID)
	default:
		return nil, fmt.Errorf("interface %s has unsupported selection reason %q", interfaceID, reason)
	}
}

func interfaceDependencyGraphReason(optional, available bool) string {
	if !optional {
		return "required"
	}
	if available {
		return "optional-available"
	}
	return "optional-unavailable"
}

func intrinsicInterfaceGraphSource(packagePath string) (diagnosticjson.Source, error) {
	prefix := intrinsicKernelModulePath + "/"
	if !strings.HasPrefix(packagePath, prefix) || len(packagePath) == len(prefix) {
		return diagnosticjson.Source{}, fmt.Errorf("package %q is outside %s", packagePath, intrinsicKernelModulePath)
	}
	return diagnosticjson.Source{
		Module: intrinsicKernelModulePath,
		Path:   strings.TrimPrefix(packagePath, prefix),
		Kind:   "intrinsic-interface",
	}, nil
}

func interfaceGraphModuleNodeID(modulePath string) string {
	return "module:" + modulePath
}

func interfaceGraphInterfaceNodeID(identifier string) string {
	return "interface:" + identifier
}

func interfaceGraphConstructorNodeID(symbol string) string {
	return "constructor:" + symbol
}

type interfaceGraphEdges map[string]diagnosticschema.GraphEdge

func (e interfaceGraphEdges) add(kind diagnosticschema.GraphEdgeKind, from, to, reason string, sources []diagnosticjson.Source) {
	key := string(kind) + "\x00" + from + "\x00" + to + "\x00" + reason
	edge := e[key]
	if edge.ID == "" {
		edge = diagnosticschema.GraphEdge{
			ID:     diagnosticschema.GraphRelationshipID(kind, from+"->"+to+"#"+reason),
			Kind:   kind,
			From:   from,
			To:     to,
			Reason: reason,
		}
	}
	edge.Sources = append(edge.Sources, sources...)
	e[key] = edge
}

func (e interfaceGraphEdges) values() []diagnosticschema.GraphEdge {
	result := make([]diagnosticschema.GraphEdge, 0, len(e))
	for _, edge := range e {
		result = append(result, edge)
	}
	return result
}

func writeHumanInterfaceGraph(writer io.Writer, result diagnosticschema.GraphResult, verbose bool) error {
	nodes := result.Nodes()
	edges := result.Edges()
	active := make(map[string]bool)
	owners := make(map[string]string)
	intrinsic := make(map[string]bool)
	interfaceCount := 0
	constructorCount := 0
	for _, node := range nodes {
		switch node.Kind {
		case "interface":
			interfaceCount++
		case "constructor":
			constructorCount++
		}
	}
	for _, edge := range edges {
		switch edge.Kind {
		case "defines-interface":
			owners[edge.To] = strings.TrimPrefix(edge.From, "module:")
			intrinsic[edge.To] = edge.Reason == "intrinsic"
		case "requires-interface":
			active[edge.To] = true
		case "selects-constructor":
			active[edge.From] = true
		case "depends-on-interface":
			if edge.Reason != "optional-unavailable" {
				active[edge.To] = true
			}
		}
	}
	activeCount := 0
	for _, isActive := range active {
		if isActive {
			activeCount++
		}
	}

	var content strings.Builder
	fmt.Fprintf(&content, "Interface graph: %d Interfaces, %d active, %d constructors, %d relationships\n", interfaceCount, activeCount, constructorCount, len(edges))
	for _, node := range nodes {
		if node.Kind != "interface" {
			continue
		}
		owner := owners[node.ID]
		if owner == "" {
			return fmt.Errorf("interface node %s has no defining module", node.ID)
		}
		state := "inactive"
		if active[node.ID] {
			state = "active"
		}
		origin := "authored"
		if intrinsic[node.ID] {
			origin = "intrinsic"
		}
		fmt.Fprintf(&content, "Interface: %s (%s, %s)\n", strings.TrimPrefix(node.ID, "interface:"), state, origin)
		fmt.Fprintf(&content, "  Package: %s\n", node.Label)
		fmt.Fprintf(&content, "  Owner: %s\n", owner)
		for _, source := range node.Sources {
			fmt.Fprintf(&content, "  Source: %s\n", explainSourceSummary(source))
		}
	}
	if constructorCount > 0 {
		content.WriteString("Active constructors:\n")
		for _, node := range nodes {
			if node.Kind != "constructor" {
				continue
			}
			fmt.Fprintf(&content, "  %s\n", node.Label)
			for _, source := range node.Sources {
				fmt.Fprintf(&content, "    Source: %s\n", explainSourceSummary(source))
			}
		}
	}
	writeHumanInterfaceRelationships(&content, "Requirements", edges, "requires-interface")
	writeHumanInterfaceRelationships(&content, "Selections", edges, "selects-constructor")
	writeHumanInterfaceRelationships(&content, "Dependencies", edges, "depends-on-interface")
	if verbose {
		if err := appendHumanGraphEvidence(&content, result); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, content.String())
	return err
}

func writeHumanInterfaceRelationships(content *strings.Builder, heading string, edges []diagnosticschema.GraphEdge, kind diagnosticschema.GraphEdgeKind) {
	wroteHeading := false
	for _, edge := range edges {
		if edge.Kind != kind {
			continue
		}
		if !wroteHeading {
			content.WriteString(heading + ":\n")
			wroteHeading = true
		}
		from := interfaceGraphRelationshipIdentity(edge.From)
		to := interfaceGraphRelationshipIdentity(edge.To)
		fmt.Fprintf(content, "  %s -> %s (%s)\n", from, to, edge.Reason)
		for _, source := range edge.Sources {
			fmt.Fprintf(content, "    Source: %s\n", explainSourceSummary(source))
		}
	}
}

func interfaceGraphRelationshipIdentity(value string) string {
	for _, prefix := range []string{"module:", "interface:", "constructor:"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return value
}
