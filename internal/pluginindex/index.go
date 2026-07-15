// Package pluginindex builds an immutable local plugin identity index.
package pluginindex

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/pluginscan"
)

var (
	// ErrIndex reports that local plugins could not be indexed safely.
	ErrIndex = errors.New("index local plugins")
	// ErrDuplicateID reports two local directories declaring one Plugin ID.
	ErrDuplicateID = errors.New("duplicate local plugin ID")
	// ErrConcurrentChange reports a marker that changed while it was read.
	ErrConcurrentChange = errors.New("plugin manifest changed during indexing")
)

// Plugin identifies one indexed root-level plugin.
type Plugin struct {
	name     string
	path     string
	id       string
	provides []capabilityid.Identifier
}

// Name returns the direct-child directory name.
func (p Plugin) Name() string { return p.name }

// Path returns the slash-separated module-relative directory path.
func (p Plugin) Path() string { return p.path }

// ID returns the canonical Plugin ID declared by plugin.yaml.
func (p Plugin) ID() string { return p.id }

// Provides returns a defensive copy of the exact capabilities declared by the
// plugin, sorted by canonical identity.
func (p Plugin) Provides() []capabilityid.Identifier {
	return append([]capabilityid.Identifier(nil), p.provides...)
}

// Index is an immutable deterministic collection of local plugins.
type Index struct {
	plugins []Plugin
}

// Plugins returns a defensive copy sorted by module-relative path.
func (i Index) Plugins() []Plugin {
	return append([]Plugin(nil), i.plugins...)
}

// ByName returns the plugin in one exact direct-child directory.
func (i Index) ByName(name string) (Plugin, bool) {
	for _, plugin := range i.plugins {
		if plugin.name == name {
			return plugin, true
		}
	}
	return Plugin{}, false
}

// ByReference resolves either an exact directory name or canonical Plugin ID.
func (i Index) ByReference(reference string) (Plugin, bool) {
	for _, plugin := range i.plugins {
		if plugin.name == reference || plugin.id == reference {
			return plugin, true
		}
	}
	return Plugin{}, false
}

// Scan discovers and indexes every safe direct-child plugin in rootPath.
func Scan(rootPath string) (result Index, indexErr error) {
	directories, err := pluginscan.ScanRoot(rootPath)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrIndex, err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Index{}, fmt.Errorf("%w: open root: %w", ErrIndex, err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			indexErr = errors.Join(indexErr, fmt.Errorf("%w: close root: %w", ErrIndex, err))
		}
	}()

	discovered := directories.Directories()
	plugins := make([]Plugin, 0, len(discovered))
	ids := make(map[string]string, len(discovered))
	for _, directory := range discovered {
		markerPath := path.Join(directory.Path(), "plugin.yaml")
		data, err := readStableMarker(root, markerPath)
		if err != nil {
			return Index{}, err
		}
		metadata, err := pluginmeta.Parse(data)
		if err != nil {
			return Index{}, fmt.Errorf("%w: %s: %w", ErrIndex, markerPath, err)
		}
		id := metadata.ID()
		if previous, duplicate := ids[id]; duplicate {
			return Index{}, fmt.Errorf("%w: %w %q in %s and %s", ErrIndex, ErrDuplicateID, id, previous, directory.Path())
		}
		ids[id] = directory.Path()
		plugins = append(plugins, Plugin{
			name:     directory.Name(),
			path:     directory.Path(),
			id:       id,
			provides: metadata.Provides(),
		})
	}
	after, err := pluginscan.ScanRoot(rootPath)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w: rescan root: %v", ErrIndex, ErrConcurrentChange, err)
	}
	if !sameDirectories(discovered, after.Directories()) {
		return Index{}, fmt.Errorf("%w: %w: local plugin inventory changed", ErrIndex, ErrConcurrentChange)
	}
	return Index{plugins: plugins}, nil
}

func readStableMarker(root *os.Root, markerPath string) ([]byte, error) {
	before, err := root.Lstat(markerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect %s: %w", ErrIndex, markerPath, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is not a regular non-symbolic file", ErrIndex, markerPath)
	}
	if before.Size() > pluginmeta.MaximumSize {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrIndex, markerPath, pluginmeta.MaximumSize)
	}
	file, err := root.Open(markerPath)
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %w", ErrIndex, markerPath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: inspect opened %s: %w", ErrIndex, markerPath, err)
	}
	if !opened.Mode().IsRegular() || !sameFileState(before, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %w: %s was replaced before open", ErrIndex, ErrConcurrentChange, markerPath)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, pluginmeta.MaximumSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrIndex, markerPath, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close %s: %w", ErrIndex, markerPath, closeErr)
	}
	after, err := root.Lstat(markerPath)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&fs.ModeSymlink != 0 || !sameFileState(opened, after) {
		return nil, fmt.Errorf("%w: %w: %s changed after open", ErrIndex, ErrConcurrentChange, markerPath)
	}
	return data, nil
}

func sameFileState(left, right fs.FileInfo) bool {
	return os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func sameDirectories(left, right []pluginscan.Directory) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name() != right[index].Name() || left[index].Path() != right[index].Path() {
			return false
		}
	}
	return true
}
