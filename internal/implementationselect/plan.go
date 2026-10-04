package implementationselect

import (
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

func planSelection(inputs applicationresolve.SelectionInputs, id interfaceid.Identifier, target string, constructor constructorsymbol.Symbol) ([]byte, string, error) {
	original := inputs.SelectedSnapshot().Data()
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	var updated []byte
	var err error
	kind := "interface"
	if id.String() != "" {
		set := applicationmeta.SetImplementationChoice
		if overlay {
			set = applicationmeta.SetImplementationChoiceOverlay
		}
		updated, _, err = set(original, id, constructor)
	} else {
		kind = "resource"
		updated, err = selectResourceProvider(inputs, original, target, constructor, overlay)
	}
	if err != nil {
		return nil, kind, err
	}
	if kind == "interface" {
		before, err := inputs.ComposeSelectionCandidate(original)
		if err != nil {
			return nil, kind, err
		}
		candidate, err := inputs.ComposeSelectionCandidate(updated)
		if err != nil {
			return nil, kind, err
		}
		updated, err = cleanupImplementationOwners(inputs, before, candidate, updated, overlay)
		if err != nil {
			return nil, kind, err
		}
	}
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return nil, kind, err
	}
	// Planning checks the complete remaining binding graph before any live write.
	// Generation repeats resolution and owns compilation and rollback validation.
	if err := inputs.ValidateCandidate(candidate); err != nil {
		return nil, kind, err
	}
	return updated, kind, nil
}

func selectResourceProvider(inputs applicationresolve.SelectionInputs, original []byte, target string, constructor constructorsymbol.Symbol, overlay bool) ([]byte, error) {
	instances, previousBindings, err := applicationmeta.ResourceSelectionIdentities(append(inputs.LowerLayers(), inputs.SelectedManifest()))
	if err != nil {
		return nil, err
	}
	var instance applicationmeta.ResourceInstance
	found := false
	for _, value := range instances {
		if value.Name() == target {
			instance, found = value, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: named Resource instance %s", ErrTargetNotFound, target)
	}
	provider, visible := inputs.Declarations().ResourceProviders().BySymbol(constructor)
	previous, previousVisible := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider())
	if !visible || previousVisible && previous.ID() != provider.ID() {
		return nil, fmt.Errorf("%w: %s for %s", ErrProviderIncompatible, constructor, target)
	}
	changedProvider := instance.Provider().String() != "" && instance.Provider() != constructor
	set := applicationmeta.SetResourceProvider
	if overlay {
		set = applicationmeta.SetResourceProviderOverlay
	}
	updated, _, err := set(original, target, constructor, changedProvider, nil)
	if err != nil || !changedProvider || !previousVisible {
		return updated, err
	}
	oldParameters, newParameters := make(map[string]bool), make(map[string]bool)
	for _, dependency := range previous.Dependencies() {
		oldParameters[dependency.ParameterName()] = true
	}
	for _, dependency := range provider.Dependencies() {
		newParameters[dependency.ParameterName()] = true
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, binding := range previousBindings {
		if binding.Namespace() == "instances" && binding.Consumer() == target && oldParameters[binding.ParameterName()] && !newParameters[binding.ParameterName()] {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return removeOwnedPaths(inputs, updated, nil, bindings, overlay)
}

func cleanupImplementationOwners(inputs applicationresolve.SelectionInputs, before, candidate applicationmeta.Composition, updated []byte, overlay bool) ([]byte, error) {
	finalOwners, err := inputs.CandidateOwners(candidate)
	if err != nil {
		return nil, err
	}
	retained := make(map[constructorsymbol.Symbol]bool, len(finalOwners))
	for _, constructor := range finalOwners {
		retained[constructor] = true
	}
	previousOwners := make(map[constructorsymbol.Symbol]bool)
	// An invalid old graph cannot block a repair. Its explicit choices still
	// establish exact owners even when the former reachable closure is unknown.
	for _, choice := range before.Manifest().ImplementationChoices() {
		previousOwners[choice.Constructor()] = true
	}
	owners, _ := inputs.CandidateOwners(before)
	for _, constructor := range owners {
		previousOwners[constructor] = true
	}
	var configurations []applicationmeta.ConstructorConfigurationRemoval
	seen := make(map[constructorsymbol.Symbol]bool)
	layers := append(inputs.LowerLayers(), inputs.SelectedManifest())
	for _, layer := range layers {
		for _, configured := range layer.Configurations() {
			constructor := configured.Constructor()
			if previousOwners[constructor] && !retained[constructor] && !seen[constructor] {
				configurations = append(configurations, applicationmeta.ConstructorConfigurationRemoval{Constructor: constructor})
				seen[constructor] = true
			}
		}
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, binding := range candidate.Manifest().ResourceBindings() {
		if binding.Namespace() != "implementations" {
			continue
		}
		constructor, err := constructorsymbol.Parse(binding.Consumer())
		if err != nil {
			return nil, err
		}
		if previousOwners[constructor] && !retained[constructor] {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return removeOwnedPaths(inputs, updated, configurations, bindings, overlay)
}

func bindingRemoval(binding applicationmeta.ResourceBinding) applicationmeta.ResourceBindingRemoval {
	return applicationmeta.ResourceBindingRemoval{Namespace: binding.Namespace(), Consumer: binding.Consumer(), ParameterName: binding.ParameterName()}
}

func removeOwnedPaths(inputs applicationresolve.SelectionInputs, updated []byte, configurations []applicationmeta.ConstructorConfigurationRemoval, bindings []applicationmeta.ResourceBindingRemoval, overlay bool) ([]byte, error) {
	if len(configurations) == 0 && len(bindings) == 0 {
		return updated, nil
	}
	cleanup := applicationmeta.CleanupSelectionOwnership
	if overlay {
		cleanup = applicationmeta.CleanupSelectionOwnershipOverlay
	}
	// Existing selected-layer exclusions remain authored intent even when a
	// nearer template also suppresses the same path. Probe only local values.
	localConfigurations := make([]applicationmeta.ConstructorConfigurationRemoval, 0, len(configurations))
	for _, removal := range configurations {
		if _, exists := inputs.SelectedManifest().Configuration(removal.Constructor); exists {
			localConfigurations = append(localConfigurations, removal)
		}
	}
	deleted, _, err := cleanup(updated, localConfigurations, bindings)
	if err != nil {
		return nil, err
	}
	// Test the exact local deletions against the lower layers. Only paths that
	// would reappear need tombstones; unrelated baseline values stay unmaterialized.
	lower, err := inputs.ComposeCandidate(deleted)
	if err != nil {
		return nil, err
	}
	finalConfigurations := make([]applicationmeta.ConstructorConfigurationRemoval, 0, len(configurations))
	for _, removal := range configurations {
		_, removal.Tombstone = lower.Manifest().Configuration(removal.Constructor)
		_, local := inputs.SelectedManifest().Configuration(removal.Constructor)
		if removal.Tombstone || local {
			finalConfigurations = append(finalConfigurations, removal)
		}
	}
	for index := range bindings {
		for _, binding := range lower.Manifest().ResourceBindings() {
			if binding.Namespace() == bindings[index].Namespace && binding.Consumer() == bindings[index].Consumer && binding.ParameterName() == bindings[index].ParameterName {
				bindings[index].Tombstone = true
				break
			}
		}
	}
	result, _, err := cleanup(updated, finalConfigurations, bindings)
	return result, err
}
