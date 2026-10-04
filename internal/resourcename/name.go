// Package resourcename validates the exact configured identity of a Resource instance.
package resourcename

import (
	"errors"
	"regexp"
)

// MaximumLength bounds a Resource instance name in ASCII bytes.
const MaximumLength = 128

// ErrInvalid reports a name outside the canonical dot-separated lower-kebab grammar.
var ErrInvalid = errors.New("invalid Resource instance name")

var grammar = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$`)

// Check validates a name without normalizing it or exposing invalid input.
func Check(name string) error {
	if len(name) == 0 || len(name) > MaximumLength || !grammar.MatchString(name) {
		return ErrInvalid
	}
	return nil
}
