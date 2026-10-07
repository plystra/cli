// Package applicationresolve constructs one complete resolved application from
// the nearest Plystra Project without mutating application input.
package applicationresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/resolutionevidence"
	"github.com/plystra/cli/internal/resourceproviderinventory"
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
	// ErrConcurrentChange reports authored documents, module identities or
	// declaration semantics changing before resolution or planning completed.
	ErrConcurrentChange = errors.New("application manifest changed during resolution")
	// ErrUnownedConstructorConfiguration reports effective constructor
	// configuration whose constructor is neither explicitly selected nor
	// reachable in the frozen Interface constructor graph.
	ErrUnownedConstructorConfiguration = errors.New("constructor configuration has no explicit selection or reachable constructor")
	// ErrDataCompilerUnavailable reports selected Data activation before the
	// CLI can emit and install the accepted compiler result.
	ErrDataCompilerUnavailable = errors.New("Data compiler integration is unavailable")
	// ErrDataCompilerAnalysisUnavailable reports a failure to invoke the
	// selected compiler or independently accept its analyze result.
	ErrDataCompilerAnalysisUnavailable = errors.New("Data compiler analyze integration is unavailable")
	// ErrDataAssignment reports an invalid Core-owned activation after Data
	// analysis has already been accepted.
	ErrDataAssignment = errors.New("invalid Data member activation")
)

// DataCompilerUnavailableError identifies the selected member whose accepted
// Data result cannot yet be installed by this CLI.
type DataCompilerUnavailableError struct {
	member       applicationmeta.DataMember
	cause        error
	acquisition  DataCompilerAcquisition
	observations []datacompiler.Observation
}

// DataAssignmentError identifies a selected member whose accepted Data model
// cannot be mapped to the current Project's explicit Resource assignments.
type DataAssignmentError struct {
	member applicationmeta.DataMember
	cause  error
}

func (e *DataAssignmentError) Error() string {
	if e == nil || e.cause == nil {
		return ErrDataAssignment.Error()
	}
	return fmt.Sprintf("%s: %s: %v", ErrDataAssignment, e.member.Source(), e.cause)
}

func (e *DataAssignmentError) Unwrap() []error {
	if e == nil || e.cause == nil {
		return []error{ErrDataAssignment}
	}
	return []error{ErrDataAssignment, e.cause}
}

// Source returns the selected member declaration that owns the invalid
// activation.
func (e *DataAssignmentError) Source() applicationmeta.ConfigurationDeclarationSource {
	if e == nil {
		return applicationmeta.ConfigurationDeclarationSource{}
	}
	return e.member.DeclarationSource()
}

func (e *DataCompilerUnavailableError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", ErrDataCompilerUnavailable, e.member.Source(), e.cause)
	}
	return fmt.Sprintf("%s: %s requires the selected Data compiler before generation", ErrDataCompilerUnavailable, e.member.Source())
}
func (e *DataCompilerUnavailableError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrDataCompilerUnavailable}
	}
	return []error{ErrDataCompilerUnavailable, e.cause}
}
func (e *DataCompilerUnavailableError) Source() applicationmeta.ConfigurationDeclarationSource {
	return e.member.DeclarationSource()
}

// Acquisition returns the verified compiler identity when acquisition
// completed before a later Data phase failed. The boolean is false when no
// compiler executable was materialized or reused.
func (e *DataCompilerUnavailableError) Acquisition() (DataCompilerAcquisition, bool) {
	if e == nil || !e.acquisition.Valid() {
		return DataCompilerAcquisition{}, false
	}
	return e.acquisition, true
}

// Observations returns public-safe compiler effects observed before the Data
// boundary failed. Host paths and raw compiler arguments are never retained.
func (e *DataCompilerUnavailableError) Observations() []datacompiler.Observation {
	if e == nil {
		return nil
	}
	return cloneDataCompilerObservations(e.observations)
}

type dependencyConcurrentChangeError struct {
	cause error
}

func (e *dependencyConcurrentChangeError) Error() string {
	if e == nil || e.cause == nil {
		return ErrConcurrentChange.Error()
	}
	return e.cause.Error()
}

