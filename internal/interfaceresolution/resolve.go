package interfaceresolution

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/intrinsicinterface"
	"github.com/plystra/cli/internal/modulepath"
)

type constructorRecord struct {
	symbol     constructorsymbol.Symbol
	source     string
	modulePath string
	sourcePath string
	line       int
	column     int
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
	sources     []ChoiceSource
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
		return Result{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrInvalidInput, err)
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
		position := implementation.Declaration().Position()
		if _, duplicate := result.constructors[symbol.String()]; duplicate {
			return catalog{}, fmt.Errorf("constructor %s appears more than once", symbol)
		}
		record := constructorRecord{
			symbol:     symbol,
			source:     implementation.Source(),
			modulePath: implementation.ModulePath(),
			sourcePath: implementation.SourcePath(),
			line:       position.Line,
			column:     position.Column,
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
		if err := constructorgraph.CheckRequirementSource(input.Source); err != nil {
			return nil, nil, fmt.Errorf("%w: requirements[%d] source %v", ErrInvalidInput, index, err)
		}
		if _, intrinsic := intrinsics[identifier]; intrinsic {
			intrinsicSources[identifier] = append(intrinsicSources[identifier], input.Source.Reference)
			continue
		}
		if strings.HasPrefix(input.InterfaceID.Name(), "kernel.") {
			return nil, nil, fmt.Errorf("%w: required reserved Interface %s is not published by the selected Kernel API", ErrUnknownInterface, input.InterfaceID)
		}
		if _, visible := interfaces[identifier]; !visible {
			return nil, nil, fmt.Errorf("%w: required Interface %s is not defined by a visible canonical package", ErrUnknownInterface, input.InterfaceID)
		}
		result = append(result, Requirement{InterfaceID: input.InterfaceID, Source: input.Source})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].InterfaceID != result[right].InterfaceID {
			return result[left].InterfaceID.String() < result[right].InterfaceID.String()
		}
		return result[left].Source.Reference < result[right].Source.Reference
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
		sources, err := normalizeChoiceSources(input.Sources)
		if err != nil {
			return nil, fmt.Errorf("%w: choices[%d] sources: %v", ErrInvalidInput, index, err)
		}
		if strings.HasPrefix(input.InterfaceID.Name(), "kernel.") {
			return nil, &IntrinsicChoiceError{
				interfaceID: input.InterfaceID,
				constructor: input.Constructor,
				sources:     sources,
			}
		}
		if _, visible := catalog.interfaces[identifier]; !visible {
			return nil, fmt.Errorf("%w: interfaces.use[%q] is not defined by a visible canonical package", ErrUnknownInterface, identifier)
		}
		constructor, visible := catalog.constructors[constructorID]
		if !visible {
			return nil, &UnknownConstructorError{
				interfaceID: input.InterfaceID,
				constructor: input.Constructor,
				sources:     sources,
			}
		}
		if _, compatible := constructor.implements[identifier]; !compatible {
			return nil, &IncompatibleChoiceError{
				interfaceID: input.InterfaceID,
				constructor: input.Constructor,
				sources:     sources,
			}
		}
		normalized := normalizedChoice{
			interfaceID: input.InterfaceID,
			constructor: input.Constructor,
			sources:     sources,
		}
		if previous, duplicate := result[identifier]; duplicate {
			if previous.constructor != normalized.constructor {
				return nil, fmt.Errorf("%w: interfaces.use[%q] selects both %s and %s", ErrInvalidInput, identifier, previous.constructor, normalized.constructor)
			}
			previous.sources = uniqueSortedChoiceSources(append(previous.sources, normalized.sources...))
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
		sources = choiceSourceReferences(choice.sources)
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
				values[index] = Candidate{
					constructor: candidate.symbol,
					source:      candidate.source,
					modulePath:  candidate.modulePath,
					sourcePath:  candidate.sourcePath,
					line:        candidate.line,
					column:      candidate.column,
				}
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

func normalizeChoiceSources(inputs []ChoiceSource) ([]ChoiceSource, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one typed source is required")
	}
	values := make([]ChoiceSource, len(inputs))
	for index, input := range inputs {
		reference, err := normalizeSource(input.Reference)
		if err != nil {
			return nil, fmt.Errorf("sources[%d].reference %v", index, err)
		}
		if err := modulepath.CheckProject(input.ModulePath); err != nil {
			return nil, fmt.Errorf("sources[%d].module_path %q is invalid: %v", index, input.ModulePath, err)
		}
		if input.Path == "" || path.IsAbs(input.Path) || path.Clean(input.Path) != input.Path || input.Path == "." || input.Path == ".." || strings.HasPrefix(input.Path, "../") || strings.Contains(input.Path, "/../") || strings.Contains(input.Path, "\\") || strings.ContainsAny(input.Path, "\x00\r\n") || !utf8.ValidString(input.Path) {
			return nil, fmt.Errorf("sources[%d].path %q must be one safe module-relative slash path", index, input.Path)
		}
		if input.Line < 1 || input.Column < 1 {
			return nil, fmt.Errorf("sources[%d].line and column must be positive", index)
		}
		input.Reference = reference
		values[index] = input
	}
	return uniqueSortedChoiceSources(values), nil
}

func uniqueSortedChoiceSources(values []ChoiceSource) []ChoiceSource {
	sort.Slice(values, func(left, right int) bool {
		leftKey := choiceSourceKey(values[left])
		rightKey := choiceSourceKey(values[right])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return values[left].Reference < values[right].Reference
	})
	result := values[:0]
	for _, value := range values {
		if len(result) != 0 && choiceSourceKey(result[len(result)-1]) == choiceSourceKey(value) {
			continue
		}
		result = append(result, value)
	}
	return append([]ChoiceSource(nil), result...)
}

func choiceSourceKey(value ChoiceSource) string {
	return strings.Join([]string{
		value.ModulePath,
		value.Path,
		fmt.Sprintf("%010d", value.Line),
		fmt.Sprintf("%010d", value.Column),
	}, "\x00")
}

func choiceSourceReferences(values []ChoiceSource) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Reference
	}
	return uniqueSorted(result)
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
