package applicationmeta

import (
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/resourcename"
)

// ResourceSelectionIdentity projects one selected-layer instance change without
// Config or document encoding. It is ownership input, never final validation.
func ResourceSelectionIdentity(manifest Manifest, name string, provider constructorsymbol.Symbol, remove, overlay bool) (Manifest, error) {
	if manifest.source == "" || len(manifest.layers) != 0 || resourcename.Check(name) != nil || remove != (provider.String() == "") {
		return Manifest{}, ErrEditConfiguration
	}
	manifest = WithoutConstructorConfiguration(manifest)
	instances := make(map[string]ResourceInstance)
	removals := make(map[string]ResourceInstance)
	for _, instance := range manifest.resourceInstances {
		if instance.name != name {
			instances[instance.name] = instance
		}
	}
	for _, instance := range manifest.removedResourceInstances {
		if instance.name != name {
			removals[instance.name] = instance
		}
	}
	source := manifest.source + " " + resourceInstancePath(name)
	instance := ResourceInstance{name: name, source: source, declarationSource: resourceLocation(manifest.source, nil)}
	if remove {
		if overlay {
			removals[name] = instance
		}
	} else {
		instance.provider, instance.hasProvider = provider, true
		instance.providerSource = source + ".use"
		instance.providerDeclarationSource = instance.declarationSource
		instances[name] = instance
	}
	manifest.resourceInstances, manifest.removedResourceInstances = sortedResourceInstances(instances), sortedResourceInstances(removals)
	return manifest, nil
}

// ResourceSelectionEdit records an exact instance/provider change. The caller
// proves ownership and validates the complete candidate. DiscardConfiguration
// applies only to provider changes, never to an unchanged or first provider.
type ResourceSelectionEdit struct {
	Instance             string
	Provider             constructorsymbol.Symbol
	Remove               bool
	DiscardConfiguration bool
}
