package applicationmeta

import (
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
)

const constructorConfigurationSourceKind = "configuration-declaration"

// ConstructorConfigurationSchemaError reports constructor-keyed configuration
// whose constructor has no compiled same-package Config schema, together with
// the Project document that owns the invalid declaration.
type ConstructorConfigurationSchemaError struct {
	constructor constructorsymbol.Symbol
	reference   string
	source      ConfigurationDeclarationSource
}

// Constructor returns the exact constructor named by the configuration key.
func (e *ConstructorConfigurationSchemaError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// ModulePath returns the Go Module identity that owns the configuration.
func (e *ConstructorConfigurationSchemaError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.source.ModulePath()
}

// SourcePath returns the slash-separated module-relative configuration path.
func (e *ConstructorConfigurationSchemaError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.source.Path()
}

// SourceKind returns the canonical diagnostic source category.
func (e *ConstructorConfigurationSchemaError) SourceKind() string {
	if e == nil {
		return ""
	}
	return constructorConfigurationSourceKind
}

// Line returns the one-based declaration line, or zero when unavailable.
func (e *ConstructorConfigurationSchemaError) Line() int {
	if e == nil {
		return 0
	}
	return e.source.Line()
}

// Column returns the one-based declaration column, or zero when unavailable.
func (e *ConstructorConfigurationSchemaError) Column() int {
	if e == nil {
		return 0
	}
	return e.source.Column()
}

func (e *ConstructorConfigurationSchemaError) Error() string {
	if e == nil {
		return ErrConfigurationSchema.Error()
	}
	if e.reference == "" {
		return fmt.Sprintf("%s for constructor %q", ErrConfigurationSchema, e.constructor)
	}
	return fmt.Sprintf("%s for constructor %q at %s", ErrConfigurationSchema, e.constructor, e.reference)
}

// Unwrap supports errors.Is with ErrConfigurationSchema.
func (*ConstructorConfigurationSchemaError) Unwrap() error { return ErrConfigurationSchema }

func newConstructorConfigurationSchemaError(constructor constructorsymbol.Symbol, reference string, source ConfigurationDeclarationSource) error {
	return &ConstructorConfigurationSchemaError{
		constructor: constructor,
		reference:   reference,
		source:      source,
	}
}
