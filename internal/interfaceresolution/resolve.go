package interfaceresolution

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/intrinsicinterface"
)

type constructorRecord struct {
	symbol     constructorsymbol.Symbol
	source     string
	implements map[string]struct{}
	required   []interfaceid.Identifier
}

type catalog struct {
	interfaces   map[string]interfaceinventory.Interface
	intrinsics   map[string]intrinsicinterface.Definition
	constructors map[string]constructorRecord
	candidates   map[string][]constructorRecord
}

type normalizedChoice struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	sources     []string
}

type selector struct {
	catalog             catalog
	choices             map[string]normalizedChoice
	selections          map[string]constructorgraph.Selection
	visitedConstructors map[string]struct{}
}

// Resolve applies explicit effective choices first, otherwise selects only a
// sole compatible visible Implementation, expands required constructor
// dependencies, and validates the resulting graph. Optional parameters do not
// create requirements and become available only when their Interface is
// selected for another reason.
func Resolve(input Input) (Result, error) {
	catalog, err := buildCatalog(input.Interfaces, input.Implementations, intrinsicinterface.Definitions())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: %v", ErrResolve, ErrInvalidInput, err)
	}
	requirements, intrinsicRequirements, err := normalizeRequirements(input.Requirements, catalog.interfaces, catalog.intrinsics)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	choices, err := normalizeChoices(input.Choices, catalog)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}

	resolver := selector{
		catalog:             catalog,
		choices:             choices,
		selections:          make(map[string]constructorgraph.Selection),
		visitedConstructors: make(map[string]struct{}),
	}
	for _, requirement := range requirements {
		missing, selectErr := resolver.selectInterface(requirement.InterfaceID)
		if selectErr != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrResolve, selectErr)
		}
		if missing {
			break
		}
	}
	selections := resolver.sortedSelections()
	graphRequirements := make([]constructorgraph.Requirement, len(requirements))
	for index, requirement := range requirements {
		graphRequirements[index] = constructorgraph.Requirement(requirement)
	}
	graph, err := constructorgraph.Build(constructorgraph.Input{
		Implementations: input.Implementations,
		Requirements:    graphRequirements,
		Selections:      selections,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return Result{
		graph:                 graph,
		selections:            cloneSelections(selections),
		intrinsicRequirements: cloneIntrinsicRequirements(intrinsicRequirements),
	}, nil
}

func buildCatalog(interfaces interfaceinventory.Index, implementations implementationinventory.Index, intrinsicDefinitions []intrinsicinterface.Definition) (catalog, error) {
	if err := interfaceinventory.ValidateUniqueIDs(interfaces); err != nil {
		return catalog{}, err
	}
	result := catalog{
		interfaces:   make(map[string]interfaceinventory.Interface),
		intrinsics:   make(map[string]intrinsicinterface.Definition, len(intrinsicDefinitions)),
		constructors: make(map[string]constructorRecord),
		candidates:   make(map[string][]constructorRecord),
	}
	for _, definition := range intrinsicDefinitions {
		identifier := definition.ID()
		if identifier.String() == "" || !strings.HasPrefix(identifier.Name(), "kernel.") || definition.PackagePath() == "" || definition.Source() == "" {
			return catalog{}, errors.New("intrinsic Kernel Interface has invalid identity or provenance")
		}
		if _, duplicate := result.intrinsics[identifier.String()]; duplicate {
			return catalog{}, fmt.Errorf("intrinsic Kernel Interface %s appears more than once", identifier)
		}
		result.intrinsics[identifier.String()] = definition
	}
	for _, definition := range interfaces.Interfaces() {
		identifier, err := interfaceid.Parse(definition.ID())
		if err != nil || definition.PackagePath() == "" || definition.Source() == "" {
			return catalog{}, fmt.Errorf("visible Interface has invalid identity or provenance: %q", definition.ID())
		}
		if strings.HasPrefix(identifier.Name(), "kernel.") {
			return catalog{}, fmt.Errorf("%w %s: application package %q at %s uses the reserved kernel.* namespace; correction: remove the declaration and import the canonical Kernel Interface package", ErrReservedInterface, identifier, definition.PackagePath(), definition.Source())
		}
		result.interfaces[identifier.String()] = definition
	}
	for _, implementation := range implementations.Implementations() {
		symbol := implementation.Symbol()
		if symbol.String() == "" || implementation.Source() == "" {
			return catalog{}, errors.New("visible Implementation has empty identity or provenance")
		}
		if _, duplicate := result.constructors[symbol.String()]; duplicate {
			return catalog{}, fmt.Errorf("constructor %s appears more than once", symbol)
		}
		record := constructorRecord{
			symbol:     symbol,
			source:     implementation.Source(),
			implements: make(map[string]struct{}),
		}
		for _, declaration := range implementation.Declaration().ImplementedInterfaces() {
			identifier := declaration.ID()
			if _, visible := result.interfaces[identifier.String()]; !visible {
				return catalog{}, fmt.Errorf("constructor %s declares invisible Interface %s", symbol, identifier)
			}
			if _, duplicate := record.implements[identifier.String()]; duplicate {
				return catalog{}, fmt.Errorf("constructor %s repeats Interface %s", symbol, identifier)
			}
			record.implements[identifier.String()] = struct{}{}
		}
		if len(record.implements) == 0 {
			return catalog{}, fmt.Errorf("constructor %s declares no Interface", symbol)
		}
		for _, dependency := range implementation.RequiredInterfaces() {
			if _, visible := result.interfaces[dependency.ID().String()]; !visible {
				return catalog{}, fmt.Errorf("constructor %s requires invisible Interface %s", symbol, dependency.ID())
			}
			record.required = append(record.required, dependency.ID())
		}
		result.constructors[symbol.String()] = record
		for identifier := range record.implements {
			result.candidates[identifier] = append(result.candidates[identifier], record)
		}
	}
	for identifier := range result.candidates {
		sort.Slice(result.candidates[identifier], func(left, right int) bool {
			return result.candidates[identifier][left].symbol.String() < result.candidates[identifier][right].symbol.String()
		})
	}
	return result, nil
}

func normalizeRequirements(inputs []Requirement, interfaces map[string]interfaceinventory.Interface, intrinsics map[string]intrinsicinterface.Definition) ([]Requirement, []IntrinsicRequirement, error) {
	result := make([]Requirement, 0, len(inputs))
	intrinsicSources := make(map[string][]string, len(intrinsics))
	for identifier, definition := range intrinsics {
		intrinsicSources[identifier] = []string{definition.Source()}
	}
	for index, input := range inputs {
		identifier := input.InterfaceID.String()
		if identifier == "" {
			return nil, nil, fmt.Errorf("%w: requirements[%d] has an empty Interface ID", ErrInvalidInput, index)
		}
		source, err := normalizeSource(input.Source)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: requirements[%d] source %v", ErrInvalidInput, index, err)
		}
		if _, intrinsic := intrinsics[identifier]; intrinsic {
			intrinsicSources[identifier] = append(intrinsicSources[identifier], source)
			continue
		}
		if strings.HasPrefix(input.InterfaceID.Name(), "kernel.") {
			return nil, nil, fmt.Errorf("%w: required reserved Interface %s is not published by the selected Kernel API", ErrUnknownInterface, input.InterfaceID)
		}
		if _, visible := interfaces[identifier]; !visible {
			return nil, nil, fmt.Errorf("%w: required Interface %s is not defined by a visible canonical package", ErrUnknownInterface, input.InterfaceID)
		}
		result = append(result, Requirement{InterfaceID: input.InterfaceID, Source: source})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].InterfaceID != result[right].InterfaceID {
			return result[left].InterfaceID.String() < result[right].InterfaceID.String()
		}
		return result[left].Source < result[right].Source
	})
	intrinsicRequirements := make([]IntrinsicRequirement, 0, len(intrinsics))
	for identifier, definition := range intrinsics {
		intrinsicRequirements = append(intrinsicRequirements, IntrinsicRequirement{
			interfaceID: definition.ID(),
			packagePath: definition.PackagePath(),
			sources:     uniqueSorted(intrinsicSources[identifier]),
		})
	}
	sort.Slice(intrinsicRequirements, func(left, right int) bool {
		return intrinsicRequirements[left].interfaceID.String() < intrinsicRequirements[right].interfaceID.String()
	})
	return result, intrinsicRequirements, nil
}

