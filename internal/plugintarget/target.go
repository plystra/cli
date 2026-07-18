// Package plugintarget applies the documented local plugin inference order.
package plugintarget

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/projectlocate"
)

var (
	// ErrInfer reports that a plugin target could not be inferred safely.
	ErrInfer = errors.New("infer plugin target")
	// ErrNotFound reports that no matching local plugin exists.
	ErrNotFound = errors.New("plugin target not found")
	// ErrAmbiguous reports multiple local plugins without an available selector.
	ErrAmbiguous = errors.New("plugin target is ambiguous")
	// ErrSelection reports an interactive selector failure or invalid result.
	ErrSelection = errors.New("invalid plugin selection")
)

// Selector chooses one zero-based candidate index. Callers provide a selector
// only when an interactive terminal is available.
type Selector func(candidates []Target) (int, error)

// Options controls one inference operation.
type Options struct {
	Start    string
	Explicit string
	Select   Selector
}

// Target identifies one selected local plugin.
type Target struct {
	id         string
	directory  string
	path       string
	moduleRoot string
	manifest   []byte
}

// ID returns the canonical Plugin ID.
func (t Target) ID() string { return t.id }

// Directory returns the direct-child directory name.
func (t Target) Directory() string { return t.directory }

// Path returns the canonical absolute plugin directory path.
func (t Target) Path() string { return t.path }

// ModuleRoot returns the canonical absolute Go Module root.
func (t Target) ModuleRoot() string { return t.moduleRoot }

// ManifestData returns a defensive copy of the validated plugin.yaml snapshot
// used to select this target.
func (t Target) ManifestData() []byte { return append([]byte(nil), t.manifest...) }

// Infer selects an explicit reference, enclosing plugin, sole local plugin, or
// selector result in that exact order.
func Infer(options Options) (Target, error) {
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return Target{}, fmt.Errorf("%w: locate Project: %w", ErrInfer, err)
	}
	index, err := pluginindex.Scan(module.Path())
	if err != nil {
		return Target{}, fmt.Errorf("%w: %w", ErrInfer, err)
	}
	return InferIndexed(options, module, index)
}

// InferIndexed selects a target from one already validated module and immutable
// plugin snapshot. It lets compound authoring plans avoid inconsistent rescans.
func InferIndexed(options Options, module modulelocate.Module, index pluginindex.Index) (Target, error) {
	if module.Path() == "" {
		return Target{}, fmt.Errorf("%w: module root is empty", ErrInfer)
	}
	if options.Start == "" {
		return Target{}, fmt.Errorf("%w: start path is empty", ErrInfer)
	}
	if options.Explicit != "" {
		plugin, ok := index.ByReference(options.Explicit)
		if !ok {
			return Target{}, fmt.Errorf("%w: %w: %q did not match a local directory or Plugin ID", ErrInfer, ErrNotFound, options.Explicit)
		}
		return makeTarget(module.Path(), plugin), nil
	}

	canonicalStart, err := canonicalDirectory(options.Start)
	if err != nil {
		return Target{}, fmt.Errorf("%w: resolve start: %w", ErrInfer, err)
	}
	if plugin, ok := enclosingPlugin(module.Path(), canonicalStart, index); ok {
		return makeTarget(module.Path(), plugin), nil
	}

	plugins := index.Plugins()
	switch len(plugins) {
	case 0:
		return Target{}, fmt.Errorf("%w: %w: module has no local plugins", ErrInfer, ErrNotFound)
	case 1:
		return makeTarget(module.Path(), plugins[0]), nil
	}
	candidates := make([]Target, len(plugins))
	for index := range plugins {
		candidates[index] = makeTarget(module.Path(), plugins[index])
	}
	if options.Select == nil {
		return Target{}, ambiguous(candidates)
	}
	selected, err := options.Select(append([]Target(nil), candidates...))
	if err != nil {
		return Target{}, fmt.Errorf("%w: %w: %w", ErrInfer, ErrSelection, err)
	}
	if selected < 0 || selected >= len(candidates) {
		return Target{}, fmt.Errorf("%w: %w: candidate index %d is outside 0..%d", ErrInfer, ErrSelection, selected, len(candidates)-1)
	}
	return candidates[selected], nil
}

func canonicalDirectory(start string) (string, error) {
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

func enclosingPlugin(moduleRoot, start string, index pluginindex.Index) (pluginindex.Plugin, bool) {
	relative, err := filepath.Rel(moduleRoot, start)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return pluginindex.Plugin{}, false
	}
	component := relative
	if separator := strings.IndexRune(component, filepath.Separator); separator >= 0 {
		component = component[:separator]
	}
	return index.ByName(component)
}

func makeTarget(moduleRoot string, plugin pluginindex.Plugin) Target {
	return Target{
		id:         plugin.ID(),
		directory:  plugin.Name(),
		path:       filepath.Join(moduleRoot, filepath.FromSlash(plugin.Path())),
		moduleRoot: moduleRoot,
		manifest:   plugin.ManifestData(),
	}
}

func ambiguous(candidates []Target) error {
	values := make([]string, len(candidates))
	for index, candidate := range candidates {
		values[index] = fmt.Sprintf("%s (%s)", candidate.id, candidate.directory)
	}
	return fmt.Errorf("%w: %w: multiple local plugins: %s; use --plugin or an interactive terminal", ErrInfer, ErrAmbiguous, strings.Join(values, ", "))
}
