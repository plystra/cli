package command

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/implementationinventory"
)

func inspectImplementationsGraph(resolved applicationresolve.Result) (diagnosticschema.GraphResult, error) {
	evidence := resolved.ResolutionEvidence()
	moduleNodes := make(map[string]string)
	nodes := make([]diagnosticschema.GraphNode, 0, len(evidence.Modules())+len(resolved.Interfaces().Interfaces())+len(resolved.Implementations().Implementations()))
	for _, module := range evidence.Modules() {
		nodeID := inspectGraphNodeID("module", module.Path())
		moduleNodes[module.Path()] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID:      nodeID,
			Kind:    "module",
			Label:   module.Path(),
			Sources: []diagnosticjson.Source{explainSource(module.Source())},
		})
	}
	currentModule := resolved.Module().ModulePath()
	currentModuleNode, exists := moduleNodes[currentModule]
	if currentModule == "" || !exists {
		return diagnosticschema.GraphResult{}, fmt.Errorf("resolution evidence omits the current Project module %s", currentModule)
	}

	interfaceNodes := make(map[string]string)
	for _, definition := range resolved.Interfaces().Interfaces() {
		identifier := definition.ID()
		if _, duplicate := interfaceNodes[identifier]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("visible Interface %s appears more than once", identifier)
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
		nodeID := inspectGraphNodeID("interface", identifier)
		interfaceNodes[identifier] = nodeID
		nodes = append(nodes, diagnosticschema.GraphNode{ID: nodeID, Kind: "interface", Label: definition.PackagePath(), Sources: sources})
	}

	edges := make(inspectGraphEdges)
	implementations := make(map[string]implementationinventory.Implementation)
	constructorSources := make(map[string]diagnosticjson.Source)
	configurationNodes := make(map[string]string)
	for _, implementation := range resolved.Implementations().Implementations() {
		symbol := implementation.Symbol().String()
		if _, duplicate := implementations[symbol]; duplicate {
			return diagnosticschema.GraphResult{}, fmt.Errorf("visible constructor %s appears more than once", symbol)
		}
		owner, exists := moduleNodes[implementation.ModulePath()]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("visible constructor %s owner module %s is absent from resolution evidence", symbol, implementation.ModulePath())
		}
		constructorSource := implementationGraphConstructorSource(implementation)
		constructorNode := inspectGraphNodeID("constructor", symbol)
		implementations[symbol] = implementation
		constructorSources[symbol] = constructorSource
		nodes = append(nodes, diagnosticschema.GraphNode{ID: constructorNode, Kind: "constructor", Label: symbol, Sources: []diagnosticjson.Source{constructorSource}})
		edges.add("defines-constructor", owner, constructorNode, "authored", []diagnosticjson.Source{constructorSource})

		for _, implemented := range implementation.Declaration().ImplementedInterfaces() {
			interfaceNode, exists := interfaceNodes[implemented.ID().String()]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s declares absent Interface %s", symbol, implemented.ID())
			}
			position := implemented.Position()
			directiveSource := diagnosticjson.Source{
				Module: implementation.ModulePath(),
				Path:   position.Path,
				Kind:   "implementation-declaration",
				Line:   position.Line,
				Column: position.Column,
			}
			edges.add("implements-interface", constructorNode, interfaceNode, "declared", []diagnosticjson.Source{directiveSource})
		}
		for _, dependency := range implementation.RequiredInterfaces() {
			interfaceNode, exists := interfaceNodes[dependency.ID().String()]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s requires absent Interface %s", symbol, dependency.ID())
			}
			edges.add("declares-dependency", constructorNode, interfaceNode, "required", []diagnosticjson.Source{constructorSource})
		}
		for _, dependency := range implementation.OptionalInterfaces() {
			interfaceNode, exists := interfaceNodes[dependency.ID().String()]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s optionally requires absent Interface %s", symbol, dependency.ID())
			}
			edges.add("declares-dependency", constructorNode, interfaceNode, "optional", []diagnosticjson.Source{constructorSource})
		}
		if configuration, present := implementation.Configuration(); present {
			configurationNode := inspectGraphNodeID("configuration", symbol)
			configurationNodes[symbol] = configurationNode
			nodes = append(nodes, diagnosticschema.GraphNode{ID: configurationNode, Kind: "configuration", Label: configuration.String(), Sources: []diagnosticjson.Source{constructorSource}})
			edges.add("owns-configuration", constructorNode, configurationNode, "schema", []diagnosticjson.Source{constructorSource})
		}
	}

	bindings := make(map[string]constructorgraph.Binding)
	graph := resolved.InterfaceResolution().Graph()
	for _, binding := range graph.Bindings() {
		identifier := binding.InterfaceID().String()
		interfaceNode, exists := interfaceNodes[identifier]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("bound Interface %s is absent from the visible graph", binding.InterfaceID())
		}
		symbol := binding.Constructor().String()
		if _, exists := implementations[symbol]; !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("bound Interface %s selects constructor %s outside the visible inventory", binding.InterfaceID(), binding.Constructor())
		}
		sources, err := interfaceSelectionGraphSources(evidence, identifier, binding.Reason(), constructorSources[symbol])
		if err != nil {
			return diagnosticschema.GraphResult{}, err
		}
		edges.add("selects-constructor", interfaceNode, inspectGraphNodeID("constructor", symbol), string(binding.Reason()), sources)
		bindings[identifier] = binding
	}
	for _, choice := range resolved.Manifest().ImplementationChoices() {
		identifier := choice.InterfaceID().String()
		if binding, active := bindings[identifier]; active {
			if binding.Constructor() != choice.Constructor() || binding.Reason() != constructorgraph.SelectionExplicit {
				return diagnosticschema.GraphResult{}, fmt.Errorf("active Interface %s does not retain its effective explicit constructor choice", identifier)
			}
			continue
		}
		interfaceNode, exists := interfaceNodes[identifier]
		if !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("dormant explicit choice names absent Interface %s", identifier)
		}
		symbol := choice.Constructor().String()
		if _, exists := implementations[symbol]; !exists {
			return diagnosticschema.GraphResult{}, fmt.Errorf("dormant explicit choice for %s selects constructor %s outside the visible inventory", identifier, symbol)
		}
		sources, err := interfaceSelectionGraphSources(evidence, identifier, constructorgraph.SelectionExplicit, constructorSources[symbol])
		if err != nil {
			return diagnosticschema.GraphResult{}, err
		}
		edges.add("dormant-selects-constructor", interfaceNode, inspectGraphNodeID("constructor", symbol), "explicit", sources)
	}

	for _, constructor := range graph.ConstructionOrder() {
		symbol := constructor.Symbol().String()
		implementation, exists := implementations[symbol]
		if !exists || implementation.Symbol() != constructor.Implementation().Symbol() {
			return diagnosticschema.GraphResult{}, fmt.Errorf("reachable constructor %s is absent or inconsistent in the visible inventory", constructor.Symbol())
		}
		constructorNode := inspectGraphNodeID("constructor", symbol)
		constructorSource := constructorSources[symbol]
		edges.add("assembles-constructor", currentModuleNode, constructorNode, "active-reachable", []diagnosticjson.Source{constructorSource})
		for _, dependency := range constructor.Dependencies() {
			interfaceNode, exists := interfaceNodes[dependency.InterfaceID().String()]
			if !exists {
				return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s dependency Interface %s is absent from the visible graph", constructor.Symbol(), dependency.InterfaceID())
			}
			edges.add("depends-on-interface", constructorNode, interfaceNode, interfaceDependencyGraphReason(dependency.Optional(), dependency.Available()), []diagnosticjson.Source{constructorSource})
		}
	}

	for symbol, configurationNode := range configurationNodes {
		prefix := "config[" + strconv.Quote(symbol) + "]"
		for _, field := range evidence.ConfigurationFields() {
			if field.Path() != prefix && !strings.HasPrefix(field.Path(), prefix+"[") {
				continue
			}
			for _, contribution := range field.Contributors() {
				if !contribution.Effective() {
					continue
				}
				for _, rawSource := range contribution.Sources() {
					source := explainSource(rawSource)
					moduleNode, exists := moduleNodes[source.Module]
					if !exists {
						return diagnosticschema.GraphResult{}, fmt.Errorf("constructor %s configuration source module %s is absent from resolution evidence", symbol, source.Module)
					}
					edges.add("supplies-configuration", moduleNode, configurationNode, string(contribution.Owner()), []diagnosticjson.Source{source})
				}
			}
		}
	}

	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: evidence,
		Type:     diagnosticschema.GraphTypeImplementations,
		Nodes:    nodes,
		Edges:    edges.values(),
	})
}

