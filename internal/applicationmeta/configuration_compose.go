package applicationmeta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/kernel/configuration"
	"go.yaml.in/yaml/v3"
)

const (
	maximumConstructorConfigurationDepth = 64
	maximumConstructorConfigurationNodes = 65_536
)

type constructorConfigDecisionKind uint8

const (
	constructorConfigObject constructorConfigDecisionKind = iota + 1
	constructorConfigValue
	constructorConfigRemoval
)

type constructorConfigDecision struct {
	constructor constructorsymbol.Symbol
	segments    []string
	kind        constructorConfigDecisionKind
	valueType   string
	yaml        []byte
	digest      string
	source      string
}

type constructorConfigCandidate struct {
	decision constructorConfigDecision
	sources  map[string]struct{}
}

func composeConstructorConfigurations(dependencies []Dependency, current Manifest, schemas SchemaLookup, records map[string]*provenanceRecord) ([]ConstructorConfiguration, error) {
	inherited := make(map[string]map[string]*constructorConfigCandidate)
	for _, dependency := range dependencies {
		decisions, err := manifestConfigDecisions(dependency.Manifest, schemas)
		if err != nil {
			return nil, fmt.Errorf("dependency %s: %w", dependencyIdentity(dependency), err)
		}
		for _, decision := range decisions {
			decision.source = dependencySource(dependency, decision.source)
			path := constructorConfigPath(decision.constructor, decision.segments)
			addProvenance(records, path, decision.digest, decision.source, decision.kind == constructorConfigRemoval)
			byDecision := inherited[path]
			if byDecision == nil {
				byDecision = make(map[string]*constructorConfigCandidate)
				inherited[path] = byDecision
			}
			key := constructorConfigCandidateKey(decision)
			candidate := byDecision[key]
			if candidate == nil {
				candidate = &constructorConfigCandidate{decision: cloneConstructorConfigDecision(decision), sources: make(map[string]struct{})}
				byDecision[key] = candidate
			}
			candidate.sources[decision.source] = struct{}{}
			if decision.source < candidate.decision.source || decision.source == candidate.decision.source && bytes.Compare(decision.yaml, candidate.decision.yaml) < 0 {
				candidate.decision = cloneConstructorConfigDecision(decision)
			}
		}
	}

	currentDecisions, err := manifestConfigDecisions(current, schemas)
	if err != nil {
		return nil, err
	}
	currentByPath := make(map[string]constructorConfigDecision, len(currentDecisions))
	for _, decision := range currentDecisions {
		currentByPath[constructorConfigPath(decision.constructor, decision.segments)] = decision
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

	selected := make(map[string]constructorConfigDecision, len(orderedPaths))
	for _, path := range orderedPaths {
		prototype, ok := currentByPath[path]
		if !ok {
			for _, candidate := range inherited[path] {
				prototype = candidate.decision
				break
			}
		}
		if suppressedByConstructorConfigAncestor(selected, prototype) {
			continue
		}
		candidates := inherited[path]
		if local, exists := currentByPath[path]; exists {
			if err := validateCurrentConstructorConfigDecision(path, local, candidates); err != nil {
				return nil, err
			}
			selected[path] = cloneConstructorConfigDecision(local)
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedConstructorConfigConflict(path, candidates)
		}
		for _, candidate := range candidates {
			selected[path] = cloneConstructorConfigDecision(candidate.decision)
		}
	}
	return renderConstructorConfigurations(selected)
}

func manifestConfigDecisions(manifest Manifest, schemas SchemaLookup) ([]constructorConfigDecision, error) {
	var result []constructorConfigDecision
	for _, configured := range manifest.Configurations() {
		decisions, err := normalizeConstructorConfigDecisions(configured, schemas)
		if err != nil {
			return nil, err
		}
		result = append(result, decisions...)
	}
	for _, removal := range manifest.removedConfigurations {
		if _, exists := schemas(removal.constructor); !exists {
			return nil, fmt.Errorf("%w for constructor %q at %s", ErrConfigurationSchema, removal.constructor, removal.source)
		}
		result = append(result, newConstructorConfigDecision(removal.constructor, nil, constructorConfigRemoval, "", nil, removal.source))
	}
	sort.Slice(result, func(left, right int) bool {
		return constructorConfigPath(result[left].constructor, result[left].segments) < constructorConfigPath(result[right].constructor, result[right].segments)
	})
	return result, nil
}

func normalizeConstructorConfigDecisions(configured ConstructorConfiguration, schemas SchemaLookup) ([]constructorConfigDecision, error) {
	schema, exists := schemas(configured.constructor)
	if !exists {
		return nil, fmt.Errorf("%w for constructor %q at %s", ErrConfigurationSchema, configured.constructor, configured.source)
	}
	root, err := decodeNormalizedConfigNode(configured.yaml)
	if err != nil || root.Kind != yaml.MappingNode {
		return nil, constructorConfigValueError(configured.constructor, configured.source, nil, ErrConfigurationInvalidValue)
	}
	provided, err := safeConstructorConfigMapping(root)
	if err != nil {
		return nil, constructorConfigValueError(configured.constructor, configured.source, nil, err)
	}
	state := &constructorConfigNormalizeState{}
	result := []constructorConfigDecision{newConstructorConfigDecision(configured.constructor, nil, constructorConfigObject, schema.String(), nil, configured.source)}
	for _, name := range sortedNodeKeys(provided) {
		field, declared := schema.Lookup(name)
		if !declared {
			return nil, constructorConfigValueError(configured.constructor, configured.source, nil, ErrConfigurationUnknownField)
		}
		segments := []string{name}
		source := constructorConfigDecisionSource(configured.source, segments)
		if isNull(provided[name]) {
			result = append(result, newConstructorConfigDecision(configured.constructor, segments, constructorConfigRemoval, "", nil, source))
			continue
		}
		decisions, err := normalizeDeclaredConstructorConfigValue(configured.constructor, segments, field.Value(), provided[name], configured.source, state, 1)
		if err != nil {
			return nil, err
		}
		result = append(result, decisions...)
	}
	return result, nil
}

type constructorConfigNormalizeState struct {
	nodes int
}

func normalizeDeclaredConstructorConfigValue(constructor constructorsymbol.Symbol, segments []string, schema implementationinventory.ConfigurationValue, node *yaml.Node, baseSource string, state *constructorConfigNormalizeState, depth int) ([]constructorConfigDecision, error) {
	objectSchema, object := constructorConfigObjectSchema(schema)
	if object {
		return normalizeConstructorConfigObject(constructor, segments, schema.TypeIdentity(), objectSchema, node, baseSource, state, depth)
	}
	normalized, err := normalizeConstructorConfigNode(schema, node, state, depth)
	if err != nil {
		return nil, constructorConfigValueError(constructor, baseSource, segments, err)
	}
	data, err := marshalConstructorConfigNode(normalized)
	if err != nil {
		return nil, constructorConfigValueError(constructor, baseSource, segments, ErrConfigurationInvalidValue)
	}
	valueType := constructorConfigDeclaredType(schema)
	return []constructorConfigDecision{{
		constructor: constructor,
		segments:    append([]string(nil), segments...),
		kind:        constructorConfigValue,
		valueType:   valueType,
		yaml:        data,
		digest:      digestStrings("config.value", valueType, string(data)),
		source:      constructorConfigDecisionSource(baseSource, segments),
	}}, nil
}

func normalizeConstructorConfigObject(constructor constructorsymbol.Symbol, segments []string, declaredType string, schema implementationinventory.ConfigurationValue, node *yaml.Node, baseSource string, state *constructorConfigNormalizeState, depth int) ([]constructorConfigDecision, error) {
	if err := enterConstructorConfigNode(node, state, depth); err != nil || node.Kind != yaml.MappingNode {
		return nil, constructorConfigValueError(constructor, baseSource, segments, ErrConfigurationInvalidValue)
	}
	provided, err := safeConstructorConfigMapping(node)
	if err != nil {
		return nil, constructorConfigValueError(constructor, baseSource, segments, err)
	}
	result := []constructorConfigDecision{newConstructorConfigDecision(constructor, segments, constructorConfigObject, declaredType, nil, constructorConfigDecisionSource(baseSource, segments))}
	for _, name := range sortedNodeKeys(provided) {
		field, declared := lookupConstructorConfigField(schema.Fields(), name)
		if !declared {
			return nil, constructorConfigValueError(constructor, baseSource, segments, ErrConfigurationUnknownField)
		}
		childSegments := append(append([]string(nil), segments...), name)
		if isNull(provided[name]) {
			result = append(result, newConstructorConfigDecision(constructor, childSegments, constructorConfigRemoval, "", nil, constructorConfigDecisionSource(baseSource, childSegments)))
			continue
		}
		children, err := normalizeDeclaredConstructorConfigValue(constructor, childSegments, field.Value(), provided[name], baseSource, state, depth+1)
		if err != nil {
			return nil, err
		}
		result = append(result, children...)
	}
	return result, nil
}

func normalizeConstructorConfigNode(schema implementationinventory.ConfigurationValue, node *yaml.Node, state *constructorConfigNormalizeState, depth int) (*yaml.Node, error) {
	if err := enterConstructorConfigNode(node, state, depth); err != nil {
		return nil, err
	}
	invalid := func() (*yaml.Node, error) { return nil, ErrConfigurationInvalidValue }
	scalar := func(tag, value string) (*yaml.Node, error) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}, nil
	}
	switch schema.Kind() {
	case implementationinventory.ConfigurationValueString:
		value, err := strictString(node)
		if err != nil {
			return invalid()
		}
		return scalar("!!str", value)
	case implementationinventory.ConfigurationValueBoolean:
		value, err := strictBool(node)
		if err != nil {
			return invalid()
		}
		return scalar("!!bool", strconv.FormatBool(value))
	case implementationinventory.ConfigurationValueSignedInteger:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalConstructorConfigInteger(node.Value) {
			return invalid()
		}
		bits, _ := schema.NumericBits()
		if schema.PlatformSized() {
			bits = 32
		}
		value, err := strconv.ParseInt(node.Value, 10, bits)
		if err != nil {
			return invalid()
		}
		return scalar("!!int", strconv.FormatInt(value, 10))
	case implementationinventory.ConfigurationValueUnsignedInteger:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalConstructorConfigUnsignedInteger(node.Value) {
			return invalid()
		}
		bits, _ := schema.NumericBits()
		if schema.PlatformSized() {
			bits = 32
		}
		value, err := strconv.ParseUint(node.Value, 10, bits)
		if err != nil {
			return invalid()
		}
		return scalar("!!int", strconv.FormatUint(value, 10))
	case implementationinventory.ConfigurationValueNumber:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" && node.Tag != "!!float" {
			return invalid()
		}
		decoder := json.NewDecoder(strings.NewReader(node.Value))
		var value float64
		if err := decoder.Decode(&value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return invalid()
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return invalid()
		}
		bits, _ := schema.NumericBits()
		if bits == 32 {
			value = float64(float32(value))
			if math.IsInf(value, 0) {
				return invalid()
			}
		}
		formatted := strconv.FormatFloat(value, 'g', -1, bits)
		if !strings.ContainsAny(formatted, ".eE") {
			formatted += ".0"
		}
		return scalar("!!float", formatted)
	case implementationinventory.ConfigurationValueDuration:
		value, err := strictString(node)
		if err != nil {
			return invalid()
		}
		duration, err := time.ParseDuration(value)
		if err != nil {
			return invalid()
		}
		return scalar("!!str", duration.String())
	case implementationinventory.ConfigurationValueURL:
		value, err := strictString(node)
		if err != nil {
			return invalid()
		}
		parsed, err := url.Parse(value)
		if err != nil {
			return invalid()
		}
		return scalar("!!str", parsed.String())
	case implementationinventory.ConfigurationValueSecret:
		return normalizeConstructorSecretReference(node)
	case implementationinventory.ConfigurationValueObject:
		return normalizeAtomicConstructorConfigObject(schema, node, state, depth)
	case implementationinventory.ConfigurationValuePointer:
		if isNull(node) {
			return scalar("!!null", "null")
		}
		element, exists := schema.Element()
		if !exists {
			return invalid()
		}
		return normalizeConstructorConfigNode(element, node, state, depth+1)
	case implementationinventory.ConfigurationValueList:
		if node.Kind != yaml.SequenceNode {
			return invalid()
		}
		if length, fixed := schema.ArrayLength(); fixed && int64(len(node.Content)) != length {
			return invalid()
		}
		element, exists := schema.Element()
		if !exists {
			return invalid()
		}
		result := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, child := range node.Content {
			normalized, err := normalizeConstructorConfigNode(element, child, state, depth+1)
			if err != nil {
				return invalid()
			}
			result.Content = append(result.Content, normalized)
		}
		return result, nil
	case implementationinventory.ConfigurationValueMap:
		if node.Kind != yaml.MappingNode {
			return invalid()
		}
		element, exists := schema.Element()
		if !exists {
			return invalid()
		}
		provided, err := safeConstructorConfigMapping(node)
		if err != nil {
			return invalid()
		}
		result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, name := range sortedNodeKeys(provided) {
			normalized, err := normalizeConstructorConfigNode(element, provided[name], state, depth+1)
			if err != nil {
				return invalid()
			}
			result.Content = append(result.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, normalized)
		}
		return result, nil
	default:
		return invalid()
	}
}

