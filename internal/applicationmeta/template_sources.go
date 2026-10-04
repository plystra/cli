package applicationmeta

func qualifyTemplateSources(layer Manifest, owner Dependency) Manifest {
	layer = qualifyResourceSources(layer, owner)
	layer.httpExposures = append([]HTTPExposure(nil), layer.httpExposures...)
	for index := range layer.httpExposures {
		layer.httpExposures[index].source = dependencySource(owner, layer.httpExposures[index].source)
	}
	layer.requirements = append([]CapabilityRequirement(nil), layer.requirements...)
	for index := range layer.requirements {
		layer.requirements[index].source = dependencySource(owner, layer.requirements[index].source)
	}
	layer.providerChoices = append([]ProviderChoice(nil), layer.providerChoices...)
	for index := range layer.providerChoices {
		layer.providerChoices[index].source = dependencySource(owner, layer.providerChoices[index].source)
	}
	layer.interfaceRequirements = append([]InterfaceRequirement(nil), layer.interfaceRequirements...)
	for index := range layer.interfaceRequirements {
		layer.interfaceRequirements[index].source = dependencySource(owner, layer.interfaceRequirements[index].source)
	}
	layer.implementationChoices = append([]ImplementationChoice(nil), layer.implementationChoices...)
	for index := range layer.implementationChoices {
		layer.implementationChoices[index].source = dependencySource(owner, layer.implementationChoices[index].source)
	}
	layer.interfacePolicies = append([]InterfacePolicy(nil), layer.interfacePolicies...)
	for index := range layer.interfacePolicies {
		layer.interfacePolicies[index].source = dependencySource(owner, layer.interfacePolicies[index].source)
	}
	layer.aliases = append([]Alias(nil), layer.aliases...)
	for index := range layer.aliases {
		layer.aliases[index].source = dependencySource(owner, layer.aliases[index].source)
	}
	layer.configurations = append([]ConstructorConfiguration(nil), layer.configurations...)
	for index := range layer.configurations {
		layer.configurations[index].source = dependencySource(owner, layer.configurations[index].source)
	}
	return layer
}
