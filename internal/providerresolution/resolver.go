// Package providerresolution selects one plugin provider for every exact
// ordinary canonical Capability requirement. Reserved kernel.* contracts are
// resolved intrinsically and never enter the ordinary provider map.
package providerresolution

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

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/pluginid"
)

var (
	// ErrResolve reports that a complete provider mapping could not be built.
	ErrResolve = errors.New("resolve canonical Capability providers")
	// ErrInvalidInput reports malformed or contradictory normalized resolver input.
	ErrInvalidInput = errors.New("invalid provider resolution input")
	// ErrRequirementConflict reports different exact contracts required under one ID.
	ErrRequirementConflict = errors.New("conflicting canonical Capability requirements")
	// ErrInvalidProvider reports an invalid provider claim or duplicate candidate.
	ErrInvalidProvider = errors.New("invalid canonical Capability provider")
	// ErrProviderContract reports a provider that carries a different exact contract.
	ErrProviderContract = errors.New("incompatible provider contract")
	// ErrMissingProvider reports an ordinary requirement with no candidate provider.
	ErrMissingProvider = errors.New("missing canonical Capability provider")
	// ErrAmbiguousProvider reports several candidates without an explicit choice.
	ErrAmbiguousProvider = errors.New("ambiguous canonical Capability provider")
	// ErrInvalidChoice reports an invalid explicit capabilities.use selection.
	ErrInvalidChoice = errors.New("invalid explicit Capability provider choice")
)

// RequirementSourceKind identifies the semantic edge that introduced one
// exact canonical Capability requirement.
type RequirementSourceKind string

const (
	// RequirementDeclaration identifies an explicit capabilities.require entry.
	RequirementDeclaration RequirementSourceKind = "declaration"
	// RequirementExposure identifies an explicit public HTTP exposure.
	RequirementExposure RequirementSourceKind = "exposure"
	// RequirementGeneratedClient identifies a statically detected generated-client use.
	RequirementGeneratedClient RequirementSourceKind = "generated-client"
	// RequirementPlugin identifies one selected Plugin's declared requires edge.
	RequirementPlugin RequirementSourceKind = "plugin"
	// RequirementAliasTarget identifies an application Alias target edge.
	RequirementAliasTarget RequirementSourceKind = "alias-target"
	// RequirementActivation identifies a generation namespace activation edge.
	RequirementActivation RequirementSourceKind = "activation"
	// RequirementGenerationRule identifies a selected generation rule output edge.
	RequirementGenerationRule RequirementSourceKind = "generation-rule"
)

// RequirementSource is one typed, stable, module-relative explanation for a
// canonical requirement. Reference is retained for human diagnostics; the
// remaining fields let evidence consumers classify provenance without parsing
// diagnostic text.
type RequirementSource struct {
	Kind             RequirementSourceKind
	Reference        string
	ModulePath       string
	Path             string
	Line             int
	Column           int
	PluginID         string
	Alias            string
	Namespace        string
	SourceCapability string
	RuleID           string
}

// String returns the bounded stable diagnostic reference.
func (s RequirementSource) String() string { return s.Reference }

// Requirement carries deterministic typed provenance describing why the
// application requires one exact canonical Capability. Contract may use the
// accepted capability.yaml syntax. Capability may instead carry an exact
// ordinary ID whose contract must be inferred identically from all visible
// providers. When both are present, they must identify the same exact
// Capability.
type Requirement struct {
	Capability string
	Contract   []byte
	Source     RequirementSource
}

// Candidate carries one plugin's provider copy of an exact canonical contract.
type Candidate struct {
	PluginID string
	Contract []byte
	Source   string
}

// ChoiceSourceKind identifies whether one effective capabilities.use source
// belongs to the selected current Project or one dependency Project.
type ChoiceSourceKind string

const (
	// ChoiceSourceCurrentProject identifies the winning selected-current-project
	// configuration declaration.
	ChoiceSourceCurrentProject ChoiceSourceKind = "current-project"
	// ChoiceSourceDependencyProject identifies one compatible inherited
	// dependency-Project declaration.
	ChoiceSourceDependencyProject ChoiceSourceKind = "dependency-project"
)

// ChoiceSource is one typed, stable, module-relative source for an effective
// capabilities.use selection. Reference is retained only for diagnostics.
type ChoiceSource struct {
	Kind       ChoiceSourceKind
	Reference  string
	ModulePath string
	Path       string
	Line       int
	Column     int
}

// String returns the bounded stable diagnostic reference.
func (s ChoiceSource) String() string { return s.Reference }

// Choice is one normalized explicit capabilities.use entry. Sources contains
// either the one winning current-Project declaration or every compatible
// inherited dependency declaration.
type Choice struct {
	Capability string
	PluginID   string
	Sources    []ChoiceSource
}

// ChoiceProblem classifies one rejected explicit provider choice.
type ChoiceProblem string

const (
	// ChoiceDuplicate reports several choices for one exact Capability.
	ChoiceDuplicate ChoiceProblem = "duplicate"
	// ChoiceUnknownCapability reports a choice outside the visible canonical catalog.
	ChoiceUnknownCapability ChoiceProblem = "unknown"
	// ChoiceUnrequiredCapability reports a known Capability not currently required.
	ChoiceUnrequiredCapability ChoiceProblem = "unrequired"
	// ChoiceIntrinsicCapability reports a choice for a reserved kernel.* contract.
	ChoiceIntrinsicCapability ChoiceProblem = "intrinsic"
	// ChoiceUnknownPlugin reports a Plugin ID absent from the visible candidates.
	ChoiceUnknownPlugin ChoiceProblem = "unknown-plugin"
	// ChoiceNonProvider reports a visible plugin that does not provide the chosen contract.
	ChoiceNonProvider ChoiceProblem = "non-provider"
)

// Input contains the complete visible inputs for one provider-resolution pass.
type Input struct {
	Requirements []Requirement
	Candidates   []Candidate
	Choices      []Choice
}

