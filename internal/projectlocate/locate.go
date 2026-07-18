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

// Find locates the nearest Go Module and requires a regular non-symbolic root
// plystra.yaml. It never crosses an ordinary nested Go Module to find an outer
// Project.
func Find(start string) (modulelocate.Module, error) {
	module, err := modulelocate.Find(start)
	if err != nil {
		return modulelocate.Module{}, fmt.Errorf("%w: %w", ErrLocate, err)
	}
	manifestPath := filepath.Join(module.Path(), ManifestName)
	info, err := os.Lstat(manifestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return modulelocate.Module{}, fmt.Errorf("%w: %w: nearest Go Module %q has no root %s", ErrLocate, ErrNotFound, module.ModulePath(), ManifestName)
	case err != nil:
		return modulelocate.Module{}, fmt.Errorf("%w: inspect %s: %w", ErrLocate, manifestPath, err)
	case !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0:
		return modulelocate.Module{}, fmt.Errorf("%w: %w: %s must be a regular non-symbolic file", ErrLocate, ErrInvalidManifest, manifestPath)
	default:
		return module, nil
	}
}
