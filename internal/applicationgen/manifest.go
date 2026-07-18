package applicationgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/generationresolution"
	"go.yaml.in/yaml/v3"
)

const (
	applicationManifestConfigurationVersion = 2
	// ConfigurationModeDefault identifies the mandatory root plystra.yaml as
	// the current-project document.
	ConfigurationModeDefault = "default"
	// ConfigurationModeExplicit identifies one complete explicitly selected
	// current-project document.
	ConfigurationModeExplicit = "explicit-config"
)

type applicationManifestProvenance struct {
	Path    string   `json:"path"`
	Digest  string   `json:"digest"`
	Removed bool     `json:"removed,omitempty"`
	Sources []string `json:"sources"`
}

type applicationManifestDocumentReference struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type applicationManifestConfiguration struct {
	Version                int                                    `json:"version"`
	Mode                   string                                 `json:"mode"`
	Root                   applicationManifestDocumentReference   `json:"root"`
	Selected               applicationManifestDocumentReference   `json:"selected"`
	DependencyBaselines    []applicationManifestSelectionBaseline `json:"dependency_baselines"`
	ApplicationModelDigest string                                 `json:"application_model_digest"`
}

type applicationManifestSelectionBaseline struct {
	Mode                        string                          `json:"mode"`
	Path                        string                          `json:"path"`
	DependencyCompositionDigest string                          `json:"dependency_composition_digest"`
	DependencyBaseline          []applicationManifestProvenance `json:"dependency_baseline"`
}

type applicationManifestDocument struct {
	CapabilityAliases json.RawMessage                  `json:"capability_aliases"`
	Configuration     applicationManifestConfiguration `json:"configuration"`
}

// ManifestProvenanceOptions contains the complete non-secret generation
// provenance. RootData and SelectedData are normalized semantically; raw YAML
// values and Secret reference targets are never retained.
type ManifestProvenanceOptions struct {
	Mode                   string
	RootPath               string
	RootData               []byte
	SelectedPath           string
	SelectedData           []byte
	Composition            applicationmeta.Composition
	ApplicationModelDigest string
	Previous               ManifestProvenance
}

// ManifestProvenance is one immutable validated generated-manifest
// configuration record.
type ManifestProvenance struct {
	mode                   string
	rootPath               string
	rootDigest             string
	selectedPath           string
	selectedDigest         string
	baselines              []manifestSelectionBaseline
	applicationModelDigest string
}

type manifestSelectionBaseline struct {
	mode     string
	path     string
	baseline applicationmeta.DependencyBaseline
}

// NewManifestProvenance constructs the only supported generated-manifest
// provenance schema and computes semantic configuration digests centrally.
func NewManifestProvenance(options ManifestProvenanceOptions) (ManifestProvenance, error) {
	rootDigest, err := ConfigurationDigest(options.RootData)
	if err != nil {
		return ManifestProvenance{}, fmt.Errorf("root configuration: %w", err)
	}
	selectedDigest, err := ConfigurationDigest(options.SelectedData)
	if err != nil {
		return ManifestProvenance{}, fmt.Errorf("selected configuration: %w", err)
	}
	baselines := make([]manifestSelectionBaseline, 0, len(options.Previous.baselines)+1)
	if validateManifestProvenance(options.Previous) == nil {
		baselines = cloneSelectionBaselines(options.Previous.baselines)
	}
	current := manifestSelectionBaseline{
		mode:     options.Mode,
		path:     options.SelectedPath,
		baseline: options.Composition.DependencyBaseline(),
	}
	replaced := false
	for index := range baselines {
		if baselines[index].mode == current.mode && baselines[index].path == current.path {
			baselines[index] = current
			replaced = true
			break
		}
	}
	if !replaced {
		baselines = append(baselines, current)
	}
	sort.Slice(baselines, func(left, right int) bool {
		if baselines[left].mode != baselines[right].mode {
			return baselines[left].mode < baselines[right].mode
		}
		return baselines[left].path < baselines[right].path
	})
	provenance := ManifestProvenance{
		mode:                   options.Mode,
		rootPath:               options.RootPath,
		rootDigest:             rootDigest,
		selectedPath:           options.SelectedPath,
		selectedDigest:         selectedDigest,
		baselines:              baselines,
		applicationModelDigest: options.ApplicationModelDigest,
	}
	if err := validateManifestProvenance(provenance); err != nil {
		return ManifestProvenance{}, err
	}
	return provenance, nil
}

