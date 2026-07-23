// Package applicationinput loads indexed filesystem contracts into the
// existing deterministic provider and generation-resolution input model.
package applicationinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/providerresolution"
)

const maximumSourceSize = 1024

var (
	// ErrBuild reports that filesystem-backed resolution input could not be
	// constructed completely.
	ErrBuild = errors.New("build filesystem application resolution input")
	// ErrContractConflict reports visible providers carrying different exact
	// contracts for one canonical Capability ID.
	ErrContractConflict = errors.New("conflicting visible Capability contracts")
	// ErrIntrinsicProvider reports an ordinary plugin claiming a reserved
	// kernel.* Capability.
	ErrIntrinsicProvider = errors.New("plugin provides intrinsic Capability")
)

// SourceContext identifies the current and dependency Project modules used to
// turn parsed configuration references into stable typed requirement sources.
type SourceContext struct {
	CurrentModulePath    string
	Dependencies         []DependencySource
	DependencyProvenance []DependencyProvenance
	CurrentProjectPaths  []string
}

// DependencySource identifies one effective-graph dependency Project version.
type DependencySource struct {
	ModulePath string
	Version    string
}

// DependencyProvenance identifies every dependency declaration that matches
// one effective resolution field after current-project replacement.
type DependencyProvenance struct {
	Path    string
	Sources []string
}

