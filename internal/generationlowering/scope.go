// Package generationlowering converts validated generation contributions into
// deterministic CLI-owned application code.
package generationlowering

import (
	"errors"
	"fmt"
	"go/token"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/module"
)

const (
	maximumScopeEntries = 16 << 10
	maximumSourceBytes  = 4096
)

var (
	// ErrScope reports failure to construct one deterministic generated Go
	// lexical scope.
	ErrScope = errors.New("build generated Go scope")
	// ErrInvalidImport reports an invalid generated import request.
	ErrInvalidImport = errors.New("invalid generated Go import")
	// ErrInvalidIdentifier reports an invalid generated identifier request.
	ErrInvalidIdentifier = errors.New("invalid generated Go identifier")
	// ErrIdentifierCollision reports two distinct generated meanings that would
	// occupy the same Go identifier in one lexical scope.
	ErrIdentifierCollision = errors.New("generated Go identifier collision")
)

// ImportRequest declares one generated import use and its exact diagnostic
// provenance. Repeated requests for the same path and name are merged.
type ImportRequest struct {
	Path   string
	Name   string
	Source string
	_      struct{}
}

// IdentifierRequest reserves one non-import identifier in the same generated
// Go lexical scope. Exact duplicate requests deduplicate; distinct sources may
// not claim the same identifier.
type IdentifierRequest struct {
	Name   string
	Source string
	_      struct{}
}

// Import is one immutable merged generated import.
type Import struct {
	path    string
	name    string
	sources []string
}

// Path returns the canonical Go import path.
func (i Import) Path() string { return i.path }

// Name returns the explicit local package identifier.
func (i Import) Name() string { return i.name }

// Sources returns sorted unique diagnostic provenance.
func (i Import) Sources() []string { return append([]string(nil), i.sources...) }

// Identifier is one immutable reserved non-import identifier.
type Identifier struct {
	name   string
	source string
}

// Name returns the exact generated Go identifier.
func (i Identifier) Name() string { return i.name }

// Source returns its diagnostic provenance.
func (i Identifier) Source() string { return i.source }

// Scope is one immutable generated Go lexical scope. Imports and identifiers
// are returned in canonical structural order, independent of request order.
type Scope struct {
	imports     []Import
	identifiers []Identifier
}

// Imports returns defensive immutable imports sorted by path and name.
func (s Scope) Imports() []Import {
	result := make([]Import, len(s.imports))
	for index, value := range s.imports {
		result[index] = Import{
			path:    value.path,
			name:    value.name,
			sources: append([]string(nil), value.sources...),
		}
	}
	return result
}

// Identifiers returns defensive immutable reservations sorted by name and
// source.
func (s Scope) Identifiers() []Identifier {
	return append([]Identifier(nil), s.identifiers...)
}

// BuildScope merges structurally identical imports and identifier requests,
// then rejects every ambiguous Go name with deterministic complete provenance.
func BuildScope(imports []ImportRequest, identifiers []IdentifierRequest) (Scope, error) {
	if len(imports)+len(identifiers) > maximumScopeEntries {
		return Scope{}, fmt.Errorf(
			"%w: scope contains %d requests; maximum is %d",
			ErrScope,
			len(imports)+len(identifiers),
			maximumScopeEntries,
		)
	}

	importRequests := append([]ImportRequest(nil), imports...)
	sort.Slice(importRequests, func(left, right int) bool {
		return importRequestKey(importRequests[left]) < importRequestKey(importRequests[right])
	})
	for index, request := range importRequests {
		if err := validateImportRequest(request); err != nil {
			return Scope{}, fmt.Errorf("%w: imports[%d]: %w", ErrScope, index, err)
		}
	}

	identifierRequests := append([]IdentifierRequest(nil), identifiers...)
	sort.Slice(identifierRequests, func(left, right int) bool {
		return identifierRequestKey(identifierRequests[left]) < identifierRequestKey(identifierRequests[right])
	})
	for index, request := range identifierRequests {
		if err := validateIdentifierRequest(request); err != nil {
			return Scope{}, fmt.Errorf("%w: identifiers[%d]: %w", ErrScope, index, err)
		}
	}

	mergedImports, err := mergeImports(importRequests)
	if err != nil {
		return Scope{}, fmt.Errorf("%w: %w", ErrScope, err)
	}
	mergedIdentifiers := mergeIdentifiers(identifierRequests)
	if err := rejectIdentifierCollisions(mergedImports, mergedIdentifiers); err != nil {
		return Scope{}, fmt.Errorf("%w: %w", ErrScope, err)
	}

	return Scope{
		imports:     mergedImports,
		identifiers: mergedIdentifiers,
	}, nil
}

