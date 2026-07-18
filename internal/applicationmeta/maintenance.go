package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrMaintainConfiguration reports failure to preserve current-project
	// intent while the dependency-derived configuration baseline changes.
	ErrMaintainConfiguration = errors.New("maintain dependency-derived project configuration")
	// ErrAmbiguousConfigurationOwnership reports a prior inherited decision
	// whose authored representation disappeared without an explicit tombstone.
	ErrAmbiguousConfigurationOwnership = errors.New("ambiguous current-project configuration ownership")
)

type maintenanceField uint8

const (
	maintenanceHTTPExposure maintenanceField = iota + 1
	maintenanceRequirement
	maintenanceProvider
	maintenanceAlias
	maintenancePluginConfig
)

type maintenanceDecision struct {
	path       string
	digest     string
	removed    bool
	field      maintenanceField
	id         capabilityid.Identifier
	pluginID   string
	providerID string
	alias      Alias
	config     pluginConfigDecision
	source     string
}

type maintenanceCandidate struct {
	decision maintenanceDecision
	sources  map[string]struct{}
}

// ConfigurationMaintenance is one immutable, comment-preserving planned
// update of the selected current-project document.
type ConfigurationMaintenance struct {
	data    []byte
	changed bool
}

// Data returns defensive planned YAML bytes.
func (m ConfigurationMaintenance) Data() []byte { return append([]byte(nil), m.data...) }

// Changed reports whether dependency recomposition changes the selected
// current-project document.
func (m ConfigurationMaintenance) Changed() bool { return m.changed }

// MaintainDependencyConfiguration performs a typed three-way merge using the
// prior generated dependency baseline, the developer's current YAML, and the
// newly discovered dependency Projects. A zero previous baseline means no
// generated baseline has been recorded yet; existing values are then treated
// as current-project decisions and missing compatible dependency values are
// introduced without overwriting them.
func MaintainDependencyConfiguration(data []byte, previous DependencyBaseline, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	if schemas == nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: schema lookup is nil", ErrMaintainConfiguration)
	}
	currentManifest, err := Parse(data)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: current Project configuration: %w", ErrMaintainConfiguration, err)
	}
	current, err := maintenanceDecisions(currentManifest, schemas)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: current Project configuration: %w", ErrMaintainConfiguration, err)
	}
	currentByPath, err := indexMaintenanceDecisions(current)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	newCandidates, err := dependencyMaintenanceCandidates(dependencies, schemas)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	oldCandidates := make(map[string]map[string]BaselineRecord)
	if previous.Valid() {
		for _, record := range previous.Records() {
			if !supportedMaintenancePath(record.Path) {
				return ConfigurationMaintenance{}, fmt.Errorf("%w: prior baseline path %q has no typed composition rule", ErrMaintainConfiguration, record.Path)
			}
			byDecision := oldCandidates[record.Path]
			if byDecision == nil {
				byDecision = make(map[string]BaselineRecord)
				oldCandidates[record.Path] = byDecision
			}
			byDecision[maintenanceRecordKey(record.Digest, record.Removed)] = record
		}
	}

	local := make(map[string]maintenanceDecision)
	for path, decision := range currentByPath {
		old := oldCandidates[path]
		if len(old) != 1 || !baselineMatchesDecision(old, decision) {
			local[path] = cloneMaintenanceDecision(decision)
		}
	}
	propagateLocalConfigObjects(currentByPath, local)
	for path, old := range oldCandidates {
		if _, exists := currentByPath[path]; exists {
			continue
		}
		if suppressedByMaintenanceAncestor(local, path) {
			continue
		}
		sources := baselineSources(old)
		return ConfigurationMaintenance{}, fmt.Errorf(
			"%w: %w: inherited %s from %s is absent; restore it or add an explicit typed removal",
			ErrMaintainConfiguration,
			ErrAmbiguousConfigurationOwnership,
			path,
			strings.Join(sources, ", "),
		)
	}

	target := make(map[string]maintenanceDecision)
	paths := sortedMaintenanceCandidatePaths(newCandidates)
	for _, path := range paths {
		candidates := newCandidates[path]
		if len(candidates) != 1 {
			if _, resolved := local[path]; resolved || suppressedByMaintenanceAncestor(local, path) {
				continue
			}
			return ConfigurationMaintenance{}, dependencyMaintenanceConflict(path, candidates)
		}
		for _, candidate := range candidates {
			target[path] = cloneMaintenanceDecision(candidate.decision)
		}
	}
	for path, decision := range local {
		target[path] = cloneMaintenanceDecision(decision)
	}
	target = suppressMaintenanceDescendants(target)

	if maintenanceDecisionMapsEqual(currentByPath, target) {
		return ConfigurationMaintenance{data: append([]byte(nil), data...)}, nil
	}
	document, err := decodeDocument(data)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: decode current Project configuration: %w", ErrMaintainConfiguration, err)
	}
	if err := applyMaintenanceDecisions(document, currentByPath, target); err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: update current Project configuration: %w", ErrMaintainConfiguration, err)
	}
	updated, err := encodeMaintainedDocument(document)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: encode current Project configuration: %w", ErrMaintainConfiguration, err)
	}
	if len(updated) > MaximumSize {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: updated Project configuration exceeds %d bytes", ErrMaintainConfiguration, MaximumSize)
	}
	afterManifest, err := Parse(updated)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: validate updated Project configuration: %w", ErrMaintainConfiguration, err)
	}
	after, err := maintenanceDecisions(afterManifest, schemas)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: normalize updated Project configuration: %w", ErrMaintainConfiguration, err)
	}
	afterByPath, err := indexMaintenanceDecisions(after)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	if !maintenanceDecisionMapsEqual(afterByPath, target) {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: updated Project configuration does not represent the planned typed decisions", ErrMaintainConfiguration)
	}
	if !sameCurrentProjectProcessSettings(currentManifest, afterManifest) {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: updated Project configuration changed current-project process settings", ErrMaintainConfiguration)
	}
	return ConfigurationMaintenance{data: append([]byte(nil), updated...), changed: !bytes.Equal(data, updated)}, nil
}

