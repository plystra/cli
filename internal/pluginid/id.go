// Package pluginid validates canonical Plugin IDs inside the standalone CLI.
package pluginid

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid reports a non-canonical Plugin ID.
var ErrInvalid = errors.New("invalid plugin ID")

// Validate checks a lower-case dot-separated Plugin ID.
func Validate(value string) error {
	segments := strings.Split(value, ".")
	if len(segments) < 2 {
		return invalid()
	}
	for _, segment := range segments {
		if !ValidSegment(segment) {
			return invalid()
		}
	}
	return nil
}

// ValidSegment reports whether value is one canonical Plugin ID segment.
func ValidSegment(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func invalid() error {
	return fmt.Errorf("%w: expected lower-case dot-separated segments", ErrInvalid)
}