func validateImportRequest(request ImportRequest) error {
	if err := module.CheckImportPath(request.Path); err != nil {
		return fmt.Errorf("%w: path %q: %v", ErrInvalidImport, request.Path, err)
	}
	if !validIdentifier(request.Name) {
		return fmt.Errorf("%w: name %q is not a non-blank ASCII Go identifier", ErrInvalidImport, request.Name)
	}
	if !validSource(request.Source) {
		return fmt.Errorf("%w: source %q must be non-empty, bounded UTF-8 without surrounding whitespace or control characters", ErrInvalidImport, request.Source)
	}
	return nil
}

func validateIdentifierRequest(request IdentifierRequest) error {
	if !validIdentifier(request.Name) {
		return fmt.Errorf("%w: name %q is not a non-blank ASCII Go identifier", ErrInvalidIdentifier, request.Name)
	}
	if !validSource(request.Source) {
		return fmt.Errorf("%w: source %q must be non-empty, bounded UTF-8 without surrounding whitespace or control characters", ErrInvalidIdentifier, request.Source)
	}
	return nil
}

func validIdentifier(value string) bool {
	if value == "" || value == "_" || !token.IsIdentifier(value) || token.Lookup(value).IsKeyword() {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' || index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func validSource(value string) bool {
	if value == "" || len(value) > maximumSourceBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < ' ' || character == '\x7f' {
			return false
		}
	}
	return true
}

func mergeImports(requests []ImportRequest) ([]Import, error) {
	byPath := make(map[string]map[string]map[string]struct{})
	for _, request := range requests {
		byName, exists := byPath[request.Path]
		if !exists {
			byName = make(map[string]map[string]struct{})
			byPath[request.Path] = byName
		}
		sources, exists := byName[request.Name]
		if !exists {
			sources = make(map[string]struct{})
			byName[request.Name] = sources
		}
		sources[request.Source] = struct{}{}
	}

	paths := sortedKeys(byPath)
	imports := make([]Import, 0, len(paths))
	for _, importPath := range paths {
		byName := byPath[importPath]
		names := sortedKeys(byName)
		if len(names) != 1 {
			claims := make([]string, len(names))
			for index, name := range names {
				claims[index] = fmt.Sprintf("%q from [%s]", name, strings.Join(sortedKeys(byName[name]), ", "))
			}
			return nil, fmt.Errorf(
				"%w: import path %q requests incompatible identifiers [%s]",
				ErrIdentifierCollision,
				importPath,
				strings.Join(claims, "; "),
			)
		}
		imports = append(imports, Import{
			path:    importPath,
			name:    names[0],
			sources: sortedKeys(byName[names[0]]),
		})
	}
	sort.Slice(imports, func(left, right int) bool {
		if imports[left].path != imports[right].path {
			return imports[left].path < imports[right].path
		}
		return imports[left].name < imports[right].name
	})
	return imports, nil
}

func mergeIdentifiers(requests []IdentifierRequest) []Identifier {
	seen := make(map[string]struct{}, len(requests))
	identifiers := make([]Identifier, 0, len(requests))
	for _, request := range requests {
		key := identifierRequestKey(request)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		identifiers = append(identifiers, Identifier{name: request.Name, source: request.Source})
	}
	return identifiers
}

type identifierClaim struct {
	name        string
	description string
}

func rejectIdentifierCollisions(imports []Import, identifiers []Identifier) error {
	claims := make(map[string][]identifierClaim, len(imports)+len(identifiers))
	for _, imported := range imports {
		claims[imported.name] = append(claims[imported.name], identifierClaim{
			name: imported.name,
			description: fmt.Sprintf(
				"import %q from [%s]",
				imported.path,
				strings.Join(imported.sources, ", "),
			),
		})
	}
	for _, identifier := range identifiers {
		claims[identifier.name] = append(claims[identifier.name], identifierClaim{
			name:        identifier.name,
			description: fmt.Sprintf("identifier from %q", identifier.source),
		})
	}

	names := sortedKeys(claims)
	for _, name := range names {
		matches := claims[name]
		if len(matches) < 2 {
			continue
		}
		descriptions := make([]string, len(matches))
		for index, match := range matches {
			descriptions[index] = match.description
		}
		sort.Strings(descriptions)
		return fmt.Errorf(
			"%w: identifier %q is claimed by [%s]",
			ErrIdentifierCollision,
			name,
			strings.Join(descriptions, "; "),
		)
	}
	return nil
}

func importRequestKey(request ImportRequest) string {
	return strings.Join([]string{request.Path, request.Name, request.Source}, "\x00")
}

func identifierRequestKey(request IdentifierRequest) string {
	return request.Name + "\x00" + request.Source
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
