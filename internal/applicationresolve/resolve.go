// Package applicationresolve constructs one complete resolved application from
// the nearest runnable Plystra Go Module without mutating application input.
package applicationresolve

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
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
	module       modulelocate.Module
	manifest     applicationmeta.Manifest
	dependencies moduledependency.Index
	inventory    plugininventory.Index
	resolution   generationresolution.ExtensionResult
	configs      configurationresolve.Result
}

// Module returns the nearest runnable application Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Manifest returns the normalized root application declaration.
func (r Result) Manifest() applicationmeta.Manifest { return r.manifest }

// Dependencies returns the immutable explicit Go Module dependency index used
// for plugin discovery and generated runtime build provenance.
func (r Result) Dependencies() moduledependency.Index { return r.dependencies }

// Inventory returns every visible local and explicit-dependency plugin.
func (r Result) Inventory() plugininventory.Index { return r.inventory }

// Resolution returns the stable provider, extension, contribution, and Alias
// closure.
func (r Result) Resolution() generationresolution.ExtensionResult { return r.resolution }

// Configurations returns the validated private selected-plugin configuration
// closure. Its values never enter generation-extension input.
func (r Result) Configurations() configurationresolve.Result { return r.configs }

// Resolve locates the nearest module, loads its root plystra.yaml, discovers
// only explicit Go Module dependencies, indexes visible plugins and contracts,
// and runs the deterministic generation-resolution fixed point. It rechecks the
// application manifest before returning and writes no application files.
func Resolve(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrResolve)
	}
	module, err := modulelocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate application module: %w", ErrResolve, err)
	}
	manifestSnapshot, manifest, err := loadManifest(module.Path())
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
	inventory, err := plugininventory.Build(module, dependencies)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
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
	return Result{
		module:       module,
		manifest:     manifest,
		dependencies: dependencies,
		inventory:    inventory,
		resolution:   resolution,
		configs:      configs,
	}, nil
}