func maintenanceDecisions(manifest Manifest, schemas SchemaLookup) ([]maintenanceDecision, error) {
	result := make([]maintenanceDecision, 0)
	for _, exposure := range manifest.httpExposures {
		result = append(result, maintenanceDecision{
			path:   fmt.Sprintf("http.expose[%q]", exposure.id.String()),
			digest: declarationDigest("http.expose", exposure.id, false),
			field:  maintenanceHTTPExposure,
			id:     exposure.id,
			source: exposure.source,
		})
	}
	for _, removal := range manifest.removedHTTPExposures {
		result = append(result, maintenanceDecision{
			path:    fmt.Sprintf("http.expose[%q]", removal.id.String()),
			digest:  declarationDigest("http.expose", removal.id, true),
			removed: true,
			field:   maintenanceHTTPExposure,
			id:      removal.id,
			source:  removal.source,
		})
	}
	for _, requirement := range manifest.requirements {
		result = append(result, maintenanceDecision{
			path:   fmt.Sprintf("capabilities.require[%q]", requirement.id.String()),
			digest: declarationDigest("capabilities.require", requirement.id, false),
			field:  maintenanceRequirement,
			id:     requirement.id,
			source: requirement.source,
		})
	}
	for _, removal := range manifest.removedRequirements {
		result = append(result, maintenanceDecision{
			path:    fmt.Sprintf("capabilities.require[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.require", removal.id, true),
			removed: true,
			field:   maintenanceRequirement,
			id:      removal.id,
			source:  removal.source,
		})
	}
	for _, choice := range manifest.providerChoices {
		result = append(result, maintenanceDecision{
			path:       fmt.Sprintf("capabilities.use[%q]", choice.capability.String()),
			digest:     digestStrings("capabilities.use", choice.capability.String(), choice.pluginID),
			field:      maintenanceProvider,
			id:         choice.capability,
			providerID: choice.pluginID,
			source:     choice.source,
		})
	}
	for _, removal := range manifest.removedProviderChoices {
		result = append(result, maintenanceDecision{
			path:    fmt.Sprintf("capabilities.use[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.use", removal.id, true),
			removed: true,
			field:   maintenanceProvider,
			id:      removal.id,
			source:  removal.source,
		})
	}
	for _, alias := range manifest.aliases {
		result = append(result, maintenanceDecision{
			path:   fmt.Sprintf("capabilities.aliases[%q]", alias.id.String()),
			digest: aliasDigest(alias),
			field:  maintenanceAlias,
			id:     alias.id,
			alias:  alias,
			source: alias.source,
		})
	}
	for _, removal := range manifest.removedAliases {
		result = append(result, maintenanceDecision{
			path:    fmt.Sprintf("capabilities.aliases[%q]", removal.id.String()),
			digest:  declarationDigest("capabilities.aliases", removal.id, true),
			removed: true,
			field:   maintenanceAlias,
			id:      removal.id,
			source:  removal.source,
		})
	}
	configurations, err := manifestConfigDecisions(manifest, schemas)
	if err != nil {
		return nil, err
	}
	for _, configuration := range configurations {
		result = append(result, maintenanceDecision{
			path:     pluginConfigPath(configuration.pluginID, configuration.segments),
			digest:   configuration.digest,
			removed:  configuration.kind == pluginConfigRemoval,
			field:    maintenancePluginConfig,
			pluginID: configuration.pluginID,
			config:   clonePluginConfigDecision(configuration),
			source:   configuration.source,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].path < result[right].path })
	return result, nil
}

