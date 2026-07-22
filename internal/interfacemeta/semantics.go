package interfacemeta

import (
	"errors"

	"go.yaml.in/yaml/v3"
)

// ErrInvalidSemantics reports an invalid closed Interface operation-semantics
// declaration.
var ErrInvalidSemantics = errors.New("invalid Interface operation semantics")

// OperationKind is the closed operation classification declared by optional
// Interface metadata.
type OperationKind string

const (
	// OperationKindQuery identifies the query classification.
	OperationKindQuery OperationKind = "query"
	// OperationKindCommand identifies the command classification.
	OperationKindCommand OperationKind = "command"
)

// Semantics is one immutable normalized operation-semantics declaration.
type Semantics struct {
	kind OperationKind
}

// Kind returns the exact normalized operation kind.
func (s Semantics) Kind() OperationKind { return s.kind }

func normalizeSemantics(sourcePath string, root *yaml.Node) (Semantics, bool, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "semantics" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return Semantics{}, false, nil
	}
	if value.Kind != yaml.MappingNode || value.Tag != "!!map" {
		return Semantics{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidSemantics, "semantics must be a mapping containing exactly one required field, semantics.kind")
	}

	var kindNode *yaml.Node
	for index := 0; index < len(value.Content); index += 2 {
		field := value.Content[index]
		if field.Value != "kind" {
			return Semantics{}, false, invalidWith(sourcePath, field.Line, field.Column, ErrInvalidSemantics, "unknown field %q; the only allowed field is semantics.kind", "semantics."+field.Value)
		}
		kindNode = value.Content[index+1]
	}
	if kindNode == nil {
		return Semantics{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidSemantics, "required field semantics.kind is missing")
	}
	if kindNode.Kind != yaml.ScalarNode || kindNode.Tag != "!!str" {
		return Semantics{}, false, invalidWith(sourcePath, kindNode.Line, kindNode.Column, ErrInvalidSemantics, "semantics.kind must be the string query or command")
	}

	kind := OperationKind(kindNode.Value)
	if kind != OperationKindQuery && kind != OperationKindCommand {
		return Semantics{}, false, invalidWith(sourcePath, kindNode.Line, kindNode.Column, ErrInvalidSemantics, "semantics.kind %q is not supported; expected query or command", kindNode.Value)
	}
	return Semantics{kind: kind}, true, nil
}
