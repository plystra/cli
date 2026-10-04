package applicationmeta

import (
	"fmt"
	"sort"
	"strings"
)

// CurrentLayers returns defensive selected-current-project layers in authored
// order. Inherited Resource providers are attached as schema context, without
// becoming authored use decisions or copying lower configuration into a delta.
// Use these layers for typed decisions and digests after Compose.
func (c Composition) CurrentLayers() []Manifest {
	if !c.Valid() {
		return nil
	}
	return append([]Manifest(nil), c.currentLayers...)
}

func bindResourceProviders(layer, lower Manifest) Manifest {
	providers := make(map[string]ResourceInstance, len(lower.resourceInstances))
	for _, instance := range lower.resourceInstances {
		providers[instance.name] = instance
	}
	layer.resourceInstances = layer.ResourceInstances()
	for index := range layer.resourceInstances {
		instance := &layer.resourceInstances[index]
		if !instance.hasProvider {
			instance.provider = providers[instance.name].provider
			instance.providerSource = providers[instance.name].providerSource
			instance.providerDeclarationSource = providers[instance.name].providerDeclarationSource
		}
		// A later layer may remove or replace this incomplete entry. Until then
		// no schema can make its configuration fields safe for public evidence.
		instance.unboundConfiguration = instance.provider.String() == ""
	}
	return layer
}

func overlayResources(base, overlay Manifest, schemas SchemaLookup) ([]ResourceInstance, []ResourceInstance, []ResourceBinding, []ResourceBinding, error) {
	values := make(map[string]ResourceInstance)
	removals := make(map[string]ResourceInstance)
	for _, value := range base.resourceInstances {
		values[value.name] = value
	}
	for _, value := range base.removedResourceInstances {
		removals[value.name] = value
	}
	for _, upper := range overlay.resourceInstances {
		lower, exists := values[upper.name]
		if !upper.hasProvider {
			upper.provider = lower.provider
			upper.providerSource = lower.providerSource
			upper.providerDeclarationSource = lower.providerDeclarationSource
		}
		if exists && lower.provider == upper.provider {
			upper.configuration = append(append([]resourceConfigurationLayer(nil), lower.configuration...), upper.configuration...)
			upper.hasProvider = upper.hasProvider || lower.hasProvider
			if len(upper.configuration) != 0 {
				upper.yaml = upper.configuration[len(upper.configuration)-1].yaml
			}
		}
		if upper.provider.String() != "" {
			var err error
			upper, err = normalizeResourceConfiguration(upper, schemas, true)
			if err != nil {
				return nil, nil, nil, nil, err
			}
		}
		values[upper.name] = upper
		delete(removals, upper.name)
	}
	for _, value := range overlay.removedResourceInstances {
		delete(values, value.name)
		removals[value.name] = value
	}
	bindings := make(map[string]ResourceBinding)
	removedBindings := make(map[string]ResourceBinding)
	for _, binding := range base.resourceBindings {
		bindings[resourceBindingPath(binding)] = binding
	}
	for _, binding := range base.removedResourceBindings {
		removedBindings[resourceBindingPath(binding)] = binding
	}
	for _, binding := range overlay.resourceBindings {
		bindings[resourceBindingPath(binding)] = binding
		delete(removedBindings, resourceBindingPath(binding))
	}
	for _, binding := range overlay.removedResourceBindings {
		delete(bindings, resourceBindingPath(binding))
		removedBindings[resourceBindingPath(binding)] = binding
	}
	return sortedResourceInstances(values), sortedResourceInstances(removals), sortedResourceBindings(bindings), sortedResourceBindings(removedBindings), nil
}

