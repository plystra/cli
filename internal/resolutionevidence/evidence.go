// Package resolutionevidence derives immutable diagnostic evidence from the
// same normalized application model used by generation and assembly.
package resolutionevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/pluginscan"
	"github.com/plystra/cli/internal/providerresolution"
	gomodule "golang.org/x/mod/module"
)

const schemaVersion = 1

// ErrBuild reports an absent or internally inconsistent normalized model.
var ErrBuild = errors.New("build resolution evidence")

// ModuleRole distinguishes the selected current Project from dependency
// Projects participating through the effective Go Module graph.
type ModuleRole string

const (
	// ModuleRoleCurrent identifies the selected current Plystra Project.
	ModuleRoleCurrent ModuleRole = "current"
	// ModuleRoleDependency identifies one dependency Plystra Project.
	ModuleRoleDependency ModuleRole = "dependency"
)

// ReplacementKind identifies stable Go Module replacement provenance without
// exposing a local filesystem path.
type ReplacementKind string

const (
	// ReplacementModule identifies a replacement resolved by module version.
	ReplacementModule ReplacementKind = "module"
	// ReplacementLocal identifies a local filesystem replacement by the stable
	// module directive at its selected source root.
	ReplacementLocal ReplacementKind = "local"
)

// PluginSelectionReasonKind identifies why one discovered Plugin entered the
// final normalized application model.
type PluginSelectionReasonKind string

const (
	// PluginSelectionCurrentProject identifies a root-level Plugin in the
	// selected current Project, which is included by definition.
	PluginSelectionCurrentProject PluginSelectionReasonKind = "current-project"
	// PluginSelectionProvider identifies a Plugin selected to provide one exact
	// required ordinary Capability.
	PluginSelectionProvider PluginSelectionReasonKind = "provider"
)

// ProviderRejectionReason identifies why one valid visible Provider
// declaration did not become the selected Provider in the successful final
// application model.
type ProviderRejectionReason string

const (
	// ProviderRejectionCapabilityNotRequired identifies a visible Provider for
	// an exact Capability outside the final requirement closure.
	ProviderRejectionCapabilityNotRequired ProviderRejectionReason = "capability-not-required"
	// ProviderRejectionAnotherProviderSelected identifies a compatible
	// candidate rejected because the application's explicit Provider choice
	// selected another candidate for the same required Capability.
	ProviderRejectionAnotherProviderSelected ProviderRejectionReason = "another-provider-selected"
)

// ProviderSelectionReason identifies the successful rule that selected one
// implementation for an exact required canonical Capability.
type ProviderSelectionReason string

const (
	// ProviderSelectionSoleProvider identifies automatic selection of the only
	// compatible visible ordinary Provider.
	ProviderSelectionSoleProvider ProviderSelectionReason = "sole-provider"
	// ProviderSelectionCurrentProject identifies an explicit replacement in the
	// selected current-Project configuration layer.
	ProviderSelectionCurrentProject ProviderSelectionReason = "current-project-replacement"
	// ProviderSelectionInherited identifies one compatible Provider choice
	// inherited from one or more dependency Projects.
	ProviderSelectionInherited ProviderSelectionReason = "inherited-selection"
	// ProviderSelectionIntrinsic identifies a Kernel-owned intrinsic
	// implementation outside ordinary Plugin Provider selection.
	ProviderSelectionIntrinsic ProviderSelectionReason = "intrinsic-kernel"
)

// Input is the construction-only selected-model, participating-Project, and
// discovered-Plugin input for one evidence document.
type Input struct {
	Context            generation.Context
	ProviderResolution providerresolution.Result
	Modules            []ModuleInput
	PluginCandidates   []PluginCandidateInput
}

// ModuleInput identifies one participating Plystra Project without carrying
// its absolute source root or unrestricted source contents.
type ModuleInput struct {
	Path             string
	Role             ModuleRole
	RequiredVersion  string
	SelectedVersion  string
	Direct           bool
	Indirect         bool
	Workspace        bool
	SourceModulePath string
	Replacement      *ReplacementInput
}

// PluginCandidateInput identifies one discovered root-level Plugin without
// carrying its absolute source root or unrestricted manifest contents.
type PluginCandidateInput struct {
	ID         string
	ModulePath string
	Path       string
}

// ReplacementInput is one construction-only stable replacement identity.
type ReplacementInput struct {
	Kind       ReplacementKind
	ModulePath string
	Version    string
}

// Evidence is one immutable deterministic identity for a selected normalized
// application model. Detailed decision and declaration records are added by
// their owning resolution boundaries rather than inferred here.
type Evidence struct {
	generationAPI            string
	selectedModelDigest      string
	buildModelDigest         string
	canonicalCapabilityCount int
	requirementCount         int
	selectedProviderCount    int
	capabilityAliasCount     int
	modules                  []Module
	pluginCandidates         []PluginCandidate
	selectedPlugins          []SelectedPlugin
	requirements             []CapabilityRequirement
	providerCandidates       []ProviderCandidate
	selectedProviders        []SelectedProvider
	generationActivations    []GenerationActivation
	generatedRequirements    []GeneratedRequirement
	canonicalJSON            []byte
	digest                   string
	prepared                 bool
}

type canonicalCounts struct {
	ParticipatingModules  int `json:"participating_modules"`
	DiscoveredPlugins     int `json:"discovered_plugins"`
	SelectedPlugins       int `json:"selected_plugins"`
	CanonicalCapabilities int `json:"canonical_capabilities"`
	Requirements          int `json:"requirements"`
	ProviderCandidates    int `json:"provider_candidates"`
	RejectedProviders     int `json:"rejected_providers"`
	SelectedProviders     int `json:"selected_providers"`
	GenerationActivations int `json:"generation_activations"`
	GeneratedRequirements int `json:"generated_requirements"`
	CapabilityAliases     int `json:"capability_aliases"`
}

// Module is one immutable participating Plystra Project identity.
type Module struct {
	path            string
	role            ModuleRole
	requiredVersion string
	selectedVersion string
	direct          bool
	indirect        bool
	workspace       bool
	source          Source
	replacement     Replacement
	hasReplacement  bool
}

// Path returns the effective graph module path.
func (m Module) Path() string { return m.path }

// Role returns current or dependency.
func (m Module) Role() ModuleRole { return m.role }

// RequiredVersion returns the direct go.mod requirement, when present.
func (m Module) RequiredVersion() string { return m.requiredVersion }

// SelectedVersion returns the version selected by Go, or an empty string for
// the current Project and workspace-supplied dependency Projects.
func (m Module) SelectedVersion() string { return m.selectedVersion }

// Direct reports whether the current Project directly requires this dependency.
func (m Module) Direct() bool { return m.direct }

// Indirect reports whether a direct requirement carries Go's indirect marker.
func (m Module) Indirect() bool { return m.indirect }

// Workspace reports whether the active go.work supplies this dependency.
func (m Module) Workspace() bool { return m.workspace }

// Source returns the stable root Project-marker provenance.
func (m Module) Source() Source { return m.source }

// Replacement returns stable replacement provenance, when present.
func (m Module) Replacement() (Replacement, bool) { return m.replacement, m.hasReplacement }

// PluginCandidate is one immutable visible Plugin declaration from the
// current Project or a dependency Project in the effective Go Module graph.
type PluginCandidate struct {
	id         string
	modulePath string
	moduleRole ModuleRole
	path       string
	source     Source
}

// ID returns the exact canonical Plugin ID.
func (p PluginCandidate) ID() string { return p.id }

// ModulePath returns the effective graph module containing the Plugin.
func (p PluginCandidate) ModulePath() string { return p.modulePath }

// ModuleRole returns whether the declaration belongs to the current or a
// dependency Project.
func (p PluginCandidate) ModuleRole() ModuleRole { return p.moduleRole }

// Path returns the slash-separated module-relative Plugin directory.
func (p PluginCandidate) Path() string { return p.path }

// Local reports whether the declaration belongs to the current Project.
func (p PluginCandidate) Local() bool { return p.moduleRole == ModuleRoleCurrent }

// Source returns the stable Plugin declaration provenance.
func (p PluginCandidate) Source() Source { return p.source }

// SelectedPlugin is one immutable discovered candidate that entered the final
// normalized application model.
type SelectedPlugin struct {
	id            string
	modulePath    string
	moduleVersion string
	moduleRole    ModuleRole
	path          string
	source        Source
	reasons       []PluginSelectionReason
}

// ID returns the exact canonical Plugin ID.
func (p SelectedPlugin) ID() string { return p.id }

// ModulePath returns the effective graph module containing the Plugin.
func (p SelectedPlugin) ModulePath() string { return p.modulePath }

// ModuleVersion returns the selected graph version, or an empty string for a
// current-Project or workspace Plugin.
func (p SelectedPlugin) ModuleVersion() string { return p.moduleVersion }

// ModuleRole returns whether the Plugin belongs to the current or a dependency
// Project.
func (p SelectedPlugin) ModuleRole() ModuleRole { return p.moduleRole }

// Path returns the slash-separated module-relative Plugin directory.
func (p SelectedPlugin) Path() string { return p.path }

// Local reports whether the Plugin belongs to the current Project.
func (p SelectedPlugin) Local() bool { return p.moduleRole == ModuleRoleCurrent }

// Source returns the stable Plugin declaration provenance.
func (p SelectedPlugin) Source() Source { return p.source }

// Reasons returns every deterministic reason this Plugin entered the model.
func (p SelectedPlugin) Reasons() []PluginSelectionReason {
	return append([]PluginSelectionReason(nil), p.reasons...)
}

// PluginSelectionReason identifies current-Project inclusion or one exact
// ordinary Capability for which the Plugin is the selected Provider.
type PluginSelectionReason struct {
	kind       PluginSelectionReasonKind
	capability string
}

// Kind returns current-project or provider.
func (r PluginSelectionReason) Kind() PluginSelectionReasonKind { return r.kind }

// Capability returns the exact canonical Capability for a provider reason and
// an empty string for current-Project inclusion.
func (r PluginSelectionReason) Capability() string { return r.capability }

// CapabilityRequirement is one final exact canonical requirement with its
// contract identity, intrinsic ownership, and every typed introducing edge.
type CapabilityRequirement struct {
	capability     string
	contractDigest string
	intrinsic      bool
	sources        []RequirementSource
}

// Capability returns the exact canonical Capability ID.
func (r CapabilityRequirement) Capability() string { return r.capability }

// ContractDigest returns the normalized exact contract identity.
func (r CapabilityRequirement) ContractDigest() string { return r.contractDigest }

// Intrinsic reports whether the Kernel owns this required Capability.
func (r CapabilityRequirement) Intrinsic() bool { return r.intrinsic }

// Sources returns every deterministic introducing edge in canonical order.
func (r CapabilityRequirement) Sources() []RequirementSource {
	return append([]RequirementSource(nil), r.sources...)
}

// RequirementSource is one typed stable source for a canonical requirement.
type RequirementSource struct {
	kind             providerresolution.RequirementSourceKind
	projectModule    string
	source           Source
	pluginID         string
	alias            string
	namespace        string
	sourceCapability string
	ruleID           string
}

// Kind returns declaration, exposure, generated-client, plugin, alias-target,
// activation, or generation-rule.
func (s RequirementSource) Kind() providerresolution.RequirementSourceKind { return s.kind }

// ProjectModule returns the effective graph module that owns the source.
func (s RequirementSource) ProjectModule() string { return s.projectModule }

// Source returns the stable module-relative location for this edge.
func (s RequirementSource) Source() Source { return s.source }

// PluginID returns the requiring or generating Plugin where applicable.
func (s RequirementSource) PluginID() string { return s.pluginID }

// Alias returns the application Alias ID for an Alias-target edge.
func (s RequirementSource) Alias() string { return s.alias }

// Namespace returns the generation namespace for activation and rule edges.
func (s RequirementSource) Namespace() string { return s.namespace }

// SourceCapability returns the metadata-bearing or rule-source Capability.
func (s RequirementSource) SourceCapability() string { return s.sourceCapability }

// RuleID returns the selected generation rule ID where applicable.
func (s RequirementSource) RuleID() string { return s.ruleID }