func (e *dependencyConcurrentChangeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *dependencyConcurrentChangeError) Is(target error) bool {
	return e != nil && target == ErrConcurrentChange
}

func normalizeDependencyConcurrentChange(cause error) error {
	if cause == nil || errors.Is(cause, ErrConcurrentChange) || !errors.Is(cause, moduledependency.ErrConcurrentChange) {
		return cause
	}
	return &dependencyConcurrentChangeError{cause: cause}
}

// UnownedConstructorConfigurationError reports one effective constructor
// configuration together with every contributing Project document.
// Configuration values and Secret-reference targets are never retained by
// this error.
type UnownedConstructorConfigurationError struct {
	constructor constructorsymbol.Symbol
	sources     []applicationinput.ConfigurationSource
}

// Constructor returns the exact unselected constructor symbol.
func (e *UnownedConstructorConfigurationError) Constructor() constructorsymbol.Symbol {
	if e == nil {
		return constructorsymbol.Symbol{}
	}
	return e.constructor
}

// Sources returns a defensive copy in deterministic module/path/span order.
func (e *UnownedConstructorConfigurationError) Sources() []applicationinput.ConfigurationSource {
	if e == nil {
		return nil
	}
	return append([]applicationinput.ConfigurationSource(nil), e.sources...)
}

func (e *UnownedConstructorConfigurationError) Error() string {
	if e == nil {
		return ErrUnownedConstructorConfiguration.Error()
	}
	reference := "selected Project configuration"
	if len(e.sources) != 0 {
		reference = e.sources[0].Reference
	}
	return fmt.Sprintf("%s: config[%q] at %s", ErrUnownedConstructorConfiguration, e.constructor, reference)
}

// Unwrap supports errors.Is with ErrUnownedConstructorConfiguration.
func (*UnownedConstructorConfigurationError) Unwrap() error {
	return ErrUnownedConstructorConfiguration
}

// Options contains the application location and bounded Go helper settings.
// Environment is shared by read-only module and authored-declaration package
// discovery plus selected legacy generation compilation so each observes the
// same Go workspace state during the architecture transition.
type Options struct {
	// RequireExecutablePolicies rejects active policy fields that the installed
	// CLI/Kernel pair cannot enforce. Read-only inspection leaves it false.
	RequireExecutablePolicies bool
	Start                     string
	ConfigurationPath         string
	EnvironmentName           string
	GoCommand                 string
	Environment               []string
	DependencyOutputLimit     int
	// Offline constrains module and eligible-package discovery to locally
	// available selected sources. Other Go helpers still need their own guard.
	Offline               bool
	DataCompilerCacheRoot string
	CompileTimeout        time.Duration
	ExecutionTimeout      time.Duration
	TemporaryParent       string
	DataCompilerObserve   datacompiler.ObservationSink
}

// Result is one immutable filesystem provenance and stable generation
// resolution assembled from the same application snapshot. On an active Data
// boundary failure, only verified Data handoff fields and public-safe compiler
// observations may be populated.
type Result struct {
	module                   modulelocate.Module
	currentManifest          applicationmeta.Manifest
	composition              applicationmeta.Composition
	dependencies             moduledependency.Index
	interfaces               interfaceinventory.Index
	resources                interfaceinventory.ResourceIndex
	resourceProviders        resourceproviderinventory.Index
	implementations          implementationinventory.Index
	interfaceResolution      interfaceresolution.Result
	inventory                plugininventory.Index
	resolution               generationresolution.ExtensionResult
	configs                  configurationresolve.Result
	selection                ConfigurationSelection
	evidence                 resolutionevidence.Evidence
	rootData                 []byte
	rootDigest               string
	configurationSource      []byte
	dataCompiler             datacompiler.Artifact
	dataCompilerManifest     datacompiler.Manifest
	dataCompilerStatus       DataCompilerAcquisition
	dataAnalysis             DataAnalysisAcceptance
	dataActivation           DataActivation
	dataAnalyzeOutput        json.RawMessage
	dataCompilerObservations []datacompiler.Observation
}

// Module returns the nearest Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Manifest returns the effective current-project application declaration.
func (r Result) Manifest() applicationmeta.Manifest { return r.composition.Manifest() }

