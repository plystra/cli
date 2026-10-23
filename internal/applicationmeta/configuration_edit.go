package applicationmeta

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ErrEditConfiguration reports an unsafe exact typed configuration edit.
var ErrEditConfiguration = errors.New("edit typed configuration")

// EditConfigurationValue sets one declared field from JSON, or removes it when
// value is nil. Only fixed non-pointer objects may be traversed. The caller
// proves the owner/schema association and validates the complete final state.
func EditConfigurationValue(data []byte, namespace ConfigurationNamespace, owner, path string, schema implementationinventory.Configuration, value []byte, overlay bool) ([]byte, bool, error) {
	invalid := func() ([]byte, bool, error) { return nil, false, ErrEditConfiguration }
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
	return editSelectionDocument(data, overlay, ErrEditConfiguration, func(root *yaml.Node) (bool, error) {
		parent := root
		for _, name := range names[:len(names)-1] {
			if isRemovalMapping(parent) && value == nil {
				return false, nil
			}
			if parent == nil || parent.Kind != yaml.MappingNode || isReservedRemovalMapping(parent) {
				return false, ErrEditConfiguration
			}
			next := mappingChild(parent, name)
			if next == nil {
				if value == nil && !overlay {
					return false, nil
				}
				next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				setMappingValue(parent, name, next)
				sortYAMLMapping(parent)
			}
			parent = next
		}
		if isRemovalMapping(parent) && value == nil {
			return false, nil
		}
		if parent.Kind != yaml.MappingNode || isReservedRemovalMapping(parent) {
			return false, ErrEditConfiguration
		}
		key := names[len(names)-1]
		previous := mappingChild(parent, key)
		if value == nil {
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
			setMappingValue(parent, key, requested)
		}
		sortYAMLMapping(parent)
		return true, nil
	})
}
