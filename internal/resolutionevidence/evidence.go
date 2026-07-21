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
	Version             int                              `json:"version"`
	GenerationAPI       string                           `json:"generation_api"`
	SelectedModelDigest string                           `json:"selected_model_digest"`
	BuildModelDigest    string                           `json:"build_model_digest"`
	Modules             []canonicalModule                `json:"modules"`
	PluginCandidates    []canonicalPluginCandidate       `json:"plugin_candidates"`
	SelectedPlugins     []canonicalSelectedPlugin        `json:"selected_plugins"`
	Requirements        []canonicalCapabilityRequirement `json:"requirements"`
	ProviderCandidates  []canonicalProviderCandidate     `json:"provider_candidates"`
	Counts              canonicalCounts                  `json:"counts"`
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
	input := Evidence{
		generationAPI:            context.APIVersion(),
		selectedModelDigest:      context.Digest(),
		buildModelDigest:         context.BuildModelDigest(),
		canonicalCapabilityCount: len(context.Capabilities()),
		requirementCount:         len(requirements),
		selectedProviderCount:    len(context.Providers()),
		capabilityAliasCount:     len(context.CapabilityAliases()),
		modules:                  modules,
		pluginCandidates:         pluginCandidates,
		selectedPlugins:          selectedPlugins,
		requirements:             requirements,
		providerCandidates:       providerCandidates,
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

// SelectedProviderCount returns the number of selected ordinary Providers.
func (e Evidence) SelectedProviderCount() int { return e.selectedProviderCount }

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
	if e.selectedProviderCount > e.requirementCount {
		return errors.New("selected Provider count exceeds requirement count")
	}
	if e.selectedProviderCount > len(e.providerCandidates) {
		return errors.New("selected Provider count exceeds Provider candidate count")
	}
	providerReasons := 0
	for _, plugin := range e.selectedPlugins {
		for _, reason := range plugin.reasons {
			if reason.kind == PluginSelectionProvider {
				providerReasons++
			}
		}
	}
	if providerReasons != e.selectedProviderCount {
		return fmt.Errorf("selected Provider count %d does not match selected Plugin provider reasons %d", e.selectedProviderCount, providerReasons)
	}
	if err := validateProviderCandidates(e.providerCandidates, e.modules, e.pluginCandidates, e.requirements, e.selectedPlugins); err != nil {
		return err
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
	return json.Marshal(canonicalEvidence{
		Version:             schemaVersion,
		GenerationAPI:       e.generationAPI,
		SelectedModelDigest: e.selectedModelDigest,
		BuildModelDigest:    e.buildModelDigest,
		Modules:             modules,
		PluginCandidates:    pluginCandidates,
		SelectedPlugins:     selectedPlugins,
		Requirements:        requirements,
		ProviderCandidates:  providerCandidates,
		Counts: canonicalCounts{
			ParticipatingModules:  len(e.modules),
			DiscoveredPlugins:     len(e.pluginCandidates),
			SelectedPlugins:       len(e.selectedPlugins),
			CanonicalCapabilities: e.canonicalCapabilityCount,
			Requirements:          e.requirementCount,
			ProviderCandidates:    len(e.providerCandidates),
			RejectedProviders:     e.RejectedProviderCount(),
			SelectedProviders:     e.selectedProviderCount,
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