// Build loads every indexed provider contract, merges the exact visible
// canonical catalog with Kernel intrinsics, carries normalized selected-
// configuration provenance, and constructs the fixed-point resolver input. It
// performs no provider selection itself.
func Build(manifest applicationmeta.Manifest, inventory plugininventory.Index, sourceContext SourceContext, configurationProvenance *generation.ConfigurationProvenanceInput, buildOptions generationexec.BuildOptions) (generationresolution.ExtensionInput, error) {
	if err := validateSourceContext(sourceContext); err != nil {
		return generationresolution.ExtensionInput{}, fmt.Errorf("%w: requirement source context: %v", ErrBuild, err)
	}
	groups := make(map[capabilityid.Identifier]*contractGroup)
	for _, definition := range intrinsiccatalog.Definitions() {
		groups[definition.ID()] = &contractGroup{
			intrinsic: true,
			variants: []contractVariant{{
				contract: definition.ContractJSON(),
				digest:   definition.ContractDigest(),
				sources:  []string{definition.Source()},
			}},
		}
	}

	plugins := make([]generationresolution.Plugin, 0, len(inventory.Plugins()))
	candidates := make([]providerresolution.Candidate, 0)
	declarations := make([]generationactivation.Declaration, 0)
	for _, plugin := range inventory.Plugins() {
		provides := identifierStrings(plugin.Provides())
		requires := identifierStrings(plugin.Requires())
		plugins = append(plugins, generationresolution.Plugin{
			Context: generation.PluginInput{
				ID:                plugin.ID(),
				ModulePath:        plugin.ModulePath(),
				ModuleVersion:     plugin.ModuleVersion(),
				Provides:          provides,
				Requires:          requires,
				BuildMetadataJSON: []byte("{}"),
			},
			Local:      plugin.Local(),
			ModuleRoot: plugin.ModuleRoot(),
			PluginPath: plugin.Path(),
		})
		if declaration, exists := plugin.Generation(); exists {
			declarations = append(declarations, generationactivation.Declaration{
				PluginID:   plugin.ID(),
				Source:     boundedSource(plugin.Source()),
				Generation: declaration,
			})
		}
		for _, provided := range plugin.Provides() {
			if strings.HasPrefix(provided.Name(), "kernel.") {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w: plugin %q at %s claims %s", ErrBuild, ErrIntrinsicProvider, plugin.ID(), plugin.Source(), provided)
			}
			source, err := capabilitysource.Load(plugin.PluginRoot(), provided)
			if err != nil {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrBuild, plugin.ID(), provided, err)
			}
			canonical, err := capabilitymeta.NormalizeSchema(source.Data())
			if err != nil {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: plugin %q Capability %s: normalize contract: %w", ErrBuild, plugin.ID(), provided, err)
			}
			provenance := capabilityProvenance(plugin, source.RelativePath())
			group, exists := groups[provided]
			if !exists {
				group = &contractGroup{}
				groups[provided] = group
			}
			group.add(canonical, provenance)
			candidates = append(candidates, providerresolution.Candidate{
				PluginID: plugin.ID(),
				Contract: append([]byte(nil), canonical...),
				Source:   provenance,
			})
		}
	}

	identifiers := make([]capabilityid.Identifier, 0, len(groups))
	for identifier := range groups {
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left].String() < identifiers[right].String()
	})
	var conflicts []error
	capabilities := make([]generation.CapabilityInput, 0, len(identifiers))
	for _, identifier := range identifiers {
		group := groups[identifier]
		group.normalize()
		if len(group.variants) != 1 {
			variants := make([]ContractVariant, len(group.variants))
			for index, variant := range group.variants {
				variants[index] = ContractVariant{digest: variant.digest, sources: append([]string(nil), variant.sources...)}
			}
			conflicts = append(conflicts, &ContractConflictError{id: identifier, variants: variants})
			continue
		}
		capabilities = append(capabilities, generation.CapabilityInput{
			ContractJSON: append([]byte(nil), group.variants[0].contract...),
			Sources:      append([]string(nil), group.variants[0].sources...),
			Intrinsic:    group.intrinsic,
			Exposure:     generation.Exposure{Go: true},
		})
	}
	if len(conflicts) != 0 {
		return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w", ErrBuild, errors.Join(conflicts...))
	}

	requirements := make([]providerresolution.Requirement, 0, len(manifest.Requirements()))
	for _, declared := range manifest.Requirements() {
		field := fmt.Sprintf("capabilities.require[%q]", declared.ID().String())
		sources, err := configurationRequirementSources(sourceContext, declared.Source(), field, providerresolution.RequirementDeclaration)
		if err != nil {
			return generationresolution.ExtensionInput{}, fmt.Errorf("%w: requirement %s: %v", ErrBuild, declared.ID(), err)
		}
		for _, source := range sources {
			requirement := providerresolution.Requirement{
				Capability: declared.ID().String(),
				Source:     source,
			}
			if group, exists := groups[declared.ID()]; exists && len(group.variants) == 1 {
				requirement.Contract = append([]byte(nil), group.variants[0].contract...)
			}
			requirements = append(requirements, requirement)
		}
	}
	httpExposures := make([]generationresolution.ApplicationHTTPExposure, 0, len(manifest.HTTPExposures()))
	for _, exposure := range manifest.HTTPExposures() {
		legacyID, err := capabilityid.Parse(exposure.ID().String())
		if err != nil {
			return generationresolution.ExtensionInput{}, fmt.Errorf("%w: HTTP exposure %s cannot enter the legacy Capability pipeline: %v", ErrBuild, exposure.ID(), err)
		}
		if _, legacy := groups[legacyID]; !legacy {
			continue
		}
		field := fmt.Sprintf("http.expose[%q]", exposure.ID().String())
		sources, err := configurationRequirementSources(sourceContext, exposure.Source(), field, providerresolution.RequirementExposure)
		if err != nil {
			return generationresolution.ExtensionInput{}, fmt.Errorf("%w: HTTP exposure %s: %v", ErrBuild, exposure.ID(), err)
		}
		httpExposures = append(httpExposures, generationresolution.ApplicationHTTPExposure{Exposure: exposure, Sources: sources})
	}
	aliases := make([]generationresolution.ApplicationAlias, 0, len(manifest.Aliases()))
	for _, alias := range manifest.Aliases() {
		field := fmt.Sprintf("capabilities.aliases[%q]", alias.ID().String())
		sources, err := configurationRequirementSources(sourceContext, alias.Source(), field, providerresolution.RequirementAliasTarget)
		if err != nil {
			return generationresolution.ExtensionInput{}, fmt.Errorf("%w: Alias %s: %v", ErrBuild, alias.ID(), err)
		}
		for index := range sources {
			sources[index].Reference += " target"
			sources[index].Alias = alias.ID().String()
		}
		aliases = append(aliases, generationresolution.ApplicationAlias{Alias: alias, Sources: sources})
	}
	choices := make([]providerresolution.Choice, 0, len(manifest.ProviderChoices()))
	for _, declared := range manifest.ProviderChoices() {
		field := fmt.Sprintf("capabilities.use[%q]", declared.Capability().String())
		sources, err := configurationProviderChoiceSources(sourceContext, declared.Source(), field)
		if err != nil {
			return generationresolution.ExtensionInput{}, fmt.Errorf("%w: Provider choice %s: %v", ErrBuild, declared.Capability(), err)
		}
		choices = append(choices, providerresolution.Choice{
			Capability: declared.Capability().String(),
			PluginID:   declared.PluginID(),
			Sources:    sources,
		})
	}
	activations, err := generationactivation.New(declarations)
	if err != nil {
		return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].PluginID != candidates[right].PluginID {
			return candidates[left].PluginID < candidates[right].PluginID
		}
		return candidates[left].Source < candidates[right].Source
	})
	buildOptions.BuildEnvironment = append([]string(nil), buildOptions.BuildEnvironment...)
	var provenance *generation.ConfigurationProvenanceInput
	if configurationProvenance != nil {
		copy := *configurationProvenance
		provenance = &copy
	}
	return generationresolution.ExtensionInput{
		Input: generationresolution.Input{
			Requirements: requirements,
			Candidates:   candidates,
			Choices:      choices,
			Activations:  activations,
		},
		ConfigurationProvenance:  provenance,
		Plugins:                  plugins,
		Capabilities:             capabilities,
		ApplicationHTTPExposures: httpExposures,
		ApplicationAliases:       aliases,
		BuildOptions:             buildOptions,
	}, nil
}

