package applicationmeta

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/kernel/configuration"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrCompose reports that typed Project configuration composition failed.
	ErrCompose = errors.New("compose Project configuration")
	// ErrInheritedConflict reports incompatible dependency declarations that
	// the current Project did not explicitly replace.
	ErrInheritedConflict = errors.New("inherited Project configuration conflict")
	// ErrConfigurationSchema reports configuration for a Plugin without one
	// visible machine-readable declaration.
	ErrConfigurationSchema = errors.New("plugin configuration schema unavailable")
)

// Dependency is one dependency Project's parsed root configuration and stable
// Go Module provenance. Directness and graph position deliberately do not
// participate in composition.
type Dependency struct {
	ModulePath    string
	ModuleVersion string
	Manifest      Manifest
}

// SchemaLookup returns the machine-readable configuration declaration for one
// exact visible Plugin ID.
type SchemaLookup func(pluginID string) (kernelmanifest.Config, bool)

// Provenance records one dependency-derived typed field value without storing
// the value itself. Several records may share a Path when dependencies
// contribute incompatible values that a current-project replacement resolves.
type Provenance struct {
	path    string
	digest  string
	sources []string
}

// Path returns the stable schema field or canonical declaration key.
func (p Provenance) Path() string { return p.path }

// Digest returns the normalized value digest.
func (p Provenance) Digest() string { return p.digest }

// Sources returns every contributing module and YAML field path in stable
// lexical order.
func (p Provenance) Sources() []string { return append([]string(nil), p.sources...) }

// Composition is one immutable effective Manifest plus its complete
// dependency-derived non-secret provenance.
type Composition struct {
	manifest         Manifest
	provenance       []Provenance
	dependencyDigest string
	prepared         bool
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
	result := make([]Provenance, len(c.provenance))
	for index := range c.provenance {
		result[index] = Provenance{
			path:    c.provenance[index].path,
			digest:  c.provenance[index].digest,
			sources: append([]string(nil), c.provenance[index].sources...),
		}
	}
	return result
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
// Manifest. Process settings remain current-project-owned; canonical sets
// union; keyed Providers, Aliases, and Plugin fields require compatible
// inherited values unless the current Project replaces that exact key.
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
	exposures := composeExposureSet(ordered, current.HTTPExposures(), records)
	requirements := composeRequirementSet(ordered, current.Requirements(), records)
	choices, err := composeProviderChoices(ordered, current.ProviderChoices(), records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	aliases, err := composeAliases(ordered, current.Aliases(), records)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	configurations, err := composeConfigurations(ordered, current.Configurations(), schemas, records)
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
		httpAddress:     current.httpAddress,
		hasHTTPAddress:  current.hasHTTPAddress,
		httpExposures:   exposures,
		requirements:    requirements,
		providerChoices: choices,
		aliases:         aliases,
		configurations:  configurations,
		startupTimeout:  current.startupTimeout,
	}
	return Composition{manifest: manifest, provenance: provenance, dependencyDigest: digest, prepared: true}, nil
}

type provenanceRecord struct {
	path    string
	digest  string
	sources map[string]struct{}
}

func addProvenance(records map[string]*provenanceRecord, path, digest, source string) {
	key := path + "\x00" + digest
	record, exists := records[key]
	if !exists {
		record = &provenanceRecord{path: path, digest: digest, sources: make(map[string]struct{})}
		records[key] = record
	}
	record.sources[source] = struct{}{}
}

