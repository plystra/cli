// Package constructorgraph builds the deterministic synchronous constructor
// graph from already selected Interface bindings and named Resource instances.
package constructorgraph

import (
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourceproviderinventory"
)

// SelectionReason identifies an explicit or unique-compatible Interface
// selection or Resource dependency binding.
type SelectionReason string

const (
	// SelectionExplicit records an effective Interface choice or Resource binding.
	SelectionExplicit SelectionReason = "explicit"
	// SelectionUnique records automatic selection of the sole compatible
	// visible Implementation or compatible selected Resource instance.
	SelectionUnique SelectionReason = "unique-compatible"
)

// Valid reports whether the reason belongs to the closed selection vocabulary.
func (r SelectionReason) Valid() bool {
	return r == SelectionExplicit || r == SelectionUnique
}

// RequirementSourceKind identifies the application configuration edge that
// introduced one root Interface requirement.
type RequirementSourceKind string

const (
	// RequirementDeclaration identifies an explicit interfaces.require entry.
	RequirementDeclaration RequirementSourceKind = "declaration"
	// RequirementExposure identifies an explicit public HTTP exposure.
	RequirementExposure RequirementSourceKind = "exposure"
)

// Valid reports whether the kind belongs to the closed root-source vocabulary.
func (k RequirementSourceKind) Valid() bool {
	return k == RequirementDeclaration || k == RequirementExposure
}

// RequirementSource is one typed stable Project-document location that made a
// root Interface necessary. Reference retains the existing bounded provenance
// used by generated evidence and human error paths.
type RequirementSource struct {
	Kind       RequirementSourceKind
	Reference  string
	ModulePath string
	Path       string
	Line       int
	Column     int
}

// String returns the bounded stable requirement reference.
func (s RequirementSource) String() string { return s.Reference }

// Requirement is one root Interface requirement and its stable provenance.
// Several requirements for the same Interface are normalized into one root.
type Requirement struct {
	InterfaceID interfaceid.Identifier
	Source      RequirementSource
}

// Selection binds one exact Interface to one already selected constructor.
// Sources contain the stable selection provenance used by diagnostics.
type Selection struct {
	InterfaceID interfaceid.Identifier
	Constructor constructorsymbol.Symbol
	Reason      SelectionReason
	Sources     []string
}

// Input contains discovered inventories from the same application snapshot,
// root requirements, resolved Interface selections, and effective Resource
// instance selections and bindings. Configuration composition precedes Build.
type Input struct {
	Implementations   implementationinventory.Index
	Requirements      []Requirement
	Selections        []Selection
	ResourceProviders resourceproviderinventory.Index
	ResourceInstances []ResourceInstanceInput
	ResourceBindings  []ResourceBindingInput
}

// Graph is an immutable deterministic constructor dependency graph.
type Graph struct {
	roots                []Root
	bindings             []Binding
	construction         []Node
	resourceConstruction []ResourceNode
	resourceDependencies map[constructorsymbol.Symbol][]ResourceDependency
}

// Roots returns the normalized Interface roots in canonical ID order.
func (g Graph) Roots() []Root { return cloneRoots(g.roots) }

// Bindings returns every reachable Interface binding in canonical ID order.
func (g Graph) Bindings() []Binding { return cloneBindings(g.bindings) }

// ConstructionOrder returns each reachable Implementation constructor exactly
// once after its Interface dependencies. Construct ResourceConstructionOrder
// first, then this order, for a complete dependency-ordered static assembly.
func (g Graph) ConstructionOrder() []Node { return cloneNodes(g.construction) }

// Root is one normalized required Interface with all stable requirement
// sources that contributed it.
type Root struct {
	interfaceID interfaceid.Identifier
	sources     []RequirementSource
}

// InterfaceID returns the exact required Interface ID.
func (r Root) InterfaceID() interfaceid.Identifier { return r.interfaceID }

// Sources returns every sorted unique requirement source.
func (r Root) Sources() []string {
	result := make([]string, len(r.sources))
	for index, source := range r.sources {
		result[index] = source.Reference
	}
	return result
}

// RequirementSources returns every sorted unique typed root source.
func (r Root) RequirementSources() []RequirementSource {
	return append([]RequirementSource(nil), r.sources...)
}

// Binding is one reachable exact Interface-to-constructor selection.
type Binding struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	reason      SelectionReason
	sources     []string
}

// InterfaceID returns the exact bound Interface ID.
func (b Binding) InterfaceID() interfaceid.Identifier { return b.interfaceID }

// Constructor returns the selected fully qualified constructor symbol.
func (b Binding) Constructor() constructorsymbol.Symbol { return b.constructor }

// Reason returns the exact selection reason.
func (b Binding) Reason() SelectionReason { return b.reason }

// Sources returns every stable selection source.
func (b Binding) Sources() []string { return append([]string(nil), b.sources...) }

// Node is one selected constructor and its parameter-ordered dependency edges.
type Node struct {
	implementation implementationinventory.Implementation
	symbol         constructorsymbol.Symbol
	source         string
	dependencies   []Dependency
}

// Implementation returns the validated discovered constructor declaration.
func (n Node) Implementation() implementationinventory.Implementation {
	return n.implementation
}

// Symbol returns the exact constructor identity.
func (n Node) Symbol() constructorsymbol.Symbol { return n.symbol }

// Source returns stable module-qualified constructor provenance.
func (n Node) Source() string { return n.source }

// Dependencies returns a defensive parameter-ordered dependency view.
func (n Node) Dependencies() []Dependency {
	return append([]Dependency(nil), n.dependencies...)
}

// Dependency is one required Interface or optional Interface constructor
// parameter. An unavailable optional dependency has no selected constructor.
type Dependency struct {
	interfaceID       interfaceid.Identifier
	packagePath       string
	parameterName     string
	parameterPosition int
	optional          bool
	available         bool
	constructor       constructorsymbol.Symbol
}

// InterfaceID returns the exact constructor-parameter Interface ID.
func (d Dependency) InterfaceID() interfaceid.Identifier { return d.interfaceID }

// PackagePath returns the canonical Interface package import path.
func (d Dependency) PackagePath() string { return d.packagePath }

// ParameterName returns the authored parameter name, when present.
func (d Dependency) ParameterName() string { return d.parameterName }

// ParameterPosition returns the one-based constructor parameter position.
func (d Dependency) ParameterPosition() int { return d.parameterPosition }

// Optional reports whether the parameter uses plystra.Optional[T].
func (d Dependency) Optional() bool { return d.optional }

// Available reports whether an optional dependency has a selected binding.
// Required dependencies in a successful graph are always available.
func (d Dependency) Available() bool { return d.available }

// Constructor returns the selected target constructor when Available is true.
func (d Dependency) Constructor() constructorsymbol.Symbol { return d.constructor }

func cloneBindings(values []Binding) []Binding {
	result := append([]Binding(nil), values...)
	for index := range result {
		result[index].sources = append([]string(nil), result[index].sources...)
	}
	return result
}

func cloneNodes(values []Node) []Node {
	result := append([]Node(nil), values...)
	for index := range result {
		result[index].dependencies = append([]Dependency(nil), result[index].dependencies...)
	}
	return result
}