// ProviderCandidate is one exact ordinary Provider declaration from the
// complete visible catalog. A zero rejection reason identifies the selected
// candidate; every other candidate carries one stable rejection reason.
type ProviderCandidate struct {
	capability      string
	pluginID        string
	projectModule   string
	contractDigest  string
	source          Source
	rejectionReason ProviderRejectionReason
}

// Capability returns the exact canonical Capability ID.
func (c ProviderCandidate) Capability() string { return c.capability }

// PluginID returns the canonical Plugin declaring this Provider.
func (c ProviderCandidate) PluginID() string { return c.pluginID }

// ProjectModule returns the effective graph module containing the Plugin.
func (c ProviderCandidate) ProjectModule() string { return c.projectModule }

// ContractDigest returns the normalized exact Provider contract identity.
func (c ProviderCandidate) ContractDigest() string { return c.contractDigest }

// Source returns stable replacement-safe Capability declaration provenance.
func (c ProviderCandidate) Source() Source { return c.source }

// Rejected reports whether this candidate was not selected in the successful
// final application model.
func (c ProviderCandidate) Rejected() bool { return c.rejectionReason != "" }

// RejectionReason returns the stable reason for a rejected candidate and an
// empty value for the selected candidate.
func (c ProviderCandidate) RejectionReason() ProviderRejectionReason {
	return c.rejectionReason
}

// SelectedProvider is one successful implementation decision for an exact
// required canonical Capability. Intrinsic Kernel decisions have no Plugin or
// participating Project module.
type SelectedProvider struct {
	capability       string
	pluginID         string
	projectModule    string
	contractDigest   string
	providerSource   Source
	selectionReason  ProviderSelectionReason
	selectionSources []ProviderSelectionSource
}

// Capability returns the exact required canonical Capability ID.
func (p SelectedProvider) Capability() string { return p.capability }

// PluginID returns the selected ordinary Plugin ID, or an empty string for an
// intrinsic Kernel implementation.
func (p SelectedProvider) PluginID() string { return p.pluginID }

// ProjectModule returns the effective graph Project module containing the
// ordinary Provider, or an empty string for an intrinsic implementation.
func (p SelectedProvider) ProjectModule() string { return p.projectModule }

// ContractDigest returns the normalized exact contract identity.
func (p SelectedProvider) ContractDigest() string { return p.contractDigest }

// ProviderSource returns the stable ordinary Provider declaration or
// intrinsic Kernel catalog location.
func (p SelectedProvider) ProviderSource() Source { return p.providerSource }

// SelectionReason returns the closed successful selection rule.
func (p SelectedProvider) SelectionReason() ProviderSelectionReason {
	return p.selectionReason
}

// SelectionSources returns the winning current-Project declaration or every
// compatible inherited dependency declaration. Automatic and intrinsic
// decisions return nil.
func (p SelectedProvider) SelectionSources() []ProviderSelectionSource {
	return append([]ProviderSelectionSource(nil), p.selectionSources...)
}

// Intrinsic reports whether the Kernel owns this selected implementation.
func (p SelectedProvider) Intrinsic() bool {
	return p.selectionReason == ProviderSelectionIntrinsic
}

// ProviderSelectionSource is one typed stable configuration source for an
// explicit ordinary Provider decision.
type ProviderSelectionSource struct {
	projectModule string
	source        Source
}

// ProjectModule returns the effective graph Project module that owns the
// configuration declaration.
func (s ProviderSelectionSource) ProjectModule() string { return s.projectModule }

// Source returns the replacement-safe module-relative configuration location.
func (s ProviderSelectionSource) Source() Source { return s.source }

// GenerationActivation is one selected extension-namespace edge from an
// originating required Capability through an ordinary activation Capability.
type GenerationActivation struct {
	namespace            string
	sourceCapability     string
	activationCapability string
	pluginID             string
	projectModule        string
	causes               []GenerationActivationCause
}

// Namespace returns the exact extension namespace.
func (a GenerationActivation) Namespace() string { return a.namespace }

// SourceCapability returns the required Capability whose contract activated
// the namespace.
func (a GenerationActivation) SourceCapability() string { return a.sourceCapability }

// ActivationCapability returns the ordinary Capability whose selected
// Provider owns the extension.
func (a GenerationActivation) ActivationCapability() string { return a.activationCapability }

// PluginID returns the selected activation Provider and extension owner.
func (a GenerationActivation) PluginID() string { return a.pluginID }

// ProjectModule returns the participating Project that supplies the selected
// extension Plugin.
func (a GenerationActivation) ProjectModule() string { return a.projectModule }

// Causes returns every stable declaration location that introduced the source
// Capability and therefore the activation edge.
func (a GenerationActivation) Causes() []GenerationActivationCause {
	return append([]GenerationActivationCause(nil), a.causes...)
}

// GenerationActivationCause is one stable Project-owned source for an
// activation edge.
type GenerationActivationCause struct {
	projectModule string
	source        Source
}

// ProjectModule returns the participating Project that owns the source.
func (c GenerationActivationCause) ProjectModule() string { return c.projectModule }

// Source returns the replacement-safe module-relative activation location.
func (c GenerationActivationCause) Source() Source { return c.source }

// GeneratedRequirement is one selected extension rule edge from an originating
// Capability through its activation to one exact generated requirement.
type GeneratedRequirement struct {
	capability           string
	sourceCapability     string
	activationCapability string
	namespace            string
	pluginID             string
	projectModule        string
	ruleID               string
	source               Source
}

// Capability returns the exact generated canonical requirement.
func (r GeneratedRequirement) Capability() string { return r.capability }

// SourceCapability returns the required Capability whose metadata matched the
// selected extension rule.
func (r GeneratedRequirement) SourceCapability() string { return r.sourceCapability }

// ActivationCapability returns the ordinary Capability that selected the
// extension owner.
func (r GeneratedRequirement) ActivationCapability() string { return r.activationCapability }

// Namespace returns the interpreted extension namespace.
func (r GeneratedRequirement) Namespace() string { return r.namespace }

// PluginID returns the selected extension owner.
func (r GeneratedRequirement) PluginID() string { return r.pluginID }

// ProjectModule returns the participating Project that supplies the extension.
func (r GeneratedRequirement) ProjectModule() string { return r.projectModule }

// RuleID returns the stable extension rule identity.
func (r GeneratedRequirement) RuleID() string { return r.ruleID }

// Source returns the replacement-safe generation declaration location.
func (r GeneratedRequirement) Source() Source { return r.source }

// Source is one stable module-relative declaration reference.
type Source struct {
	module string
	path   string
	kind   string
	line   int
	column int
}

// Module returns the source Go Module identity.
func (s Source) Module() string { return s.module }

// Path returns the source module-relative slash path.
func (s Source) Path() string { return s.path }

// Kind returns the closed declaration kind.
func (s Source) Kind() string { return s.kind }

// Line returns the one-based source line.
func (s Source) Line() int { return s.line }

// Column returns the one-based source column.
func (s Source) Column() int { return s.column }

// Replacement is one immutable module-version or local-source identity.
type Replacement struct {
	kind       ReplacementKind
	modulePath string
	version    string
}

// Kind returns module or local.
func (r Replacement) Kind() ReplacementKind { return r.kind }

// ModulePath returns the stable selected replacement module identity.
func (r Replacement) ModulePath() string { return r.modulePath }

// Version returns the selected replacement version for module replacements.
func (r Replacement) Version() string { return r.version }

type canonicalModule struct {
	Path            string                `json:"path"`
	Role            ModuleRole            `json:"role"`
	RequiredVersion string                `json:"required_version,omitempty"`
	SelectedVersion string                `json:"selected_version,omitempty"`
	Direct          bool                  `json:"direct"`
	Indirect        bool                  `json:"indirect"`
	Workspace       bool                  `json:"workspace"`
	Replacement     *canonicalReplacement `json:"replacement,omitempty"`
	Source          canonicalSource       `json:"source"`
}

type canonicalPluginCandidate struct {
	ID         string          `json:"id"`
	ModulePath string          `json:"module_path"`
	ModuleRole ModuleRole      `json:"module_role"`
	Path       string          `json:"path"`
	Source     canonicalSource `json:"source"`
}

type canonicalSelectedPlugin struct {
	ID            string                           `json:"id"`
	ModulePath    string                           `json:"module_path"`
	ModuleVersion string                           `json:"module_version,omitempty"`
	ModuleRole    ModuleRole                       `json:"module_role"`
	Path          string                           `json:"path"`
	Source        canonicalSource                  `json:"source"`
	Reasons       []canonicalPluginSelectionReason `json:"reasons"`
}

type canonicalPluginSelectionReason struct {
	Kind       PluginSelectionReasonKind `json:"kind"`
	Capability string                    `json:"capability,omitempty"`
}

type canonicalCapabilityRequirement struct {
	Capability     string                       `json:"capability"`
	ContractDigest string                       `json:"contract_digest"`
	Intrinsic      bool                         `json:"intrinsic"`
	Sources        []canonicalRequirementSource `json:"sources"`
}

type canonicalRequirementSource struct {
	Kind             providerresolution.RequirementSourceKind `json:"kind"`
	ProjectModule    string                                   `json:"project_module"`
	Source           canonicalSource                          `json:"source"`
	PluginID         string                                   `json:"plugin_id,omitempty"`
	Alias            string                                   `json:"alias,omitempty"`
	Namespace        string                                   `json:"namespace,omitempty"`
	SourceCapability string                                   `json:"source_capability,omitempty"`
	RuleID           string                                   `json:"rule_id,omitempty"`
}

type canonicalProviderCandidate struct {
	Capability      string                  `json:"capability"`
	PluginID        string                  `json:"plugin_id"`
	ProjectModule   string                  `json:"project_module"`
	ContractDigest  string                  `json:"contract_digest"`
	Source          canonicalSource         `json:"source"`
	RejectionReason ProviderRejectionReason `json:"rejection_reason,omitempty"`
}

type canonicalSelectedProvider struct {
	Capability       string                             `json:"capability"`
	PluginID         string                             `json:"plugin_id,omitempty"`
	ProjectModule    string                             `json:"project_module,omitempty"`
	ContractDigest   string                             `json:"contract_digest"`
	ProviderSource   canonicalSource                    `json:"provider_source"`
	SelectionReason  ProviderSelectionReason            `json:"selection_reason"`
	SelectionSources []canonicalProviderSelectionSource `json:"selection_sources"`
}

type canonicalProviderSelectionSource struct {
	ProjectModule string          `json:"project_module"`
	Source        canonicalSource `json:"source"`
}

type canonicalGenerationActivation struct {
	Namespace            string                               `json:"namespace"`
	SourceCapability     string                               `json:"source_capability"`
	ActivationCapability string                               `json:"activation_capability"`
	PluginID             string                               `json:"plugin_id"`
	ProjectModule        string                               `json:"project_module"`
	Causes               []canonicalGenerationActivationCause `json:"causes"`
}

type canonicalGenerationActivationCause struct {
	ProjectModule string          `json:"project_module"`
	Source        canonicalSource `json:"source"`
}

type canonicalGeneratedRequirement struct {
	Capability           string          `json:"capability"`
	SourceCapability     string          `json:"source_capability"`
	ActivationCapability string          `json:"activation_capability"`
	Namespace            string          `json:"namespace"`
	PluginID             string          `json:"plugin_id"`
	ProjectModule        string          `json:"project_module"`
	RuleID               string          `json:"rule_id"`
	Source               canonicalSource `json:"source"`
}

type canonicalReplacement struct {
	Kind       ReplacementKind `json:"kind"`
	ModulePath string          `json:"module_path"`
	Version    string          `json:"version,omitempty"`
}

type canonicalSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type canonicalEvidence struct {
	Version               int                              `json:"version"`
	GenerationAPI         string                           `json:"generation_api"`
	SelectedModelDigest   string                           `json:"selected_model_digest"`
	BuildModelDigest      string                           `json:"build_model_digest"`
	Modules               []canonicalModule                `json:"modules"`
	PluginCandidates      []canonicalPluginCandidate       `json:"plugin_candidates"`
	SelectedPlugins       []canonicalSelectedPlugin        `json:"selected_plugins"`
	Requirements          []canonicalCapabilityRequirement `json:"requirements"`
	ProviderCandidates    []canonicalProviderCandidate     `json:"provider_candidates"`
	SelectedProviders     []canonicalSelectedProvider      `json:"selected_providers"`
	GenerationActivations []canonicalGenerationActivation  `json:"generation_activations"`
	GeneratedRequirements []canonicalGeneratedRequirement  `json:"generated_requirements"`
	Counts                canonicalCounts                  `json:"counts"`
}

