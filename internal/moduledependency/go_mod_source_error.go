package moduledependency

const goModSourceKind = "module-dependency"
const goModSourcePath = "go.mod"

// GoModSourceError attaches the current Project's Go Module declaration to an
// invalid dependency requirement without changing its human message or typed
// error chain.
type GoModSourceError struct {
	modulePath string
	line       int
	column     int
	cause      error
}

// ModulePath returns the current Project module that owns the invalid
// dependency declaration.
func (e *GoModSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative Go Module manifest path.
func (e *GoModSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return goModSourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *GoModSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return goModSourceKind
}

// Line returns the exact one-based declaration line.
func (e *GoModSourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the exact one-based declaration column.
func (e *GoModSourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *GoModSourceError) Error() string {
	if e == nil || e.cause == nil {
		return ErrInvalidGoMod.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original invalid-go.mod failure chain.
func (e *GoModSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func goModSourceError(modulePath string, line, column int, cause error) error {
	if cause == nil || modulePath == "" || line <= 0 || column <= 0 {
		return cause
	}
	return &GoModSourceError{
		modulePath: modulePath,
		line:       line,
		column:     column,
		cause:      cause,
	}
}
