package applicationmeta

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

type configurationField uint8

const (
	configurationHTTPExposure configurationField = iota + 1
	configurationRequirement
	configurationProvider
	configurationInterfaceRequirement
	configurationImplementationChoice
	configurationInterfacePolicy
	configurationAlias
	configurationConstructorConfig
)

type configurationDecision struct {
	path        string
	digest      string
	removed     bool
	field       configurationField
	id          capabilityid.Identifier
	interfaceID interfaceid.Identifier
	providerID  string
	constructor constructorsymbol.Symbol
	policy      InterfacePolicy
	exposure    HTTPExposure
	alias       Alias
	config      constructorConfigDecision
	source      string
}

func configurationDecisions(manifest Manifest, schemas SchemaLookup) ([]configurationDecision, error) {
	result := make([]configurationDecision, 0)
	for _, exposure := range manifest.httpExposures {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("http.expose[%q]", exposure.id.String()),
			digest:      httpExposureDigest(exposure),
			field:       configurationHTTPExposure,
			interfaceID: exposure.id,
			exposure:    exposure,
			source:      exposure.source,
		})
	}
	for _, removal := range manifest.removedHTTPExposures {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("http.expose[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("http.expose", removal.id, true),
			removed:     true,
			field:       configurationHTTPExposure,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, requirement := range manifest.requirements {
		result = append(result, configurationDecision{
			path:   fmt.Sprintf("capabilities.require[%q]", requirement.id.String()),
			digest: declarationDigest("capabilities.require", requirement.id, false),
			field:  configurationRequirement,
			id:     requirement.id,
			source: requirement.source,
		})
	}
	for _, removal := range manifest.removedRequirements {
		result = append(result, configurationDecision{
			path:    fmt.Sprintf("capabilities.require[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.require", removal.id, true),
			removed: true,
			field:   configurationRequirement,
			id:      removal.id,
			source:  removal.source,
		})
	}
	for _, choice := range manifest.providerChoices {
		result = append(result, configurationDecision{
			path:       fmt.Sprintf("capabilities.use[%q]", choice.capability.String()),
			digest:     digestStrings("capabilities.use", choice.capability.String(), choice.pluginID),
			field:      configurationProvider,
			id:         choice.capability,
			providerID: choice.pluginID,
			source:     choice.source,
		})
	}
	for _, removal := range manifest.removedProviderChoices {
		result = append(result, configurationDecision{
			path:    fmt.Sprintf("capabilities.use[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.use", removal.id, true),
			removed: true,
			field:   configurationProvider,
			id:      removal.id,
			source:  removal.source,
		})
	}
	for _, requirement := range manifest.interfaceRequirements {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("interfaces.require[%q]", requirement.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.require", requirement.id, false),
			field:       configurationInterfaceRequirement,
			interfaceID: requirement.id,
			source:      requirement.source,
		})
	}
	for _, removal := range manifest.removedInterfaceReqs {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("interfaces.require[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.require", removal.id, true),
			removed:     true,
			field:       configurationInterfaceRequirement,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, choice := range manifest.implementationChoices {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("interfaces.use[%q]", choice.interfaceID.String()),
			digest:      implementationChoiceDigest(choice),
			field:       configurationImplementationChoice,
			interfaceID: choice.interfaceID,
			constructor: choice.constructor,
			source:      choice.source,
		})
	}
	for _, removal := range manifest.removedImplementationChoices {
		result = append(result, configurationDecision{
			path:        fmt.Sprintf("interfaces.use[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.use", removal.id, true),
			removed:     true,
			field:       configurationImplementationChoice,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, policy := range manifest.interfacePolicies {
		result = append(result, configurationDecision{
			path:        interfacePolicyPath(policy.interfaceID),
			digest:      interfacePolicyDigest(policy),
			field:       configurationInterfacePolicy,
			interfaceID: policy.interfaceID,
			policy:      policy,
			source:      policy.source,
		})
	}
	for _, removal := range manifest.removedInterfacePolicies {
		result = append(result, configurationDecision{
			path:        interfacePolicyPath(removal.id),
			digest:      interfacePolicyRemovalDigest(removal.id),
			removed:     true,
			field:       configurationInterfacePolicy,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, alias := range manifest.aliases {
		result = append(result, configurationDecision{
			path:   fmt.Sprintf("capabilities.aliases[%q]", alias.id.String()),
			digest: aliasDigest(alias),
			field:  configurationAlias,
			id:     alias.id,
			alias:  alias,
			source: alias.source,
		})
	}
	for _, removal := range manifest.removedAliases {
		result = append(result, configurationDecision{
			path:    fmt.Sprintf("capabilities.aliases[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.aliases", removal.id, true),
			removed: true,
			field:   configurationAlias,
			id:      removal.id,
			source:  removal.source,
		})
	}
	configurations, err := manifestConfigDecisions(manifest, schemas)
	if err != nil {
		return nil, err
	}
	for _, configuration := range configurations {
		result = append(result, configurationDecision{
			path:        constructorConfigPath(configuration.constructor, configuration.segments),
			digest:      constructorConfigPublicDigest(configuration),
			removed:     configuration.kind == constructorConfigRemoval,
			field:       configurationConstructorConfig,
			constructor: configuration.constructor,
			config:      cloneConstructorConfigDecision(configuration),
			source:      configuration.source,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].path < result[right].path })
	return result, nil
}

func setKeyedConfigurationDecision(root *yaml.Node, path []string, key string, value *yaml.Node) error {
	parent, err := ensureMappingPath(root, path)
	if err != nil {
		return err
	}
	setMappingValue(parent, key, value)
	sortYAMLMapping(parent)
	return nil
}

func ensureMappingPath(root *yaml.Node, path []string) (*yaml.Node, error) {
	current := root
	for _, name := range path {
		if current == nil || current.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s is not a mapping", name)
		}
		next := mappingChild(current, name)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(current, name, next)
			sortYAMLMapping(current)
		} else if next.Kind != yaml.MappingNode {
			replacement := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			preserveYAMLComments(replacement, next)
			setMappingValue(current, name, replacement)
			next = replacement
		}
		current = next
	}
	return current, nil
}

func setMappingValue(mapping *yaml.Node, name string, value *yaml.Node) {
	for index := 0; index < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == name {
			preserveYAMLComments(value, mapping.Content[index+1])
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, stringYAMLNode(name), value)
}

func removeMappingValue(mapping *yaml.Node, name string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == name {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
	}
}

func sortYAMLMapping(mapping *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	type pair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	pairs := make([]pair, 0, len(mapping.Content)/2)
	for index := 0; index < len(mapping.Content); index += 2 {
		pairs = append(pairs, pair{key: mapping.Content[index], value: mapping.Content[index+1]})
	}
	sort.SliceStable(pairs, func(left, right int) bool { return pairs[left].key.Value < pairs[right].key.Value })
	mapping.Content = mapping.Content[:0]
	for _, item := range pairs {
		mapping.Content = append(mapping.Content, item.key, item.value)
	}
}

func preserveYAMLComments(target, source *yaml.Node) {
	if target == nil || source == nil {
		return
	}
	target.HeadComment = source.HeadComment
	target.LineComment = source.LineComment
	target.FootComment = source.FootComment
}

func stringYAMLNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func configurationRemovalYAMLNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle, Content: []*yaml.Node{
		stringYAMLNode("$remove"),
		{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
	}}
}

func encodeConfigurationDocument(root *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		_ = encoder.Close()
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func sortProvenance(records []Provenance) {
	sort.Slice(records, func(left, right int) bool {
		if records[left].path != records[right].path {
			return records[left].path < records[right].path
		}
		if records[left].removed != records[right].removed {
			return !records[left].removed
		}
		return records[left].digest < records[right].digest
	})
}
