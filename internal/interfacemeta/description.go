package interfacemeta

import (
	"errors"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// ErrInvalidDescription reports invalid Interface documentation text.
var ErrInvalidDescription = errors.New("invalid Interface description")

func normalizeDescription(sourcePath string, root *yaml.Node) (string, bool, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "description" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return "", false, nil
	}
	if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return "", false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidDescription, "description must be a string")
	}
	if strings.TrimSpace(value.Value) == "" {
		return "", false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidDescription, "description must not be empty")
	}
	if !utf8.ValidString(value.Value) || strings.IndexByte(value.Value, 0) >= 0 {
		return "", false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidDescription, "description must be valid UTF-8 public text without NUL")
	}
	return value.Value, true, nil
}