func validateSourceContext(input SourceContext) error {
	if err := modulepath.CheckProject(input.CurrentModulePath); err != nil {
		return fmt.Errorf("current module path %q is invalid: %v", input.CurrentModulePath, err)
	}
	seen := make(map[string]struct{}, len(input.Dependencies))
	for index, dependency := range input.Dependencies {
		if err := modulepath.CheckProject(dependency.ModulePath); err != nil {
			return fmt.Errorf("dependencies[%d].module_path %q is invalid: %v", index, dependency.ModulePath, err)
		}
		if dependency.ModulePath == input.CurrentModulePath {
			return fmt.Errorf("dependencies[%d] repeats the current module", index)
		}
		if _, duplicate := seen[dependency.ModulePath]; duplicate {
			return fmt.Errorf("dependencies[%d] repeats module %q", index, dependency.ModulePath)
		}
		if strings.ContainsAny(dependency.Version, "\x00\r\n/") {
			return fmt.Errorf("dependencies[%d].version is invalid", index)
		}
		seen[dependency.ModulePath] = struct{}{}
	}
	seenProvenance := make(map[string]struct{}, len(input.DependencyProvenance))
	for index, provenance := range input.DependencyProvenance {
		if provenance.Path == "" || strings.ContainsAny(provenance.Path, "\x00\r\n") {
			return fmt.Errorf("dependency_provenance[%d].path is invalid", index)
		}
		if _, duplicate := seenProvenance[provenance.Path]; duplicate {
			return fmt.Errorf("dependency_provenance[%d] repeats path %q", index, provenance.Path)
		}
		if len(provenance.Sources) == 0 {
			return fmt.Errorf("dependency_provenance[%d].sources is empty", index)
		}
		seenSources := make(map[string]struct{}, len(provenance.Sources))
		for sourceIndex, source := range provenance.Sources {
			if source == "" || len(source) > maximumSourceSize || !utf8.ValidString(source) || strings.ContainsAny(source, "\x00\r\n") {
				return fmt.Errorf("dependency_provenance[%d].sources[%d] is invalid", index, sourceIndex)
			}
			if _, duplicate := seenSources[source]; duplicate {
				return fmt.Errorf("dependency_provenance[%d].sources[%d] is duplicated", index, sourceIndex)
			}
			seenSources[source] = struct{}{}
		}
		seenProvenance[provenance.Path] = struct{}{}
	}
	seenCurrentPaths := make(map[string]struct{}, len(input.CurrentProjectPaths))
	for index, currentPath := range input.CurrentProjectPaths {
		if currentPath == "" || strings.ContainsAny(currentPath, "\x00\r\n") {
			return fmt.Errorf("current_project_paths[%d] is invalid", index)
		}
		if _, duplicate := seenCurrentPaths[currentPath]; duplicate {
			return fmt.Errorf("current_project_paths[%d] repeats %q", index, currentPath)
		}
		seenCurrentPaths[currentPath] = struct{}{}
	}
	return nil
}