func normalizeAtomicConstructorConfigObject(schema implementationinventory.ConfigurationValue, node *yaml.Node, state *constructorConfigNormalizeState, depth int) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, ErrConfigurationInvalidValue
	}
	provided, err := safeConstructorConfigMapping(node)
	if err != nil {
		return nil, err
	}
	result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range sortedNodeKeys(provided) {
		field, declared := lookupConstructorConfigField(schema.Fields(), name)
		if !declared {
			return nil, ErrConfigurationUnknownField
		}
		normalized, err := normalizeConstructorConfigNode(field.Value(), provided[name], state, depth+1)
		if err != nil {
			return nil, err
		}
		result.Content = append(result.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, normalized)
	}
	return result, nil
}

func normalizeConstructorSecretReference(node *yaml.Node) (*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return nil, ErrConfigurationInvalidValue
	}
	kind, err := strictString(node.Content[0])
	if err != nil {
		return nil, ErrConfigurationInvalidValue
	}
	target, err := strictString(node.Content[1])
	if err != nil {
		return nil, ErrConfigurationInvalidValue
	}
	switch kind {
	case "env":
		if _, err := configuration.NewEnvironmentReference(target); err != nil {
			return nil, ErrConfigurationInvalidValue
		}
	case "file":
		if _, err := configuration.NewFileReference(target); err != nil {
			return nil, ErrConfigurationInvalidValue
		}
	default:
		return nil, ErrConfigurationInvalidValue
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: kind},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: target},
	}}, nil
}

