// Package interfaceresolution deterministically selects visible ordinary Go
// Implementations for required canonical Interfaces and freezes their
// constructor dependency graph.
package interfaceresolution

import (
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
)

// Requirement is one root Interface requirement with stable non-secret
// provenance. Repeated identical requirements are deduplicated by resolution.
type Requirement struct {
	InterfaceID interfaceid.Identifier
	Source      string
}

// Choice is one effective interfaces.use decision. Choices influence only an
// Interface that is otherwise required; they do not create requirements.
type Choice struct {
	InterfaceID interfaceid.Identifier
	Constructor constructorsymbol.Symbol
	Sources     []string
}

// Input contains the complete validated visible inventories plus selected
// current-application requirements and explicit Implementation choices.
type Input struct {
	Interfaces      interfaceinventory.Index
	Implementations implementationinventory.Index
	Requirements    []Requirement
	Choices         []Choice
}

// IntrinsicRequirement is one always-present reserved Kernel Interface with
// its canonical package and complete stable requirement provenance. It has no
// ordinary Implementation selection.
type IntrinsicRequirement struct {
	interfaceID interfaceid.Identifier
	packagePath string
	sources     []string
}

// InterfaceID returns the exact reserved Interface ID.
func (r IntrinsicRequirement) InterfaceID() interfaceid.Identifier { return r.interfaceID }

// PackagePath returns the canonical Kernel-owned Interface package.
func (r IntrinsicRequirement) PackagePath() string { return r.packagePath }

// Sources returns sorted unique Kernel and current-application provenance.
func (r IntrinsicRequirement) Sources() []string {
	return append([]string(nil), r.sources...)
}

// Result is one immutable resolved selection closure and validated constructor
// dependency graph.
type Result struct {
	graph                 constructorgraph.Graph
	selections            []constructorgraph.Selection
	intrinsicRequirements []IntrinsicRequirement
}

// Graph returns the immutable reachable constructor dependency graph.
func (r Result) Graph() constructorgraph.Graph { return r.graph }

// Selections returns every reachable Interface binding in canonical ID order.
func (r Result) Selections() []constructorgraph.Selection {
	return cloneSelections(r.selections)
}

// IntrinsicRequirements returns every reserved Kernel Interface in exact ID
// order. These requirements never enter ordinary Implementation selection.
func (r Result) IntrinsicRequirements() []IntrinsicRequirement {
	result := append([]IntrinsicRequirement(nil), r.intrinsicRequirements...)
	for index := range result {
		result[index].sources = append([]string(nil), result[index].sources...)
	}
	return result
}

func cloneSelections(values []constructorgraph.Selection) []constructorgraph.Selection {
	result := append([]constructorgraph.Selection(nil), values...)
	for index := range result {
		result[index].Sources = append([]string(nil), result[index].Sources...)
	}
	return result
}
