package applicationgenerate

const dependencySourceKind = "module-dependency"
const dependencySourcePath = "go.mod"

// DependencySourceError attaches the current Project's Go Module declaration
// to an actionable generated-runtime dependency failure without changing its
// human message or typed error chain.
type DependencySourceError struct {
	modulePath string
	cause      error
}

// ModulePath returns the current Project module that owns the dependency
// declaration.
func (e *DependencySourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative Go Module manifest path.
func (e *DependencySourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return dependencySourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *DependencySourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return dependencySourceKind
}

// Line returns the conservative one-based document line.
func (e *DependencySourceError) Line() int {
	if e == nil {
		return 0
	}
	return 1
}

// Column returns the conservative one-based document column.
func (e *DependencySourceError) Column() int {
	if e == nil {
		return 0
	}
	return 1
}

func (e *DependencySourceError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid application dependency"
	}
	return e.cause.Error()
}

// Unwrap preserves the original dependency failure chain.
func (e *DependencySourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func dependencySourceError(modulePath string, cause error) error {
	if cause == nil || modulePath == "" {
		return cause
	}
	return &DependencySourceError{modulePath: modulePath, cause: cause}
}
