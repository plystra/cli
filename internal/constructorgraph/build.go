package constructorgraph

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

var (
	// ErrBuild reports a constructor-graph build failure.
	ErrBuild = errors.New("build constructor dependency graph")
	// ErrInvalidInput reports inconsistent requirements, selections, or
	// discovered constructor provenance.
	ErrInvalidInput = errors.New("invalid constructor dependency graph input")
	// ErrMissingBinding reports a required Interface with no supplied selected
	// Implementation binding.
	ErrMissingBinding = errors.New("missing required Interface binding")
	// ErrCycle reports a synchronous selected-constructor dependency cycle.
	ErrCycle = errors.New("constructor dependency cycle")
)

type normalizedConstructor struct {
	implementation implementationinventory.Implementation
	symbol         constructorsymbol.Symbol
	source         string
	implements     map[string]struct{}
	dependencies   []normalizedDependency
}

type normalizedDependency struct {
	interfaceID       interfaceid.Identifier
	packagePath       string
	parameterName     string
	parameterPosition int
	optional          bool
}

type normalizedSelection struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	reason      SelectionReason
	sources     []string
}

// Build validates one already resolved selection set and constructs its exact
// reachable constructor graph. Optional parameters without a supplied binding
// remain unavailable and never create a requirement.
func Build(input Input) (Graph, error) {
	constructors, err := normalizeConstructors(input.Implementations)
	if err != nil {
		return Graph{}, fmt.Errorf("%w: %w: %v", ErrBuild, ErrInvalidInput, err)
	}
	return build(constructors, input.Requirements, input.Selections)
}

func build(constructors []normalizedConstructor, requirements []Requirement, selections []Selection) (Graph, error) {
	constructorIndex, err := indexConstructors(constructors)
	if err != nil {
		return Graph{}, fmt.Errorf("%w: %w: %v", ErrBuild, ErrInvalidInput, err)
	}
	roots, err := normalizeRequirements(requirements)
	if err != nil {
		return Graph{}, fmt.Errorf("%w: %w: %v", ErrBuild, ErrInvalidInput, err)
	}
	selectionIndex, err := normalizeSelections(selections, constructorIndex)
	if err != nil {
		return Graph{}, fmt.Errorf("%w: %w: %v", ErrBuild, ErrInvalidInput, err)
	}

	builder := graphBuilder{
		constructors: constructorIndex,
		selections:   selectionIndex,
		states:       make(map[string]visitState, len(constructorIndex)),
		bindings:     make(map[string]Binding, len(selectionIndex)),
	}
	for _, root := range roots {
		path := dependencyPath{root: root}
		if err := builder.visitInterface(root.interfaceID, path, nil); err != nil {
			return Graph{}, fmt.Errorf("%w: %w", ErrBuild, err)
		}
	}

	bindings := make([]Binding, 0, len(builder.bindings))
	for _, binding := range builder.bindings {
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(left, right int) bool {
		return bindings[left].interfaceID.String() < bindings[right].interfaceID.String()
	})
	return Graph{
		roots:        cloneRoots(roots),
		bindings:     cloneBindings(bindings),
		construction: cloneNodes(builder.construction),
	}, nil
}

func normalizeConstructors(index implementationinventory.Index) ([]normalizedConstructor, error) {
	implementations := index.Implementations()
	result := make([]normalizedConstructor, len(implementations))
	for index, implementation := range implementations {
		symbol := implementation.Symbol()
		if symbol.String() == "" || implementation.Source() == "" {
			return nil, errors.New("discovered Implementation has empty identity or source")
		}
		implemented := implementation.Declaration().ImplementedInterfaces()
		if len(implemented) == 0 {
			return nil, fmt.Errorf("Implementation %s declares no Interface", symbol)
		}
		identities := make(map[string]struct{}, len(implemented))
		for _, declaration := range implemented {
			identifier := declaration.ID().String()
			if identifier == "" {
				return nil, fmt.Errorf("Implementation %s declares an empty Interface", symbol)
			}
			if _, duplicate := identities[identifier]; duplicate {
				return nil, fmt.Errorf("Implementation %s declares Interface %s more than once", symbol, identifier)
			}
			identities[identifier] = struct{}{}
		}

		required := implementation.RequiredInterfaces()
		optional := implementation.OptionalInterfaces()
		dependencies := make([]normalizedDependency, 0, len(required)+len(optional))
		for _, dependency := range required {
			dependencies = append(dependencies, normalizedDependency{
				interfaceID:       dependency.ID(),
				packagePath:       dependency.PackagePath(),
				parameterName:     dependency.ParameterName(),
				parameterPosition: dependency.ParameterPosition(),
			})
		}
		for _, dependency := range optional {
			dependencies = append(dependencies, normalizedDependency{
				interfaceID:       dependency.ID(),
				packagePath:       dependency.PackagePath(),
				parameterName:     dependency.ParameterName(),
				parameterPosition: dependency.ParameterPosition(),
				optional:          true,
			})
		}
		sort.Slice(dependencies, func(left, right int) bool {
			return dependencies[left].parameterPosition < dependencies[right].parameterPosition
		})
		for dependencyIndex, dependency := range dependencies {
			if dependency.interfaceID.String() == "" || dependency.packagePath == "" || dependency.parameterPosition < 1 {
				return nil, fmt.Errorf("Implementation %s has invalid dependency metadata", symbol)
			}
			if dependencyIndex != 0 && dependencies[dependencyIndex-1].parameterPosition == dependency.parameterPosition {
				return nil, fmt.Errorf("Implementation %s has duplicate parameter position %d", symbol, dependency.parameterPosition)
			}
		}
		result[index] = normalizedConstructor{
			implementation: implementation,
			symbol:         symbol,
			source:         implementation.Source(),
			implements:     identities,
			dependencies:   dependencies,
		}
	}
	return result, nil
}

