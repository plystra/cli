// Package pluginmeta reads the bounded identity envelope of plugin.yaml.
package pluginmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/plystra/cli/internal/pluginid"
	"go.yaml.in/yaml/v3"
)

// MaximumSize is the largest plugin declaration inspected by the CLI index.
const MaximumSize = 1 << 20

// ErrInvalidManifest reports an unsafe or invalid plugin identity envelope.
var ErrInvalidManifest = errors.New("invalid plugin manifest identity")

// ParseID returns the canonical top-level id from one plugin.yaml document.
// Full capability and configuration validation remains the Kernel parser's
// responsibility; this reader validates only the strict top-level envelope.
func ParseID(data []byte) (string, error) {
	if len(data) == 0 {
		return "", invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return "", invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return "", invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", invalid("multiple YAML documents are not allowed")
		}
		return "", invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return "", invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return "", err
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", invalid("document must be a mapping")
	}

	var idNode *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		keyNode, valueNode := root.Content[index], root.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return "", invalid("document contains a non-string key")
		}
		key := keyNode.Value
		if _, duplicate := seen[key]; duplicate {
			return "", invalid("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "id":
			idNode = valueNode
		case "provides", "requires", "config":
		default:
			return "", invalid("unknown key %q", key)
		}
	}
	if idNode == nil {
		return "", invalid("id is required")
	}
	if idNode.Kind != yaml.ScalarNode || idNode.Tag != "!!str" {
		return "", invalid("id must be a string")
	}
	if err := pluginid.Validate(idNode.Value); err != nil {
		return "", invalid("id %q is not canonical", idNode.Value)
	}
	return idNode.Value, nil
}

func rejectReferences(root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return invalid("YAML anchors and aliases are not allowed")
		}
		stack = append(stack, node.Content...)
	}
	return nil
}

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, arguments...))
}
