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
)

// Candidate is one compatible visible Implementation retained by an
// ambiguity diagnostic.
type Candidate struct {
	constructor constructorsymbol.Symbol
	source      string
}

// Constructor returns the candidate's fully qualified constructor symbol.
func (c Candidate) Constructor() constructorsymbol.Symbol { return c.constructor }

// Source returns stable module-qualified constructor provenance.
func (c Candidate) Source() string { return c.source }

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