// Catalog is one immutable validated visible Provider candidate set. A caller
// may reuse it while fixed-point resolution changes only requirements or
// explicit current-Project choices.
type Catalog struct {
	candidatesByCapability map[capabilityid.Identifier][]normalizedCandidate
	knownPlugins           map[string]struct{}
	valid                  bool
}

// ResolvedCapability is one immutable exact requirement. Intrinsic requirements
// intentionally have no Selection.
type ResolvedCapability struct {
	id           capabilityid.Identifier
	contractJSON []byte
	digest       string
	intrinsic    bool
	sources      []RequirementSource
}

// ID returns the exact canonical Capability identity.
func (c ResolvedCapability) ID() capabilityid.Identifier { return c.id }

// ContractJSON returns a defensive copy of the normalized exact contract.
func (c ResolvedCapability) ContractJSON() []byte {
	return append([]byte(nil), c.contractJSON...)
}

// ContractDigest returns the SHA-256 digest of ContractJSON.
func (c ResolvedCapability) ContractDigest() string { return c.digest }

// Intrinsic reports whether the Kernel owns this reserved kernel.* Capability.
func (c ResolvedCapability) Intrinsic() bool { return c.intrinsic }

// Sources returns sorted, deduplicated typed requirement provenance.
func (c ResolvedCapability) Sources() []RequirementSource {
	return append([]RequirementSource(nil), c.sources...)
}

// Selection records one selected ordinary provider.
type Selection struct {
	capability       capabilityid.Identifier
	pluginID         string
	providerSource   string
	choiceSources    []ChoiceSource
	explicitlyChosen bool
}

// Capability returns the exact canonical Capability being provided.
func (s Selection) Capability() capabilityid.Identifier { return s.capability }

// PluginID returns the selected canonical Plugin ID.
func (s Selection) PluginID() string { return s.pluginID }

// ProviderSource returns the selected provider declaration provenance.
func (s Selection) ProviderSource() string { return s.providerSource }

// Explicit reports whether capabilities.use selected this provider.
func (s Selection) Explicit() bool { return s.explicitlyChosen }

// ChoiceSource returns the first deterministic explicit choice diagnostic
// reference, or an empty string for an automatically selected sole provider.
func (s Selection) ChoiceSource() string {
	if len(s.choiceSources) == 0 {
		return ""
	}
	return s.choiceSources[0].Reference
}

// ChoiceSources returns the one winning current-Project source or every
// compatible inherited dependency source. Automatic selections return nil.
func (s Selection) ChoiceSources() []ChoiceSource {
	return append([]ChoiceSource(nil), s.choiceSources...)
}

// ProviderCandidate is one immutable normalized ordinary Provider declaration
// from the complete visible catalog. It is retained independently of whether
// its Capability is required or its Plugin is selected.
type ProviderCandidate struct {
	capability     capabilityid.Identifier
	pluginID       string
	contractDigest string
	source         string
}

// Capability returns the exact canonical Capability declared by the Provider.
func (c ProviderCandidate) Capability() capabilityid.Identifier { return c.capability }

// PluginID returns the canonical Plugin ID declaring the Provider.
func (c ProviderCandidate) PluginID() string { return c.pluginID }

// ContractDigest returns the normalized exact contract identity.
func (c ProviderCandidate) ContractDigest() string { return c.contractDigest }

// Source returns the bounded Provider declaration provenance used by resolver
// diagnostics. Structured evidence derives its typed source independently.
func (c ProviderCandidate) Source() string { return c.source }

// Result is one immutable deterministic provider resolution.
type Result struct {
	capabilities       []ResolvedCapability
	capabilityIndex    map[capabilityid.Identifier]int
	providerCandidates []ProviderCandidate
	selections         []Selection
	selectionIndex     map[capabilityid.Identifier]int
}

// Capabilities returns defensive requirement views sorted by canonical ID.
func (r Result) Capabilities() []ResolvedCapability {
	return append([]ResolvedCapability(nil), r.capabilities...)
}

// Capability returns one exact resolved requirement.
func (r Result) Capability(id capabilityid.Identifier) (ResolvedCapability, bool) {
	position, exists := r.capabilityIndex[id]
	if !exists {
		return ResolvedCapability{}, false
	}
	return r.capabilities[position], true
}

// ProviderCandidates returns every normalized ordinary Provider declaration
// in canonical Capability, Plugin ID, and source order.
func (r Result) ProviderCandidates() []ProviderCandidate {
	return append([]ProviderCandidate(nil), r.providerCandidates...)
}

// Selections returns defensive ordinary provider mappings sorted by Capability ID.
func (r Result) Selections() []Selection {
	values := append([]Selection(nil), r.selections...)
	for index := range values {
		values[index].choiceSources = append([]ChoiceSource(nil), values[index].choiceSources...)
	}
	return values
}

// SelectedProvider returns the selected ordinary provider. It returns false for
// intrinsic and unrequired Capabilities.
func (r Result) SelectedProvider(id capabilityid.Identifier) (Selection, bool) {
	position, exists := r.selectionIndex[id]
	if !exists {
		return Selection{}, false
	}
	selection := r.selections[position]
	selection.choiceSources = append([]ChoiceSource(nil), selection.choiceSources...)
	return selection, true
}

type normalizedContract struct {
	id     capabilityid.Identifier
	json   []byte
	digest string
	source string
}

type normalizedRequirement struct {
	id          capabilityid.Identifier
	contract    normalizedContract
	hasContract bool
	source      RequirementSource
}

type normalizedCandidate struct {
	pluginID string
	contract normalizedContract
}

type normalizedChoice struct {
	capability capabilityid.Identifier
	pluginID   string
	sources    []ChoiceSource
}

type requirementGroup struct {
	id           capabilityid.Identifier
	contractJSON []byte
	digest       string
	sources      []RequirementSource
}

// Resolve validates every input and returns no partial result unless every
// exact requirement resolves. Empty input is a valid empty application.
func Resolve(input Input) (Result, error) {
	requirements, issues := normalizeRequirements(input.Requirements)
	candidates, candidateIssues := normalizeCandidates(input.Candidates)
	choices, choiceIssues := normalizeChoices(input.Choices)
	issues = append(issues, candidateIssues...)
	issues = append(issues, choiceIssues...)
	if len(issues) != 0 {
		return Result{}, resolutionError(issues)
	}

	candidatesByCapability, knownPlugins, issues := groupCandidates(candidates)
	if len(issues) != 0 {
		return Result{}, resolutionError(issues)
	}
	return resolveNormalized(requirements, choices, candidatesByCapability, knownPlugins)
}