// Mode returns default or explicit-config.
func (p ManifestProvenance) Mode() string { return p.mode }

// RootPath returns the stable Project-relative root marker path.
func (p ManifestProvenance) RootPath() string { return p.rootPath }

// RootDigest returns the normalized semantic root-document digest.
func (p ManifestProvenance) RootDigest() string { return p.rootDigest }

// SelectedPath returns the stable Project-relative current-project document.
func (p ManifestProvenance) SelectedPath() string { return p.selectedPath }

// SelectedDigest returns the normalized semantic selected-document digest.
func (p ManifestProvenance) SelectedDigest() string { return p.selectedDigest }

// DependencyBaseline returns the typed dependency-composition baseline.
func (p ManifestProvenance) DependencyBaseline() applicationmeta.DependencyBaseline {
	baseline, _ := p.BaselineForSelection(p.mode, p.selectedPath)
	return baseline
}

// BaselineForSelection returns retained dependency ownership for one exact
// selection mode and Project-relative path.
func (p ManifestProvenance) BaselineForSelection(mode, selectedPath string) (applicationmeta.DependencyBaseline, bool) {
	index := sort.Search(len(p.baselines), func(index int) bool {
		return p.baselines[index].mode > mode ||
			(p.baselines[index].mode == mode && p.baselines[index].path >= selectedPath)
	})
	if index >= len(p.baselines) || p.baselines[index].mode != mode || p.baselines[index].path != selectedPath {
		return applicationmeta.DependencyBaseline{}, false
	}
	return p.baselines[index].baseline, true
}

// ApplicationModelDigest returns the final build-affecting generation-context
// digest.
func (p ManifestProvenance) ApplicationModelDigest() string {
	return p.applicationModelDigest
}

// MatchesSelection reports whether this provenance owns the baseline for the
// exact current-project selection.
func (p ManifestProvenance) MatchesSelection(mode, selectedPath string) bool {
	return p.mode == mode && p.selectedPath == selectedPath
}

func (p ManifestProvenance) matches(composition applicationmeta.Composition, applicationModelDigest string) bool {
	baseline := p.DependencyBaseline()
	return validateManifestProvenance(p) == nil && composition.Valid() &&
		baseline.Digest() == composition.DependencyBaseline().Digest() &&
		p.applicationModelDigest == applicationModelDigest
}