func sortedResourceInstances(values map[string]ResourceInstance) []ResourceInstance {
	result := make([]ResourceInstance, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func sortedResourceBindings(values map[string]ResourceBinding) []ResourceBinding {
	result := make([]ResourceBinding, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return resourceBindingPath(result[i]) < resourceBindingPath(result[j]) })
	return result
}

func resourceConfigDecisions(instance ResourceInstance, schemas SchemaLookup) (map[string]constructorConfigDecision, error) {
	selected := make(map[string]constructorConfigDecision)
	for _, layer := range instance.configuration {
		var decisions []constructorConfigDecision
		if layer.remove {
			if _, exists := schemas(instance.provider); !exists {
				return nil, resourceConfigurationError(instance, layer.declarationSource, ErrConfigurationSchema)
			}
			decision := newConstructorConfigDecision(instance.provider, nil, constructorConfigRemoval, "", nil, layer.source)
			decision.declarationSource = layer.declarationSource
			decisions = []constructorConfigDecision{decision}
		} else {
			var err error
			decisions, err = normalizeConstructorConfigDecisions(ConstructorConfiguration{constructor: instance.provider, source: layer.source, declarationSource: layer.declarationSource, yaml: layer.yaml}, schemas)
			if err != nil {
				return nil, resourceConfigurationError(instance, layer.declarationSource, err)
			}
			for index := range decisions {
				decisions[index].declarationSource = layer.declarationSource
			}
		}
		for _, decision := range decisions {
			path := constructorConfigPath(decision.constructor, decision.segments)
			if decision.kind != constructorConfigObject {
				for candidate := range selected {
					if strings.HasPrefix(candidate, path+"[") {
						delete(selected, candidate)
					}
				}
			}
			selected[path] = decision
		}
	}
	return selected, nil
}

func normalizeResourceConfiguration(instance ResourceInstance, schemas SchemaLookup, preserveRemoval bool) (ResourceInstance, error) {
	if len(instance.configuration) == 0 {
		return instance, nil
	}
	decisions, err := resourceConfigDecisions(instance, schemas)
	if err != nil {
		return ResourceInstance{}, err
	}
	var configs []ConstructorConfiguration
	if preserveRemoval {
		configs, _, err = renderConstructorConfigurationLayer(decisions)
	} else {
		configs, err = renderConstructorConfigurations(decisions)
	}
	if err != nil {
		return ResourceInstance{}, resourceConfigurationError(instance, instance.declarationSource, ErrConfigurationInvalidValue)
	}
	instance.yaml = nil
	if len(configs) != 0 {
		instance.yaml = configs[0].yaml
	}
	if !preserveRemoval {
		instance.configuration = nil
		if len(configs) != 0 {
			instance.configuration = []resourceConfigurationLayer{{yaml: configs[0].yaml, source: configs[0].source, declarationSource: configs[0].declarationSource}}
		}
	}
	return instance, nil
}

func finalizeResources(manifest Manifest, schemas SchemaLookup) (Manifest, error) {
	manifest.resourceInstances = manifest.ResourceInstances()
	for index, instance := range manifest.resourceInstances {
		if instance.provider.String() == "" {
			return Manifest{}, &ResourceMetadataError{source: instance.declarationSource, field: resourceInstancePath(instance.name) + ".use", rule: "requires a provider after composition"}
		}
		normalized, err := normalizeResourceConfiguration(instance, schemas, false)
		if err != nil {
			return Manifest{}, err
		}
		manifest.resourceInstances[index] = normalized
	}
	return manifest, nil
}

func resourceConfigurationDecisions(manifest Manifest, schemas SchemaLookup, opaque bool) ([]ConfigurationDecision, error) {
	var result []ConfigurationDecision
	add := func(path, value string, summary ConfigurationDecisionSummary, removed bool, source string) {
		result = append(result, ConfigurationDecision{path: path, digest: digestStrings("resource.declaration/v1", path, value), summary: summary, removed: removed, source: source, dependencyComposable: true})
	}
	for _, instance := range manifest.resourceInstances {
		path := resourceInstancePath(instance.name)
		add(path, "object", ConfigurationSummaryObject, false, instance.source)
		if instance.hasProvider {
			add(path+".use", instance.provider.String(), ConfigurationSummaryProvider, false, instance.providerSource)
		}
		decisions, err := resourceConfigDecisions(instance, schemas)
		if err != nil {
			if !opaque && !(instance.unboundConfiguration && instance.provider.String() == "") {
				return nil, err
			}
			if len(instance.configuration) != 0 {
				layer := instance.configuration[len(instance.configuration)-1]
				if layer.remove {
					add(path+".config", "removed", ConfigurationSummaryRemoval, true, layer.source)
				} else {
					add(path+".config", "unvalidated-object", ConfigurationSummaryObject, false, layer.source)
				}
			}
			continue
		}
		for _, decision := range decisions {
			evidence := constructorConfigurationDecision(decision)
			evidence.path = constructorConfigDecisionSource(path+".config", decision.segments)
			evidence.digest = digestStrings("resource.config/v1", instance.provider.String(), evidence.path, evidence.digest)
			result = append(result, evidence)
		}
	}
	for _, instance := range manifest.removedResourceInstances {
		add(resourceInstancePath(instance.name), "removed", ConfigurationSummaryRemoval, true, instance.source)
	}
	for _, binding := range manifest.resourceBindings {
		add(resourceBindingPath(binding), binding.target, ConfigurationSummaryString, false, binding.source)
	}
	for _, binding := range manifest.removedResourceBindings {
		add(resourceBindingPath(binding), "removed", ConfigurationSummaryRemoval, true, binding.source)
	}
	return result, nil
}

func clearReplacedResourceSources(active map[string]Provenance, lower, upper Manifest) {
	previous := make(map[string]ResourceInstance, len(lower.resourceInstances))
	for _, instance := range lower.resourceInstances {
		previous[instance.name] = instance
	}
	for _, instance := range upper.resourceInstances {
		if instance.hasProvider && previous[instance.name].provider != instance.provider {
			prefix := resourceInstancePath(instance.name)
			for path := range active {
				if path == prefix || strings.HasPrefix(path, prefix+".") {
					delete(active, path)
				}
			}
		}
	}
}

func qualifyResourceSources(layer Manifest, owner Dependency) Manifest {
	instances := func(values []ResourceInstance) []ResourceInstance {
		values = append([]ResourceInstance(nil), values...)
		for index := range values {
			values[index].source = dependencySource(owner, values[index].source)
			if values[index].hasProvider {
				values[index].providerSource = dependencySource(owner, values[index].providerSource)
			}
			values[index].configuration = append([]resourceConfigurationLayer(nil), values[index].configuration...)
			for config := range values[index].configuration {
				values[index].configuration[config].source = dependencySource(owner, values[index].configuration[config].source)
			}
		}
		return values
	}
	bindings := func(values []ResourceBinding) []ResourceBinding {
		values = append([]ResourceBinding(nil), values...)
		for index := range values {
			values[index].source = dependencySource(owner, values[index].source)
		}
		return values
	}
	layer.resourceInstances, layer.removedResourceInstances = instances(layer.resourceInstances), instances(layer.removedResourceInstances)
	layer.resourceBindings, layer.removedResourceBindings = bindings(layer.resourceBindings), bindings(layer.removedResourceBindings)
	return layer
}

func (c Composition) validateRequiredResourceConfiguration(schemas SchemaLookup, source ConfigurationDeclarationSource) error {
	for _, instance := range c.manifest.resourceInstances {
		schema, exists := schemas(instance.provider)
		if !exists {
			continue
		}
		data := instance.ConfigurationYAML()
		if len(data) == 0 {
			data = []byte("{}\n")
		}
		node, err := decodeNormalizedConfigNode(data)
		if err != nil {
			return resourceConfigurationError(instance, source, ErrConfigurationInvalidValue)
		}
		segments, err := requiredConstructorConfigFields(schema.Fields(), node, &constructorConfigNormalizeState{}, 0)
		if err != nil {
			return resourceConfigurationError(instance, source, constructorConfigValueError(instance.provider, fmt.Sprintf("%s %s.config", source.Path(), resourceInstancePath(instance.name)), source, segments, err))
		}
	}
	return nil
}
