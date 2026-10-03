package applicationmeta

import (
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

// ValidateRequiredConfiguration checks the final composed objects and implicit
// empty configuration of active constructors. Partial template layers must
// remain composable without this check. Missing values belong to the selected
// current document, not to any one contributing template.
func (c Composition) ValidateRequiredConfiguration(schemas SchemaLookup, active []constructorsymbol.Symbol, selectedPath string) error {
	if !c.Valid() || schemas == nil || selectedPath == "" {
		return fmt.Errorf("%w: required configuration validation needs a composition, schemas, and selected document", ErrCompose)
	}
	constructors := make(map[string]constructorsymbol.Symbol)
	for _, configured := range c.manifest.Configurations() {
		constructors[configured.constructor.String()] = configured.constructor
	}
	for _, constructor := range active {
		constructors[constructor.String()] = constructor
	}
	ordered := make([]string, 0, len(constructors))
	for name := range constructors {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	source := ConfigurationDeclarationSource{modulePath: c.current.modulePath, path: selectedPath, line: 1, column: 1}
	for _, name := range ordered {
		constructor := constructors[name]
		schema, exists := schemas(constructor)
		if !exists {
			continue // Active constructors without a Config parameter need no object.
		}
		var node *yaml.Node
		if configured, present := c.manifest.Configuration(constructor); present {
			var err error
			node, err = decodeNormalizedConfigNode(configured.yaml)
			if err != nil {
				return constructorConfigValueError(constructor, selectedPath, source, nil, ErrConfigurationInvalidValue)
			}
		}
		segments, err := requiredConstructorConfigFields(schema.Fields(), node, &constructorConfigNormalizeState{}, 0)
		if err != nil {
			return constructorConfigValueError(constructor, selectedPath, source, segments, err)
		}
	}
	return nil
}

func requiredConstructorConfigFields(fields []implementationinventory.ConfigurationField, node *yaml.Node, state *constructorConfigNormalizeState, depth int) ([]string, error) {
	var provided map[string]*yaml.Node
	if node != nil {
		var err error
		provided, err = safeConstructorConfigMapping(node)
		if err != nil {
			return nil, err
		}
	}
	for _, field := range fields {
		child := provided[field.Name()]
		if child == nil && field.Required() {
			return []string{field.Name()}, ErrConfigurationRequired
		}
		segments, err := requiredConstructorConfigValue(field.Value(), child, state, depth+1)
		if err != nil {
			return append([]string{field.Name()}, segments...), err
		}
	}
	return nil, nil
}

func requiredConstructorConfigValue(schema implementationinventory.ConfigurationValue, node *yaml.Node, state *constructorConfigNormalizeState, depth int) ([]string, error) {
	state.nodes++
	if depth > maximumConstructorConfigurationDepth || state.nodes > maximumConstructorConfigurationNodes {
		return nil, ErrConfigurationInvalidValue
	}
	if node != nil && isNull(node) {
		return nil, nil // Typed normalization already proved null is permitted.
	}
	switch schema.Kind() {
	case implementationinventory.ConfigurationValueObject:
		return requiredConstructorConfigFields(schema.Fields(), node, state, depth)
	case implementationinventory.ConfigurationValuePointer:
		if node != nil {
			element, _ := schema.Element()
			return requiredConstructorConfigValue(element, node, state, depth+1)
		}
	case implementationinventory.ConfigurationValueList, implementationinventory.ConfigurationValueMap:
		element, _ := schema.Element()
		if node == nil {
			// An absent fixed array still contains zero-valued elements. Checking
			// one proves all identical implicit elements without expanding it.
			if length, fixed := schema.ArrayLength(); fixed && length > 0 {
				_, err := requiredConstructorConfigValue(element, nil, state, depth+1)
				return nil, err
			}
			return nil, nil
		}
		start, step := 0, 1
		if schema.Kind() == implementationinventory.ConfigurationValueMap {
			start, step = 1, 2
		}
		for index := start; index < len(node.Content); index += step {
			if _, err := requiredConstructorConfigValue(element, node.Content[index], state, depth+1); err != nil {
				// Atomic collections report their declared field, never dynamic keys.
				return nil, err
			}
		}
	}
	return nil, nil
}
