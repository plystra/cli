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
	"github.com/plystra/cli/internal/modulepath"
)

var (
	// ErrCompose reports that typed Project configuration composition failed.
	ErrCompose = errors.New("compose Project configuration")
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
	// ErrConfigurationRequired reports a required field absent after composition.
	ErrConfigurationRequired = errors.New("required constructor configuration field is missing")
)

// Dependency is one template root and its effective Go Module identity.
// Compose consumes dependencies in explicit oldest-to-nearest order.
type Dependency struct {
	ModulePath    string
	ModuleVersion string
	Manifest      Manifest
}

// SchemaLookup returns the compiled same-package Config schema for one exact
// visible Implementation constructor.
type SchemaLookup func(constructor constructorsymbol.Symbol) (implementationinventory.Configuration, bool)

// Provenance records public-safe typed decisions and declaration ownership.
// Private values never influence contributor selection or grouping.
type Provenance struct {
	path    string
	digest  string
	removed bool
	sources []string
}

// Path returns the stable schema field or canonical declaration key.
func (p Provenance) Path() string { return p.path }

// Digest returns the normalized public identity, excluding Secret targets.
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
	current           Manifest
	manifest          Manifest
	templateLayers    []TemplateLayer
	provenance        []Provenance
	resolutionSources []Provenance
	dependencyDigest  string
	prepared          bool
}

// TemplateLayer is one public-safe authored template layer. Its position in
// TemplateLayers is precedence, not Implementation discovery priority.
type TemplateLayer struct {
	ModulePath    string
	ModuleVersion string
	Source        string
	Decisions     []ConfigurationDecision
}

