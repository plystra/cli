// Package capabilitycreate plans local custom-capability authoring.
package capabilitycreate

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/plugintarget"
)

// ErrPlan reports that a coherent local capability-creation plan could not be
// produced.
var ErrPlan = errors.New("plan local capability creation")

// Options contains the inputs needed to prepare one local authoring plan.
type Options struct {
	Start     string
	Reference string
	Plugin    string
	Select    plugintarget.Selector
}

// Provider identifies one local plugin declaring the source capability.
type Provider struct {
	pluginID   string
	directory  string
	path       string
	capability capabilityid.Identifier
}

// PluginID returns the provider's canonical Plugin ID.
func (p Provider) PluginID() string { return p.pluginID }

// Directory returns the provider's direct-child directory name.
func (p Provider) Directory() string { return p.directory }

// Path returns the canonical absolute provider directory path.
func (p Provider) Path() string { return p.path }

// Capability returns the exact capability declared by the provider.
func (p Provider) Capability() capabilityid.Identifier { return p.capability }

// Plan is one deterministic target, version decision, and set of local source
// provider candidates derived from a single plugin snapshot.
type Plan struct {
	target    plugintarget.Target
	version   capabilityversion.Plan
	providers []Provider
}

// Target returns the selected plugin that will implement the capability.
func (p Plan) Target() plugintarget.Target { return p.target }

// Version returns the exact capability version decision.
func (p Plan) Version() capabilityversion.Plan { return p.version }

// SourceProviders returns defensive copies in plugin-directory order. Every
// provider is retained so later schema loading can enforce equality.
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

	providers := sourceProviders(module.Path(), plugins, version)
	return Plan{target: target, version: version, providers: providers}, nil
}

func sourceProviders(moduleRoot string, plugins []pluginindex.Plugin, version capabilityversion.Plan) []Provider {
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
				path:       filepath.Join(moduleRoot, filepath.FromSlash(plugin.Path())),
				capability: provided,
			})
		}
	}
	return providers
}
