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
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/pluginscan"
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

// Input is the construction-only selected-model, participating-Project, and
// discovered-Plugin input for one evidence document.
type Input struct {
	Context          generation.Context
	Modules          []ModuleInput
	PluginCandidates []PluginCandidateInput
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
	selectedPluginCount      int
	canonicalCapabilityCount int
	requirementCount         int
	selectedProviderCount    int
	capabilityAliasCount     int
	modules                  []Module
	pluginCandidates         []PluginCandidate
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
	Version             int                        `json:"version"`
	GenerationAPI       string                     `json:"generation_api"`
	SelectedModelDigest string                     `json:"selected_model_digest"`
	BuildModelDigest    string                     `json:"build_model_digest"`
	Modules             []canonicalModule          `json:"modules"`
	PluginCandidates    []canonicalPluginCandidate `json:"plugin_candidates"`
	Counts              canonicalCounts            `json:"counts"`
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
	if err := validateSelectedPluginCandidates(context, pluginCandidates, modules); err != nil {
		return Evidence{}, fmt.Errorf("%w: selected Plugins: %v", ErrBuild, err)
	}
	input := Evidence{
		generationAPI:            context.APIVersion(),
		selectedModelDigest:      context.Digest(),
		buildModelDigest:         context.BuildModelDigest(),
		selectedPluginCount:      len(context.Plugins()),
		canonicalCapabilityCount: len(context.Capabilities()),
		requirementCount:         len(context.Requirements()),
		selectedProviderCount:    len(context.Providers()),
		capabilityAliasCount:     len(context.CapabilityAliases()),
		modules:                  modules,
		pluginCandidates:         pluginCandidates,
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

// SelectedPluginCount returns the number of selected Plugins.
func (e Evidence) SelectedPluginCount() int { return e.selectedPluginCount }

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
	if e.selectedPluginCount > len(e.pluginCandidates) {
		return errors.New("selected Plugin count exceeds discovered Plugin candidate count")
	}
	counts := []struct {
		name  string
		value int
	}{
		{name: "selected Plugin", value: e.selectedPluginCount},
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
	return json.Marshal(canonicalEvidence{
		Version:             schemaVersion,
		GenerationAPI:       e.generationAPI,
		SelectedModelDigest: e.selectedModelDigest,
		BuildModelDigest:    e.buildModelDigest,
		Modules:             modules,
		PluginCandidates:    pluginCandidates,
		Counts: canonicalCounts{
			ParticipatingModules:  len(e.modules),
			DiscoveredPlugins:     len(e.pluginCandidates),
			SelectedPlugins:       e.selectedPluginCount,
			CanonicalCapabilities: e.canonicalCapabilityCount,
			Requirements:          e.requirementCount,
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

func validateSelectedPluginCandidates(context generation.Context, candidates []PluginCandidate, modules []Module) error {
	candidateByID := make(map[string]PluginCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.id] = candidate
	}
	moduleByPath := make(map[string]Module, len(modules))
	for _, project := range modules {
		moduleByPath[project.path] = project
	}
	selected := make(map[string]struct{}, len(context.Plugins()))
	for _, plugin := range context.Plugins() {
		id := plugin.ID().String()
		candidate, exists := candidateByID[id]
		if !exists {
			return fmt.Errorf("selected Plugin %q has no discovered candidate", id)
		}
		moduleView := plugin.Module()
		if candidate.modulePath != moduleView.Path() {
			return fmt.Errorf("selected Plugin %q module %q does not match candidate module %q", id, moduleView.Path(), candidate.modulePath)
		}
		project := moduleByPath[candidate.modulePath]
		expectedVersion := project.selectedVersion
		if project.role == ModuleRoleCurrent || project.workspace {
			expectedVersion = ""
		}
		if moduleView.Version() != expectedVersion {
			return fmt.Errorf("selected Plugin %q module version %q does not match participating Project version %q", id, moduleView.Version(), expectedVersion)
		}
		selected[id] = struct{}{}
	}
	for _, candidate := range candidates {
		if candidate.moduleRole != ModuleRoleCurrent {
			continue
		}
		if _, exists := selected[candidate.id]; !exists {
			return fmt.Errorf("current-Project Plugin candidate %q is not selected", candidate.id)
		}
	}
	return nil
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