func constructorConfigObjectSchema(schema implementationinventory.ConfigurationValue) (implementationinventory.ConfigurationValue, bool) {
	current := schema
	for current.Kind() == implementationinventory.ConfigurationValuePointer {
		element, exists := current.Element()
		if !exists {
			return implementationinventory.ConfigurationValue{}, false
		}
		current = element
	}
	return current, current.Kind() == implementationinventory.ConfigurationValueObject
}

func lookupConstructorConfigField(fields []implementationinventory.ConfigurationField, name string) (implementationinventory.ConfigurationField, bool) {
	index := sort.Search(len(fields), func(index int) bool { return fields[index].Name() >= name })
	if index >= len(fields) || fields[index].Name() != name {
		return implementationinventory.ConfigurationField{}, false
	}
	return fields[index], true
}

func enterConstructorConfigNode(node *yaml.Node, state *constructorConfigNormalizeState, depth int) error {
	if node == nil || state == nil || depth > maximumConstructorConfigurationDepth {
		return ErrConfigurationInvalidValue
	}
	state.nodes++
	if state.nodes > maximumConstructorConfigurationNodes {
		return ErrConfigurationInvalidValue
	}
	return nil
}

func safeConstructorConfigMapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content)%2 != 0 {
		return nil, ErrConfigurationInvalidValue
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		name, err := strictString(node.Content[index])
		if err != nil {
			return nil, ErrConfigurationUnknownField
		}
		if _, duplicate := result[name]; duplicate {
			return nil, ErrConfigurationInvalidValue
		}
		result[name] = node.Content[index+1]
	}
	return result, nil
}

