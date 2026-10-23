package applicationmeta

import (
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
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

// ResourceConfigurationEdit combines one provider/instance change, proven exact
// binding cleanup and disjoint fields. The caller proves ownership and validates
// the complete candidate. DiscardConfiguration applies only to provider changes.
type ResourceConfigurationEdit struct {
	Instance             string
	Provider             constructorsymbol.Symbol
	Remove               bool
	DiscardConfiguration bool
	Bindings             []ResourceBindingRemoval
	Values               []ConfigurationValueEdit
}

// EditResourceConfiguration checks document bounds only on input and complete
// output; no provider-only or cleanup-only intermediate is encoded.
func EditResourceConfiguration(data []byte, edit ResourceConfigurationEdit, overlay bool) ([]byte, bool, error) {
	if resourcename.Check(edit.Instance) != nil || edit.Remove != (edit.Provider.String() == "") || edit.Remove && edit.DiscardConfiguration || validateConfigurationEdits(edit.Values) != nil {
		return nil, false, ErrEditConfiguration
	}
	_, bindings, err := selectionRemovals(nil, edit.Bindings)
	if err != nil {
		return nil, false, ErrEditConfiguration
	}
	for _, value := range edit.Values {
		if edit.Remove && len(value.names) >= 3 && value.names[0] == "resources" && value.names[1] == "instances" && value.names[2] == edit.Instance {
			return nil, false, ErrEditConfiguration
		}
	}
	return editSelectionDocument(data, overlay, ErrEditConfiguration, func(root *yaml.Node) (bool, error) {
		var changed bool
		var err error
		if edit.Remove {
			changed, err = removeSelectionLeaf(root, []string{"resources", "instances", edit.Instance}, overlay)
		} else {
			changed, err = applyResourceProvider(root, edit.Instance, edit.Provider, edit.DiscardConfiguration)
		}
		if err != nil {
			return false, err
		}
		cleaned, err := applySelectionRemovals(root, nil, bindings)
		if err != nil {
			return false, err
		}
		configured, err := applyConfigurationEdits(root, edit.Values, overlay)
		return changed || cleaned || configured, err
	})
}