func configurationRequirementSources(input SourceContext, reference, field string, kind providerresolution.RequirementSourceKind) ([]providerresolution.RequirementSource, error) {
	references := make([]string, 0, 2)
	for _, currentPath := range input.CurrentProjectPaths {
		if currentPath == field {
			references = append(references, reference)
			break
		}
	}
	currentSourceCount := len(references)
	for _, provenance := range input.DependencyProvenance {
		if provenance.Path == field {
			references = append(references, provenance.Sources...)
			break
		}
	}
	if len(references) == 0 {
		references = append(references, reference)
		currentSourceCount = 1
	}
	values := make([]providerresolution.RequirementSource, 0, len(references))
	for index, value := range references {
		dependencySource := index >= currentSourceCount
		source, err := configurationRequirementSource(input, value, field, kind, dependencySource)
		if err != nil {
			return nil, err
		}
		if dependencySource && source.ModulePath == input.CurrentModulePath {
			return nil, fmt.Errorf("dependency source %q does not identify a discovered dependency Project", value)
		}
		values = append(values, source)
	}
	return values, nil
}

func configurationProviderChoiceSources(input SourceContext, reference, field string) ([]providerresolution.ChoiceSource, error) {
	for _, currentPath := range input.CurrentProjectPaths {
		if currentPath != field {
			continue
		}
		source, err := configurationProviderChoiceSource(input, reference, field, providerresolution.ChoiceSourceCurrentProject)
		if err != nil {
			return nil, err
		}
		return []providerresolution.ChoiceSource{source}, nil
	}
	for _, provenance := range input.DependencyProvenance {
		if provenance.Path != field {
			continue
		}
		values := make([]providerresolution.ChoiceSource, 0, len(provenance.Sources))
		for _, value := range provenance.Sources {
			source, err := configurationProviderChoiceSource(input, value, field, providerresolution.ChoiceSourceDependencyProject)
			if err != nil {
				return nil, err
			}
			if source.ModulePath == input.CurrentModulePath {
				return nil, fmt.Errorf("dependency source %q does not identify a discovered dependency Project", value)
			}
			values = append(values, source)
		}
		sort.Slice(values, func(left, right int) bool {
			if values[left].ModulePath != values[right].ModulePath {
				return values[left].ModulePath < values[right].ModulePath
			}
			if values[left].Path != values[right].Path {
				return values[left].Path < values[right].Path
			}
			if values[left].Line != values[right].Line {
				return values[left].Line < values[right].Line
			}
			if values[left].Column != values[right].Column {
				return values[left].Column < values[right].Column
			}
			return values[left].Reference < values[right].Reference
		})
		return values, nil
	}
	source, err := configurationProviderChoiceSource(input, reference, field, providerresolution.ChoiceSourceCurrentProject)
	if err != nil {
		return nil, err
	}
	return []providerresolution.ChoiceSource{source}, nil
}

func configurationProviderChoiceSource(input SourceContext, reference, field string, kind providerresolution.ChoiceSourceKind) (providerresolution.ChoiceSource, error) {
	document, err := configurationDocument(reference, field)
	if err != nil {
		return providerresolution.ChoiceSource{}, err
	}
	modulePath := input.CurrentModulePath
	relativePath := document
	if kind == providerresolution.ChoiceSourceDependencyProject {
		for _, dependency := range input.Dependencies {
			version := dependency.Version
			if version == "" {
				version = "workspace"
			}
			prefix := dependency.ModulePath + "@" + version + "/"
			if strings.HasPrefix(document, prefix) {
				modulePath = dependency.ModulePath
				relativePath = strings.TrimPrefix(document, prefix)
				break
			}
		}
	}
	if relativePath == "" || path.IsAbs(relativePath) || path.Clean(relativePath) != relativePath || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, "../") || strings.Contains(relativePath, "/../") || strings.Contains(relativePath, "\\") || strings.ContainsAny(relativePath, "\x00\r\n") {
		return providerresolution.ChoiceSource{}, fmt.Errorf("source %q has an unsafe Project-relative document", reference)
	}
	return providerresolution.ChoiceSource{
		Kind:       kind,
		Reference:  reference,
		ModulePath: modulePath,
		Path:       relativePath,
		Line:       1,
		Column:     1,
	}, nil
}