func dependencyMaintenanceCandidates(dependencies []Dependency, schemas SchemaLookup) (map[string]map[string]*maintenanceCandidate, error) {
	ordered := append([]Dependency(nil), dependencies...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].ModulePath != ordered[right].ModulePath {
			return ordered[left].ModulePath < ordered[right].ModulePath
		}
		return ordered[left].ModuleVersion < ordered[right].ModuleVersion
	})
	result := make(map[string]map[string]*maintenanceCandidate)
	for _, dependency := range ordered {
		decisions, err := maintenanceDecisions(dependency.Manifest, schemas)
		if err != nil {
			return nil, fmt.Errorf("dependency %s: %w", dependencyIdentity(dependency), err)
		}
		for _, decision := range decisions {
			source := dependencySource(dependency, decision.source)
			byDecision := result[decision.path]
			if byDecision == nil {
				byDecision = make(map[string]*maintenanceCandidate)
				result[decision.path] = byDecision
			}
			key := maintenanceDecisionKey(decision)
			candidate := byDecision[key]
			if candidate == nil {
				candidate = &maintenanceCandidate{decision: cloneMaintenanceDecision(decision), sources: make(map[string]struct{})}
				byDecision[key] = candidate
			}
			candidate.sources[source] = struct{}{}
			if candidate.decision.source == "" || source < candidate.decision.source {
				candidate.decision.source = source
			}
		}
	}
	return result, nil
}

func indexMaintenanceDecisions(decisions []maintenanceDecision) (map[string]maintenanceDecision, error) {
	result := make(map[string]maintenanceDecision, len(decisions))
	for _, decision := range decisions {
		if previous, duplicate := result[decision.path]; duplicate {
			return nil, fmt.Errorf("typed configuration path %s has both %s and %s", decision.path, maintenanceDecisionDescription(previous), maintenanceDecisionDescription(decision))
		}
		result[decision.path] = cloneMaintenanceDecision(decision)
	}
	return result, nil
}

func maintenanceDecisionKey(decision maintenanceDecision) string {
	return maintenanceRecordKey(decision.digest, decision.removed)
}

func maintenanceRecordKey(digest string, removed bool) string {
	return fmt.Sprintf("%s\x00%t", digest, removed)
}

func baselineMatchesDecision(records map[string]BaselineRecord, decision maintenanceDecision) bool {
	_, exists := records[maintenanceDecisionKey(decision)]
	return exists
}

func cloneMaintenanceDecision(decision maintenanceDecision) maintenanceDecision {
	decision.config = clonePluginConfigDecision(decision.config)
	return decision
}

func propagateLocalConfigObjects(current, local map[string]maintenanceDecision) {
	changed := true
	for changed {
		changed = false
		for path, decision := range current {
			if decision.field != maintenancePluginConfig || decision.config.kind != pluginConfigObject {
				continue
			}
			if _, exists := local[path]; exists {
				continue
			}
			for localPath := range local {
				if strings.HasPrefix(localPath, path+"[") {
					local[path] = cloneMaintenanceDecision(decision)
					changed = true
					break
				}
			}
		}
	}
}