func indexConstructors(constructors []normalizedConstructor) (map[string]normalizedConstructor, error) {
	result := make(map[string]normalizedConstructor, len(constructors))
	for _, constructor := range constructors {
		value := constructor.symbol.String()
		if value == "" || constructor.source == "" || len(constructor.implements) == 0 {
			return nil, errors.New("constructor has empty identity, source, or Interface declarations")
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fmt.Errorf("constructor %s appears more than once", constructor.symbol)
		}
		positions := make(map[int]struct{}, len(constructor.dependencies))
		for _, dependency := range constructor.dependencies {
			if dependency.interfaceID.String() == "" || dependency.packagePath == "" || dependency.parameterPosition < 1 {
				return nil, fmt.Errorf("constructor %s has invalid dependency metadata", constructor.symbol)
			}
			if _, duplicate := positions[dependency.parameterPosition]; duplicate {
				return nil, fmt.Errorf("constructor %s repeats parameter position %d", constructor.symbol, dependency.parameterPosition)
			}
			positions[dependency.parameterPosition] = struct{}{}
		}
		result[value] = constructor
	}
	return result, nil
}

func normalizeRequirements(inputs []Requirement) ([]Root, error) {
	byID := make(map[string]Root, len(inputs))
	for index, input := range inputs {
		identifier := input.InterfaceID.String()
		if identifier == "" {
			return nil, fmt.Errorf("requirements[%d] has an empty Interface ID", index)
		}
		source, err := normalizeSource(input.Source)
		if err != nil {
			return nil, fmt.Errorf("requirements[%d] source %v", index, err)
		}
		root := byID[identifier]
		root.interfaceID = input.InterfaceID
		root.sources = append(root.sources, source)
		byID[identifier] = root
	}
	result := make([]Root, 0, len(byID))
	for _, root := range byID {
		root.sources = uniqueSorted(root.sources)
		result = append(result, root)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].interfaceID.String() < result[right].interfaceID.String()
	})
	return result, nil
}