// NewCatalog validates and normalizes one complete visible Provider candidate
// set for reuse across deterministic fixed-point passes.
func NewCatalog(inputs []Candidate) (Catalog, error) {
	candidates, issues := normalizeCandidates(inputs)
	if len(issues) != 0 {
		return Catalog{}, resolutionError(issues)
	}
	candidatesByCapability, knownPlugins, issues := groupCandidates(candidates)
	if len(issues) != 0 {
		return Catalog{}, resolutionError(issues)
	}
	return Catalog{
		candidatesByCapability: candidatesByCapability,
		knownPlugins:           knownPlugins,
		valid:                  true,
	}, nil
}

// Resolve validates one changing requirement and choice set against the
// catalog and returns no partial result unless every exact requirement
// resolves.
func (c Catalog) Resolve(requirements []Requirement, choices []Choice) (Result, error) {
	if !c.valid {
		return Result{}, resolutionError([]error{fmt.Errorf("%w: provider catalog is not initialized", ErrInvalidInput)})
	}
	normalizedRequirements, issues := normalizeRequirements(requirements)
	normalizedChoices, choiceIssues := normalizeChoices(choices)
	issues = append(issues, choiceIssues...)
	if len(issues) != 0 {
		return Result{}, resolutionError(issues)
	}
	return resolveNormalized(normalizedRequirements, normalizedChoices, c.candidatesByCapability, c.knownPlugins)
}

func resolveNormalized(
	requirements []normalizedRequirement,
	choices []normalizedChoice,
	candidatesByCapability map[capabilityid.Identifier][]normalizedCandidate,
	knownPlugins map[string]struct{},
) (Result, error) {
	groups, issues := groupRequirements(requirements, candidatesByCapability)
	if len(issues) != 0 {
		return Result{}, resolutionError(issues)
	}
	choicesByCapability, choiceIssues := validateChoices(choices, groups, candidatesByCapability, knownPlugins)
	issues = append(issues, choiceIssues...)

	capabilities := make([]ResolvedCapability, 0, len(groups))
	selections := make([]Selection, 0, len(groups))
	for _, group := range groups {
		intrinsic := isIntrinsic(group.id)
		capabilities = append(capabilities, ResolvedCapability{
			id:           group.id,
			contractJSON: append([]byte(nil), group.contractJSON...),
			digest:       group.digest,
			intrinsic:    intrinsic,
			sources:      append([]RequirementSource(nil), group.sources...),
		})
		if intrinsic {
			continue
		}

		providers := candidatesByCapability[group.id]
		var incompatible []ProviderDetail
		for _, provider := range providers {
			if !bytes.Equal(provider.contract.json, group.contractJSON) {
				incompatible = append(incompatible, providerDetail(provider))
			}
		}
		if len(incompatible) != 0 {
			issues = append(issues, &ProviderContractError{
				capability:      group.id,
				expectedDigest:  group.digest,
				expectedSources: requirementSourceStrings(group.sources),
				providers:       incompatible,
			})
			continue
		}

		choice, explicitlyChosen := choicesByCapability[group.id]
		switch len(providers) {
		case 0:
			issues = append(issues, &MissingProviderError{
				capability: group.id,
				sources:    requirementSourceStrings(group.sources),
			})
		case 1:
			selected := providers[0]
			if explicitlyChosen && choice.pluginID != selected.pluginID {
				continue
			}
			selections = append(selections, newSelection(group.id, selected, choice, explicitlyChosen))
		default:
			if !explicitlyChosen {
				details := make([]ProviderDetail, len(providers))
				for index, provider := range providers {
					details[index] = providerDetail(provider)
				}
				issues = append(issues, &AmbiguousProviderError{
					capability: group.id,
					sources:    requirementSourceStrings(group.sources),
					providers:  details,
				})
				continue
			}
			for _, provider := range providers {
				if provider.pluginID == choice.pluginID {
					selections = append(selections, newSelection(group.id, provider, choice, true))
					break
				}
			}
		}
	}
	if len(issues) != 0 {
		return Result{}, resolutionError(issues)
	}

	capabilityIndex := make(map[capabilityid.Identifier]int, len(capabilities))
	for index, capability := range capabilities {
		capabilityIndex[capability.id] = index
	}
	selectionIndex := make(map[capabilityid.Identifier]int, len(selections))
	for index, selection := range selections {
		selectionIndex[selection.capability] = index
	}
	return Result{
		capabilities:       capabilities,
		capabilityIndex:    capabilityIndex,
		providerCandidates: providerCandidateViews(candidatesByCapability),
		selections:         selections,
		selectionIndex:     selectionIndex,
	}, nil
}

func providerCandidateViews(candidates map[capabilityid.Identifier][]normalizedCandidate) []ProviderCandidate {
	capabilities := make([]capabilityid.Identifier, 0, len(candidates))
	for capability := range candidates {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool {
		return capabilities[left].String() < capabilities[right].String()
	})
	result := make([]ProviderCandidate, 0)
	for _, capability := range capabilities {
		for _, candidate := range candidates[capability] {
			result = append(result, ProviderCandidate{
				capability:     capability,
				pluginID:       candidate.pluginID,
				contractDigest: candidate.contract.digest,
				source:         candidate.contract.source,
			})
		}
	}
	return result
}

