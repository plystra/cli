package interfaceprovenance

import (
	"errors"
	"fmt"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/resourcename"
	"golang.org/x/mod/module"
)

// ResourceSource contains only stable declaration coordinates, never YAML values.
type ResourceSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// ResourceInput records one selected instance, including unconsumed instances.
// ConstructionOrder is dependency-first within the Resource graph, which is
// constructed before the Implementation graph.
type ResourceInput struct {
	Name                 string           `json:"name"`
	ResourceID           string           `json:"resource_id"`
	PackagePath          string           `json:"package_path"`
	ContractDigest       string           `json:"contract_digest"`
	ContractSource       ResourceSource   `json:"contract_source"`
	Provider             string           `json:"provider"`
	ModulePath           string           `json:"module_path"`
	ModuleVersion        string           `json:"module_version"`
	DeclarationSource    ResourceSource   `json:"declaration_source"`
	SelectionSources     []ResourceSource `json:"selection_sources"`
	ConstructionOrder    int              `json:"construction_order"`
	ConfigurationOwner   string           `json:"configuration_owner,omitempty"`
	ConfigurationSources []ResourceSource `json:"configuration_sources"`
}

// ResourceBindingInput is an active consumer-to-instance edge. ConsumerKind is
// implementations or instances, matching the two authored binding namespaces.
type ResourceBindingInput struct {
	ConsumerKind             string           `json:"consumer_kind"`
	Consumer                 string           `json:"consumer"`
	Constructor              string           `json:"constructor"`
	ResourceID               string           `json:"resource_id"`
	PackagePath              string           `json:"package_path"`
	ParameterName            string           `json:"parameter_name"`
	ParameterPosition        int              `json:"parameter_position"`
	InstanceName             string           `json:"instance_name"`
	Provider                 string           `json:"provider"`
	Reason                   SelectionReason  `json:"reason"`
	DeclarationSource        ResourceSource   `json:"declaration_source"`
	BindingSources           []ResourceSource `json:"binding_sources"`
	SelectionSources         []ResourceSource `json:"selection_sources"`
	ConsumerSelectionSources []ResourceSource `json:"consumer_selection_sources"`
}

// Resources returns defensive records, in Resource construction order.
func (p Provenance) Resources() []ResourceInput { return cloneResources(p.record.Resources) }

// ResourceBindings returns defensive consumer/parameter-ordered edges.
func (p Provenance) ResourceBindings() []ResourceBindingInput {
	return cloneResourceBindings(p.record.ResourceBindings)
}

// NormalizeResources validates and copies the shared non-secret record format
// used by live resolution evidence and persisted application provenance.
// Instances sharing a provider must agree on its declared dependency signature.
func NormalizeResources(resources []ResourceInput, bindings []ResourceBindingInput) ([]ResourceInput, []ResourceBindingInput, error) {
	if len(resources) > maximumRecords || len(bindings) > maximumRecords {
		return nil, nil, fmt.Errorf("%w: too many Resource records", ErrInvalid)
	}
	resources = cloneResources(resources)
	bindings = cloneResourceBindings(bindings)
	for i := range resources {
		var err error
		resources[i].SelectionSources, err = normalizeResourceSources(resources[i].SelectionSources)
		if err != nil {
			return nil, nil, err
		}
		resources[i].ConfigurationSources, err = normalizeResourceSources(resources[i].ConfigurationSources)
		if err != nil {
			return nil, nil, err
		}
	}
	for i := range bindings {
		for _, sources := range []*[]ResourceSource{&bindings[i].BindingSources, &bindings[i].SelectionSources, &bindings[i].ConsumerSelectionSources} {
			var err error
			*sources, err = normalizeResourceSources(*sources)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ConstructionOrder < resources[j].ConstructionOrder })
	sort.Slice(bindings, func(i, j int) bool { return resourceBindingKey(bindings[i]) < resourceBindingKey(bindings[j]) })
	if err := validateResourceRecords(resources, bindings, nil); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return resources, bindings, nil
}

