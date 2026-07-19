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
	ConfigurationPath     string
	EnvironmentName       string
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
	module              modulelocate.Module
	currentManifest     applicationmeta.Manifest
	composition         applicationmeta.Composition
	dependencies        moduledependency.Index
	inventory           plugininventory.Index
	resolution          generationresolution.ExtensionResult
	configs             configurationresolve.Result
	maintenance         applicationmeta.ConfigurationMaintenance
	selection           ConfigurationSelection
	rootData            []byte
	configurationSource []byte
	maintenancePath     string
	maintenanceSource   []byte
	previousProvenance  applicationgen.ManifestProvenance
}

// Module returns the nearest Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Manifest returns the effective dependency-composed application declaration.
func (r Result) Manifest() applicationmeta.Manifest { return r.composition.Manifest() }

// CurrentManifest returns the normalized selected current-project layer before
// dependency composition. Environment mode includes root plus its overlay.
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
// planned against the exact owned configuration snapshot used for resolution.
func (r Result) ConfigurationMaintenance() applicationmeta.ConfigurationMaintenance {
	return r.maintenance
}

// ConfigurationSelection returns the immutable current-project document
// selection and normalized semantic digest used by this resolution.
func (r Result) ConfigurationSelection() ConfigurationSelection { return r.selection }

// RootConfigurationData returns the final root marker document represented by
// generated provenance. It includes planned root maintenance in default and
// environment modes.
func (r Result) RootConfigurationData() []byte { return append([]byte(nil), r.rootData...) }

// ConfigurationSource returns defensive original selected-document bytes.
func (r Result) ConfigurationSource() []byte {
	return append([]byte(nil), r.configurationSource...)
}

// ConfigurationMaintenancePath returns the Project-relative document owned by
// dependency-baseline maintenance for this selection.
func (r Result) ConfigurationMaintenancePath() string { return r.maintenancePath }

// ConfigurationMaintenanceSource returns defensive original maintenance-target
// bytes used as the concurrency precondition for a planned write.
func (r Result) ConfigurationMaintenanceSource() []byte {
	return append([]byte(nil), r.maintenanceSource...)
}

