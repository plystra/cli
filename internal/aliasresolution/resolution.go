// Package aliasresolution merges explicit and selected-extension Capability
// Alias candidates after canonical provider resolution has stabilized.
package aliasresolution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
)

const maximumCanonicalResultBytes = 16 << 20

var (
	// ErrResolve reports failure to produce one deterministic final Alias map.
	ErrResolve = errors.New("resolve Capability Aliases")
	// ErrInvalidContext reports an incomplete canonical application view.
	ErrInvalidContext = errors.New("invalid Alias resolution context")
	// ErrInvalidExtensionOutput reports Alias output whose selected-plugin
	// provenance is inconsistent with the supplied application context.
	ErrInvalidExtensionOutput = errors.New("invalid Alias extension output")
	// ErrConflict reports valid candidates for one Alias that disagree on their
	// direct target, exposure, or deprecation metadata.
	ErrConflict = errors.New("conflicting Capability Alias candidates")
)

// ExtensionOutputView is the selected-plugin output surface consumed by final
// Alias resolution. generationresolution.ExtensionOutput satisfies it.
type ExtensionOutputView interface {
	PluginID() string
	Output() generation.NormalizedOutput
}

// Source is one immutable declaration or selected-extension provenance record.
type Source struct {
	kind             generation.AliasSourceKind
	id               string
	contributionID   string
	namespace        string
	sourceCapability generation.CapabilityID
}

// Kind returns application or generation-extension provenance.
func (s Source) Kind() generation.AliasSourceKind { return s.kind }

// ID returns application or the selected contributing Plugin ID.
func (s Source) ID() string { return s.id }

// ContributionID returns the extension rule contribution when available.
func (s Source) ContributionID() string { return s.contributionID }

// Namespace returns the interpreted extension namespace when available.
func (s Source) Namespace() string { return s.namespace }

// SourceCapability returns the metadata-bearing canonical Capability when
// available.
func (s Source) SourceCapability() generation.CapabilityID { return s.sourceCapability }

// Alias is one immutable final application-local direct mapping.
type Alias struct {
	id             generation.CapabilityID
	target         generation.CapabilityID
	targetDigest   string
	targetExposure generation.Exposure
	exposure       generation.Exposure
	deprecated     string
	sources        []Source
}

// ID returns the application-local Alias ID.
func (a Alias) ID() generation.CapabilityID { return a.id }

// Target returns the direct canonical Capability ID.
func (a Alias) Target() generation.CapabilityID { return a.target }

// TargetContractDigest returns the exact canonical target contract digest.
func (a Alias) TargetContractDigest() string { return a.targetDigest }

// Exposure returns the normalized effective generated surfaces.
func (a Alias) Exposure() generation.Exposure { return a.exposure }

// ExposureNarrowing returns explicit normalized narrowing when the effective
// Alias exposure differs from its target.
func (a Alias) ExposureNarrowing() (generation.Exposure, bool) {
	if a.exposure == a.targetExposure {
		return generation.Exposure{}, false
	}
	return a.exposure, true
}

// Deprecated returns the application-local deprecation message, if any.
func (a Alias) Deprecated() string { return a.deprecated }

// Sources returns deterministic complete contributing provenance.
func (a Alias) Sources() []Source { return append([]Source(nil), a.sources...) }

// Result is one immutable final Alias map with canonical manifest bytes.
type Result struct {
	aliases       []Alias
	canonicalJSON []byte
	digest        string
}

// Aliases returns defensive mappings sorted by Alias ID.
func (r Result) Aliases() []Alias {
	result := make([]Alias, len(r.aliases))
	for index, alias := range r.aliases {
		result[index] = alias
		result[index].sources = append([]Source(nil), alias.sources...)
	}
	return result
}

// Inputs returns the normalized final map accepted by generation.NewContext.
// Rich rule provenance is reduced to unique source kind and ID pairs because
// the versioned context intentionally exposes only selected source identity.
func (r Result) Inputs() []generation.CapabilityAliasInput {
	inputs := make([]generation.CapabilityAliasInput, len(r.aliases))
	for index, alias := range r.aliases {
		sourceSet := make(map[string]generation.AliasSourceInput)
		for _, source := range alias.sources {
			key := string(source.kind) + "\x00" + source.id
			sourceSet[key] = generation.AliasSourceInput{Kind: source.kind, ID: source.id}
		}
		keys := sortedKeys(sourceSet)
		sources := make([]generation.AliasSourceInput, len(keys))
		for sourceIndex, key := range keys {
			sources[sourceIndex] = sourceSet[key]
		}
		inputs[index] = generation.CapabilityAliasInput{
			ID:         alias.id.String(),
			Target:     alias.target.String(),
			Exposure:   alias.exposure,
			Deprecated: alias.deprecated,
			Sources:    sources,
		}
	}
	return inputs
}

