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

var (
	// ErrInvalid reports an unsafe or malformed Interface metadata document.
	ErrInvalid = errors.New("invalid Interface metadata")
	// ErrAuthoritativeField reports metadata that attempts to redeclare a
	// contract fact owned by the canonical Interface Go package.
	ErrAuthoritativeField = errors.New("authoritative Go contract field in Interface metadata")
	// ErrUnknownField reports a top-level metadata field outside the closed
	// Interface metadata vocabulary.
	ErrUnknownField = errors.New("unknown Interface metadata field")
)

var allowedTopLevelFields = map[string]struct{}{
	"description": {},
	"semantics":   {},
	"errors":      {},
	"constraints": {},
	"examples":    {},
	"deprecation": {},
	"conformance": {},
}

var authoritativeTopLevelFields = map[string]string{
	"id":                  "Interface ID",
	"interface":           "Interface declaration",
	"interface_id":        "Interface ID",
	"method":              "operation method",
	"method_name":         "operation method",
	"operation":           "operation method",
	"request":             "request type",
	"request_type":        "request type",
	"request_fields":      "request fields",
	"response":            "response type",
	"response_type":       "response type",
	"response_fields":     "response fields",
	"fields":              "request and response fields",
	"types":               "Go field types",
	"go_types":            "Go field types",
	"field_numbers":       "stable field numbers",
	"required":            "required-field markers",
	"required_fields":     "required-field markers",
	"json_names":          "explicit JSON field names",
	"implementation":      "Implementation identity",
	"implementation_id":   "Implementation identity",
	"implementation_type": "Implementation identity",
	"constructor":         "Implementation constructor identity",
}

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
	if err := validateTopLevelFields(sourcePath, root); err != nil {
		return Document{}, err
	}
	return Document{path: sourcePath, data: append([]byte(nil), data...)}, nil
}

func validateTopLevelFields(sourcePath string, root *yaml.Node) error {
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		if _, allowed := allowedTopLevelFields[key.Value]; allowed {
			if err := rejectNestedAuthoritativeFields(sourcePath, key.Value, root.Content[index+1]); err != nil {
				return err
			}
			continue
		}
		if authority, duplicated := authoritativeTopLevelFields[key.Value]; duplicated {
			return invalidWith(sourcePath, key.Line, key.Column, ErrAuthoritativeField, "field %q is not allowed because %s is authoritative in the Interface Go package", key.Value, authority)
		}
		return invalidWith(sourcePath, key.Line, key.Column, ErrUnknownField, "unknown top-level field %q; allowed fields are description, semantics, errors, constraints, examples, deprecation, and conformance", key.Value)
	}
	return nil
}

func rejectNestedAuthoritativeFields(sourcePath, section string, node *yaml.Node) error {
	if section != "examples" || node == nil || node.Kind != yaml.SequenceNode {
		return walkAuthoritativeFields(sourcePath, node)
	}
	for _, example := range node.Content {
		if example == nil || example.Kind != yaml.MappingNode {
			if err := walkAuthoritativeFields(sourcePath, example); err != nil {
				return err
			}
			continue
		}
		for index := 0; index < len(example.Content); index += 2 {
			key := example.Content[index]
			value := example.Content[index+1]
			if key.Value == "request" || key.Value == "response" || key.Value == "error" {
				// Request, response, and error examples carry ordinary application data.
				// Their field names may coincide with metadata vocabulary without
				// attempting to redeclare the Go contract.
				continue
			}
			if authority, duplicated := authoritativeTopLevelFields[key.Value]; duplicated {
				return invalidWith(sourcePath, key.Line, key.Column, ErrAuthoritativeField, "field %q is not allowed here because %s is authoritative in the Interface Go package", key.Value, authority)
			}
			if err := walkAuthoritativeFields(sourcePath, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func walkAuthoritativeFields(sourcePath string, node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if authority, duplicated := authoritativeTopLevelFields[key.Value]; duplicated {
				return invalidWith(sourcePath, key.Line, key.Column, ErrAuthoritativeField, "field %q is not allowed here because %s is authoritative in the Interface Go package", key.Value, authority)
			}
		}
	}
	for _, child := range node.Content {
		if err := walkAuthoritativeFields(sourcePath, child); err != nil {
			return err
		}
	}
	return nil
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
	return invalidWith(sourcePath, line, column, nil, format, arguments...)
}

func invalidWith(sourcePath string, line, column int, kind error, format string, arguments ...any) error {
	location := sourcePath
	if location == "" {
		location = Name
	}
	if line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, line, column)
	}
	message := fmt.Sprintf(format, arguments...)
	if kind != nil {
		return fmt.Errorf("%w: %w: %s: %s", ErrInvalid, kind, location, message)
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalid, location, message)
}