func constructorConfigValueError(constructor constructorsymbol.Symbol, source string, segments []string, reason error) error {
	return fmt.Errorf("%s at %s: %w: %w", constructorConfigPath(constructor, segments), source, ErrConfigurationValues, reason)
}

func newConstructorConfigDecision(constructor constructorsymbol.Symbol, segments []string, kind constructorConfigDecisionKind, valueType string, data []byte, source string) constructorConfigDecision {
	path := constructorConfigPath(constructor, segments)
	kindName := "object"
	if kind == constructorConfigRemoval {
		kindName = "removed"
	}
	return constructorConfigDecision{
		constructor: constructor,
		segments:    append([]string(nil), segments...),
		kind:        kind,
		valueType:   valueType,
		yaml:        append([]byte(nil), data...),
		digest:      digestStrings("config."+kindName, path, valueType),
		source:      source,
	}
}

func constructorConfigDeclaredType(value implementationinventory.ConfigurationValue) string {
	return string(value.Kind()) + ":" + value.TypeIdentity()
}

func canonicalConstructorConfigInteger(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value == "-0" {
		return false
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return false
		}
		start = 1
	}
	if value[start] < '1' || value[start] > '9' {
		return false
	}
	for index := start + 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func canonicalConstructorConfigUnsignedInteger(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func constructorConfigCandidateKey(decision constructorConfigDecision) string {
	return fmt.Sprintf("%d\x00%s\x00%s", decision.kind, decision.valueType, decision.digest)
}

func cloneConstructorConfigDecision(decision constructorConfigDecision) constructorConfigDecision {
	decision.segments = append([]string(nil), decision.segments...)
	decision.yaml = append([]byte(nil), decision.yaml...)
	return decision
}

func suppressedByConstructorConfigAncestor(selected map[string]constructorConfigDecision, decision constructorConfigDecision) bool {
	for length := 0; length < len(decision.segments); length++ {
		ancestor, exists := selected[constructorConfigPath(decision.constructor, decision.segments[:length])]
		if exists && ancestor.kind != constructorConfigObject {
			return true
		}
	}
	return false
}

func validateCurrentConstructorConfigDecision(path string, current constructorConfigDecision, inherited map[string]*constructorConfigCandidate) error {
	if current.kind == constructorConfigRemoval {
		return nil
	}
	for _, candidate := range inherited {
		lower := candidate.decision
		if lower.kind == constructorConfigRemoval {
			continue
		}
		if lower.kind != current.kind || lower.kind == constructorConfigValue && lower.valueType != current.valueType {
			return fmt.Errorf("%w: %s has incompatible lower %s and current %s types from %s and %s", ErrInheritedConflict, path, constructorConfigDecisionDescription(lower), constructorConfigDecisionDescription(current), strings.Join(sortedSet(candidate.sources), ", "), current.source)
		}
	}
	return nil
}

func inheritedConstructorConfigConflict(path string, candidates map[string]*constructorConfigCandidate) error {
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		candidate := candidates[key]
		parts = append(parts, fmt.Sprintf("%s from %s", constructorConfigDecisionDescription(candidate.decision), strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: %s has incompatible declarations: %s; set or remove that exact field in the current Project configuration", ErrInheritedConflict, path, strings.Join(parts, "; "))
}

func constructorConfigDecisionDescription(decision constructorConfigDecision) string {
	switch decision.kind {
	case constructorConfigObject:
		return "object"
	case constructorConfigRemoval:
		return "removal"
	case constructorConfigValue:
		return decision.valueType + " value"
	default:
		return "invalid value"
	}
}

type renderedConstructorConfigNode struct {
	decision *constructorConfigDecision
	children map[string]*renderedConstructorConfigNode
}

func renderConstructorConfigurations(selected map[string]constructorConfigDecision) ([]ConstructorConfiguration, error) {
	roots := renderedConstructorConfigRoots(selected)
	constructors := sortedRenderedConstructors(roots)
	result := make([]ConstructorConfiguration, 0, len(constructors))
	for _, constructor := range constructors {
		root := roots[constructor]
		if root.decision == nil {
			return nil, errors.New("composed constructor configuration has no root decision")
		}
		node, present, err := renderConstructorConfigNode(root, false)
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %v", constructor, err)
		}
		if !present {
			continue
		}
		data, err := marshalConstructorConfigNode(node)
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %v", constructor, err)
		}
		result = append(result, ConstructorConfiguration{constructor: root.decision.constructor, source: root.decision.source, yaml: data})
	}
	return result, nil
}

func renderConstructorConfigurationLayer(selected map[string]constructorConfigDecision) ([]ConstructorConfiguration, []constructorConfigurationRemoval, error) {
	roots := renderedConstructorConfigRoots(selected)
	constructors := sortedRenderedConstructors(roots)
	configurations := make([]ConstructorConfiguration, 0, len(constructors))
	removals := make([]constructorConfigurationRemoval, 0)
	for _, constructor := range constructors {
		root := roots[constructor]
		if root.decision == nil {
			return nil, nil, errors.New("overlaid constructor configuration has no root decision")
		}
		if root.decision.kind == constructorConfigRemoval {
			removals = append(removals, constructorConfigurationRemoval{constructor: root.decision.constructor, source: root.decision.source})
			continue
		}
		node, present, err := renderConstructorConfigNode(root, true)
		if err != nil {
			return nil, nil, fmt.Errorf("config[%q]: %v", constructor, err)
		}
		if !present || node.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("config[%q]: overlaid configuration must remain an object", constructor)
		}
		data, err := marshalConstructorConfigNode(node)
		if err != nil {
			return nil, nil, fmt.Errorf("config[%q]: %v", constructor, err)
		}
		configurations = append(configurations, ConstructorConfiguration{constructor: root.decision.constructor, source: root.decision.source, yaml: data})
	}
	return configurations, removals, nil
}

