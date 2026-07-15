// Package pluginindex builds an immutable local plugin identity index.
package pluginindex

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/pluginscan"
)

var (
	// ErrIndex reports that local plugins could not be indexed safely.
	ErrIndex = errors.New("index local plugins")
	// ErrDuplicateID reports two local directories declaring one Plugin ID.
	ErrDuplicateID = errors.New("duplicate local plugin ID")
	// ErrConcurrentChange reports plugin input that changed during indexing.
	ErrConcurrentChange = errors.New("plugin input changed during indexing")
	// ErrInvalidGenerationPackage reports a missing, non-directory, or symbolic
	// package path declared by a plugin generation extension.
	ErrInvalidGenerationPackage = errors.New("invalid generation package")
)

// Plugin identifies one indexed root-level plugin.
type Plugin struct {
	name                  string
	path                  string
	id                    string
	provides              []capabilityid.Identifier
	generation            pluginmeta.Generation
	generationPackagePath string
	manifest              []byte
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

// Generation returns the optional trusted build-time generation declaration.
func (p Plugin) Generation() (pluginmeta.Generation, bool) {
	return p.generation, p.generation.API() != ""
}

// GenerationPackagePath returns the canonical module-relative path of the
// validated generation package. This CLI-only provenance is not extension
// input.
func (p Plugin) GenerationPackagePath() (string, bool) {
	return p.generationPackagePath, p.generationPackagePath != ""
}

// ManifestData returns a defensive copy of the validated plugin.yaml snapshot.
func (p Plugin) ManifestData() []byte { return append([]byte(nil), p.manifest...) }

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
	generationPackages := make([]generationPackageSnapshot, 0, len(discovered))
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
		generation, hasGeneration := metadata.Generation()
		var generationPackagePath string
		if hasGeneration {
			snapshot, err := inspectGenerationPackage(root, id, directory.Path(), generation)
			if err != nil {
				return Index{}, fmt.Errorf("%w: %s: %w", ErrIndex, markerPath, err)
			}
			generationPackagePath = snapshot.modulePath
			generationPackages = append(generationPackages, snapshot)
		}
		ids[id] = directory.Path()
		plugins = append(plugins, Plugin{
			name:                  directory.Name(),
			path:                  directory.Path(),
			id:                    id,
			provides:              metadata.Provides(),
			generation:            generation,
			generationPackagePath: generationPackagePath,
			manifest:              append([]byte(nil), data...),
		})
	}
	after, err := pluginscan.ScanRoot(rootPath)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w: rescan root: %v", ErrIndex, ErrConcurrentChange, err)
	}
	if !sameDirectories(discovered, after.Directories()) {
		return Index{}, fmt.Errorf("%w: %w: local plugin inventory changed", ErrIndex, ErrConcurrentChange)
	}
	for _, before := range generationPackages {
		after, err := inspectGenerationPackage(root, before.pluginID, before.pluginPath, before.declaration)
		if err != nil {
			return Index{}, fmt.Errorf("%w: %w: generation package changed while indexing: %w", ErrIndex, ErrConcurrentChange, err)
		}
		if !sameGenerationPackage(before, after) {
			return Index{}, fmt.Errorf("%w: %w: generation package %s for plugin %q changed while indexing", ErrIndex, ErrConcurrentChange, before.modulePath, before.pluginID)
		}
	}
	return Index{plugins: plugins}, nil
}

type generationPackageSnapshot struct {
	pluginID    string
	pluginPath  string
	declaration pluginmeta.Generation
	modulePath  string
	components  []generationPackageComponent
}

type generationPackageComponent struct {
	path string
	info fs.FileInfo
}

type lstatFS interface {
	Lstat(name string) (fs.FileInfo, error)
}

func inspectGenerationPackage(source lstatFS, pluginID, pluginPath string, generation pluginmeta.Generation) (generationPackageSnapshot, error) {
	modulePath := path.Join(pluginPath, strings.TrimPrefix(generation.Package(), "./"))
	parts := strings.Split(modulePath, "/")
	components := make([]generationPackageComponent, 0, len(parts))
	for index := range parts {
		componentPath := path.Join(parts[:index+1]...)
		info, err := source.Lstat(componentPath)
		if errors.Is(err, fs.ErrNotExist) {
			return generationPackageSnapshot{}, fmt.Errorf("%w: plugin %q generation.package %q resolves to %s, but path component %s does not exist", ErrInvalidGenerationPackage, pluginID, generation.Package(), modulePath, componentPath)
		}
		if err != nil {
			return generationPackageSnapshot{}, fmt.Errorf("%w: plugin %q generation.package %q cannot inspect path component %s: %w", ErrInvalidGenerationPackage, pluginID, generation.Package(), componentPath, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return generationPackageSnapshot{}, fmt.Errorf("%w: plugin %q generation.package %q resolves through symbolic path component %s", ErrInvalidGenerationPackage, pluginID, generation.Package(), componentPath)
		}
		if !info.IsDir() {
			return generationPackageSnapshot{}, fmt.Errorf("%w: plugin %q generation.package %q resolves through non-directory path component %s", ErrInvalidGenerationPackage, pluginID, generation.Package(), componentPath)
		}
		components = append(components, generationPackageComponent{path: componentPath, info: info})
	}
	return generationPackageSnapshot{
		pluginID:    pluginID,
		pluginPath:  pluginPath,
		declaration: generation,
		modulePath:  modulePath,
		components:  components,
	}, nil
}

func sameGenerationPackage(left, right generationPackageSnapshot) bool {
	if left.pluginID != right.pluginID || left.pluginPath != right.pluginPath || left.modulePath != right.modulePath || len(left.components) != len(right.components) {
		return false
	}
	for index := range left.components {
		if left.components[index].path != right.components[index].path || !sameFileState(left.components[index].info, right.components[index].info) {
			return false
		}
	}
	return true
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
