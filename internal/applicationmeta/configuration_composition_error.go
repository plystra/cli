package applicationmeta

// ConfigurationCompositionError locates the template or selected current
// document responsible for a final configuration invariant failure.
type ConfigurationCompositionError struct {
	source ConfigurationDeclarationSource
	cause  error
}

// ModulePath returns the Project module that owns the declaration.
func (e *ConfigurationCompositionError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.source.ModulePath()
}

// SourcePath returns the slash-separated module-relative document path.
func (e *ConfigurationCompositionError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.source.Path()
}

// SourceKind returns the canonical diagnostic source category.
func (e *ConfigurationCompositionError) SourceKind() string {
	if e == nil {
		return ""
	}
	return "configuration-declaration"
}

// Line returns the one-based document line.
func (e *ConfigurationCompositionError) Line() int {
	if e == nil {
		return 0
	}
	return e.source.Line()
}

// Column returns the one-based document column.
func (e *ConfigurationCompositionError) Column() int {
	if e == nil {
		return 0
	}
	return e.source.Column()
}

func (e *ConfigurationCompositionError) Error() string {
	if e == nil || e.cause == nil {
		return ErrCompose.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original composition and validation error chain.
func (e *ConfigurationCompositionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
