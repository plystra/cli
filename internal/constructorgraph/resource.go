package constructorgraph

import (
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourceproviderinventory"
)

// ResourceConsumerNamespace identifies one exact Resource binding address space.
type ResourceConsumerNamespace string

const (
	ResourceConsumerImplementation ResourceConsumerNamespace = "implementations"
	ResourceConsumerInstance       ResourceConsumerNamespace = "instances"
)

// ResourceSource is stable, non-secret provenance for an instance selection or
// explicit binding. It is independent of configuration parsing and precedence.
type ResourceSource struct {
	Reference  string
	ModulePath string
	Path       string
	Line       int
	Column     int
}

func (s ResourceSource) String() string { return s.Reference }

// ResourceInstanceInput selects one exact provider for a named instance.
// Every selected instance is active, including instances without consumers.
type ResourceInstanceInput struct {
	Name     string
	Provider constructorsymbol.Symbol
	Sources  []ResourceSource
}

// ResourceBindingInput addresses one exact Resource parameter. Instances use
// the consumer instance name; Implementations use the constructor symbol.
type ResourceBindingInput struct {
	Namespace ResourceConsumerNamespace
	Consumer  string
	Parameter string
	Target    string
	Sources   []ResourceSource
}

// GeneratedResourceInput describes a provider that will be emitted into the
// current Project after the application model has been frozen. It is a graph
// consumer rather than a selected user Resource instance: its dependency is
// still resolved by the ordinary Resource binding rules before Freeze.
type GeneratedResourceInput struct {
	MemberID                  string
	Name                      string
	ResourceID                interfaceid.Identifier
	PackagePath               string
	TypeName                  string
	Constructor               constructorsymbol.Symbol
	DatabaseResourceID        interfaceid.Identifier
	DatabasePackagePath       string
	DatabaseParameter         string
	DatabaseParameterPosition int
	Sources                   []ResourceSource
}

// ResourceNode is one selected process-local instance, not a provider singleton.
type ResourceNode struct {
	name         string
	resourceID   interfaceid.Identifier
	provider     resourceproviderinventory.Provider
	sources      []ResourceSource
	dependencies []ResourceDependency
}

// GeneratedResourceNode is one planned current-Project provider for an
// authored access Resource contract. It is not a selected database instance
// and therefore is kept in a separate construction-order view.
type GeneratedResourceNode struct {
	memberID     string
	name         string
	resourceID   interfaceid.Identifier
	packagePath  string
	typeName     string
	constructor  constructorsymbol.Symbol
	sources      []ResourceSource
	dependencies []ResourceDependency
}

func (n GeneratedResourceNode) MemberID() string                      { return n.memberID }
func (n GeneratedResourceNode) Name() string                          { return n.name }
func (n GeneratedResourceNode) ResourceID() interfaceid.Identifier    { return n.resourceID }
func (n GeneratedResourceNode) PackagePath() string                   { return n.packagePath }
func (n GeneratedResourceNode) TypeName() string                      { return n.typeName }
func (n GeneratedResourceNode) Constructor() constructorsymbol.Symbol { return n.constructor }
func (n GeneratedResourceNode) Sources() []ResourceSource {
	return append([]ResourceSource(nil), n.sources...)
}
func (n GeneratedResourceNode) Dependencies() []ResourceDependency {
	return cloneResourceDependencies(n.dependencies)
}

func (n ResourceNode) Name() string                                 { return n.name }
func (n ResourceNode) ResourceID() interfaceid.Identifier           { return n.resourceID }
func (n ResourceNode) Provider() resourceproviderinventory.Provider { return n.provider }
func (n ResourceNode) Sources() []ResourceSource {
	return append([]ResourceSource(nil), n.sources...)
}
func (n ResourceNode) Dependencies() []ResourceDependency {
	return cloneResourceDependencies(n.dependencies)
}

