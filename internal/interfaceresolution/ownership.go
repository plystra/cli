package interfaceresolution

import (
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/intrinsicinterface"
)

// The ordinary selection pass remains the error authority. This recovery-only
// traversal proves presence on independent branches, never completeness.
func collectPositiveOwners(input Input) map[string]constructorsymbol.Symbol {
	catalog, err := buildCatalog(input.Interfaces, input.Implementations, intrinsicinterface.Definitions())
	if err != nil {
		return nil
	}
	owners := make(map[string]constructorsymbol.Symbol)
	choices := make(map[string]normalizedChoice)
	blocked := make(map[string]bool)
	groups := make(map[string][]Choice)
	for _, choice := range input.Choices {
		key := choice.InterfaceID.String()
		groups[key] = append(groups[key], choice)
	}
	// Normalize each exact key as a unit so an invalid or conflicting duplicate
	// cannot be hidden by a valid sibling or replaced by a sole implicit candidate.
	for key, group := range groups {
		normalized, err := normalizeChoices(group, catalog)
		if err != nil {
			blocked[key] = true
			continue
		}
		choice := normalized[key]
		choices[key] = choice
		owners[choice.constructor.String()] = choice.constructor
	}
	var pending []interfaceid.Identifier
	for _, requirement := range input.Requirements {
		normalized, _, err := normalizeRequirements([]Requirement{requirement}, catalog.interfaces, catalog.intrinsics)
		if err != nil {
			continue
		}
		for _, value := range normalized {
			pending = append(pending, value.InterfaceID)
		}
	}
	visited := make(map[string]bool)
	expanded := make(map[string]bool)
	for len(pending) != 0 {
		identifier := pending[0]
		pending = pending[1:]
		key := identifier.String()
		if visited[key] || blocked[key] {
			continue
		}
		visited[key] = true
		var selected constructorRecord
		if choice, explicit := choices[key]; explicit {
			selected = catalog.constructors[choice.constructor.String()]
		} else if candidates := catalog.candidates[key]; len(candidates) == 1 {
			selected = candidates[0]
		} else {
			continue
		}
		constructor := selected.symbol.String()
		owners[constructor] = selected.symbol
		if !expanded[constructor] {
			expanded[constructor] = true
			pending = append(pending, selected.required...)
		}
	}
	return owners
}
