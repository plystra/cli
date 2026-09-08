package interfaceresolution

import (
	"errors"
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

var (
	// ErrResolve reports failure to select and validate one Interface binding
	// closure.
	ErrResolve = errors.New("resolve Interface Implementations")
	// ErrInvalidInput reports inconsistent visible inventory, requirement, or
	// explicit-choice input.
	ErrInvalidInput = errors.New("invalid Interface resolution input")
	// ErrUnknownInterface reports a requirement or choice for an Interface not
	// defined by exactly one visible canonical package.
	ErrUnknownInterface = errors.New("unknown Interface")
	// ErrUnknownConstructor reports an explicit choice naming a constructor
	// outside the effective visible Project graph.
	ErrUnknownConstructor = errors.New("unknown Implementation constructor")
	// ErrIncompatibleChoice reports an explicit constructor that does not
	// implement the selected canonical Interface.
	ErrIncompatibleChoice = errors.New("incompatible Implementation choice")
	// ErrAmbiguousImplementation reports a required Interface with several
	// compatible visible constructors and no explicit effective choice.
	ErrAmbiguousImplementation = errors.New("ambiguous Interface Implementation")
	// ErrReservedInterface reports application code declaring an Interface in
	// the intrinsic kernel.* namespace.
	ErrReservedInterface = errors.New("reserved intrinsic Kernel Interface")
	// ErrIntrinsicChoice reports an ordinary Implementation selection for an
	// Interface that Kernel construction supplies intrinsically.
	ErrIntrinsicChoice = errors.New("intrinsic Kernel Interface cannot select an Implementation")
)

// UnknownInterfaceError identifies one required or explicitly selected
// Interface that is absent from the visible canonical catalog and retains
// every declaration that introduced that exact reference.
type UnknownInterfaceError struct {
	interfaceID        interfaceid.Identifier
	kernelAPI          bool
	requirementSources []RequirementSource
	choiceSources      []ChoiceSource
}

// InterfaceID returns the exact missing Interface ID.
func (e *UnknownInterfaceError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// RequirementSources returns every sorted typed requirement or exposure that
// introduced the missing Interface.
func (e *UnknownInterfaceError) RequirementSources() []RequirementSource {
	if e == nil {
		return nil
	}
	return append([]RequirementSource(nil), e.requirementSources...)
}

// ChoiceSources returns every sorted typed interfaces.use declaration that
// selected the missing Interface.
func (e *UnknownInterfaceError) ChoiceSources() []ChoiceSource {
	if e == nil {
		return nil
	}
	return append([]ChoiceSource(nil), e.choiceSources...)
}

func (e *UnknownInterfaceError) Error() string {
	if e == nil {
		return ErrUnknownInterface.Error()
	}
	if len(e.choiceSources) != 0 {
		return fmt.Sprintf(
			"%s: interfaces.use[%q] is not defined by a visible canonical package",
			ErrUnknownInterface,
			e.interfaceID,
		)
	}
	if e.kernelAPI {
		return fmt.Sprintf(
			"%s: required reserved Interface %s is not published by the selected Kernel API",
			ErrUnknownInterface,
			e.interfaceID,
		)
	}
	return fmt.Sprintf(
		"%s: required Interface %s is not defined by a visible canonical package",
		ErrUnknownInterface,
		e.interfaceID,
	)
}

// Unwrap supports errors.Is with ErrUnknownInterface.
func (*UnknownInterfaceError) Unwrap() error { return ErrUnknownInterface }

// UnknownConstructorError identifies an explicit choice whose constructor is
// outside the effective visible Project graph and retains every declaration
// that contributed the rejected selection.
type UnknownConstructorError struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	sources     []ChoiceSource
}

// InterfaceID returns the exact selected Interface ID.
func (e *UnknownConstructorError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// Constructor returns the invisible constructor symbol.
func (e *UnknownConstructorError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// ChoiceSources returns every sorted typed module-relative declaration that
// contributed the invalid effective selection.
func (e *UnknownConstructorError) ChoiceSources() []ChoiceSource {
	if e == nil {
		return nil
	}
	return append([]ChoiceSource(nil), e.sources...)
}

func (e *UnknownConstructorError) Error() string {
	if e == nil {
		return ErrUnknownConstructor.Error()
	}
	return fmt.Sprintf(
		"%s: interfaces.use[%q] names invisible constructor %s",
		ErrUnknownConstructor,
		e.interfaceID,
		e.constructor,
	)
}

// Unwrap supports errors.Is with ErrUnknownConstructor.
func (*UnknownConstructorError) Unwrap() error { return ErrUnknownConstructor }

// IncompatibleChoiceError identifies an explicit constructor that does not
// implement the selected canonical Interface and retains every declaration
// that contributed the rejected selection.
type IncompatibleChoiceError struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	sources     []ChoiceSource
}

// InterfaceID returns the exact selected Interface ID.
func (e *IncompatibleChoiceError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// Constructor returns the incompatible constructor symbol.
func (e *IncompatibleChoiceError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// ChoiceSources returns every sorted typed module-relative declaration that
// contributed the invalid effective selection.
func (e *IncompatibleChoiceError) ChoiceSources() []ChoiceSource {
	if e == nil {
		return nil
	}
	return append([]ChoiceSource(nil), e.sources...)
}

func (e *IncompatibleChoiceError) Error() string {
	if e == nil {
		return ErrIncompatibleChoice.Error()
	}
	return fmt.Sprintf(
		"%s: constructor %s does not implement Interface %s",
		ErrIncompatibleChoice,
		e.constructor,
		e.interfaceID,
	)
}

// Unwrap supports errors.Is with ErrIncompatibleChoice.
func (*IncompatibleChoiceError) Unwrap() error { return ErrIncompatibleChoice }

// IntrinsicChoiceError identifies an effective ordinary Implementation choice
// for an Interface supplied intrinsically by Kernel and retains every
// declaration that contributed the forbidden selection.
type IntrinsicChoiceError struct {
	interfaceID interfaceid.Identifier
	constructor constructorsymbol.Symbol
	sources     []ChoiceSource
}

// InterfaceID returns the exact intrinsic Interface ID.
func (e *IntrinsicChoiceError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// Constructor returns the ordinary constructor selected for the intrinsic
// Interface.
func (e *IntrinsicChoiceError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// ChoiceSources returns every sorted typed module-relative declaration that
// contributed the forbidden effective selection.
func (e *IntrinsicChoiceError) ChoiceSources() []ChoiceSource {
	if e == nil {
		return nil
	}
	return append([]ChoiceSource(nil), e.sources...)
}

func (e *IntrinsicChoiceError) Error() string {
	if e == nil {
		return ErrIntrinsicChoice.Error()
	}
	return fmt.Sprintf(
		"%s: interfaces.use[%q] names %s",
		ErrIntrinsicChoice,
		e.interfaceID,
		e.constructor,
	)
}

// Unwrap supports errors.Is with ErrIntrinsicChoice.
func (*IntrinsicChoiceError) Unwrap() error { return ErrIntrinsicChoice }

// Candidate is one compatible visible Implementation retained by an
// ambiguity diagnostic.
type Candidate struct {
	constructor constructorsymbol.Symbol
	source      string
	modulePath  string
	sourcePath  string
	line        int
	column      int
}

// Constructor returns the candidate's fully qualified constructor symbol.
func (c Candidate) Constructor() constructorsymbol.Symbol { return c.constructor }

// Source returns stable module-qualified constructor provenance.
func (c Candidate) Source() string { return c.source }

// ModulePath returns the Go Module identity that owns the candidate.
func (c Candidate) ModulePath() string { return c.modulePath }

// SourcePath returns the slash-separated module-relative constructor source.
func (c Candidate) SourcePath() string { return c.sourcePath }

// Line returns the one-based constructor declaration line.
func (c Candidate) Line() int { return c.line }

// Column returns the one-based constructor declaration column.
func (c Candidate) Column() int { return c.column }

// AmbiguousImplementationError identifies every compatible candidate for one
// required Interface in deterministic constructor-symbol order.
type AmbiguousImplementationError struct {
	interfaceID interfaceid.Identifier
	candidates  []Candidate
}

// InterfaceID returns the unresolved exact Interface ID.
func (e *AmbiguousImplementationError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// Candidates returns a defensive constructor-symbol-ordered view.
func (e *AmbiguousImplementationError) Candidates() []Candidate {
	if e == nil {
		return nil
	}
	return append([]Candidate(nil), e.candidates...)
}

func (e *AmbiguousImplementationError) Error() string {
	if e == nil {
		return ErrAmbiguousImplementation.Error()
	}
	values := make([]string, len(e.candidates))
	for index, candidate := range e.candidates {
		values[index] = fmt.Sprintf("%s at %s", candidate.constructor, candidate.source)
	}
	return fmt.Sprintf(
		"%s %s: %d compatible constructors [%s]; correction: set interfaces.use[%q] to one exact constructor symbol",
		ErrAmbiguousImplementation,
		e.interfaceID,
		len(e.candidates),
		strings.Join(values, ", "),
		e.interfaceID.String(),
	)
}

// Unwrap supports errors.Is with ErrAmbiguousImplementation.
func (*AmbiguousImplementationError) Unwrap() error { return ErrAmbiguousImplementation }
