package applicationmeta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/kernel/configuration"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

type pluginConfigDecisionKind uint8

const (
	pluginConfigObject pluginConfigDecisionKind = iota + 1
	pluginConfigValue
	pluginConfigRemoval
)

type pluginConfigDecision struct {
	pluginID  string
	segments  []string
	kind      pluginConfigDecisionKind
	valueType string
	yaml      []byte
	digest    string
	source    string
}

type pluginConfigCandidate struct {
	decision pluginConfigDecision
	sources  map[string]struct{}
}

func composePluginConfigurations(dependencies []Dependency, current Manifest, schemas SchemaLookup, records map[string]*provenanceRecord) ([]PluginConfiguration, error) {
	inherited := make(map[string]map[string]*pluginConfigCandidate)
	for _, dependency := range dependencies {
		decisions, err := manifestConfigDecisions(dependency.Manifest, schemas)
		if err != nil {
			return nil, fmt.Errorf("dependency %s: %w", dependencyIdentity(dependency), err)
		}
		for _, decision := range decisions {
			decision.source = dependencySource(dependency, decision.source)
			path := pluginConfigPath(decision.pluginID, decision.segments)
			addProvenance(records, path, decision.digest, decision.source, decision.kind == pluginConfigRemoval)
			byDecision := inherited[path]
			if byDecision == nil {
				byDecision = make(map[string]*pluginConfigCandidate)
				inherited[path] = byDecision
			}
			key := pluginConfigCandidateKey(decision)
			candidate := byDecision[key]
			if candidate == nil {
				candidate = &pluginConfigCandidate{decision: clonePluginConfigDecision(decision), sources: make(map[string]struct{})}
				byDecision[key] = candidate
			}
			candidate.sources[decision.source] = struct{}{}
			if decision.source < candidate.decision.source || decision.source == candidate.decision.source && bytes.Compare(decision.yaml, candidate.decision.yaml) < 0 {
				candidate.decision = clonePluginConfigDecision(decision)
			}
		}
	}

	currentDecisions, err := manifestConfigDecisions(current, schemas)
	if err != nil {
		return nil, err
	}
	currentByPath := make(map[string]pluginConfigDecision, len(currentDecisions))
	for _, decision := range currentDecisions {
		currentByPath[pluginConfigPath(decision.pluginID, decision.segments)] = decision
	}
	paths := make(map[string]struct{}, len(inherited)+len(currentByPath))
	for path := range inherited {
		paths[path] = struct{}{}
	}
	for path := range currentByPath {
		paths[path] = struct{}{}
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)

	selected := make(map[string]pluginConfigDecision, len(orderedPaths))
	for _, path := range orderedPaths {
		prototype, ok := currentByPath[path]
		if !ok {
			for _, candidate := range inherited[path] {
				prototype = candidate.decision
				break
			}
		}
		if suppressedByConfigAncestor(selected, prototype) {
			continue
		}
		candidates := inherited[path]
		if local, exists := currentByPath[path]; exists {
			if err := validateCurrentConfigDecision(path, local, candidates); err != nil {
				return nil, err
			}
			selected[path] = clonePluginConfigDecision(local)
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedPluginConfigConflict(path, candidates)
		}
		for _, candidate := range candidates {
			selected[path] = clonePluginConfigDecision(candidate.decision)
		}
	}
	return renderPluginConfigurations(selected)
}

func manifestConfigDecisions(manifest Manifest, schemas SchemaLookup) ([]pluginConfigDecision, error) {
	var result []pluginConfigDecision
	for _, configured := range manifest.Configurations() {
		decisions, err := normalizePluginConfigDecisions(configured, schemas)
		if err != nil {
			return nil, err
		}
		result = append(result, decisions...)
	}
	for _, removal := range manifest.removedConfigurations {
		if _, exists := schemas(removal.pluginID); !exists {
			return nil, fmt.Errorf("%w for Plugin %q at %s", ErrConfigurationSchema, removal.pluginID, removal.source)
		}
		result = append(result, newPluginConfigDecision(removal.pluginID, nil, pluginConfigRemoval, "", nil, removal.source))
	}
	sort.Slice(result, func(left, right int) bool {
		return pluginConfigPath(result[left].pluginID, result[left].segments) < pluginConfigPath(result[right].pluginID, result[right].segments)
	})
	return result, nil
}

