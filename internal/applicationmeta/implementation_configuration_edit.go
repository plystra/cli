package applicationmeta

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// ImplementationSelectionIdentity projects one selected-layer choice without
// Config values or document encoding. It is only ownership-planning input, never
// final validation, generated state or executable configuration.
func ImplementationSelectionIdentity(manifest Manifest, id interfaceid.Identifier, constructor constructorsymbol.Symbol, remove, overlay bool) (Manifest, error) {
	if manifest.source == "" || len(manifest.layers) != 0 || id.String() == "" || remove != (constructor.String() == "") {
		return Manifest{}, ErrEditConfiguration
	}
	manifest = WithoutConstructorConfiguration(manifest)
	manifest.implementationChoices = implementationChoicesExcept(manifest.implementationChoices, id)
	manifest.removedImplementationChoices = interfaceRemovalsExcept(manifest.removedImplementationChoices, id)
	source := fmt.Sprintf("%s interfaces.use[%q]", manifest.source, id.String())
	if remove {
		if overlay {
			manifest.removedImplementationChoices = append(manifest.removedImplementationChoices, interfaceRemoval{id: id, source: source})
		}
	} else {
		manifest.implementationChoices = append(manifest.implementationChoices, ImplementationChoice{interfaceID: id, constructor: constructor, source: source})
	}
	slices.SortFunc(manifest.implementationChoices, func(a, b ImplementationChoice) int {
		return strings.Compare(a.interfaceID.String(), b.interfaceID.String())
	})
	slices.SortFunc(manifest.removedImplementationChoices, func(a, b interfaceRemoval) int { return strings.Compare(a.id.String(), b.id.String()) })
	return manifest, nil
}

// ImplementationSelectionEdit changes one exact selected-layer choice.
type ImplementationSelectionEdit struct {
	Interface   interfaceid.Identifier
	Constructor constructorsymbol.Symbol
	Remove      bool
}

func applyImplementationSelection(root *yaml.Node, edit ImplementationSelectionEdit, overlay bool) (bool, error) {
	path := []string{"interfaces", "use", edit.Interface.String()}
	if edit.Remove {
		return removeSelectionLeaf(root, path, overlay)
	}
	previous := mappingChild(mappingChild(mappingChild(root, "interfaces"), "use"), edit.Interface.String())
	if previous != nil && previous.Kind == yaml.ScalarNode && previous.Tag == "!!str" && previous.Value == edit.Constructor.String() {
		return false, nil
	}
	err := setKeyedConfigurationDecision(root, path[:2], path[2], stringYAMLNode(edit.Constructor.String()))
	return true, err
}
