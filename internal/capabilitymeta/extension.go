package capabilitymeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"
)

const maximumCapabilityExtensionDepth = 64

// CapabilityExtension is one immutable namespaced build-time metadata value.
type CapabilityExtension struct {
	namespace string
	valueJSON []byte
}

// Namespace returns the canonical extension namespace.
func (e CapabilityExtension) Namespace() string {
	return e.namespace
}

// ValueJSON returns a defensive copy of the canonical JSON-compatible value.
func (e CapabilityExtension) ValueJSON() []byte {
	return append([]byte(nil), e.valueJSON...)
}

// CapabilityExtensions is immutable build-time metadata sorted by namespace.
type CapabilityExtensions struct {
	values []CapabilityExtension
}

// Values returns a defensive copy in canonical namespace order.
func (e CapabilityExtensions) Values() []CapabilityExtension {
	return append([]CapabilityExtension(nil), e.values...)
}

// Lookup returns one value by exact namespace.
func (e CapabilityExtensions) Lookup(namespace string) (CapabilityExtension, bool) {
	index := sort.Search(len(e.values), func(index int) bool {
		return e.values[index].namespace >= namespace
	})
	if index >= len(e.values) || e.values[index].namespace != namespace {
		return CapabilityExtension{}, false
	}
	return e.values[index], true
}

func parseCapabilityExtensions(node *yaml.Node) (CapabilityExtensions, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return CapabilityExtensions{}, invalid("extensions must be a mapping")
	}

	values := make([]CapabilityExtension, 0, len(node.Content)/2)
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		namespace, err := strictString(keyNode)
		if err != nil || !validExtensionNamespace(namespace) {
			return CapabilityExtensions{}, invalid("extension namespace %q is not canonical lower kebab case", keyNode.Value)
		}
		if _, duplicate := seen[namespace]; duplicate {
			return CapabilityExtensions{}, invalid("extensions contains duplicate namespace %q", namespace)
		}
		if err := validateCapabilityExtensionDepth(valueNode); err != nil {
			return CapabilityExtensions{}, invalid("extensions.%s: %v", namespace, err)
		}

		value, err := decodeJSONValue(valueNode)
		if err != nil {
			return CapabilityExtensions{}, invalid("extensions.%s: %v", namespace, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return CapabilityExtensions{}, invalid("extensions.%s cannot be encoded", namespace)
		}
		seen[namespace] = struct{}{}
		values = append(values, CapabilityExtension{namespace: namespace, valueJSON: encoded})
	}

	sort.Slice(values, func(left, right int) bool {
		return values[left].namespace < values[right].namespace
	})
	return CapabilityExtensions{values: values}, nil
}

func validateCapabilityExtensionDepth(root *yaml.Node) error {
	type pendingNode struct {
		node  *yaml.Node
		depth int
	}
	stack := []pendingNode{{node: root, depth: 1}}
	for len(stack) > 0 {
		last := len(stack) - 1
		pending := stack[last]
		stack = stack[:last]
		if pending.node == nil {
			return errors.New("invalid JSON-compatible value")
		}
		if pending.depth > maximumCapabilityExtensionDepth {
			return fmt.Errorf("value exceeds maximum depth %d", maximumCapabilityExtensionDepth)
		}
		switch pending.node.Kind {
		case yaml.MappingNode:
			for index := 1; index < len(pending.node.Content); index += 2 {
				stack = append(stack, pendingNode{node: pending.node.Content[index], depth: pending.depth + 1})
			}
		case yaml.SequenceNode:
			for _, child := range pending.node.Content {
				stack = append(stack, pendingNode{node: child, depth: pending.depth + 1})
			}
		}
	}
	return nil
}

func decodeJSONValue(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, errors.New("invalid JSON-compatible value")
	}
	switch node.Kind {
	case yaml.MappingNode:
		return decodeJSONObject(node)
	case yaml.SequenceNode:
		values := make([]any, 0, len(node.Content))
		for index, child := range node.Content {
			value, err := decodeJSONValue(child)
			if err != nil {
				return nil, fmt.Errorf("array item %d: %w", index, err)
			}
			values = append(values, value)
		}
		return values, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value, nil
		case "!!bool":
			return strictBool(node)
		case "!!int":
			return strictInteger(node)
		case "!!float":
			return strictNumber(node)
		case "!!null":
			return nil, nil
		default:
			return nil, fmt.Errorf("unsupported scalar tag %q", node.Tag)
		}
	default:
		return nil, errors.New("must be JSON-compatible")
	}
}

func decodeJSONObject(node *yaml.Node) (map[string]any, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, errors.New("must be an object")
	}
	result := make(map[string]any, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictString(node.Content[index])
		if err != nil {
			return nil, errors.New("object keys must be strings")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate object key %q", key)
		}
		value, err := decodeJSONValue(node.Content[index+1])
		if err != nil {
			return nil, fmt.Errorf("object key %q: %w", key, err)
		}
		result[key] = value
	}
	return result, nil
}

func validExtensionNamespace(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}