func normalizeChoices(inputs []Choice, catalog catalog) (map[string]normalizedChoice, error) {
	result := make(map[string]normalizedChoice, len(inputs))
	for index, input := range inputs {
		identifier := input.InterfaceID.String()
		constructorID := input.Constructor.String()
		if identifier == "" || constructorID == "" {
			return nil, fmt.Errorf("%w: choices[%d] has an empty Interface or constructor", ErrInvalidInput, index)
		}
		if strings.HasPrefix(input.InterfaceID.Name(), "kernel.") {
			return nil, fmt.Errorf("%w: interfaces.use[%q] names %s", ErrIntrinsicChoice, identifier, input.Constructor)
		}
		if _, visible := catalog.interfaces[identifier]; !visible {
			return nil, fmt.Errorf("%w: interfaces.use[%q] is not defined by a visible canonical package", ErrUnknownInterface, identifier)
		}
		constructor, visible := catalog.constructors[constructorID]
		if !visible {
			return nil, fmt.Errorf("%w: interfaces.use[%q] names invisible constructor %s", ErrUnknownConstructor, identifier, input.Constructor)
		}
		if _, compatible := constructor.implements[identifier]; !compatible {
			return nil, fmt.Errorf("%w: constructor %s does not implement Interface %s", ErrIncompatibleChoice, input.Constructor, input.InterfaceID)
		}
		if len(input.Sources) == 0 {
			return nil, fmt.Errorf("%w: choices[%d] has no source", ErrInvalidInput, index)
		}
		sources := make([]string, len(input.Sources))
		for sourceIndex, value := range input.Sources {
			source, err := normalizeSource(value)
			if err != nil {
				return nil, fmt.Errorf("%w: choices[%d].sources[%d] %v", ErrInvalidInput, index, sourceIndex, err)
			}
			sources[sourceIndex] = source
		}
		sources = uniqueSorted(sources)
		normalized := normalizedChoice{
			interfaceID: input.InterfaceID,
			constructor: input.Constructor,
			sources:     sources,
		}
		if previous, duplicate := result[identifier]; duplicate {
			if previous.constructor != normalized.constructor {
				return nil, fmt.Errorf("%w: interfaces.use[%q] selects both %s and %s", ErrInvalidInput, identifier, previous.constructor, normalized.constructor)
			}
			previous.sources = uniqueSorted(append(previous.sources, normalized.sources...))
			result[identifier] = previous
			continue
		}
		result[identifier] = normalized
	}
	return result, nil
}

