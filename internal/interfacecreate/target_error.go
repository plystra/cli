package interfacecreate

// TargetExistsError retains the existing package path or canonical Interface
// declaration that prevents creation without inspecting an occupied target.
type TargetExistsError struct {
	modulePath string
	sourcePath string
	line       int
	column     int
	cause      error
}

// ModulePath returns the owning Project's Go Module identity.
func (e *TargetExistsError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the slash-separated path within the owning Module.
func (e *TargetExistsError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind distinguishes a discovered declaration from an occupied package.
func (e *TargetExistsError) SourceKind() string {
	if e == nil || e.sourcePath == "" {
		return ""
	}
	if e.line > 0 {
		return "interface-declaration"
	}
	return "authored-package"
}

// Line returns the discovered declaration line, or zero for an occupied path.
func (e *TargetExistsError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the discovered declaration column, or zero when unavailable.
func (e *TargetExistsError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *TargetExistsError) Error() string {
	if e == nil || e.cause == nil {
		return ErrTargetExists.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the existing target-conflict sentinel.
func (e *TargetExistsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
