package applicationmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// ErrConfigurationSchema reports configuration for a constructor without
	// one compiled same-package Config schema in the owning namespace.
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

// Dependency retains the internal shape used by configuration-maintenance
// callers. Ordinary Go Module dependencies never contribute application
// configuration to Compose.
type Dependency struct {
	ModulePath    string
	ModuleVersion string
	Manifest      Manifest
}

// ConfigurationNamespace identifies the constructor kind allowed to own a
// configuration path, including a removed entry.
type ConfigurationNamespace string

const (
	ConfigurationNamespaceImplementation ConfigurationNamespace = "implementation"
	ConfigurationNamespaceResource       ConfigurationNamespace = "resource"
)

// SchemaLookup returns the compiled same-package Config schema for one exact
// visible constructor in the requested namespace. A constructor of another kind,
// an unknown constructor, or a constructor without Config must return false.
// Implementations own top-level config; Resource providers own instance config.
type SchemaLookup func(namespace ConfigurationNamespace, constructor constructorsymbol.Symbol) (implementationinventory.Configuration, bool)

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

// Removed reports whether this composition record is an explicit typed
// tombstone rather than a contributed value.
func (p Provenance) Removed() bool { return p.removed }

// Sources returns every contributing module and YAML field path in stable
// lexical order.
func (p Provenance) Sources() []string { return append([]string(nil), p.sources...) }

// Composition is one immutable effective Manifest plus its complete
// current-project non-secret provenance.
type Composition struct {
	current           Manifest
	currentLayers     []Manifest
	manifest          Manifest
	provenance        []Provenance
	resolutionSources []Provenance
	compositionDigest string
	prepared          bool
}

// String redacts private configuration in nested manifests and layers.
func (Composition) String() string { return "<redacted-application-composition>" }

// GoString prevents Go-syntax formatting from exposing private configuration.
func (Composition) GoString() string { return "<redacted-application-composition>" }

// Format redacts private configuration for every fmt verb.
func (Composition) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-application-composition>"))
}

// LogValue redacts the composition for structured standard-library logging.
func (Composition) LogValue() slog.Value {
	return slog.StringValue("<redacted-application-composition>")
}

// Valid reports whether the value was produced by Compose.
func (c Composition) Valid() bool {
	return c.prepared && validCompositionDigest(c.compositionDigest)
}

// Manifest returns the effective typed application declaration.
func (c Composition) Manifest() Manifest {
	if !c.Valid() {
		return Manifest{}
	}
	return c.manifest
}

// CurrentManifest returns the selected current-Project declaration before any
// environment overlay is applied.
func (c Composition) CurrentManifest() Manifest {
	if !c.Valid() {
		return Manifest{}
	}
	return c.current
}

// Provenance returns defensive path-and-digest-sorted current-project records.
func (c Composition) Provenance() []Provenance {
	if !c.Valid() {
		return nil
	}
	return cloneProvenance(c.provenance)
}

// ResolutionSources returns the current-project declarations that contribute
// to the effective model, selected by layer precedence rather than value equality.
func (c Composition) ResolutionSources() []Provenance {
	if !c.Valid() {
		return nil
	}
	return cloneProvenance(c.resolutionSources)
}

// CompositionDigest returns the stable digest of the normalized current-Project
// composition and its source provenance. It contains no configuration values
// or resolved Secrets.
func (c Composition) CompositionDigest() string {
	if !c.Valid() {
		return ""
	}
	return c.compositionDigest
}

// Compose applies only the selected current-project layers. Ordinary Go Module
// dependencies remain visible to discovery but never contribute configuration.
func Compose(_ []Dependency, current Manifest, schemas SchemaLookup) (Composition, error) {
	if schemas == nil {
		return Composition{}, fmt.Errorf("%w: schema lookup is nil", ErrCompose)
	}
	records := make(map[string]*provenanceRecord)
	var currentLayers []Manifest
	active := make(map[string]Provenance)
	effective := Manifest{startupTimeout: DefaultStartupTimeout}
	var corsSources httpCORSCompositionSources
	apply := func(layer Manifest, environmentOverlay bool) error {
		layer = bindResourceProviders(layer, effective)
		decisions, err := ConfigurationDecisions(layer, schemas)
		if err != nil {
			return err
		}
		currentLayers = append(currentLayers, layer)
		clearReplacedResourceSources(active, effective, layer)
		for _, decision := range decisions {
			addProvenance(records, decision.path, decision.digest, decision.source, decision.removed)
			if decision.resolutionRelevant {
				applyResolutionDecision(active, decision)
			}
		}
		corsSources.apply(layer, current.modulePath, environmentOverlay)
		effective, err = applyManifestLayer(effective, layer, schemas)
		return err
	}
	for index, layer := range manifestLayers(current) {
		// ApplyOverlay retains the selected base followed by its overlay layers.
		// A replacement document is the base, regardless of its filename.
		if err := apply(layer, index > 0); err != nil {
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
	effective, err = finalizeResources(effective, schemas)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	effective.modulePath, effective.source = current.modulePath, current.source
	effective.layers = nil
	provenance := finalizeProvenance(records)
	digest, err := digestProvenance(provenance)
	if err != nil {
		return Composition{}, fmt.Errorf("%w: %w", ErrCompose, err)
	}
	return Composition{
		current: current, currentLayers: currentLayers, manifest: effective, provenance: provenance,
		resolutionSources: activeResolutionSources(active), compositionDigest: digest, prepared: true,
	}, nil
}

func applyResolutionDecision(active map[string]Provenance, decision ConfigurationDecision) {
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
	record := Provenance{path: path, digest: decision.digest, removed: decision.removed, sources: []string{decision.source}}
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
			// Configuration ownership includes surviving lower-layer fixed-struct
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
