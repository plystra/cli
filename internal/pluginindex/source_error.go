package pluginindex

import (
	"errors"

	"github.com/plystra/cli/internal/pluginmeta"
)

const manifestSourceKind = "plugin-declaration"

// ManifestSourceError attaches stable owning-Project provenance to an authored
// plugin manifest failure without changing its human message or typed error
// chain.
type ManifestSourceError struct {
	modulePath string
	sourcePath string
	line       int
	column     int
	cause      error
}

// ModulePath returns the Go Module identity that owns the plugin declaration.
func (e *ManifestSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable slash-separated module-relative plugin.yaml
// path.
func (e *ManifestSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *ManifestSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return manifestSourceKind
}

// Line returns the one-based declaration line.
func (e *ManifestSourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the one-based declaration column.
func (e *ManifestSourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *ManifestSourceError) Error() string {
	if e == nil || e.cause == nil {
		return pluginmeta.ErrInvalidManifest.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original plugin manifest failure chain.
func (e *ManifestSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func manifestSourceError(modulePath, sourcePath string, cause error) error {
	if cause == nil || !errors.Is(cause, pluginmeta.ErrInvalidManifest) {
		return cause
	}
	line, column := 1, 1
	var positioned interface {
		Line() int
		Column() int
	}
	if errors.As(cause, &positioned) && positioned != nil && positioned.Line() > 0 && positioned.Column() > 0 {
		line = positioned.Line()
		column = positioned.Column()
	}
	return &ManifestSourceError{
		modulePath: modulePath,
		sourcePath: sourcePath,
		line:       line,
		column:     column,
		cause:      cause,
	}
}

func generationPackageSourceError(modulePath, sourcePath string, declaration pluginmeta.Generation, cause error) error {
	if cause == nil || !errors.Is(cause, ErrInvalidGenerationPackage) || declaration.PackageLine() < 1 || declaration.PackageColumn() < 1 {
		return cause
	}
	return &ManifestSourceError{
		modulePath: modulePath,
		sourcePath: sourcePath,
		line:       declaration.PackageLine(),
		column:     declaration.PackageColumn(),
		cause:      cause,
	}
}
