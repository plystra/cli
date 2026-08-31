package applicationmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

var (
	// ErrCompose reports that typed Project configuration composition failed.
	ErrCompose = errors.New("compose Project configuration")
	// ErrHTTPTransportSelection reports an effective public HTTP surface with
	// no selected external transport.
	ErrHTTPTransportSelection = errors.New("invalid HTTP transport selection")
	// ErrInheritedConflict reports incompatible dependency declarations that
	// the current Project did not explicitly replace.
	ErrInheritedConflict = errors.New("inherited Project configuration conflict")
	// ErrConfigurationSchema reports configuration for a constructor without
	// one compiled same-package Config schema.
	ErrConfigurationSchema = errors.New("constructor configuration schema unavailable")
	// ErrConfigurationValues reports constructor configuration that does not
	// conform to its compiled Go Config schema. Values never enter this error.
	ErrConfigurationValues = errors.New("invalid constructor configuration values")
	// ErrConfigurationUnknownField reports an undeclared configuration field.
	ErrConfigurationUnknownField = errors.New("unknown constructor configuration field")
	// ErrConfigurationInvalidValue reports a value that does not match its
	// compiled Go type. The value and Secret reference target remain redacted.
	ErrConfigurationInvalidValue = errors.New("invalid constructor configuration field value")
)

// Dependency is one dependency Project's parsed root configuration and stable
// Go Module provenance. Directness and graph position deliberately do not
// participate in composition.
type Dependency struct {
	ModulePath    string
	ModuleVersion string
	Manifest      Manifest
}

// SchemaLookup returns the compiled same-package Config schema for one exact
// visible Implementation constructor.
type SchemaLookup func(constructor constructorsymbol.Symbol) (implementationinventory.Configuration, bool)

// Provenance records one dependency-derived typed field value without storing
// the value itself. Several records may share a Path when dependencies
// contribute incompatible values that a current-project replacement resolves.
type Provenance struct {
	path    string
	digest  string
	removed bool
	sources []string
}

// Path returns the stable schema field or canonical declaration key.
func (p Provenance) Path() string { return p.path }

// Digest returns the normalized value digest.
func (p Provenance) Digest() string { return p.digest }

// Removed reports whether this baseline record is an explicit typed
// tombstone rather than a contributed value.
func (p Provenance) Removed() bool { return p.removed }

// Sources returns every contributing module and YAML field path in stable
// lexical order.
func (p Provenance) Sources() []string { return append([]string(nil), p.sources...) }

// Composition is one immutable effective Manifest plus its complete
// dependency-derived non-secret provenance.
type Composition struct {
	manifest          Manifest
	provenance        []Provenance
	resolutionSources []Provenance
	dependencyDigest  string
	prepared          bool
}

// DependencyBaseline returns the validated non-secret dependency provenance
// needed for schema-aware current-project maintenance.
func (c Composition) DependencyBaseline() DependencyBaseline {
	if !c.Valid() {
		return DependencyBaseline{}
	}
	return DependencyBaseline{
		records:  c.Provenance(),
		digest:   c.dependencyDigest,
		prepared: true,
	}
}

// Valid reports whether the value was produced by Compose.
func (c Composition) Valid() bool {
	return c.prepared && validCompositionDigest(c.dependencyDigest)
}

// Manifest returns the effective typed application declaration.
func (c Composition) Manifest() Manifest {
	if !c.Valid() {
		return Manifest{}
	}
	return c.manifest
}

// Provenance returns defensive path-and-digest-sorted dependency baseline
// records.
func (c Composition) Provenance() []Provenance {
	if !c.Valid() {
		return nil
	}
	return cloneProvenance(c.provenance)
}

// ResolutionSources returns dependency provenance whose normalized value
// matches one effective Interface or legacy requirement, selection, or
// remaining Alias. Public exposure is current-Project-owned and therefore has
// no dependency provenance. Superseded and removed dependency declarations
// remain in Provenance but do not introduce final application requirements.
func (c Composition) ResolutionSources() []Provenance {
	if !c.Valid() {
		return nil
	}
	return cloneProvenance(c.resolutionSources)
}

// DependencyDigest returns the stable digest of dependency-derived normalized
// values and all-source provenance. It contains no configuration values or
// resolved Secrets.
func (c Composition) DependencyDigest() string {
	if !c.Valid() {
		return ""
	}
	return c.dependencyDigest
}

