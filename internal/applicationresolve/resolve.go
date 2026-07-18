// Package applicationresolve constructs one complete resolved application from
// the nearest Plystra Project without mutating application input.
package applicationresolve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

var (
	// ErrResolve reports failure to construct one complete filesystem-backed
	// application resolution.
	ErrResolve = errors.New("resolve filesystem application")
	// ErrManifest reports a missing, unsafe, unreadable, or invalid root
	// plystra.yaml.
	ErrManifest = errors.New("load application manifest")
	// ErrUnsafeManifest reports a plystra.yaml that is symbolic or not a
	// regular bounded file.
	ErrUnsafeManifest = errors.New("unsafe application manifest")
	// ErrConcurrentChange reports plystra.yaml changing while it was read or
	// before the complete resolution finished.
	ErrConcurrentChange = errors.New("application manifest changed during resolution")
)

// Options contains the application location and bounded Go helper settings.
// Environment is shared by read-only module discovery and selected generation
// extension compilation so both observe the same Go workspace state.
type Options struct {
	Start                 string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	CompileTimeout        time.Duration
	ExecutionTimeout      time.Duration
	TemporaryParent       string
}

// Result is one immutable filesystem provenance and stable generation
// resolution assembled from the same application snapshot.
type Result struct {
	module          modulelocate.Module
	currentManifest applicationmeta.Manifest
	composition     applicationmeta.Composition
	dependencies    moduledependency.Index
	inventory       plugininventory.Index
	resolution      generationresolution.ExtensionResult
	configs         configurationresolve.Result
	maintenance     applicationmeta.ConfigurationMaintenance
	manifestSource  []byte
}

// Module returns the nearest Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Manifest returns the effective dependency-composed application declaration.
func (r Result) Manifest() applicationmeta.Manifest { return r.composition.Manifest() }

// CurrentManifest returns the normalized selected current-project declaration
// before dependency composition.
func (r Result) CurrentManifest() applicationmeta.Manifest { return r.currentManifest }

// Composition returns dependency baseline provenance and the effective
// application declaration.
func (r Result) Composition() applicationmeta.Composition { return r.composition }

// Dependencies returns the immutable effective Go Module graph used for
// dependency-Project discovery and generated runtime build provenance.
func (r Result) Dependencies() moduledependency.Index { return r.dependencies }

// Inventory returns every visible local and dependency-Project plugin.
func (r Result) Inventory() plugininventory.Index { return r.inventory }

// Resolution returns the stable provider, extension, contribution, and Alias
// closure.
func (r Result) Resolution() generationresolution.ExtensionResult { return r.resolution }

// Configurations returns the validated private selected-plugin configuration
// closure. Its values never enter generation-extension input.
func (r Result) Configurations() configurationresolve.Result { return r.configs }

// ConfigurationMaintenance returns the typed dependency-recomposition update
// planned against the exact root configuration snapshot used for resolution.
func (r Result) ConfigurationMaintenance() applicationmeta.ConfigurationMaintenance {
	return r.maintenance
}

// ManifestSource returns defensive original root configuration bytes used as
// the concurrency precondition for a planned maintenance write.
func (r Result) ManifestSource() []byte { return append([]byte(nil), r.manifestSource...) }

// Resolve locates the nearest Project, loads its root plystra.yaml, discovers
// the effective Go Module graph, indexes visible Project plugins and contracts,
// and runs the deterministic generation-resolution fixed point. It rechecks
// the application manifest before returning and writes no application files.
func Resolve(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrResolve)
	}
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrResolve, err)
	}
	manifestSnapshot, _, err := loadManifest(module.Path())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	dependencies, err := moduledependency.Discover(ctx, module, moduledependency.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	dependencySnapshots, dependencyManifests, err := loadDependencyManifests(dependencies.Projects())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	inventory, err := plugininventory.Build(module, dependencies)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	schemaLookup := func(pluginID string) (kernelmanifest.Config, bool) {
		plugin, exists := inventory.ByID(pluginID)
		if !exists {
			return kernelmanifest.Config{}, false
		}
		return plugin.Config(), true
	}
	previousBaseline, err := loadGeneratedDependencyBaseline(module.Path())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	maintenance, err := applicationmeta.MaintainDependencyConfiguration(manifestSnapshot.data, previousBaseline, dependencyManifests, schemaLookup)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	currentManifest, err := applicationmeta.Parse(maintenance.Data())
	if err != nil {
		return Result{}, fmt.Errorf("%w: maintained application manifest: %w", ErrResolve, err)
	}
	composition, err := applicationmeta.Compose(dependencyManifests, currentManifest, schemaLookup)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	manifest := composition.Manifest()
	input, err := applicationinput.Build(manifest, inventory, generationexec.BuildOptions{
		GoCommand:        options.GoCommand,
		BuildEnvironment: append([]string(nil), options.Environment...),
		CompileTimeout:   options.CompileTimeout,
		ExecutionTimeout: options.ExecutionTimeout,
		TemporaryParent:  options.TemporaryParent,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	resolution, err := generationresolution.ResolveExtensions(ctx, input)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	configs, err := configurationresolve.Resolve(manifest, inventory, resolution.Context())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	after, err := ReadManifestSnapshot(module.Path())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: recheck plystra.yaml: %v", ErrResolve, ErrConcurrentChange, err)
	}
	if !sameManifestSnapshot(manifestSnapshot, after) {
		return Result{}, fmt.Errorf("%w: %w: plystra.yaml changed before resolution completed", ErrResolve, ErrConcurrentChange)
	}
	if err := recheckDependencyManifests(dependencySnapshots); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return Result{
		module:          module,
		currentManifest: currentManifest,
		composition:     composition,
		dependencies:    dependencies,
		inventory:       inventory,
		resolution:      resolution,
		configs:         configs,
		maintenance:     maintenance,
		manifestSource:  manifestSnapshot.Data(),
	}, nil
}

func loadGeneratedDependencyBaseline(moduleRoot string) (applicationmeta.DependencyBaseline, error) {
	recovery, exists, err := generatedfiles.ReadApplicationManifestRecovery(moduleRoot)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("load generated dependency baseline recovery: %w", err)
	}
	if exists {
		baseline, err := applicationgen.DecodeDependencyBaseline(recovery)
		if err != nil {
			return applicationmeta.DependencyBaseline{}, fmt.Errorf("load generated dependency baseline recovery: %w", err)
		}
		return baseline, nil
	}
	data, exists, err := readGeneratedApplicationManifest(moduleRoot)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("load generated dependency baseline: %w", err)
	}
	if !exists {
		return applicationmeta.DependencyBaseline{}, nil
	}
	baseline, err := applicationgen.DecodeDependencyBaseline(data)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("load generated dependency baseline: %w", err)
	}
	return baseline, nil
}