func normalizeRequirements(inputs []Requirement) ([]normalizedRequirement, []error) {
	values := make([]normalizedRequirement, 0, len(inputs))
	var issues []error
	for _, input := range inputs {
		source, err := normalizeRequirementSource(input.Source)
		if err != nil {
			issues = append(issues, fmt.Errorf("%w: requirement source: %v", ErrInvalidInput, err))
			continue
		}
		reference := source.String()
		var identifier capabilityid.Identifier
		var contract normalizedContract
		hasContract := len(input.Contract) != 0
		if hasContract {
			contract, err = normalizeContract(input.Contract, reference)
			if err != nil {
				issues = append(issues, fmt.Errorf("%w: requirement at %q: %v", ErrInvalidInput, reference, err))
				continue
			}
			identifier = contract.id
		}
		if input.Capability != "" {
			declared, err := capabilityid.Parse(input.Capability)
			if err != nil {
				issues = append(issues, fmt.Errorf("%w: requirement at %q has non-canonical Capability ID %q", ErrInvalidInput, reference, input.Capability))
				continue
			}
			if hasContract && declared != contract.id {
				issues = append(issues, fmt.Errorf("%w: requirement at %q names %s but its contract declares %s", ErrInvalidInput, reference, declared, contract.id))
				continue
			}
			identifier = declared
		}
		if identifier.String() == "" {
			issues = append(issues, fmt.Errorf("%w: requirement at %q must contain Capability or Contract", ErrInvalidInput, reference))
			continue
		}
		values = append(values, normalizedRequirement{
			id:          identifier,
			contract:    contract,
			hasContract: hasContract,
			source:      source,
		})
	}
	return values, issues
}

func normalizeCandidates(inputs []Candidate) ([]normalizedCandidate, []error) {
	values := make([]normalizedCandidate, 0, len(inputs))
	var issues []error
	for _, input := range inputs {
		source, err := normalizeSource(input.Source)
		if err != nil {
			issues = append(issues, fmt.Errorf("%w: candidate source for plugin %q: %v", ErrInvalidInput, input.PluginID, err))
			continue
		}
		if err := pluginid.Validate(input.PluginID); err != nil {
			issues = append(issues, fmt.Errorf("%w: candidate at %q has non-canonical Plugin ID %q", ErrInvalidInput, source, input.PluginID))
			continue
		}
		contract, err := normalizeContract(input.Contract, source)
		if err != nil {
			issues = append(issues, fmt.Errorf("%w: candidate plugin %q at %q: %v", ErrInvalidInput, input.PluginID, source, err))
			continue
		}
		if isIntrinsic(contract.id) {
			issues = append(issues, fmt.Errorf("%w: %w: plugin %q at %q cannot provide intrinsic Capability %s", ErrInvalidInput, ErrInvalidProvider, input.PluginID, source, contract.id))
			continue
		}
		values = append(values, normalizedCandidate{pluginID: input.PluginID, contract: contract})
	}
	return values, issues
}

func normalizeChoices(inputs []Choice) ([]normalizedChoice, []error) {
	values := make([]normalizedChoice, 0, len(inputs))
	var issues []error
	for _, input := range inputs {
		sources, err := normalizeChoiceSources(input.Sources)
		if err != nil {
			issues = append(issues, fmt.Errorf("%w: choice source for %q -> %q: %v", ErrInvalidInput, input.Capability, input.PluginID, err))
			continue
		}
		source := sources[0].Reference
		capability, err := capabilityid.Parse(input.Capability)
		if err != nil {
			issues = append(issues, fmt.Errorf("%w: choice at %q has non-canonical Capability ID %q", ErrInvalidInput, source, input.Capability))
			continue
		}
		if err := pluginid.Validate(input.PluginID); err != nil {
			issues = append(issues, fmt.Errorf("%w: choice for %s at %q has non-canonical Plugin ID %q", ErrInvalidInput, capability, source, input.PluginID))
			continue
		}
		values = append(values, normalizedChoice{capability: capability, pluginID: input.PluginID, sources: sources})
	}
	return values, issues
}

func normalizeContract(input []byte, source string) (normalizedContract, error) {
	canonical, manifest, err := capabilitymeta.NormalizeSchemaAndManifest(input)
	if err != nil {
		return normalizedContract{}, fmt.Errorf("contract is invalid: %w", err)
	}
	return normalizedContract{
		id:     manifest.ID(),
		json:   append([]byte(nil), canonical...),
		digest: contractDigest(canonical),
		source: source,
	}, nil
}

func groupRequirements(inputs []normalizedRequirement, candidates map[capabilityid.Identifier][]normalizedCandidate) ([]requirementGroup, []error) {
	sort.Slice(inputs, func(left, right int) bool {
		if inputs[left].id != inputs[right].id {
			return inputs[left].id.String() < inputs[right].id.String()
		}
		if inputs[left].hasContract != inputs[right].hasContract {
			return inputs[left].hasContract
		}
		if inputs[left].hasContract {
			if inputs[left].contract.digest != inputs[right].contract.digest {
				return inputs[left].contract.digest < inputs[right].contract.digest
			}
			if compared := bytes.Compare(inputs[left].contract.json, inputs[right].contract.json); compared != 0 {
				return compared < 0
			}
		}
		return requirementSourceKey(inputs[left].source) < requirementSourceKey(inputs[right].source)
	})
	groups := make([]requirementGroup, 0, len(inputs))
	var issues []error
	for first := 0; first < len(inputs); {
		last := first + 1
		for last < len(inputs) && inputs[last].id == inputs[first].id {
			last++
		}
		contracts := make([]normalizedContract, 0, last-first)
		sources := make([]RequirementSource, 0, last-first)
		for _, input := range inputs[first:last] {
			sources = append(sources, input.source)
			if input.hasContract {
				contracts = append(contracts, input.contract)
			}
		}
		sources = uniqueRequirementSources(sources)
		variants := contractVariants(contracts)
		if len(variants) > 1 {
			issues = append(issues, &RequirementConflictError{capability: inputs[first].id, variants: variants})
		} else if len(variants) == 1 {
			groups = append(groups, requirementGroup{
				id:           inputs[first].id,
				contractJSON: append([]byte(nil), contracts[0].json...),
				digest:       contracts[0].digest,
				sources:      sources,
			})
		} else if isIntrinsic(inputs[first].id) {
			issues = append(issues, fmt.Errorf(
				"%w: intrinsic requirement %s from [%s] must include its authoritative Kernel contract",
				ErrInvalidInput,
				inputs[first].id,
				strings.Join(requirementSourceStrings(sources), ", "),
			))
		} else {
			providers := candidates[inputs[first].id]
			if providersHaveDifferentContracts(providers) {
				details := make([]ProviderDetail, len(providers))
				for index, provider := range providers {
					details[index] = providerDetail(provider)
				}
				issues = append(issues, &ProviderContractConflictError{
					capability: inputs[first].id,
					sources:    requirementSourceStrings(sources),
					providers:  details,
				})
			} else {
				group := requirementGroup{id: inputs[first].id, sources: sources}
				if len(providers) != 0 {
					group.contractJSON = append([]byte(nil), providers[0].contract.json...)
					group.digest = providers[0].contract.digest
				}
				groups = append(groups, group)
			}
		}
		first = last
	}
	return groups, issues
}

