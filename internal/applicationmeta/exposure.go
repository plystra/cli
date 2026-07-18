package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

// ErrAddHTTPExposure reports that plystra.yaml could not safely expose one
// more canonical Capability.
var ErrAddHTTPExposure = errors.New("add HTTP Capability exposure")

// AddHTTPExposure returns deterministic plystra.yaml bytes that include id in
// http.expose. Existing bytes are returned unchanged when id is already
// present. Changed documents retain comments and all unrelated YAML values.
func AddHTTPExposure(data []byte, id capabilityid.Identifier) ([]byte, bool, error) {
	if id.String() == "" {
		return nil, false, fmt.Errorf("%w: Capability is empty", ErrAddHTTPExposure)
	}
	before, err := Parse(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrAddHTTPExposure, err)
	}
	for _, exposure := range before.HTTPExposures() {
		if exposure.ID() == id {
			return append([]byte(nil), data...), false, nil
		}
	}

	document, err := decodeDocument(data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrAddHTTPExposure, err)
	}
	root := document
	httpNode := mappingChild(root, "http")
	if httpNode == nil {
		httpNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "http"},
			httpNode,
		)
	}
	exposeNode := mappingChild(httpNode, "expose")
	if exposeNode == nil {
		exposeNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		httpNode.Content = append(httpNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "expose"},
			exposeNode,
		)
	}
	addNode := exposeNode
	if exposeNode.Kind == yaml.MappingNode {
		addNode = mappingChild(exposeNode, "add")
		if addNode == nil {
			addNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			exposeNode.Content = append(exposeNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "add"},
				addNode,
			)
		}
		if removeNode := mappingChild(exposeNode, "remove"); removeNode != nil {
			removeSequenceValue(removeNode, id.String())
		}
	}
	addNode.Content = append(addNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id.String()})
	sort.Slice(addNode.Content, func(left, right int) bool {
		return addNode.Content[left].Value < addNode.Content[right].Value
	})

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, false, fmt.Errorf("%w: encode application manifest: %w", ErrAddHTTPExposure, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("%w: close application manifest encoder: %w", ErrAddHTTPExposure, err)
	}
	updated := output.Bytes()
	after, err := Parse(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%w: validate updated application manifest: %w", ErrAddHTTPExposure, err)
	}
	if difference := manifestDifferenceOutsideHTTPExposure(before, after); difference != "" {
		return nil, false, fmt.Errorf("%w: updated application manifest changed %s", ErrAddHTTPExposure, difference)
	}
	if !hasExactlyOneAddedExposure(before.HTTPExposures(), after.HTTPExposures(), id) || !preservedOtherExposureRemovals(before.removedHTTPExposures, after.removedHTTPExposures, id) {
		return nil, false, fmt.Errorf("%w: updated application manifest did not add exactly one %s exposure", ErrAddHTTPExposure, id)
	}
	return append([]byte(nil), updated...), true, nil
}

func removeSequenceValue(node *yaml.Node, value string) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	filtered := node.Content[:0]
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag == "!!str" && item.Value == value {
			continue
		}
		filtered = append(filtered, item)
	}
	node.Content = filtered
}

func mappingChild(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		if key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == name {
			return node.Content[index+1]
		}
	}
	return nil
}

func manifestDifferenceOutsideHTTPExposure(left, right Manifest) string {
	leftAddress, leftHasAddress := left.HTTPAddress()
	rightAddress, rightHasAddress := right.HTTPAddress()
	if leftAddress != rightAddress || leftHasAddress != rightHasAddress {
		return "http.address"
	}
	if left.httpTransports != right.httpTransports {
		return "http.transports"
	}
	if left.StartupTimeout() != right.StartupTimeout() || left.hasStartupTimeout != right.hasStartupTimeout {
		return "timeouts.startup"
	}
	if left.removeHTTPAddress != right.removeHTTPAddress || left.removeStartupTimeout != right.removeStartupTimeout {
		return "scalar removals"
	}
	if !slices.Equal(left.Requirements(), right.Requirements()) {
		return "capabilities.require"
	}
	if !slices.Equal(left.removedRequirements, right.removedRequirements) {
		return "capabilities.require removals"
	}
	if !slices.Equal(left.ProviderChoices(), right.ProviderChoices()) {
		return "capabilities.use"
	}
	if !slices.Equal(left.removedProviderChoices, right.removedProviderChoices) {
		return "capabilities.use removals"
	}
	if !slices.Equal(left.Aliases(), right.Aliases()) {
		return "capabilities.aliases"
	}
	if !slices.Equal(left.removedAliases, right.removedAliases) {
		return "capabilities.aliases removals"
	}
	if !slices.Equal(left.removedConfigurations, right.removedConfigurations) {
		return "config removals"
	}
	leftConfigurations := left.Configurations()
	rightConfigurations := right.Configurations()
	if len(leftConfigurations) != len(rightConfigurations) {
		return "config"
	}
	for index := range leftConfigurations {
		if leftConfigurations[index].pluginID != rightConfigurations[index].pluginID ||
			leftConfigurations[index].source != rightConfigurations[index].source ||
			!bytes.Equal(leftConfigurations[index].yaml, rightConfigurations[index].yaml) {
			return "config"
		}
	}
	return ""
}

func preservedOtherExposureRemovals(before, after []capabilityRemoval, added capabilityid.Identifier) bool {
	filter := func(values []capabilityRemoval) []capabilityRemoval {
		result := make([]capabilityRemoval, 0, len(values))
		for _, value := range values {
			if value.id != added {
				result = append(result, value)
			}
		}
		return result
	}
	for _, value := range after {
		if value.id == added {
			return false
		}
	}
	return slices.Equal(filter(before), filter(after))
}

func hasExactlyOneAddedExposure(before, after []HTTPExposure, added capabilityid.Identifier) bool {
	if len(after) != len(before)+1 {
		return false
	}
	beforeIDs := make([]capabilityid.Identifier, len(before))
	for index, exposure := range before {
		beforeIDs[index] = exposure.ID()
	}
	afterIDs := make([]capabilityid.Identifier, 0, len(after)-1)
	found := 0
	for _, exposure := range after {
		if exposure.ID() == added {
			found++
			continue
		}
		afterIDs = append(afterIDs, exposure.ID())
	}
	return found == 1 && slices.Equal(beforeIDs, afterIDs)
}
