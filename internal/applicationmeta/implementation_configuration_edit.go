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

// ImplementationConfigurationEdit combines one choice, proven ownership cleanup,
// and disjoint typed field edits. The caller proves final ownership and validates
// the full composed Config and graph; no intermediate document is authoritative.
type ImplementationConfigurationEdit struct {
	Interface      interfaceid.Identifier
	Constructor    constructorsymbol.Symbol
	Remove         bool
	Configurations []ConstructorConfigurationRemoval
	Bindings       []ResourceBindingRemoval
	Values         []ConfigurationValueEdit
}

// EditImplementationConfiguration checks bounds only on input and complete
// output, preserving comments, exclusions and unrelated authored intent.
func EditImplementationConfiguration(data []byte, edit ImplementationConfigurationEdit, overlay bool) ([]byte, bool, error) {
	if edit.Interface.String() == "" || edit.Remove != (edit.Constructor.String() == "") || validateConfigurationEdits(edit.Values) != nil {
		return nil, false, ErrEditConfiguration
	}
	configurations, bindings, err := selectionRemovals(edit.Configurations, edit.Bindings)
	if err != nil {
		return nil, false, ErrEditConfiguration
	}
	for _, removal := range configurations {
		for _, value := range edit.Values {
			if len(value.names) >= 2 && value.names[0] == "config" && value.names[1] == removal.Constructor.String() {
				return nil, false, ErrEditConfiguration
			}
		}
	}
	return editSelectionDocument(data, overlay, ErrEditConfiguration, func(root *yaml.Node) (bool, error) {
		path := []string{"interfaces", "use", edit.Interface.String()}
		choiceChanged := false
		if edit.Remove {
			choiceChanged, err = removeSelectionLeaf(root, path, overlay)
		} else {
			previous := mappingChild(mappingChild(mappingChild(root, "interfaces"), "use"), edit.Interface.String())
			if previous == nil || previous.Kind != yaml.ScalarNode || previous.Tag != "!!str" || previous.Value != edit.Constructor.String() {
				err = setKeyedConfigurationDecision(root, path[:2], path[2], stringYAMLNode(edit.Constructor.String()))
				choiceChanged = true
			}
		}
		if err != nil {
			return false, err
		}
		cleaned, err := applySelectionRemovals(root, configurations, bindings)
		if err != nil {
			return false, err
		}
		configured, err := applyConfigurationEdits(root, edit.Values, overlay)
		return choiceChanged || cleaned || configured, err
	})
}
