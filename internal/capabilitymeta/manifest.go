// Package capabilitymeta reads the bounded planning metadata of capability.yaml.
package capabilitymeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/plystra/cli/internal/capabilityid"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

// MaximumSize is the largest capability declaration inspected by the CLI.
const MaximumSize = kernelmanifest.MaximumDeclarationSize

// ErrInvalidManifest reports unsafe or invalid capability planning metadata.
var ErrInvalidManifest = errors.New("invalid capability manifest metadata")

// Manifest is the immutable subset of capability.yaml needed for planning.
// Complete contract validation remains the Kernel parser's responsibility.
type Manifest struct {
	id         capabilityid.Identifier
	semantics  CapabilitySemantics
	extensions CapabilityExtensions
}

// ID returns the exact canonical Capability ID.
func (m Manifest) ID() capabilityid.Identifier { return m.id }

// Semantics returns the complete validated provider-independent operation
// declaration owned by the Kernel Capability model.
func (m Manifest) Semantics() CapabilitySemantics { return m.semantics }

// Extensions returns immutable normalized build-time metadata.
func (m Manifest) Extensions() CapabilityExtensions { return m.extensions }

// Parse validates one complete capability.yaml through the Kernel-owned parser
// and returns the immutable planning view consumed by CLI resolution.
func Parse(data []byte) (Manifest, error) {
	declaration, err := kernelmanifest.ParseCapability(data)
	if err != nil {
		return Manifest{}, invalid("%v", err)
	}
	return manifestFromCapability(declaration)
}

func manifestFromCapability(declaration kernelmanifest.Capability) (Manifest, error) {
	identifier, err := capabilityid.Parse(declaration.ID().String())
	if err != nil {
		return Manifest{}, invalid("Kernel returned invalid Capability ID %q: %v", declaration.ID(), err)
	}
	return Manifest{
		id:         identifier,
		semantics:  declaration.Semantics(),
		extensions: declaration.Extensions(),
	}, nil
}

// ParseID returns the exact canonical ID from one capability.yaml document.
func ParseID(data []byte) (capabilityid.Identifier, error) {
	manifest, err := Parse(data)
	if err != nil {
		return capabilityid.Identifier{}, err
	}
	return manifest.ID(), nil
}

func decodeYAMLDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return nil, invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, invalid("multiple YAML documents are not allowed")
		}
		return nil, invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return nil, err
	}
	return &document, nil
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