func configurationRequirementSource(input SourceContext, reference, field string, kind providerresolution.RequirementSourceKind, dependencySource bool) (providerresolution.RequirementSource, error) {
	document, err := configurationDocument(reference, field)
	if err != nil {
		return providerresolution.RequirementSource{}, err
	}
	modulePath := input.CurrentModulePath
	relativePath := document
	if dependencySource {
		for _, dependency := range input.Dependencies {
			version := dependency.Version
			if version == "" {
				version = "workspace"
			}
			prefix := dependency.ModulePath + "@" + version + "/"
			if strings.HasPrefix(document, prefix) {
				modulePath = dependency.ModulePath
				relativePath = strings.TrimPrefix(document, prefix)
				break
			}
		}
	}
	if relativePath == "" || path.IsAbs(relativePath) || path.Clean(relativePath) != relativePath || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, "../") || strings.Contains(relativePath, "/../") || strings.Contains(relativePath, "\\") || strings.ContainsAny(relativePath, "\x00\r\n") {
		return providerresolution.RequirementSource{}, fmt.Errorf("source %q has an unsafe Project-relative document", reference)
	}
	return providerresolution.RequirementSource{
		Kind:       kind,
		Reference:  reference,
		ModulePath: modulePath,
		Path:       relativePath,
		Line:       1,
		Column:     1,
	}, nil
}

func configurationDocument(reference, field string) (string, error) {
	fields := []string{field}
	if position := strings.IndexByte(field, '['); position > 0 {
		fields = append(fields, field[:position]+".add"+field[position:])
	}
	for _, candidate := range fields {
		suffix := " " + candidate
		if strings.HasSuffix(reference, suffix) && len(reference) > len(suffix) {
			return strings.TrimSuffix(reference, suffix), nil
		}
	}
	return "", fmt.Errorf("source %q does not identify %s", reference, field)
}

type contractGroup struct {
	intrinsic bool
	variants  []contractVariant
}

type contractVariant struct {
	contract []byte
	digest   string
	sources  []string
}

func (g *contractGroup) add(contract []byte, source string) {
	for index := range g.variants {
		if bytes.Equal(g.variants[index].contract, contract) {
			g.variants[index].sources = append(g.variants[index].sources, source)
			return
		}
	}
	g.variants = append(g.variants, contractVariant{
		contract: append([]byte(nil), contract...),
		digest:   contractDigest(contract),
		sources:  []string{source},
	})
}

func (g *contractGroup) normalize() {
	for index := range g.variants {
		sort.Strings(g.variants[index].sources)
	}
	sort.Slice(g.variants, func(left, right int) bool {
		if g.variants[left].digest != g.variants[right].digest {
			return g.variants[left].digest < g.variants[right].digest
		}
		return bytes.Compare(g.variants[left].contract, g.variants[right].contract) < 0
	})
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}

func capabilityProvenance(plugin plugininventory.Plugin, relativePath string) string {
	version := plugin.ModuleVersion()
	if version == "" {
		version = "local"
	}
	return boundedSource(path.Join(plugin.ModulePath()+"@"+version, plugin.Path(), relativePath))
}

func boundedSource(value string) string {
	if len(value) <= maximumSourceSize {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	suffix := "...#sha256:" + hex.EncodeToString(digest[:])
	limit := maximumSourceSize - len(suffix)
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + suffix
}

func contractDigest(contract []byte) string {
	digest := sha256.Sum256(contract)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// ContractVariant records one exact conflicting digest and every provider
// source carrying it.
type ContractVariant struct {
	digest  string
	sources []string
}

// Digest returns the canonical contract SHA-256 digest.
func (v ContractVariant) Digest() string { return v.digest }

// Sources returns deterministic provider provenance for this exact variant.
func (v ContractVariant) Sources() []string {
	return append([]string(nil), v.sources...)
}

// ContractConflictError reports every exact variant visible under one
// canonical Capability ID.
type ContractConflictError struct {
	id       capabilityid.Identifier
	variants []ContractVariant
}

// ID returns the conflicting canonical Capability ID.
func (e *ContractConflictError) ID() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.id
}

// Variants returns defensive digest/source views in deterministic order.
func (e *ContractConflictError) Variants() []ContractVariant {
	if e == nil {
		return nil
	}
	result := make([]ContractVariant, len(e.variants))
	for index, variant := range e.variants {
		result[index] = ContractVariant{digest: variant.digest, sources: append([]string(nil), variant.sources...)}
	}
	return result
}

func (e *ContractConflictError) Error() string {
	if e == nil {
		return ErrContractConflict.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s for %s", ErrContractConflict, e.id)
	for _, variant := range e.variants {
		fmt.Fprintf(&message, "; %s at [%s]", variant.digest, strings.Join(variant.sources, ", "))
	}
	return message.String()
}

// Unwrap supports errors.Is with ErrContractConflict.
func (*ContractConflictError) Unwrap() error { return ErrContractConflict }