// Build validates one constructor-produced generation context and derives its
// bounded evidence identity without copying contracts, metadata,
// machine-specific source paths, configuration values, or Secret references
// into the evidence document.
func Build(source Input) (Evidence, error) {
	context := source.Context
	canonicalModel := context.CanonicalJSON()
	if len(canonicalModel) == 0 || !json.Valid(canonicalModel) || digest(canonicalModel) != context.Digest() {
		return Evidence{}, fmt.Errorf("%w: normalized application context is absent or has an invalid digest", ErrBuild)
	}
	modules, err := normalizeModules(source.Modules)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: participating Projects: %v", ErrBuild, err)
	}
	pluginCandidates, err := normalizePluginCandidates(source.PluginCandidates, modules)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: discovered Plugin candidates: %v", ErrBuild, err)
	}
	selectedPlugins, err := selectedPluginsFromContext(context, pluginCandidates, modules)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: selected Plugins: %v", ErrBuild, err)
	}
	requirements, err := capabilityRequirementsFromResolution(source.ProviderResolution, context, modules, pluginCandidates)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: canonical Capability requirements: %v", ErrBuild, err)
	}
	providerCandidates, err := providerCandidatesFromResolution(source.ProviderResolution, context, modules, pluginCandidates, selectedPlugins, requirements)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: Provider candidates: %v", ErrBuild, err)
	}
	selectedProviders, err := selectedProvidersFromResolution(source.ProviderResolution, modules, providerCandidates, requirements)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: selected Providers: %v", ErrBuild, err)
	}
	generationActivations, generatedRequirements, err := generationEvidenceFromRequirements(requirements, selectedProviders, pluginCandidates)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: generation provenance: %v", ErrBuild, err)
	}
	input := Evidence{
		generationAPI:            context.APIVersion(),
		selectedModelDigest:      context.Digest(),
		buildModelDigest:         context.BuildModelDigest(),
		canonicalCapabilityCount: len(context.Capabilities()),
		requirementCount:         len(requirements),
		selectedProviderCount:    len(selectedProviders),
		capabilityAliasCount:     len(context.CapabilityAliases()),
		modules:                  modules,
		pluginCandidates:         pluginCandidates,
		selectedPlugins:          selectedPlugins,
		requirements:             requirements,
		providerCandidates:       providerCandidates,
		selectedProviders:        selectedProviders,
		generationActivations:    generationActivations,
		generatedRequirements:    generatedRequirements,
		prepared:                 true,
	}
	if err := validate(input); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrBuild, err)
	}
	canonical, err := encode(input)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: encode canonical evidence: %v", ErrBuild, err)
	}
	input.canonicalJSON = canonical
	input.digest = digest(canonical)
	return input, nil
}

// Valid reports whether Build produced this internally consistent evidence.
func (e Evidence) Valid() bool {
	if !e.prepared || validate(e) != nil {
		return false
	}
	modules, err := normalizeModules(e.moduleInputs())
	if err != nil || !equalModules(e.modules, modules) {
		return false
	}
	pluginCandidates, err := normalizePluginCandidates(e.pluginCandidateInputs(), modules)
	if err != nil || !equalPluginCandidates(e.pluginCandidates, pluginCandidates) {
		return false
	}
	selectedPlugins, err := normalizeSelectedPlugins(e.selectedPluginInputs(), pluginCandidates, modules)
	if err != nil || !equalSelectedPlugins(e.selectedPlugins, selectedPlugins) {
		return false
	}
	if err := validateCapabilityRequirements(e.requirements, modules, pluginCandidates); err != nil {
		return false
	}
	if err := validateProviderCandidates(e.providerCandidates, modules, pluginCandidates, e.requirements, e.selectedPlugins); err != nil {
		return false
	}
	if err := validateSelectedProviders(e.selectedProviders, modules, e.providerCandidates, e.requirements); err != nil {
		return false
	}
	canonical, err := encode(e)
	return err == nil && bytes.Equal(e.canonicalJSON, canonical) && e.digest == digest(canonical)
}

// SchemaVersion returns the internal resolution-evidence schema version.
func (Evidence) SchemaVersion() int { return schemaVersion }

// GenerationAPIVersion returns the normalized generation-context API version.
func (e Evidence) GenerationAPIVersion() string { return e.generationAPI }

// SelectedModelDigest returns the identity of the normalized model including
// stable selected-configuration provenance.
func (e Evidence) SelectedModelDigest() string { return e.selectedModelDigest }

// BuildModelDigest returns the identity of normalized build state excluding
// configuration-document provenance.
func (e Evidence) BuildModelDigest() string { return e.buildModelDigest }

// Modules returns the current Project followed by dependency Projects in
// module-path order.
func (e Evidence) Modules() []Module { return append([]Module(nil), e.modules...) }

// ParticipatingModuleCount returns the current plus dependency Project count.
func (e Evidence) ParticipatingModuleCount() int { return len(e.modules) }

// PluginCandidates returns every discovered current- and dependency-Project
// Plugin declaration in canonical Plugin-ID order.
func (e Evidence) PluginCandidates() []PluginCandidate {
	return append([]PluginCandidate(nil), e.pluginCandidates...)
}

// DiscoveredPluginCount returns the complete visible Plugin candidate count.
func (e Evidence) DiscoveredPluginCount() int { return len(e.pluginCandidates) }

// SelectedPlugins returns every Plugin in the final normalized application
// model in canonical Plugin-ID order.
func (e Evidence) SelectedPlugins() []SelectedPlugin {
	plugins := append([]SelectedPlugin(nil), e.selectedPlugins...)
	for index := range plugins {
		plugins[index].reasons = append([]PluginSelectionReason(nil), plugins[index].reasons...)
	}
	return plugins
}

// SelectedPluginCount returns the number of selected Plugins.
func (e Evidence) SelectedPluginCount() int { return len(e.selectedPlugins) }

// Requirements returns every final canonical requirement in Capability order.
func (e Evidence) Requirements() []CapabilityRequirement {
	values := append([]CapabilityRequirement(nil), e.requirements...)
	for index := range values {
		values[index].sources = append([]RequirementSource(nil), values[index].sources...)
	}
	return values
}

// ProviderCandidates returns every visible ordinary Provider declaration in
// canonical Capability and Plugin order. Selected candidates have no rejection
// reason; every rejected candidate carries one stable reason.
func (e Evidence) ProviderCandidates() []ProviderCandidate {
	return append([]ProviderCandidate(nil), e.providerCandidates...)
}

// SelectedProviders returns one implementation decision for every final
// canonical requirement in Capability order, including intrinsic Kernel
// implementations.
func (e Evidence) SelectedProviders() []SelectedProvider {
	values := append([]SelectedProvider(nil), e.selectedProviders...)
	for index := range values {
		values[index].selectionSources = append([]ProviderSelectionSource(nil), values[index].selectionSources...)
	}
	return values
}

// GenerationActivations returns every selected extension namespace edge in
// canonical order with defensive cause storage.
func (e Evidence) GenerationActivations() []GenerationActivation {
	values := append([]GenerationActivation(nil), e.generationActivations...)
	for index := range values {
		values[index].causes = append([]GenerationActivationCause(nil), values[index].causes...)
	}
	return values
}

// GeneratedRequirements returns every selected extension rule edge in
// canonical order.
func (e Evidence) GeneratedRequirements() []GeneratedRequirement {
	return append([]GeneratedRequirement(nil), e.generatedRequirements...)
}

// ProviderCandidateCount returns the complete visible Provider declaration
// count, including Capabilities outside the final requirement closure.
func (e Evidence) ProviderCandidateCount() int { return len(e.providerCandidates) }

// RejectedProviderCount returns the number of candidates that did not become
// the selected Provider in the final application model.
func (e Evidence) RejectedProviderCount() int {
	count := 0
	for _, candidate := range e.providerCandidates {
		if candidate.Rejected() {
			count++
		}
	}
	return count
}

// CanonicalCapabilityCount returns the number of resolved canonical contracts.
func (e Evidence) CanonicalCapabilityCount() int { return e.canonicalCapabilityCount }

// RequirementCount returns the number of required canonical Capabilities.
func (e Evidence) RequirementCount() int { return e.requirementCount }

// SelectedProviderCount returns the number of successful implementation
// decisions, including intrinsic Kernel implementations.
func (e Evidence) SelectedProviderCount() int { return e.selectedProviderCount }

// GenerationActivationCount returns the selected namespace activation edge
// count.
func (e Evidence) GenerationActivationCount() int { return len(e.generationActivations) }

// GeneratedRequirementCount returns the selected extension rule edge count.
func (e Evidence) GeneratedRequirementCount() int { return len(e.generatedRequirements) }

// CapabilityAliasCount returns the number of final application Aliases.
func (e Evidence) CapabilityAliasCount() int { return e.capabilityAliasCount }

// CanonicalJSON returns a defensive copy of the deterministic bounded evidence.
func (e Evidence) CanonicalJSON() []byte { return append([]byte(nil), e.canonicalJSON...) }

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (e Evidence) Digest() string { return e.digest }

func validate(e Evidence) error {
	if e.generationAPI != generation.Version {
		return fmt.Errorf("generation API must be %q", generation.Version)
	}
	if !validDigest(e.selectedModelDigest) {
		return errors.New("selected-model digest is not a canonical SHA-256 digest")
	}
	if !validDigest(e.buildModelDigest) {
		return errors.New("build-model digest is not a canonical SHA-256 digest")
	}
	if len(e.modules) == 0 {
		return errors.New("participating Projects must not be empty")
	}
	if len(e.selectedPlugins) > len(e.pluginCandidates) {
		return errors.New("selected Plugin count exceeds discovered Plugin candidate count")
	}
	if e.requirementCount != len(e.requirements) {
		return fmt.Errorf("requirement count %d does not match records %d", e.requirementCount, len(e.requirements))
	}
	if e.selectedProviderCount != len(e.selectedProviders) {
		return fmt.Errorf("selected Provider count %d does not match records %d", e.selectedProviderCount, len(e.selectedProviders))
	}
	counts := []struct {
		name  string
		value int
	}{
		{name: "canonical Capability", value: e.canonicalCapabilityCount},
		{name: "requirement", value: e.requirementCount},
		{name: "selected Provider", value: e.selectedProviderCount},
		{name: "Capability Alias", value: e.capabilityAliasCount},
	}
	for _, count := range counts {
		if count.value < 0 {
			return fmt.Errorf("%s count must not be negative", count.name)
		}
	}
	if e.requirementCount > e.canonicalCapabilityCount {
		return errors.New("requirement count exceeds canonical Capability count")
	}
	if e.selectedProviderCount != e.requirementCount {
		return errors.New("every requirement must have exactly one selected implementation")
	}
	providerReasons := 0
	for _, plugin := range e.selectedPlugins {
		for _, reason := range plugin.reasons {
			if reason.kind == PluginSelectionProvider {
				providerReasons++
			}
		}
	}
	ordinaryProviders := 0
	for _, provider := range e.selectedProviders {
		if !provider.Intrinsic() {
			ordinaryProviders++
		}
	}
	if providerReasons != ordinaryProviders {
		return fmt.Errorf("ordinary selected Provider count %d does not match selected Plugin provider reasons %d", ordinaryProviders, providerReasons)
	}
	if err := validateProviderCandidates(e.providerCandidates, e.modules, e.pluginCandidates, e.requirements, e.selectedPlugins); err != nil {
		return err
	}
	if err := validateSelectedProviders(e.selectedProviders, e.modules, e.providerCandidates, e.requirements); err != nil {
		return err
	}
	activations, generated, err := generationEvidenceFromRequirements(e.requirements, e.selectedProviders, e.pluginCandidates)
	if err != nil {
		return err
	}
	if !equalGenerationActivations(e.generationActivations, activations) {
		return errors.New("generation activation records do not match final requirement provenance")
	}
	if !equalGeneratedRequirements(e.generatedRequirements, generated) {
		return errors.New("generated requirement records do not match final requirement provenance")
	}
	return nil
}

