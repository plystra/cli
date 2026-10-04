package diagnosticschema

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceprovenance"
)

func resourceInstanceGraphInput(t testing.TB) GraphInput {
	t.Helper()
	contract := GraphNode{ID: "resource-contract:data.database/v1", Kind: "resource-contract", Label: "example.com/app/database", ResourceID: "data.database/v1", ContractDigest: "sha256:" + strings.Repeat("a", 64)}
	sources := []interfaceprovenance.ResourceSource{{Module: "example.com/app", Path: "plystra.yaml", Kind: "resource-selection", Line: 2, Column: 3}}
	input := GraphInput{Evidence: resolvedInspectEvidence(t), Type: GraphTypeResources, Nodes: []GraphNode{contract, {ID: "constructor:example.com/app/service.New", Kind: "constructor", Label: "example.com/app/service.New"}}}
	for i, name := range []string{"database.primary", "database.unconsumed"} {
		resource := interfaceprovenance.ResourceInput{Name: name, ResourceID: contract.ResourceID, PackagePath: contract.Label, ContractDigest: contract.ContractDigest, Provider: "example.com/app/provider.New", ModulePath: "example.com/app", ModuleVersion: "local", DeclarationSource: interfaceprovenance.ResourceSource{Module: "example.com/app", Path: "provider/new.go", Kind: "resource-provider-constructor", Line: 4, Column: 6}, SelectionSources: sources, ConstructionOrder: i + 1}
		resource.ContractSource = interfaceprovenance.ResourceSource{Module: "example.com/app", Path: "database/resource.go", Kind: "resource-declaration", Line: 2, Column: 1}
		input.Nodes = append(input.Nodes, GraphNode{ID: "resource-instance:" + name, Kind: "resource-instance", Label: name, ResourceInstance: &resource})
		input.Edges = append(input.Edges, GraphEdge{ID: "instantiates-resource:" + name, Kind: "instantiates-resource", From: "resource-instance:" + name, To: contract.ID, Reason: "explicit"})
	}
	for i, name := range []string{"database", "Database"} {
		binding := interfaceprovenance.ResourceBindingInput{ConsumerKind: "implementations", Consumer: "example.com/app/service.New", Constructor: "example.com/app/service.New", ResourceID: contract.ResourceID, PackagePath: contract.Label, ParameterName: name, ParameterPosition: i + 1, InstanceName: "database.primary", Provider: "example.com/app/provider.New", Reason: interfaceprovenance.SelectionExplicit, DeclarationSource: interfaceprovenance.ResourceSource{Module: "example.com/app", Path: "service/new.go", Kind: "implementation-constructor", Line: 6, Column: 6}, BindingSources: []interfaceprovenance.ResourceSource{{Module: "example.com/app", Path: "plystra.yaml", Kind: "resource-binding", Line: 8, Column: 7}}, SelectionSources: sources}
		input.Edges = append(input.Edges, GraphEdge{ID: "depends-on-resource:" + name, Kind: "depends-on-resource", From: input.Nodes[1].ID, To: input.Nodes[2].ID, Reason: "explicit", ParameterName: name, ParameterPosition: i + 1, ResourceBinding: &binding})
	}
	return input
}

func TestResourceInstanceGraphIsDistinctDeterministicAndDefensive(t *testing.T) {
	t.Parallel()
	input := resourceInstanceGraphInput(t)
	result, err := NewGraph(input)
	if err != nil || !result.Valid() {
		t.Fatalf("NewGraph: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	for _, text := range []string{`"resource_instance"`, `"name":"database.unconsumed"`, `"consumer_kind":"implementations"`, `"parameter_name":"Database"`} {
		if !bytes.Contains(before, []byte(text)) {
			t.Fatalf("missing %s", text)
		}
	}
	slices.Reverse(input.Nodes)
	slices.Reverse(input.Edges)
	reordered, err := NewGraph(input)
	if err != nil || !bytes.Equal(before, reordered.Envelope().CanonicalJSON()) {
		t.Fatalf("reordering changed graph: %v", err)
	}
	for _, node := range result.Nodes() {
		if node.ResourceInstance != nil {
			node.ResourceInstance.Name = "changed"
			node.ResourceInstance.SelectionSources[0].Path = "changed.yaml"
		}
	}
	for _, edge := range result.Edges() {
		if edge.ResourceBinding != nil {
			edge.ResourceBinding.BindingSources[0].Path = "changed.yaml"
			edge.ResourceBinding.SelectionSources[0].Path = "changed.yaml"
		}
	}
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) {
		t.Fatal("nested Resource graph records are mutable")
	}
}

func TestResourceInstanceGraphRejectsContradictoryTypedEdges(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*GraphInput){
		"interface-view":        func(i *GraphInput) { i.Type = GraphTypeInterfaces },
		"instance-identity":     func(i *GraphInput) { i.Nodes[2].ResourceInstance.Name = "another" },
		"contract-digest":       func(i *GraphInput) { i.Nodes[2].ResourceInstance.ContractDigest = "sha256:" + strings.Repeat("b", 64) },
		"provider-module":       func(i *GraphInput) { i.Nodes[2].ResourceInstance.ModulePath = "example.com/other" },
		"missing-record":        func(i *GraphInput) { i.Edges[2].ResourceBinding = nil },
		"missing-instance":      func(i *GraphInput) { i.Edges[2].ResourceBinding.InstanceName = "missing" },
		"wrong-position":        func(i *GraphInput) { i.Edges[2].ResourceBinding.ParameterPosition = 3 },
		"wrong-kind":            func(i *GraphInput) { i.Edges[2].ResourceBinding.ConsumerKind = "instances" },
		"wrong-contract":        func(i *GraphInput) { i.Edges[2].ResourceBinding.ResourceID = "data.other/v1" },
		"missing-contract-edge": func(i *GraphInput) { i.Edges = i.Edges[1:] },
		"private-source":        func(i *GraphInput) { i.Edges[2].ResourceBinding.BindingSources[0].Path = "C:/private/secret.yaml" },
	} {
		t.Run(name, func(t *testing.T) {
			input := resourceInstanceGraphInput(t)
			change(&input)
			if _, err := NewGraph(input); !errors.Is(err, ErrGraph) {
				t.Fatalf("invalid graph accepted: %v", err)
			}
		})
	}
}
