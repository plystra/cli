package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/interfaceprovenance"
)

func validateResourceGraph(nodes []GraphNode, edges []GraphEdge) error {
	var resources []interfaceprovenance.ResourceInput
	var bindings []interfaceprovenance.ResourceBindingInput
	contracts := make(map[string]GraphNode)
	for _, node := range nodes {
		if node.Kind == "resource-contract" {
			contracts[node.ResourceID] = node
		}
		if node.ResourceInstance != nil {
			resources = append(resources, *node.ResourceInstance)
		}
	}
	for _, edge := range edges {
		if edge.ResourceBinding != nil {
			bindings = append(bindings, *edge.ResourceBinding)
		}
	}
	normalResources, normalBindings, err := interfaceprovenance.NormalizeResources(resources, bindings)
	if err != nil {
		return err
	}
	byName := make(map[string]interfaceprovenance.ResourceInput, len(normalResources))
	byParameter := make(map[string]interfaceprovenance.ResourceBindingInput, len(normalBindings))
	for _, resource := range normalResources {
		byName[resource.Name] = resource
	}
	for _, binding := range normalBindings {
		byParameter[binding.ConsumerKind+"\x00"+binding.Consumer+"\x00"+binding.ParameterName] = binding
	}
	for i, node := range nodes {
		if node.ResourceInstance != nil {
			value := byName[node.ResourceInstance.Name]
			nodes[i].ResourceInstance = &value
		}
	}
	for i, edge := range edges {
		if edge.ResourceBinding != nil {
			b := edge.ResourceBinding
			value := byParameter[b.ConsumerKind+"\x00"+b.Consumer+"\x00"+b.ParameterName]
			edges[i].ResourceBinding = &value
		}
	}
	for _, resource := range resources {
		contract, ok := contracts[resource.ResourceID]
		if !ok || contract.ContractDigest != resource.ContractDigest || contract.Label != resource.PackagePath {
			return errors.New("resource instance does not match its visible contract")
		}
		foundDeclaration := false
		for _, source := range contract.Sources {
			if source.Kind != "resource-declaration" {
				continue
			}
			if source != diagnosticjson.Source(resource.ContractSource) {
				return errors.New("resource instance contract source does not match its visible contract")
			}
			foundDeclaration = true
		}
		if !foundDeclaration {
			return errors.New("resource instance contract source is absent from its visible contract")
		}
		count := 0
		for _, edge := range edges {
			if edge.Kind == "instantiates-resource" && edge.From == GraphNodeID("resource-instance", resource.Name) {
				if edge.To != contract.ID {
					return errors.New("resource instance points to an incompatible contract")
				}
				count++
			}
		}
		if count != 1 {
			return errors.New("resource instance must identify its exact contract once")
		}
	}
	return nil
}

func equalResourceGraphRecord[T any](left, right *T) bool {
	if left == nil || right == nil {
		return left == right
	}
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}
