package constructorgraph

import (
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

type dependencyPath struct {
	root    Root
	steps   []PathStep
	missing interfaceid.Identifier
}

func (p dependencyPath) clone() dependencyPath {
	result := p
	result.root.sources = append([]string(nil), p.root.sources...)
	result.steps = clonePathSteps(p.steps)
	return result
}

// PathStep is one constructor dependency edge and its resolved target, when
// available. Source and selection data are stable and contain no configuration
// values.
type PathStep struct {
	requiringConstructor constructorsymbol.Symbol
	requiringSource      string
	interfaceID          interfaceid.Identifier
	parameterName        string
	parameterPosition    int
	optional             bool
	selectedConstructor  constructorsymbol.Symbol
	selectionReason      SelectionReason
	selectionSources     []string
}

// RequiringConstructor returns the constructor that declares this dependency.
func (s PathStep) RequiringConstructor() constructorsymbol.Symbol {
	return s.requiringConstructor
}

// RequiringSource returns stable module-qualified constructor provenance.
func (s PathStep) RequiringSource() string { return s.requiringSource }

// InterfaceID returns the required or available optional Interface edge.
func (s PathStep) InterfaceID() interfaceid.Identifier { return s.interfaceID }

// ParameterName returns the authored constructor parameter name, when present.
func (s PathStep) ParameterName() string { return s.parameterName }

// ParameterPosition returns the one-based constructor parameter position.
func (s PathStep) ParameterPosition() int { return s.parameterPosition }

// Optional reports whether the edge came from plystra.Optional[T].
func (s PathStep) Optional() bool { return s.optional }

// SelectedConstructor returns the target constructor, or zero when missing.
func (s PathStep) SelectedConstructor() constructorsymbol.Symbol {
	return s.selectedConstructor
}

// SelectionReason returns the target selection reason, or zero when missing.
func (s PathStep) SelectionReason() SelectionReason { return s.selectionReason }

// SelectionSources returns the target selection provenance.
func (s PathStep) SelectionSources() []string {
	return append([]string(nil), s.selectionSources...)
}

func (s PathStep) clone() PathStep {
	s.selectionSources = append([]string(nil), s.selectionSources...)
	return s
}

func clonePathSteps(values []PathStep) []PathStep {
	result := append([]PathStep(nil), values...)
	for index := range result {
		result[index].selectionSources = append([]string(nil), result[index].selectionSources...)
	}
	return result
}

// MissingBindingError reports one required Interface absent from the supplied
// frozen selection set and retains its complete requiring path.
type MissingBindingError struct {
	path dependencyPath
}

// InterfaceID returns the missing exact Interface ID.
func (e *MissingBindingError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.path.missing
}

// Root returns the root requirement that reaches the missing Interface.
func (e *MissingBindingError) Root() Root {
	if e == nil {
		return Root{}
	}
	root := e.path.root
	root.sources = append([]string(nil), root.sources...)
	return root
}

// Steps returns the complete dependency path. The final step identifies the
// missing Interface and has no selected constructor.
func (e *MissingBindingError) Steps() []PathStep {
	if e == nil {
		return nil
	}
	return clonePathSteps(e.path.steps)
}

func (e *MissingBindingError) Error() string {
	if e == nil {
		return ErrMissingBinding.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: %s required from [%s]", ErrMissingBinding, e.path.root.interfaceID, strings.Join(e.path.root.sources, ", "))
	for _, step := range e.path.steps {
		kind := "requires"
		if step.optional {
			kind = "optionally uses"
		}
		fmt.Fprintf(
			&message,
			"; %s at %s %s %s through parameter %d",
			step.requiringConstructor,
			step.requiringSource,
			kind,
			step.interfaceID,
			step.parameterPosition,
		)
		if step.parameterName != "" {
			fmt.Fprintf(&message, " (%s)", step.parameterName)
		}
		if step.selectedConstructor.String() != "" {
			fmt.Fprintf(
				&message,
				" selected %s by %s from [%s]",
				step.selectedConstructor,
				step.selectionReason,
				strings.Join(step.selectionSources, ", "),
			)
		}
	}
	fmt.Fprintf(&message, "; no selected binding exists for %s; correction: select one compatible visible Implementation before generation", e.path.missing)
	return message.String()
}

// Unwrap supports errors.Is with ErrMissingBinding.
func (*MissingBindingError) Unwrap() error { return ErrMissingBinding }

// CycleError reports one complete deterministic constructor dependency cycle.
// Steps are ordered around the cycle and the final selected constructor equals
// the first requiring constructor.
type CycleError struct {
	steps []PathStep
}

// Steps returns a defensive complete ordered cycle.
func (e *CycleError) Steps() []PathStep {
	if e == nil {
		return nil
	}
	return clonePathSteps(e.steps)
}

func (e *CycleError) Error() string {
	if e == nil {
		return ErrCycle.Error()
	}
	var message strings.Builder
	message.WriteString(ErrCycle.Error())
	for _, step := range e.steps {
		kind := "requires"
		if step.optional {
			kind = "optionally uses"
		}
		fmt.Fprintf(
			&message,
			"; %s at %s %s %s through parameter %d",
			step.requiringConstructor,
			step.requiringSource,
			kind,
			step.interfaceID,
			step.parameterPosition,
		)
		if step.parameterName != "" {
			fmt.Fprintf(&message, " (%s)", step.parameterName)
		}
		fmt.Fprintf(
			&message,
			" -> %s selected by %s from [%s]",
			step.selectedConstructor,
			step.selectionReason,
			strings.Join(step.selectionSources, ", "),
		)
	}
	message.WriteString("; correction: remove one constructor dependency or select an acyclic compatible Implementation")
	return message.String()
}

// Unwrap supports errors.Is with ErrCycle.
func (*CycleError) Unwrap() error { return ErrCycle }
