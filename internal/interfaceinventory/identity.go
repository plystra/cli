package interfaceinventory

import (
	"errors"
	"fmt"
	"strings"
)

// ErrDuplicateID reports one exact Interface ID defined by more than one
// visible Go package.
var ErrDuplicateID = errors.New("duplicate visible Interface ID")

// ValidateUniqueIDs rejects the first duplicate Interface ID in canonical ID
// order while retaining every defining package and source location for that
// identity.
func ValidateUniqueIDs(index Index) error {
	interfaces := index.interfaces
	for first := 0; first < len(interfaces); {
		last := first + 1
		for last < len(interfaces) && interfaces[last].ID() == interfaces[first].ID() {
			last++
		}
		if last-first > 1 {
			return &DuplicateIDError{
				id:          interfaces[first].ID(),
				definitions: append([]Interface(nil), interfaces[first:last]...),
			}
		}
		first = last
	}
	return nil
}

// DuplicateIDError identifies every visible Go package and source location
// defining one exact Interface ID.
type DuplicateIDError struct {
	id          string
	definitions []Interface
}

// ID returns the duplicated exact Interface ID.
func (e *DuplicateIDError) ID() string {
	if e == nil {
		return ""
	}
	return e.id
}

// Definitions returns every conflicting Interface definition in deterministic
// package and source order.
func (e *DuplicateIDError) Definitions() []Interface {
	if e == nil {
		return nil
	}
	return append([]Interface(nil), e.definitions...)
}

func (e *DuplicateIDError) Error() string {
	if e == nil {
		return ErrDuplicateID.Error()
	}
	definitions := make([]string, len(e.definitions))
	for index, definition := range e.definitions {
		definitions[index] = fmt.Sprintf("package %q at %s", definition.PackagePath(), definition.Source())
	}
	return fmt.Sprintf("%s %q in [%s]", ErrDuplicateID, e.id, strings.Join(definitions, ", "))
}

// Unwrap supports errors.Is with ErrDuplicateID.
func (*DuplicateIDError) Unwrap() error { return ErrDuplicateID }
