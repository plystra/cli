package diagnosticschema

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestImplementationGraphRetainsResourceParameters(t *testing.T) {
	t.Parallel()
	resourceID := "data." + strings.Repeat("long", 300) + "/v1"
	resourceNode := GraphNodeID("resource-contract", resourceID)
	input := GraphInput{
		Evidence: resolvedInspectEvidence(t), Type: GraphTypeImplementations,
		Nodes: []GraphNode{
			{ID: "constructor:example.com/app.New", Kind: "constructor", Label: "example.com/app.New"},
			{ID: resourceNode, Kind: "resource-contract", Label: "example.com/api", ResourceID: resourceID, ContractDigest: "sha256:" + strings.Repeat("a", 64)},
		},
	}
	for index, name := range []string{"database", "Database", "_database", "\u03b4", strings.Repeat("x", 1100)} {
		input.Edges = append(input.Edges, GraphEdge{
			ID: GraphRelationshipID("declares-dependency", name), Kind: "declares-dependency",
			From: input.Nodes[0].ID, To: resourceNode, Reason: "resource",
			ParameterName: name, ParameterPosition: index + 2,
		})
	}
	result, err := NewGraph(input)
	if err != nil || !result.Valid() || result.EdgeCount() != 5 {
		t.Fatalf("Resource dependency graph: %#v, %v", result, err)
	}
	if !bytes.Contains(result.Envelope().CanonicalJSON(), []byte(resourceID)) || result.Nodes()[1].ContractDigest != input.Nodes[1].ContractDigest {
		t.Fatal("graph lost the exact Resource identity or digest")
	}
	for _, expected := range input.Edges {
		if !slices.ContainsFunc(result.Edges(), func(edge GraphEdge) bool {
			return edge.ID == expected.ID && edge.ParameterName == expected.ParameterName && edge.ParameterPosition == expected.ParameterPosition && edge.Reason == "resource"
		}) {
			t.Fatalf("lost Resource parameter: %#v", expected)
		}
	}
	slices.Reverse(input.Nodes)
	slices.Reverse(input.Edges)
	reordered, err := NewGraph(input)
	if err != nil || !bytes.Equal(result.Envelope().CanonicalJSON(), reordered.Envelope().CanonicalJSON()) {
		t.Fatalf("Resource graph changed with discovery order: %v", err)
	}
}

func TestResourceGraphRejectsBindingsAndInvalidConsumerEdges(t *testing.T) {
	t.Parallel()
	input := GraphInput{
		Evidence: resolvedInspectEvidence(t), Type: GraphTypeImplementations,
		Nodes: []GraphNode{
			{ID: "constructor:example.com/app.New", Kind: "constructor", Label: "example.com/app.New"},
			{ID: "resource-contract:data.database/v1", Kind: "resource-contract", Label: "example.com/api", ResourceID: "data.database/v1", ContractDigest: "sha256:" + strings.Repeat("a", 64)},
			{ID: "interface:app.run/v1", Kind: "interface", Label: "app.run/v1"},
			{ID: "module:example.com/app", Kind: "module", Label: "example.com/app"},
		},
		Edges: []GraphEdge{{ID: "declares-dependency:database", Kind: "declares-dependency", From: "constructor:example.com/app.New", To: "resource-contract:data.database/v1", Reason: "resource", ParameterName: "database", ParameterPosition: 2}},
	}
	for name, mutate := range map[string]func(*GraphInput){
		"contract-view":    func(i *GraphInput) { i.Type = GraphTypeResources },
		"interface-view":   func(i *GraphInput) { i.Type = GraphTypeInterfaces },
		"missing-digest":   func(i *GraphInput) { i.Nodes[1].ContractDigest = "" },
		"mismatched-id":    func(i *GraphInput) { i.Nodes[1].ResourceID = "data.other/v1" },
		"interface-target": func(i *GraphInput) { i.Edges[0].To = i.Nodes[2].ID },
		"module-consumer":  func(i *GraphInput) { i.Edges[0].From = i.Nodes[3].ID },
		"interface-reason": func(i *GraphInput) { i.Edges[0].Reason = "required" },
		"unnamed":          func(i *GraphInput) { i.Edges[0].ParameterName = "" },
		"blank":            func(i *GraphInput) { i.Edges[0].ParameterName = "_" },
		"missing-position": func(i *GraphInput) { i.Edges[0].ParameterPosition = 0 },
		"resolved-interface": func(i *GraphInput) {
			i.Edges[0].Kind = "depends-on-interface"
			i.Edges[0].ID = "depends-on-interface:database"
		},
		"active-binding": func(i *GraphInput) { i.Edges[0].Kind = "binds-resource"; i.Edges[0].ID = "binds-resource:database" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := cloneGraphInput(input)
			mutate(&invalid)
			if _, err := NewGraph(invalid); !errors.Is(err, ErrGraph) {
				t.Fatalf("accepted invalid Resource graph: %v", err)
			}
		})
	}
	input.Type = GraphTypeResources
	input.Nodes = []GraphNode{input.Nodes[1], input.Nodes[3]}
	input.Edges = []GraphEdge{{ID: "defines-resource:database", Kind: "defines-resource", From: input.Nodes[1].ID, To: input.Nodes[0].ID, Reason: "authored"}}
	result, err := NewGraph(input)
	if err != nil || !result.Valid() {
		t.Fatalf("contract-only Resource graph: %v", err)
	}
	for _, mutate := range []func(*GraphInput){
		func(i *GraphInput) { i.Edges[0].Reason = "selected" },
		func(i *GraphInput) { i.Edges[0].ParameterName = "database"; i.Edges[0].ParameterPosition = 1 },
		func(i *GraphInput) { i.Edges[0].From, i.Edges[0].To = i.Edges[0].To, i.Edges[0].From },
		func(i *GraphInput) {
			i.Nodes[1].Kind = "resource-instance"
			i.Nodes[1].ID = "resource-instance:database"
			i.Edges = nil
		},
	} {
		invalid := cloneGraphInput(input)
		mutate(&invalid)
		if _, err := NewGraph(invalid); !errors.Is(err, ErrGraph) {
			t.Fatalf("accepted runtime facts in contract-only view: %v", err)
		}
	}
}