func encode(e Evidence) ([]byte, error) {
	modules := make([]canonicalModule, len(e.modules))
	for index, value := range e.modules {
		var replacement *canonicalReplacement
		if value.hasReplacement {
			replacement = &canonicalReplacement{
				Kind:       value.replacement.kind,
				ModulePath: value.replacement.modulePath,
				Version:    value.replacement.version,
			}
		}
		modules[index] = canonicalModule{
			Path:            value.path,
			Role:            value.role,
			RequiredVersion: value.requiredVersion,
			SelectedVersion: value.selectedVersion,
			Direct:          value.direct,
			Indirect:        value.indirect,
			Workspace:       value.workspace,
			Replacement:     replacement,
			Source: canonicalSource{
				Module: value.source.module,
				Path:   value.source.path,
				Kind:   value.source.kind,
				Line:   value.source.line,
				Column: value.source.column,
			},
		}
	}
	pluginCandidates := make([]canonicalPluginCandidate, len(e.pluginCandidates))
	for index, value := range e.pluginCandidates {
		pluginCandidates[index] = canonicalPluginCandidate{
			ID:         value.id,
			ModulePath: value.modulePath,
			ModuleRole: value.moduleRole,
			Path:       value.path,
			Source: canonicalSource{
				Module: value.source.module,
				Path:   value.source.path,
				Kind:   value.source.kind,
				Line:   value.source.line,
				Column: value.source.column,
			},
		}
	}
	selectedPlugins := make([]canonicalSelectedPlugin, len(e.selectedPlugins))
	for index, value := range e.selectedPlugins {
		reasons := make([]canonicalPluginSelectionReason, len(value.reasons))
		for reasonIndex, reason := range value.reasons {
			reasons[reasonIndex] = canonicalPluginSelectionReason{
				Kind:       reason.kind,
				Capability: reason.capability,
			}
		}
		selectedPlugins[index] = canonicalSelectedPlugin{
			ID:            value.id,
			ModulePath:    value.modulePath,
			ModuleVersion: value.moduleVersion,
			ModuleRole:    value.moduleRole,
			Path:          value.path,
			Source: canonicalSource{
				Module: value.source.module,
				Path:   value.source.path,
				Kind:   value.source.kind,
				Line:   value.source.line,
				Column: value.source.column,
			},
			Reasons: reasons,
		}
	}
	requirements := make([]canonicalCapabilityRequirement, len(e.requirements))
	for index, value := range e.requirements {
		sources := make([]canonicalRequirementSource, len(value.sources))
		for sourceIndex, requirementSource := range value.sources {
			source := requirementSource.source
			sources[sourceIndex] = canonicalRequirementSource{
				Kind:          requirementSource.kind,
				ProjectModule: requirementSource.projectModule,
				Source: canonicalSource{
					Module: source.module,
					Path:   source.path,
					Kind:   source.kind,
					Line:   source.line,
					Column: source.column,
				},
				PluginID:         requirementSource.pluginID,
				Alias:            requirementSource.alias,
				Namespace:        requirementSource.namespace,
				SourceCapability: requirementSource.sourceCapability,
				RuleID:           requirementSource.ruleID,
			}
		}
		requirements[index] = canonicalCapabilityRequirement{
			Capability:     value.capability,
			ContractDigest: value.contractDigest,
			Intrinsic:      value.intrinsic,
			Sources:        sources,
		}
	}
	providerCandidates := make([]canonicalProviderCandidate, len(e.providerCandidates))
	for index, value := range e.providerCandidates {
		providerCandidates[index] = canonicalProviderCandidate{
			Capability:     value.capability,
			PluginID:       value.pluginID,
			ProjectModule:  value.projectModule,
			ContractDigest: value.contractDigest,
			Source: canonicalSource{
				Module: value.source.module,
				Path:   value.source.path,
				Kind:   value.source.kind,
				Line:   value.source.line,
				Column: value.source.column,
			},
			RejectionReason: value.rejectionReason,
		}
	}
	selectedProviders := make([]canonicalSelectedProvider, len(e.selectedProviders))
	for index, value := range e.selectedProviders {
		selectionSources := make([]canonicalProviderSelectionSource, len(value.selectionSources))
		for sourceIndex, selectionSource := range value.selectionSources {
			source := selectionSource.source
			selectionSources[sourceIndex] = canonicalProviderSelectionSource{
				ProjectModule: selectionSource.projectModule,
				Source: canonicalSource{
					Module: source.module,
					Path:   source.path,
					Kind:   source.kind,
					Line:   source.line,
					Column: source.column,
				},
			}
		}
		providerSource := value.providerSource
		selectedProviders[index] = canonicalSelectedProvider{
			Capability:     value.capability,
			PluginID:       value.pluginID,
			ProjectModule:  value.projectModule,
			ContractDigest: value.contractDigest,
			ProviderSource: canonicalSource{
				Module: providerSource.module,
				Path:   providerSource.path,
				Kind:   providerSource.kind,
				Line:   providerSource.line,
				Column: providerSource.column,
			},
			SelectionReason:  value.selectionReason,
			SelectionSources: selectionSources,
		}
	}
	generationActivations := make([]canonicalGenerationActivation, len(e.generationActivations))
	for index, value := range e.generationActivations {
		causes := make([]canonicalGenerationActivationCause, len(value.causes))
		for causeIndex, cause := range value.causes {
			source := cause.source
			causes[causeIndex] = canonicalGenerationActivationCause{
				ProjectModule: cause.projectModule,
				Source: canonicalSource{
					Module: source.module,
					Path:   source.path,
					Kind:   source.kind,
					Line:   source.line,
					Column: source.column,
				},
			}
		}
		generationActivations[index] = canonicalGenerationActivation{
			Namespace:            value.namespace,
			SourceCapability:     value.sourceCapability,
			ActivationCapability: value.activationCapability,
			PluginID:             value.pluginID,
			ProjectModule:        value.projectModule,
			Causes:               causes,
		}
	}
	generatedRequirements := make([]canonicalGeneratedRequirement, len(e.generatedRequirements))
	for index, value := range e.generatedRequirements {
		source := value.source
		generatedRequirements[index] = canonicalGeneratedRequirement{
			Capability:           value.capability,
			SourceCapability:     value.sourceCapability,
			ActivationCapability: value.activationCapability,
			Namespace:            value.namespace,
			PluginID:             value.pluginID,
			ProjectModule:        value.projectModule,
			RuleID:               value.ruleID,
			Source: canonicalSource{
				Module: source.module,
				Path:   source.path,
				Kind:   source.kind,
				Line:   source.line,
				Column: source.column,
			},
		}
	}
	return json.Marshal(canonicalEvidence{
		Version:               schemaVersion,
		GenerationAPI:         e.generationAPI,
		SelectedModelDigest:   e.selectedModelDigest,
		BuildModelDigest:      e.buildModelDigest,
		Modules:               modules,
		PluginCandidates:      pluginCandidates,
		SelectedPlugins:       selectedPlugins,
		Requirements:          requirements,
		ProviderCandidates:    providerCandidates,
		SelectedProviders:     selectedProviders,
		GenerationActivations: generationActivations,
		GeneratedRequirements: generatedRequirements,
		Counts: canonicalCounts{
			ParticipatingModules:  len(e.modules),
			DiscoveredPlugins:     len(e.pluginCandidates),
			SelectedPlugins:       len(e.selectedPlugins),
			CanonicalCapabilities: e.canonicalCapabilityCount,
			Requirements:          e.requirementCount,
			ProviderCandidates:    len(e.providerCandidates),
			RejectedProviders:     e.RejectedProviderCount(),
			SelectedProviders:     e.selectedProviderCount,
			GenerationActivations: len(e.generationActivations),
			GeneratedRequirements: len(e.generatedRequirements),
			CapabilityAliases:     e.capabilityAliasCount,
		},
	})
}

func normalizeModules(inputs []ModuleInput) ([]Module, error) {
	if len(inputs) == 0 {
		return nil, errors.New("one current Project is required")
	}
	modules := make([]Module, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	currentCount := 0
	for index, input := range inputs {
		if _, duplicate := seen[input.Path]; duplicate {
			return nil, fmt.Errorf("modules[%d].path duplicates %q", index, input.Path)
		}
		value, err := normalizeModule(input)
		if err != nil {
			return nil, fmt.Errorf("modules[%d]: %v", index, err)
		}
		seen[input.Path] = struct{}{}
		if value.role == ModuleRoleCurrent {
			currentCount++
		}
		modules = append(modules, value)
	}
	if currentCount != 1 {
		return nil, fmt.Errorf("exactly one current Project is required, got %d", currentCount)
	}
	sort.Slice(modules, func(left, right int) bool {
		if modules[left].role != modules[right].role {
			return modules[left].role == ModuleRoleCurrent
		}
		return modules[left].path < modules[right].path
	})
	return modules, nil
}

func normalizePluginCandidates(inputs []PluginCandidateInput, modules []Module) ([]PluginCandidate, error) {
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	candidates := make([]PluginCandidate, 0, len(inputs))
	seenIDs := make(map[string]struct{}, len(inputs))
	seenSources := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if err := pluginid.Validate(input.ID); err != nil {
			return nil, fmt.Errorf("plugin_candidates[%d].id %q is invalid: %v", index, input.ID, err)
		}
		if _, duplicate := seenIDs[input.ID]; duplicate {
			return nil, fmt.Errorf("plugin_candidates[%d].id duplicates %q", index, input.ID)
		}
		project, exists := moduleByPath[input.ModulePath]
		if !exists {
			return nil, fmt.Errorf("plugin_candidates[%d].module_path %q is not a participating Project", index, input.ModulePath)
		}
		if err := validatePluginPath(input.ModulePath, input.Path); err != nil {
			return nil, fmt.Errorf("plugin_candidates[%d].path %q is invalid: %v", index, input.Path, err)
		}
		sourceKey := input.ModulePath + "\x00" + input.Path
		if _, duplicate := seenSources[sourceKey]; duplicate {
			return nil, fmt.Errorf("plugin_candidates[%d] duplicates source %s/%s/plugin.yaml", index, input.ModulePath, input.Path)
		}
		seenIDs[input.ID] = struct{}{}
		seenSources[sourceKey] = struct{}{}
		candidates = append(candidates, PluginCandidate{
			id:         input.ID,
			modulePath: input.ModulePath,
			moduleRole: project.role,
			path:       input.Path,
			source: Source{
				module: project.source.module,
				path:   path.Join(input.Path, "plugin.yaml"),
				kind:   "plugin-declaration",
				line:   1,
				column: 1,
			},
		})
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].id < candidates[right].id
	})
	return candidates, nil
}

