package interfaceinventory

// SourceError attaches stable owning-Project provenance to an authored
// Interface or Implementation error without changing its human message or
// typed error chain.
type SourceError struct {
	modulePath string
	sourcePath string
	sourceKind string
	line       int
	column     int
	cause      error
}

// ModulePath returns the Go Module identity that owns the authored source.
func (e *SourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable slash-separated module-relative path.
func (e *SourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical lower-kebab source category.
func (e *SourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return e.sourceKind
}

// Line returns the one-based source line, or zero when unavailable.
func (e *SourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the one-based source column, or zero when unavailable.
func (e *SourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *SourceError) Error() string {
	if e == nil || e.cause == nil {
		return "authored source error"
	}
	return e.cause.Error()
}

// Unwrap preserves the original typed authored error chain.
func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func sourceError(modulePath, sourcePath, sourceKind string, line, column int, cause error) error {
	if cause == nil {
		return nil
	}
	return &SourceError{
		modulePath: modulePath,
		sourcePath: sourcePath,
		sourceKind: sourceKind,
		line:       line,
		column:     column,
		cause:      cause,
	}
}