// CanonicalJSON returns defensive deterministic final-map manifest data.
func (r Result) CanonicalJSON() []byte { return append([]byte(nil), r.canonicalJSON...) }

// Digest returns the sha256 digest of CanonicalJSON.
func (r Result) Digest() string { return r.digest }

// Resolve merges aliases already normalized into context with structured
// proposals from selected extension outputs. It never chooses among conflicts.
func Resolve[O ExtensionOutputView](context generation.Context, outputs []O) (Result, error) {
	if context.APIVersion() != generation.Version || len(context.CanonicalJSON()) == 0 {
		return Result{}, fmt.Errorf("%w: %w: expected generation API %q context", ErrResolve, ErrInvalidContext, generation.Version)
	}

	candidates := make(map[generation.CapabilityID][]candidate)
	for _, existing := range context.CapabilityAliases() {
		target, exists := context.Capability(existing.Target())
		if !exists {
			return Result{}, fmt.Errorf("%w: %w: existing Alias %s target %s is not canonical", ErrResolve, ErrInvalidContext, existing.ID(), existing.Target())
		}
		sources := make([]Source, 0, len(existing.Sources()))
		for _, input := range existing.Sources() {
			source := Source{kind: input.Kind(), id: input.ID()}
			if err := validateSource(context, source); err != nil {
				return Result{}, fmt.Errorf("%w: %w: existing Alias %s: %v", ErrResolve, ErrInvalidContext, existing.ID(), err)
			}
			sources = append(sources, source)
		}
		value := candidate{
			alias:          existing.ID(),
			target:         existing.Target(),
			targetDigest:   target.ContractDigest(),
			targetExposure: target.Exposure(),
			exposure:       existing.Exposure(),
			deprecated:     existing.Deprecated(),
			sources:        sources,
		}
		if err := validateCandidate(context, value); err != nil {
			return Result{}, fmt.Errorf("%w: %w: existing Alias %s: %v", ErrResolve, ErrInvalidContext, existing.ID(), err)
		}
		candidates[value.alias] = append(candidates[value.alias], value)
	}

	records := make([]extensionRecord, len(outputs))
	for index, output := range outputs {
		records[index] = extensionRecord{pluginID: output.PluginID(), output: output.Output()}
	}
	sort.Slice(records, func(left, right int) bool { return records[left].pluginID < records[right].pluginID })
	for index, record := range records {
		pluginID, err := generation.ParsePluginID(record.pluginID)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w: output plugin %q is not canonical", ErrResolve, ErrInvalidExtensionOutput, record.pluginID)
		}
		if _, selected := context.Plugin(pluginID); !selected {
			return Result{}, fmt.Errorf("%w: %w: output plugin %q is not selected in the generation context", ErrResolve, ErrInvalidExtensionOutput, record.pluginID)
		}
		if index > 0 && records[index-1].pluginID == record.pluginID {
			return Result{}, fmt.Errorf("%w: %w: selected plugin %q returned more than one final output", ErrResolve, ErrInvalidExtensionOutput, record.pluginID)
		}
		for _, contribution := range record.output.AliasContributions() {
			target, exists := context.Capability(contribution.Target())
			if !exists {
				return Result{}, fmt.Errorf("%w: %w: plugin %q contribution %q target %s is not canonical in this context", ErrResolve, ErrInvalidExtensionOutput, record.pluginID, contribution.ID(), contribution.Target())
			}
			exposure := target.Exposure()
			if narrowed, explicit := contribution.Exposure(); explicit {
				exposure = narrowed
			}
			source := Source{
				kind:             generation.AliasSourceGenerationExtension,
				id:               record.pluginID,
				contributionID:   contribution.ID(),
				namespace:        contribution.Namespace(),
				sourceCapability: contribution.Source(),
			}
			if err := validateSource(context, source); err != nil {
				return Result{}, fmt.Errorf("%w: %w: plugin %q contribution %q: %v", ErrResolve, ErrInvalidExtensionOutput, record.pluginID, contribution.ID(), err)
			}
			value := candidate{
				alias:          contribution.Alias(),
				target:         contribution.Target(),
				targetDigest:   target.ContractDigest(),
				targetExposure: target.Exposure(),
				exposure:       exposure,
				deprecated:     contribution.Deprecated(),
				sources:        []Source{source},
			}
			if err := validateCandidate(context, value); err != nil {
				return Result{}, fmt.Errorf("%w: %w: plugin %q contribution %q: %v", ErrResolve, ErrInvalidExtensionOutput, record.pluginID, contribution.ID(), err)
			}
			candidates[value.alias] = append(candidates[value.alias], value)
		}
	}

	aliasIDs := make([]generation.CapabilityID, 0, len(candidates))
	for aliasID := range candidates {
		aliasIDs = append(aliasIDs, aliasID)
	}
	sort.Slice(aliasIDs, func(left, right int) bool { return aliasIDs[left].String() < aliasIDs[right].String() })
	aliases := make([]Alias, 0, len(aliasIDs))
	for _, aliasID := range aliasIDs {
		values := candidates[aliasID]
		sort.Slice(values, func(left, right int) bool { return candidateSortKey(values[left]) < candidateSortKey(values[right]) })
		metadata := make(map[string]struct{})
		for _, value := range values {
			metadata[candidateMetadataKey(value)] = struct{}{}
		}
		if len(metadata) != 1 {
			return Result{}, conflictError(aliasID, values)
		}
		sources := mergeSources(values)
		selected := values[0]
		aliases = append(aliases, Alias{
			id:             aliasID,
			target:         selected.target,
			targetDigest:   selected.targetDigest,
			targetExposure: selected.targetExposure,
			exposure:       selected.exposure,
			deprecated:     selected.deprecated,
			sources:        sources,
		})
	}

	canonical, err := encodeResult(aliases)
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode final Alias map: %v", ErrResolve, err)
	}
	if len(canonical) > maximumCanonicalResultBytes {
		return Result{}, fmt.Errorf("%w: final Alias map exceeds %d bytes", ErrResolve, maximumCanonicalResultBytes)
	}
	return Result{aliases: aliases, canonicalJSON: canonical, digest: digest(canonical)}, nil
}

