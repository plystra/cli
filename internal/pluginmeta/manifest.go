// Package pluginmeta reads the bounded indexing envelope of plugin.yaml.
package pluginmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginid"
	"go.yaml.in/yaml/v3"
)

// MaximumSize is the largest plugin declaration inspected by the CLI index.
const MaximumSize = 1 << 20

// ErrInvalidManifest reports unsafe or invalid indexed plugin metadata.
var ErrInvalidManifest = errors.New("invalid plugin manifest metadata")

// Manifest is the immutable subset of plugin.yaml needed for local indexing.
// The Kernel parser remains responsible for complete manifest validation.
type Manifest struct {
	id       string
	provides []capabilityid.Identifier
}

// ID returns the canonical Plugin ID.
func (m Manifest) ID() string { return m.id }

// Provides returns a defensive copy sorted by canonical capability identity.
func (m Manifest) Provides() []capabilityid.Identifier {
	return append([]capabilityid.Identifier(nil), m.provides...)
}

// Parse returns the strict top-level identity and capability declarations from
// one plugin.yaml document. Configuration contents are intentionally opaque.
func Parse(data []byte) (Manifest, error) {
	if len(data) == 0 {
		return Manifest{}, invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return Manifest{}, invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Manifest{}, invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, invalid("multiple YAML documents are not allowed")
		}
		return Manifest{}, invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return Manifest{}, invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return Manifest{}, err
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return Manifest{}, invalid("document must be a mapping")
	}

	var idNode, providesNode, requiresNode *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		keyNode, valueNode := root.Content[index], root.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return Manifest{}, invalid("document contains a non-string key")
		}
		key := keyNode.Value
		if _, duplicate := seen[key]; duplicate {
			return Manifest{}, invalid("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "id":
			idNode = valueNode
		case "provides":
			providesNode = valueNode
		case "requires":
			requiresNode = valueNode
		case "config":
		default:
			return Manifest{}, invalid("unknown key %q", key)
		}
	}
	if idNode == nil {
		return Manifest{}, invalid("id is required")
	}
	if idNode.Kind != yaml.ScalarNode || idNode.Tag != "!!str" {
		return Manifest{}, invalid("id must be a string")
	}
	if err := pluginid.Validate(idNode.Value); err != nil {
		return Manifest{}, invalid("id %q is not canonical", idNode.Value)
	}
	provides, err := parseCapabilities("provides", providesNode)
	if err != nil {
		return Manifest{}, err
	}
	if _, err := parseCapabilities("requires", requiresNode); err != nil {
		return Manifest{}, err
	}
	return Manifest{id: idNode.Value, provides: provides}, nil
}

func parseCapabilities(field string, node *yaml.Node) ([]capabilityid.Identifier, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalid("%s must be a sequence", field)
	}
	identifiers := make([]capabilityid.Identifier, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for index, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, invalid("%s[%d] must be a string", field, index)
		}
		identifier, err := capabilityid.Parse(item.Value)
		if err != nil {
			return nil, invalid("%s[%d] %q is not canonical", field, index, item.Value)
		}
		if _, duplicate := seen[identifier.String()]; duplicate {
			return nil, invalid("%s contains duplicate capability %q", field, identifier)
		}
		seen[identifier.String()] = struct{}{}
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left].String() < identifiers[right].String()
	})
	return identifiers, nil
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