func validateResourceRecords(resources []ResourceInput, bindings []ResourceBindingInput, constructors map[string]wireConstructor) error {
	if len(resources) > maximumRecords || len(bindings) > maximumRecords {
		return errors.New("too many Resource records")
	}
	instances := make(map[string]ResourceInput, len(resources))
	contracts := make(map[string]ResourceInput)
	providers := make(map[string]ResourceInput)
	for i, resource := range resources {
		if resourcename.Check(resource.Name) != nil {
			return errors.New("invalid Resource instance name")
		}
		if _, exists := instances[resource.Name]; exists {
			return errors.New("duplicate Resource instance")
		}
		if resource.ConstructionOrder != i+1 {
			return errors.New("resource construction order must be contiguous and dependency-first")
		}
		if _, err := interfaceid.Parse(resource.ResourceID); err != nil {
			return errors.New("invalid Resource contract ID")
		}
		if module.CheckImportPath(resource.PackagePath) != nil || !validDigest(resource.ContractDigest) {
			return errors.New("invalid Resource contract package or digest")
		}
		if err := validateResourceSource(resource.ContractSource, "resource-declaration"); err != nil {
			return err
		}
		if resource.PackagePath != resource.ContractSource.Module && !strings.HasPrefix(resource.PackagePath, resource.ContractSource.Module+"/") {
			return errors.New("resource contract module disagrees with package")
		}
		provider, err := constructorsymbol.Parse(resource.Provider)
		if err != nil || modulepath.CheckProject(resource.ModulePath) != nil || !(provider.PackagePath() == resource.ModulePath || strings.HasPrefix(provider.PackagePath(), resource.ModulePath+"/")) || !validModuleVersion(resource.ModulePath, resource.ModuleVersion) {
			return errors.New("invalid Resource provider identity")
		}
		if err := validateResourceSource(resource.DeclarationSource, "resource-provider-constructor"); err != nil {
			return err
		}
		if resource.DeclarationSource.Module != resource.ModulePath {
			return errors.New("resource declaration module disagrees with provider")
		}
		if err := validateResourceSources(resource.SelectionSources, "resource-selection", true); err != nil {
			return err
		}
		if resource.ConfigurationOwner != "" && resource.ConfigurationOwner != "resources.instances["+strconv.Quote(resource.Name)+"].config" {
			return errors.New("resource configuration owner must identify its instance")
		}
		if err := validateResourceSources(resource.ConfigurationSources, "configuration-declaration", false); err != nil {
			return err
		}
		if resource.ConfigurationOwner == "" && len(resource.ConfigurationSources) > 0 {
			return errors.New("resource configuration sources have no instance owner")
		}
		if prior, ok := contracts[resource.ResourceID]; ok && (prior.PackagePath != resource.PackagePath || prior.ContractDigest != resource.ContractDigest || prior.ContractSource != resource.ContractSource) {
			return errors.New("inconsistent Resource contract")
		}
		if prior, ok := providers[resource.Provider]; ok && (prior.ResourceID != resource.ResourceID || prior.ModulePath != resource.ModulePath || prior.ModuleVersion != resource.ModuleVersion || prior.DeclarationSource != resource.DeclarationSource) {
			return errors.New("inconsistent Resource provider")
		}
		contracts[resource.ResourceID], providers[resource.Provider], instances[resource.Name] = resource, resource, resource
	}
	parameters := make(map[string]bool)
	instanceDependencies := make(map[string][]ResourceBindingInput)
	for i, binding := range bindings {
		if i > 0 && resourceBindingKey(bindings[i-1]) >= resourceBindingKey(binding) {
			return errors.New("resource bindings must be unique and ordered by consumer and parameter position")
		}
		if binding.ParameterName == "_" || !token.IsIdentifier(binding.ParameterName) || binding.ParameterPosition <= 0 || binding.ParameterPosition > 65535 {
			return errors.New("invalid Resource dependency parameter")
		}
		key := binding.ConsumerKind + "\x00" + binding.Consumer + "\x00" + binding.ParameterName
		if parameters[key] {
			return errors.New("duplicate Resource dependency parameter name")
		}
		parameters[key] = true
		target, exists := instances[binding.InstanceName]
		if !exists || target.Provider != binding.Provider || target.ResourceID != binding.ResourceID || target.PackagePath != binding.PackagePath {
			return errors.New("resource binding does not match its selected target")
		}
		if !equalResourceSources(binding.SelectionSources, target.SelectionSources) {
			return errors.New("resource binding target provenance does not match its selection")
		}
		if binding.Reason != SelectionExplicit && binding.Reason != SelectionUniqueCompatible {
			return errors.New("invalid Resource binding reason")
		}
		if err := validateResourceSources(binding.SelectionSources, "resource-selection", true); err != nil {
			return err
		}
		if err := validateResourceSources(binding.ConsumerSelectionSources, "resource-selection", binding.ConsumerKind == "instances"); err != nil {
			return err
		}
		if err := validateResourceSources(binding.BindingSources, "resource-binding", binding.Reason == SelectionExplicit); err != nil {
			return err
		}
		if binding.Reason == SelectionUniqueCompatible {
			if len(binding.BindingSources) != 0 {
				return errors.New("implicit Resource binding has explicit sources")
			}
			count := 0
			for _, candidate := range resources {
				if candidate.ResourceID == binding.ResourceID {
					count++
				}
			}
			if count != 1 {
				return errors.New("implicit Resource binding is not uniquely compatible")
			}
		}
		if _, err := constructorsymbol.Parse(binding.Constructor); err != nil {
			return errors.New("invalid Resource consumer constructor")
		}
		switch binding.ConsumerKind {
		case "instances":
			consumer, exists := instances[binding.Consumer]
			if !exists || consumer.Provider != binding.Constructor || consumer.ConstructionOrder <= target.ConstructionOrder {
				return errors.New("resource consumer must follow its dependency with the exact provider")
			}
			if binding.DeclarationSource != consumer.DeclarationSource || !equalResourceSources(binding.ConsumerSelectionSources, consumer.SelectionSources) {
				return errors.New("resource consumer provenance disagrees with selection")
			}
			instanceDependencies[binding.Consumer] = append(instanceDependencies[binding.Consumer], binding)
		case "implementations":
			if binding.Consumer != binding.Constructor || len(binding.ConsumerSelectionSources) != 0 {
				return errors.New("invalid Implementation Resource consumer")
			}
			if err := validateResourceSource(binding.DeclarationSource, "implementation-constructor"); err != nil {
				return err
			}
			if constructors != nil {
				consumer, exists := constructors[binding.Constructor]
				if !exists {
					return errors.New("resource binding consumer is not active")
				}
				if binding.DeclarationSource.Module != consumer.ModulePath {
					return errors.New("resource consumer module disagrees with active constructor")
				}
				for _, dependency := range consumer.Dependencies {
					if dependency.ParameterPosition == binding.ParameterPosition || dependency.ParameterName == binding.ParameterName {
						return errors.New("resource parameter collides with Interface dependency")
					}
				}
			}
		default:
			return errors.New("invalid Resource consumer kind")
		}
	}
	// Each instance calls the same typed provider, even when its targets differ.
	for _, resource := range resources {
		dependencies := instanceDependencies[resource.Name]
		expected := instanceDependencies[providers[resource.Provider].Name]
		if len(dependencies) != len(expected) {
			return errors.New("inconsistent Resource provider dependency signature")
		}
		for i, dependency := range dependencies {
			other := expected[i]
			if dependency.ParameterName != other.ParameterName || dependency.ParameterPosition != other.ParameterPosition || dependency.ResourceID != other.ResourceID || dependency.PackagePath != other.PackagePath {
				return errors.New("inconsistent Resource provider dependency signature")
			}
		}
	}
	return nil
}