func cloneIntrinsicRequirements(values []IntrinsicRequirement) []IntrinsicRequirement {
	result := append([]IntrinsicRequirement(nil), values...)
	for index := range result {
		result[index].sources = append([]string(nil), result[index].sources...)
	}
	return result
}

func (s *selector) selectInterface(identifier interfaceid.Identifier) (bool, error) {
	key := identifier.String()
	if _, selected := s.selections[key]; selected {
		return false, nil
	}

	var selected constructorRecord
	reason := constructorgraph.SelectionUnique
	sources := []string(nil)
	if choice, explicit := s.choices[key]; explicit {
		selected = s.catalog.constructors[choice.constructor.String()]
		reason = constructorgraph.SelectionExplicit
		sources = append([]string(nil), choice.sources...)
	} else {
		candidates := s.catalog.candidates[key]
		switch len(candidates) {
		case 0:
			return true, nil
		case 1:
			selected = candidates[0]
			sources = []string{selected.source}
		default:
			values := make([]Candidate, len(candidates))
			for index, candidate := range candidates {
				values[index] = Candidate{constructor: candidate.symbol, source: candidate.source}
			}
			return false, &AmbiguousImplementationError{interfaceID: identifier, candidates: values}
		}
	}
	s.selections[key] = constructorgraph.Selection{
		InterfaceID: identifier,
		Constructor: selected.symbol,
		Reason:      reason,
		Sources:     sources,
	}
	if _, visited := s.visitedConstructors[selected.symbol.String()]; visited {
		return false, nil
	}
	s.visitedConstructors[selected.symbol.String()] = struct{}{}
	for _, dependency := range selected.required {
		missing, err := s.selectInterface(dependency)
		if err != nil || missing {
			return missing, err
		}
	}
	return false, nil
}

func (s *selector) sortedSelections() []constructorgraph.Selection {
	result := make([]constructorgraph.Selection, 0, len(s.selections))
	for _, selection := range s.selections {
		result = append(result, selection)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].InterfaceID.String() < result[right].InterfaceID.String()
	})
	return cloneSelections(result)
}

func normalizeSource(value string) (string, error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("must be non-empty single-line UTF-8 at most 4096 bytes")
	}
	return value, nil
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return append([]string(nil), result...)
}
