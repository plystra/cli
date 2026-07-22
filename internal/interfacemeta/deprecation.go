package interfacemeta

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

const (
	// MaximumDeprecationMessageLength bounds public deprecation guidance in
	// UTF-8 bytes.
	MaximumDeprecationMessageLength = 1024
	// MaximumDeprecationSinceLength bounds the optional public lifecycle label
	// in UTF-8 bytes.
	MaximumDeprecationSinceLength = 128
)

// ErrInvalidDeprecation reports invalid Interface lifecycle metadata.
var ErrInvalidDeprecation = errors.New("invalid Interface deprecation")

type deprecationDeclaration struct {
	value             Deprecation
	replacementLine   int
	replacementColumn int
}

// Deprecation is immutable public lifecycle documentation for one exact
// Interface. It never creates forwarding or changes resolution.
type Deprecation struct {
	message        string
	replacement    interfaceid.Identifier
	hasReplacement bool
	since          string
	hasSince       bool
}

// Message returns the required public migration guidance.
func (d Deprecation) Message() string { return d.message }

// Replacement returns the optional direct canonical replacement Interface ID.
func (d Deprecation) Replacement() (interfaceid.Identifier, bool) {
	return d.replacement, d.hasReplacement
}

// Since returns the optional public lifecycle label.
func (d Deprecation) Since() (string, bool) { return d.since, d.hasSince }

func normalizeDeprecation(sourcePath string, root *yaml.Node) (deprecationDeclaration, bool, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "deprecation" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return deprecationDeclaration{}, false, nil
	}
	if value.Kind != yaml.MappingNode || value.Tag != "!!map" {
		return deprecationDeclaration{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidDeprecation, "deprecation must be a mapping with a required message and optional replacement and since fields")
	}

	var messageNode, replacementNode, sinceNode *yaml.Node
	for index := 0; index < len(value.Content); index += 2 {
		field := value.Content[index]
		switch field.Value {
		case "message":
			messageNode = value.Content[index+1]
		case "replacement":
			replacementNode = value.Content[index+1]
		case "since":
			sinceNode = value.Content[index+1]
		default:
			return deprecationDeclaration{}, false, invalidWith(sourcePath, field.Line, field.Column, ErrInvalidDeprecation, "unknown field %q; allowed fields are message, replacement, and since", "deprecation."+field.Value)
		}
	}
	if messageNode == nil {
		return deprecationDeclaration{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidDeprecation, "required field deprecation.message is missing")
	}
	if err := validateDeprecationText(sourcePath, messageNode, "deprecation.message", MaximumDeprecationMessageLength); err != nil {
		return deprecationDeclaration{}, false, err
	}

	deprecation := Deprecation{message: messageNode.Value}
	declaration := deprecationDeclaration{value: deprecation}
	if replacementNode != nil {
		if replacementNode.Kind != yaml.ScalarNode || replacementNode.Tag != "!!str" {
			return deprecationDeclaration{}, false, invalidWith(sourcePath, replacementNode.Line, replacementNode.Column, ErrInvalidDeprecation, "deprecation.replacement must be a canonical Interface ID string")
		}
		replacement, err := interfaceid.Parse(replacementNode.Value)
		if err != nil {
			return deprecationDeclaration{}, false, invalidWith(sourcePath, replacementNode.Line, replacementNode.Column, ErrInvalidDeprecation, "deprecation.replacement %q is not a canonical Interface ID: %v", replacementNode.Value, err)
		}
		declaration.value.replacement = replacement
		declaration.value.hasReplacement = true
		declaration.replacementLine = replacementNode.Line
		declaration.replacementColumn = replacementNode.Column
	}
	if sinceNode != nil {
		if err := validateDeprecationText(sourcePath, sinceNode, "deprecation.since", MaximumDeprecationSinceLength); err != nil {
			return deprecationDeclaration{}, false, err
		}
		declaration.value.since = sinceNode.Value
		declaration.value.hasSince = true
	}
	return declaration, true, nil
}

// ResolveDeprecation validates any replacement against the current canonical
// contract and the complete visible Interface inventory. It changes no
// binding, exposure, policy, or invocation behavior.
func ResolveDeprecation(document Document, contract interfacecontract.Contract, visibleIDs map[string]struct{}) (Deprecation, bool, error) {
	if !document.hasDeprecation {
		return Deprecation{}, false, nil
	}
	declaration := document.deprecation
	replacement, present := declaration.value.Replacement()
	if !present {
		return declaration.value, true, nil
	}
	if contract.ID().String() == "" {
		return Deprecation{}, false, invalidWith(document.path, declaration.replacementLine, declaration.replacementColumn, ErrInvalidDeprecation, "a canonical Interface contract is required to validate deprecation.replacement")
	}
	if replacement.String() == contract.ID().String() {
		return Deprecation{}, false, invalidWith(document.path, declaration.replacementLine, declaration.replacementColumn, ErrInvalidDeprecation, "deprecation.replacement %q must differ from the deprecated Interface", replacement.String())
	}
	if _, visible := visibleIDs[replacement.String()]; visible {
		return declaration.value, true, nil
	}
	return Deprecation{}, false, invalidWith(document.path, declaration.replacementLine, declaration.replacementColumn, ErrInvalidDeprecation, "deprecation.replacement %q is not a visible Interface", replacement.String())
}

func validateDeprecationText(sourcePath string, node *yaml.Node, field string, maximum int) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return invalidWith(sourcePath, node.Line, node.Column, ErrInvalidDeprecation, "%s must be a string", field)
	}
	if strings.TrimSpace(node.Value) == "" {
		return invalidWith(sourcePath, node.Line, node.Column, ErrInvalidDeprecation, "%s must not be empty", field)
	}
	if !utf8.ValidString(node.Value) || strings.IndexByte(node.Value, 0) >= 0 {
		return invalidWith(sourcePath, node.Line, node.Column, ErrInvalidDeprecation, "%s must be valid UTF-8 public text without NUL", field)
	}
	if len(node.Value) > maximum {
		return invalidWith(sourcePath, node.Line, node.Column, ErrInvalidDeprecation, "%s must contain at most %d UTF-8 bytes", field, maximum)
	}
	return nil
}