func renderedConstructorConfigRoots(selected map[string]constructorConfigDecision) map[string]*renderedConstructorConfigNode {
	roots := make(map[string]*renderedConstructorConfigNode)
	for _, decision := range selected {
		key := decision.constructor.String()
		root := roots[key]
		if root == nil {
			root = &renderedConstructorConfigNode{children: make(map[string]*renderedConstructorConfigNode)}
			roots[key] = root
		}
		node := root
		for _, segment := range decision.segments {
			child := node.children[segment]
			if child == nil {
				child = &renderedConstructorConfigNode{children: make(map[string]*renderedConstructorConfigNode)}
				node.children[segment] = child
			}
			node = child
		}
		copy := cloneConstructorConfigDecision(decision)
		node.decision = &copy
	}
	return roots
}

func sortedRenderedConstructors(roots map[string]*renderedConstructorConfigNode) []string {
	constructors := make([]string, 0, len(roots))
	for constructor := range roots {
		constructors = append(constructors, constructor)
	}
	sort.Strings(constructors)
	return constructors
}

func renderConstructorConfigNode(node *renderedConstructorConfigNode, preserveRemoval bool) (*yaml.Node, bool, error) {
	if node == nil || node.decision == nil {
		return nil, false, errors.New("configuration node has no decision")
	}
	switch node.decision.kind {
	case constructorConfigRemoval:
		if preserveRemoval {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, true, nil
		}
		return nil, false, nil
	case constructorConfigValue:
		value, err := decodeNormalizedConfigNode(node.decision.yaml)
		return value, err == nil, err
	case constructorConfigObject:
		result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		names := make([]string, 0, len(node.children))
		for name := range node.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, present, err := renderConstructorConfigNode(node.children[name], preserveRemoval)
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

func marshalConstructorConfigNode(node *yaml.Node) ([]byte, error) {
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

func constructorConfigPath(constructor constructorsymbol.Symbol, segments []string) string {
	var result strings.Builder
	result.WriteString("config[")
	result.WriteString(strconv.Quote(constructor.String()))
	result.WriteByte(']')
	for _, segment := range segments {
		result.WriteByte('[')
		result.WriteString(strconv.Quote(segment))
		result.WriteByte(']')
	}
	return result.String()
}

func constructorConfigDecisionSource(base string, segments []string) string {
	var result strings.Builder
	result.WriteString(base)
	for _, segment := range segments {
		result.WriteByte('[')
		result.WriteString(strconv.Quote(segment))
		result.WriteByte(']')
	}
	return result.String()
}