// Compose applies the typed dependency rules beneath one current-project
// Manifest. Process settings and public exposure remain current-project-owned;
// composable canonical sets union; keyed Implementation selections and
// remaining keyed declarations require compatible inherited values unless the
// current Project replaces that exact key.
func Compose(dependencies []Dependency, current Manifest, schemas SchemaLookup) (Composition, error) {
	if schemas == nil {
		return Composition{}, fmt.Errorf("%w: schema lookup is nil", ErrCompose)
	}
	ordered := append([]Dependency(nil), dependencies...)
	for _, dependency := range ordered {
		if dependency.ModulePath == "" {
			return Composition{}, fmt.Errorf("%w: dependency module path is empty", ErrCompose)
		}
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].ModulePath != ordered[right].ModulePath {
			return ordered[left].ModulePath < ordered[right].ModulePath
		}
		return ordered[left].ModuleVersion < ordered[right].ModuleVersion
	})
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].ModulePath == ordered[index].ModulePath {
			return Composition{}, fmt.Errorf("%w: dependency module %q is repeated", ErrCompose, ordered[index].ModulePath)
		}
	}

	records := make(map[string]*provenanceRecord)
	exposures := current.HTTPExposures()
	requirements, err := composeRequirementSet(ordered, current.Requirements(), current.removedRequirements, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	choices, err := composeProviderChoices(ordered, current.ProviderChoices(), current.removedProviderChoices, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	interfaceRequirements, err := composeInterfaceRequirementSet(ordered, current.InterfaceRequirements(), current.removedInterfaceReqs, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	implementationChoices, err := composeImplementationChoices(ordered, current.ImplementationChoices(), current.removedImplementationChoices, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	interfacePolicies, err := composeInterfacePolicies(ordered, current.InterfacePolicies(), current.removedInterfacePolicies, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	aliases, err := composeAliases(ordered, current.Aliases(), current.removedAliases, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	configurations, err := composeConstructorConfigurations(ordered, current, schemas, records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	if err := rejectAliasResolutionInputs(requirements, choices, aliases); err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	if err := rejectAliasChains(aliases); err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}

	provenance := finalizeProvenance(records)
	digest, err := digestProvenance(provenance)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: encode dependency provenance: %v", ErrCompose, err)
	}
	manifest := Manifest{
		source:                current.source,
		httpAddress:           current.httpAddress,
		hasHTTPAddress:        current.hasHTTPAddress,
		removeHTTPAddress:     current.removeHTTPAddress,
		httpTransports:        current.httpTransports,
		httpCORS:              cloneHTTPCORSLayer(current.httpCORS),
		httpExposures:         exposures,
		requirements:          requirements,
		providerChoices:       choices,
		interfaceRequirements: interfaceRequirements,
		implementationChoices: implementationChoices,
		interfacePolicies:     interfacePolicies,
		aliases:               aliases,
		configurations:        configurations,
		startupTimeout:        current.startupTimeout,
		hasStartupTimeout:     current.hasStartupTimeout,
		removeStartupTimeout:  current.removeStartupTimeout,
	}
	if err := validateHTTPTransportSelection(manifest); err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	return Composition{
		manifest:          manifest,
		provenance:        provenance,
		resolutionSources: effectiveResolutionSources(manifest, provenance),
		dependencyDigest:  digest,
		prepared:          true,
	}, nil
}

func cloneProvenance(values []Provenance) []Provenance {
	result := make([]Provenance, len(values))
	for index := range values {
		result[index] = Provenance{
			path:    values[index].path,
			digest:  values[index].digest,
			removed: values[index].removed,
			sources: append([]string(nil), values[index].sources...),
		}
	}
	return result
}

func effectiveResolutionSources(manifest Manifest, provenance []Provenance) []Provenance {
	effective := make(map[string]string)
	for _, exposure := range manifest.httpExposures {
		path := fmt.Sprintf("http.expose[%q]", exposure.id.String())
		effective[path] = interfaceDeclarationDigest("http.expose", exposure.id, false)
	}
	for _, requirement := range manifest.requirements {
		path := fmt.Sprintf("capabilities.require[%q]", requirement.id.String())
		effective[path] = declarationDigest("capabilities.require", requirement.id, false)
	}
	for _, choice := range manifest.providerChoices {
		path := fmt.Sprintf("capabilities.use[%q]", choice.capability.String())
		effective[path] = digestStrings("capabilities.use", choice.capability.String(), choice.pluginID)
	}
	for _, requirement := range manifest.interfaceRequirements {
		path := fmt.Sprintf("interfaces.require[%q]", requirement.id.String())
		effective[path] = interfaceDeclarationDigest("interfaces.require", requirement.id, false)
	}
	for _, choice := range manifest.implementationChoices {
		path := fmt.Sprintf("interfaces.use[%q]", choice.interfaceID.String())
		effective[path] = implementationChoiceDigest(choice)
	}
	for _, alias := range manifest.aliases {
		path := fmt.Sprintf("capabilities.aliases[%q]", alias.id.String())
		effective[path] = aliasDigest(alias)
	}
	result := make([]Provenance, 0, len(effective))
	for _, record := range provenance {
		if record.removed || effective[record.path] != record.digest {
			continue
		}
		result = append(result, record)
	}
	return cloneProvenance(result)
}

func validateHTTPTransportSelection(manifest Manifest) error {
	exposures := manifest.HTTPExposures()
	if len(exposures) == 0 {
		return nil
	}
	transports := manifest.HTTPTransports()
	if transports.Connect || transports.REST {
		return nil
	}

	declared := make([]string, len(exposures))
	for index, exposure := range exposures {
		declared[index] = fmt.Sprintf("%s at %s", exposure.ID(), exposure.Source())
	}
	return fmt.Errorf(
		"%w: http.expose is nonempty while http.transports.connect and http.transports.rest are both false; enable at least one transport in the selected current-project configuration; exposed Interfaces: %s",
		ErrHTTPTransportSelection,
		strings.Join(declared, ", "),
	)
}

type provenanceRecord struct {
	path    string
	digest  string
	removed bool
	sources map[string]struct{}
}

func addProvenance(records map[string]*provenanceRecord, path, digest, source string, removed bool) {
	key := path + "\x00" + digest + fmt.Sprintf("\x00%t", removed)
	record, exists := records[key]
	if !exists {
		record = &provenanceRecord{path: path, digest: digest, removed: removed, sources: make(map[string]struct{})}
		records[key] = record
	}
	record.sources[source] = struct{}{}
}

type setCandidate struct {
	valueSources   map[string]struct{}
	removalSources map[string]struct{}
}

func composeRequirementSet(dependencies []Dependency, current []CapabilityRequirement, currentRemovals []capabilityRemoval, records map[string]*provenanceRecord) ([]CapabilityRequirement, error) {
	inherited := make(map[capabilityid.Identifier]*setCandidate)
	for _, dependency := range dependencies {
		for _, requirement := range dependency.Manifest.Requirements() {
			path := fmt.Sprintf("capabilities.require[%q]", requirement.id.String())
			source := dependencySource(dependency, requirement.source)
			addProvenance(records, path, declarationDigest("capabilities.require", requirement.id, false), source, false)
			candidate := ensureSetCandidate(inherited, requirement.id)
			candidate.valueSources[source] = struct{}{}
		}
		for _, removal := range dependency.Manifest.removedRequirements {
			path := fmt.Sprintf("capabilities.require[%q]", removal.id.String())
			source := dependencySource(dependency, removal.source)
			addProvenance(records, path, declarationDigest("capabilities.require", removal.id, true), source, true)
			candidate := ensureSetCandidate(inherited, removal.id)
			candidate.removalSources[source] = struct{}{}
		}
	}
	values := make(map[capabilityid.Identifier]CapabilityRequirement)
	for _, requirement := range current {
		values[requirement.id] = requirement
	}
	removed := capabilityRemovalSet(currentRemovals)
	ids := sortedCandidateIDs(inherited)
	for _, id := range ids {
		if _, replaced := values[id]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[id]; explicitlyRemoved {
			continue
		}
		candidate := inherited[id]
		if len(candidate.valueSources) > 0 && len(candidate.removalSources) > 0 {
			return nil, inheritedSetConflict("capabilities.require", id, candidate)
		}
		if len(candidate.valueSources) > 0 {
			values[id] = CapabilityRequirement{id: id, source: sortedSet(candidate.valueSources)[0]}
		}
	}
	result := make([]CapabilityRequirement, 0, len(values))
	for _, requirement := range values {
		result = append(result, requirement)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, nil
}

func ensureSetCandidate(values map[capabilityid.Identifier]*setCandidate, id capabilityid.Identifier) *setCandidate {
	candidate := values[id]
	if candidate == nil {
		candidate = &setCandidate{valueSources: make(map[string]struct{}), removalSources: make(map[string]struct{})}
		values[id] = candidate
	}
	return candidate
}

func sortedCandidateIDs(values map[capabilityid.Identifier]*setCandidate) []capabilityid.Identifier {
	ids := make([]capabilityid.Identifier, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	return ids
}

func capabilityRemovalSet(values []capabilityRemoval) map[capabilityid.Identifier]struct{} {
	result := make(map[capabilityid.Identifier]struct{}, len(values))
	for _, value := range values {
		result[value.id] = struct{}{}
	}
	return result
}

func inheritedSetConflict(path string, id capabilityid.Identifier, candidate *setCandidate) error {
	return fmt.Errorf(
		"%w: %s[%q] is added by %s and removed by %s; explicitly add or remove that exact Capability in the current Project configuration",
		ErrInheritedConflict,
		path,
		id.String(),
		strings.Join(sortedSet(candidate.valueSources), ", "),
		strings.Join(sortedSet(candidate.removalSources), ", "),
	)
}

type providerCandidate struct {
	choice  ProviderChoice
	removed bool
	sources map[string]struct{}
}

func composeProviderChoices(dependencies []Dependency, current []ProviderChoice, currentRemovals []capabilityRemoval, records map[string]*provenanceRecord) ([]ProviderChoice, error) {
	inherited := make(map[capabilityid.Identifier]map[string]*providerCandidate)
	for _, dependency := range dependencies {
		for _, choice := range dependency.Manifest.ProviderChoices() {
			path := fmt.Sprintf("capabilities.use[%q]", choice.capability.String())
			source := dependencySource(dependency, choice.source)
			digest := digestStrings("capabilities.use", choice.capability.String(), choice.pluginID)
			addProvenance(records, path, digest, source, false)
			byDigest := inherited[choice.capability]
			if byDigest == nil {
				byDigest = make(map[string]*providerCandidate)
				inherited[choice.capability] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				candidate = &providerCandidate{choice: ProviderChoice{capability: choice.capability, pluginID: choice.pluginID, source: source}, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
			if source < candidate.choice.source {
				candidate.choice.source = source
			}
		}
		for _, removal := range dependency.Manifest.removedProviderChoices {
			path := fmt.Sprintf("capabilities.use[%q]", removal.id.String())
			source := dependencySource(dependency, removal.source)
			digest := declarationDigest("capabilities.use", removal.id, true)
			addProvenance(records, path, digest, source, true)
			byDigest := inherited[removal.id]
			if byDigest == nil {
				byDigest = make(map[string]*providerCandidate)
				inherited[removal.id] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				candidate = &providerCandidate{removed: true, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
		}
	}
	selected := make(map[capabilityid.Identifier]ProviderChoice)
	for _, choice := range current {
		selected[choice.capability] = choice
	}
	removed := capabilityRemovalSet(currentRemovals)
	capabilities := make([]capabilityid.Identifier, 0, len(inherited))
	for capability := range inherited {
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left].String() < capabilities[right].String() })
	for _, capability := range capabilities {
		candidates := inherited[capability]
		if _, replaced := selected[capability]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[capability]; explicitlyRemoved {
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedProviderConflict(capability, candidates)
		}
		for _, candidate := range candidates {
			if !candidate.removed {
				selected[capability] = candidate.choice
			}
		}
	}
	result := make([]ProviderChoice, 0, len(selected))
	for _, choice := range selected {
		result = append(result, choice)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].capability.String() < result[right].capability.String()
	})
	return result, nil
}

func inheritedProviderConflict(capability capabilityid.Identifier, candidates map[string]*providerCandidate) error {
	digests := make([]string, 0, len(candidates))
	for digest := range candidates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	parts := make([]string, 0, len(digests))
	for _, digest := range digests {
		candidate := candidates[digest]
		declaration := candidate.choice.pluginID
		if candidate.removed {
			declaration = "<removed>"
		}
		parts = append(parts, fmt.Sprintf("%s from %s", declaration, strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: capabilities.use[%q] has incompatible Provider declarations: %s; set or remove that exact key in the current Project configuration", ErrInheritedConflict, capability.String(), strings.Join(parts, "; "))
}

type aliasCandidate struct {
	alias   Alias
	removed bool
	sources map[string]struct{}
}

func composeAliases(dependencies []Dependency, current []Alias, currentRemovals []capabilityRemoval, records map[string]*provenanceRecord) ([]Alias, error) {
	inherited := make(map[capabilityid.Identifier]map[string]*aliasCandidate)
	for _, dependency := range dependencies {
		for _, alias := range dependency.Manifest.Aliases() {
			path := fmt.Sprintf("capabilities.aliases[%q]", alias.id.String())
			source := dependencySource(dependency, alias.source)
			digest := aliasDigest(alias)
			addProvenance(records, path, digest, source, false)
			byDigest := inherited[alias.id]
			if byDigest == nil {
				byDigest = make(map[string]*aliasCandidate)
				inherited[alias.id] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				alias.source = source
				candidate = &aliasCandidate{alias: alias, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
			if source < candidate.alias.source {
				candidate.alias.source = source
			}
		}
		for _, removal := range dependency.Manifest.removedAliases {
			path := fmt.Sprintf("capabilities.aliases[%q]", removal.id.String())
			source := dependencySource(dependency, removal.source)
			digest := declarationDigest("capabilities.aliases", removal.id, true)
			addProvenance(records, path, digest, source, true)
			byDigest := inherited[removal.id]
			if byDigest == nil {
				byDigest = make(map[string]*aliasCandidate)
				inherited[removal.id] = byDigest
			}
			candidate := byDigest[digest]
			if candidate == nil {
				candidate = &aliasCandidate{removed: true, sources: make(map[string]struct{})}
				byDigest[digest] = candidate
			}
			candidate.sources[source] = struct{}{}
		}
	}
	selected := make(map[capabilityid.Identifier]Alias)
	for _, alias := range current {
		selected[alias.id] = alias
	}
	removed := capabilityRemovalSet(currentRemovals)
	ids := make([]capabilityid.Identifier, 0, len(inherited))
	for id := range inherited {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left].String() < ids[right].String() })
	for _, id := range ids {
		candidates := inherited[id]
		if _, replaced := selected[id]; replaced {
			continue
		}
		if _, explicitlyRemoved := removed[id]; explicitlyRemoved {
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedAliasConflict(id, candidates)
		}
		for _, candidate := range candidates {
			if !candidate.removed {
				selected[id] = candidate.alias
			}
		}
	}
	result := make([]Alias, 0, len(selected))
	for _, alias := range selected {
		result = append(result, alias)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, nil
}

func inheritedAliasConflict(id capabilityid.Identifier, candidates map[string]*aliasCandidate) error {
	digests := make([]string, 0, len(candidates))
	for digest := range candidates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	parts := make([]string, 0, len(digests))
	for _, digest := range digests {
		candidate := candidates[digest]
		declaration := "removed"
		if !candidate.removed {
			declaration = "target " + candidate.alias.target.String()
		}
		parts = append(parts, fmt.Sprintf("%s from %s", declaration, strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: capabilities.aliases[%q] has incompatible declarations: %s; set or remove that exact key in the current Project configuration", ErrInheritedConflict, id.String(), strings.Join(parts, "; "))
}

func dependencySource(dependency Dependency, source string) string {
	return dependencyIdentity(dependency) + "/" + source
}

func dependencyIdentity(dependency Dependency) string {
	version := dependency.ModuleVersion
	if version == "" {
		version = "workspace"
	}
	return dependency.ModulePath + "@" + version
}

func finalizeProvenance(records map[string]*provenanceRecord) []Provenance {
	result := make([]Provenance, 0, len(records))
	for _, record := range records {
		result = append(result, Provenance{path: record.path, digest: record.digest, removed: record.removed, sources: sortedSet(record.sources)})
	}
	sortProvenance(result)
	return result
}

func digestProvenance(records []Provenance) (string, error) {
	type record struct {
		Path    string   `json:"path"`
		Digest  string   `json:"digest"`
		Removed bool     `json:"removed,omitempty"`
		Sources []string `json:"sources"`
	}
	document := struct {
		Version int      `json:"version"`
		Records []record `json:"records"`
	}{Version: 1, Records: make([]record, len(records))}
	for index := range records {
		document.Records[index] = record{Path: records[index].path, Digest: records[index].digest, Removed: records[index].removed, Sources: append([]string(nil), records[index].sources...)}
	}
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func aliasDigest(alias Alias) string {
	exposure := generation.Exposure{}
	if alias.hasExposure {
		exposure = alias.exposure
	}
	data, _ := json.Marshal(struct {
		ID          string              `json:"id"`
		Target      string              `json:"target"`
		HasExposure bool                `json:"has_exposure"`
		Exposure    generation.Exposure `json:"exposure"`
		Deprecated  string              `json:"deprecated"`
	}{
		ID:          alias.id.String(),
		Target:      alias.target.String(),
		HasExposure: alias.hasExposure,
		Exposure:    exposure,
		Deprecated:  alias.deprecated,
	})
	return digestStrings("capabilities.aliases", string(data))
}

func declarationDigest(path string, id capabilityid.Identifier, removed bool) string {
	if removed {
		return digestStrings(path, id.String(), "removed")
	}
	return digestStrings(path, id.String())
}

func digestStrings(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(fmt.Sprintf("%d:", len(value))))
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validCompositionDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
