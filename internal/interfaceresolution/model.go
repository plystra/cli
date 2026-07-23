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

// Result is one immutable resolved selection closure and validated constructor
// dependency graph.
type Result struct {
	graph      constructorgraph.Graph
	selections []constructorgraph.Selection
}

// Graph returns the immutable reachable constructor dependency graph.
func (r Result) Graph() constructorgraph.Graph { return r.graph }

// Selections returns every reachable Interface binding in canonical ID order.
func (r Result) Selections() []constructorgraph.Selection {
	return cloneSelections(r.selections)
}

func cloneSelections(values []constructorgraph.Selection) []constructorgraph.Selection {
	result := append([]constructorgraph.Selection(nil), values...)
	for index := range result {
		result[index].Sources = append([]string(nil), result[index].Sources...)
	}
	return result
}