// CurrentManifest returns the normalized selected current-project layer.
func (r Result) CurrentManifest() applicationmeta.Manifest { return r.currentManifest }

// Composition returns the effective current-project application declaration.
func (r Result) Composition() applicationmeta.Composition { return r.composition }

// Dependencies returns the immutable effective Go Module graph used for
// dependency-Project discovery and generated runtime build provenance.
func (r Result) Dependencies() moduledependency.Index { return r.dependencies }

// Interfaces returns every active local and dependency-Project Interface
// declaration discovered through ordinary Go package loading.
func (r Result) Interfaces() interfaceinventory.Index { return r.interfaces }

// Resources returns visible consumer contracts without activating instances.
func (r Result) Resources() interfaceinventory.ResourceIndex { return r.resources }

// ResourceProviders returns visible validated providers, independently of selection.
func (r Result) ResourceProviders() resourceproviderinventory.Index { return r.resourceProviders }

// Implementations returns every active local and dependency-Project
// constructor declaration discovered through the same ordinary Go package
// loading boundary as Interfaces.
func (r Result) Implementations() implementationinventory.Index { return r.implementations }

// InterfaceResolution returns the immutable selected target-architecture
// Interface bindings and validated reachable constructor dependency graph.
func (r Result) InterfaceResolution() interfaceresolution.Result { return r.interfaceResolution }

// Inventory returns every visible local and dependency-Project plugin.
func (r Result) Inventory() plugininventory.Index { return r.inventory }

// Resolution returns the stable provider, extension, contribution, and Alias
// closure.
func (r Result) Resolution() generationresolution.ExtensionResult { return r.resolution }

// Configurations returns the validated private selected-plugin configuration
// closure. Its values never enter generation-extension input.
func (r Result) Configurations() configurationresolve.Result { return r.configs }

// ConfigurationSelection returns the immutable current-project document
// selection and normalized semantic digest used by this resolution.
func (r Result) ConfigurationSelection() ConfigurationSelection { return r.selection }

// ResolutionEvidence returns the immutable deterministic identity derived
// from the same normalized application model used for generation and assembly.
func (r Result) ResolutionEvidence() resolutionevidence.Evidence { return r.evidence }

// RootConfigurationData returns the root marker document represented by
// generated provenance.
func (r Result) RootConfigurationData() []byte { return append([]byte(nil), r.rootData...) }

// SelectedConfigurationData returns the selected document bytes. These private
// transaction inputs never enter public provenance.
func (r Result) SelectedConfigurationData() []byte {
	return r.ConfigurationSource()
}

// RootConfigurationDigest returns the normalized identity of the mandatory
// root configuration document represented by generated provenance.
func (r Result) RootConfigurationDigest() string { return r.rootDigest }

// ConfigurationSource returns defensive original selected-document bytes.
func (r Result) ConfigurationSource() []byte {
	return append([]byte(nil), r.configurationSource...)
}

// DataCompilerAcquisition returns the verified compiler identity retained by
// resolution, including a partial result returned with a later Data error.
func (r Result) DataCompilerAcquisition() (DataCompilerAcquisition, bool) {
	if !r.dataCompilerStatus.Valid() {
		return DataCompilerAcquisition{}, false
	}
	return r.dataCompilerStatus, true
}

// DataCompilerArtifact returns the verified private compiler executable for a
// later protocol phase. Callers must not persist or expose its path.
func (r Result) DataCompilerArtifact() (datacompiler.Artifact, bool) {
	if r.dataCompiler.Path == "" {
		return datacompiler.Artifact{}, false
	}
	return r.dataCompiler, true
}

// DataCompilerManifest returns the exact selected distribution contract for
// the retained compiler. It is absent when no compiler was acquired.
func (r Result) DataCompilerManifest() (datacompiler.Manifest, bool) {
	if !r.dataCompilerStatus.Valid() {
		return datacompiler.Manifest{}, false
	}
	return r.dataCompilerManifest, true
}

// DataAnalysis returns the independently accepted Data model retained before
// generation. Its contents are immutable through defensive accessors.
func (r Result) DataAnalysis() (DataAnalysisAcceptance, bool) {
	if !r.dataAnalysis.Valid() {
		return DataAnalysisAcceptance{}, false
	}
	return r.dataAnalysis, true
}

