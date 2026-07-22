// Package applicationresolve constructs one complete resolved application from
// the nearest Plystra Project without mutating application input.
package applicationresolve

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	generation "github.com/plystra/cli/generation/v1"
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
	"github.com/plystra/cli/internal/resolutionevidence"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"golang.org/x/mod/modfile"
)

var (
	// ErrResolve reports failure to construct one complete filesystem-backed
	// application resolution.
	ErrResolve = errors.New("resolve filesystem application")
	// ErrManifest reports a missing, unsafe, unreadable, or invalid root
	// plystra.yaml.
	ErrManifest = errors.New("load application manifest")
	// ErrConfigurationSelection reports an invalid, missing, unsafe, or
	// conflicting current-project configuration selector or selected document.
	ErrConfigurationSelection = errors.New("select current-project configuration")
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
	evidence            resolutionevidence.Evidence
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

// ResolutionEvidence returns the immutable deterministic identity derived
// from the same normalized application model used for generation and assembly.
func (r Result) ResolutionEvidence() resolutionevidence.Evidence { return r.evidence }

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
		return Result{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrConfigurationSelection, err)
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
			return Result{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrConfigurationSelection, err)
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
	rootDigest, err := applicationgen.ConfigurationDigest(rootData)
	if err != nil {
		return Result{}, fmt.Errorf("%w: digest root configuration %s: %w", ErrResolve, applicationManifestName, err)
	}
	configurationProvenance := &generation.ConfigurationProvenanceInput{
		Mode:                        generation.ConfigurationMode(selector.mode),
		Environment:                 selector.environment,
		RootPath:                    applicationManifestName,
		RootDigest:                  rootDigest,
		SelectedPath:                selector.path,
		SelectedDigest:              selectedDigest,
		DependencyCompositionDigest: composition.DependencyDigest(),
	}
	currentProjectPaths := maintenance.LocalPaths()
	if selector.mode == configurationModeEnvironment {
		currentProjectPaths = append(currentProjectPaths, resolutionDeclarationPaths(selectedManifest)...)
	}
	input, err := applicationinput.Build(manifest, inventory, applicationInputSourceContext(module, dependencies, composition, currentProjectPaths), configurationProvenance, generationexec.BuildOptions{
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
	evidenceModules, err := resolutionEvidenceModules(module, dependencies)
	if err != nil {
		return Result{}, fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
	}
	configurationEvidence, err := resolutionEvidenceConfigurationInput(selector, composition, maintainedManifest, selectedManifest, maintenance, schemaLookup)
	if err != nil {
		return Result{}, fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
	}
	assemblyEvidence := resolutionEvidenceAssemblyInput(configs)
	httpTransports := manifest.HTTPTransports()
	evidence, err := resolutionevidence.Build(resolutionevidence.Input{
		Context:            resolution.Context(),
		ProviderResolution: resolution.ActivationResolution().ProviderResolution(),
		AliasResolution:    resolution.AliasResolution(),
		Modules:            evidenceModules,
		PluginCandidates:   resolutionEvidencePluginCandidates(inventory),
		Configuration:      &configurationEvidence,
		StaticAssembly:     &assemblyEvidence,
		HTTPTransports:     &httpTransports,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
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
		evidence:        evidence,
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

func applicationInputSourceContext(module modulelocate.Module, dependencies moduledependency.Index, composition applicationmeta.Composition, currentProjectPaths []string) applicationinput.SourceContext {
	projects := dependencies.Projects()
	values := make([]applicationinput.DependencySource, len(projects))
	for index, dependency := range projects {
		values[index] = applicationinput.DependencySource{
			ModulePath: dependency.Path(),
			Version:    dependency.SelectedVersion(),
		}
	}
	provenance := composition.ResolutionSources()
	configurationSources := make([]applicationinput.DependencyProvenance, len(provenance))
	for index, record := range provenance {
		configurationSources[index] = applicationinput.DependencyProvenance{
			Path:    record.Path(),
			Sources: record.Sources(),
		}
	}
	return applicationinput.SourceContext{
		CurrentModulePath:    module.ModulePath(),
		Dependencies:         values,
		DependencyProvenance: configurationSources,
		CurrentProjectPaths:  uniqueSortedStrings(currentProjectPaths),
	}
}

func resolutionDeclarationPaths(manifest applicationmeta.Manifest) []string {
	paths := make([]string, 0, len(manifest.HTTPExposures())+len(manifest.Requirements())+len(manifest.ProviderChoices())+len(manifest.Aliases()))
	for _, exposure := range manifest.HTTPExposures() {
		paths = append(paths, fmt.Sprintf("http.expose[%q]", exposure.ID().String()))
	}
	for _, requirement := range manifest.Requirements() {
		paths = append(paths, fmt.Sprintf("capabilities.require[%q]", requirement.ID().String()))
	}
	for _, choice := range manifest.ProviderChoices() {
		paths = append(paths, fmt.Sprintf("capabilities.use[%q]", choice.Capability().String()))
	}
	for _, alias := range manifest.Aliases() {
		paths = append(paths, fmt.Sprintf("capabilities.aliases[%q]", alias.ID().String()))
	}
	return paths
}

func uniqueSortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write != 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func resolutionEvidenceModules(current modulelocate.Module, dependencies moduledependency.Index) ([]resolutionevidence.ModuleInput, error) {
	modules := []resolutionevidence.ModuleInput{{
		Path:             current.ModulePath(),
		Role:             resolutionevidence.ModuleRoleCurrent,
		SourceModulePath: current.ModulePath(),
	}}
	for _, dependency := range dependencies.Projects() {
		input := resolutionevidence.ModuleInput{
			Path:             dependency.Path(),
			Role:             resolutionevidence.ModuleRoleDependency,
			RequiredVersion:  dependency.RequiredVersion(),
			SelectedVersion:  dependency.SelectedVersion(),
			Direct:           dependency.Direct(),
			Indirect:         dependency.Indirect(),
			Workspace:        dependency.Workspace(),
			SourceModulePath: dependency.Path(),
		}
		if replacement, exists := dependency.Replacement(); exists {
			kind := resolutionevidence.ReplacementModule
			sourceModulePath := replacement.Path()
			if replacement.Local() {
				kind = resolutionevidence.ReplacementLocal
				sourceModulePath = modfile.ModulePath(dependency.ProjectGoMod())
				if sourceModulePath == "" {
					return nil, fmt.Errorf("dependency Project %q local replacement has no stable source module identity", dependency.Path())
				}
			}
			input.SourceModulePath = sourceModulePath
			input.Replacement = &resolutionevidence.ReplacementInput{
				Kind:       kind,
				ModulePath: sourceModulePath,
				Version:    replacement.Version(),
			}
		}
		modules = append(modules, input)
	}
	return modules, nil
}

func resolutionEvidencePluginCandidates(inventory plugininventory.Index) []resolutionevidence.PluginCandidateInput {
	plugins := inventory.Plugins()
	inputs := make([]resolutionevidence.PluginCandidateInput, len(plugins))
	for index, plugin := range plugins {
		inputs[index] = resolutionevidence.PluginCandidateInput{
			ID:         plugin.ID(),
			ModulePath: plugin.ModulePath(),
			Path:       plugin.Path(),
		}
	}
	return inputs
}

func resolutionEvidenceConfigurationInput(
	selector configurationSelector,
	composition applicationmeta.Composition,
	maintained applicationmeta.Manifest,
	selected applicationmeta.Manifest,
	maintenance applicationmeta.ConfigurationMaintenance,
	schemas applicationmeta.SchemaLookup,
) (resolutionevidence.ConfigurationInput, error) {
	local := make(map[string]struct{}, len(maintenance.LocalPaths()))
	for _, path := range maintenance.LocalPaths() {
		local[path] = struct{}{}
	}
	currentDecisions := func(manifest applicationmeta.Manifest, filterMaintained bool) ([]applicationmeta.ConfigurationDecision, error) {
		decisions, err := applicationmeta.ConfigurationDecisions(manifest, schemas)
		if err != nil {
			return nil, err
		}
		if !filterMaintained {
			return decisions, nil
		}
		result := make([]applicationmeta.ConfigurationDecision, 0, len(decisions))
		for _, decision := range decisions {
			if !decision.DependencyComposable() {
				result = append(result, decision)
				continue
			}
			if _, explicit := local[decision.Path()]; explicit {
				result = append(result, decision)
			}
		}
		return result, nil
	}

	base, err := currentDecisions(maintained, true)
	if err != nil {
		return resolutionevidence.ConfigurationInput{}, err
	}
	layers := make([]resolutionevidence.ConfigurationLayerInput, 0, 2)
	switch selector.mode {
	case configurationModeDefault:
		layers = append(layers, resolutionevidence.ConfigurationLayerInput{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: base})
	case configurationModeEnvironment:
		overlay, err := currentDecisions(selected, false)
		if err != nil {
			return resolutionevidence.ConfigurationInput{}, err
		}
		layers = append(layers,
			resolutionevidence.ConfigurationLayerInput{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: base},
			resolutionevidence.ConfigurationLayerInput{Owner: resolutionevidence.ConfigurationOwnerEnvironment, Decisions: overlay},
		)
	case configurationModeExplicit:
		layers = append(layers, resolutionevidence.ConfigurationLayerInput{Owner: resolutionevidence.ConfigurationOwnerExplicit, Decisions: base})
	default:
		return resolutionevidence.ConfigurationInput{}, fmt.Errorf("unsupported configuration selection mode %q", selector.mode)
	}
	effective, err := applicationmeta.ConfigurationDecisions(composition.Manifest(), schemas)
	if err != nil {
		return resolutionevidence.ConfigurationInput{}, err
	}
	return resolutionevidence.ConfigurationInput{
		DependencyBaseline: composition.DependencyBaseline(),
		Layers:             layers,
		Effective:          effective,
	}, nil
}

func resolutionEvidenceAssemblyInput(configs configurationresolve.Result) resolutionevidence.StaticAssemblyInput {
	bindings := configs.Bindings()
	plugins := make([]resolutionevidence.AssemblyPluginInput, len(bindings))
	for index, binding := range bindings {
		plugins[index] = resolutionevidence.AssemblyPluginInput{
			PluginID:      binding.PluginID(),
			ModulePath:    binding.ModulePath(),
			ModuleVersion: binding.ModuleVersion(),
			ImportPath:    binding.ImportPath(),
		}
	}
	return resolutionevidence.StaticAssemblyInput{Plugins: plugins}
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