func composeExposureSet(dependencies []Dependency, current []HTTPExposure, records map[string]*provenanceRecord) []HTTPExposure {
	values := make(map[capabilityid.Identifier]HTTPExposure)
	for _, dependency := range dependencies {
		for _, exposure := range dependency.Manifest.HTTPExposures() {
			path := fmt.Sprintf("http.expose[%q]", exposure.id.String())
			source := dependencySource(dependency, exposure.source)
			addProvenance(records, path, digestStrings("http.expose", exposure.id.String()), source)
			if existing, exists := values[exposure.id]; !exists || source < existing.source {
				values[exposure.id] = HTTPExposure{id: exposure.id, source: source}
			}
		}
	}
	for _, exposure := range current {
		values[exposure.id] = exposure
	}
	result := make([]HTTPExposure, 0, len(values))
	for _, exposure := range values {
		result = append(result, exposure)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result
}

func composeRequirementSet(dependencies []Dependency, current []CapabilityRequirement, records map[string]*provenanceRecord) []CapabilityRequirement {
	values := make(map[capabilityid.Identifier]CapabilityRequirement)
	for _, dependency := range dependencies {
		for _, requirement := range dependency.Manifest.Requirements() {
			path := fmt.Sprintf("capabilities.require[%q]", requirement.id.String())
			source := dependencySource(dependency, requirement.source)
			addProvenance(records, path, digestStrings("capabilities.require", requirement.id.String()), source)
			if existing, exists := values[requirement.id]; !exists || source < existing.source {
				values[requirement.id] = CapabilityRequirement{id: requirement.id, source: source}
			}
		}
	}
	for _, requirement := range current {
		values[requirement.id] = requirement
	}
	result := make([]CapabilityRequirement, 0, len(values))
	for _, requirement := range values {
		result = append(result, requirement)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result
}

type providerCandidate struct {
	choice  ProviderChoice
	sources map[string]struct{}
}

func composeProviderChoices(dependencies []Dependency, current []ProviderChoice, records map[string]*provenanceRecord) ([]ProviderChoice, error) {
	inherited := make(map[capabilityid.Identifier]map[string]*providerCandidate)
	for _, dependency := range dependencies {
		for _, choice := range dependency.Manifest.ProviderChoices() {
			path := fmt.Sprintf("capabilities.use[%q]", choice.capability.String())
			source := dependencySource(dependency, choice.source)
			digest := digestStrings("capabilities.use", choice.capability.String(), choice.pluginID)
			addProvenance(records, path, digest, source)
			byProvider := inherited[choice.capability]
			if byProvider == nil {
				byProvider = make(map[string]*providerCandidate)
				inherited[choice.capability] = byProvider
			}
			candidate := byProvider[choice.pluginID]
			if candidate == nil {
				candidate = &providerCandidate{choice: ProviderChoice{capability: choice.capability, pluginID: choice.pluginID, source: source}, sources: make(map[string]struct{})}
				byProvider[choice.pluginID] = candidate
			}
			candidate.sources[source] = struct{}{}
			if source < candidate.choice.source {
				candidate.choice.source = source
			}
		}
	}
	selected := make(map[capabilityid.Identifier]ProviderChoice)
	for _, choice := range current {
		selected[choice.capability] = choice
	}
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
		if len(candidates) != 1 {
			return nil, inheritedProviderConflict(capability, candidates)
		}
		for _, candidate := range candidates {
			selected[capability] = candidate.choice
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
	providers := make([]string, 0, len(candidates))
	for provider := range candidates {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	parts := make([]string, 0, len(providers))
	for _, provider := range providers {
		parts = append(parts, fmt.Sprintf("%s from %s", provider, strings.Join(sortedSet(candidates[provider].sources), ", ")))
	}
	return fmt.Errorf("%w: capabilities.use[%q] selects different Providers: %s; set that exact key in the current Project configuration", ErrInheritedConflict, capability.String(), strings.Join(parts, "; "))
}

type aliasCandidate struct {
	alias   Alias
	sources map[string]struct{}
}

func composeAliases(dependencies []Dependency, current []Alias, records map[string]*provenanceRecord) ([]Alias, error) {
	inherited := make(map[capabilityid.Identifier]map[string]*aliasCandidate)
	for _, dependency := range dependencies {
		for _, alias := range dependency.Manifest.Aliases() {
			path := fmt.Sprintf("capabilities.aliases[%q]", alias.id.String())
			source := dependencySource(dependency, alias.source)
			digest := aliasDigest(alias)
			addProvenance(records, path, digest, source)
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
	}
	selected := make(map[capabilityid.Identifier]Alias)
	for _, alias := range current {
		selected[alias.id] = alias
	}
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
		if len(candidates) != 1 {
			return nil, inheritedAliasConflict(id, candidates)
		}
		for _, candidate := range candidates {
			selected[id] = candidate.alias
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
		parts = append(parts, fmt.Sprintf("target %s from %s", candidate.alias.target.String(), strings.Join(sortedSet(candidate.sources), ", ")))
	}
	return fmt.Errorf("%w: capabilities.aliases[%q] has incompatible declarations: %s; set or remove that exact key in the current Project configuration", ErrInheritedConflict, id.String(), strings.Join(parts, "; "))
}

type configCandidate struct {
	yaml    []byte
	sources map[string]struct{}
}

type configuredField struct {
	pluginID string
	name     string
	yaml     []byte
	digest   string
	source   string
}

func composeConfigurations(dependencies []Dependency, current []PluginConfiguration, schemas SchemaLookup, records map[string]*provenanceRecord) ([]PluginConfiguration, error) {
	inherited := make(map[string]map[string]*configCandidate)
	configuredPlugins := make(map[string]string)
	for _, dependency := range dependencies {
		for _, configured := range dependency.Manifest.Configurations() {
			source := dependencySource(dependency, configured.source)
			if existing, exists := configuredPlugins[configured.pluginID]; !exists || source < existing {
				configuredPlugins[configured.pluginID] = source
			}
			fields, err := normalizeConfiguredFields(configured, schemas)
			if err != nil {
				return nil, fmt.Errorf("dependency %s: %w", dependencyIdentity(dependency), err)
			}
			for _, field := range fields {
				path := configFieldPath(field.pluginID, field.name)
				source := dependencySource(dependency, field.source)
				addProvenance(records, path, field.digest, source)
				byDigest := inherited[path]
				if byDigest == nil {
					byDigest = make(map[string]*configCandidate)
					inherited[path] = byDigest
				}
				candidate := byDigest[field.digest]
				if candidate == nil {
					candidate = &configCandidate{yaml: append([]byte(nil), field.yaml...), sources: make(map[string]struct{})}
					byDigest[field.digest] = candidate
				}
				candidate.sources[source] = struct{}{}
				if bytes.Compare(field.yaml, candidate.yaml) < 0 {
					candidate.yaml = append(candidate.yaml[:0], field.yaml...)
				}
			}
		}
	}

	selected := make(map[string]configuredField)
	for _, configured := range current {
		configuredPlugins[configured.pluginID] = configured.source
		fields, err := normalizeConfiguredFields(configured, schemas)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			selected[configFieldPath(field.pluginID, field.name)] = field
		}
	}
	paths := make([]string, 0, len(inherited))
	for path := range inherited {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		candidates := inherited[path]
		if _, replaced := selected[path]; replaced {
			continue
		}
		if len(candidates) != 1 {
			return nil, inheritedConfigurationConflict(path, candidates)
		}
		pluginID, name, err := parseConfigFieldPath(path)
		if err != nil {
			return nil, err
		}
		for digest, candidate := range candidates {
			sources := sortedSet(candidate.sources)
			selected[path] = configuredField{pluginID: pluginID, name: name, yaml: append([]byte(nil), candidate.yaml...), digest: digest, source: sources[0]}
		}
	}

	byPlugin := make(map[string]map[string]configuredField)
	for pluginID := range configuredPlugins {
		byPlugin[pluginID] = make(map[string]configuredField)
	}
	for _, field := range selected {
		fields := byPlugin[field.pluginID]
		if fields == nil {
			fields = make(map[string]configuredField)
			byPlugin[field.pluginID] = fields
		}
		fields[field.name] = field
	}
	pluginIDs := make([]string, 0, len(byPlugin))
	for pluginID := range byPlugin {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	result := make([]PluginConfiguration, 0, len(pluginIDs))
	for _, pluginID := range pluginIDs {
		data, err := marshalConfigurationFields(byPlugin[pluginID])
		if err != nil {
			return nil, fmt.Errorf("config[%q]: %v", pluginID, err)
		}
		source := configuredPlugins[pluginID]
		result = append(result, PluginConfiguration{pluginID: pluginID, source: source, yaml: data})
	}
	return result, nil
}

func normalizeConfiguredFields(configured PluginConfiguration, schemas SchemaLookup) ([]configuredField, error) {
	schema, exists := schemas(configured.pluginID)
	if !exists {
		return nil, fmt.Errorf("%w for Plugin %q at %s", ErrConfigurationSchema, configured.pluginID, configured.source)
	}
	partial, err := configuration.NormalizePartial(schema, configured.yaml)
	if err != nil {
		return nil, fmt.Errorf("config[%q] at %s: %w", configured.pluginID, configured.source, err)
	}
	fields := make([]configuredField, 0, len(partial.Names()))
	for _, name := range partial.Names() {
		data, _ := partial.YAML(name)
		digest, _ := partial.Digest(name)
		fields = append(fields, configuredField{
			pluginID: configured.pluginID,
			name:     name,
			yaml:     data,
			digest:   digest,
			source:   configFieldSource(configured.source, name),
		})
	}
	return fields, nil
}

func inheritedConfigurationConflict(path string, candidates map[string]*configCandidate) error {
	digests := make([]string, 0, len(candidates))
	for digest := range candidates {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	parts := make([]string, 0, len(digests))
	for _, digest := range digests {
		parts = append(parts, strings.Join(sortedSet(candidates[digest].sources), ", "))
	}
	return fmt.Errorf("%w: %s has incompatible normalized values from %s; set or remove that exact field in the current Project configuration", ErrInheritedConflict, path, strings.Join(parts, "; "))
}

func marshalConfigurationFields(fields map[string]configuredField) ([]byte, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, name := range names {
		node, err := decodeFieldNode(fields[name].yaml)
		if err != nil {
			return nil, err
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
			node,
		)
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		_ = encoder.Close()
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func decodeFieldNode(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, errors.New("normalized field is not one YAML document")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("normalized field has trailing YAML")
	}
	return document.Content[0], nil
}

func configFieldPath(pluginID, name string) string {
	return fmt.Sprintf("config[%q][%q]", pluginID, name)
}

func parseConfigFieldPath(path string) (string, string, error) {
	const prefix = "config[\""
	if !strings.HasPrefix(path, prefix) {
		return "", "", errors.New("invalid composed configuration field path")
	}
	separator := "\"][\""
	remainder := strings.TrimPrefix(path, prefix)
	pluginID, name, exists := strings.Cut(remainder, separator)
	if !exists || !strings.HasSuffix(name, "\"]") {
		return "", "", errors.New("invalid composed configuration field path")
	}
	return pluginID, strings.TrimSuffix(name, "\"]"), nil
}

func configFieldSource(source, name string) string {
	return source + fmt.Sprintf("[%q]", name)
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
		result = append(result, Provenance{path: record.path, digest: record.digest, sources: sortedSet(record.sources)})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].path != result[right].path {
			return result[left].path < result[right].path
		}
		return result[left].digest < result[right].digest
	})
	return result
}

func digestProvenance(records []Provenance) (string, error) {
	type record struct {
		Path    string   `json:"path"`
		Digest  string   `json:"digest"`
		Sources []string `json:"sources"`
	}
	document := struct {
		Version int      `json:"version"`
		Records []record `json:"records"`
	}{Version: 1, Records: make([]record, len(records))}
	for index := range records {
		document.Records[index] = record{Path: records[index].path, Digest: records[index].digest, Sources: append([]string(nil), records[index].sources...)}
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
