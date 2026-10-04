package applicationmeta

import (
	"slices"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

// WithoutConstructorConfiguration returns an identity-only planning copy of the
// manifest and its stored layers. It removes Implementation Config entries and
// removals, plus Resource instance Config values and removals. All other intent,
// declaration sources, and provider-selection sources are retained.
//
// Use this only for ownership/identity planning before a selection repair. Never
// use the result for final configuration validation, generation, or runtime input;
// those paths must compose and validate the original, fully configured layers.
func WithoutConstructorConfiguration(manifest Manifest) Manifest {
	manifest.configurations = nil
	manifest.removedConfigurations = nil
	manifest.resourceInstances = withoutResourceConfiguration(manifest.resourceInstances)
	manifest.removedResourceInstances = withoutResourceConfiguration(manifest.removedResourceInstances)
	manifest.layers = slices.Clone(manifest.layers)
	for index := range manifest.layers {
		manifest.layers[index] = WithoutConstructorConfiguration(manifest.layers[index])
	}
	return manifest
}

func withoutResourceConfiguration(instances []ResourceInstance) []ResourceInstance {
	instances = slices.Clone(instances)
	for index := range instances {
		instances[index].configuration = nil
		instances[index].yaml = nil
		instances[index].unboundConfiguration = false
	}
	return instances
}

// ResourceSelectionIdentities folds only Resource identities and exact binding
// addresses through the ordinary layer algebra. Unlike a finalized composition,
// it preserves existing entries with no provider so selection can repair them.
// Config is excluded; callers must compose and validate the real final document.
func ResourceSelectionIdentities(layers []Manifest) ([]ResourceInstance, []ResourceBinding, error) {
	var state Manifest
	noConfiguration := func(ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, manifest := range layers {
		for _, layer := range manifestLayers(manifest) {
			layer = WithoutConstructorConfiguration(layer)
			var err error
			state.resourceInstances, state.removedResourceInstances, state.resourceBindings, state.removedResourceBindings, err = overlayResources(state, layer, noConfiguration)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return state.ResourceInstances(), state.ResourceBindings(), nil
}
