package applicationmeta

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// InterfaceRequirement is one explicit canonical Interface requirement.
type InterfaceRequirement struct {
	id     interfaceid.Identifier
	source string
}

// ID returns the exact canonical Interface ID declared under
// interfaces.require.
func (r InterfaceRequirement) ID() interfaceid.Identifier { return r.id }

// Source returns stable configuration-path provenance for diagnostics.
func (r InterfaceRequirement) Source() string { return r.source }

// ImplementationChoice is one explicit canonical Interface-to-constructor
// selection.
type ImplementationChoice struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	source      string
}

// InterfaceID returns the exact canonical Interface selected under
// interfaces.use.
func (c ImplementationChoice) InterfaceID() interfaceid.Identifier { return c.interfaceID }

// Constructor returns the selected fully qualified Implementation constructor
// symbol.
func (c ImplementationChoice) Constructor() constructorsymbol.Symbol { return c.constructor }

// Source returns stable configuration-path provenance for diagnostics.
func (c ImplementationChoice) Source() string { return c.source }

// InterfacePolicy is one closed invocation policy attached to an exact
// non-intrinsic Interface. Runtime compilation consumes this typed value; the
// Kernel never parses its YAML source.
type InterfacePolicy struct {
	interfaceID interfaceid.Identifier
	timeout     time.Duration
	source      string
}

// InterfaceID returns the exact canonical Interface governed by the policy.
func (p InterfacePolicy) InterfaceID() interfaceid.Identifier { return p.interfaceID }

// Timeout returns the normalized positive invocation timeout.
func (p InterfacePolicy) Timeout() time.Duration { return p.timeout }

// Source returns stable configuration-field provenance for diagnostics.
func (p InterfacePolicy) Source() string { return p.source }

// interfaceRemoval is one typed null or sparse-set tombstone retained until
// schema-aware overlay or dependency composition applies it.
type interfaceRemoval struct {
	id     interfaceid.Identifier
	source string
}

