package applicationresolve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
)

// SelectionInputs is the immutable authored discovery boundary shared with
// Resolve. It does not establish a valid composed or executable application.
// Private document bytes are available only through explicit snapshot access.
type SelectionInputs struct {
	module             modulelocate.Module
	dependencies       moduledependency.Index
	declarations       interfaceinventory.Discovery
	inventory          plugininventory.Index
	rootManifest       applicationmeta.Manifest
	selectedManifest   applicationmeta.Manifest
	rootSnapshot       ManifestSnapshot
	selectedSnapshot   ManifestSnapshot
	selector           configurationSelector
	moduleMetadata     []ModuleMetadataSnapshot
	workspaceSnapshots []selectionWorkspaceSnapshot
	dependencyOptions  moduledependency.Options
}

func (SelectionInputs) String() string   { return "<private-selection-inputs>" }
func (SelectionInputs) GoString() string { return "<private-selection-inputs>" }
func (SelectionInputs) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private-selection-inputs>"))
}
func (SelectionInputs) LogValue() slog.Value { return slog.StringValue("<private-selection-inputs>") }

// Module returns the nearest Project, independently of the selected document.
func (s SelectionInputs) Module() modulelocate.Module { return s.module }

// Dependencies returns the captured effective Go Module graph.
func (s SelectionInputs) Dependencies() moduledependency.Index { return s.dependencies }

// DataCompilerSelection resolves the exact published Data distribution from
// this Project's selected Go Module graph. It does not activate Data members.
func (s SelectionInputs) DataCompilerSelection() (datacompiler.Selection, error) {
	if s.module.Path() == "" {
		return datacompiler.Selection{}, fmt.Errorf("%w: Project selection inputs are empty", datacompiler.ErrSelection)
	}
	module, exists := s.dependencies.ByPath(datacompiler.ModulePath)
	if !exists {
		return datacompiler.Selection{}, fmt.Errorf("%w: selected Project graph has no %s module", datacompiler.ErrSelection, datacompiler.ModulePath)
	}
	_, replaced := module.Replacement()
	return datacompiler.Resolve(datacompiler.Source{
		ModulePath: module.Path(), ModuleVersion: module.SelectedVersion(),
		ModuleChecksum: module.Checksum(), Root: module.Root(),
		Workspace: module.Workspace(), Replacement: replaced,
	})
}

// Declarations returns the same validated Go declarations used by Resolve.
func (s SelectionInputs) Declarations() interfaceinventory.Discovery { return s.declarations }

// RootManifest returns the root application layer, or only its metadata in
// replacement mode, matching ordinary resolution's exclusion policy.
func (s SelectionInputs) RootManifest() applicationmeta.Manifest { return s.rootManifest }

// SelectedManifest returns the authored selected layer before typed composition.
func (s SelectionInputs) SelectedManifest() applicationmeta.Manifest { return s.selectedManifest }

// CurrentLayers returns authored current-Project layers before schema lookup:
// root then overlay in environment mode, otherwise the selected document only.
func (s SelectionInputs) CurrentLayers() []applicationmeta.Manifest {
	if s.module.Path() == "" {
		return nil
	}
	if s.selector.mode == configurationModeEnvironment {
		return []applicationmeta.Manifest{s.rootManifest, s.selectedManifest}
	}
	return []applicationmeta.Manifest{s.selectedManifest}
}

// RootSnapshot returns the original private root document and path identity.
func (s SelectionInputs) RootSnapshot() ManifestSnapshot { return s.rootSnapshot }

// SelectedSnapshot returns the exact preimage of the only selected YAML target.
func (s SelectionInputs) SelectedSnapshot() ManifestSnapshot { return s.selectedSnapshot }

// ConfigurationSelection returns mode, path and environment. Digest is empty:
// schema-directed composition has deliberately not occurred at this boundary.
func (s SelectionInputs) ConfigurationSelection() ConfigurationSelection {
	return ConfigurationSelection{mode: s.selector.mode, path: s.selector.path, environment: s.selector.environment}
}

