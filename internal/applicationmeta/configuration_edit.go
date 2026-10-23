package applicationmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ErrEditConfiguration reports an unsafe exact typed configuration edit.
var ErrEditConfiguration = errors.New("edit typed configuration")

// ConfigurationValueEdit is one prepared exact typed field edit. Its value is
// private and can only be applied through EditConfigurationValues.
type ConfigurationValueEdit struct {
	names     []string
	requested *yaml.Node
	remove    bool
}

func (ConfigurationValueEdit) String() string   { return "<redacted-configuration-edit>" }
func (ConfigurationValueEdit) GoString() string { return "<redacted-configuration-edit>" }
func (ConfigurationValueEdit) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-configuration-edit>"))
}

// EditConfigurationValue sets one declared field from JSON, or removes it when
// value is nil. Only fixed non-pointer objects may be traversed. The caller
// proves the owner/schema association and validates the complete final state.
func EditConfigurationValue(data []byte, namespace ConfigurationNamespace, owner, path string, schema implementationinventory.Configuration, value []byte, overlay bool) ([]byte, bool, error) {
	edit, err := PrepareConfigurationValueEdit(namespace, owner, path, schema, value)
	if err != nil {
		return nil, false, err
	}
	return EditConfigurationValues(data, []ConfigurationValueEdit{edit}, overlay)
}

// PrepareConfigurationValueEdit validates a declared field path and typed JSON
// value without reading or editing a configuration document. A nil value removes
// the field; JSON null is a non-nil value.
func PrepareConfigurationValueEdit(namespace ConfigurationNamespace, owner, path string, schema implementationinventory.Configuration, value []byte) (ConfigurationValueEdit, error) {
	invalid := func() (ConfigurationValueEdit, error) { return ConfigurationValueEdit{}, ErrEditConfiguration }
	var names []string
	switch namespace {
	case ConfigurationNamespaceImplementation:
		if _, err := constructorsymbol.Parse(owner); err != nil {
			return invalid()
		}
		names = []string{"config", owner}
	case ConfigurationNamespaceResource:
		if resourcename.Check(owner) != nil {
			return invalid()
		}
		names = []string{"resources", "instances", owner, "config"}
	default:
		return invalid()
	}
	segments := strings.Split(path, ".")
	if len(segments) > 64 || schema.TypeName() == "" {
		return invalid()
	}
	fields := schema.Fields()
	var field implementationinventory.ConfigurationField
	for index, name := range segments {
		var exists bool
		field, exists = lookupConstructorConfigField(fields, name)
		if !exists || index < len(segments)-1 && field.Value().Kind() != implementationinventory.ConfigurationValueObject {
			return invalid()
		}
		fields = field.Value().Fields()
	}
	names = append(names, segments...)
	var requested *yaml.Node
	if value != nil {
		if len(value) > 1<<20 || !json.Valid(value) {
			return invalid()
		}
		var err error
		requested, err = decodeNormalizedConfigNode(value)
		if err != nil {
			return invalid()
		}
		_, err = normalizeConstructorConfigNode(field.Value(), requested, &constructorConfigNormalizeState{}, 0)
		if err != nil {
			return invalid()
		}
	}
	return ConfigurationValueEdit{names: names, requested: requested, remove: value == nil}, nil
}

// EditConfigurationValues applies disjoint prepared edits in memory, checking
// document limits only before and after the complete batch. The caller proves
// owner/schema associations and validates the complete final model.
func EditConfigurationValues(data []byte, edits []ConfigurationValueEdit, overlay bool) ([]byte, bool, error) {
	if err := validateConfigurationEdits(edits); err != nil {
		return nil, false, err
	}
	return editSelectionDocument(data, overlay, ErrEditConfiguration, func(root *yaml.Node) (bool, error) {
		return applyConfigurationEdits(root, edits, overlay)
	})
}

func validateConfigurationEdits(edits []ConfigurationValueEdit) error {
	if len(edits) == 0 {
		return ErrEditConfiguration
	}
	keys := make([]string, len(edits))
	for index, edit := range edits {
		if len(edit.names) == 0 {
			return ErrEditConfiguration
		}
		keys[index] = strings.Join(edit.names, "\x00")
	}
	sort.Strings(keys)
	for index := 1; index < len(keys); index++ {
		if keys[index] == keys[index-1] || strings.HasPrefix(keys[index], keys[index-1]+"\x00") {
			return ErrEditConfiguration
		}
	}
	return nil
}

func applyConfigurationEdits(root *yaml.Node, edits []ConfigurationValueEdit, overlay bool) (bool, error) {
	changed := false
	for _, edit := range edits {
		edited, err := edit.apply(root, overlay)
		if err != nil {
			return false, err
		}
		changed = changed || edited
	}
	return changed, nil
}

func (edit ConfigurationValueEdit) apply(root *yaml.Node, overlay bool) (bool, error) {
	parent := root
	for _, name := range edit.names[:len(edit.names)-1] {
		if isRemovalMapping(parent) && edit.remove {
			return false, nil
		}
		if parent == nil || parent.Kind != yaml.MappingNode || isReservedRemovalMapping(parent) {
			return false, ErrEditConfiguration
		}
		next := mappingChild(parent, name)
		if next == nil {
			if edit.remove && !overlay {
				return false, nil
			}
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(parent, name, next)
			sortYAMLMapping(parent)
		}
		parent = next
	}
	if isRemovalMapping(parent) && edit.remove {
		return false, nil
	}
	if parent.Kind != yaml.MappingNode || isReservedRemovalMapping(parent) {
		return false, ErrEditConfiguration
	}
	key := edit.names[len(edit.names)-1]
	previous := mappingChild(parent, key)
	if edit.remove {
		if overlay {
			if isRemovalMapping(previous) {
				return false, nil
			}
			setMappingValue(parent, key, configurationRemovalYAMLNode())
		} else {
			if previous == nil {
				return false, nil
			}
			removeMappingValue(parent, key)
		}
	} else {
		// Comment preservation mutates the inserted node, not the prepared edit.
		setMappingValue(parent, key, cloneConfigurationEditNode(edit.requested))
	}
	sortYAMLMapping(parent)
	return true, nil
}

func cloneConfigurationEditNode(node *yaml.Node) *yaml.Node {
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		copy.Content[index] = cloneConfigurationEditNode(child)
	}
	return &copy
}
