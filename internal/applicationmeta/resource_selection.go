package applicationmeta

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ErrSetResourceProvider reports that a selected current-Project document could
// not safely record a Resource provider choice and its proven ownership cleanup.
var ErrSetResourceProvider = errors.New("set Resource provider choice")

// ResourceParameterRemoval removes one exact provider dependency binding.
// Tombstone suppresses an inherited binding; false deletes only the local leaf.
// The caller must prove the parameter is absent from the new provider.
type ResourceParameterRemoval struct {
	ParameterName string
	Tombstone     bool
}

// SetResourceProvider records only resources.instances.<name>.use and the
// caller's exact cleanup decisions. removeConfiguration deletes this instance's
// local config, including a local config tombstone. Provider replacement itself
// suppresses the prior provider's inherited config during composition.
// With removeConfiguration false, even same-provider configuration is untouched.
// The caller owns visibility, compatibility, ownership, and final-state validation.
func SetResourceProvider(data []byte, name string, provider constructorsymbol.Symbol, removeConfiguration bool, removedParameters []ResourceParameterRemoval) ([]byte, bool, error) {
	return setResourceProvider(data, name, provider, removeConfiguration, removedParameters, false)
}

// SetResourceProviderOverlay makes the same exact edits to a sparse environment
// overlay. It does not copy inherited instance configuration or binding values.
func SetResourceProviderOverlay(data []byte, name string, provider constructorsymbol.Symbol, removeConfiguration bool, removedParameters []ResourceParameterRemoval) ([]byte, bool, error) {
	return setResourceProvider(data, name, provider, removeConfiguration, removedParameters, true)
}

func setResourceProvider(data []byte, name string, provider constructorsymbol.Symbol, removeConfiguration bool, removedParameters []ResourceParameterRemoval, overlay bool) ([]byte, bool, error) {
	if resourcename.Check(name) != nil {
		return nil, false, fmt.Errorf("%w: invalid instance name", ErrSetResourceProvider)
	}
	if provider.String() == "" {
		return nil, false, fmt.Errorf("%w: provider constructor is empty", ErrSetResourceProvider)
	}
	bindings := make([]ResourceBindingRemoval, len(removedParameters))
	for index, removal := range removedParameters {
		bindings[index] = ResourceBindingRemoval{Namespace: "instances", Consumer: name, ParameterName: removal.ParameterName, Tombstone: removal.Tombstone}
	}
	_, bindings, err := selectionRemovals(nil, bindings)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSetResourceProvider, err)
	}
	return editSelectionDocument(data, overlay, ErrSetResourceProvider, func(root *yaml.Node) (bool, error) {
		instances, err := ensureMappingPath(root, []string{"resources", "instances"})
		if err != nil {
			return false, err
		}
		instance := mappingChild(instances, name)
		changed := false
		if instance == nil || isRemovalMapping(instance) {
			instance = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(instances, name, instance)
			sortYAMLMapping(instances)
			changed = true
		}
		use := mappingChild(instance, "use")
		if use == nil || use.Value != provider.String() {
			setMappingValue(instance, "use", stringYAMLNode(provider.String()))
			changed = true
		}
		if removeConfiguration && mappingChild(instance, "config") != nil {
			removeMappingValue(instance, "config")
			changed = true
		}
		for _, removal := range bindings {
			removed, err := removeSelectionLeaf(root, resourceBindingRemovalPath(removal), removal.Tombstone)
			if err != nil {
				return false, err
			}
			changed = changed || removed
		}
		return changed, nil
	})
}
