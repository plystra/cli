package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginid"
	"go.yaml.in/yaml/v3"
)

// ErrSetProviderChoice reports that a selected current-Project document could
// not safely record one explicit Provider replacement.
var ErrSetProviderChoice = errors.New("set Capability Provider choice")

// SetProviderChoice returns deterministic current-Project YAML bytes that map
// capability to pluginID under capabilities.use. Existing bytes are returned
// unchanged when the exact choice is already present. Comments and unrelated
// values remain owned by the user.
func SetProviderChoice(data []byte, capability capabilityid.Identifier, pluginID string) ([]byte, bool, error) {
	return setProviderChoice(data, capability, pluginID, Parse)
}

// SetProviderChoiceOverlay applies the same edit to one sparse environment
// overlay while validating it with overlay semantics.
func SetProviderChoiceOverlay(data []byte, capability capabilityid.Identifier, pluginID string) ([]byte, bool, error) {
	return setProviderChoice(data, capability, pluginID, func(input []byte) (Manifest, error) {
		return ParseOverlaySource("plystra.<environment>.yaml", input)
	})
}

func setProviderChoice(data []byte, capability capabilityid.Identifier, pluginID string, parse func([]byte) (Manifest, error)) ([]byte, bool, error) {
	if capability.String() == "" {
		return nil, false, fmt.Errorf("%w: Capability is empty", ErrSetProviderChoice)
	}
	if strings.HasPrefix(capability.Name(), "kernel.") {
		return nil, false, fmt.Errorf("%w: intrinsic kernel.* Capability %s does not select a Plugin Provider", ErrSetProviderChoice, capability)
	}
	if err := pluginid.Validate(pluginID); err != nil {
		return nil, false, fmt.Errorf("%w: Plugin ID %q: %w", ErrSetProviderChoice, pluginID, err)
	}
	before, err := parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSetProviderChoice, err)
	}
	for _, choice := range before.ProviderChoices() {
		if choice.Capability() == capability && choice.PluginID() == pluginID {
			return append([]byte(nil), data...), false, nil
		}
	}

	document, err := decodeDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSetProviderChoice, err)
	}
	root := document
	capabilitiesNode := mappingChild(root, "capabilities")
	if capabilitiesNode == nil {
		capabilitiesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "capabilities"},
			capabilitiesNode,
		)
	}
	useNode := mappingChild(capabilitiesNode, "use")
	if useNode == nil {
		useNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		capabilitiesNode.Content = append(capabilitiesNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "use"},
			useNode,
		)
	}
	setMappingString(useNode, capability.String(), pluginID)
	sortScalarMapping(useNode)

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, false, fmt.Errorf("%w: encode application manifest: %w", ErrSetProviderChoice, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("%w: close application manifest encoder: %w", ErrSetProviderChoice, err)
	}
	updated := output.Bytes()
	after, err := parse(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%w: validate updated application manifest: %w", ErrSetProviderChoice, err)
	}
	if difference := manifestDifferenceOutsideProviderChoice(before, after, capability); difference != "" {
		return nil, false, fmt.Errorf("%w: updated application manifest changed %s", ErrSetProviderChoice, difference)
	}
	if !hasProviderChoice(after.ProviderChoices(), capability, pluginID) || hasCapabilityRemoval(after.removedProviderChoices, capability) {
		return nil, false, fmt.Errorf("%w: updated application manifest did not set exactly %s to %s", ErrSetProviderChoice, capability, pluginID)
	}
	return append([]byte(nil), updated...), true, nil
}

func setMappingString(node *yaml.Node, key, value string) {
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Kind != yaml.ScalarNode || node.Content[index].Tag != "!!str" || node.Content[index].Value != key {
			continue
		}
		target := node.Content[index+1]
		target.Kind = yaml.ScalarNode
		target.Tag = "!!str"
		target.Value = value
		target.Content = nil
		return
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func sortScalarMapping(node *yaml.Node) {
	type pair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	pairs := make([]pair, 0, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		pairs = append(pairs, pair{key: node.Content[index], value: node.Content[index+1]})
	}
	sort.SliceStable(pairs, func(left, right int) bool {
		return pairs[left].key.Value < pairs[right].key.Value
	})
	node.Content = node.Content[:0]
	for _, entry := range pairs {
		node.Content = append(node.Content, entry.key, entry.value)
	}
}

func manifestDifferenceOutsideProviderChoice(left, right Manifest, selected capabilityid.Identifier) string {
	if !slices.Equal(left.HTTPExposures(), right.HTTPExposures()) || !slices.Equal(left.removedHTTPExposures, right.removedHTTPExposures) {
		return "http.expose"
	}
	left.providerChoices = providerChoicesExcept(left.providerChoices, selected)
	right.providerChoices = providerChoicesExcept(right.providerChoices, selected)
	left.removedProviderChoices = capabilityRemovalsExcept(left.removedProviderChoices, selected)
	right.removedProviderChoices = capabilityRemovalsExcept(right.removedProviderChoices, selected)
	return manifestDifferenceOutsideHTTPExposure(left, right)
}

func providerChoicesExcept(values []ProviderChoice, selected capabilityid.Identifier) []ProviderChoice {
	result := make([]ProviderChoice, 0, len(values))
	for _, value := range values {
		if value.capability != selected {
			result = append(result, value)
		}
	}
	return result
}

func capabilityRemovalsExcept(values []capabilityRemoval, selected capabilityid.Identifier) []capabilityRemoval {
	result := make([]capabilityRemoval, 0, len(values))
	for _, value := range values {
		if value.id != selected {
			result = append(result, value)
		}
	}
	return result
}

func hasProviderChoice(values []ProviderChoice, capability capabilityid.Identifier, pluginID string) bool {
	for _, value := range values {
		if value.capability == capability && value.pluginID == pluginID {
			return true
		}
	}
	return false
}

func hasCapabilityRemoval(values []capabilityRemoval, capability capabilityid.Identifier) bool {
	for _, value := range values {
		if value.id == capability {
			return true
		}
	}
	return false
}
