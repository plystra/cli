package pluginmeta

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

// ErrAddProvided reports that plugin.yaml could not safely declare one more
// provided capability.
var ErrAddProvided = errors.New("add provided capability")

// AddProvided returns a deterministic plugin.yaml declaring capability. It is
// idempotent and preserves exact source bytes when the declaration already
// exists. Changed manifests retain their YAML comments and opaque config.
func AddProvided(data []byte, capability capabilityid.Identifier) ([]byte, bool, error) {
	if capability.String() == "" {
		return nil, false, fmt.Errorf("%w: capability is empty", ErrAddProvided)
	}
	metadata, err := Parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrAddProvided, err)
	}
	for _, provided := range metadata.Provides() {
		if provided == capability {
			return append([]byte(nil), data...), false, nil
		}
	}

	document, err := decodeYAMLDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrAddProvided, err)
	}
	root := document.Content[0]
	var provides *yaml.Node
	insertAt := len(root.Content)
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			continue
		}
		switch key.Value {
		case "id":
			insertAt = index + 2
		case "provides":
			provides = root.Content[index+1]
		}
	}
	item := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: capability.String()}
	if provides == nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "provides"}
		provides = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{item}}
		root.Content = append(root.Content, nil, nil)
		copy(root.Content[insertAt+2:], root.Content[insertAt:len(root.Content)-2])
		root.Content[insertAt], root.Content[insertAt+1] = key, provides
	} else {
		provides.Content = append(provides.Content, item)
		sort.Slice(provides.Content, func(left, right int) bool {
			return provides.Content[left].Value < provides.Content[right].Value
		})
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, false, fmt.Errorf("%w: encode manifest: %w", ErrAddProvided, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("%w: close manifest encoder: %w", ErrAddProvided, err)
	}
	updated := output.Bytes()
	parsed, err := Parse(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%w: validate updated manifest: %w", ErrAddProvided, err)
	}
	if parsed.ID() != metadata.ID() || len(parsed.Provides()) != len(metadata.Provides())+1 || !containsCapability(parsed.Provides(), capability) || !sameGeneration(metadata, parsed) {
		return nil, false, fmt.Errorf("%w: updated manifest did not preserve identity and add %s", ErrAddProvided, capability)
	}
	return append([]byte(nil), updated...), true, nil
}

func sameGeneration(left, right Manifest) bool {
	leftGeneration, leftExists := left.Generation()
	rightGeneration, rightExists := right.Generation()
	if leftExists != rightExists || !leftExists {
		return leftExists == rightExists
	}
	if leftGeneration.API() != rightGeneration.API() || leftGeneration.Package() != rightGeneration.Package() {
		return false
	}
	leftActivations := leftGeneration.Activations()
	rightActivations := rightGeneration.Activations()
	if len(leftActivations) != len(rightActivations) {
		return false
	}
	for index := range leftActivations {
		if leftActivations[index] != rightActivations[index] {
			return false
		}
	}
	return true
}

func containsCapability(values []capabilityid.Identifier, target capabilityid.Identifier) bool {
	index := sort.Search(len(values), func(index int) bool {
		return values[index].String() >= target.String()
	})
	return index < len(values) && values[index] == target
}
