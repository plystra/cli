// Package interfacemeta parses optional authored Interface metadata documents.
package interfacemeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"

	"go.yaml.in/yaml/v3"
)

const (
	// Name is the only optional metadata filename recognized beside an
	// authoritative Interface Go declaration.
	Name = "interface.yaml"
	// MaximumSize bounds one authored metadata document before parsing.
	MaximumSize = 1 << 20
)

// ErrInvalid reports an unsafe or malformed Interface metadata document.
var ErrInvalid = errors.New("invalid Interface metadata")

// Document is one immutable, syntactically valid optional metadata document.
// Semantic normalization is intentionally owned by later validation stages.
type Document struct {
	path string
	data []byte
}

// Path returns the stable slash-separated module-relative source path.
func (d Document) Path() string { return d.path }

// Data returns a defensive copy of the exact authored YAML bytes.
func (d Document) Data() []byte { return append([]byte(nil), d.data...) }

// ParseFile validates one bounded, single-document YAML mapping while
// preserving its exact authored bytes for schema-aware normalization.
func ParseFile(sourcePath string, data []byte) (Document, error) {
	if !fs.ValidPath(sourcePath) || sourcePath == "." || path.Base(sourcePath) != Name {
		return Document{}, invalid(sourcePath, 0, 0, "expected a canonical module-relative %s path", Name)
	}
	if len(data) == 0 {
		return Document{}, invalid(sourcePath, 0, 0, "document is empty")
	}
	if len(data) > MaximumSize {
		return Document{}, invalid(sourcePath, 0, 0, "document exceeds %d bytes", MaximumSize)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return Document{}, invalid(sourcePath, 0, 0, "expected one YAML document")
		}
		return Document{}, invalid(sourcePath, 0, 0, "decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Document{}, invalid(sourcePath, trailing.Line, trailing.Column, "multiple YAML documents are not allowed")
		}
		return Document{}, invalid(sourcePath, 0, 0, "decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return Document{}, invalid(sourcePath, document.Line, document.Column, "expected one YAML document")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || root.Tag != "!!map" {
		return Document{}, invalid(sourcePath, root.Line, root.Column, "metadata root must be a mapping")
	}
	if err := validateYAMLTree(sourcePath, root); err != nil {
		return Document{}, err
	}
	return Document{path: sourcePath, data: append([]byte(nil), data...)}, nil
}

func validateYAMLTree(sourcePath string, node *yaml.Node) error {
	if node == nil {
		return invalid(sourcePath, 0, 0, "document contains an empty node")
	}
	if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
		return invalid(sourcePath, node.Line, node.Column, "YAML anchors and aliases are not allowed")
	}
	if node.Kind == yaml.MappingNode {
		if len(node.Content)%2 != 0 {
			return invalid(sourcePath, node.Line, node.Column, "mapping contains an unmatched key")
		}
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" {
				line, column := 0, 0
				if key != nil {
					line, column = key.Line, key.Column
				}
				return invalid(sourcePath, line, column, "mapping keys must be nonempty strings")
			}
			if _, duplicate := seen[key.Value]; duplicate {
				return invalid(sourcePath, key.Line, key.Column, "duplicate mapping key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLTree(sourcePath, child); err != nil {
			return err
		}
	}
	return nil
}

func invalid(sourcePath string, line, column int, format string, arguments ...any) error {
	location := sourcePath
	if location == "" {
		location = Name
	}
	if line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, line, column)
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalid, location, fmt.Sprintf(format, arguments...))
}
