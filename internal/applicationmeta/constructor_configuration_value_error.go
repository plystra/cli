package applicationmeta

import (
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
)

// ConstructorConfigurationValueError reports one constructor configuration
// path that does not match its compiled same-package Config schema, together
// with the Project document that owns the invalid declaration.
type ConstructorConfigurationValueError struct {
	constructor constructorsymbol.Symbol
	segments    []string
	reference   string
	source      ConfigurationDeclarationSource
	reason      error
}

// Constructor returns the exact constructor that owns the configuration.
func (e *ConstructorConfigurationValueError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// Field returns the stable redacted configuration path that failed schema
// validation. Unknown authored field names remain excluded.
func (e *ConstructorConfigurationValueError) Field() string {
	if e == nil {
		return ""
	}
	return constructorConfigPath(e.constructor, e.segments)
}

// ModulePath returns the Go Module identity that owns the configuration.
func (e *ConstructorConfigurationValueError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.source.ModulePath()
}

// SourcePath returns the slash-separated module-relative configuration path.
func (e *ConstructorConfigurationValueError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.source.Path()
}

// SourceKind returns the canonical diagnostic source category.
func (e *ConstructorConfigurationValueError) SourceKind() string {
	if e == nil {
		return ""
	}
	return constructorConfigurationSourceKind
}

// Line returns the one-based declaration line, or zero when unavailable.
func (e *ConstructorConfigurationValueError) Line() int {
	if e == nil {
		return 0
	}
	return e.source.Line()
}

// Column returns the one-based declaration column, or zero when unavailable.
func (e *ConstructorConfigurationValueError) Column() int {
	if e == nil {
		return 0
	}
	return e.source.Column()
}

func (e *ConstructorConfigurationValueError) Error() string {
	if e == nil {
		return ErrConfigurationValues.Error()
	}
	reason := e.reason
	if reason == nil {
		reason = ErrConfigurationInvalidValue
	}
	prefix := e.Field()
	if e.reference != "" {
		prefix += " at " + e.reference
	}
	return fmt.Sprintf("%s: %s: %s", prefix, ErrConfigurationValues, reason)
}

// Unwrap supports errors.Is with both the configuration-value family and the
// exact redacted reason class.
func (e *ConstructorConfigurationValueError) Unwrap() []error {
	if e == nil || e.reason == nil {
		return []error{ErrConfigurationValues, ErrConfigurationInvalidValue}
	}
	return []error{ErrConfigurationValues, e.reason}
}

func newConstructorConfigurationValueError(constructor constructorsymbol.Symbol, segments []string, reference string, source ConfigurationDeclarationSource, reason error) error {
	return &ConstructorConfigurationValueError{
		constructor: constructor,
		segments:    append([]string(nil), segments...),
		reference:   reference,
		source:      source,
		reason:      reason,
	}
}
