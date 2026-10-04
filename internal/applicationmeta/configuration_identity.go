package applicationmeta

import "slices"

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
