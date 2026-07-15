// Package capabilityid parses canonical capability references inside the
// independently distributed CLI.
package capabilityid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid reports a non-canonical capability reference.
var ErrInvalid = errors.New("invalid capability reference")

// Identifier names one exact major version of a capability contract.
type Identifier struct {
	name  string
	major uint64
}

// New validates a capability name and positive major version.
func New(name string, major uint64) (Identifier, error) {
	if !validName(name) {
		return Identifier{}, invalid("invalid name")
	}
	if major == 0 {
		return Identifier{}, invalid("major version must be positive")
	}
	return Identifier{name: name, major: major}, nil
}

// Parse parses an exact identity such as email.send/v1.
func Parse(value string) (Identifier, error) {
	name, version, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(version, "/") || len(version) < 2 || version[0] != 'v' {
		return Identifier{}, invalid("expected <capability-name>/v<major>")
	}
	if version[1] == '0' {
		return Identifier{}, invalid("major version must not have a leading zero")
	}
	major, err := strconv.ParseUint(version[1:], 10, 64)
	if err != nil || major == 0 {
		return Identifier{}, invalid("invalid major version")
	}
	return New(name, major)
}

// Name returns the unversioned capability name.
func (i Identifier) Name() string { return i.name }

// Major returns the exact capability contract major version.
func (i Identifier) Major() uint64 { return i.major }

// String returns the canonical exact identity, or an empty string for the zero
// value.
func (i Identifier) String() string {
	if i.name == "" || i.major == 0 {
		return ""
	}
	return i.name + "/v" + strconv.FormatUint(i.major, 10)
}

// Reference is a canonical capability name with an optional explicit major
// version.
type Reference struct {
	name  string
	major uint64
}

// ParseReference parses either account.register or account.register/v2.
func ParseReference(value string) (Reference, error) {
	if strings.Contains(value, "/") {
		identifier, err := Parse(value)
		if err != nil {
			return Reference{}, err
		}
		return Reference(identifier), nil
	}
	if !validName(value) {
		return Reference{}, invalid("expected <capability-name> with optional /v<major>")
	}
	return Reference{name: value}, nil
}

// Name returns the unversioned capability name.
func (r Reference) Name() string { return r.name }

// Major returns the explicit major version, or zero when it was omitted.
func (r Reference) Major() uint64 { return r.major }

// Versioned reports whether the reference contains an explicit major version.
func (r Reference) Versioned() bool { return r.name != "" && r.major != 0 }

// Exact returns the exact identifier when a version was supplied.
func (r Reference) Exact() (Identifier, bool) {
	if !r.Versioned() {
		return Identifier{}, false
	}
	return Identifier(r), true
}

// String returns the canonical reference, or an empty string for the zero
// value.
func (r Reference) String() string {
	if r.name == "" {
		return ""
	}
	if r.major == 0 {
		return r.name
	}
	return r.name + "/v" + strconv.FormatUint(r.major, 10)
}

func validName(name string) bool {
	segments := strings.Split(name, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if !validSegment(segment) {
			return false
		}
	}
	return true
}

func validSegment(segment string) bool {
	if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
		return false
	}
	for index := 1; index < len(segment); index++ {
		character := segment[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