func groupCandidates(inputs []normalizedCandidate) (map[capabilityid.Identifier][]normalizedCandidate, map[string]struct{}, []error) {
	sort.Slice(inputs, func(left, right int) bool {
		if inputs[left].contract.id != inputs[right].contract.id {
			return inputs[left].contract.id.String() < inputs[right].contract.id.String()
		}
		if inputs[left].pluginID != inputs[right].pluginID {
			return inputs[left].pluginID < inputs[right].pluginID
		}
		return inputs[left].contract.source < inputs[right].contract.source
	})
	byCapability := make(map[capabilityid.Identifier][]normalizedCandidate)
	knownPlugins := make(map[string]struct{})
	var issues []error
	for index, candidate := range inputs {
		knownPlugins[candidate.pluginID] = struct{}{}
		if index > 0 && inputs[index-1].contract.id == candidate.contract.id && inputs[index-1].pluginID == candidate.pluginID {
			issues = append(issues, fmt.Errorf(
				"%w: %w: plugin %q declares %s at both %q and %q",
				ErrInvalidInput,
				ErrInvalidProvider,
				candidate.pluginID,
				candidate.contract.id,
				inputs[index-1].contract.source,
				candidate.contract.source,
			))
			continue
		}
		byCapability[candidate.contract.id] = append(byCapability[candidate.contract.id], candidate)
	}
	return byCapability, knownPlugins, issues
}

func validateChoices(choices []normalizedChoice, requirements []requirementGroup, candidates map[capabilityid.Identifier][]normalizedCandidate, knownPlugins map[string]struct{}) (map[capabilityid.Identifier]normalizedChoice, []error) {
	sort.Slice(choices, func(left, right int) bool {
		if choices[left].capability != choices[right].capability {
			return choices[left].capability.String() < choices[right].capability.String()
		}
		if choices[left].pluginID != choices[right].pluginID {
			return choices[left].pluginID < choices[right].pluginID
		}
		return choiceSourceKey(choices[left].sources[0]) < choiceSourceKey(choices[right].sources[0])
	})
	required := make(map[capabilityid.Identifier]requirementGroup, len(requirements))
	for _, requirement := range requirements {
		required[requirement.id] = requirement
	}
	result := make(map[capabilityid.Identifier]normalizedChoice, len(choices))
	var issues []error
	for index, choice := range choices {
		if index > 0 && choices[index-1].capability == choice.capability {
			issues = append(issues, newChoiceError(choice, ChoiceDuplicate, fmt.Sprintf("another choice at %q selects plugin %q", choices[index-1].sources[0].Reference, choices[index-1].pluginID)))
			delete(result, choice.capability)
			continue
		}
		if _, exists := required[choice.capability]; !exists {
			if _, known := candidates[choice.capability]; known {
				issues = append(issues, newChoiceError(choice, ChoiceUnrequiredCapability, "capabilities.use may select providers only for current requirements"))
			} else {
				issues = append(issues, newChoiceError(choice, ChoiceUnknownCapability, "no canonical requirement or visible provider declares this Capability"))
			}
			continue
		}
		if isIntrinsic(choice.capability) {
			issues = append(issues, newChoiceError(choice, ChoiceIntrinsicCapability, "reserved kernel.* Capabilities have no plugin provider"))
			continue
		}
		providers := candidates[choice.capability]
		provides := false
		for _, provider := range providers {
			if provider.pluginID == choice.pluginID {
				provides = true
				break
			}
		}
		if !provides {
			if _, known := knownPlugins[choice.pluginID]; !known {
				issues = append(issues, newChoiceError(choice, ChoiceUnknownPlugin, "the selected Plugin ID is not visible"))
			} else {
				available := make([]string, len(providers))
				for index, provider := range providers {
					available[index] = provider.pluginID
				}
				detail := "the selected plugin does not provide this exact Capability"
				if len(available) != 0 {
					detail += "; visible providers: [" + strings.Join(available, ", ") + "]"
				}
				issues = append(issues, newChoiceError(choice, ChoiceNonProvider, detail))
			}
			continue
		}
		result[choice.capability] = choice
	}
	return result, issues
}

func newSelection(capability capabilityid.Identifier, provider normalizedCandidate, choice normalizedChoice, explicit bool) Selection {
	selection := Selection{
		capability:       capability,
		pluginID:         provider.pluginID,
		providerSource:   provider.contract.source,
		explicitlyChosen: explicit,
	}
	if explicit {
		selection.choiceSources = append([]ChoiceSource(nil), choice.sources...)
	}
	return selection
}

func newChoiceError(choice normalizedChoice, problem ChoiceProblem, detail string) *ChoiceError {
	return &ChoiceError{
		capability: choice.capability,
		pluginID:   choice.pluginID,
		source:     choice.sources[0].Reference,
		problem:    problem,
		detail:     detail,
	}
}

func contractVariants(inputs []normalizedContract) []ContractVariant {
	variants := make([]ContractVariant, 0)
	for first := 0; first < len(inputs); {
		last := first + 1
		for last < len(inputs) && inputs[last].digest == inputs[first].digest && bytes.Equal(inputs[last].json, inputs[first].json) {
			last++
		}
		sources := make([]string, 0, last-first)
		for _, input := range inputs[first:last] {
			sources = append(sources, input.source)
		}
		variants = append(variants, ContractVariant{digest: inputs[first].digest, sources: uniqueStrings(sources)})
		first = last
	}
	return variants
}