// DataActivation returns the explicit Core-owned Resource assignment retained
// after Data analysis acceptance. It is absent until the later emit and graph
// installation boundaries consume it.
func (r Result) DataActivation() (DataActivation, bool) {
	if !r.dataActivation.Valid() {
		return DataActivation{}, false
	}
	return DataActivation{assignments: r.dataActivation.Assignments()}, true
}

// DataAnalyzeOutput returns the accepted versioned analyze payload for emit.
func (r Result) DataAnalyzeOutput() (json.RawMessage, bool) {
	if !r.dataAnalysis.Valid() || len(r.dataAnalyzeOutput) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), r.dataAnalyzeOutput...), true
}

// DataCompilerObservations returns public-safe compiler effects observed while
// acquiring or invoking the selected Data compiler.
func (r Result) DataCompilerObservations() []datacompiler.Observation {
	return cloneDataCompilerObservations(r.dataCompilerObservations)
}

func cloneDataCompilerObservations(input []datacompiler.Observation) []datacompiler.Observation {
	result := make([]datacompiler.Observation, len(input))
	for index, observation := range input {
		result[index] = observation
		result[index].Verification = append([]string(nil), observation.Verification...)
	}
	return result
}

// Resolve locates the nearest Project, loads its root plystra.yaml, discovers
// the effective Go Module graph and active authored Interface and Implementation
// packages, indexes legacy Project inputs not yet removed by later roadmap
// gates, and resolves the application. It rechecks captured documents, module
// identities and declaration semantics before returning and writes no
// application files.
func Resolve(ctx context.Context, options Options) (Result, error) {
	inputs, err := discoverSelectionInputs(ctx, options)
	if err != nil {
		return Result{}, err
	}
	module, dependencies, declarations := inputs.module, inputs.dependencies, inputs.declarations
	rootSnapshot, configurationSnapshot := inputs.rootSnapshot, inputs.selectedSnapshot
	rootManifest, selectedManifest, selector := inputs.rootManifest, inputs.selectedManifest, inputs.selector
	interfaces := declarations.Interfaces()
	implementations := declarations.Implementations()
	inventory, schemaLookup := inputs.inventory, inputs.schemaLookup
	currentManifest, composition, err := inputs.composeCurrent(rootManifest, selectedManifest)
	if err != nil {
		return Result{}, err
	}
	manifest := composition.Manifest()
	var dataMembers []applicationmeta.DataMember
	var dataRun dataAnalyzeRun
	var dataAnalysis DataAnalysisAcceptance
	var dataActivation DataActivation
	if members := manifest.DataMembers(); len(members) != 0 {
		dataMembers = members
		memberIDs := make([]string, len(members))
		for index, member := range members {
			memberIDs[index] = member.ID()
		}
		dataRun, err = inputs.analyzeData(ctx, options)
		partial := Result{
			module:       module,
			selection:    ConfigurationSelection{mode: selector.mode, path: selector.path, environment: selector.environment},
			dataCompiler: dataRun.artifact, dataCompilerManifest: dataRun.manifest,
			dataCompilerStatus:       dataRun.status,
			dataCompilerObservations: cloneDataCompilerObservations(dataRun.observations),
		}
		if err != nil {
			return partial, fmt.Errorf("%w: %w", ErrResolve, &DataCompilerUnavailableError{member: members[0], cause: err, acquisition: dataRun.status, observations: dataRun.observations})
		}
		dataAnalysis, err = validateDataAnalysisWithBounds(dataRun.response, memberIDs, dataRun.snapshot, dataRun.bounds, true)
		if err != nil {
			return partial, fmt.Errorf("%w: %w", ErrResolve, &DataCompilerUnavailableError{member: members[0], cause: fmt.Errorf("%w: %v", ErrDataCompilerAnalysisUnavailable, err), acquisition: dataRun.status, observations: dataRun.observations})
		}
		dataActivation, err = BuildDataActivation(manifest, dataAnalysis)
		if err != nil {
			return partial, fmt.Errorf("%w: %w", ErrResolve, &DataAssignmentError{member: members[0], cause: err})
		}
	}
	dataPartial := func() Result {
		return Result{
			module:       module,
			selection:    ConfigurationSelection{mode: selector.mode, path: selector.path, environment: selector.environment},
			dataCompiler: dataRun.artifact, dataCompilerManifest: dataRun.manifest,
			dataCompilerStatus: dataRun.status,
			dataAnalysis:       dataAnalysis, dataActivation: dataActivation,
			dataAnalyzeOutput:        append(json.RawMessage(nil), dataRun.response.Output...),
			dataCompilerObservations: cloneDataCompilerObservations(dataRun.observations),
		}
	}
	currentLayers := composition.CurrentLayers()
	if len(currentLayers) == 0 {
		return dataPartial(), fmt.Errorf("%w: composed current-project layers are absent", ErrResolve)
	}
	// Every selected layer is authored by the current Project. Keep its exact
	// decision paths available to source validation without inferring ownership
	// from dependency state or an earlier generated manifest.
	selectedLayer := applicationmeta.WithRootMetadata(currentLayers[len(currentLayers)-1], selectedManifest)
	baseLayer := rootManifest
	if selector.mode == configurationModeExplicit {
		baseLayer = selectedManifest
	}
	currentProjectPaths, err := currentProjectConfigurationPaths(baseLayer, selectedLayer, selector.mode == configurationModeEnvironment, schemaLookup)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: selected configuration provenance: %w", ErrResolve, err)
	}
	sourceContext := applicationInputSourceContext(module, dependencies, composition, currentProjectPaths)
	var generatedResources []constructorgraph.GeneratedResourceInput
	if dataActivation.Valid() {
		generatedResources, err = buildDataGeneratedResources(module.ModulePath(), manifest, dataActivation)
		if err != nil {
			return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, &DataAssignmentError{member: dataMembers[0], cause: err})
		}
	}
	interfaceResolution, err := resolveInterfaces(manifest, composition, interfaces, implementations, declarations.ResourceProviders(), inventory, sourceContext, generatedResources)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := inputs.validateCandidateConfiguration(composition, interfaceResolution, sourceContext); err != nil {
		return dataPartial(), err
	}
	rootLayerManifest := rootManifest
	selectedLayerManifest := selectedManifest
	if selector.mode == configurationModeExplicit {
		rootLayerManifest = selectedManifest
		selectedLayerManifest = selectedManifest
	} else if selector.mode == configurationModeEnvironment {
		selectedLayerManifest = selectedLayer
	}
	selectedDigest, err := applicationmeta.ConfigurationLayerDigest(selectedLayerManifest, schemaLookup)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: digest selected configuration %s: %w", ErrResolve, selector.path, err)
	}
	rootData := rootSnapshot.Data()
	rootDigest, err := applicationmeta.ConfigurationLayerDigest(rootLayerManifest, schemaLookup)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: digest root configuration %s: %w", ErrResolve, applicationManifestName, err)
	}
	configurationProvenance := &generation.ConfigurationProvenanceInput{
		Mode:           generation.ConfigurationMode(selector.mode),
		Environment:    selector.environment,
		RootPath:       applicationManifestName,
		RootDigest:     rootDigest,
		SelectedPath:   selector.path,
		SelectedDigest: selectedDigest,
	}
	input, err := applicationinput.Build(manifest, inventory, sourceContext, configurationProvenance, generationexec.BuildOptions{
		GoCommand:        options.GoCommand,
		BuildEnvironment: append([]string(nil), options.Environment...),
		CompileTimeout:   options.CompileTimeout,
		ExecutionTimeout: options.ExecutionTimeout,
		TemporaryParent:  options.TemporaryParent,
	})
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, err)
	}
	resolution, err := generationresolution.ResolveExtensions(ctx, input)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if options.RequireExecutablePolicies {
		if err := validateExecutablePolicies(manifest, interfaceResolution, resolution.Context(), sourceContext); err != nil {
			return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, err)
		}
	}
	configs, err := configurationresolve.Resolve(manifest, inventory, resolution.Context())
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: %w", ErrResolve, err)
	}
	evidenceModules, err := resolutionEvidenceModules(module, dependencies)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
	}
	configurationEvidence, err := resolutionEvidenceConfigurationInput(selector, composition, rootManifest, baseLayer, selectedLayer, schemaLookup)
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
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
		return dataPartial(), fmt.Errorf("%w: construct resolution evidence: %w", ErrResolve, err)
	}
	evidence, err = resolutionevidence.WithResources(evidence, interfaceResolution.Graph(), declarations.Resources())
	if err != nil {
		return dataPartial(), fmt.Errorf("%w: construct Resource resolution evidence: %w", ErrResolve, err)
	}
	if err := inputs.ValidateSnapshot(ctx); err != nil {
		return dataPartial(), err
	}
	result := Result{
		module:              module,
		currentManifest:     currentManifest,
		composition:         composition,
		dependencies:        dependencies,
		interfaces:          interfaces,
		resources:           declarations.Resources(),
		resourceProviders:   declarations.ResourceProviders(),
		implementations:     implementations,
		interfaceResolution: interfaceResolution,
		inventory:           inventory,
		resolution:          resolution,
		configs:             configs,
		evidence:            evidence,
		selection: ConfigurationSelection{
			mode:        selector.mode,
			path:        selector.path,
			environment: selector.environment,
			digest:      selectedDigest,
		},
		rootData:                 append([]byte(nil), rootData...),
		rootDigest:               rootDigest,
		configurationSource:      configurationSnapshot.Data(),
		dataCompiler:             dataRun.artifact,
		dataCompilerManifest:     dataRun.manifest,
		dataCompilerStatus:       dataRun.status,
		dataAnalysis:             dataAnalysis,
		dataActivation:           dataActivation,
		dataAnalyzeOutput:        append(json.RawMessage(nil), dataRun.response.Output...),
		dataCompilerObservations: cloneDataCompilerObservations(dataRun.observations),
	}
	if dataActivation.Valid() {
		if err := validateDataBackend(interfaceResolution.Graph(), declarations.Resources(), dataActivation); err != nil {
			return result, fmt.Errorf("%w: %w", ErrResolve, &DataAssignmentError{member: dataMembers[0], cause: err})
		}
		result.dataActivation = dataActivation.withBackend(dataPostgresBackend)
	}
	return result, nil
}