type extensionRecord struct {
	pluginID string
	output   generation.NormalizedOutput
}

type candidate struct {
	alias          generation.CapabilityID
	target         generation.CapabilityID
	targetDigest   string
	targetExposure generation.Exposure
	exposure       generation.Exposure
	deprecated     string
	sources        []Source
}

func validateCandidate(context generation.Context, value candidate) error {
	if value.alias.String() == "" || value.target.String() == "" {
		return errors.New("Alias and target IDs must be canonical")
	}
	if _, collision := context.Capability(value.alias); collision {
		return fmt.Errorf("Alias %s collides with a canonical Capability", value.alias)
	}
	if strings.HasPrefix(value.alias.Name(), "kernel.") {
		return fmt.Errorf("Alias %s uses the reserved kernel.* namespace", value.alias)
	}
	target, exists := context.Capability(value.target)
	if !exists {
		return fmt.Errorf("target %s is not a canonical Capability", value.target)
	}
	if !containsCapability(context.Requirements(), value.target) {
		return fmt.Errorf("target %s is not a resolved canonical requirement", value.target)
	}
	if value.alias.Major() != value.target.Major() {
		return fmt.Errorf("Alias %s and target %s do not use the same version", value.alias, value.target)
	}
	if value.targetDigest != target.ContractDigest() {
		return fmt.Errorf("target %s contract digest is inconsistent", value.target)
	}
	if !exposureSubset(value.exposure, target.Exposure()) {
		return fmt.Errorf("Alias %s exposure broadens target %s", value.alias, value.target)
	}
	if len(value.deprecated) > 1024 || strings.ContainsRune(value.deprecated, '\x00') {
		return fmt.Errorf("Alias %s deprecation metadata is invalid", value.alias)
	}
	return nil
}

func validateSource(context generation.Context, source Source) error {
	switch source.kind {
	case generation.AliasSourceApplication:
		if source.id != "application" || source.contributionID != "" || source.namespace != "" || source.sourceCapability.String() != "" {
			return errors.New("application source provenance is invalid")
		}
	case generation.AliasSourceGenerationExtension:
		pluginID, err := generation.ParsePluginID(source.id)
		if err != nil {
			return fmt.Errorf("generation-extension source plugin %q is invalid", source.id)
		}
		if _, selected := context.Plugin(pluginID); !selected {
			return fmt.Errorf("generation-extension source plugin %q is not selected", source.id)
		}
	default:
		return fmt.Errorf("source kind %q is unsupported", source.kind)
	}
	return nil
}

func containsCapability(values []generation.CapabilityID, target generation.CapabilityID) bool {
	index := sort.Search(len(values), func(index int) bool { return values[index].String() >= target.String() })
	return index < len(values) && values[index] == target
}

