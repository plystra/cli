// Package modulelocate finds and validates the nearest enclosing Go Module.
package modulelocate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/modfile"
)

const maximumGoModSize = 1 << 20

var (
	// ErrLocate reports that module-root inspection failed.
	ErrLocate = errors.New("locate Go Module")
	// ErrNotFound reports that no enclosing go.mod exists.
	ErrNotFound = errors.New("go module root not found")
	// ErrInvalidGoMod reports an unsafe or invalid go.mod.
	ErrInvalidGoMod = errors.New("invalid go.mod")
)

// Module is one validated nearest enclosing Go Module.
type Module struct {
	path       string
	modulePath string
}

// Path returns the canonical absolute module root.
func (m Module) Path() string { return m.path }

// ModulePath returns the validated module directive.
func (m Module) ModulePath() string { return m.modulePath }

// Find walks from start to the nearest enclosing regular go.mod.
func Find(start string) (Module, error) {
	if start == "" {
		return Module{}, fmt.Errorf("%w: start path is empty", ErrLocate)
	}
	absolute, err := filepath.Abs(start)
	if err != nil {
		return Module{}, fmt.Errorf("%w: resolve start: %w", ErrLocate, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return Module{}, fmt.Errorf("%w: resolve start links: %w", ErrLocate, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return Module{}, fmt.Errorf("%w: inspect start: %w", ErrLocate, err)
	}
	if !info.IsDir() {
		return Module{}, fmt.Errorf("%w: start is not a directory", ErrLocate)
	}

	for directory := canonical; ; directory = filepath.Dir(directory) {
		goModPath := filepath.Join(directory, "go.mod")
		goModInfo, err := os.Lstat(goModPath)
		switch {
		case err == nil:
			if !goModInfo.Mode().IsRegular() || goModInfo.Mode()&fs.ModeSymlink != 0 {
				return Module{}, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrInvalidGoMod, goModPath)
			}
			if goModInfo.Size() > maximumGoModSize {
				return Module{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidGoMod, goModPath, maximumGoModSize)
			}
			data, err := os.ReadFile(goModPath)
			if err != nil {
				return Module{}, fmt.Errorf("%w: read %s: %w", ErrLocate, goModPath, err)
			}
			parsed, err := modfile.Parse(goModPath, data, nil)
			if err != nil {
				return Module{}, fmt.Errorf("%w: %v", ErrInvalidGoMod, err)
			}
			if parsed.Module == nil {
				return Module{}, fmt.Errorf("%w: module directive is required", ErrInvalidGoMod)
			}
			modulePath := parsed.Module.Mod.Path
			if err := modulepath.CheckProject(modulePath); err != nil {
				return Module{}, fmt.Errorf("%w: module path %q: %v", ErrInvalidGoMod, modulePath, err)
			}
			return Module{path: directory, modulePath: modulePath}, nil
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return Module{}, fmt.Errorf("%w: inspect %s: %w", ErrLocate, goModPath, err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return Module{}, ErrNotFound
		}
	}
}
