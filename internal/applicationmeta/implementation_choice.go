package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

// ErrSetImplementationChoice reports that a selected current-Project document
// could not safely record one explicit Implementation replacement.
var ErrSetImplementationChoice = errors.New("set Interface Implementation choice")

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

func setImplementationChoice(data []byte, id interfaceid.Identifier, constructor constructorsymbol.Symbol, parse func([]byte) (Manifest, error)) ([]byte, bool, error) {
	if id.String() == "" {
		return nil, false, fmt.Errorf("%w: Interface is empty", ErrSetImplementationChoice)
	}
	if strings.HasPrefix(id.Name(), "kernel.") {
		return nil, false, fmt.Errorf("%w: intrinsic kernel.* Interface %s does not select an application Implementation", ErrSetImplementationChoice, id)
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
