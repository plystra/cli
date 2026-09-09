package applicationmeta

import "path/filepath"

const environmentOverlaySourceKind = "configuration-declaration"

// EnvironmentOverlayError attaches the selected overlay document to a typed
// overlay-composition failure without changing its human message or sentinel
// chain.
type EnvironmentOverlayError struct {
	source ConfigurationDeclarationSource
	cause  error
}

// ModulePath returns the current Project module that owns the overlay.
func (e *EnvironmentOverlayError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.source.ModulePath()
}

// SourcePath returns the slash-separated Project-relative overlay path.
func (e *EnvironmentOverlayError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.source.Path()
}

// SourceKind returns the canonical diagnostic source category.
func (e *EnvironmentOverlayError) SourceKind() string {
	if e == nil {
		return ""
	}
	return environmentOverlaySourceKind
}

// Line returns the one-based document line.
func (e *EnvironmentOverlayError) Line() int {
	if e == nil {
		return 0
	}
	return e.source.Line()
}

// Column returns the one-based document column.
func (e *EnvironmentOverlayError) Column() int {
	if e == nil {
		return 0
	}
	return e.source.Column()
}

func (e *EnvironmentOverlayError) Error() string {
	if e == nil || e.cause == nil {
		return ErrApplyOverlay.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original overlay error chain.
func (e *EnvironmentOverlayError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func environmentOverlayError(overlay Manifest, cause error) error {
	if cause == nil {
		return nil
	}
	return &EnvironmentOverlayError{
		source: ConfigurationDeclarationSource{
			modulePath: overlay.modulePath,
			path:       filepath.ToSlash(overlay.source),
			line:       1,
			column:     1,
		},
		cause: cause,
	}
}