func normalizePluginConfigDecisions(configured PluginConfiguration, schemas SchemaLookup) ([]pluginConfigDecision, error) {
	schema, exists := schemas(configured.pluginID)
	if !exists {
		return nil, fmt.Errorf("%w for Plugin %q at %s", ErrConfigurationSchema, configured.pluginID, configured.source)
	}
	root, err := decodeNormalizedConfigNode(configured.yaml)
	if err != nil || root.Kind != yaml.MappingNode {
		return nil, pluginConfigValueError(configured.pluginID, configured.source, configuration.ErrInvalidValue)
	}
	provided, err := safePluginConfigMapping(root)
	if err != nil {
		return nil, pluginConfigValueError(configured.pluginID, configured.source, err)
	}

	result := []pluginConfigDecision{newPluginConfigDecision(configured.pluginID, nil, pluginConfigObject, "object", nil, configured.source)}
	nonNull := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range sortedNodeKeys(provided) {
		if _, exists := schema.Lookup(name); !exists {
			return nil, pluginConfigValueError(configured.pluginID, configured.source, configuration.ErrUnknownField)
		}
		if isNull(provided[name]) {
			segments := []string{name}
			result = append(result, newPluginConfigDecision(configured.pluginID, segments, pluginConfigRemoval, "", nil, pluginConfigDecisionSource(configured.source, segments)))
			continue
		}
		nonNull.Content = append(nonNull.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			provided[name],
		)
	}
	data, err := yaml.Marshal(nonNull)
	if err != nil {
		return nil, pluginConfigValueError(configured.pluginID, configured.source, configuration.ErrInvalidValue)
	}
	partial, err := configuration.NormalizePartial(schema, data)
	if err != nil {
		return nil, fmt.Errorf("config[%q] at %s: %w", configured.pluginID, configured.source, err)
	}
	for _, name := range partial.Names() {
		field, _ := schema.Lookup(name)
		fieldYAML, _ := partial.YAML(name)
		fieldDigest, _ := partial.Digest(name)
		segments := []string{name}
		source := pluginConfigDecisionSource(configured.source, segments)
		if field.Type() != kernelmanifest.ConfigObject {
			result = append(result, pluginConfigDecision{
				pluginID:  configured.pluginID,
				segments:  segments,
				kind:      pluginConfigValue,
				valueType: pluginConfigDeclaredType(field),
				yaml:      append([]byte(nil), fieldYAML...),
				digest:    fieldDigest,
				source:    source,
			})
			continue
		}
		node, err := decodeNormalizedConfigNode(fieldYAML)
		if err != nil || node.Kind != yaml.MappingNode {
			return nil, pluginConfigValueError(configured.pluginID, configured.source, configuration.ErrInvalidValue)
		}
		decisions, err := flattenPluginConfigObject(configured.pluginID, segments, node, configured.source)
		if err != nil {
			return nil, pluginConfigValueError(configured.pluginID, configured.source, configuration.ErrInvalidValue)
		}
		result = append(result, decisions...)
	}
	return result, nil
}

func flattenPluginConfigObject(pluginID string, segments []string, node *yaml.Node, baseSource string) ([]pluginConfigDecision, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, errors.New("configuration object is not a mapping")
	}
	result := []pluginConfigDecision{newPluginConfigDecision(pluginID, segments, pluginConfigObject, "object", nil, pluginConfigDecisionSource(baseSource, segments))}
	for index := 0; index < len(node.Content); index += 2 {
		name, err := strictString(node.Content[index])
		if err != nil {
			return nil, err
		}
		child := node.Content[index+1]
		childSegments := append(append([]string(nil), segments...), name)
		source := pluginConfigDecisionSource(baseSource, childSegments)
		switch {
		case isNull(child):
			result = append(result, newPluginConfigDecision(pluginID, childSegments, pluginConfigRemoval, "", nil, source))
		case child.Kind == yaml.MappingNode:
			children, err := flattenPluginConfigObject(pluginID, childSegments, child, baseSource)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
		default:
			yamlValue, err := yaml.Marshal(child)
			if err != nil {
				return nil, err
			}
			valueType, err := pluginConfigNodeType(child)
			if err != nil {
				return nil, err
			}
			canonical, err := canonicalPluginConfigNode(child)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return nil, err
			}
			result = append(result, pluginConfigDecision{
				pluginID:  pluginID,
				segments:  childSegments,
				kind:      pluginConfigValue,
				valueType: valueType,
				yaml:      append([]byte(nil), yamlValue...),
				digest:    digestStrings("config.value", valueType, string(encoded)),
				source:    source,
			})
		}
	}
	return result, nil
}

func safePluginConfigMapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, configuration.ErrInvalidValue
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		name, err := strictString(node.Content[index])
		if err != nil {
			return nil, configuration.ErrUnknownField
		}
		if _, duplicate := result[name]; duplicate {
			return nil, configuration.ErrInvalidValue
		}
		result[name] = node.Content[index+1]
	}
	return result, nil
}

func pluginConfigValueError(pluginID, source string, reason error) error {
	return fmt.Errorf("config[%q] at %s: %w: %w", pluginID, source, configuration.ErrInvalidValues, reason)
}

func newPluginConfigDecision(pluginID string, segments []string, kind pluginConfigDecisionKind, valueType string, data []byte, source string) pluginConfigDecision {
	path := pluginConfigPath(pluginID, segments)
	kindName := "object"
	if kind == pluginConfigRemoval {
		kindName = "removed"
	}
	return pluginConfigDecision{
		pluginID:  pluginID,
		segments:  append([]string(nil), segments...),
		kind:      kind,
		valueType: valueType,
		yaml:      append([]byte(nil), data...),
		digest:    digestStrings("config."+kindName, path),
		source:    source,
	}
}

func pluginConfigDeclaredType(field kernelmanifest.ConfigField) string {
	if field.Type() == kernelmanifest.ConfigArray {
		return string(field.Type()) + ":" + string(field.Items())
	}
	return string(field.Type())
}

func pluginConfigNodeType(node *yaml.Node) (string, error) {
	if node == nil {
		return "", errors.New("configuration value is absent")
	}
	switch node.Kind {
	case yaml.SequenceNode:
		return "array", nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return "string", nil
		case "!!bool":
			return "boolean", nil
		case "!!int", "!!float":
			return "number", nil
		default:
			return "", errors.New("unsupported configuration scalar")
		}
	default:
		return "", errors.New("unsupported configuration value")
	}
}

func canonicalPluginConfigNode(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, errors.New("configuration value is absent")
	}
	switch node.Kind {
	case yaml.MappingNode:
		result := make(map[string]any, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			name, err := strictString(node.Content[index])
			if err != nil {
				return nil, err
			}
			value, err := canonicalPluginConfigNode(node.Content[index+1])
			if err != nil {
				return nil, err
			}
			result[name] = value
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]any, len(node.Content))
		for index, child := range node.Content {
			value, err := canonicalPluginConfigNode(child)
			if err != nil {
				return nil, err
			}
			result[index] = value
		}
		return result, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!str":
			return node.Value, nil
		case "!!bool":
			return node.Value == "true", nil
		case "!!int":
			return strconv.ParseInt(node.Value, 10, 64)
		case "!!float":
			return strconv.ParseFloat(node.Value, 64)
		default:
			return nil, errors.New("unsupported configuration scalar")
		}
	default:
		return nil, errors.New("unsupported configuration node")
	}
}

func pluginConfigCandidateKey(decision pluginConfigDecision) string {
	return fmt.Sprintf("%d\x00%s\x00%s", decision.kind, decision.valueType, decision.digest)
}

func clonePluginConfigDecision(decision pluginConfigDecision) pluginConfigDecision {
	decision.segments = append([]string(nil), decision.segments...)
	decision.yaml = append([]byte(nil), decision.yaml...)
	return decision
}

func suppressedByConfigAncestor(selected map[string]pluginConfigDecision, decision pluginConfigDecision) bool {
	for length := 0; length < len(decision.segments); length++ {
		ancestor, exists := selected[pluginConfigPath(decision.pluginID, decision.segments[:length])]
		if exists && ancestor.kind != pluginConfigObject {
			return true
		}
	}
	return false
}

func validateCurrentConfigDecision(path string, current pluginConfigDecision, inherited map[string]*pluginConfigCandidate) error {
	if current.kind == pluginConfigRemoval {
		return nil
	}
	for _, candidate := range inherited {
		lower := candidate.decision
		if lower.kind == pluginConfigRemoval {
			continue
		}
		if lower.kind != current.kind || lower.kind == pluginConfigValue && lower.valueType != current.valueType {
			return fmt.Errorf("%w: %s has incompatible lower %s and current %s types from %s and %s", ErrInheritedConflict, path, pluginConfigDecisionDescription(lower), pluginConfigDecisionDescription(current), strings.Join(sortedSet(candidate.sources), ", "), current.source)
		}
	}
	return nil
}

