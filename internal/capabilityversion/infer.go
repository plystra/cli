// Package capabilityversion plans exact capability versions for authoring
// commands without making filesystem changes.
package capabilityversion

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/capabilityid"
)

var (
	// ErrInfer reports invalid inputs or an impossible capability version plan.
	ErrInfer = errors.New("infer capability version")
	// ErrOverflow reports that no major exists above the highest visible version.
	ErrOverflow = errors.New("capability major version overflow")
)

// Action identifies whether authoring creates a new exact capability or
// implements one that is already visible.
type Action string

const (
	ActionCreate    Action = "create"
	ActionImplement Action = "implement"
)

// Caution identifies why a plan requires explicit confirmation.
type Caution string

const (
	CautionNone     Caution = ""
	CautionExisting Caution = "existing"
	CautionOlder    Caution = "older"
	CautionSkipped  Caution = "skipped"
)

// Plan is one deterministic capability authoring decision.
type Plan struct {
	target  capabilityid.Identifier
	source  capabilityid.Identifier
	highest capabilityid.Identifier
	action  Action
	caution Caution
}

// Target returns the exact capability identity to author or implement.
func (p Plan) Target() capabilityid.Identifier { return p.target }

// Source returns the existing schema version to reuse or copy when one is
// appropriate.
func (p Plan) Source() (capabilityid.Identifier, bool) {
	return p.source, p.source.String() != ""
}

// HighestVisible returns the highest visible version of the target capability
// name, when one exists.
func (p Plan) HighestVisible() (capabilityid.Identifier, bool) {
	return p.highest, p.highest.String() != ""
}

// Action returns whether the target is new or already exists.
func (p Plan) Action() Action { return p.action }

// Caution returns the confirmation reason, or CautionNone.
func (p Plan) Caution() Caution { return p.caution }

// RequiresConfirmation reports whether a future mutating command must receive
// explicit user acceptance before applying the plan.
func (p Plan) RequiresConfirmation() bool { return p.caution != CautionNone }

// Infer plans a capability version from one canonical reference and all
// visible exact identifiers. Identifiers with other names are ignored.
func Infer(reference capabilityid.Reference, visible []capabilityid.Identifier) (Plan, error) {
	if reference.String() == "" {
		return Plan{}, fmt.Errorf("%w: reference is empty", ErrInfer)
	}

	majors := make(map[uint64]struct{}, len(visible))
	var highestMajor uint64
	for index, identifier := range visible {
		if identifier.String() == "" {
			return Plan{}, fmt.Errorf("%w: visible identifier %d is empty", ErrInfer, index)
		}
		if identifier.Name() != reference.Name() {
			continue
		}
		majors[identifier.Major()] = struct{}{}
		if identifier.Major() > highestMajor {
			highestMajor = identifier.Major()
		}
	}

	highest, err := makeIdentifier(reference.Name(), highestMajor)
	if err != nil {
		return Plan{}, err
	}
	if !reference.Versioned() {
		targetMajor := uint64(1)
		if highestMajor != 0 {
			if highestMajor == ^uint64(0) {
				return Plan{}, fmt.Errorf("%w: %w for %s", ErrInfer, ErrOverflow, reference.Name())
			}
			targetMajor = highestMajor + 1
		}
		target, err := capabilityid.New(reference.Name(), targetMajor)
		if err != nil {
			return Plan{}, fmt.Errorf("%w: create target: %v", ErrInfer, err)
		}
		return Plan{target: target, source: highest, highest: highest, action: ActionCreate}, nil
	}

	target, _ := reference.Exact()
	if _, exists := majors[target.Major()]; exists {
		return Plan{
			target:  target,
			source:  target,
			highest: highest,
			action:  ActionImplement,
			caution: CautionExisting,
		}, nil
	}

	plan := Plan{target: target, highest: highest, action: ActionCreate}
	switch {
	case highestMajor == 0 && target.Major() == 1:
	case highestMajor == 0:
		plan.caution = CautionSkipped
	case target.Major() < highestMajor:
		plan.caution = CautionOlder
	case target.Major() == highestMajor+1:
		plan.source = highest
	default:
		plan.source = highest
		plan.caution = CautionSkipped
	}
	return plan, nil
}

func makeIdentifier(name string, major uint64) (capabilityid.Identifier, error) {
	if major == 0 {
		return capabilityid.Identifier{}, nil
	}
	identifier, err := capabilityid.New(name, major)
	if err != nil {
		return capabilityid.Identifier{}, fmt.Errorf("%w: highest visible version: %v", ErrInfer, err)
	}
	return identifier, nil
}
