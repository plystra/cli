// Package interfaceid parses canonical Plystra Interface IDs.
package interfaceid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid reports a non-canonical Interface ID.
var ErrInvalid = errors.New("invalid Interface ID")

// Identifier names one exact major version of an Interface.
type Identifier struct {
	name  string
	major uint64
}

// New validates an Interface name and positive major version.
func New(name string, major uint64) (Identifier, error) {
	if !validName(name) {
		return Identifier{}, invalid("invalid name")
	}
	if major == 0 {
		return Identifier{}, invalid("major version must be positive")
	}
	return Identifier{name: name, major: major}, nil
}

// Parse parses an exact Interface ID such as order.create/v1.
func Parse(value string) (Identifier, error) {
	name, version, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(version, "/") || len(version) < 2 || version[0] != 'v' {
		return Identifier{}, invalid("expected <interface-name>/v<major>")
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

// Name returns the unversioned Interface name.
func (i Identifier) Name() string { return i.name }

// Major returns the exact Interface major version.
func (i Identifier) Major() uint64 { return i.major }

// String returns the canonical exact ID, or an empty string for the zero value.
func (i Identifier) String() string {
	if i.name == "" || i.major == 0 {
		return ""
	}
	return i.name + "/v" + strconv.FormatUint(i.major, 10)
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
