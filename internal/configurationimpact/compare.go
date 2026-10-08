package configurationimpact

import (
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/applicationmeta"
)

// Compare derives ownership consequences from two already validated effective
// manifests. It compares identities and provenance only; configuration bytes
// and Secret references never enter the result.
func Compare(before, after applicationmeta.Manifest, input Input) (Impact, error) {
	result := input
	beforeConfigurations := make(map[string]applicationmeta.ConstructorConfiguration)
	afterConfigurations := make(map[string]applicationmeta.ConstructorConfiguration)
	for _, value := range before.Configurations() {
		beforeConfigurations[value.Constructor().String()] = value
	}
	for _, value := range after.Configurations() {
		afterConfigurations[value.Constructor().String()] = value
	}
	configurationOwners := make([]string, 0, len(beforeConfigurations))
	for owner := range beforeConfigurations {
		configurationOwners = append(configurationOwners, owner)
	}
	sort.Strings(configurationOwners)
	for _, owner := range configurationOwners {
		beforeValue := beforeConfigurations[owner]
		_, retained := afterConfigurations[owner]
		path := "config." + owner
		if retained {
			result.RetainedOwners = append(result.RetainedOwners, RetainedOwner{Owner: owner, Path: path, Reason: "owner-remains-effective"})
			continue
		}
		result.NewlyUnownedPaths = append(result.NewlyUnownedPaths, NewlyUnownedPath{Owner: owner, Path: path, Source: sourceOrDefault(beforeValue.Source())})
		result.DerivedOperations = append(result.DerivedOperations, DerivedOperation{Kind: "configuration", Action: "remove", Owner: owner, Path: path, Source: sourceOrDefault(beforeValue.Source())})
	}

	beforeInstances := make(map[string]applicationmeta.ResourceInstance)
	afterInstances := make(map[string]applicationmeta.ResourceInstance)
	for _, value := range before.ResourceInstances() {
		if value.HasConfiguration() {
			beforeInstances[value.Name()] = value
		}
	}
	for _, value := range after.ResourceInstances() {
		if value.HasConfiguration() {
			afterInstances[value.Name()] = value
		}
	}
	instanceNames := make([]string, 0, len(beforeInstances))
	for name := range beforeInstances {
		instanceNames = append(instanceNames, name)
	}
	sort.Strings(instanceNames)
	for _, name := range instanceNames {
		beforeValue := beforeInstances[name]
		path := fmt.Sprintf("resources.instances[%q].config", name)
		if _, retained := afterInstances[name]; retained {
			result.RetainedOwners = append(result.RetainedOwners, RetainedOwner{Owner: name, Path: path, Reason: "resource-instance-remains-effective"})
			continue
		}
		source := sourceOrDefault(beforeValue.Source())
		result.NewlyUnownedPaths = append(result.NewlyUnownedPaths, NewlyUnownedPath{Owner: name, Path: path, Source: source})
		result.DerivedOperations = append(result.DerivedOperations, DerivedOperation{Kind: "resource_configuration", Action: "remove", Owner: name, Path: path, Source: source})
	}

	beforeBindings := bindingMap(before.ResourceBindings())
	afterBindings := bindingMap(after.ResourceBindings())
	bindingKeys := make([]string, 0, len(beforeBindings))
	for key := range beforeBindings {
		bindingKeys = append(bindingKeys, key)
	}
	sort.Strings(bindingKeys)
	for _, key := range bindingKeys {
		beforeValue := beforeBindings[key]
		if _, retained := afterBindings[key]; retained {
			continue
		}
		path := fmt.Sprintf("resources.bind.%s.%s.%s", beforeValue.Namespace(), beforeValue.Consumer(), beforeValue.ParameterName())
		source := sourceOrDefault(beforeValue.Source())
		result.NewlyUnownedPaths = append(result.NewlyUnownedPaths, NewlyUnownedPath{Owner: beforeValue.Consumer(), Path: path, Source: source})
		result.DerivedOperations = append(result.DerivedOperations, DerivedOperation{Kind: "resource_binding", Action: "remove", Owner: beforeValue.Consumer(), Path: path, Source: source})
	}
	return New(result)
}

func bindingMap(values []applicationmeta.ResourceBinding) map[string]applicationmeta.ResourceBinding {
	result := make(map[string]applicationmeta.ResourceBinding, len(values))
	for _, value := range values {
		key := value.Namespace() + "\x00" + value.Consumer() + "\x00" + value.ParameterName()
		result[key] = value
	}
	return result
}

func sourceOrDefault(source string) string {
	if source == "" {
		return "plystra.yaml"
	}
	return source
}
