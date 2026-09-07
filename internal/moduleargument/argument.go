// Package moduleargument validates public Go Module paths and queries before a
// dependency command can mutate a Plystra Project.
package moduleargument

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/mod/module"
)

var (
	// ErrInvalidQuery reports a malformed Go Module query supplied to a public
	// dependency command.
	ErrInvalidQuery = errors.New("invalid Go Module query")
	// ErrInvalidPath reports a malformed exact Go Module path supplied to a
	// public dependency command.
	ErrInvalidPath = errors.New("invalid Go Module path")
)

// classifiedError preserves the established human-facing parser message while
// exposing one stable error class to public command boundaries.
type classifiedError struct {
	class error
	cause error
}

func (e *classifiedError) Error() string { return e.cause.Error() }
func (e *classifiedError) Unwrap() error { return e.cause }
func (e *classifiedError) Is(target error) bool {
	return target == e.class
}

func classify(class, cause error) error {
	return &classifiedError{class: class, cause: cause}
}

// ParseQuery validates one standard Go Module query and returns its exact
// module path. Removal queries are rejected in favor of plystra remove.
func ParseQuery(value string) (string, string, error) {
	query := strings.TrimSpace(value)
	if query == "" {
		return "", "", classify(ErrInvalidQuery, errors.New("invalid Go Module query: value is empty"))
	}
	if query != value || strings.HasPrefix(query, "-") || strings.IndexFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", "", classify(ErrInvalidQuery, fmt.Errorf("invalid Go Module query %q", value))
	}
	path := query
	if separator := strings.LastIndexByte(query, '@'); separator >= 0 {
		path = query[:separator]
		version := query[separator+1:]
		if version == "" {
			return "", "", classify(ErrInvalidQuery, fmt.Errorf("invalid Go Module query %q: version query is empty", query))
		}
		if version == "none" {
			return "", "", classify(ErrInvalidQuery, errors.New("invalid Go Module query @none: use plystra remove to remove the dependency"))
		}
	}
	if err := module.CheckPath(path); err != nil {
		return "", "", classify(ErrInvalidQuery, fmt.Errorf("invalid Go Module path %q: %w", path, err))
	}
	return query, path, nil
}

// ParsePath validates one exact Go Module path without a version query.
func ParsePath(value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		return "", classify(ErrInvalidPath, errors.New("invalid Go Module path: value is empty"))
	}
	if path != value || strings.HasPrefix(path, "-") || strings.Contains(path, "@") || strings.IndexFunc(path, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", classify(ErrInvalidPath, fmt.Errorf("invalid Go Module path %q: provide a path without a version query", value))
	}
	if err := module.CheckPath(path); err != nil {
		return "", classify(ErrInvalidPath, fmt.Errorf("invalid Go Module path %q: %w", path, err))
	}
	return path, nil
}
