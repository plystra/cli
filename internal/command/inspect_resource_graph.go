package command

import (
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
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
	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: resolved.ResolutionEvidence(), Type: diagnosticschema.GraphTypeResources,
		Nodes: nodes, Edges: edges.values(),
	})
}

func writeHumanResourceGraph(writer io.Writer, result diagnosticschema.GraphResult, verbose bool) error {
	var output strings.Builder
	fmt.Fprintf(&output, "Resource contracts: %d visible\n", result.EdgeCount())
	for _, node := range result.Nodes() {
		if node.Kind != "resource-contract" {
			continue
		}
		fmt.Fprintf(&output, "Resource: %s\n  Package: %s\n  Contract digest: %s\n", node.ResourceID, node.Label, node.ContractDigest)
		for _, source := range node.Sources {
			fmt.Fprintf(&output, "  Source: %s\n", explainSourceSummary(source))
		}
	}
	output.WriteString("Resource instance construction and binding are not supported.\n")
	if verbose {
		if err := appendHumanGraphEvidence(&output, result); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, output.String())
	return err
}
