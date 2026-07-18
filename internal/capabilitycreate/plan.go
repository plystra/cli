// Package capabilitycreate plans and applies Capability authoring into a
// selected local plugin.
package capabilitycreate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/plugintarget"
)

// ErrPlan reports that a coherent Capability authoring plan could not be
// produced.
var ErrPlan = errors.New("plan capability authoring")

// Options contains the inputs needed to prepare one authoring plan.
type Options struct {
	Start                 string
	Reference             string
	Plugin                string
	Select                plugintarget.Selector
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
}

// Provider identifies one visible plugin declaring the source Capability.
type Provider struct {
	pluginID      string
	directory     string
	path          string
	modulePath    string
	moduleVersion string
	moduleRoot    string
	local         bool
	capability    capabilityid.Identifier
}

// PluginID returns the provider's canonical Plugin ID.
func (p Provider) PluginID() string { return p.pluginID }

// Directory returns the provider's direct-child directory name.
func (p Provider) Directory() string { return p.directory }

// Path returns the canonical absolute provider directory path.
func (p Provider) Path() string { return p.path }

// ModulePath returns the provider's Go Module identity.
func (p Provider) ModulePath() string { return p.modulePath }

// ModuleVersion returns the selected dependency version, or empty for a local
// or active workspace module.
func (p Provider) ModuleVersion() string { return p.moduleVersion }

// ModuleRoot returns the canonical read-only source root used during planning.
func (p Provider) ModuleRoot() string { return p.moduleRoot }

// Local reports whether the provider belongs to the module being mutated.
func (p Provider) Local() bool { return p.local }

// Capability returns the exact capability declared by the provider.
func (p Provider) Capability() capabilityid.Identifier { return p.capability }

// Plan is one deterministic target, version decision, and set of local source
// provider candidates derived from a single plugin snapshot.
type Plan struct {
	modulePath string
	target     plugintarget.Target
	version    capabilityversion.Plan
	providers  []Provider
}

// ModulePath returns the exact Go Module identity that owns the target plugin.
func (p Plan) ModulePath() string { return p.modulePath }

// Target returns the selected plugin that will implement the capability.
func (p Plan) Target() plugintarget.Target { return p.target }

// Version returns the exact capability version decision.
func (p Plan) Version() capabilityversion.Plan { return p.version }

// SourceProviders returns defensive copies in deterministic provider order.
// Every provider is retained so later schema loading can enforce equality.
func (p Plan) SourceProviders() []Provider {
	return append([]Provider(nil), p.providers...)
}

// Prepare creates a non-mutating plan from capabilities declared by local
// plugins in one immutable module index.
func Prepare(options Options) (Plan, error) {
	reference, err := capabilityid.ParseReference(options.Reference)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: parse reference: %w", ErrPlan, err)
	}
	module, err := modulelocate.Find(options.Start)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: locate module: %w", ErrPlan, err)
	}
	index, err := pluginindex.Scan(module.Path())
	if err != nil {
		return Plan{}, fmt.Errorf("%w: index plugins: %w", ErrPlan, err)
	}
	target, err := plugintarget.InferIndexed(plugintarget.Options{
		Start:    options.Start,
		Explicit: options.Plugin,
		Select:   options.Select,
	}, module, index)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrPlan, err)
	}

	plugins := index.Plugins()
	visible := make([]capabilityid.Identifier, 0)
	for _, plugin := range plugins {
		visible = append(visible, plugin.Provides()...)
	}
	version, err := capabilityversion.Infer(reference, visible)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrPlan, err)
	}

	providers := sourceProviders(module, plugins, version)
	return Plan{modulePath: module.ModulePath(), target: target, version: version, providers: providers}, nil
}

// PrepareVisible creates a non-mutating plan from the selected local plugin
// and every plugin in the module's explicit Go Module dependency inventory.
// Dependency source remains read-only and is retained only as schema
// provenance for version inference and exact contract copying.
func PrepareVisible(ctx context.Context, options Options) (Plan, error) {
	if ctx == nil {
		return Plan{}, fmt.Errorf("%w: context is nil", ErrPlan)
	}
	reference, err := capabilityid.ParseReference(options.Reference)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: parse reference: %w", ErrPlan, err)
	}
	module, err := modulelocate.Find(options.Start)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: locate module: %w", ErrPlan, err)
	}
	local, err := pluginindex.Scan(module.Path())
	if err != nil {
		return Plan{}, fmt.Errorf("%w: index local plugins: %w", ErrPlan, err)
	}
	target, err := plugintarget.InferIndexed(plugintarget.Options{
		Start:    options.Start,
		Explicit: options.Plugin,
		Select:   options.Select,
	}, module, local)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrPlan, err)
	}
	dependencies, err := moduledependency.Discover(ctx, module, moduledependency.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("%w: discover visible modules: %w", ErrPlan, err)
	}
	inventory, err := plugininventory.Build(module, dependencies)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: index visible plugins: %w", ErrPlan, err)
	}
	plugins := inventory.Plugins()
	visible := make([]capabilityid.Identifier, 0)
	for _, plugin := range plugins {
		visible = append(visible, plugin.Provides()...)
	}
	version, err := capabilityversion.Infer(reference, visible)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %w", ErrPlan, err)
	}
	return Plan{
		modulePath: module.ModulePath(),
		target:     target,
		version:    version,
		providers:  visibleSourceProviders(plugins, version),
	}, nil
}

func sourceProviders(module modulelocate.Module, plugins []pluginindex.Plugin, version capabilityversion.Plan) []Provider {
	source, ok := version.Source()
	if !ok {
		return nil
	}
	providers := make([]Provider, 0)
	for _, plugin := range plugins {
		for _, provided := range plugin.Provides() {
			if provided != source {
				continue
			}
			providers = append(providers, Provider{
				pluginID:   plugin.ID(),
				directory:  plugin.Name(),
				path:       filepath.Join(module.Path(), filepath.FromSlash(plugin.Path())),
				modulePath: module.ModulePath(),
				moduleRoot: module.Path(),
				local:      true,
				capability: provided,
			})
		}
	}
	return providers
}

func visibleSourceProviders(plugins []plugininventory.Plugin, version capabilityversion.Plan) []Provider {
	source, ok := version.Source()
	if !ok {
		return nil
	}
	providers := make([]Provider, 0)
	for _, plugin := range plugins {
		for _, provided := range plugin.Provides() {
			if provided != source {
				continue
			}
			providers = append(providers, Provider{
				pluginID:      plugin.ID(),
				directory:     plugin.Name(),
				path:          plugin.PluginRoot(),
				modulePath:    plugin.ModulePath(),
				moduleVersion: plugin.ModuleVersion(),
				moduleRoot:    plugin.ModuleRoot(),
				local:         plugin.Local(),
				capability:    provided,
			})
		}
	}
	return providers
}
