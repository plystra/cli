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

// ParseQuery validates one standard Go Module query and returns its exact
// module path. Removal queries are rejected in favor of plystra remove.
func ParseQuery(value string) (string, string, error) {
	query := strings.TrimSpace(value)
	if query == "" {
		return "", "", errors.New("Go Module query is empty")
	}
	if query != value || strings.HasPrefix(query, "-") || strings.IndexFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", "", fmt.Errorf("Go Module query %q is invalid", value)
	}
	path := query
	if separator := strings.LastIndexByte(query, '@'); separator >= 0 {
		path = query[:separator]
		version := query[separator+1:]
		if version == "" {
			return "", "", fmt.Errorf("Go Module query %q has an empty version query", query)
		}
		if version == "none" {
			return "", "", errors.New("Go Module query @none removes a dependency; use plystra remove")
		}
	}
	if err := module.CheckPath(path); err != nil {
		return "", "", fmt.Errorf("Go Module path %q: %w", path, err)
	}
	return query, path, nil
}

// ParsePath validates one exact Go Module path without a version query.
func ParsePath(value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		return "", errors.New("Go Module path is empty")
	}
	if path != value || strings.HasPrefix(path, "-") || strings.Contains(path, "@") || strings.IndexFunc(path, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", fmt.Errorf("Go Module path %q is invalid; provide a path without a version query", value)
	}
	if err := module.CheckPath(path); err != nil {
		return "", fmt.Errorf("Go Module path %q: %w", path, err)
	}
	return path, nil
}