func inheritedPluginConfigConflict(path string, candidates map[string]*pluginConfigCandidate) error {
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		candidate := candidates[key]
		parts = append(parts, fmt.Sprintf("%s from %s", pluginConfigDecisionDescription(candidate.decision), strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: %s has incompatible declarations: %s; set or remove that exact field in the current Project configuration", ErrInheritedConflict, path, strings.Join(parts, "; "))
}

func pluginConfigDecisionDescription(decision pluginConfigDecision) string {
	switch decision.kind {
	case pluginConfigObject:
		return "object"
	case pluginConfigRemoval:
		return "removal"
	case pluginConfigValue:
		return decision.valueType + " value"
	default:
		return "invalid value"
	}
}

type renderedPluginConfigNode struct {
	decision *pluginConfigDecision
	children map[string]*renderedPluginConfigNode
}

func renderPluginConfigurations(selected map[string]pluginConfigDecision) ([]PluginConfiguration, error) {
	roots := make(map[string]*renderedPluginConfigNode)
	for _, decision := range selected {
		root := roots[decision.pluginID]
		if root == nil {
			root = &renderedPluginConfigNode{children: make(map[string]*renderedPluginConfigNode)}
			roots[decision.pluginID] = root
		}
		node := root
		for _, segment := range decision.segments {
			child := node.children[segment]
			if child == nil {
				child = &renderedPluginConfigNode{children: make(map[string]*renderedPluginConfigNode)}
				node.children[segment] = child
			}
			node = child
		}
		copy := clonePluginConfigDecision(decision)
		node.decision = &copy
	}

	pluginIDs := make([]string, 0, len(roots))
	for pluginID := range roots {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	result := make([]PluginConfiguration, 0, len(pluginIDs))
	for _, pluginID := range pluginIDs {
		root := roots[pluginID]
		if root.decision == nil {
			return nil, errors.New("composed Plugin configuration has no root decision")
		}
		node, present, err := renderPluginConfigNode(root)
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %v", pluginID, err)
		}
		if !present {
			continue
		}
		data, err := marshalPluginConfigNode(node)
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %v", pluginID, err)
		}
		result = append(result, PluginConfiguration{pluginID: pluginID, source: root.decision.source, yaml: data})
	}
	return result, nil
}

func renderPluginConfigNode(node *renderedPluginConfigNode) (*yaml.Node, bool, error) {
	if node == nil || node.decision == nil {
		return nil, false, errors.New("configuration node has no decision")
	}
	switch node.decision.kind {
	case pluginConfigRemoval:
		return nil, false, nil
	case pluginConfigValue:
		value, err := decodeNormalizedConfigNode(node.decision.yaml)
		return value, err == nil, err
	case pluginConfigObject:
		result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		names := make([]string, 0, len(node.children))
		for name := range node.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, present, err := renderPluginConfigNode(node.children[name])
			if err != nil {
				return nil, false, err
			}
			if !present {
				continue
			}
			result.Content = append(result.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
				child,
			)
		}
		return result, true, nil
	default:
		return nil, false, errors.New("configuration node has invalid decision")
	}
}

func decodeNormalizedConfigNode(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, errors.New("normalized configuration is not one YAML document")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("normalized configuration has trailing YAML")
	}
	return document.Content[0], nil
}

func marshalPluginConfigNode(node *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		_ = encoder.Close()
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func pluginConfigPath(pluginID string, segments []string) string {
	var result strings.Builder
	result.WriteString("config[")
	result.WriteString(strconv.Quote(pluginID))
	result.WriteByte(']')
	for _, segment := range segments {
		result.WriteByte('[')
		result.WriteString(strconv.Quote(segment))
		result.WriteByte(']')
	}
	return result.String()
}

func pluginConfigDecisionSource(base string, segments []string) string {
	var result strings.Builder
	result.WriteString(base)
	for _, segment := range segments {
		result.WriteByte('[')
		result.WriteString(strconv.Quote(segment))
		result.WriteByte(']')
	}
	return result.String()
}
