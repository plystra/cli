// Package projectlocate finds the nearest enclosing Plystra Project.
package projectlocate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/modulelocate"
)

const ManifestName = "plystra.yaml"

var (
	// ErrLocate reports that Project-root inspection failed.
	ErrLocate = errors.New("locate Plystra Project")
	// ErrNotFound reports that the nearest Go Module is not a Plystra Project.
	ErrNotFound = errors.New("project root not found")
	// ErrInvalidManifest reports an unsafe root Project marker.
	ErrInvalidManifest = errors.New("invalid Plystra Project marker")
)

const manifestSourceKind = "project-marker"

// ManifestSourceError attaches stable owning-Project provenance to an invalid
// root marker without changing its human message or typed error chain.
type ManifestSourceError struct {
	modulePath string
	sourcePath string
	cause      error
}

// ModulePath returns the Go Module identity that owns the invalid marker.
func (e *ManifestSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable slash-separated module-relative marker path.
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

// Line returns zero because an unsafe marker has no readable document span.
func (*ManifestSourceError) Line() int { return 0 }

// Column returns zero because an unsafe marker has no readable document span.
func (*ManifestSourceError) Column() int { return 0 }

func (e *ManifestSourceError) Error() string {
	if e == nil || e.cause == nil {
		return ErrInvalidManifest.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original marker error chain.
func (e *ManifestSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Find locates the nearest Go Module and requires a regular non-symbolic root
// plystra.yaml. It never crosses an ordinary nested Go Module to find an outer
// Project.
func Find(start string) (modulelocate.Module, error) {
	module, err := modulelocate.Find(start)
	if err != nil {
		return modulelocate.Module{}, fmt.Errorf("%w: %w", ErrLocate, err)
	}
	recognized, err := Recognize(module.Path(), module.ModulePath())
	if err != nil {
		return modulelocate.Module{}, fmt.Errorf("%w: %w", ErrLocate, err)
	}
	if !recognized {
		return modulelocate.Module{}, fmt.Errorf("%w: %w: nearest Go Module %q has no root %s", ErrLocate, ErrNotFound, module.ModulePath(), ManifestName)
	}
	return module, nil
}

// Recognize reports whether root contains the regular non-symbolic marker
// required of every Plystra Project. A missing marker identifies an ordinary
// Go Module and is not an error. Invalid markers retain the owning module and
// module-relative marker path without exposing the filesystem root.
func Recognize(root, modulePath string) (bool, error) {
	if root == "" {
		return false, manifestSourceError(modulePath, fmt.Errorf("%w: root path is empty", ErrInvalidManifest))
	}
	manifestPath := filepath.Join(root, ManifestName)
	info, err := os.Lstat(manifestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, manifestSourceError(modulePath, fmt.Errorf("inspect %s: %w", ManifestName, markerInspectionCause(err)))
	case !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0:
		return false, manifestSourceError(modulePath, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrInvalidManifest, ManifestName))
	default:
		return true, nil
	}
}

func manifestSourceError(modulePath string, cause error) error {
	if cause == nil {
		return nil
	}
	return &ManifestSourceError{
		modulePath: modulePath,
		sourcePath: ManifestName,
		cause:      cause,
	}
}

func markerInspectionCause(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && pathErr != nil && pathErr.Err != nil {
		return pathErr.Err
	}
	return err
}