// ResourceDependency retains a consumer parameter, its exact selected instance,
// provider, and independent binding and instance-selection provenance.
type ResourceDependency struct {
	namespace                ResourceConsumerNamespace
	consumer                 string
	constructor              constructorsymbol.Symbol
	declaration              ResourceSource
	resourceID               interfaceid.Identifier
	packagePath              string
	parameterName            string
	parameterPosition        int
	instanceName             string
	provider                 constructorsymbol.Symbol
	reason                   SelectionReason
	sources                  []ResourceSource
	selectionSources         []ResourceSource
	consumerSelectionSources []ResourceSource
}

func (d ResourceDependency) Namespace() ResourceConsumerNamespace  { return d.namespace }
func (d ResourceDependency) Consumer() string                      { return d.consumer }
func (d ResourceDependency) Constructor() constructorsymbol.Symbol { return d.constructor }
func (d ResourceDependency) DeclarationSource() ResourceSource     { return d.declaration }
func (d ResourceDependency) ResourceID() interfaceid.Identifier    { return d.resourceID }
func (d ResourceDependency) PackagePath() string                   { return d.packagePath }
func (d ResourceDependency) ParameterName() string                 { return d.parameterName }
func (d ResourceDependency) ParameterPosition() int                { return d.parameterPosition }
func (d ResourceDependency) InstanceName() string                  { return d.instanceName }
func (d ResourceDependency) Provider() constructorsymbol.Symbol    { return d.provider }
func (d ResourceDependency) Reason() SelectionReason               { return d.reason }
func (d ResourceDependency) Sources() []ResourceSource {
	return append([]ResourceSource(nil), d.sources...)
}
func (d ResourceDependency) SelectionSources() []ResourceSource {
	return append([]ResourceSource(nil), d.selectionSources...)
}

// ConsumerSelectionSources returns the consumer instance's selection sources.
// Implementation consumers have no instance selection and return nil.
func (d ResourceDependency) ConsumerSelectionSources() []ResourceSource {
	return append([]ResourceSource(nil), d.consumerSelectionSources...)
}

// ResourceConstructionOrder returns every selected instance once, with its
// upstream instances first. Construct these before ConstructionOrder.
func (g Graph) ResourceConstructionOrder() []ResourceNode {
	return cloneResourceNodes(g.resourceConstruction)
}

// GeneratedResourceConstructionOrder returns planned generated access
// providers in deterministic name order. These providers are validated as
// ordinary Resource consumers but are not executable until Data emission and
// installation complete.
func (g Graph) GeneratedResourceConstructionOrder() []GeneratedResourceNode {
	return cloneGeneratedResourceNodes(g.generatedResources)
}

// ResourceDependencies returns the parameter-ordered bindings of an active
// Implementation. Dormant constructors have no executable Resource edges.
func (g Graph) ResourceDependencies(constructor constructorsymbol.Symbol) []ResourceDependency {
	return cloneResourceDependencies(g.resourceDependencies[constructor])
}

func cloneResourceDependencies(values []ResourceDependency) []ResourceDependency {
	result := append([]ResourceDependency(nil), values...)
	for index := range result {
		result[index].sources = append([]ResourceSource(nil), result[index].sources...)
		result[index].selectionSources = append([]ResourceSource(nil), result[index].selectionSources...)
		result[index].consumerSelectionSources = append([]ResourceSource(nil), result[index].consumerSelectionSources...)
	}
	return result
}

func cloneResourceNodes(values []ResourceNode) []ResourceNode {
	result := append([]ResourceNode(nil), values...)
	for index := range result {
		result[index].sources = append([]ResourceSource(nil), result[index].sources...)
		result[index].dependencies = cloneResourceDependencies(result[index].dependencies)
	}
	return result
}

func cloneGeneratedResourceNodes(values []GeneratedResourceNode) []GeneratedResourceNode {
	result := append([]GeneratedResourceNode(nil), values...)
	for index := range result {
		result[index].sources = append([]ResourceSource(nil), result[index].sources...)
		result[index].dependencies = cloneResourceDependencies(result[index].dependencies)
	}
	return result
}