func providerDetail(candidate normalizedCandidate) ProviderDetail {
	return ProviderDetail{
		pluginID: candidate.pluginID,
		source:   candidate.contract.source,
		digest:   candidate.contract.digest,
	}
}

func providersHaveDifferentContracts(providers []normalizedCandidate) bool {
	if len(providers) < 2 {
		return false
	}
	baseline := providers[0].contract.json
	for _, provider := range providers[1:] {
		if !bytes.Equal(baseline, provider.contract.json) {
			return true
		}
	}
	return false
}

func normalizeSource(value string) (string, error) {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("must be non-empty valid single-line UTF-8, at most 1024 bytes")
	}
	return value, nil
}

func normalizeChoiceSources(inputs []ChoiceSource) ([]ChoiceSource, error) {
	if len(inputs) == 0 {
		return nil, errors.New("at least one typed source is required")
	}
	values := make([]ChoiceSource, 0, len(inputs))
	var kind ChoiceSourceKind
	for index, input := range inputs {
		reference, err := normalizeSource(input.Reference)
		if err != nil {
			return nil, fmt.Errorf("sources[%d].reference %v", index, err)
		}
		switch input.Kind {
		case ChoiceSourceCurrentProject, ChoiceSourceDependencyProject:
		default:
			return nil, fmt.Errorf("sources[%d].kind %q is invalid", index, input.Kind)
		}
		if kind == "" {
			kind = input.Kind
		} else if input.Kind != kind {
			return nil, errors.New("sources cannot mix current-Project and dependency-Project declarations")
		}
		if err := modulepath.CheckProject(input.ModulePath); err != nil {
			return nil, fmt.Errorf("sources[%d].module_path %q is invalid: %v", index, input.ModulePath, err)
		}
		if input.Path == "" || path.IsAbs(input.Path) || path.Clean(input.Path) != input.Path || input.Path == "." || input.Path == ".." || strings.HasPrefix(input.Path, "../") || strings.Contains(input.Path, "/../") || strings.Contains(input.Path, "\\") || strings.ContainsAny(input.Path, "\x00\r\n") || !utf8.ValidString(input.Path) {
			return nil, fmt.Errorf("sources[%d].path %q must be one safe module-relative slash path", index, input.Path)
		}
		if input.Line < 1 || input.Column < 1 {
			return nil, fmt.Errorf("sources[%d].line and column must be positive", index)
		}
		input.Reference = reference
		values = append(values, input)
	}
	sort.Slice(values, func(left, right int) bool {
		leftKey := choiceSourceKey(values[left])
		rightKey := choiceSourceKey(values[right])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return values[left].Reference < values[right].Reference
	})
	unique := values[:0]
	for _, value := range values {
		if len(unique) != 0 && choiceSourceKey(unique[len(unique)-1]) == choiceSourceKey(value) {
			continue
		}
		unique = append(unique, value)
	}
	if kind == ChoiceSourceCurrentProject && len(unique) != 1 {
		return nil, errors.New("a current-Project choice must have exactly one winning source")
	}
	return append([]ChoiceSource(nil), unique...), nil
}

func choiceSourceKey(value ChoiceSource) string {
	return strings.Join([]string{
		string(value.Kind),
		value.ModulePath,
		value.Path,
		fmt.Sprintf("%010d", value.Line),
		fmt.Sprintf("%010d", value.Column),
	}, "\x00")
}

func normalizeRequirementSource(input RequirementSource) (RequirementSource, error) {
	reference, err := normalizeSource(input.Reference)
	if err != nil {
		return RequirementSource{}, fmt.Errorf("reference %v", err)
	}
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return RequirementSource{}, fmt.Errorf("module path %q is invalid: %v", input.ModulePath, err)
	}
	if input.Path == "" || path.IsAbs(input.Path) || path.Clean(input.Path) != input.Path || input.Path == "." || input.Path == ".." || strings.HasPrefix(input.Path, "../") || strings.Contains(input.Path, "/../") || strings.Contains(input.Path, "\\") || strings.ContainsAny(input.Path, "\x00\r\n") || !utf8.ValidString(input.Path) {
		return RequirementSource{}, fmt.Errorf("path %q must be one safe module-relative slash path", input.Path)
	}
	if input.Line < 1 || input.Column < 1 {
		return RequirementSource{}, errors.New("line and column must be positive")
	}

	empty := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return false
			}
		}
		return true
	}
	validatePlugin := func() error {
		if err := pluginid.Validate(input.PluginID); err != nil {
			return fmt.Errorf("plugin ID %q is invalid", input.PluginID)
		}
		return nil
	}
	validateSourceCapability := func() error {
		if _, err := capabilityid.Parse(input.SourceCapability); err != nil {
			return fmt.Errorf("source Capability %q is invalid", input.SourceCapability)
		}
		return nil
	}

	switch input.Kind {
	case RequirementDeclaration, RequirementExposure:
		if !empty(input.PluginID, input.Alias, input.Namespace, input.SourceCapability, input.RuleID) {
			return RequirementSource{}, fmt.Errorf("kind %q contains unrelated semantic fields", input.Kind)
		}
	case RequirementGeneratedClient, RequirementPlugin:
		if err := validatePlugin(); err != nil {
			return RequirementSource{}, err
		}
		if !empty(input.Alias, input.Namespace, input.SourceCapability, input.RuleID) {
			return RequirementSource{}, fmt.Errorf("kind %q contains unrelated semantic fields", input.Kind)
		}
	case RequirementAliasTarget:
		if _, err := capabilityid.Parse(input.Alias); err != nil {
			return RequirementSource{}, fmt.Errorf("alias %q is invalid", input.Alias)
		}
		if !empty(input.PluginID, input.Namespace, input.SourceCapability, input.RuleID) {
			return RequirementSource{}, fmt.Errorf("kind %q contains unrelated semantic fields", input.Kind)
		}
	case RequirementActivation:
		if !validLowerKebab(input.Namespace) {
			return RequirementSource{}, fmt.Errorf("namespace %q is invalid", input.Namespace)
		}
		if err := validateSourceCapability(); err != nil {
			return RequirementSource{}, err
		}
		if !empty(input.PluginID, input.Alias, input.RuleID) {
			return RequirementSource{}, fmt.Errorf("kind %q contains unrelated semantic fields", input.Kind)
		}
	case RequirementGenerationRule:
		if err := validatePlugin(); err != nil {
			return RequirementSource{}, err
		}
		if !validLowerKebab(input.Namespace) {
			return RequirementSource{}, fmt.Errorf("namespace %q is invalid", input.Namespace)
		}
		if err := validateSourceCapability(); err != nil {
			return RequirementSource{}, err
		}
		if _, err := normalizeSource(input.RuleID); err != nil {
			return RequirementSource{}, fmt.Errorf("rule ID %v", err)
		}
		if input.Alias != "" {
			return RequirementSource{}, fmt.Errorf("kind %q contains an unrelated Alias", input.Kind)
		}
	default:
		return RequirementSource{}, fmt.Errorf("kind %q is invalid", input.Kind)
	}
	input.Reference = reference
	return input, nil
}

