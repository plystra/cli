// Package capabilitymeta reads the bounded identity envelope of capability.yaml.
package capabilitymeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

// MaximumSize is the largest capability declaration inspected by the CLI.
const MaximumSize = 1 << 20

// ErrInvalidManifest reports an unsafe or invalid capability identity envelope.
var ErrInvalidManifest = errors.New("invalid capability manifest identity")

// ParseID returns the exact canonical ID from one capability.yaml document.
// Full contract validation remains the Kernel parser's responsibility.
func ParseID(data []byte) (capabilityid.Identifier, error) {
	if len(data) == 0 {
		return capabilityid.Identifier{}, invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return capabilityid.Identifier{}, invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return capabilityid.Identifier{}, invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return capabilityid.Identifier{}, invalid("multiple YAML documents are not allowed")
		}
		return capabilityid.Identifier{}, invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return capabilityid.Identifier{}, invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return capabilityid.Identifier{}, err
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return capabilityid.Identifier{}, invalid("document must be a mapping")
	}

	var idNode *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		keyNode, valueNode := root.Content[index], root.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return capabilityid.Identifier{}, invalid("document contains a non-string key")
		}
		key := keyNode.Value
		if _, duplicate := seen[key]; duplicate {
			return capabilityid.Identifier{}, invalid("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "id":
			idNode = valueNode
		case "description", "request", "response", "errors":
		default:
			return capabilityid.Identifier{}, invalid("unknown key %q", key)
		}
	}
	if idNode == nil {
		return capabilityid.Identifier{}, invalid("id is required")
	}
	if idNode.Kind != yaml.ScalarNode || idNode.Tag != "!!str" {
		return capabilityid.Identifier{}, invalid("id must be a string")
	}
	identifier, err := capabilityid.Parse(idNode.Value)
	if err != nil {
		return capabilityid.Identifier{}, invalid("id %q is not canonical", idNode.Value)
	}
	return identifier, nil
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