func normalizeSelections(inputs []Selection, constructors map[string]normalizedConstructor) (map[string]normalizedSelection, error) {
	result := make(map[string]normalizedSelection, len(inputs))
	for index, input := range inputs {
		identifier := input.InterfaceID.String()
		constructorID := input.Constructor.String()
		if identifier == "" || constructorID == "" {
			return nil, fmt.Errorf("selections[%d] has an empty Interface or constructor", index)
		}
		if !input.Reason.Valid() {
			return nil, fmt.Errorf("selections[%d] has invalid reason %q", index, input.Reason)
		}
		if len(input.Sources) == 0 {
			return nil, fmt.Errorf("selections[%d] has no source", index)
		}
		sources := make([]string, len(input.Sources))
		for sourceIndex, value := range input.Sources {
			source, err := normalizeSource(value)
			if err != nil {
				return nil, fmt.Errorf("selections[%d].sources[%d] %v", index, sourceIndex, err)
			}
			sources[sourceIndex] = source
		}
		sources = uniqueSorted(sources)
		constructor, exists := constructors[constructorID]
		if !exists {
			return nil, fmt.Errorf("selection for Interface %s names invisible constructor %s", identifier, input.Constructor)
		}
		if _, implements := constructor.implements[identifier]; !implements {
			return nil, fmt.Errorf("selection for Interface %s names constructor %s, which does not declare that Interface", identifier, input.Constructor)
		}
		normalized := normalizedSelection{
			interfaceID: input.InterfaceID,
			constructor: input.Constructor,
			reason:      input.Reason,
			sources:     sources,
		}
		if previous, duplicate := result[identifier]; duplicate {
			if previous.constructor != normalized.constructor || previous.reason != normalized.reason {
				return nil, fmt.Errorf("Interface %s has conflicting selected constructors or reasons", identifier)
			}
			previous.sources = uniqueSorted(append(previous.sources, normalized.sources...))
			result[identifier] = previous
			continue
		}
		result[identifier] = normalized
	}
	return result, nil
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

func cloneRoots(values []Root) []Root {
	result := append([]Root(nil), values...)
	for index := range result {
		result[index].sources = append([]string(nil), result[index].sources...)
	}
	return result
}

type visitState uint8

const (
	visitActive visitState = iota + 1
	visitDone
)

type activeFrame struct {
	constructor normalizedConstructor
	incoming    *PathStep
}

type graphBuilder struct {
	constructors map[string]normalizedConstructor
	selections   map[string]normalizedSelection
	states       map[string]visitState
	bindings     map[string]Binding
	stack        []activeFrame
	construction []Node
}

func (b *graphBuilder) visitInterface(identifier interfaceid.Identifier, path dependencyPath, incoming *PathStep) error {
	selection, exists := b.selections[identifier.String()]
	if !exists {
		path.missing = identifier
		return &MissingBindingError{path: path.clone()}
	}
	binding := Binding{
		interfaceID: selection.interfaceID,
		constructor: selection.constructor,
		reason:      selection.reason,
		sources:     append([]string(nil), selection.sources...),
	}
	b.bindings[identifier.String()] = binding
	constructor := b.constructors[selection.constructor.String()]

	switch b.states[selection.constructor.String()] {
	case visitDone:
		return nil
	case visitActive:
		if incoming == nil {
			return fmt.Errorf("%w: active constructor %s was reached without a dependency edge", ErrInvalidInput, selection.constructor)
		}
		return b.cycle(selection.constructor, *incoming)
	}

	b.states[selection.constructor.String()] = visitActive
	b.stack = append(b.stack, activeFrame{constructor: constructor, incoming: clonePathStepPointer(incoming)})
	dependencies := make([]Dependency, 0, len(constructor.dependencies))
	for _, declared := range constructor.dependencies {
		selected, available := b.selections[declared.interfaceID.String()]
		dependency := Dependency{
			interfaceID:       declared.interfaceID,
			packagePath:       declared.packagePath,
			parameterName:     declared.parameterName,
			parameterPosition: declared.parameterPosition,
			optional:          declared.optional,
			available:         !declared.optional || available,
		}
		if declared.optional && !available {
			dependencies = append(dependencies, dependency)
			continue
		}
		if available {
			dependency.constructor = selected.constructor
		}
		dependencies = append(dependencies, dependency)
		step := PathStep{
			requiringConstructor: constructor.symbol,
			requiringSource:      constructor.source,
			interfaceID:          declared.interfaceID,
			parameterName:        declared.parameterName,
			parameterPosition:    declared.parameterPosition,
			optional:             declared.optional,
		}
		if available {
			step.selectedConstructor = selected.constructor
			step.selectionReason = selected.reason
			step.selectionSources = append([]string(nil), selected.sources...)
		}
		nextPath := path.clone()
		nextPath.steps = append(nextPath.steps, step)
		if err := b.visitInterface(declared.interfaceID, nextPath, &step); err != nil {
			return err
		}
	}

	b.stack = b.stack[:len(b.stack)-1]
	b.states[selection.constructor.String()] = visitDone
	b.construction = append(b.construction, Node{
		implementation: constructor.implementation,
		symbol:         constructor.symbol,
		source:         constructor.source,
		dependencies:   dependencies,
	})
	return nil
}

func (b *graphBuilder) cycle(target constructorsymbol.Symbol, closing PathStep) error {
	start := -1
	for index, frame := range b.stack {
		if frame.constructor.symbol == target {
			start = index
			break
		}
	}
	if start < 0 {
		return fmt.Errorf("%w: active constructor %s is absent from traversal stack", ErrInvalidInput, target)
	}
	steps := make([]PathStep, 0, len(b.stack)-start)
	for index := start + 1; index < len(b.stack); index++ {
		if b.stack[index].incoming == nil {
			return fmt.Errorf("%w: constructor %s has no incoming dependency", ErrInvalidInput, b.stack[index].constructor.symbol)
		}
		steps = append(steps, b.stack[index].incoming.clone())
	}
	steps = append(steps, closing.clone())
	return &CycleError{steps: steps}
}

func clonePathStepPointer(value *PathStep) *PathStep {
	if value == nil {
		return nil
	}
	cloned := value.clone()
	return &cloned
}