func suppressedByMaintenanceAncestor(decisions map[string]maintenanceDecision, path string) bool {
	if !strings.HasPrefix(path, "config[") {
		return false
	}
	for candidatePath, candidate := range decisions {
		if candidate.field != maintenancePluginConfig || candidate.config.kind == pluginConfigObject {
			continue
		}
		if strings.HasPrefix(path, candidatePath+"[") {
			return true
		}
	}
	return false
}

func suppressMaintenanceDescendants(decisions map[string]maintenanceDecision) map[string]maintenanceDecision {
	paths := make([]string, 0, len(decisions))
	for path := range decisions {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(left, right int) bool {
		leftDepth := strings.Count(paths[left], "[")
		rightDepth := strings.Count(paths[right], "[")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return paths[left] < paths[right]
	})
	result := make(map[string]maintenanceDecision, len(decisions))
	for _, path := range paths {
		if suppressedByMaintenanceAncestor(result, path) {
			continue
		}
		result[path] = cloneMaintenanceDecision(decisions[path])
	}
	return result
}

func sortedMaintenanceCandidatePaths(values map[string]map[string]*maintenanceCandidate) []string {
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func dependencyMaintenanceConflict(path string, candidates map[string]*maintenanceCandidate) error {
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		candidate := candidates[key]
		parts = append(parts, fmt.Sprintf("%s from %s", maintenanceDecisionDescription(candidate.decision), strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: %s has incompatible dependency declarations: %s; set or remove that exact field in the current Project configuration", ErrInheritedConflict, path, strings.Join(parts, "; "))
}

func maintenanceDecisionDescription(decision maintenanceDecision) string {
	if decision.removed {
		return "removal"
	}
	switch decision.field {
	case maintenanceHTTPExposure, maintenanceRequirement:
		return "addition"
	case maintenanceProvider:
		return "Provider " + decision.providerID
	case maintenanceAlias:
		return "Alias target " + decision.alias.target.String()
	case maintenancePluginConfig:
		return pluginConfigDecisionDescription(decision.config)
	default:
		return "invalid decision"
	}
}

func baselineSources(records map[string]BaselineRecord) []string {
	set := make(map[string]struct{})
	for _, record := range records {
		for _, source := range record.Sources {
			set[source] = struct{}{}
		}
	}
	return sortedSet(set)
}

func supportedMaintenancePath(path string) bool {
	return strings.HasPrefix(path, "http.expose[") ||
		strings.HasPrefix(path, "capabilities.require[") ||
		strings.HasPrefix(path, "capabilities.use[") ||
		strings.HasPrefix(path, "capabilities.aliases[") ||
		strings.HasPrefix(path, "config[")
}

func maintenanceDecisionMapsEqual(left, right map[string]maintenanceDecision) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftDecision := range left {
		rightDecision, exists := right[path]
		if !exists || maintenanceDecisionKey(leftDecision) != maintenanceDecisionKey(rightDecision) || leftDecision.field != rightDecision.field {
			return false
		}
	}
	return true
}

func sameCurrentProjectProcessSettings(left, right Manifest) bool {
	leftAddress, leftHasAddress := left.HTTPAddress()
	rightAddress, rightHasAddress := right.HTTPAddress()
	return leftAddress == rightAddress && leftHasAddress == rightHasAddress &&
		left.removeHTTPAddress == right.removeHTTPAddress &&
		left.StartupTimeout() == right.StartupTimeout() &&
		left.removeStartupTimeout == right.removeStartupTimeout
}

func applyMaintenanceDecisions(root *yaml.Node, current, target map[string]maintenanceDecision) error {
	removePaths := make([]string, 0)
	setPaths := make([]string, 0)
	for path, decision := range current {
		targetDecision, exists := target[path]
		if !exists {
			removePaths = append(removePaths, path)
			continue
		}
		if maintenanceDecisionKey(decision) != maintenanceDecisionKey(targetDecision) || decision.field != targetDecision.field {
			setPaths = append(setPaths, path)
		}
	}
	for path := range target {
		if _, exists := current[path]; !exists {
			setPaths = append(setPaths, path)
		}
	}
	sort.Slice(removePaths, func(left, right int) bool {
		leftDepth := strings.Count(removePaths[left], "[")
		rightDepth := strings.Count(removePaths[right], "[")
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return removePaths[left] > removePaths[right]
	})
	for _, path := range removePaths {
		if err := removeMaintenanceDecision(root, current[path]); err != nil {
			return err
		}
	}
	sort.Slice(setPaths, func(left, right int) bool {
		leftDepth := strings.Count(setPaths[left], "[")
		rightDepth := strings.Count(setPaths[right], "[")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return setPaths[left] < setPaths[right]
	})
	for _, path := range setPaths {
		if err := setMaintenanceDecision(root, target[path]); err != nil {
			return err
		}
	}
	return nil
}

func removeMaintenanceDecision(root *yaml.Node, decision maintenanceDecision) error {
	switch decision.field {
	case maintenanceHTTPExposure:
		return removeSetMaintenanceDecision(root, []string{"http", "expose"}, decision.id.String(), decision.removed)
	case maintenanceRequirement:
		return removeSetMaintenanceDecision(root, []string{"capabilities", "require"}, decision.id.String(), decision.removed)
	case maintenanceProvider:
		return removeKeyedMaintenanceDecision(root, []string{"capabilities", "use"}, decision.id.String())
	case maintenanceAlias:
		return removeKeyedMaintenanceDecision(root, []string{"capabilities", "aliases"}, decision.id.String())
	case maintenancePluginConfig:
		return removeConfigMaintenanceDecision(root, decision.config)
	default:
		return errors.New("unknown configuration decision field")
	}
}

func setMaintenanceDecision(root *yaml.Node, decision maintenanceDecision) error {
	switch decision.field {
	case maintenanceHTTPExposure:
		return setSetMaintenanceDecision(root, []string{"http", "expose"}, decision.id.String(), decision.removed)
	case maintenanceRequirement:
		return setSetMaintenanceDecision(root, []string{"capabilities", "require"}, decision.id.String(), decision.removed)
	case maintenanceProvider:
		value := nullYAMLNode()
		if !decision.removed {
			value = stringYAMLNode(decision.providerID)
		}
		return setKeyedMaintenanceDecision(root, []string{"capabilities", "use"}, decision.id.String(), value)
	case maintenanceAlias:
		value := nullYAMLNode()
		if !decision.removed {
			value = aliasYAMLNode(decision.alias)
		}
		return setKeyedMaintenanceDecision(root, []string{"capabilities", "aliases"}, decision.id.String(), value)
	case maintenancePluginConfig:
		return setConfigMaintenanceDecision(root, decision.config)
	default:
		return errors.New("unknown configuration decision field")
	}
}

func setSetMaintenanceDecision(root *yaml.Node, path []string, value string, removed bool) error {
	parent, err := ensureMappingPath(root, path[:len(path)-1])
	if err != nil {
		return err
	}
	fieldName := path[len(path)-1]
	field := mappingChild(parent, fieldName)
	if field == nil {
		if removed {
			field = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(parent, fieldName, field)
		} else {
			field = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			setMappingValue(parent, fieldName, field)
		}
	}
	if field.Kind == yaml.SequenceNode && removed {
		add := cloneYAMLNode(field)
		field.Kind = yaml.MappingNode
		field.Tag = "!!map"
		field.Value = ""
		field.Content = nil
		setMappingValue(field, "add", add)
		setMappingValue(field, "remove", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})
	}
	if field.Kind == yaml.SequenceNode {
		removeSequenceValue(field, value)
		field.Content = append(field.Content, stringYAMLNode(value))
		sortYAMLStringSequence(field)
		return nil
	}
	if field.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must remain a sequence or sparse set mapping", strings.Join(path, "."))
	}
	desired := "add"
	opposite := "remove"
	if removed {
		desired, opposite = opposite, desired
	}
	if oppositeNode := mappingChild(field, opposite); oppositeNode != nil {
		removeSequenceValue(oppositeNode, value)
	}
	sequence := mappingChild(field, desired)
	if sequence == nil {
		sequence = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMappingValue(field, desired, sequence)
	}
	if sequence.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s.%s must remain a sequence", strings.Join(path, "."), desired)
	}
	removeSequenceValue(sequence, value)
	sequence.Content = append(sequence.Content, stringYAMLNode(value))
	sortYAMLStringSequence(sequence)
	return nil
}

