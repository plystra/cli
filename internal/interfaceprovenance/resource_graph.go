package interfaceprovenance

import (
	"fmt"
	"strconv"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceinventory"
)

// ResourceInputs projects the already resolved graph without selecting or
// activating anything. Only canonical contract identity and declaration facts
// enter the result; configuration contents are deliberately inaccessible here.
func ResourceInputs(graph constructorgraph.Graph, catalog interfaceinventory.ResourceIndex) ([]ResourceInput, []ResourceBindingInput, error) {
	contracts := make(map[string]interfaceinventory.Resource)
	for _, contract := range catalog.Resources() {
		contracts[contract.ID()] = contract
	}
	var resources []ResourceInput
	var bindings []ResourceBindingInput
	appendBindings := func(dependencies []constructorgraph.ResourceDependency) {
		for _, d := range dependencies {
			kind := "implementation-constructor"
			if d.Namespace() == constructorgraph.ResourceConsumerInstance {
				kind = "resource-provider-constructor"
			}
			bindings = append(bindings, ResourceBindingInput{
				ConsumerKind: string(d.Namespace()), Consumer: d.Consumer(), Constructor: d.Constructor().String(),
				ResourceID: d.ResourceID().String(), PackagePath: d.PackagePath(), ParameterName: d.ParameterName(), ParameterPosition: d.ParameterPosition(),
				InstanceName: d.InstanceName(), Provider: d.Provider().String(), Reason: SelectionReason(d.Reason()),
				DeclarationSource: resourceSource(d.DeclarationSource(), kind), BindingSources: resourceSources(d.Sources(), "resource-binding"),
				SelectionSources: resourceSources(d.SelectionSources(), "resource-selection"), ConsumerSelectionSources: resourceSources(d.ConsumerSelectionSources(), "resource-selection"),
			})
		}
	}
	for i, node := range graph.ResourceConstructionOrder() {
		contract, ok := contracts[node.ResourceID().String()]
		if !ok {
			return nil, nil, fmt.Errorf("%w: selected Resource contract is absent", ErrInvalid)
		}
		provider := node.Provider()
		position := provider.Declaration().Position()
		contractPosition := contract.Declaration().Position()
		resource := ResourceInput{Name: node.Name(), ResourceID: contract.ID(), PackagePath: contract.PackagePath(), ContractDigest: contract.ContractDigest(),
			ContractSource: ResourceSource{Module: contract.ModulePath(), Path: contract.SourcePath(), Kind: "resource-declaration", Line: contractPosition.Line, Column: contractPosition.Column},
			Provider:       provider.Symbol().String(), ModulePath: provider.ModulePath(), ModuleVersion: provider.ModuleVersion(), ConstructionOrder: i + 1,
			DeclarationSource: ResourceSource{Module: provider.ModulePath(), Path: position.Path, Kind: "resource-provider-constructor", Line: position.Line, Column: position.Column},
			SelectionSources:  resourceSources(node.Sources(), "resource-selection")}
		if resource.ModuleVersion == "" {
			resource.ModuleVersion = "local"
		}
		if _, ok := provider.Configuration(); ok {
			resource.ConfigurationOwner = "resources.instances[" + strconv.Quote(node.Name()) + "].config"
		}
		resources = append(resources, resource)
		appendBindings(node.Dependencies())
	}
	for _, node := range graph.ConstructionOrder() {
		appendBindings(graph.ResourceDependencies(node.Symbol()))
	}
	return NormalizeResources(resources, bindings)
}

func resourceSource(source constructorgraph.ResourceSource, kind string) ResourceSource {
	return ResourceSource{Module: source.ModulePath, Path: source.Path, Kind: kind, Line: source.Line, Column: source.Column}
}

func resourceSources(sources []constructorgraph.ResourceSource, kind string) []ResourceSource {
	result := make([]ResourceSource, len(sources))
	for i, source := range sources {
		result[i] = resourceSource(source, kind)
	}
	return result
}
