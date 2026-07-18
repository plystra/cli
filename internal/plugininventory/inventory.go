// Package plugininventory builds one deterministic visible plugin inventory
// from the current Project and every dependency Plystra Project.
package plugininventory

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"golang.org/x/mod/module"
)

var (
	// ErrBuild reports that the complete visible plugin inventory could not be
	// constructed safely.
	ErrBuild = errors.New("build application plugin inventory")
	// ErrDuplicateID reports one Plugin ID declared by more than one visible
	// module-relative plugin directory.
	ErrDuplicateID = pluginindex.ErrDuplicateID
)

// Plugin is one indexed declaration with deterministic public module
// provenance and CLI-private filesystem provenance.
type Plugin struct {
	indexed       pluginindex.Plugin
	modulePath    string
	moduleVersion string
	moduleRoot    string
	local         bool
}

// ID returns the exact canonical Plugin ID.
func (p Plugin) ID() string { return p.indexed.ID() }

// Name returns the direct-child plugin directory name.
func (p Plugin) Name() string { return p.indexed.Name() }

// Path returns the slash-separated module-relative plugin path.
func (p Plugin) Path() string { return p.indexed.Path() }

// ImportPath returns the canonical Go import path for the plugin package.
func (p Plugin) ImportPath() string { return path.Join(p.modulePath, p.indexed.Path()) }

// Source returns deterministic manifest provenance without a local filesystem
// path.
func (p Plugin) Source() string {
	version := p.moduleVersion
	if version == "" {
		version = "local"
	}
	return path.Join(p.modulePath+"@"+version, p.indexed.Path(), "plugin.yaml")
}

// ModulePath returns the canonical Go Module path used for imports.
func (p Plugin) ModulePath() string { return p.modulePath }

// ModuleVersion returns the selected version, or empty for the application or
// an active go.work module.
func (p Plugin) ModuleVersion() string { return p.moduleVersion }

// ModuleRoot returns the canonical absolute source root. It is CLI-only
// provenance and must not enter generation-extension input or generated data.
func (p Plugin) ModuleRoot() string { return p.moduleRoot }

// PluginRoot returns the canonical absolute plugin directory.
func (p Plugin) PluginRoot() string {
	return filepath.Join(p.moduleRoot, filepath.FromSlash(p.indexed.Path()))
}

// Local reports whether this is a root-level plugin in the current Project and
// therefore selected independently of provider use.
func (p Plugin) Local() bool { return p.local }

// Provides returns a defensive copy of declared canonical Capabilities.
func (p Plugin) Provides() []capabilityid.Identifier { return p.indexed.Provides() }

// Requires returns a defensive copy of declared canonical requirements.
func (p Plugin) Requires() []capabilityid.Identifier { return p.indexed.Requires() }

// Config returns the immutable validated runtime configuration declaration.
func (p Plugin) Config() kernelmanifest.Config { return p.indexed.Config() }

// Generation returns the optional trusted build-time generation declaration.
func (p Plugin) Generation() (pluginmeta.Generation, bool) { return p.indexed.Generation() }

// GenerationPackagePath returns the validated module-relative generation
// package path. This CLI-only path is not extension input.
func (p Plugin) GenerationPackagePath() (string, bool) {
	return p.indexed.GenerationPackagePath()
}

// ManifestData returns a defensive copy of the validated plugin.yaml bytes.
func (p Plugin) ManifestData() []byte { return p.indexed.ManifestData() }

// Index is one immutable Plugin-ID-sorted visible inventory.
type Index struct {
	plugins []Plugin
	byID    map[string]int
}

// Plugins returns a defensive copy sorted by Plugin ID.
func (i Index) Plugins() []Plugin { return append([]Plugin(nil), i.plugins...) }

// ByID returns one exact visible Plugin ID.
func (i Index) ByID(pluginID string) (Plugin, bool) {
	position, exists := i.byID[pluginID]
	if !exists {
		return Plugin{}, false
	}
	return i.plugins[position], true
}

// Build scans the current Project and every direct or transitive dependency
// Project. Ordinary Go dependencies are never scanned. Dependency plugins
// remain candidates; Local marks the plugins included by default.
func Build(application modulelocate.Module, dependencies moduledependency.Index) (Index, error) {
	if application.Path() == "" || application.ModulePath() == "" {
		return Index{}, fmt.Errorf("%w: application module is empty", ErrBuild)
	}
	sources := make([]moduleSource, 0, len(dependencies.Projects())+1)
	sources = append(sources, moduleSource{
		path:  application.ModulePath(),
		root:  application.Path(),
		local: true,
	})
	for _, dependency := range dependencies.Projects() {
		sources = append(sources, moduleSource{
			path:    dependency.Path(),
			version: dependency.SelectedVersion(),
			root:    dependency.Root(),
		})
	}

	plugins := make([]Plugin, 0)
	for _, source := range sources {
		indexed, err := pluginindex.Scan(source.root)
		if err != nil {
			return Index{}, fmt.Errorf("%w: scan %s: %w", ErrBuild, source.label(), err)
		}
		for _, declaration := range indexed.Plugins() {
			importPath := path.Join(source.path, declaration.Path())
			if err := module.CheckImportPath(importPath); err != nil {
				return Index{}, fmt.Errorf("%w: plugin %q at %s has invalid Go import path %q: %v", ErrBuild, declaration.ID(), source.label(), importPath, err)
			}
			plugins = append(plugins, Plugin{
				indexed:       declaration,
				modulePath:    source.path,
				moduleVersion: source.version,
				moduleRoot:    source.root,
				local:         source.local,
			})
		}
	}
	sort.Slice(plugins, func(left, right int) bool {
		if plugins[left].ID() != plugins[right].ID() {
			return plugins[left].ID() < plugins[right].ID()
		}
		return plugins[left].Source() < plugins[right].Source()
	})
	for first := 0; first < len(plugins); {
		last := first + 1
		for last < len(plugins) && plugins[last].ID() == plugins[first].ID() {
			last++
		}
		if last-first > 1 {
			sources := make([]string, last-first)
			for index, duplicate := range plugins[first:last] {
				sources[index] = duplicate.Source()
			}
			return Index{}, fmt.Errorf("%w: %w", ErrBuild, &DuplicateIDError{id: plugins[first].ID(), sources: sources})
		}
		first = last
	}
	byID := make(map[string]int, len(plugins))
	for position, plugin := range plugins {
		byID[plugin.ID()] = position
	}
	return Index{plugins: plugins, byID: byID}, nil
}

type moduleSource struct {
	path    string
	version string
	root    string
	local   bool
}

func (s moduleSource) label() string {
	version := s.version
	if version == "" {
		version = "local"
	}
	return s.path + "@" + version
}

// DuplicateIDError identifies every deterministic manifest source declaring
// one visible Plugin ID.
type DuplicateIDError struct {
	id      string
	sources []string
}

// ID returns the duplicated canonical Plugin ID.
func (e *DuplicateIDError) ID() string {
	if e == nil {
		return ""
	}
	return e.id
}

// Sources returns all conflicting manifest sources in deterministic order.
func (e *DuplicateIDError) Sources() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.sources...)
}

func (e *DuplicateIDError) Error() string {
	if e == nil {
		return ErrDuplicateID.Error()
	}
	return fmt.Sprintf("%s %q in [%s]", ErrDuplicateID, e.id, strings.Join(e.sources, ", "))
}

// Unwrap supports errors.Is with ErrDuplicateID.
func (*DuplicateIDError) Unwrap() error { return ErrDuplicateID }