func removeSetMaintenanceDecision(root *yaml.Node, path []string, value string, removed bool) error {
	field := mappingPath(root, path)
	if field == nil {
		return nil
	}
	if field.Kind == yaml.SequenceNode {
		if !removed {
			removeSequenceValue(field, value)
		}
		return nil
	}
	if field.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must remain a sequence or sparse set mapping", strings.Join(path, "."))
	}
	name := "add"
	if removed {
		name = "remove"
	}
	removeSequenceValue(mappingChild(field, name), value)
	return nil
}

func setKeyedMaintenanceDecision(root *yaml.Node, path []string, key string, value *yaml.Node) error {
	parent, err := ensureMappingPath(root, path)
	if err != nil {
		return err
	}
	setMappingValue(parent, key, value)
	sortYAMLMapping(parent)
	return nil
}

func removeKeyedMaintenanceDecision(root *yaml.Node, path []string, key string) error {
	parent := mappingPath(root, path)
	if parent == nil {
		return nil
	}
	removeMappingValue(parent, key)
	return nil
}

func setConfigMaintenanceDecision(root *yaml.Node, decision pluginConfigDecision) error {
	path := append([]string{"config", decision.pluginID}, decision.segments...)
	parent, err := ensureMappingPath(root, path[:len(path)-1])
	if err != nil {
		return err
	}
	name := path[len(path)-1]
	var value *yaml.Node
	switch decision.kind {
	case pluginConfigObject:
		value = mappingChild(parent, name)
		if value != nil && value.Kind == yaml.MappingNode {
			return nil
		}
		value = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case pluginConfigRemoval:
		value = nullYAMLNode()
	case pluginConfigValue:
		value, err = decodeNormalizedConfigNode(decision.yaml)
		if err != nil {
			return err
		}
	default:
		return errors.New("invalid Plugin configuration decision")
	}
	setMappingValue(parent, name, value)
	sortYAMLMapping(parent)
	return nil
}

