package applicationmeta

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
)

// ResourceConfigurationError identifies the instance-owned Config boundary.
// Its reasons share the constructor Config sentinel families; private values,
// unknown keys, defaults, and Secret targets never enter the diagnostic.
type ResourceConfigurationError struct {
	instance string
	provider constructorsymbol.Symbol
	field    string
	source   ConfigurationDeclarationSource
	reason   error
}

func (e *ResourceConfigurationError) InstanceName() string                   { return e.instance }
func (e *ResourceConfigurationError) Provider() constructorsymbol.Symbol     { return e.provider }
func (e *ResourceConfigurationError) Field() string                          { return e.field }
func (e *ResourceConfigurationError) Source() ConfigurationDeclarationSource { return e.source }
func (e *ResourceConfigurationError) ModulePath() string                     { return e.source.ModulePath() }
func (e *ResourceConfigurationError) SourcePath() string                     { return e.source.Path() }
func (*ResourceConfigurationError) SourceKind() string                       { return constructorConfigurationSourceKind }
func (e *ResourceConfigurationError) Line() int                              { return e.source.Line() }
func (e *ResourceConfigurationError) Column() int                            { return e.source.Column() }
func (e *ResourceConfigurationError) Error() string                          { return fmt.Sprintf("%s: %s", e.field, e.reason) }
func (e *ResourceConfigurationError) Unwrap() error                          { return e.reason }

func resourceConfigurationError(instance ResourceInstance, source ConfigurationDeclarationSource, err error) error {
	field := resourceInstancePath(instance.name) + ".config"
	reason := err
	var value *ConstructorConfigurationValueError
	if errors.As(err, &value) {
		field = constructorConfigDecisionSource(field, value.segments)
		reason = errors.Join(ErrConfigurationValues, value.reason)
	} else if errors.Is(err, ErrConfigurationSchema) {
		reason = ErrConfigurationSchema
	} else {
		reason = errors.Join(ErrConfigurationValues, err)
	}
	return &ResourceConfigurationError{instance: instance.name, provider: instance.provider, field: field, source: source, reason: reason}
}
