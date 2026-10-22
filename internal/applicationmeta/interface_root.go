package applicationmeta

import (
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// ErrSetInterfaceRoot reports an unsafe exact application-root edit.
var ErrSetInterfaceRoot = errors.New("set Interface application root")

// SetInterfaceRoot changes one require or expose root in a complete document.
func SetInterfaceRoot(data []byte, id interfaceid.Identifier, root string, present bool) ([]byte, bool, error) {
	return setInterfaceRoot(data, id, root, present, false)
}

// SetInterfaceRootOverlay changes one root without copying inherited entries.
// Existing complete requirement sets retain their complete-set boundary.
func SetInterfaceRootOverlay(data []byte, id interfaceid.Identifier, root string, present bool) ([]byte, bool, error) {
	return setInterfaceRoot(data, id, root, present, true)
}

func setInterfaceRoot(data []byte, id interfaceid.Identifier, root string, present, overlay bool) ([]byte, bool, error) {
	if id.String() == "" || root != "require" && root != "expose" {
		return nil, false, fmt.Errorf("%w: exact Interface and root kind are required", ErrSetInterfaceRoot)
	}
	return editSelectionDocument(data, overlay, ErrSetInterfaceRoot, func(document *yaml.Node) (bool, error) {
		if root == "expose" {
			if !present {
				return removeSelectionLeaf(document, []string{"http", "expose", id.String()}, overlay)
			}
			current := mappingChild(mappingChild(mappingChild(document, "http"), "expose"), id.String())
			if current != nil && !isRemovalMapping(current) {
				return false, nil
			}
			exposure := HTTPExposure{id: id, transport: HTTPTransportConnect}
			if err := setKeyedConfigurationDecision(document, []string{"http", "expose"}, id.String(), httpExposureYAML(exposure)); err != nil {
				return false, err
			}
			return true, nil
		}

		interfaces := mappingChild(document, "interfaces")
		requirements := mappingChild(interfaces, "require")
		if !overlay || requirements != nil && requirements.Kind == yaml.SequenceNode {
			if requirements == nil {
				if !present {
					return false, nil
				}
				var err error
				interfaces, err = ensureMappingPath(document, []string{"interfaces"})
				if err != nil {
					return false, err
				}
				requirements = interfaceRootSequence()
				setMappingValue(interfaces, "require", requirements)
			}
			if requirements.Kind != yaml.SequenceNode {
				return false, ErrInvalidManifest
			}
			return editInterfaceRootSequence(requirements, id, present), nil
		}

		if requirements == nil {
			var err error
			interfaces, err = ensureMappingPath(document, []string{"interfaces"})
			if err != nil {
				return false, err
			}
			requirements = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(interfaces, "require", requirements)
		}
		addKey, removeKey := "add", "remove"
		if !present {
			addKey, removeKey = removeKey, addKey
		}
		changed := false
		if opposite := mappingChild(requirements, removeKey); opposite != nil {
			changed = editInterfaceRootSequence(opposite, id, false)
		}
		selected := mappingChild(requirements, addKey)
		if selected == nil {
			selected = interfaceRootSequence()
			setMappingValue(requirements, addKey, selected)
		}
		return editInterfaceRootSequence(selected, id, true) || changed, nil
	})
}

func interfaceRootSequence() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
}

func editInterfaceRootSequence(sequence *yaml.Node, id interfaceid.Identifier, present bool) bool {
	for index, item := range sequence.Content {
		if item.Value != id.String() {
			continue
		}
		if present {
			return false
		}
		sequence.Content = append(sequence.Content[:index], sequence.Content[index+1:]...)
		return true
	}
	if !present {
		return false
	}
	sequence.Content = append(sequence.Content, stringYAMLNode(id.String()))
	sort.SliceStable(sequence.Content, func(left, right int) bool { return sequence.Content[left].Value < sequence.Content[right].Value })
	return true
}