func validatePluginPath(modulePath, value string) error {
	if value == "" || path.IsAbs(value) || path.Clean(value) != value || strings.ContainsAny(value, `/\`) || pluginscan.IsReserved(value) {
		return errors.New("must be one safe non-reserved root child")
	}
	if err := gomodule.CheckImportPath(path.Join(modulePath, value)); err != nil {
		return fmt.Errorf("does not form a valid Go import path: %v", err)
	}
	return nil
}

type selectedPluginInput struct {
	id            string
	modulePath    string
	moduleVersion string
	reasons       []PluginSelectionReason
}

func selectedPluginsFromContext(context generation.Context, candidates []PluginCandidate, modules []Module) ([]SelectedPlugin, error) {
	candidateByID := make(map[string]PluginCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.id] = candidate
	}
	providerReasons := make(map[string][]PluginSelectionReason)
	for _, provider := range context.Providers() {
		pluginID := provider.Plugin().String()
		providerReasons[pluginID] = append(providerReasons[pluginID], PluginSelectionReason{
			kind:       PluginSelectionProvider,
			capability: provider.Capability().String(),
		})
	}
	plugins := context.Plugins()
	inputs := make([]selectedPluginInput, len(plugins))
	selected := make(map[string]struct{}, len(plugins))
	for index, plugin := range plugins {
		id := plugin.ID().String()
		reasons := append([]PluginSelectionReason(nil), providerReasons[id]...)
		if candidate, exists := candidateByID[id]; exists && candidate.moduleRole == ModuleRoleCurrent {
			reasons = append(reasons, PluginSelectionReason{kind: PluginSelectionCurrentProject})
		}
		inputs[index] = selectedPluginInput{
			id:            id,
			modulePath:    plugin.Module().Path(),
			moduleVersion: plugin.Module().Version(),
			reasons:       reasons,
		}
		selected[id] = struct{}{}
	}
	for pluginID := range providerReasons {
		if _, exists := selected[pluginID]; !exists {
			return nil, fmt.Errorf("provider selection names Plugin %q outside the selected Plugin set", pluginID)
		}
	}
	return normalizeSelectedPlugins(inputs, candidates, modules)
}

func normalizeSelectedPlugins(inputs []selectedPluginInput, candidates []PluginCandidate, modules []Module) ([]SelectedPlugin, error) {
	candidateByID := make(map[string]PluginCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.id] = candidate
	}
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	selected := make([]SelectedPlugin, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		if err := pluginid.Validate(input.id); err != nil {
			return nil, fmt.Errorf("selected_plugins[%d].id %q is invalid: %v", index, input.id, err)
		}
		if _, duplicate := seen[input.id]; duplicate {
			return nil, fmt.Errorf("selected_plugins[%d].id duplicates %q", index, input.id)
		}
		candidate, exists := candidateByID[input.id]
		if !exists {
			return nil, fmt.Errorf("selected Plugin %q has no discovered candidate", input.id)
		}
		if candidate.modulePath != input.modulePath {
			return nil, fmt.Errorf("selected Plugin %q module %q does not match candidate module %q", input.id, input.modulePath, candidate.modulePath)
		}
		project := moduleByPath[candidate.modulePath]
		expectedVersion := selectedPluginModuleVersion(project)
		if input.moduleVersion != expectedVersion {
			return nil, fmt.Errorf("selected Plugin %q module version %q does not match participating Project version %q", input.id, input.moduleVersion, expectedVersion)
		}
		reasons, err := normalizePluginSelectionReasons(input.reasons, candidate.moduleRole)
		if err != nil {
			return nil, fmt.Errorf("selected Plugin %q reasons: %v", input.id, err)
		}
		seen[input.id] = struct{}{}
		selected = append(selected, SelectedPlugin{
			id:            input.id,
			modulePath:    candidate.modulePath,
			moduleVersion: input.moduleVersion,
			moduleRole:    candidate.moduleRole,
			path:          candidate.path,
			source:        candidate.source,
			reasons:       reasons,
		})
	}
	for _, candidate := range candidates {
		if candidate.moduleRole != ModuleRoleCurrent {
			continue
		}
		if _, exists := seen[candidate.id]; !exists {
			return nil, fmt.Errorf("current-Project Plugin candidate %q is not selected", candidate.id)
		}
	}
	sort.Slice(selected, func(left, right int) bool {
		return selected[left].id < selected[right].id
	})
	return selected, nil
}

func selectedPluginModuleVersion(project Module) string {
	if project.role == ModuleRoleCurrent || project.workspace {
		return ""
	}
	return project.selectedVersion
}

func normalizePluginSelectionReasons(inputs []PluginSelectionReason, moduleRole ModuleRole) ([]PluginSelectionReason, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one selection reason is required")
	}
	reasons := make([]PluginSelectionReason, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	currentProject := false
	for index, input := range inputs {
		switch input.kind {
		case PluginSelectionCurrentProject:
			if input.capability != "" {
				return nil, fmt.Errorf("reasons[%d] current-project cannot name a Capability", index)
			}
			currentProject = true
		case PluginSelectionProvider:
			if _, err := generation.ParseCapabilityID(input.capability); err != nil {
				return nil, fmt.Errorf("reasons[%d] provider Capability %q is invalid: %v", index, input.capability, err)
			}
		default:
			return nil, fmt.Errorf("reasons[%d] kind %q is invalid", index, input.kind)
		}
		key := string(input.kind) + "\x00" + input.capability
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("reasons[%d] duplicates %s %q", index, input.kind, input.capability)
		}
		seen[key] = struct{}{}
		reasons = append(reasons, input)
	}
	if currentProject != (moduleRole == ModuleRoleCurrent) {
		if moduleRole == ModuleRoleCurrent {
			return nil, errors.New("current-Project Plugin requires a current-project reason")
		}
		return nil, errors.New("dependency Plugin cannot have a current-project reason")
	}
	sort.Slice(reasons, func(left, right int) bool {
		if reasons[left].kind != reasons[right].kind {
			return reasons[left].kind < reasons[right].kind
		}
		return reasons[left].capability < reasons[right].capability
	})
	return reasons, nil
}

func capabilityRequirementsFromResolution(
	resolution providerresolution.Result,
	context generation.Context,
	modules []Module,
	candidates []PluginCandidate,
) ([]CapabilityRequirement, error) {
	contextRequirements := context.Requirements()
	resolved := resolution.Capabilities()
	if len(resolved) != len(contextRequirements) {
		return nil, fmt.Errorf("provider resolution has %d requirements while the selected model has %d", len(resolved), len(contextRequirements))
	}
	contextCapabilities := make(map[string]generation.CapabilityView, len(context.Capabilities()))
	for _, capability := range context.Capabilities() {
		contextCapabilities[capability.ID().String()] = capability
	}
	required := make(map[string]struct{}, len(contextRequirements))
	for _, capability := range contextRequirements {
		required[capability.String()] = struct{}{}
	}
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	candidateByID := make(map[string]PluginCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.id] = candidate
	}
	values := make([]CapabilityRequirement, 0, len(resolved))
	seen := make(map[string]struct{}, len(resolved))
	for index, capability := range resolved {
		id := capability.ID().String()
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("provider resolution requirement %q is duplicated", id)
		}
		if _, exists := required[id]; !exists {
			return nil, fmt.Errorf("provider resolution requirement %q is absent from the selected model", id)
		}
		view, exists := contextCapabilities[id]
		if !exists {
			return nil, fmt.Errorf("provider resolution requirement %q has no selected canonical contract", id)
		}
		if capability.ContractDigest() != view.ContractDigest() || capability.Intrinsic() != view.Intrinsic() {
			return nil, fmt.Errorf("provider resolution requirement %q does not match the selected canonical contract", id)
		}
		sources, err := normalizeRequirementSources(capability.Sources(), moduleByPath, candidateByID)
		if err != nil {
			return nil, fmt.Errorf("requirements[%d] %s sources: %v", index, id, err)
		}
		seen[id] = struct{}{}
		values = append(values, CapabilityRequirement{
			capability:     id,
			contractDigest: capability.ContractDigest(),
			intrinsic:      capability.Intrinsic(),
			sources:        sources,
		})
	}
	for id := range required {
		if _, exists := seen[id]; !exists {
			return nil, fmt.Errorf("selected-model requirement %q is absent from provider resolution", id)
		}
	}
	sort.Slice(values, func(left, right int) bool { return values[left].capability < values[right].capability })
	if err := validateCapabilityRequirements(values, modules, candidates); err != nil {
		return nil, err
	}
	return values, nil
}

func normalizeRequirementSources(
	inputs []providerresolution.RequirementSource,
	modules map[string]Module,
	candidates map[string]PluginCandidate,
) ([]RequirementSource, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one typed source is required")
	}
	values := make([]RequirementSource, 0, len(inputs))
	for index, input := range inputs {
		project, exists := modules[input.ModulePath]
		if !exists {
			return nil, fmt.Errorf("sources[%d].module_path %q is not a participating Project", index, input.ModulePath)
		}
		value := RequirementSource{
			kind:          input.Kind,
			projectModule: input.ModulePath,
			source: Source{
				module: project.source.module,
				path:   input.Path,
				kind:   string(input.Kind),
				line:   input.Line,
				column: input.Column,
			},
			pluginID:         input.PluginID,
			alias:            input.Alias,
			namespace:        input.Namespace,
			sourceCapability: input.SourceCapability,
			ruleID:           input.RuleID,
		}
		if input.PluginID != "" {
			candidate, exists := candidates[input.PluginID]
			if !exists {
				return nil, fmt.Errorf("sources[%d].plugin_id %q is not a discovered Plugin", index, input.PluginID)
			}
			if candidate.modulePath != input.ModulePath {
				return nil, fmt.Errorf("sources[%d].plugin_id %q belongs to module %q, not %q", index, input.PluginID, candidate.modulePath, input.ModulePath)
			}
			switch input.Kind {
			case providerresolution.RequirementPlugin, providerresolution.RequirementGenerationRule:
				if candidate.path+"/plugin.yaml" != input.Path {
					return nil, fmt.Errorf("sources[%d].path %q does not match Plugin %q declaration", index, input.Path, input.PluginID)
				}
			case providerresolution.RequirementGeneratedClient:
				if input.Path != candidate.path && !strings.HasPrefix(input.Path, candidate.path+"/") {
					return nil, fmt.Errorf("sources[%d].path %q is outside Plugin %q", index, input.Path, input.PluginID)
				}
			}
		}
		values = append(values, value)
	}
	sort.Slice(values, func(left, right int) bool {
		return requirementSourceKey(values[left]) < requirementSourceKey(values[right])
	})
	unique := values[:0]
	for _, value := range values {
		if len(unique) != 0 && unique[len(unique)-1] == value {
			continue
		}
		unique = append(unique, value)
	}
	return append([]RequirementSource(nil), unique...), nil
}

func validateCapabilityRequirements(values []CapabilityRequirement, modules []Module, candidates []PluginCandidate) error {
	moduleByPath := make(map[string]Module, len(modules))
	moduleBySource := make(map[string]struct{}, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
		moduleBySource[project.source.module] = struct{}{}
	}
	candidateByID := make(map[string]PluginCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.id] = candidate
	}
	for index, value := range values {
		id, err := capabilityid.Parse(value.capability)
		if err != nil {
			return fmt.Errorf("requirements[%d].capability %q is invalid", index, value.capability)
		}
		if value.intrinsic != strings.HasPrefix(id.Name(), "kernel.") {
			return fmt.Errorf("requirements[%d].intrinsic contradicts %s", index, id)
		}
		if !validDigest(value.contractDigest) {
			return fmt.Errorf("requirements[%d].contract_digest is invalid", index)
		}
		if len(value.sources) == 0 {
			return fmt.Errorf("requirements[%d].sources must not be empty", index)
		}
		if index > 0 && values[index-1].capability >= value.capability {
			return fmt.Errorf("requirements are not in unique canonical Capability order at %q", value.capability)
		}
		for sourceIndex, source := range value.sources {
			project, exists := moduleByPath[source.projectModule]
			if !exists {
				return fmt.Errorf("requirements[%d].sources[%d].project_module %q is not participating", index, sourceIndex, source.projectModule)
			}
			if _, exists := moduleBySource[source.source.module]; !exists || project.source.module != source.source.module {
				return fmt.Errorf("requirements[%d].sources[%d].source.module %q is inconsistent", index, sourceIndex, source.source.module)
			}
			if source.source.kind != string(source.kind) || source.source.path == "" || path.IsAbs(source.source.path) || path.Clean(source.source.path) != source.source.path || strings.Contains(source.source.path, "\\") || source.source.line < 1 || source.source.column < 1 {
				return fmt.Errorf("requirements[%d].sources[%d] has invalid stable location", index, sourceIndex)
			}
			if err := validateRequirementSourceSemantics(source, candidateByID); err != nil {
				return fmt.Errorf("requirements[%d].sources[%d]: %v", index, sourceIndex, err)
			}
			if sourceIndex > 0 && requirementSourceKey(value.sources[sourceIndex-1]) >= requirementSourceKey(source) {
				return fmt.Errorf("requirements[%d].sources are not in unique canonical order", index)
			}
		}
	}
	return nil
}

func validateRequirementSourceSemantics(source RequirementSource, candidates map[string]PluginCandidate) error {
	empty := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return false
			}
		}
		return true
	}
	validatePlugin := func() error {
		if err := pluginid.Validate(source.pluginID); err != nil {
			return fmt.Errorf("plugin_id %q is invalid", source.pluginID)
		}
		candidate, exists := candidates[source.pluginID]
		if !exists || candidate.modulePath != source.projectModule {
			return fmt.Errorf("plugin_id %q does not match the source Project", source.pluginID)
		}
		return nil
	}
	validateSourceCapability := func() error {
		if _, err := capabilityid.Parse(source.sourceCapability); err != nil {
			return fmt.Errorf("source_capability %q is invalid", source.sourceCapability)
		}
		return nil
	}
	switch source.kind {
	case providerresolution.RequirementDeclaration, providerresolution.RequirementExposure:
		if !empty(source.pluginID, source.alias, source.namespace, source.sourceCapability, source.ruleID) {
			return errors.New("declarative source contains unrelated semantic fields")
		}
	case providerresolution.RequirementGeneratedClient, providerresolution.RequirementPlugin:
		if err := validatePlugin(); err != nil {
			return err
		}
		if !empty(source.alias, source.namespace, source.sourceCapability, source.ruleID) {
			return errors.New("plugin source contains unrelated semantic fields")
		}
	case providerresolution.RequirementAliasTarget:
		if _, err := capabilityid.Parse(source.alias); err != nil {
			return fmt.Errorf("alias %q is invalid", source.alias)
		}
		if !empty(source.pluginID, source.namespace, source.sourceCapability, source.ruleID) {
			return errors.New("alias source contains unrelated semantic fields")
		}
	case providerresolution.RequirementActivation:
		if source.namespace == "" {
			return errors.New("activation namespace is empty")
		}
		if err := validateSourceCapability(); err != nil {
			return err
		}
		if !empty(source.pluginID, source.alias, source.ruleID) {
			return errors.New("activation source contains unrelated semantic fields")
		}
	case providerresolution.RequirementGenerationRule:
		if err := validatePlugin(); err != nil {
			return err
		}
		if source.namespace == "" || source.ruleID == "" {
			return errors.New("generation-rule namespace and rule_id are required")
		}
		if err := validateSourceCapability(); err != nil {
			return err
		}
		if source.alias != "" {
			return errors.New("generation-rule source contains an Alias")
		}
	default:
		return fmt.Errorf("kind %q is invalid", source.kind)
	}
	return nil
}

func providerCandidatesFromResolution(
	resolution providerresolution.Result,
	context generation.Context,
	modules []Module,
	plugins []PluginCandidate,
	selectedPlugins []SelectedPlugin,
	requirements []CapabilityRequirement,
) ([]ProviderCandidate, error) {
	contextCapabilities := make(map[string]generation.CapabilityView, len(context.Capabilities()))
	for _, capability := range context.Capabilities() {
		contextCapabilities[capability.ID().String()] = capability
	}
	required := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		required[requirement.capability] = struct{}{}
	}
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	pluginByID := make(map[string]PluginCandidate, len(plugins))
	for _, plugin := range plugins {
		pluginByID[plugin.id] = plugin
	}
	contextProviders := make(map[string]string, len(context.Providers()))
	for _, provider := range context.Providers() {
		contextProviders[provider.Capability().String()] = provider.Plugin().String()
	}
	resolverProviders := make(map[string]string, len(resolution.Selections()))
	for _, selection := range resolution.Selections() {
		capability := selection.Capability().String()
		pluginID := selection.PluginID()
		if selected, exists := contextProviders[capability]; !exists || selected != pluginID {
			return nil, fmt.Errorf("selected model Provider %s -> %q does not match provider resolution %q", capability, selected, pluginID)
		}
		resolverProviders[capability] = pluginID
	}
	if len(resolverProviders) != len(contextProviders) {
		return nil, fmt.Errorf("provider resolution has %d selections while the selected model has %d", len(resolverProviders), len(contextProviders))
	}

	inputs := resolution.ProviderCandidates()
	values := make([]ProviderCandidate, 0, len(inputs))
	for index, input := range inputs {
		capability := input.Capability().String()
		view, exists := contextCapabilities[capability]
		if !exists {
			return nil, fmt.Errorf("candidates[%d] Capability %s is absent from the selected canonical catalog", index, capability)
		}
		if view.Intrinsic() {
			return nil, fmt.Errorf("candidates[%d] Capability %s is intrinsic", index, capability)
		}
		if input.ContractDigest() != view.ContractDigest() {
			return nil, fmt.Errorf("candidates[%d] %s contract does not match the selected canonical contract", index, capability)
		}
		plugin, exists := pluginByID[input.PluginID()]
		if !exists {
			return nil, fmt.Errorf("candidates[%d] Plugin %q is not discovered", index, input.PluginID())
		}
		project, exists := moduleByPath[plugin.modulePath]
		if !exists {
			return nil, fmt.Errorf("candidates[%d] Plugin %q belongs to nonparticipating module %q", index, input.PluginID(), plugin.modulePath)
		}
		rejection := ProviderRejectionCapabilityNotRequired
		if _, exists := required[capability]; exists {
			selected, exists := resolverProviders[capability]
			if !exists {
				return nil, fmt.Errorf("required Provider candidate %s from %q has no selected Provider", capability, input.PluginID())
			}
			if selected == input.PluginID() {
				rejection = ""
			} else {
				rejection = ProviderRejectionAnotherProviderSelected
			}
		}
		values = append(values, ProviderCandidate{
			capability:     capability,
			pluginID:       input.PluginID(),
			projectModule:  plugin.modulePath,
			contractDigest: input.ContractDigest(),
			source: Source{
				module: project.source.module,
				path:   providerCapabilityPath(plugin.path, input.Capability()),
				kind:   "provider-declaration",
				line:   1,
				column: 1,
			},
			rejectionReason: rejection,
		})
	}
	sort.Slice(values, func(left, right int) bool {
		return providerCandidateKey(values[left]) < providerCandidateKey(values[right])
	})
	if err := validateProviderCandidates(values, modules, plugins, requirements, selectedPlugins); err != nil {
		return nil, err
	}
	return values, nil
}

func validateProviderCandidates(values []ProviderCandidate, modules []Module, plugins []PluginCandidate, requirements []CapabilityRequirement, selectedPlugins []SelectedPlugin) error {
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	pluginByID := make(map[string]PluginCandidate, len(plugins))
	for _, plugin := range plugins {
		pluginByID[plugin.id] = plugin
	}
	required := make(map[string]bool, len(requirements))
	for _, requirement := range requirements {
		required[requirement.capability] = requirement.intrinsic
	}
	selectedByCapability := make(map[string]string)
	for _, plugin := range selectedPlugins {
		for _, reason := range plugin.reasons {
			if reason.kind != PluginSelectionProvider {
				continue
			}
			if previous, duplicate := selectedByCapability[reason.capability]; duplicate {
				return fmt.Errorf("selected Provider %s is attributed to both %q and %q", reason.capability, previous, plugin.id)
			}
			selectedByCapability[reason.capability] = plugin.id
		}
	}
	selectedCandidates := 0
	for index, value := range values {
		identifier, err := capabilityid.Parse(value.capability)
		if err != nil || strings.HasPrefix(identifier.Name(), "kernel.") {
			return fmt.Errorf("provider_candidates[%d].capability %q is not an ordinary canonical Capability", index, value.capability)
		}
		if err := pluginid.Validate(value.pluginID); err != nil {
			return fmt.Errorf("provider_candidates[%d].plugin_id %q is invalid", index, value.pluginID)
		}
		plugin, exists := pluginByID[value.pluginID]
		if !exists {
			return fmt.Errorf("provider_candidates[%d].plugin_id %q is not discovered", index, value.pluginID)
		}
		if plugin.modulePath != value.projectModule {
			return fmt.Errorf("provider_candidates[%d].project_module %q does not match Plugin %q module %q", index, value.projectModule, value.pluginID, plugin.modulePath)
		}
		project, exists := moduleByPath[value.projectModule]
		if !exists {
			return fmt.Errorf("provider_candidates[%d].project_module %q is not participating", index, value.projectModule)
		}
		if !validDigest(value.contractDigest) {
			return fmt.Errorf("provider_candidates[%d].contract_digest is invalid", index)
		}
		expectedPath := providerCapabilityPath(plugin.path, identifier)
		if value.source.module != project.source.module || value.source.path != expectedPath || value.source.kind != "provider-declaration" || value.source.line != 1 || value.source.column != 1 {
			return fmt.Errorf("provider_candidates[%d].source is inconsistent with Plugin %q and Capability %s", index, value.pluginID, identifier)
		}
		if index > 0 && providerCandidateKey(values[index-1]) >= providerCandidateKey(value) {
			return fmt.Errorf("provider candidates are not in unique canonical order at %s from %q", identifier, value.pluginID)
		}
		intrinsic, isRequired := required[value.capability]
		selectedPlugin, hasSelectedProvider := selectedByCapability[value.capability]
		switch value.rejectionReason {
		case "":
			if !isRequired || intrinsic || !hasSelectedProvider || selectedPlugin != value.pluginID {
				return fmt.Errorf("provider_candidates[%d] is selected inconsistently", index)
			}
			selectedCandidates++
		case ProviderRejectionCapabilityNotRequired:
			if isRequired {
				return fmt.Errorf("provider_candidates[%d] rejects required Capability %s as not required", index, identifier)
			}
		case ProviderRejectionAnotherProviderSelected:
			if !isRequired || intrinsic || !hasSelectedProvider || selectedPlugin == value.pluginID {
				return fmt.Errorf("provider_candidates[%d] has an invalid another-provider-selected rejection", index)
			}
		default:
			return fmt.Errorf("provider_candidates[%d].rejection_reason %q is invalid", index, value.rejectionReason)
		}
	}
	if selectedCandidates != len(selectedByCapability) {
		return fmt.Errorf("selected Provider candidate count %d does not match selected Plugin Provider reasons %d", selectedCandidates, len(selectedByCapability))
	}
	for capability, intrinsic := range required {
		if intrinsic {
			if _, exists := selectedByCapability[capability]; exists {
				return fmt.Errorf("intrinsic requirement %s has a selected Plugin Provider", capability)
			}
			continue
		}
		if _, exists := selectedByCapability[capability]; !exists {
			return fmt.Errorf("ordinary requirement %s has no selected Plugin Provider", capability)
		}
	}
	return nil
}

func selectedProvidersFromResolution(
	resolution providerresolution.Result,
	modules []Module,
	candidates []ProviderCandidate,
	requirements []CapabilityRequirement,
) ([]SelectedProvider, error) {
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	candidateBySelection := make(map[string]ProviderCandidate)
	candidateCounts := make(map[string]int)
	for _, candidate := range candidates {
		candidateCounts[candidate.capability]++
		if !candidate.Rejected() {
			candidateBySelection[candidate.capability+"\x00"+candidate.pluginID] = candidate
		}
	}
	selectionByCapability := make(map[string]providerresolution.Selection)
	for _, selection := range resolution.Selections() {
		capability := selection.Capability().String()
		if _, duplicate := selectionByCapability[capability]; duplicate {
			return nil, fmt.Errorf("provider resolution repeats selected Capability %s", capability)
		}
		selectionByCapability[capability] = selection
	}

	values := make([]SelectedProvider, 0, len(requirements))
	for _, requirement := range requirements {
		identifier, err := capabilityid.Parse(requirement.capability)
		if err != nil {
			return nil, fmt.Errorf("required Capability %q is invalid", requirement.capability)
		}
		if requirement.intrinsic {
			if _, exists := selectionByCapability[requirement.capability]; exists {
				return nil, fmt.Errorf("intrinsic Capability %s has an ordinary Provider selection", identifier)
			}
			values = append(values, SelectedProvider{
				capability:     requirement.capability,
				contractDigest: requirement.contractDigest,
				providerSource: Source{
					module: "github.com/plystra/kernel",
					path:   intrinsicProviderPath(identifier),
					kind:   "intrinsic-provider",
					line:   1,
					column: 1,
				},
				selectionReason: ProviderSelectionIntrinsic,
			})
			continue
		}

		selection, exists := selectionByCapability[requirement.capability]
		if !exists {
			return nil, fmt.Errorf("ordinary requirement %s has no Provider selection", identifier)
		}
		candidate, exists := candidateBySelection[requirement.capability+"\x00"+selection.PluginID()]
		if !exists {
			return nil, fmt.Errorf("selected Provider %s -> %q has no selected candidate record", identifier, selection.PluginID())
		}
		value := SelectedProvider{
			capability:     requirement.capability,
			pluginID:       candidate.pluginID,
			projectModule:  candidate.projectModule,
			contractDigest: requirement.contractDigest,
			providerSource: candidate.source,
		}
		choiceSources := selection.ChoiceSources()
		if !selection.Explicit() {
			if len(choiceSources) != 0 {
				return nil, fmt.Errorf("automatic Provider selection %s has explicit choice sources", identifier)
			}
			if candidateCounts[requirement.capability] != 1 {
				return nil, fmt.Errorf("automatic Provider selection %s has %d visible candidates", identifier, candidateCounts[requirement.capability])
			}
			value.selectionReason = ProviderSelectionSoleProvider
		} else {
			if len(choiceSources) == 0 {
				return nil, fmt.Errorf("explicit Provider selection %s has no typed source", identifier)
			}
			switch choiceSources[0].Kind {
			case providerresolution.ChoiceSourceCurrentProject:
				value.selectionReason = ProviderSelectionCurrentProject
			case providerresolution.ChoiceSourceDependencyProject:
				value.selectionReason = ProviderSelectionInherited
			default:
				return nil, fmt.Errorf("explicit Provider selection %s has invalid source kind %q", identifier, choiceSources[0].Kind)
			}
			value.selectionSources = make([]ProviderSelectionSource, 0, len(choiceSources))
			for sourceIndex, choiceSource := range choiceSources {
				project, exists := moduleByPath[choiceSource.ModulePath]
				if !exists {
					return nil, fmt.Errorf("provider selection %s source %d belongs to nonparticipating Project %q", identifier, sourceIndex, choiceSource.ModulePath)
				}
				if choiceSource.Kind == providerresolution.ChoiceSourceCurrentProject && project.role != ModuleRoleCurrent {
					return nil, fmt.Errorf("provider selection %s current-Project source belongs to dependency %q", identifier, choiceSource.ModulePath)
				}
				if choiceSource.Kind == providerresolution.ChoiceSourceDependencyProject && project.role != ModuleRoleDependency {
					return nil, fmt.Errorf("provider selection %s dependency source belongs to current Project", identifier)
				}
				value.selectionSources = append(value.selectionSources, ProviderSelectionSource{
					projectModule: choiceSource.ModulePath,
					source: Source{
						module: project.source.module,
						path:   choiceSource.Path,
						kind:   "provider-selection",
						line:   choiceSource.Line,
						column: choiceSource.Column,
					},
				})
			}
			sort.Slice(value.selectionSources, func(left, right int) bool {
				return providerSelectionSourceKey(value.selectionSources[left]) < providerSelectionSourceKey(value.selectionSources[right])
			})
		}
		values = append(values, value)
	}
	if err := validateSelectedProviders(values, modules, candidates, requirements); err != nil {
		return nil, err
	}
	return values, nil
}

func validateSelectedProviders(values []SelectedProvider, modules []Module, candidates []ProviderCandidate, requirements []CapabilityRequirement) error {
	if len(values) != len(requirements) {
		return fmt.Errorf("selected Provider records %d do not match requirements %d", len(values), len(requirements))
	}
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	requirementByCapability := make(map[string]CapabilityRequirement, len(requirements))
	for _, requirement := range requirements {
		requirementByCapability[requirement.capability] = requirement
	}
	candidatesByCapability := make(map[string][]ProviderCandidate)
	selectedCandidateByKey := make(map[string]ProviderCandidate)
	for _, candidate := range candidates {
		candidatesByCapability[candidate.capability] = append(candidatesByCapability[candidate.capability], candidate)
		if !candidate.Rejected() {
			selectedCandidateByKey[candidate.capability+"\x00"+candidate.pluginID] = candidate
		}
	}
	seen := make(map[string]struct{}, len(values))
	ordinarySelections := 0
	for index, value := range values {
		identifier, err := capabilityid.Parse(value.capability)
		if err != nil {
			return fmt.Errorf("selected_providers[%d].capability %q is invalid", index, value.capability)
		}
		if _, duplicate := seen[value.capability]; duplicate {
			return fmt.Errorf("selected_providers[%d].capability duplicates %s", index, identifier)
		}
		if index > 0 && values[index-1].capability >= value.capability {
			return fmt.Errorf("selected Providers are not in unique canonical order at %s", identifier)
		}
		requirement, exists := requirementByCapability[value.capability]
		if !exists || requirement.contractDigest != value.contractDigest {
			return fmt.Errorf("selected_providers[%d] does not match its canonical requirement", index)
		}
		seen[value.capability] = struct{}{}
		if value.selectionReason == ProviderSelectionIntrinsic {
			if !requirement.intrinsic || value.pluginID != "" || value.projectModule != "" || len(value.selectionSources) != 0 {
				return fmt.Errorf("selected_providers[%d] is an inconsistent intrinsic implementation", index)
			}
			expected := Source{module: "github.com/plystra/kernel", path: intrinsicProviderPath(identifier), kind: "intrinsic-provider", line: 1, column: 1}
			if value.providerSource != expected || len(candidatesByCapability[value.capability]) != 0 {
				return fmt.Errorf("selected_providers[%d] has invalid intrinsic Kernel provenance", index)
			}
			continue
		}
		if requirement.intrinsic {
			return fmt.Errorf("selected_providers[%d] selects an ordinary Provider for intrinsic %s", index, identifier)
		}
		if err := pluginid.Validate(value.pluginID); err != nil {
			return fmt.Errorf("selected_providers[%d].plugin_id %q is invalid", index, value.pluginID)
		}
		if _, exists := moduleByPath[value.projectModule]; !exists {
			return fmt.Errorf("selected_providers[%d].project_module %q is not participating", index, value.projectModule)
		}
		candidate, exists := selectedCandidateByKey[value.capability+"\x00"+value.pluginID]
		if !exists || candidate.projectModule != value.projectModule || candidate.contractDigest != value.contractDigest || candidate.source != value.providerSource {
			return fmt.Errorf("selected_providers[%d] does not match its selected Provider candidate", index)
		}
		ordinarySelections++
		switch value.selectionReason {
		case ProviderSelectionSoleProvider:
			if len(value.selectionSources) != 0 || len(candidatesByCapability[value.capability]) != 1 {
				return fmt.Errorf("selected_providers[%d] has an invalid sole-provider reason", index)
			}
		case ProviderSelectionCurrentProject:
			if len(value.selectionSources) != 1 {
				return fmt.Errorf("selected_providers[%d] current-project replacement requires one source", index)
			}
		case ProviderSelectionInherited:
			if len(value.selectionSources) == 0 {
				return fmt.Errorf("selected_providers[%d] inherited selection requires sources", index)
			}
		default:
			return fmt.Errorf("selected_providers[%d].selection_reason %q is invalid", index, value.selectionReason)
		}
		for sourceIndex, selectionSource := range value.selectionSources {
			sourceProject, exists := moduleByPath[selectionSource.projectModule]
			if !exists || sourceProject.source.module != selectionSource.source.module || selectionSource.source.kind != "provider-selection" || selectionSource.source.path == "" || path.IsAbs(selectionSource.source.path) || path.Clean(selectionSource.source.path) != selectionSource.source.path || selectionSource.source.path == "." || selectionSource.source.path == ".." || strings.HasPrefix(selectionSource.source.path, "../") || strings.Contains(selectionSource.source.path, "/../") || strings.Contains(selectionSource.source.path, "\\") || selectionSource.source.line < 1 || selectionSource.source.column < 1 {
				return fmt.Errorf("selected_providers[%d].selection_sources[%d] is invalid", index, sourceIndex)
			}
			if value.selectionReason == ProviderSelectionCurrentProject && sourceProject.role != ModuleRoleCurrent {
				return fmt.Errorf("selected_providers[%d] current-project source belongs to a dependency", index)
			}
			if value.selectionReason == ProviderSelectionInherited && sourceProject.role != ModuleRoleDependency {
				return fmt.Errorf("selected_providers[%d] inherited source belongs to the current Project", index)
			}
			if sourceIndex > 0 && providerSelectionSourceKey(value.selectionSources[sourceIndex-1]) >= providerSelectionSourceKey(selectionSource) {
				return fmt.Errorf("selected_providers[%d].selection_sources are not in unique canonical order", index)
			}
		}
	}
	if len(seen) != len(requirementByCapability) {
		return errors.New("selected Provider records omit one or more requirements")
	}
	if ordinarySelections != len(selectedCandidateByKey) {
		return fmt.Errorf("ordinary selected Provider records %d do not match selected candidates %d", ordinarySelections, len(selectedCandidateByKey))
	}
	return nil
}

func intrinsicProviderPath(identifier capabilityid.Identifier) string {
	return path.Join("capability", "catalog", "definitions", identifier.Name(), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml")
}

func providerSelectionSourceKey(value ProviderSelectionSource) string {
	return strings.Join([]string{
		value.projectModule,
		value.source.module,
		value.source.path,
		fmt.Sprintf("%010d", value.source.line),
		fmt.Sprintf("%010d", value.source.column),
	}, "\x00")
}

func generationEvidenceFromRequirements(
	requirements []CapabilityRequirement,
	providers []SelectedProvider,
	plugins []PluginCandidate,
) ([]GenerationActivation, []GeneratedRequirement, error) {
	requirementByCapability := make(map[string]CapabilityRequirement, len(requirements))
	for _, requirement := range requirements {
		requirementByCapability[requirement.capability] = requirement
	}
	providerByCapability := make(map[string]SelectedProvider, len(providers))
	for _, provider := range providers {
		providerByCapability[provider.capability] = provider
	}
	pluginByID := make(map[string]PluginCandidate, len(plugins))
	for _, plugin := range plugins {
		pluginByID[plugin.id] = plugin
	}

	activationByKey := make(map[string]int)
	activations := make([]GenerationActivation, 0)
	for _, requirement := range requirements {
		for _, source := range requirement.sources {
			if source.kind != providerresolution.RequirementActivation {
				continue
			}
			if !validGenerationNamespace(source.namespace) {
				return nil, nil, fmt.Errorf("activation for %s has invalid namespace %q", requirement.capability, source.namespace)
			}
			if _, exists := requirementByCapability[source.sourceCapability]; !exists {
				return nil, nil, fmt.Errorf("activation for %s names non-required source Capability %s", requirement.capability, source.sourceCapability)
			}
			provider, exists := providerByCapability[requirement.capability]
			if !exists || provider.Intrinsic() {
				return nil, nil, fmt.Errorf("activation Capability %s has no selected ordinary Provider", requirement.capability)
			}
			plugin, exists := pluginByID[provider.pluginID]
			if !exists || plugin.modulePath != provider.projectModule {
				return nil, nil, fmt.Errorf("activation Capability %s selected extension Plugin %q is inconsistent", requirement.capability, provider.pluginID)
			}
			key := generationActivationKeyParts(source.namespace, source.sourceCapability, requirement.capability, provider.pluginID)
			position, exists := activationByKey[key]
			if !exists {
				position = len(activations)
				activationByKey[key] = position
				activations = append(activations, GenerationActivation{
					namespace:            source.namespace,
					sourceCapability:     source.sourceCapability,
					activationCapability: requirement.capability,
					pluginID:             provider.pluginID,
					projectModule:        provider.projectModule,
				})
			}
			activations[position].causes = append(activations[position].causes, GenerationActivationCause{
				projectModule: source.projectModule,
				source:        source.source,
			})
		}
	}
	for index := range activations {
		causes := activations[index].causes
		sort.Slice(causes, func(left, right int) bool {
			return generationActivationCauseKey(causes[left]) < generationActivationCauseKey(causes[right])
		})
		unique := causes[:0]
		for _, cause := range causes {
			if len(unique) != 0 && unique[len(unique)-1] == cause {
				continue
			}
			unique = append(unique, cause)
		}
		activations[index].causes = append([]GenerationActivationCause(nil), unique...)
	}
	sort.Slice(activations, func(left, right int) bool {
		return generationActivationKey(activations[left]) < generationActivationKey(activations[right])
	})
	activationByUse := make(map[string]GenerationActivation, len(activations))
	activationBySource := make(map[string]GenerationActivation, len(activations))
	for _, activation := range activations {
		key := generationActivationUseKey(activation.pluginID, activation.namespace, activation.sourceCapability)
		if previous, duplicate := activationByUse[key]; duplicate && previous.activationCapability != activation.activationCapability {
			return nil, nil, fmt.Errorf("selected extension %q namespace %q source %s has several activation Capabilities", activation.pluginID, activation.namespace, activation.sourceCapability)
		}
		activationByUse[key] = activation
		sourceKey := generationActivationSourceKey(activation.namespace, activation.sourceCapability)
		if previous, duplicate := activationBySource[sourceKey]; duplicate && (previous.pluginID != activation.pluginID || previous.activationCapability != activation.activationCapability) {
			return nil, nil, fmt.Errorf("namespace %q source %s has several selected activations", activation.namespace, activation.sourceCapability)
		}
		activationBySource[sourceKey] = activation
	}

	generatedByKey := make(map[string]GeneratedRequirement)
	generated := make([]GeneratedRequirement, 0)
	for _, requirement := range requirements {
		for _, source := range requirement.sources {
			if source.kind != providerresolution.RequirementGenerationRule {
				continue
			}
			if !validGenerationNamespace(source.namespace) {
				return nil, nil, fmt.Errorf("generated requirement %s has invalid namespace %q", requirement.capability, source.namespace)
			}
			if !validGenerationRuleID(source.ruleID) {
				return nil, nil, fmt.Errorf("generated requirement %s has invalid rule ID %q", requirement.capability, source.ruleID)
			}
			if _, exists := requirementByCapability[source.sourceCapability]; !exists {
				return nil, nil, fmt.Errorf("generated requirement %s names non-required source Capability %s", requirement.capability, source.sourceCapability)
			}
			plugin, exists := pluginByID[source.pluginID]
			if !exists || plugin.modulePath != source.projectModule {
				return nil, nil, fmt.Errorf("generated requirement %s names inconsistent extension Plugin %q", requirement.capability, source.pluginID)
			}
			activation, exists := activationByUse[generationActivationUseKey(source.pluginID, source.namespace, source.sourceCapability)]
			if !exists {
				if selected, selectedExists := activationBySource[generationActivationSourceKey(source.namespace, source.sourceCapability)]; selectedExists {
					return nil, nil, fmt.Errorf("generated requirement %s from plugin %q rule %q differs from selected activation Plugin %q", requirement.capability, source.pluginID, source.ruleID, selected.pluginID)
				}
				return nil, nil, fmt.Errorf("generated requirement %s from plugin %q rule %q has no matching selected activation", requirement.capability, source.pluginID, source.ruleID)
			}
			value := GeneratedRequirement{
				capability:           requirement.capability,
				sourceCapability:     source.sourceCapability,
				activationCapability: activation.activationCapability,
				namespace:            source.namespace,
				pluginID:             source.pluginID,
				projectModule:        source.projectModule,
				ruleID:               source.ruleID,
				source:               source.source,
			}
			key := generatedRequirementKey(value)
			if previous, duplicate := generatedByKey[key]; duplicate {
				if previous != value {
					return nil, nil, fmt.Errorf("generated requirement %s from plugin %q rule %q has conflicting sources", requirement.capability, source.pluginID, source.ruleID)
				}
				continue
			}
			generatedByKey[key] = value
			generated = append(generated, value)
		}
	}
	sort.Slice(generated, func(left, right int) bool {
		return generatedRequirementKey(generated[left]) < generatedRequirementKey(generated[right])
	})
	return activations, generated, nil
}

func generationActivationKey(value GenerationActivation) string {
	return generationActivationKeyParts(value.namespace, value.sourceCapability, value.activationCapability, value.pluginID)
}

func generationActivationKeyParts(namespace, sourceCapability, activationCapability, pluginID string) string {
	return strings.Join([]string{namespace, sourceCapability, activationCapability, pluginID}, "\x00")
}

func generationActivationUseKey(pluginID, namespace, sourceCapability string) string {
	return strings.Join([]string{pluginID, namespace, sourceCapability}, "\x00")
}

func generationActivationSourceKey(namespace, sourceCapability string) string {
	return strings.Join([]string{namespace, sourceCapability}, "\x00")
}

func generationActivationCauseKey(value GenerationActivationCause) string {
	return strings.Join([]string{
		value.projectModule,
		value.source.module,
		value.source.path,
		value.source.kind,
		fmt.Sprintf("%010d", value.source.line),
		fmt.Sprintf("%010d", value.source.column),
	}, "\x00")
}

func generatedRequirementKey(value GeneratedRequirement) string {
	return strings.Join([]string{
		value.capability,
		value.namespace,
		value.sourceCapability,
		value.activationCapability,
		value.pluginID,
		value.ruleID,
	}, "\x00")
}

func equalGenerationActivations(left, right []GenerationActivation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].namespace != right[index].namespace ||
			left[index].sourceCapability != right[index].sourceCapability ||
			left[index].activationCapability != right[index].activationCapability ||
			left[index].pluginID != right[index].pluginID ||
			left[index].projectModule != right[index].projectModule ||
			len(left[index].causes) != len(right[index].causes) {
			return false
		}
		for causeIndex := range left[index].causes {
			if left[index].causes[causeIndex] != right[index].causes[causeIndex] {
				return false
			}
		}
	}
	return true
}

func equalGeneratedRequirements(left, right []GeneratedRequirement) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validGenerationNamespace(value string) bool {
	return validLowerKebabSegment(value, 128)
}

func validGenerationRuleID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if !validLowerKebabSegment(segment, 128) {
			return false
		}
	}
	return true
}

func validLowerKebabSegment(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func providerCapabilityPath(pluginPath string, identifier capabilityid.Identifier) string {
	return path.Join(pluginPath, "capabilities", identifier.Name(), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml")
}

func providerCandidateKey(value ProviderCandidate) string {
	return strings.Join([]string{value.capability, value.pluginID, value.projectModule, value.source.path}, "\x00")
}

func requirementSourceKey(value RequirementSource) string {
	return strings.Join([]string{
		string(value.kind),
		value.projectModule,
		value.source.module,
		value.source.path,
		fmt.Sprintf("%010d", value.source.line),
		fmt.Sprintf("%010d", value.source.column),
		value.pluginID,
		value.alias,
		value.namespace,
		value.sourceCapability,
		value.ruleID,
	}, "\x00")
}

func normalizeModule(input ModuleInput) (Module, error) {
	if input.SourceModulePath == "" {
		return Module{}, errors.New("source module path is required")
	}
	value := Module{
		path:            input.Path,
		role:            input.Role,
		requiredVersion: input.RequiredVersion,
		selectedVersion: input.SelectedVersion,
		direct:          input.Direct,
		indirect:        input.Indirect,
		workspace:       input.Workspace,
		source: Source{
			module: input.SourceModulePath,
			path:   "plystra.yaml",
			kind:   "project-marker",
			line:   1,
			column: 1,
		},
	}
	switch input.Role {
	case ModuleRoleCurrent:
		if err := modulepath.CheckProject(input.Path); err != nil {
			return Module{}, fmt.Errorf("current module path %q is invalid: %v", input.Path, err)
		}
		if input.SourceModulePath != input.Path {
			return Module{}, errors.New("current Project source module must match its module path")
		}
		if input.RequiredVersion != "" || input.SelectedVersion != "" || input.Direct || input.Indirect || input.Workspace || input.Replacement != nil {
			return Module{}, errors.New("current Project cannot have dependency graph provenance")
		}
	case ModuleRoleDependency:
		if err := gomodule.CheckPath(input.Path); err != nil {
			return Module{}, fmt.Errorf("dependency module path %q is invalid: %v", input.Path, err)
		}
		if input.Direct != (input.RequiredVersion != "") {
			return Module{}, errors.New("direct dependency must have exactly one required version")
		}
		if input.Indirect && !input.Direct {
			return Module{}, errors.New("indirect marker requires a direct dependency")
		}
		if input.RequiredVersion != "" {
			if err := gomodule.Check(input.Path, input.RequiredVersion); err != nil {
				return Module{}, fmt.Errorf("required version %q is invalid: %v", input.RequiredVersion, err)
			}
		}
		if input.Workspace {
			if input.SelectedVersion != "" || input.Replacement != nil {
				return Module{}, errors.New("workspace dependency cannot have a selected version or replacement")
			}
		} else {
			if input.SelectedVersion == "" {
				return Module{}, errors.New("non-workspace dependency requires a selected version")
			}
			if err := gomodule.Check(input.Path, input.SelectedVersion); err != nil {
				return Module{}, fmt.Errorf("selected version %q is invalid: %v", input.SelectedVersion, err)
			}
		}
		if input.Replacement == nil {
			if input.SourceModulePath != input.Path {
				return Module{}, errors.New("unreplaced dependency source module must match its graph module path")
			}
			break
		}
		replacement, err := normalizeReplacement(*input.Replacement)
		if err != nil {
			return Module{}, fmt.Errorf("replacement: %v", err)
		}
		if input.SourceModulePath != replacement.modulePath {
			return Module{}, errors.New("replacement source module does not match replacement identity")
		}
		value.replacement = replacement
		value.hasReplacement = true
	default:
		return Module{}, fmt.Errorf("role %q is invalid", input.Role)
	}
	return value, nil
}

func normalizeReplacement(input ReplacementInput) (Replacement, error) {
	if input.ModulePath == "" {
		return Replacement{}, errors.New("module path is required")
	}
	switch input.Kind {
	case ReplacementModule:
		if input.Version == "" {
			return Replacement{}, errors.New("module replacement version is required")
		}
		if err := gomodule.Check(input.ModulePath, input.Version); err != nil {
			return Replacement{}, fmt.Errorf("module replacement %s@%s is invalid: %v", input.ModulePath, input.Version, err)
		}
	case ReplacementLocal:
		if input.Version != "" {
			return Replacement{}, errors.New("local replacement cannot have a version")
		}
		if err := modulepath.CheckProject(input.ModulePath); err != nil {
			return Replacement{}, fmt.Errorf("local source module path %q is invalid: %v", input.ModulePath, err)
		}
	default:
		return Replacement{}, fmt.Errorf("kind %q is invalid", input.Kind)
	}
	return Replacement{kind: input.Kind, modulePath: input.ModulePath, version: input.Version}, nil
}

func (e Evidence) moduleInputs() []ModuleInput {
	inputs := make([]ModuleInput, len(e.modules))
	for index, value := range e.modules {
		var replacement *ReplacementInput
		if value.hasReplacement {
			replacement = &ReplacementInput{
				Kind:       value.replacement.kind,
				ModulePath: value.replacement.modulePath,
				Version:    value.replacement.version,
			}
		}
		inputs[index] = ModuleInput{
			Path:             value.path,
			Role:             value.role,
			RequiredVersion:  value.requiredVersion,
			SelectedVersion:  value.selectedVersion,
			Direct:           value.direct,
			Indirect:         value.indirect,
			Workspace:        value.workspace,
			SourceModulePath: value.source.module,
			Replacement:      replacement,
		}
	}
	return inputs
}

func (e Evidence) pluginCandidateInputs() []PluginCandidateInput {
	inputs := make([]PluginCandidateInput, len(e.pluginCandidates))
	for index, value := range e.pluginCandidates {
		inputs[index] = PluginCandidateInput{
			ID:         value.id,
			ModulePath: value.modulePath,
			Path:       value.path,
		}
	}
	return inputs
}

func (e Evidence) selectedPluginInputs() []selectedPluginInput {
	inputs := make([]selectedPluginInput, len(e.selectedPlugins))
	for index, value := range e.selectedPlugins {
		inputs[index] = selectedPluginInput{
			id:            value.id,
			modulePath:    value.modulePath,
			moduleVersion: value.moduleVersion,
			reasons:       append([]PluginSelectionReason(nil), value.reasons...),
		}
	}
	return inputs
}

func equalModules(left, right []Module) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalPluginCandidates(left, right []PluginCandidate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalSelectedPlugins(left, right []SelectedPlugin) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].id != right[index].id || left[index].modulePath != right[index].modulePath || left[index].moduleVersion != right[index].moduleVersion || left[index].moduleRole != right[index].moduleRole || left[index].path != right[index].path || left[index].source != right[index].source || len(left[index].reasons) != len(right[index].reasons) {
			return false
		}
		for reasonIndex := range left[index].reasons {
			if left[index].reasons[reasonIndex] != right[index].reasons[reasonIndex] {
				return false
			}
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
