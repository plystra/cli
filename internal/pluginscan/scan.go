// Package pluginscan discovers direct-child plugin directories in a Go Module.
package pluginscan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

var (
	// ErrScan reports that a module root could not be inspected safely.
	ErrScan = errors.New("scan module plugins")
	// ErrInvalidMarker reports a plugin.yaml that is not a regular file.
	ErrInvalidMarker = errors.New("invalid plugin marker")
)

var reservedDirectories = map[string]struct{}{
	".git":         {},
	".github":      {},
	"docs":         {},
	"generated":    {},
	"dist":         {},
	"examples":     {},
	"testdata":     {},
	"vendor":       {},
	"node_modules": {},
}

// Directory identifies one direct-child plugin directory.
type Directory struct {
	name string
	path string
}

// Name returns the plugin directory's base name.
func (d Directory) Name() string {
	return d.name
}

// Path returns the slash-separated module-relative plugin directory path.
func (d Directory) Path() string {
	return d.path
}

// Result is an immutable deterministic plugin-directory collection.
type Result struct {
	directories []Directory
}

// Directories returns a defensive copy sorted by module-relative path.
func (r Result) Directories() []Directory {
	return append([]Directory(nil), r.directories...)
}

// ScanRoot opens and scans one filesystem module root.
func ScanRoot(rootPath string) (result Result, scanErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: open root: %w", ErrScan, err)
	}
	defer func() {
		if err := root.Close(); err != nil && scanErr == nil {
			scanErr = fmt.Errorf("%w: close root: %w", ErrScan, err)
		}
	}()
	source, ok := root.FS().(fs.ReadLinkFS)
	if !ok {
		return Result{}, fmt.Errorf("%w: rooted filesystem does not support link inspection", ErrScan)
	}
	return Scan(source)
}

// Scan discovers only direct child directories containing a regular
// plugin.yaml marker. It never recursively searches the module.
func Scan(source fs.ReadLinkFS) (Result, error) {
	if source == nil {
		return Result{}, fmt.Errorf("%w: nil filesystem", ErrScan)
	}
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return Result{}, fmt.Errorf("%w: read root: %w", ErrScan, err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})

	directories := make([]Directory, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || IsReserved(name) {
			continue
		}
		info, err := source.Lstat(name)
		if err != nil {
			return Result{}, fmt.Errorf("%w: inspect directory %s: %w", ErrScan, name, err)
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}

		markerPath := path.Join(name, "plugin.yaml")
		marker, err := source.Lstat(markerPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Result{}, fmt.Errorf("%w: inspect %s: %w", ErrScan, markerPath, err)
		}
		if !marker.Mode().IsRegular() || marker.Mode()&fs.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrInvalidMarker, markerPath)
		}
		directories = append(directories, Directory{name: name, path: name})
	}
	return Result{directories: directories}, nil
}

// IsReserved reports whether a root child is excluded from plugin discovery.
func IsReserved(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	_, reserved := reservedDirectories[name]
	return reserved
}