func parseInterfaces(node *yaml.Node) ([]InterfaceRequirement, []interfaceRemoval, []ImplementationChoice, []interfaceRemoval, []InterfacePolicy, []interfaceRemoval, error) {
	if node == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}
	values, err := mapping(node, "interfaces")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "require", "use", "policies":
		default:
			return nil, nil, nil, nil, nil, nil, invalid("interfaces contains unknown key %q", key)
		}
	}
	requirements, removedRequirements, err := parseInterfaceRequirements(values["require"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	choices, removedChoices, err := parseImplementationChoices(values["use"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	policies, removedPolicies, err := parseInterfacePolicies(values["policies"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	return requirements, removedRequirements, choices, removedChoices, policies, removedPolicies, nil
}

func parseInterfaceRequirements(node *yaml.Node) ([]InterfaceRequirement, []interfaceRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	return parseInterfaceSet(node, "interfaces.require", func(id interfaceid.Identifier, source string) InterfaceRequirement {
		return InterfaceRequirement{id: id, source: source}
	})
}

func parseInterfaceSet[T any](node *yaml.Node, path string, makeValue func(interfaceid.Identifier, string) T) ([]T, []interfaceRemoval, error) {
	var addNode, removeNode *yaml.Node
	addPath := path
	removePath := path + ".remove"
	switch node.Kind {
	case yaml.SequenceNode:
		addNode = node
	case yaml.MappingNode:
		values, err := mapping(node, path)
		if err != nil {
			return nil, nil, err
		}
		for _, key := range sortedNodeKeys(values) {
			switch key {
			case "add", "remove":
			default:
				return nil, nil, invalid("%s contains unknown sparse-edit key %q", path, key)
			}
		}
		addNode = values["add"]
		removeNode = values["remove"]
		addPath = path + ".add"
	default:
		return nil, nil, invalid("%s must be a sequence or sparse {add, remove} mapping of canonical Interface IDs", path)
	}

	adds, err := parseInterfaceIDs(addNode, addPath)
	if err != nil {
		return nil, nil, err
	}
	removes, err := parseInterfaceIDs(removeNode, removePath)
	if err != nil {
		return nil, nil, err
	}
	addSet := make(map[interfaceid.Identifier]struct{}, len(adds))
	for _, id := range adds {
		addSet[id] = struct{}{}
	}
	for _, id := range removes {
		if _, ambiguous := addSet[id]; ambiguous {
			return nil, nil, invalid("%s cannot both add and remove Interface %q", path, id.String())
		}
	}

	values := make([]T, len(adds))
	for index, id := range adds {
		values[index] = makeValue(id, fmt.Sprintf("plystra.yaml %s[%q]", addPath, id.String()))
	}
	removals := make([]interfaceRemoval, len(removes))
	for index, id := range removes {
		removals[index] = interfaceRemoval{id: id, source: fmt.Sprintf("plystra.yaml %s[%q]", removePath, id.String())}
	}
	return values, removals, nil
}

func parseInterfaceIDs(node *yaml.Node, path string) ([]interfaceid.Identifier, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalid("%s must be a sequence of canonical Interface IDs", path)
	}
	values := make([]interfaceid.Identifier, 0, len(node.Content))
	seen := make(map[interfaceid.Identifier]int, len(node.Content))
	for index, item := range node.Content {
		value, err := strictString(item)
		if err != nil || value == "" {
			return nil, invalid("%s[%d] must be a canonical Interface ID string", path, index)
		}
		id, err := interfaceid.Parse(value)
		if err != nil {
			return nil, invalid("%s[%d] %q is not a canonical Interface ID", path, index, value)
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, invalid("%s[%d] duplicates Interface %q from %s[%d]", path, index, id.String(), path, previous)
		}
		seen[id] = index
		values = append(values, id)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].String() < values[right].String() })
	return values, nil
}

func parseImplementationChoices(node *yaml.Node) ([]ImplementationChoice, []interfaceRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	values, err := mapping(node, "interfaces.use")
	if err != nil {
		return nil, nil, err
	}
	choices := make([]ImplementationChoice, 0, len(values))
	removals := make([]interfaceRemoval, 0, len(values))
	for _, value := range sortedNodeKeys(values) {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			return nil, nil, invalid("interfaces.use key %q is not a canonical Interface ID", value)
		}
		if strings.HasPrefix(identifier.Name(), "kernel.") {
			return nil, nil, invalid("interfaces.use key %q selects an intrinsic kernel.* Interface", value)
		}
		source := fmt.Sprintf("plystra.yaml interfaces.use[%q]", identifier.String())
		if isNull(values[value]) {
			removals = append(removals, interfaceRemoval{id: identifier, source: source})
			continue
		}
		selected, err := strictString(values[value])
		if err != nil {
			return nil, nil, invalid("interfaces.use[%q] must be a fully qualified constructor symbol or null", value)
		}
		constructor, err := constructorsymbol.Parse(selected)
		if err != nil {
			return nil, nil, invalid("interfaces.use[%q] value %q is not a fully qualified constructor symbol", value, selected)
		}
		choices = append(choices, ImplementationChoice{
			interfaceID: identifier,
			constructor: constructor,
			source:      source,
		})
	}
	return choices, removals, nil
}

func parseInterfacePolicies(node *yaml.Node) ([]InterfacePolicy, []interfaceRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	values, err := mapping(node, "interfaces.policies")
	if err != nil {
		return nil, nil, err
	}
	policies := make([]InterfacePolicy, 0, len(values))
	removals := make([]interfaceRemoval, 0, len(values))
	for _, value := range sortedNodeKeys(values) {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			return nil, nil, invalid("interfaces.policies key %q is not a canonical Interface ID", value)
		}
		if strings.HasPrefix(identifier.Name(), "kernel.") {
			return nil, nil, invalid("interfaces.policies key %q configures an intrinsic kernel.* Interface", value)
		}
		path := fmt.Sprintf("interfaces.policies[%q]", identifier.String())
		if isNull(values[value]) {
			removals = append(removals, interfaceRemoval{id: identifier, source: "plystra.yaml " + path})
			continue
		}
		fields, err := mapping(values[value], path)
		if err != nil {
			return nil, nil, err
		}
		for _, field := range sortedNodeKeys(fields) {
			if field != "timeout" {
				return nil, nil, invalid("%s contains unknown key %q", path, field)
			}
		}
		timeoutNode, exists := fields["timeout"]
		if !exists {
			return nil, nil, invalid("%s.timeout is required", path)
		}
		timeoutText, err := strictString(timeoutNode)
		if err != nil || timeoutText == "" || len(timeoutText) > 64 || strings.TrimSpace(timeoutText) != timeoutText || strings.ContainsRune(timeoutText, '\x00') {
			return nil, nil, invalid("%s.timeout must be a non-empty trimmed Go duration string of at most 64 bytes with no NUL", path)
		}
		timeout, err := time.ParseDuration(timeoutText)
		if err != nil || timeout <= 0 {
			return nil, nil, invalid("%s.timeout must be a positive Go duration", path)
		}
		policies = append(policies, InterfacePolicy{
			interfaceID: identifier,
			timeout:     timeout,
			source:      "plystra.yaml " + path + ".timeout",
		})
	}
	return policies, removals, nil
}
