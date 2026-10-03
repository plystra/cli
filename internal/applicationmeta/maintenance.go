package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrMaintainConfiguration reports failure to preserve current-project
	// intent while the dependency-derived configuration baseline changes.
	ErrMaintainConfiguration = errors.New("maintain dependency-derived project configuration")
)

type maintenanceField uint8

const (
	maintenanceHTTPExposure maintenanceField = iota + 1
	maintenanceRequirement
	maintenanceProvider
	maintenanceInterfaceRequirement
	maintenanceImplementationChoice
	maintenanceInterfacePolicy
	maintenanceAlias
	maintenanceConstructorConfig
)

type maintenanceDecision struct {
	path        string
	digest      string
	removed     bool
	field       maintenanceField
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

// ConfigurationMaintenance is one immutable, comment-preserving planned
// update of the selected current-project document.
type ConfigurationMaintenance struct {
	data       []byte
	localPaths []string
	changed    bool
}

// Data returns defensive planned YAML bytes.
func (m ConfigurationMaintenance) Data() []byte { return append([]byte(nil), m.data...) }

// Changed reports whether dependency recomposition changes the selected
// current-project document.
func (m ConfigurationMaintenance) Changed() bool { return m.changed }

// LocalPaths returns schema paths whose effective root-document values are
// explicit current-project decisions rather than maintained dependency state.
func (m ConfigurationMaintenance) LocalPaths() []string {
	return append([]string(nil), m.localPaths...)
}

// MaintainDependencyConfiguration validates the authored document and ordered
// template composition without materializing inherited values. Prior generated
// evidence is never configuration authority.
func MaintainDependencyConfiguration(data []byte, previous DependencyBaseline, previousLocalPaths []string, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	return maintainDependencyConfiguration(data, "", "", nil, previous, previousLocalPaths, dependencies, schemas)
}

// MaintainDependencyConfigurationSource performs dependency maintenance while
// retaining the selected current-Project document's module-relative diagnostic
// provenance.
func MaintainDependencyConfigurationSource(data []byte, modulePath, sourcePath string, previous DependencyBaseline, previousLocalPaths []string, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	return maintainDependencyConfiguration(data, modulePath, sourcePath, nil, previous, previousLocalPaths, dependencies, schemas)
}

// MaintainDependencyConfigurationWithOverlay validates root and selected overlay
// without copying template values into either authored document.
func MaintainDependencyConfigurationWithOverlay(data []byte, overlay Manifest, previous DependencyBaseline, previousLocalPaths []string, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	return maintainDependencyConfiguration(data, "", "", &overlay, previous, previousLocalPaths, dependencies, schemas)
}

// MaintainDependencyConfigurationSourceWithOverlay retains diagnostic ownership
// while validating the selected root and environment layers.
func MaintainDependencyConfigurationSourceWithOverlay(data []byte, modulePath, sourcePath string, overlay Manifest, previous DependencyBaseline, previousLocalPaths []string, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	return maintainDependencyConfiguration(data, modulePath, sourcePath, &overlay, previous, previousLocalPaths, dependencies, schemas)
}

func maintainDependencyConfiguration(data []byte, modulePath, sourcePath string, overlay *Manifest, previous DependencyBaseline, previousLocalPaths []string, dependencies []Dependency, schemas SchemaLookup) (ConfigurationMaintenance, error) {
	if schemas == nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: schema lookup is nil", ErrMaintainConfiguration)
	}
	if (modulePath == "") != (sourcePath == "") {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: diagnostic module and source path must be provided together", ErrMaintainConfiguration)
	}
	current, err := parseMaintenanceManifest(data, modulePath, sourcePath)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	decisions, err := ConfigurationDecisions(current, schemas)
	if err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	local := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		if decision.dependencyComposable {
			local = append(local, decision.path)
		}
	}
	selected := current
	if overlay != nil {
		selected, err = ApplyOverlay(current, *overlay, schemas)
		if err != nil {
			return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
		}
	}
	if _, err := Compose(dependencies, selected, schemas); err != nil {
		return ConfigurationMaintenance{}, fmt.Errorf("%w: %w", ErrMaintainConfiguration, err)
	}
	return ConfigurationMaintenance{data: append([]byte(nil), data...), localPaths: local}, nil
}

func parseMaintenanceManifest(data []byte, modulePath, sourcePath string) (Manifest, error) {
	if modulePath == "" {
		return Parse(data)
	}
	manifest, err := ParseSource(sourcePath, data)
	if err != nil {
		return Manifest{}, err
	}
	return WithProjectModule(manifest, modulePath)
}

func maintenanceDecisions(manifest Manifest, schemas SchemaLookup) ([]maintenanceDecision, error) {
	result := make([]maintenanceDecision, 0)
	for _, exposure := range manifest.httpExposures {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("http.expose[%q]", exposure.id.String()),
			digest:      httpExposureDigest(exposure),
			field:       maintenanceHTTPExposure,
			interfaceID: exposure.id,
			exposure:    exposure,
			source:      exposure.source,
		})
	}
	for _, removal := range manifest.removedHTTPExposures {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("http.expose[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("http.expose", removal.id, true),
			removed:     true,
			field:       maintenanceHTTPExposure,
			interfaceID: removal.id,
			source:      removal.source,
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
	for _, requirement := range manifest.interfaceRequirements {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("interfaces.require[%q]", requirement.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.require", requirement.id, false),
			field:       maintenanceInterfaceRequirement,
			interfaceID: requirement.id,
			source:      requirement.source,
		})
	}
	for _, removal := range manifest.removedInterfaceReqs {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("interfaces.require[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.require", removal.id, true),
			removed:     true,
			field:       maintenanceInterfaceRequirement,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, choice := range manifest.implementationChoices {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("interfaces.use[%q]", choice.interfaceID.String()),
			digest:      implementationChoiceDigest(choice),
			field:       maintenanceImplementationChoice,
			interfaceID: choice.interfaceID,
			constructor: choice.constructor,
			source:      choice.source,
		})
	}
	for _, removal := range manifest.removedImplementationChoices {
		result = append(result, maintenanceDecision{
			path:        fmt.Sprintf("interfaces.use[%q]", removal.id.String()),
			digest:      interfaceDeclarationDigest("interfaces.use", removal.id, true),
			removed:     true,
			field:       maintenanceImplementationChoice,
			interfaceID: removal.id,
			source:      removal.source,
		})
	}
	for _, policy := range manifest.interfacePolicies {
		result = append(result, maintenanceDecision{
			path:        interfacePolicyPath(policy.interfaceID),
			digest:      interfacePolicyDigest(policy),
			field:       maintenanceInterfacePolicy,
			interfaceID: policy.interfaceID,
			policy:      policy,
			source:      policy.source,
		})
	}
	for _, removal := range manifest.removedInterfacePolicies {
		result = append(result, maintenanceDecision{
			path:        interfacePolicyPath(removal.id),
			digest:      interfacePolicyRemovalDigest(removal.id),
			removed:     true,
			field:       maintenanceInterfacePolicy,
			interfaceID: removal.id,
			source:      removal.source,
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
			path:        constructorConfigPath(configuration.constructor, configuration.segments),
			digest:      constructorConfigPublicDigest(configuration),
			removed:     configuration.kind == constructorConfigRemoval,
			field:       maintenanceConstructorConfig,
			constructor: configuration.constructor,
			config:      cloneConstructorConfigDecision(configuration),
			source:      configuration.source,
		})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].path < result[right].path })
	return result, nil
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

func removalYAMLNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle, Content: []*yaml.Node{
		stringYAMLNode("$remove"),
		{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
	}}
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