// DiscoverSelectionInputs discovers authored selection inputs without requiring
// the old graph, configuration ownership, or required Config values to be valid.
// It validates selected YAML syntax and declaration schemas, loads and validates
// authored Go, and rechecks its document/module read set. No constructor, Secret
// resolver, generation helper or generated-history loader is invoked.
func DiscoverSelectionInputs(ctx context.Context, options Options) (SelectionInputs, error) {
	inputs, err := discoverSelectionInputs(ctx, options)
	if err != nil {
		return SelectionInputs{}, err
	}
	if err := inputs.ValidateSnapshot(ctx); err != nil {
		return SelectionInputs{}, err
	}
	return inputs, nil
}

func discoverSelectionInputs(ctx context.Context, options Options) (SelectionInputs, error) {
	if ctx == nil {
		return SelectionInputs{}, fmt.Errorf("%w: context is nil", ErrResolve)
	}
	if err := ctx.Err(); err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: locate Project: %w", ErrResolve, err)
	}
	rootSnapshot, err := loadProjectManifestSnapshot(module.ModulePath(), module.Path())
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	selector, err := resolveConfigurationSelector(module.Path(), options.ConfigurationPath, options.EnvironmentName, options.Environment)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrConfigurationSelection, err)
	}
	rootManifest, err := parseProjectManifestSnapshot(module.ModulePath(), rootSnapshot, selector.mode == configurationModeExplicit)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	rootManifest, err = applicationmeta.WithProjectModule(rootManifest, module.ModulePath())
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: associate root configuration with Project module: %w", ErrResolve, err)
	}
	selectedSnapshot := rootSnapshot
	selectedManifest := rootManifest
	if selector.path != applicationManifestName || selector.mode == configurationModeExplicit {
		if selector.mode == configurationModeEnvironment {
			selectedSnapshot, selectedManifest, err = loadEnvironmentOverlay(module.ModulePath(), module.Path(), selector.path)
		} else {
			selectedSnapshot, selectedManifest, err = loadConfiguration(module.ModulePath(), module.Path(), selector.path)
		}
		if err != nil {
			return SelectionInputs{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrConfigurationSelection, err)
		}
		selectedManifest, err = applicationmeta.WithProjectModule(selectedManifest, module.ModulePath())
		if err != nil {
			return SelectionInputs{}, fmt.Errorf("%w: associate selected configuration with Project module: %w", ErrResolve, err)
		}
	}
	metadata, err := readModuleMetadata(module.ModulePath(), module.Path(), "", true)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	dependencyOptions := moduledependency.Options{
		GoCommand: options.GoCommand, Environment: append([]string(nil), options.Environment...), OutputLimit: options.DependencyOutputLimit,
	}
	workspaceSnapshots, err := captureSelectionWorkspace(module.Path(), dependencyOptions.Environment)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, selectionWorkspaceError(module.ModulePath()))
	}
	dependencies, err := moduledependency.Discover(ctx, module, dependencyOptions)
	if err != nil {
		err = normalizeDependencyConcurrentChange(err)
		if errors.Is(err, projectlocate.ErrInvalidManifest) {
			err = fmt.Errorf("%w: %w", ErrManifest, err)
		}
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	moduleMetadata := []ModuleMetadataSnapshot{metadata}
	for _, dependency := range dependencies.Modules() {
		metadata, err := readModuleMetadata(dependency.Path(), dependency.Root(), dependency.SelectedVersion(), dependency.Project())
		if err != nil {
			return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
		}
		if dependency.Project() && !bytes.Equal(metadata.snapshot.data, dependency.ProjectGoMod()) {
			return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(dependency.Path(), "go.mod changed during discovery"))
		}
		moduleMetadata = append(moduleMetadata, metadata)
	}
	declarations, err := interfaceinventory.DiscoverApplication(ctx, module, dependencies, interfaceinventory.Options{
		GoCommand: options.GoCommand, Environment: append([]string(nil), options.Environment...), OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := interfaceinventory.ValidateUniqueIDs(declarations.Interfaces()); err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	inventory, err := plugininventory.Build(module, dependencies)
	if err != nil {
		return SelectionInputs{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return SelectionInputs{
		module: module, dependencies: dependencies, declarations: declarations, inventory: inventory,
		rootManifest: rootManifest, selectedManifest: selectedManifest,
		rootSnapshot: rootSnapshot, selectedSnapshot: selectedSnapshot, selector: selector,
		moduleMetadata: moduleMetadata, workspaceSnapshots: workspaceSnapshots, dependencyOptions: dependencyOptions,
	}, nil
}

func (s SelectionInputs) schemaLookup(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
	switch namespace {
	case applicationmeta.ConfigurationNamespaceImplementation:
		if implementation, exists := s.declarations.Implementations().BySymbol(symbol); exists {
			return implementation.Configuration()
		}
	case applicationmeta.ConfigurationNamespaceResource:
		if provider, exists := s.declarations.ResourceProviders().BySymbol(symbol); exists {
			return provider.Configuration()
		}
	}
	return implementationinventory.Configuration{}, false
}

// ComposeCandidate parses replacement bytes for the selected document and uses
// the captured lower layers and compiled schemas. It neither writes the bytes
// nor checks graph validity, configuration ownership or required Config values.
// Changing the root Project or selected document requires fresh discovery.
func (s SelectionInputs) ComposeCandidate(selected []byte) (applicationmeta.Composition, error) {
	return s.composeCandidate(selected, false)
}

// ComposeSelectionCandidate composes only selection identities and removals,
// stripping Implementation and Resource Config from every captured layer. It
// allows ownership planning when old Config is invalid. This is never final
// validation or executable input: compose the real edited bytes with
// ComposeCandidate and call ValidateCandidate before any live mutation.
func (s SelectionInputs) ComposeSelectionCandidate(selected []byte) (applicationmeta.Composition, error) {
	return s.composeCandidate(selected, true)
}

func (s SelectionInputs) composeCandidate(selected []byte, identitiesOnly bool) (applicationmeta.Composition, error) {
	if s.module.Path() == "" {
		return applicationmeta.Composition{}, fmt.Errorf("%w: selection inputs are empty", ErrResolve)
	}
	parse := applicationmeta.ParseCompleteSource
	if s.selector.mode == configurationModeEnvironment {
		parse = applicationmeta.ParseOverlaySource
	}
	manifest, err := parseConfigurationSnapshot(s.module.ModulePath(), ManifestSnapshot{path: s.selector.path, data: selected}, parse)
	if err != nil {
		return applicationmeta.Composition{}, fmt.Errorf("%w: %w: %w", ErrResolve, ErrConfigurationSelection, err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, s.module.ModulePath())
	if err != nil {
		return applicationmeta.Composition{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if identitiesOnly {
		manifest = applicationmeta.WithoutConstructorConfiguration(manifest)
		s.rootManifest = applicationmeta.WithoutConstructorConfiguration(s.rootManifest)
	}
	_, composition, err := s.composeCurrent(s.rootManifest, manifest)
	return composition, err
}

func (s SelectionInputs) composeCurrent(base, selected applicationmeta.Manifest) (applicationmeta.Manifest, applicationmeta.Composition, error) {
	current := selected
	if s.selector.mode == configurationModeEnvironment {
		var err error
		current, err = applicationmeta.ApplyOverlay(base, selected, s.schemaLookup)
		if err != nil {
			return applicationmeta.Manifest{}, applicationmeta.Composition{}, fmt.Errorf("%w: environment %q: %w", ErrResolve, s.selector.environment, err)
		}
	}
	current = applicationmeta.WithRootMetadata(current, s.rootManifest)
	composition, err := applicationmeta.Compose(nil, current, s.schemaLookup)
	if err != nil {
		return applicationmeta.Manifest{}, applicationmeta.Composition{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return current, composition, nil
}

// ResolveCandidateInterfaces resolves only the candidate Interface and Resource
// graph, retaining ordinary legacy exposure filtering and source provenance.
// Ownership and required-value validation remain the final resolver's job.
func (s SelectionInputs) ResolveCandidateInterfaces(composition applicationmeta.Composition) (interfaceresolution.Result, error) {
	if s.module.Path() == "" || !composition.Valid() {
		return interfaceresolution.Result{}, fmt.Errorf("%w: selection inputs or composition are empty", ErrResolve)
	}
	sources, err := s.candidateSourceContext(composition)
	if err != nil {
		return interfaceresolution.Result{}, err
	}
	result, err := resolveInterfaces(composition.Manifest(), composition, s.declarations.Interfaces(), s.declarations.Implementations(), s.declarations.ResourceProviders(), s.inventory, sources)
	if err != nil {
		return interfaceresolution.Result{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return result, nil
}

// CandidateOwners returns explicit (including dormant) and Interface-reachable
// Implementation constructors without validating Resource bindings, Config
// ownership or required values. An incomplete Interface closure is still an
// error; any returned owners then prove presence only, never absence. Ownership
// is cleanup evidence, not proof of a valid application.
func (s SelectionInputs) CandidateOwners(composition applicationmeta.Composition) ([]constructorsymbol.Symbol, error) {
	if s.module.Path() == "" || !composition.Valid() {
		return nil, fmt.Errorf("%w: selection inputs or composition are empty", ErrResolve)
	}
	sources, err := s.candidateSourceContext(composition)
	if err != nil {
		return nil, err
	}
	input, err := interfaceResolutionInput(composition.Manifest(), s.declarations.Interfaces(), s.declarations.Implementations(), s.declarations.ResourceProviders(), s.inventory, sources)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	owners, err := interfaceresolution.SelectedConstructors(input)
	if err != nil {
		return owners, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return owners, nil
}

// ValidateCandidate checks the complete candidate Interface/Resource graph,
// constructor Config ownership and required values before a planned write.
// It never executes legacy generation helpers, constructors or Secret resolvers.
// Final generation still validates its remaining policies and outputs.
func (s SelectionInputs) ValidateCandidate(composition applicationmeta.Composition) error {
	resolution, err := s.ResolveCandidateInterfaces(composition)
	if err != nil {
		return err
	}
	sources, err := s.candidateSourceContext(composition)
	if err != nil {
		return err
	}
	return s.validateCandidateConfiguration(composition, resolution, sources)
}

func (s SelectionInputs) validateCandidateConfiguration(composition applicationmeta.Composition, resolution interfaceresolution.Result, sources applicationinput.SourceContext) error {
	if err := validateConstructorConfigurationOwners(composition.Manifest(), resolution, sources); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	var active []constructorsymbol.Symbol
	for _, node := range resolution.Graph().ConstructionOrder() {
		active = append(active, node.Symbol())
	}
	if err := composition.ValidateRequiredConfiguration(s.schemaLookup, active, s.selector.path); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return nil
}

func (s SelectionInputs) candidateSourceContext(composition applicationmeta.Composition) (applicationinput.SourceContext, error) {
	var paths []string
	for _, layer := range composition.CurrentLayers() {
		decisions, err := applicationmeta.ConfigurationDecisions(layer, s.schemaLookup)
		if err != nil {
			return applicationinput.SourceContext{}, fmt.Errorf("%w: selected configuration provenance: %w", ErrResolve, err)
		}
		for _, decision := range decisions {
			if decision.ResolutionRelevant() {
				paths = append(paths, decision.Path())
			}
		}
	}
	return applicationInputSourceContext(s.module, s.dependencies, composition, paths), nil
}