func validLowerKebab(value string) bool {
	if value == "" || len(value) > 128 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	previousHyphen := false
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return true
}

func requirementSourceKey(value RequirementSource) string {
	return strings.Join([]string{
		string(value.Kind),
		value.ModulePath,
		value.Path,
		fmt.Sprintf("%010d", value.Line),
		fmt.Sprintf("%010d", value.Column),
		value.PluginID,
		value.Alias,
		value.Namespace,
		value.SourceCapability,
		value.RuleID,
		value.Reference,
	}, "\x00")
}

func uniqueRequirementSources(values []RequirementSource) []RequirementSource {
	sort.Slice(values, func(left, right int) bool {
		return requirementSourceKey(values[left]) < requirementSourceKey(values[right])
	})
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return append([]RequirementSource(nil), result...)
}

func requirementSourceStrings(values []RequirementSource) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return uniqueStrings(result)
}

func isIntrinsic(id capabilityid.Identifier) bool {
	return strings.HasPrefix(id.Name(), "kernel.")
}

func contractDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func uniqueStrings(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return append([]string(nil), result...)
}

func resolutionError(issues []error) error {
	sort.SliceStable(issues, func(left, right int) bool {
		return issues[left].Error() < issues[right].Error()
	})
	return &ResolutionError{issues: append([]error(nil), issues...)}
}

// ResolutionError contains every independently diagnosable failure discovered
// during one deterministic resolution pass.
type ResolutionError struct {
	issues []error
}

// Issues returns a defensive copy in deterministic diagnostic order.
func (e *ResolutionError) Issues() []error {
	if e == nil {
		return nil
	}
	return append([]error(nil), e.issues...)
}

func (e *ResolutionError) Error() string {
	if e == nil {
		return ErrResolve.Error()
	}
	var message strings.Builder
	message.WriteString(ErrResolve.Error())
	for _, issue := range e.issues {
		message.WriteString("; ")
		message.WriteString(issue.Error())
	}
	return message.String()
}

// Unwrap supports errors.Is and errors.As for the overall and specific causes.
func (e *ResolutionError) Unwrap() []error {
	if e == nil {
		return []error{ErrResolve}
	}
	causes := make([]error, 1, len(e.issues)+1)
	causes[0] = ErrResolve
	causes = append(causes, e.issues...)
	return causes
}

// ContractVariant identifies one exact contract required under a shared ID.
type ContractVariant struct {
	digest  string
	sources []string
}

// Digest returns the exact canonical contract digest.
func (v ContractVariant) Digest() string { return v.digest }

// Sources returns every sorted provenance location requiring this variant.
func (v ContractVariant) Sources() []string { return append([]string(nil), v.sources...) }

// RequirementConflictError reports multiple exact contracts under one ID.
type RequirementConflictError struct {
	capability capabilityid.Identifier
	variants   []ContractVariant
}

// Capability returns the conflicting exact ID.
func (e *RequirementConflictError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// Variants returns defensive copies sorted by digest.
func (e *RequirementConflictError) Variants() []ContractVariant {
	if e == nil {
		return nil
	}
	return append([]ContractVariant(nil), e.variants...)
}

func (e *RequirementConflictError) Error() string {
	if e == nil {
		return ErrRequirementConflict.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: %s is required with different exact contracts", ErrRequirementConflict, e.capability)
	for _, variant := range e.variants {
		fmt.Fprintf(&message, "; %s from [%s]", variant.digest, strings.Join(variant.sources, ", "))
	}
	fmt.Fprintf(&message, "; correction: every source must require one provider-independent contract, or a semantic change must use a new /vN")
	return message.String()
}

// Unwrap supports errors.Is with ErrRequirementConflict.
func (*RequirementConflictError) Unwrap() error { return ErrRequirementConflict }

// ProviderDetail records one complete candidate used in a diagnostic.
type ProviderDetail struct {
	pluginID string
	source   string
	digest   string
}

// PluginID returns the candidate Plugin ID.
func (p ProviderDetail) PluginID() string { return p.pluginID }

// Source returns provider declaration provenance.
func (p ProviderDetail) Source() string { return p.source }

// ContractDigest returns the candidate's exact contract digest.
func (p ProviderDetail) ContractDigest() string { return p.digest }

// ProviderContractConflictError reports provider copies that disagree when a
// requirement contributes only an exact Capability ID and no baseline contract.
type ProviderContractConflictError struct {
	capability capabilityid.Identifier
	sources    []string
	providers  []ProviderDetail
}

// Capability returns the exact ID with conflicting provider contracts.
func (e *ProviderContractConflictError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// Sources returns sorted reference-only requirement provenance.
func (e *ProviderContractConflictError) Sources() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.sources...)
}

// Providers returns every conflicting candidate in Plugin ID order.
func (e *ProviderContractConflictError) Providers() []ProviderDetail {
	if e == nil {
		return nil
	}
	return append([]ProviderDetail(nil), e.providers...)
}

func (e *ProviderContractConflictError) Error() string {
	if e == nil {
		return ErrProviderContract.Error()
	}
	var message strings.Builder
	fmt.Fprintf(
		&message,
		"%s: %s referenced by [%s] has providers carrying different exact contracts",
		ErrProviderContract,
		e.capability,
		strings.Join(e.sources, ", "),
	)
	for _, provider := range e.providers {
		fmt.Fprintf(&message, "; plugin %q at %q carries %s", provider.pluginID, provider.source, provider.digest)
	}
	fmt.Fprintf(
		&message,
		"; correction: make every provider carry one provider-independent contract, including closed field constraints, typed semantics, and normalized extension metadata, or use a new /vN; no provider contract is chosen by ordering",
	)
	return message.String()
}

// Unwrap supports errors.Is with ErrProviderContract.
func (*ProviderContractConflictError) Unwrap() error { return ErrProviderContract }

// ProviderContractError reports candidates that do not carry the required exact contract.
type ProviderContractError struct {
	capability      capabilityid.Identifier
	expectedDigest  string
	expectedSources []string
	providers       []ProviderDetail
}

// Capability returns the incompatible exact ID.
func (e *ProviderContractError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// ExpectedDigest returns the required contract digest.
func (e *ProviderContractError) ExpectedDigest() string {
	if e == nil {
		return ""
	}
	return e.expectedDigest
}

// ExpectedSources returns sorted requirement provenance.
func (e *ProviderContractError) ExpectedSources() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.expectedSources...)
}