func resourceBindingKey(binding ResourceBindingInput) string {
	return fmt.Sprintf("%s\x00%s\x00%05d", binding.ConsumerKind, binding.Consumer, binding.ParameterPosition)
}

func normalizeResourceSources(values []ResourceSource) ([]ResourceSource, error) {
	if len(values) > maximumSources {
		return nil, fmt.Errorf("%w: too many Resource sources", ErrInvalid)
	}
	inputs := make([]diagnosticjson.Source, 0, len(values))
	seen := make(map[ResourceSource]bool, len(values))
	for _, v := range values {
		if !seen[v] {
			inputs = append(inputs, diagnosticjson.Source(v))
			seen[v] = true
		}
	}
	normalized, err := diagnosticjson.CanonicalizeSources(inputs)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Resource source coordinates", ErrInvalid)
	}
	result := make([]ResourceSource, len(normalized))
	for i, v := range normalized {
		result[i] = ResourceSource(v)
	}
	return result, nil
}

func validateResourceSource(source ResourceSource, kind string) error {
	if source.Kind != kind {
		return errors.New("invalid Resource source kind")
	}
	_, err := normalizeResourceSources([]ResourceSource{source})
	return err
}

func validateResourceSources(sources []ResourceSource, kind string, required bool) error {
	if sources == nil {
		return errors.New("resource sources must be an array")
	}
	if required && len(sources) == 0 {
		return errors.New("resource provenance requires selection or binding sources")
	}
	normalized, err := normalizeResourceSources(sources)
	if err != nil {
		return err
	}
	if !equalResourceSources(sources, normalized) {
		return errors.New("resource sources must be unique and canonical")
	}
	for _, source := range sources {
		if source.Kind != kind {
			return errors.New("invalid Resource source kind")
		}
	}
	return nil
}

func equalResourceSources(a, b []ResourceSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneResources(values []ResourceInput) []ResourceInput {
	result := append([]ResourceInput(nil), values...)
	for i := range result {
		result[i].SelectionSources = append([]ResourceSource{}, result[i].SelectionSources...)
		result[i].ConfigurationSources = append([]ResourceSource{}, result[i].ConfigurationSources...)
	}
	return result
}

func cloneResourceBindings(values []ResourceBindingInput) []ResourceBindingInput {
	result := append([]ResourceBindingInput(nil), values...)
	for i := range result {
		result[i].BindingSources = append([]ResourceSource{}, result[i].BindingSources...)
		result[i].SelectionSources = append([]ResourceSource{}, result[i].SelectionSources...)
		result[i].ConsumerSelectionSources = append([]ResourceSource{}, result[i].ConsumerSelectionSources...)
	}
	return result
}

// ValidateResources requires canonical record order, suitable for persisted evidence.
func ValidateResources(resources []ResourceInput, bindings []ResourceBindingInput) error {
	return validateResourceRecords(resources, bindings, nil)
}