func exposureSubset(alias, target generation.Exposure) bool {
	return (!alias.Go || target.Go) && (!alias.HTTP || target.HTTP) && (!alias.JavaScript || target.JavaScript)
}

func candidateMetadataKey(value candidate) string {
	return strings.Join([]string{
		value.target.String(),
		fmt.Sprintf("%t:%t:%t", value.exposure.Go, value.exposure.HTTP, value.exposure.JavaScript),
		value.deprecated,
	}, "\x00")
}

func candidateSortKey(value candidate) string {
	sourceKeys := make([]string, len(value.sources))
	for index, source := range value.sources {
		sourceKeys[index] = sourceSortKey(source)
	}
	sort.Strings(sourceKeys)
	return candidateMetadataKey(value) + "\x00" + strings.Join(sourceKeys, "\x01")
}

func sourceSortKey(source Source) string {
	return strings.Join([]string{
		string(source.kind),
		source.id,
		source.contributionID,
		source.namespace,
		source.sourceCapability.String(),
	}, "\x00")
}

func mergeSources(values []candidate) []Source {
	byKey := make(map[string]Source)
	for _, value := range values {
		for _, source := range value.sources {
			byKey[sourceSortKey(source)] = source
		}
	}
	keys := sortedKeys(byKey)
	sources := make([]Source, len(keys))
	for index, key := range keys {
		sources[index] = byKey[key]
	}
	return sources
}

func conflictError(alias generation.CapabilityID, values []candidate) error {
	descriptions := make([]string, len(values))
	for index, value := range values {
		sourceDescriptions := make([]string, len(value.sources))
		for sourceIndex, source := range value.sources {
			sourceDescriptions[sourceIndex] = describeSource(source)
		}
		sort.Strings(sourceDescriptions)
		descriptions[index] = fmt.Sprintf(
			"target %s exposure(go=%t,http=%t,javascript=%t) deprecated=%q from [%s]",
			value.target,
			value.exposure.Go,
			value.exposure.HTTP,
			value.exposure.JavaScript,
			value.deprecated,
			strings.Join(sourceDescriptions, "; "),
		)
	}
	sort.Strings(descriptions)
	return fmt.Errorf(
		"%w: %w: Alias %s has incompatible candidates [%s]; no source priority or override is permitted",
		ErrResolve,
		ErrConflict,
		alias,
		strings.Join(descriptions, "; "),
	)
}

func describeSource(source Source) string {
	if source.kind == generation.AliasSourceApplication {
		return "application plystra.yaml"
	}
	return fmt.Sprintf(
		"selected plugin %q contribution %q for extensions.%s on %s",
		source.id,
		source.contributionID,
		source.namespace,
		source.sourceCapability,
	)
}

type canonicalResult struct {
	Aliases []canonicalAlias `json:"capability_aliases"`
}

type canonicalAlias struct {
	ID                   string                 `json:"id"`
	Target               string                 `json:"target"`
	TargetContractDigest string                 `json:"target_contract_digest"`
	Exposure             *generation.Exposure   `json:"exposure,omitempty"`
	Deprecated           string                 `json:"deprecated,omitempty"`
	Sources              []canonicalAliasSource `json:"sources"`
}

type canonicalAliasSource struct {
	Kind             generation.AliasSourceKind `json:"kind"`
	ID               string                     `json:"id"`
	ContributionID   string                     `json:"contribution_id,omitempty"`
	Namespace        string                     `json:"namespace,omitempty"`
	SourceCapability string                     `json:"source_capability,omitempty"`
}

func encodeResult(aliases []Alias) ([]byte, error) {
	canonical := canonicalResult{Aliases: make([]canonicalAlias, len(aliases))}
	for index, alias := range aliases {
		var exposure *generation.Exposure
		if narrowed, ok := alias.ExposureNarrowing(); ok {
			value := narrowed
			exposure = &value
		}
		sources := make([]canonicalAliasSource, len(alias.sources))
		for sourceIndex, source := range alias.sources {
			sources[sourceIndex] = canonicalAliasSource{
				Kind:             source.kind,
				ID:               source.id,
				ContributionID:   source.contributionID,
				Namespace:        source.namespace,
				SourceCapability: source.sourceCapability.String(),
			}
		}
		canonical.Aliases[index] = canonicalAlias{
			ID:                   alias.id.String(),
			Target:               alias.target.String(),
			TargetContractDigest: alias.targetDigest,
			Exposure:             exposure,
			Deprecated:           alias.deprecated,
			Sources:              sources,
		}
	}
	return json.Marshal(canonical)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
