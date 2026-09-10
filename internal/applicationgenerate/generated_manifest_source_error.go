package applicationgenerate

import (
	"errors"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/generatedfiles"
)

const generatedManifestSourceKind = "generated-artifact"

// GeneratedManifestSourceError attaches the current Project identity and the
// ownership manifest path to a generated-manifest failure without changing its
// human message or typed error chain.
type GeneratedManifestSourceError struct {
	modulePath string
	cause      error
}

// ModulePath returns the current Project module containing the manifest.
func (e *GeneratedManifestSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the canonical module-relative ownership-manifest path.
func (e *GeneratedManifestSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return generatedfiles.ManifestPath
}

// SourceKind returns the canonical diagnostic source category.
func (e *GeneratedManifestSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return generatedManifestSourceKind
}

// Line returns zero because generated ownership manifests have no authored span.
func (e *GeneratedManifestSourceError) Line() int { return 0 }

// Column returns zero because generated ownership manifests have no authored span.
func (e *GeneratedManifestSourceError) Column() int { return 0 }

func (e *GeneratedManifestSourceError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid generated ownership manifest"
	}
	return e.cause.Error()
}

// Unwrap preserves the original generated-manifest failure chain.
func (e *GeneratedManifestSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func generatedManifestSourceError(modulePath string, cause error) error {
	if modulePath == "" || cause == nil || !errors.Is(cause, generatedfiles.ErrManifest) ||
		errors.Is(cause, ErrConcurrentChange) || errors.Is(cause, applicationresolve.ErrConcurrentChange) || errors.Is(cause, atomicfs.ErrConcurrentChange) {
		return cause
	}
	var existing *GeneratedManifestSourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	return &GeneratedManifestSourceError{modulePath: modulePath, cause: cause}
}
