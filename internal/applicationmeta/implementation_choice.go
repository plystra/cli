package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// ErrSetImplementationChoice reports that a selected current-Project document
// could not safely record one explicit Implementation replacement.
var ErrSetImplementationChoice = errors.New("set Interface Implementation choice")

// ErrRemoveImplementationChoice reports an unsafe selected-layer choice removal.
var ErrRemoveImplementationChoice = errors.New("remove Interface Implementation choice")

// SetImplementationChoice returns deterministic current-Project YAML bytes
// that map an Interface to a constructor under interfaces.use. Existing bytes
// are returned unchanged when the exact choice is already present. Comments
// and unrelated values remain owned by the user.
func SetImplementationChoice(data []byte, id interfaceid.Identifier, constructor constructorsymbol.Symbol) ([]byte, bool, error) {
	return setImplementationChoice(data, id, constructor, Parse)
}

// SetImplementationChoiceOverlay applies the same edit to one sparse
// environment overlay while validating it with overlay semantics.
func SetImplementationChoiceOverlay(data []byte, id interfaceid.Identifier, constructor constructorsymbol.Symbol) ([]byte, bool, error) {
	return setImplementationChoice(data, id, constructor, func(input []byte) (Manifest, error) {
		return ParseOverlaySource("plystra.<environment>.yaml", input)
	})
}

// RemoveImplementationChoice deletes only the exact local choice. A complete
// document cannot author a removal marker.
func RemoveImplementationChoice(data []byte, id interfaceid.Identifier) ([]byte, bool, error) {
	return removeImplementationChoice(data, id, Parse, false)
}

// RemoveImplementationChoiceOverlay excludes an inherited choice at the exact
// Interface key without changing the lower document.
func RemoveImplementationChoiceOverlay(data []byte, id interfaceid.Identifier) ([]byte, bool, error) {
	return removeImplementationChoice(data, id, func(input []byte) (Manifest, error) {
		return ParseOverlaySource("plystra.<environment>.yaml", input)
	}, true)
}

func removeImplementationChoice(data []byte, id interfaceid.Identifier, parse func([]byte) (Manifest, error), overlay bool) ([]byte, bool, error) {
	if id.String() == "" {
		return nil, false, fmt.Errorf("%w: Interface is empty", ErrRemoveImplementationChoice)
	}
	before, err := parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrRemoveImplementationChoice, err)
	}
	if overlay && hasInterfaceRemoval(before.removedImplementationChoices, id) {
		return append([]byte(nil), data...), false, nil
	}
	if !overlay && !slices.ContainsFunc(before.ImplementationChoices(), func(choice ImplementationChoice) bool { return choice.InterfaceID() == id }) {
		return append([]byte(nil), data...), false, nil
	}

	document, err := decodeDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrRemoveImplementationChoice, err)
	}
	interfacesNode := mappingChild(document, "interfaces")
	if interfacesNode == nil && overlay {
		interfacesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(document, "interfaces", interfacesNode)
	}
	useNode := mappingChild(interfacesNode, "use")
	if useNode == nil && overlay {
		useNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(interfacesNode, "use", useNode)
	}
	if overlay {
		setMappingValue(useNode, id.String(), configurationRemovalYAMLNode())
	} else {
		removeMappingValue(useNode, id.String())
	}
	sortYAMLMapping(useNode)
	updated, err := encodeConfigurationDocument(document)
	if err != nil {
		return nil, false, fmt.Errorf("%w: encode application manifest: %w", ErrRemoveImplementationChoice, err)
	}
	after, err := parse(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%w: validate updated application manifest: %w", ErrRemoveImplementationChoice, err)
	}
	if difference := manifestDifferenceOutsideImplementationChoice(before, after, id); difference != "" {
		return nil, false, fmt.Errorf("%w: updated application manifest changed %s", ErrRemoveImplementationChoice, difference)
	}
	if slices.ContainsFunc(after.ImplementationChoices(), func(choice ImplementationChoice) bool { return choice.InterfaceID() == id }) || hasInterfaceRemoval(after.removedImplementationChoices, id) != overlay {
		return nil, false, fmt.Errorf("%w: updated application manifest did not remove exactly %s", ErrRemoveImplementationChoice, id)
	}
	return append([]byte(nil), updated...), true, nil
}