func removeConfigMaintenanceDecision(root *yaml.Node, decision pluginConfigDecision) error {
	path := append([]string{"config", decision.pluginID}, decision.segments...)
	parent := mappingPath(root, path[:len(path)-1])
	if parent == nil {
		return nil
	}
	removeMappingValue(parent, path[len(path)-1])
	return nil
}

func aliasYAMLNode(alias Alias) *yaml.Node {
	if !alias.hasExposure && alias.deprecated == "" {
		return stringYAMLNode(alias.target.String())
	}
	result := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMappingValue(result, "target", stringYAMLNode(alias.target.String()))
	if alias.hasExposure {
		exposure := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(exposure, "go", boolYAMLNode(alias.exposure.Go))
		setMappingValue(exposure, "http", boolYAMLNode(alias.exposure.HTTP))
		setMappingValue(exposure, "javascript", boolYAMLNode(alias.exposure.JavaScript))
		setMappingValue(result, "expose", exposure)
	}
	if alias.deprecated != "" {
		deprecated := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(deprecated, "message", stringYAMLNode(alias.deprecated))
		setMappingValue(result, "deprecated", deprecated)
	}
	return result
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

func mappingPath(root *yaml.Node, path []string) *yaml.Node {
	current := root
	for _, name := range path {
		current = mappingChild(current, name)
		if current == nil {
			return nil
		}
	}
	return current
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

func sortYAMLStringSequence(sequence *yaml.Node) {
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return
	}
	sort.SliceStable(sequence.Content, func(left, right int) bool { return sequence.Content[left].Value < sequence.Content[right].Value })
}

func preserveYAMLComments(target, source *yaml.Node) {
	if target == nil || source == nil {
		return
	}
	target.HeadComment = source.HeadComment
	target.LineComment = source.LineComment
	target.FootComment = source.FootComment
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	result := *node
	result.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		result.Content[index] = cloneYAMLNode(child)
	}
	return &result
}

func stringYAMLNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func boolYAMLNode(value bool) *yaml.Node {
	if value {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
}

func nullYAMLNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}

func encodeMaintainedDocument(root *yaml.Node) ([]byte, error) {
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
