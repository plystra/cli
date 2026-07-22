package interfacemeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const maximumSemanticErrorCodeLength = 128

// ErrInvalidSemanticErrors reports an invalid declared semantic-error schema.
var ErrInvalidSemanticErrors = errors.New("invalid Interface semantic errors")

// SemanticError is one immutable normalized caller-visible domain error.
type SemanticError struct {
	code           string
	description    string
	hasDescription bool
}

// Code returns the exact normalized stable error code.
func (e SemanticError) Code() string { return e.code }

// Description returns the optional documentation description.
func (e SemanticError) Description() (string, bool) {
	return e.description, e.hasDescription
}

func normalizeSemanticErrors(sourcePath string, root *yaml.Node) ([]SemanticError, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "errors" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return nil, nil
	}
	if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
		return nil, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidSemanticErrors, "errors must be a sequence of mappings with a required code and optional description")
	}

	result := make([]SemanticError, 0, len(value.Content))
	codeLocations := make(map[string]string, len(value.Content))
	for index, entry := range value.Content {
		fieldPath := fmt.Sprintf("errors[%d]", index)
		if entry.Kind != yaml.MappingNode || entry.Tag != "!!map" {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidSemanticErrors, "%s must be a mapping with a required code and optional description", fieldPath)
		}

		var codeNode, descriptionNode *yaml.Node
		for fieldIndex := 0; fieldIndex < len(entry.Content); fieldIndex += 2 {
			field := entry.Content[fieldIndex]
			switch field.Value {
			case "code":
				codeNode = entry.Content[fieldIndex+1]
			case "description":
				descriptionNode = entry.Content[fieldIndex+1]
			default:
				return nil, invalidWith(sourcePath, field.Line, field.Column, ErrInvalidSemanticErrors, "unknown field %q; allowed fields are code and description", fieldPath+"."+field.Value)
			}
		}
		if codeNode == nil {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidSemanticErrors, "required field %s.code is missing", fieldPath)
		}
		if codeNode.Kind != yaml.ScalarNode || codeNode.Tag != "!!str" {
			return nil, invalidWith(sourcePath, codeNode.Line, codeNode.Column, ErrInvalidSemanticErrors, "%s.code must be a lower-snake-case string", fieldPath)
		}
		if !validSemanticErrorCode(codeNode.Value) {
			return nil, invalidWith(sourcePath, codeNode.Line, codeNode.Column, ErrInvalidSemanticErrors, "%s.code %q must be 1-%d characters of lower snake case", fieldPath, codeNode.Value, maximumSemanticErrorCodeLength)
		}

		location := fmt.Sprintf("%s:%d:%d", sourcePath, codeNode.Line, codeNode.Column)
		if first, duplicate := codeLocations[codeNode.Value]; duplicate {
			return nil, invalidWith(sourcePath, codeNode.Line, codeNode.Column, ErrInvalidSemanticErrors, "%s.code %q duplicates the declaration at %s", fieldPath, codeNode.Value, first)
		}
		codeLocations[codeNode.Value] = location

		semanticError := SemanticError{code: codeNode.Value}
		if descriptionNode != nil {
			if descriptionNode.Kind != yaml.ScalarNode || descriptionNode.Tag != "!!str" {
				return nil, invalidWith(sourcePath, descriptionNode.Line, descriptionNode.Column, ErrInvalidSemanticErrors, "%s.description must be a string", fieldPath)
			}
			if strings.TrimSpace(descriptionNode.Value) == "" {
				return nil, invalidWith(sourcePath, descriptionNode.Line, descriptionNode.Column, ErrInvalidSemanticErrors, "%s.description must not be empty", fieldPath)
			}
			semanticError.description = descriptionNode.Value
			semanticError.hasDescription = true
		}
		result = append(result, semanticError)
	}

	sort.Slice(result, func(left, right int) bool {
		return result[left].code < result[right].code
	})
	return result, nil
}

func validSemanticErrorCode(value string) bool {
	if len(value) == 0 || len(value) > maximumSemanticErrorCodeLength || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for _, character := range value[1:] {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
}