// RenderManifest combines the normalized Alias map with strict non-secret
// configuration and application-model provenance. It never serializes
// configuration values or Secret reference targets.
func RenderManifest(aliasJSON []byte, provenance ManifestProvenance) ([]byte, error) {
	if err := validateManifestProvenance(provenance); err != nil {
		return nil, fmt.Errorf("%w: configuration provenance: %v", ErrResolution, err)
	}
	var aliases struct {
		CapabilityAliases json.RawMessage `json:"capability_aliases"`
	}
	if err := json.Unmarshal(aliasJSON, &aliases); err != nil || !jsonArray(aliases.CapabilityAliases) {
		return nil, fmt.Errorf("%w: final Alias manifest is invalid", ErrResolution)
	}
	baselines := make([]applicationManifestSelectionBaseline, len(provenance.baselines))
	for baselineIndex, selection := range provenance.baselines {
		records := selection.baseline.Records()
		serialized := make([]applicationManifestProvenance, len(records))
		for index, record := range records {
			serialized[index] = applicationManifestProvenance{
				Path:    record.Path,
				Digest:  record.Digest,
				Removed: record.Removed,
				Sources: append([]string(nil), record.Sources...),
			}
		}
		baselines[baselineIndex] = applicationManifestSelectionBaseline{
			Mode:                        selection.mode,
			Path:                        selection.path,
			DependencyCompositionDigest: selection.baseline.Digest(),
			DependencyBaseline:          serialized,
		}
	}
	document := applicationManifestDocument{
		CapabilityAliases: aliases.CapabilityAliases,
		Configuration: applicationManifestConfiguration{
			Version: applicationManifestConfigurationVersion,
			Mode:    provenance.mode,
			Root: applicationManifestDocumentReference{
				Path:   provenance.rootPath,
				Digest: provenance.rootDigest,
			},
			Selected: applicationManifestDocumentReference{
				Path:   provenance.selectedPath,
				Digest: provenance.selectedDigest,
			},
			DependencyBaselines:    baselines,
			ApplicationModelDigest: provenance.applicationModelDigest,
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeManifestProvenance restores and validates the one current strict
// generated-manifest schema. Pre-release schema variants are rejected.
func DecodeManifestProvenance(data []byte) (ManifestProvenance, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document applicationManifestDocument
	if err := decoder.Decode(&document); err != nil {
		return ManifestProvenance{}, fmt.Errorf("decode generated application manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ManifestProvenance{}, errors.New("generated application manifest contains trailing JSON")
	}
	if !jsonArray(document.CapabilityAliases) {
		return ManifestProvenance{}, errors.New("generated application manifest capability_aliases must be an array")
	}
	configuration := document.Configuration
	if configuration.Version != applicationManifestConfigurationVersion {
		return ManifestProvenance{}, fmt.Errorf("generated application manifest configuration must use version %d", applicationManifestConfigurationVersion)
	}
	if configuration.DependencyBaselines == nil {
		return ManifestProvenance{}, errors.New("generated application manifest configuration dependency_baselines must be an array")
	}
	baselines := make([]manifestSelectionBaseline, len(configuration.DependencyBaselines))
	for baselineIndex, selection := range configuration.DependencyBaselines {
		if selection.DependencyBaseline == nil {
			return ManifestProvenance{}, fmt.Errorf("generated application manifest dependency_baselines[%d].dependency_baseline must be an array", baselineIndex)
		}
		records := make([]applicationmeta.BaselineRecord, len(selection.DependencyBaseline))
		for index, record := range selection.DependencyBaseline {
			records[index] = applicationmeta.BaselineRecord{
				Path:    record.Path,
				Digest:  record.Digest,
				Removed: record.Removed,
				Sources: append([]string(nil), record.Sources...),
			}
		}
		baseline, err := applicationmeta.RestoreDependencyBaseline(selection.DependencyCompositionDigest, records)
		if err != nil {
			return ManifestProvenance{}, fmt.Errorf("generated application manifest dependency_baselines[%d]: %w", baselineIndex, err)
		}
		baselines[baselineIndex] = manifestSelectionBaseline{mode: selection.Mode, path: selection.Path, baseline: baseline}
	}
	provenance := ManifestProvenance{
		mode:                   configuration.Mode,
		rootPath:               configuration.Root.Path,
		rootDigest:             configuration.Root.Digest,
		selectedPath:           configuration.Selected.Path,
		selectedDigest:         configuration.Selected.Digest,
		baselines:              baselines,
		applicationModelDigest: configuration.ApplicationModelDigest,
	}
	if err := validateManifestProvenance(provenance); err != nil {
		return ManifestProvenance{}, fmt.Errorf("generated application manifest configuration provenance: %w", err)
	}
	return provenance, nil
}

// ApplicationModelOptions contains every non-secret semantic input that can
// change statically generated application output. Configuration path spelling
// and private runtime values are deliberately absent.
type ApplicationModelOptions struct {
	ModulePath          string
	JavaScriptPackage   string
	KernelModuleVersion string
	KernelBuildIdentity string
	Configurations      []configurationgen.Input
	Providers           []assemblygen.ProviderInput
	Resolution          generationresolution.ExtensionResult
}

type applicationModelDocument struct {
	Version              int                                   `json:"version"`
	ModulePath           string                                `json:"module_path"`
	JavaScriptPackage    string                                `json:"javascript_package"`
	KernelModuleVersion  string                                `json:"kernel_module_version"`
	KernelBuildIdentity  string                                `json:"kernel_build_identity"`
	ContextDigest        string                                `json:"context_digest"`
	AliasDigest          string                                `json:"alias_digest"`
	Configurations       []applicationModelConfiguration       `json:"configurations"`
	Providers            []applicationModelProvider            `json:"providers"`
	GenerationExtensions []applicationModelGenerationExtension `json:"generation_extensions"`
}

type applicationModelConfiguration struct {
	PluginID   string `json:"plugin_id"`
	PluginName string `json:"plugin_name"`
	Path       string `json:"path"`
	Digest     string `json:"digest"`
}

type applicationModelProvider struct {
	PluginID                  string                               `json:"plugin_id"`
	ModulePath                string                               `json:"module_path"`
	ModuleVersion             string                               `json:"module_version"`
	ImportPath                string                               `json:"import_path"`
	ConfigurationSchemaDigest string                               `json:"configuration_schema_digest"`
	Dependencies              []applicationModelProviderDependency `json:"dependencies"`
}

type applicationModelProviderDependency struct {
	Capability     string `json:"capability"`
	ContractDigest string `json:"contract_digest"`
}

type applicationModelGenerationExtension struct {
	PluginID   string   `json:"plugin_id"`
	API        string   `json:"api"`
	Package    string   `json:"package"`
	Namespaces []string `json:"namespaces"`
	Digest     string   `json:"digest"`
}

// ApplicationModelDigest returns the deterministic non-secret digest of the
// final static application model. Alias and normalized generation-extension
// state are included even though they remain outside generation.Context.
func ApplicationModelDigest(options ApplicationModelOptions) (string, error) {
	context := options.Resolution.Context()
	aliases := options.Resolution.AliasResolution()
	if !validContext(context) || !validAliases(aliases) {
		return "", fmt.Errorf("%w: final resolution is absent or invalid", ErrResolution)
	}
	configurations := append([]configurationgen.Input(nil), options.Configurations...)
	sort.Slice(configurations, func(left, right int) bool {
		if configurations[left].PluginID != configurations[right].PluginID {
			return configurations[left].PluginID < configurations[right].PluginID
		}
		return configurations[left].PluginName < configurations[right].PluginName
	})
	configurationRecords := make([]applicationModelConfiguration, len(configurations))
	for index, input := range configurations {
		file, err := configurationgen.Render(input)
		if err != nil {
			return "", fmt.Errorf("configuration model for plugin %q: %w", input.PluginID, err)
		}
		sum := sha256.Sum256(file.Data())
		configurationRecords[index] = applicationModelConfiguration{
			PluginID:   input.PluginID,
			PluginName: input.PluginName,
			Path:       file.Path(),
			Digest:     "sha256:" + hex.EncodeToString(sum[:]),
		}
	}
	providers := append([]assemblygen.ProviderInput(nil), options.Providers...)
	sort.Slice(providers, func(left, right int) bool { return providers[left].PluginID < providers[right].PluginID })
	providerRecords := make([]applicationModelProvider, len(providers))
	for index, provider := range providers {
		configurationSchemaDigest, err := configurationgen.SchemaDigest(provider.ConfigurationSchema)
		if err != nil {
			return "", fmt.Errorf("configuration schema for provider %q: %w", provider.PluginID, err)
		}
		dependencies := append([]assemblygen.DependencyInput(nil), provider.Dependencies...)
		sort.Slice(dependencies, func(left, right int) bool { return dependencies[left].Capability < dependencies[right].Capability })
		dependencyRecords := make([]applicationModelProviderDependency, len(dependencies))
		for dependencyIndex, dependency := range dependencies {
			sum := sha256.Sum256(dependency.ContractJSON)
			dependencyRecords[dependencyIndex] = applicationModelProviderDependency{
				Capability:     dependency.Capability,
				ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
			}
		}
		providerRecords[index] = applicationModelProvider{
			PluginID:                  provider.PluginID,
			ModulePath:                provider.ModulePath,
			ModuleVersion:             provider.ModuleVersion,
			ImportPath:                provider.ImportPath,
			ConfigurationSchemaDigest: "sha256:" + hex.EncodeToString(configurationSchemaDigest[:]),
			Dependencies:              dependencyRecords,
		}
	}
	outputs := options.Resolution.Outputs()
	extensions := make([]applicationModelGenerationExtension, len(outputs))
	for index, output := range outputs {
		extensions[index] = applicationModelGenerationExtension{
			PluginID:   output.PluginID(),
			API:        output.API(),
			Package:    output.Package(),
			Namespaces: output.Namespaces(),
			Digest:     output.Output().Digest(),
		}
		if !validSHA256(extensions[index].Digest) {
			return "", fmt.Errorf("%w: generation extension %q has an invalid output digest", ErrResolution, output.PluginID())
		}
	}
	sort.Slice(extensions, func(left, right int) bool { return extensions[left].PluginID < extensions[right].PluginID })
	document := applicationModelDocument{
		Version:              1,
		ModulePath:           options.ModulePath,
		JavaScriptPackage:    options.JavaScriptPackage,
		KernelModuleVersion:  options.KernelModuleVersion,
		KernelBuildIdentity:  options.KernelBuildIdentity,
		ContextDigest:        context.Digest(),
		AliasDigest:          aliases.Digest(),
		Configurations:       configurationRecords,
		Providers:            providerRecords,
		GenerationExtensions: extensions,
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode application model: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ConfigurationDigest returns a deterministic semantic digest for one valid
// Plystra application configuration. Comments, scalar style, and mapping order
// do not enter the digest; sequence order, scalar types, and tombstones do.
func ConfigurationDigest(data []byte) (string, error) {
	if _, err := applicationmeta.Parse(data); err != nil {
		return "", fmt.Errorf("parse application configuration: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return "", fmt.Errorf("decode application configuration: %w", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("application configuration contains multiple YAML documents")
		}
		return "", fmt.Errorf("decode trailing application configuration: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return "", errors.New("application configuration must contain one YAML document")
	}
	var canonical bytes.Buffer
	if err := writeCanonicalYAML(&canonical, document.Content[0]); err != nil {
		return "", fmt.Errorf("normalize application configuration: %w", err)
	}
	sum := sha256.Sum256(canonical.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateManifestProvenance(provenance ManifestProvenance) error {
	if provenance.mode != ConfigurationModeDefault && provenance.mode != ConfigurationModeExplicit {
		return fmt.Errorf("mode must be %q or %q", ConfigurationModeDefault, ConfigurationModeExplicit)
	}
	if provenance.rootPath != rootConfigurationPath || !safeManifestPath(provenance.rootPath) {
		return fmt.Errorf("root path must be %q", rootConfigurationPath)
	}
	if !safeManifestPath(provenance.selectedPath) {
		return errors.New("selected path must be a canonical Project-relative slash path")
	}
	if provenance.mode == ConfigurationModeDefault && provenance.selectedPath != provenance.rootPath {
		return errors.New("default selection must use the root configuration path")
	}
	if !validSHA256(provenance.rootDigest) || !validSHA256(provenance.selectedDigest) {
		return errors.New("root and selected document digests must be lower-case SHA-256 digests")
	}
	if provenance.mode == ConfigurationModeDefault && provenance.rootDigest != provenance.selectedDigest {
		return errors.New("default root and selected document digests must match")
	}
	if len(provenance.baselines) == 0 {
		return errors.New("dependency configuration baseline history is absent")
	}
	active := 0
	for index, selection := range provenance.baselines {
		if selection.mode != ConfigurationModeDefault && selection.mode != ConfigurationModeExplicit {
			return fmt.Errorf("dependency baseline %d has an invalid selection mode", index)
		}
		if !safeManifestPath(selection.path) || !selection.baseline.Valid() {
			return fmt.Errorf("dependency baseline %d has an invalid selection path or baseline", index)
		}
		if selection.mode == ConfigurationModeDefault && selection.path != rootConfigurationPath {
			return fmt.Errorf("dependency baseline %d default selection must use %q", index, rootConfigurationPath)
		}
		if index > 0 {
			previous := provenance.baselines[index-1]
			if previous.mode > selection.mode || (previous.mode == selection.mode && previous.path >= selection.path) {
				return errors.New("dependency baseline selections must be unique and canonically ordered")
			}
		}
		if selection.mode == provenance.mode && selection.path == provenance.selectedPath {
			active++
		}
	}
	if active != 1 {
		return errors.New("dependency baseline history must contain the active selection exactly once")
	}
	if !validSHA256(provenance.applicationModelDigest) {
		return errors.New("application-model digest must be a lower-case SHA-256 digest")
	}
	return nil
}

func cloneSelectionBaselines(values []manifestSelectionBaseline) []manifestSelectionBaseline {
	result := make([]manifestSelectionBaseline, len(values))
	copy(result, values)
	return result
}

func safeManifestPath(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.Contains(value, "\\") &&
		!strings.ContainsRune(value, 0) && !strings.HasPrefix(value, "/") &&
		path.Clean(value) == value && value != "." && value != ".." &&
		!strings.HasPrefix(value, "../")
}

func validSHA256(value string) bool {
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

func writeCanonicalYAML(buffer *bytes.Buffer, node *yaml.Node) error {
	if node == nil || node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
		return errors.New("YAML anchors and aliases are not allowed")
	}
	switch node.Kind {
	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return errors.New("mapping has an odd number of nodes")
		}
		type entry struct {
			key   string
			value *yaml.Node
		}
		entries := make([]entry, 0, len(node.Content)/2)
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Anchor != "" || key.Alias != nil {
				return errors.New("mapping contains a non-string key")
			}
			if _, duplicate := seen[key.Value]; duplicate {
				return fmt.Errorf("mapping contains duplicate key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
			entries = append(entries, entry{key: key.Value, value: node.Content[index+1]})
		}
		sort.Slice(entries, func(left, right int) bool { return entries[left].key < entries[right].key })
		writeCanonicalToken(buffer, "map", strconv.Itoa(len(entries)))
		for _, current := range entries {
			writeCanonicalToken(buffer, "key", current.key)
			if err := writeCanonicalYAML(buffer, current.value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		writeCanonicalToken(buffer, "sequence", strconv.Itoa(len(node.Content)))
		for _, child := range node.Content {
			if err := writeCanonicalYAML(buffer, child); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		value, err := normalizedScalar(node)
		if err != nil {
			return err
		}
		writeCanonicalToken(buffer, node.Tag, value)
	default:
		return fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
	return nil
}

func normalizedScalar(node *yaml.Node) (string, error) {
	switch node.Tag {
	case "!!null":
		return "null", nil
	case "!!bool":
		var value bool
		if err := node.Decode(&value); err != nil {
			return "", fmt.Errorf("decode boolean scalar: %w", err)
		}
		return strconv.FormatBool(value), nil
	case "!!int":
		var value any
		if err := node.Decode(&value); err != nil {
			return "", fmt.Errorf("decode integer scalar: %w", err)
		}
		return fmt.Sprint(value), nil
	case "!!float":
		var value float64
		if err := node.Decode(&value); err != nil {
			return "", fmt.Errorf("decode floating-point scalar: %w", err)
		}
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case "!!timestamp":
		var value time.Time
		if err := node.Decode(&value); err != nil {
			return "", fmt.Errorf("decode timestamp scalar: %w", err)
		}
		return value.UTC().Format(time.RFC3339Nano), nil
	default:
		return node.Value, nil
	}
}

func writeCanonicalToken(buffer *bytes.Buffer, kind, value string) {
	buffer.WriteString(strconv.Itoa(len(kind)))
	buffer.WriteByte(':')
	buffer.WriteString(kind)
	buffer.WriteString(strconv.Itoa(len(value)))
	buffer.WriteByte(':')
	buffer.WriteString(value)
}

func jsonArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']'
}