func currentProjectConfigurationPaths(base, selected applicationmeta.Manifest, environment bool, schemas applicationmeta.SchemaLookup) ([]string, error) {
	paths := make([]string, 0)
	for _, layer := range []applicationmeta.Manifest{base} {
		decisions, err := applicationmeta.ConfigurationDecisions(layer, schemas)
		if err != nil {
			return nil, err
		}
		for _, decision := range decisions {
			if decision.ResolutionRelevant() {
				paths = append(paths, decision.Path())
			}
		}
	}
	if environment {
		decisions, err := applicationmeta.ConfigurationDecisions(selected, schemas)
		if err != nil {
			return nil, err
		}
		for _, decision := range decisions {
			if decision.ResolutionRelevant() {
				paths = append(paths, decision.Path())
			}
		}
	}
	return uniqueSortedStrings(paths), nil
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
	return applicationinput.SourceContext{
		CurrentModulePath:   module.ModulePath(),
		Dependencies:        values,
		CurrentProjectPaths: uniqueSortedStrings(currentProjectPaths),
	}
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
	root applicationmeta.Manifest,
	maintained applicationmeta.Manifest,
	selected applicationmeta.Manifest,
	schemas applicationmeta.SchemaLookup,
) (resolutionevidence.ConfigurationInput, error) {
	currentDecisions := func(manifest applicationmeta.Manifest) ([]applicationmeta.ConfigurationDecision, error) {
		decisions, err := applicationmeta.ConfigurationDecisions(manifest, schemas)
		if err != nil {
			return nil, err
		}
		return decisions, nil
	}

	base, err := currentDecisions(maintained)
	if err != nil {
		return resolutionevidence.ConfigurationInput{}, err
	}
	layers := make([]resolutionevidence.ConfigurationLayerInput, 0, 2)
	switch selector.mode {
	case configurationModeDefault:
		layers = append(layers, resolutionevidence.ConfigurationLayerInput{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: base})
	case configurationModeEnvironment:
		overlay, err := currentDecisions(selected)
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
		Layers:    layers,
		Effective: effective,
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