// PreviousManifestProvenance returns validated generated-manifest state used
// to preserve dependency ownership independently for every configuration
// selection.
func (r Result) PreviousManifestProvenance() applicationgen.ManifestProvenance {
	return r.previousProvenance
}

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
	rootSnapshot, rootManifest, err := loadConfiguration(module.Path(), applicationManifestName)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	selector, err := resolveConfigurationSelector(module.Path(), options.ConfigurationPath, options.EnvironmentName, options.Environment)
	if err != nil {
		return Result{}, fmt.Errorf("%w: select current-project configuration: %w", ErrResolve, err)
	}
	configurationSnapshot := rootSnapshot
	selectedManifest := rootManifest
	if selector.path != applicationManifestName {
		if selector.mode == configurationModeEnvironment {
			configurationSnapshot, selectedManifest, err = loadEnvironmentOverlay(module.Path(), selector.path)
		} else {
			configurationSnapshot, selectedManifest, err = loadConfiguration(module.Path(), selector.path)
		}
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
		}
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
	previousBaseline, previousProvenance, err := loadGeneratedDependencyBaseline(module.Path(), selector)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	maintenanceSnapshot := configurationSnapshot
	if selector.mode == configurationModeEnvironment {
		maintenanceSnapshot = rootSnapshot
	}
	var maintenance applicationmeta.ConfigurationMaintenance
	if selector.mode == configurationModeEnvironment {
		maintenance, err = applicationmeta.MaintainDependencyConfigurationWithOverlay(maintenanceSnapshot.data, selectedManifest, previousBaseline, dependencyManifests, schemaLookup)
	} else {
		maintenance, err = applicationmeta.MaintainDependencyConfiguration(maintenanceSnapshot.data, previousBaseline, dependencyManifests, schemaLookup)
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	maintainedManifest, err := applicationmeta.ParseSource(maintenanceSnapshot.path, maintenance.Data())
	if err != nil {
		return Result{}, fmt.Errorf("%w: maintained application manifest: %w", ErrResolve, err)
	}
	currentManifest := maintainedManifest
	if selector.mode == configurationModeEnvironment {
		currentManifest, err = applicationmeta.ApplyOverlay(maintainedManifest, selectedManifest, schemaLookup)
		if err != nil {
			return Result{}, fmt.Errorf("%w: environment %q: %w", ErrResolve, selector.environment, err)
		}
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
	selectedData := maintenance.Data()
	if selector.mode == configurationModeEnvironment {
		selectedData = configurationSnapshot.Data()
	}
	selectedDigestFunction := applicationgen.ConfigurationDigest
	if selector.mode == configurationModeEnvironment {
		selectedDigestFunction = applicationgen.EnvironmentOverlayDigest
	}
	selectedDigest, err := selectedDigestFunction(selectedData)
	if err != nil {
		return Result{}, fmt.Errorf("%w: digest selected configuration %s: %w", ErrResolve, selector.path, err)
	}
	rootData := rootSnapshot.Data()
	if maintenanceSnapshot.path == applicationManifestName {
		rootData = maintenance.Data()
	}
	after, err := ReadManifestSnapshot(module.Path())
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: recheck plystra.yaml: %v", ErrResolve, ErrConcurrentChange, err)
	}
	if !sameManifestSnapshot(rootSnapshot, after) {
		return Result{}, fmt.Errorf("%w: %w: plystra.yaml changed before resolution completed", ErrResolve, ErrConcurrentChange)
	}
	if selector.path != applicationManifestName {
		after, err := readManifestSnapshot(module.Path(), selector.path)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w: recheck selected configuration %s: %v", ErrResolve, ErrConcurrentChange, selector.path, err)
		}
		if !sameManifestSnapshot(configurationSnapshot, after) {
			return Result{}, fmt.Errorf("%w: %w: selected configuration %s changed before resolution completed", ErrResolve, ErrConcurrentChange, selector.path)
		}
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
		selection: ConfigurationSelection{
			mode:        selector.mode,
			path:        selector.path,
			environment: selector.environment,
			digest:      selectedDigest,
		},
		rootData:            append([]byte(nil), rootData...),
		configurationSource: configurationSnapshot.Data(),
		maintenancePath:     maintenanceSnapshot.path,
		maintenanceSource:   maintenanceSnapshot.Data(),
		previousProvenance:  previousProvenance,
	}, nil
}

func loadGeneratedDependencyBaseline(moduleRoot string, selector configurationSelector) (applicationmeta.DependencyBaseline, applicationgen.ManifestProvenance, error) {
	recovery, exists, err := generatedfiles.ReadApplicationManifestRecovery(moduleRoot)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, applicationgen.ManifestProvenance{}, fmt.Errorf("load generated dependency baseline recovery: %w", err)
	}
	if exists {
		provenance, err := applicationgen.DecodeManifestProvenance(recovery)
		if err != nil {
			return applicationmeta.DependencyBaseline{}, applicationgen.ManifestProvenance{}, fmt.Errorf("load generated dependency baseline recovery: %w", err)
		}
		baseline, _ := provenance.BaselineForSelection(selector.mode, selector.path)
		return baseline, provenance, nil
	}
	data, exists, err := readGeneratedApplicationManifest(moduleRoot)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, applicationgen.ManifestProvenance{}, fmt.Errorf("load generated dependency baseline: %w", err)
	}
	if !exists {
		return applicationmeta.DependencyBaseline{}, applicationgen.ManifestProvenance{}, nil
	}
	provenance, err := applicationgen.DecodeManifestProvenance(data)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, applicationgen.ManifestProvenance{}, fmt.Errorf("load generated dependency baseline: %w", err)
	}
	baseline, _ := provenance.BaselineForSelection(selector.mode, selector.path)
	return baseline, provenance, nil
}