func setImplementationChoice(data []byte, id interfaceid.Identifier, constructor constructorsymbol.Symbol, parse func([]byte) (Manifest, error)) ([]byte, bool, error) {
	if id.String() == "" {
		return nil, false, fmt.Errorf("%w: Interface is empty", ErrSetImplementationChoice)
	}
	if constructor.String() == "" {
		return nil, false, fmt.Errorf("%w: Implementation constructor is empty", ErrSetImplementationChoice)
	}
	before, err := parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSetImplementationChoice, err)
	}
	for _, choice := range before.ImplementationChoices() {
		if choice.InterfaceID() == id && choice.Constructor() == constructor {
			return append([]byte(nil), data...), false, nil
		}
	}

	document, err := decodeDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSetImplementationChoice, err)
	}
	interfacesNode := mappingChild(document, "interfaces")
	if interfacesNode == nil {
		interfacesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		document.Content = append(document.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "interfaces"},
			interfacesNode,
		)
	}
	useNode := mappingChild(interfacesNode, "use")
	if useNode == nil {
		useNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		interfacesNode.Content = append(interfacesNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "use"},
			useNode,
		)
	}
	setMappingString(useNode, id.String(), constructor.String())
	sortScalarMapping(useNode)

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, false, fmt.Errorf("%w: encode application manifest: %w", ErrSetImplementationChoice, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("%w: close application manifest encoder: %w", ErrSetImplementationChoice, err)
	}
	updated := output.Bytes()
	after, err := parse(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%w: validate updated application manifest: %w", ErrSetImplementationChoice, err)
	}
	if difference := manifestDifferenceOutsideImplementationChoice(before, after, id); difference != "" {
		return nil, false, fmt.Errorf("%w: updated application manifest changed %s", ErrSetImplementationChoice, difference)
	}
	if !hasImplementationChoice(after.ImplementationChoices(), id, constructor) || hasInterfaceRemoval(after.removedImplementationChoices, id) {
		return nil, false, fmt.Errorf("%w: updated application manifest did not set exactly %s to %s", ErrSetImplementationChoice, id, constructor)
	}
	return append([]byte(nil), updated...), true, nil
}

func manifestDifferenceOutsideImplementationChoice(left, right Manifest, selected interfaceid.Identifier) string {
	left.implementationChoices = implementationChoicesExcept(left.implementationChoices, selected)
	right.implementationChoices = implementationChoicesExcept(right.implementationChoices, selected)
	left.removedImplementationChoices = interfaceRemovalsExcept(left.removedImplementationChoices, selected)
	right.removedImplementationChoices = interfaceRemovalsExcept(right.removedImplementationChoices, selected)
	return manifestDifferenceOutsideHTTPExposure(left, right)
}

func implementationChoicesExcept(values []ImplementationChoice, selected interfaceid.Identifier) []ImplementationChoice {
	result := make([]ImplementationChoice, 0, len(values))
	for _, value := range values {
		if value.interfaceID != selected {
			result = append(result, value)
		}
	}
	return result
}

func interfaceRemovalsExcept(values []interfaceRemoval, selected interfaceid.Identifier) []interfaceRemoval {
	result := make([]interfaceRemoval, 0, len(values))
	for _, value := range values {
		if value.id != selected {
			result = append(result, value)
		}
	}
	return result
}

func hasImplementationChoice(values []ImplementationChoice, id interfaceid.Identifier, constructor constructorsymbol.Symbol) bool {
	return slices.ContainsFunc(values, func(value ImplementationChoice) bool {
		return value.interfaceID == id && value.constructor == constructor
	})
}

func hasInterfaceRemoval(values []interfaceRemoval, id interfaceid.Identifier) bool {
	return slices.ContainsFunc(values, func(value interfaceRemoval) bool {
		return value.id == id
	})
}

func setMappingString(node *yaml.Node, key, value string) {
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Kind != yaml.ScalarNode || node.Content[index].Tag != "!!str" || node.Content[index].Value != key {
			continue
		}
		target := node.Content[index+1]
		target.Kind = yaml.ScalarNode
		target.Tag = "!!str"
		target.Value = value
		target.Content = nil
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func sortScalarMapping(node *yaml.Node) {
	type pair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	pairs := make([]pair, 0, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		pairs = append(pairs, pair{key: node.Content[index], value: node.Content[index+1]})
	}
	sort.SliceStable(pairs, func(left, right int) bool {
		return pairs[left].key.Value < pairs[right].key.Value
	})
	node.Content = node.Content[:0]
	for _, entry := range pairs {
		node.Content = append(node.Content, entry.key, entry.value)
	}
}