// Providers returns every incompatible candidate in Plugin ID order.
func (e *ProviderContractError) Providers() []ProviderDetail {
	if e == nil {
		return nil
	}
	return append([]ProviderDetail(nil), e.providers...)
}

func (e *ProviderContractError) Error() string {
	if e == nil {
		return ErrProviderContract.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s: %s requires %s from [%s]", ErrProviderContract, e.capability, e.expectedDigest, strings.Join(e.expectedSources, ", "))
	for _, provider := range e.providers {
		fmt.Fprintf(&message, "; plugin %q at %q carries %s", provider.pluginID, provider.source, provider.digest)
	}
	fmt.Fprintf(&message, "; correction: every provider must carry the exact canonical contract, including closed field constraints, typed semantics, and normalized extension metadata, or use a new /vN")
	return message.String()
}

// Unwrap supports errors.Is with ErrProviderContract.
func (*ProviderContractError) Unwrap() error { return ErrProviderContract }

// MissingProviderError reports one unresolved ordinary requirement.
type MissingProviderError struct {
	capability capabilityid.Identifier
	sources    []string
}

// Capability returns the missing exact ID.
func (e *MissingProviderError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// Sources returns sorted requirement provenance.
func (e *MissingProviderError) Sources() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.sources...)
}

func (e *MissingProviderError) Error() string {
	if e == nil {
		return ErrMissingProvider.Error()
	}
	return fmt.Sprintf(
		"%s: %s required by [%s] has no visible provider; correction: add an intended module containing a plugin that provides the exact contract, or remove the requirement",
		ErrMissingProvider,
		e.capability,
		strings.Join(e.sources, ", "),
	)
}

// Unwrap supports errors.Is with ErrMissingProvider.
func (*MissingProviderError) Unwrap() error { return ErrMissingProvider }

// AmbiguousProviderError reports every compatible provider requiring an explicit choice.
type AmbiguousProviderError struct {
	capability capabilityid.Identifier
	sources    []string
	providers  []ProviderDetail
}

// Capability returns the ambiguous exact ID.
func (e *AmbiguousProviderError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// Sources returns sorted requirement provenance.
func (e *AmbiguousProviderError) Sources() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.sources...)
}

// Providers returns every compatible candidate in Plugin ID order.
func (e *AmbiguousProviderError) Providers() []ProviderDetail {
	if e == nil {
		return nil
	}
	return append([]ProviderDetail(nil), e.providers...)
}

func (e *AmbiguousProviderError) Error() string {
	if e == nil {
		return ErrAmbiguousProvider.Error()
	}
	providers := make([]string, len(e.providers))
	for index, provider := range e.providers {
		providers[index] = fmt.Sprintf("%s at %s", provider.pluginID, provider.source)
	}
	return fmt.Sprintf(
		"%s: %s required by [%s] has compatible providers [%s]; correction: run `plystra use %s <plugin-id>` to set capabilities.use[%s] to one Plugin ID; no priority, status, discovery-order, filesystem-order, or alphabetical fallback is applied",
		ErrAmbiguousProvider,
		e.capability,
		strings.Join(e.sources, ", "),
		strings.Join(providers, ", "),
		e.capability,
		e.capability,
	)
}

// Unwrap supports errors.Is with ErrAmbiguousProvider.
func (*AmbiguousProviderError) Unwrap() error { return ErrAmbiguousProvider }

// ChoiceError reports why one explicit provider choice is invalid.
type ChoiceError struct {
	capability capabilityid.Identifier
	pluginID   string
	source     string
	problem    ChoiceProblem
	detail     string
}

// Capability returns the selected exact ID.
func (e *ChoiceError) Capability() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.capability
}

// PluginID returns the selected Plugin ID.
func (e *ChoiceError) PluginID() string {
	if e == nil {
		return ""
	}
	return e.pluginID
}

// Source returns choice provenance.
func (e *ChoiceError) Source() string {
	if e == nil {
		return ""
	}
	return e.source
}

// Problem returns a stable machine-readable problem label.
func (e *ChoiceError) Problem() ChoiceProblem {
	if e == nil {
		return ChoiceProblem("")
	}
	return e.problem
}

func (e *ChoiceError) Error() string {
	if e == nil {
		return ErrInvalidChoice.Error()
	}
	return fmt.Sprintf("%s: %s -> %s at %q is %s: %s", ErrInvalidChoice, e.capability, e.pluginID, e.source, e.problem, e.detail)
}

// Unwrap supports errors.Is with ErrInvalidChoice.
func (*ChoiceError) Unwrap() error { return ErrInvalidChoice }
