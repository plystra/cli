package capabilitysource

import (
	"errors"
	"path"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
)

const manifestSourceKind = "provider-declaration"

// ManifestSourceError attaches the owning Provider declaration to an invalid
// authored capability manifest without changing its human message or typed
// error chain.
type ManifestSourceError struct {
	capabilityID capabilityid.Identifier
	modulePath   string
	sourcePath   string
	cause        error
}

// CapabilityID returns the exact canonical Capability whose declaration failed.
func (e *ManifestSourceError) CapabilityID() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capabilityID
}

// ModulePath returns the Go Module that owns the invalid Provider declaration.
func (e *ManifestSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative capability.yaml path.
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

// Line returns the conservative one-based document position.
func (e *ManifestSourceError) Line() int {
	if e == nil {
		return 0
	}
	return 1
}

// Column returns the conservative one-based document position.
func (e *ManifestSourceError) Column() int {
	if e == nil {
		return 0
	}
	return 1
}

func (e *ManifestSourceError) Error() string {
	if e == nil || e.cause == nil {
		return capabilitymeta.ErrInvalidManifest.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original manifest failure chain.
func (e *ManifestSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// WithManifestSource attaches trusted module-relative provenance only when
// cause identifies an invalid authored capability manifest.
func WithManifestSource(modulePath, pluginPath string, capability capabilityid.Identifier, cause error) error {
	if cause == nil || !errors.Is(cause, capabilitymeta.ErrInvalidManifest) {
		return cause
	}
	var existing *ManifestSourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	return &ManifestSourceError{
		capabilityID: capability,
		modulePath:   modulePath,
		sourcePath:   path.Join(pluginPath, RelativePath(capability)),
		cause:        cause,
	}
}
