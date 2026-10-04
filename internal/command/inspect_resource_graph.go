package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/interfaceprovenance"
)

func inspectResourcesGraph(resolved applicationresolve.Result) (diagnosticschema.GraphResult, error) {
	var nodes []diagnosticschema.GraphNode
	edges := make(inspectGraphEdges)
	owners := make(map[string]bool)
	for _, definition := range resolved.Resources().Resources() {
		owner := diagnosticschema.GraphNodeID("module", definition.ModulePath())
		if !owners[owner] {
			nodes = append(nodes, diagnosticschema.GraphNode{ID: owner, Kind: "module", Label: definition.ModulePath()})
			owners[owner] = true
		}
		position := definition.Declaration().Position()
		sources := []diagnosticjson.Source{{
			Module: definition.ModulePath(), Path: definition.SourcePath(), Kind: "resource-declaration",
			Line: position.Line, Column: position.Column,
		}}
		id := diagnosticschema.GraphNodeID("resource-contract", definition.ID())
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID: id, Kind: "resource-contract", Label: definition.PackagePath(),
			Sources: sources, ContractDigest: definition.ContractDigest(), ResourceID: definition.ID(),
		})
		edges.add("defines-resource", owner, id, "authored", sources)
	}
	var err error
	nodes, err = appendResourceInstancesGraph(resolved, nodes, edges)
	if err != nil {
		return diagnosticschema.GraphResult{}, err
	}
	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: resolved.ResolutionEvidence(), Type: diagnosticschema.GraphTypeResources,
		Nodes: nodes, Edges: edges.values(),
	})
}

func writeHumanResourceGraph(writer io.Writer, result diagnosticschema.GraphResult, verbose bool) error {
	var output strings.Builder
	contracts := 0
	for _, node := range result.Nodes() {
		if node.Kind == "resource-contract" {
			contracts++
		}
	}
	fmt.Fprintf(&output, "Resource contracts: %d visible\n", contracts)
	for _, node := range result.Nodes() {
		if node.Kind != "resource-contract" {
			continue
		}
		fmt.Fprintf(&output, "Resource: %s\n  Package: %s\n  Contract digest: %s\n", node.ResourceID, node.Label, node.ContractDigest)
		for _, source := range node.Sources {
			fmt.Fprintf(&output, "  Source: %s\n", explainSourceSummary(source))
		}
	}
	writeHumanResourceInstances(&output, result)
	if verbose {
		if err := appendHumanGraphEvidence(&output, result); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, output.String())
	return err
}

func appendResourceInstancesGraph(resolved applicationresolve.Result, nodes []diagnosticschema.GraphNode, edges inspectGraphEdges) ([]diagnosticschema.GraphNode, error) {
	resources, bindings, err := interfaceprovenance.ResourceInputs(resolved.InterfaceResolution().Graph(), resolved.Resources())
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		seen[node.ID] = true
	}
	for _, resource := range resources {
		resource.ConfigurationSources = resolved.ResolutionEvidence().ResourceConfigurationSources(resource.Name)
		id := inspectGraphNodeID("resource-instance", resource.Name)
		sources := resourceRecordSources(resource.SelectionSources)
		sources = append(sources, diagnosticjson.Source(resource.DeclarationSource))
		sources = append(sources, diagnosticjson.Source(resource.ContractSource))
		sources = append(sources, resourceRecordSources(resource.ConfigurationSources)...)
		nodes = append(nodes, diagnosticschema.GraphNode{ID: id, Kind: "resource-instance", Label: resource.Name, Sources: sources, ResourceInstance: &resource})
		edges.add("instantiates-resource", id, inspectGraphNodeID("resource-contract", resource.ResourceID), "explicit", sources)
	}
	for _, binding := range bindings {
		kind := diagnosticschema.GraphNodeKind("constructor")
		if binding.ConsumerKind == "instances" {
			kind = "resource-instance"
		}
		from, to := inspectGraphNodeID(kind, binding.Consumer), inspectGraphNodeID("resource-instance", binding.InstanceName)
		if kind == "constructor" && !seen[from] {
			nodes = append(nodes, diagnosticschema.GraphNode{ID: from, Kind: kind, Label: binding.Constructor, Sources: []diagnosticjson.Source{diagnosticjson.Source(binding.DeclarationSource)}})
			seen[from] = true
		}
		sources := []diagnosticjson.Source{diagnosticjson.Source(binding.DeclarationSource)}
		sources = append(sources, resourceRecordSources(binding.BindingSources)...)
		sources = append(sources, resourceRecordSources(binding.SelectionSources)...)
		sources = append(sources, resourceRecordSources(binding.ConsumerSelectionSources)...)
		id := diagnosticschema.GraphRelationshipID("depends-on-resource", fmt.Sprintf("%s->%s#%d:%s", from, to, binding.ParameterPosition, binding.ParameterName))
		edges[id] = diagnosticschema.GraphEdge{ID: id, Kind: "depends-on-resource", From: from, To: to, Reason: string(binding.Reason), ParameterName: binding.ParameterName, ParameterPosition: binding.ParameterPosition, Sources: sources, ResourceBinding: &binding}
	}
	return nodes, nil
}

func resourceRecordSources(values []interfaceprovenance.ResourceSource) []diagnosticjson.Source {
	result := make([]diagnosticjson.Source, len(values))
	for i, v := range values {
		result[i] = diagnosticjson.Source(v)
	}
	return result
}

func writeHumanResourceInstances(output *strings.Builder, result diagnosticschema.GraphResult) {
	count := 0
	for _, node := range result.Nodes() {
		if node.ResourceInstance != nil {
			count++
		}
	}
	fmt.Fprintf(output, "Resource instances: %d selected\n", count)
	for _, node := range result.Nodes() {
		if node.ResourceInstance == nil {
			continue
		}
		resource := node.ResourceInstance
		fmt.Fprintf(output, "Instance: %s\n  Resource: %s\n  Provider: %s\n  Module: %s@%s\n  Construction order: %d\n", resource.Name, resource.ResourceID, resource.Provider, resource.ModulePath, resource.ModuleVersion, resource.ConstructionOrder)
		if resource.ConfigurationOwner != "" {
			fmt.Fprintf(output, "  Configuration: %s\n", resource.ConfigurationOwner)
		}
		for _, source := range node.Sources {
			fmt.Fprintf(output, "  Source: %s\n", explainSourceSummary(source))
		}
	}
	for _, edge := range result.Edges() {
		if edge.ResourceBinding == nil {
			continue
		}
		binding := edge.ResourceBinding
		fmt.Fprintf(output, "Resource binding: %s %s parameter %d %s -> %s (%s)\n  Constructor: %s\n  Resource: %s\n  Provider: %s\n", binding.ConsumerKind, binding.Consumer, binding.ParameterPosition, binding.ParameterName, binding.InstanceName, binding.Reason, binding.Constructor, binding.ResourceID, binding.Provider)
		for _, source := range edge.Sources {
			fmt.Fprintf(output, "  Source: %s\n", explainSourceSummary(source))
		}
	}
}