// TemplateLayers returns every root layer in oldest-to-nearest order,
// including declarations suppressed by later templates or the current Project.
func (c Composition) TemplateLayers() []TemplateLayer {
	if !c.Valid() {
		return nil
	}
	result := append([]TemplateLayer(nil), c.templateLayers...)
	for index := range result {
		result[index].Decisions = append([]ConfigurationDecision(nil), result[index].Decisions...)
	}
	return result
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

// CurrentManifest returns the selected current-Project declaration before any
// template contributes its lower-precedence values.
func (c Composition) CurrentManifest() Manifest {
	if !c.Valid() {
		return Manifest{}
	}
	return c.current
}

// Provenance returns defensive path-and-digest-sorted dependency baseline
// records.
func (c Composition) Provenance() []Provenance {
	if !c.Valid() {
		return nil
	}
	return cloneProvenance(c.provenance)
}

// ResolutionSources returns the inherited declarations that still contribute
// to the effective model, selected by layer precedence rather than value equality.
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

// Compose applies template roots in explicit oldest-to-nearest order, followed
// by the selected current-project layers. It never ranks Implementation candidates.
func Compose(dependencies []Dependency, current Manifest, schemas SchemaLookup) (Composition, error) {
	if schemas == nil {
		return Composition{}, fmt.Errorf("%w: schema lookup is nil", ErrCompose)
	}
	seen := make(map[string]bool, len(dependencies)+1)
	if current.modulePath != "" {
		seen[current.modulePath] = true
	}
	records := make(map[string]*provenanceRecord)
	var templateLayers []TemplateLayer
	active := make(map[string]Provenance)
	effective := Manifest{startupTimeout: DefaultStartupTimeout}
	var corsSources httpCORSCompositionSources
	apply := func(layer Manifest, owner *Dependency, environmentOverlay bool) error {
		decisions, err := ConfigurationDecisions(layer, schemas)
		if err != nil {
			return err
		}
		if owner != nil {
			templateLayers = append(templateLayers, TemplateLayer{ModulePath: owner.ModulePath, ModuleVersion: owner.ModuleVersion, Source: layer.source, Decisions: append([]ConfigurationDecision(nil), decisions...)})
		}
		for _, decision := range decisions {
			if !decision.dependencyComposable {
				continue
			}
			if owner != nil {
				source := dependencySource(*owner, declarationReference(layer, decision))
				addProvenance(records, decision.path, decision.digest, source, decision.removed)
			}
			applyResolutionDecision(active, layer, decision, owner)
		}
		if owner != nil {
			layer = qualifyTemplateSources(layer, *owner)
		}
		corsSources.apply(layer, current.modulePath, environmentOverlay)
		effective, err = applyManifestLayer(effective, layer, schemas)
		return err
	}
	for index, dependency := range dependencies {
		if len(dependency.ModulePath) > 4096 || modulepath.CheckProject(dependency.ModulePath) != nil {
			return Composition{}, fmt.Errorf("%w: invalid template module path", ErrCompose)
		}
		if seen[dependency.ModulePath] {
			return Composition{}, fmt.Errorf("%w: template module %q is repeated", ErrCompose, dependency.ModulePath)
		}
		seen[dependency.ModulePath] = true
		if dependency.Manifest.modulePath != "" && dependency.Manifest.modulePath != dependency.ModulePath {
			return Composition{}, fmt.Errorf("%w: template module %q does not match its declaration owner", ErrCompose, dependency.ModulePath)
		}
		if strings.ContainsAny(dependency.ModuleVersion, "\x00\r\n") {
			return Composition{}, fmt.Errorf("%w: invalid template module version", ErrCompose)
		}
		manifest, err := WithProjectModule(dependency.Manifest, dependency.ModulePath)
		if err != nil {
			return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
		}
		path := fmt.Sprintf("template.ancestry[%d]", index)
		source := manifest.source
		if source == "" {
			source = "plystra.yaml"
		}
		addProvenance(records, path, digestStrings("template.ancestry/v1", dependency.ModulePath, dependency.ModuleVersion, source, manifest.template), dependencySource(dependency, source), false)
		for _, layer := range manifestLayers(manifest) {
			layer.httpAddress, layer.hasHTTPAddress, layer.removeHTTPAddress = "", false, false
			layer.startupTimeout, layer.hasStartupTimeout, layer.removeStartupTimeout = DefaultStartupTimeout, false, false
			if err := apply(layer, &dependency, false); err != nil {
				return Composition{}, fmt.Errorf("%w: template %s: %w", ErrCompose, dependencyIdentity(dependency), err)
			}
		}
	}
	for index, layer := range manifestLayers(current) {
		// ApplyOverlay retains the selected base followed by its overlay layers.
		// A replacement document is the base, regardless of its filename.
		if err := apply(layer, nil, index > 0); err != nil {
			return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
		}
	}
	if err := validateHTTPCORSLayer(effective.httpCORS); err != nil {
		return Composition{}, corsSources.invalid(effective.httpCORS, err)
	}
	if err := rejectAliasResolutionInputs(effective.requirements, effective.providerChoices, effective.aliases); err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	if err := rejectAliasChains(effective.aliases); err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	configured, err := manifestConfigDecisions(effective, schemas)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	effective.configurations, err = renderConstructorConfigurations(constructorConfigDecisionsByPath(configured))
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	effective.modulePath, effective.source = current.modulePath, current.source
	effective.template, effective.templateSource = current.template, current.templateSource
	effective.layers = nil
	provenance := finalizeProvenance(records)
	digest, err := digestProvenance(provenance)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	return Composition{
		current: current, manifest: effective, provenance: provenance,
		templateLayers:    templateLayers,
		resolutionSources: activeResolutionSources(active), dependencyDigest: digest, prepared: true,
	}, nil
}

func declarationReference(layer Manifest, decision ConfigurationDecision) string {
	// Typed configuration keeps per-object source references; ordinary decisions
	// expose document names and get their canonical path appended here.
	source := decision.source
	if source == "" {
		source = layer.source
	}
	if source == "" {
		source = "plystra.yaml"
	}
	if strings.Contains(source, " ") {
		return source
	}
	return source + " " + decision.path
}

func applyResolutionDecision(active map[string]Provenance, layer Manifest, decision ConfigurationDecision, owner *Dependency) {
	path := decision.path
	clear := func(prefix string) {
		for key := range active {
			if strings.HasPrefix(key, prefix) {
				delete(active, key)
			}
		}
	}
	if decision.summary == ConfigurationSummaryCompleteSet {
		clear(path + "[")
	}
	if decision.removed || decision.summary != ConfigurationSummaryObject {
		clear(path + "[")
		clear(path + ".")
	}
	// Empty sources mark current ownership and suppress inherited attribution.
	record := Provenance{path: path, digest: decision.digest, removed: decision.removed}
	if owner != nil {
		record.sources = []string{dependencySource(*owner, declarationReference(layer, decision))}
	}
	active[path] = record
}

func activeResolutionSources(active map[string]Provenance) []Provenance {
	records := make(map[string]*provenanceRecord)
	for _, record := range active {
		if record.removed || len(record.sources) == 0 {
			continue
		}
		for _, source := range record.sources {
			if !strings.HasPrefix(record.path, "config[") {
				addProvenance(records, record.path, record.digest, source, false)
			}
			// Configuration ownership includes surviving inherited fixed-struct
			// fields even when a nearer layer supplied the object container.
			if strings.HasPrefix(record.path, "config[") {
				root := record.path
				if end := strings.Index(root, "]["); end >= 0 {
					root = root[:end+1]
				}
				if field := strings.Index(source, " config["); field >= 0 {
					source = source[:field+1] + root
				}
				addProvenance(records, root, digestStrings("config.contributor", root), source, false)
			}
		}
	}
	return finalizeProvenance(records)
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

func dependencySource(dependency Dependency, source string) string {
	return dependencyModuleIdentity(dependency) + "/" + source
}

func dependencyIdentity(dependency Dependency) string {
	return dependencyModuleIdentity(dependency)
}

func dependencyModuleIdentity(dependency Dependency) string {
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