func implementationGraphConstructorSource(implementation implementationinventory.Implementation) diagnosticjson.Source {
	position := implementation.Declaration().Position()
	return diagnosticjson.Source{
		Module: implementation.ModulePath(),
		Path:   implementation.SourcePath(),
		Kind:   "implementation-constructor",
		Line:   position.Line,
		Column: position.Column,
	}
}

func writeHumanImplementationGraph(writer io.Writer, result diagnosticschema.GraphResult, verbose bool) error {
	nodes := result.Nodes()
	edges := result.Edges()
	owners := make(map[string]string)
	active := make(map[string]bool)
	dormant := make(map[string]bool)
	configurations := make(map[string]string)
	candidateCount := 0
	configurationCount := 0
	for _, node := range nodes {
		switch node.Kind {
		case "constructor":
			candidateCount++
		case "configuration":
			configurationCount++
		}
	}
	for _, edge := range edges {
		switch edge.Kind {
		case "defines-constructor":
			owners[edge.To] = strings.TrimPrefix(edge.From, "module:")
		case "assembles-constructor":
			active[edge.To] = true
		case "dormant-selects-constructor":
			dormant[edge.To] = true
		case "owns-configuration":
			for _, node := range nodes {
				if node.ID == edge.To {
					configurations[edge.From] = node.Label
					break
				}
			}
		}
	}
	activeCount := 0
	dormantCount := 0
	for _, node := range nodes {
		if node.Kind != "constructor" {
			continue
		}
		if active[node.ID] {
			activeCount++
		} else if dormant[node.ID] {
			dormantCount++
		}
	}

	var content strings.Builder
	fmt.Fprintf(
		&content,
		"Implementation graph: %d candidates, %d active, %d dormant explicit, %d unselected, %d configuration schemas, %d relationships\n",
		candidateCount,
		activeCount,
		dormantCount,
		candidateCount-activeCount-dormantCount,
		configurationCount,
		len(edges),
	)
	for _, node := range nodes {
		if node.Kind != "constructor" {
			continue
		}
		owner := owners[node.ID]
		if owner == "" {
			return fmt.Errorf("constructor node %s has no defining module", node.ID)
		}
		state := "unselected-candidate"
		if active[node.ID] {
			state = "active"
		} else if dormant[node.ID] {
			state = "dormant-explicit"
		}
		fmt.Fprintf(&content, "Implementation: %s (%s)\n", node.Label, state)
		fmt.Fprintf(&content, "  Module: %s\n", owner)
		if configuration := configurations[node.ID]; configuration != "" {
			fmt.Fprintf(&content, "  Configuration: %s\n", configuration)
		}
		for _, source := range node.Sources {
			fmt.Fprintf(&content, "  Source: %s\n", explainSourceSummary(source))
		}
	}
	writeHumanInterfaceRelationships(&content, "Implemented Interfaces", edges, "implements-interface")
	writeHumanInterfaceRelationships(&content, "Active selections", edges, "selects-constructor")
	writeHumanInterfaceRelationships(&content, "Dormant explicit selections", edges, "dormant-selects-constructor")
	writeHumanInterfaceRelationships(&content, "Declared dependencies", edges, "declares-dependency")
	writeHumanInterfaceRelationships(&content, "Resolved dependencies", edges, "depends-on-interface")
	writeHumanInterfaceRelationships(&content, "Assembly", edges, "assembles-constructor")
	writeHumanInterfaceRelationships(&content, "Configuration ownership", edges, "owns-configuration")
	writeHumanInterfaceRelationships(&content, "Configuration sources", edges, "supplies-configuration")
	if verbose {
		if err := appendHumanGraphEvidence(&content, result); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, content.String())
	return err
}
