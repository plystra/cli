package generation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/module"
)

// ErrInvalidContext reports generation input that is not one complete,
// internally consistent normalized application view.
var ErrInvalidContext = errors.New("invalid generation context")

// Exposure is the normalized set of generated application surfaces available
// to a canonical Capability or Alias.
type Exposure struct {
	Go         bool `json:"go"`
	HTTP       bool `json:"http"`
	JavaScript bool `json:"javascript"`
}

// AliasSourceKind identifies normalized Alias provenance.
type AliasSourceKind string

const (
	// AliasSourceApplication identifies an explicit plystra.yaml declaration.
	AliasSourceApplication AliasSourceKind = "application"
	// AliasSourceGenerationExtension identifies a selected plugin extension.
	AliasSourceGenerationExtension AliasSourceKind = "generation-extension"
)

// Input is the construction-only representation of one normalized resolved
// application. NewContext validates, sorts, canonicalizes, and defensively
// copies every member before exposing it to an extension.
type Input struct {
	Plugins           []PluginInput          `json:"plugins"`
	Capabilities      []CapabilityInput      `json:"capabilities"`
	Requirements      []string               `json:"requirements"`
	Providers         []ProviderInput        `json:"providers"`
	CapabilityAliases []CapabilityAliasInput `json:"capability_aliases"`
}

// PluginInput describes public selected-plugin metadata. BuildMetadataJSON
// must contain only fields explicitly classified as non-secret and
// build-visible by the CLI.
type PluginInput struct {
	ID                string          `json:"id"`
	ModulePath        string          `json:"module_path"`
	ModuleVersion     string          `json:"module_version,omitempty"`
	Provides          []string        `json:"provides"`
	Requires          []string        `json:"requires"`
	BuildMetadataJSON json.RawMessage `json:"build_metadata"`
}

// CapabilityInput carries one complete canonical normalized contract. The ID
// and namespaced extension metadata are read from ContractJSON.
type CapabilityInput struct {
	ContractJSON json.RawMessage `json:"contract"`
	Intrinsic    bool            `json:"intrinsic"`
	Exposure     Exposure        `json:"exposure"`
}

// ProviderInput records one selected ordinary canonical provider.
type ProviderInput struct {
	Capability string `json:"capability"`
	Plugin     string `json:"plugin"`
}

// CapabilityAliasInput carries one already-normalized application-local Alias.
// Parsing and merging declarations and contributions remains CLI-owned.
type CapabilityAliasInput struct {
	ID         string             `json:"id"`
	Target     string             `json:"target"`
	Exposure   Exposure           `json:"exposure"`
	Deprecated string             `json:"deprecated,omitempty"`
	Sources    []AliasSourceInput `json:"sources"`
}

// AliasSourceInput records one normalized Alias source.
type AliasSourceInput struct {
	Kind AliasSourceKind `json:"kind"`
	ID   string          `json:"id"`
}

// GenerationContext is the read-only input surface supported by v1 extension
// entry points.
type GenerationContext interface {
	APIVersion() string
	Plugins() []PluginView
	Plugin(PluginID) (PluginView, bool)
	Capabilities() []CapabilityView
	Capability(CapabilityID) (CapabilityView, bool)
	Requirements() []CapabilityID
	Providers() []ProviderView
	SelectedProvider(CapabilityID) (PluginID, bool)
	CapabilityAliases() []CapabilityAliasView
	CapabilityAlias(CapabilityID) (CapabilityAliasView, bool)
	CanonicalJSON() []byte
	Digest() string
}

// Context is one immutable, canonically ordered normalized application view.
type Context struct {
	plugins              []PluginView
	pluginIndex          map[PluginID]int
	capabilities         []CapabilityView
	capabilityIndex      map[CapabilityID]int
	requirements         []CapabilityID
	providers            []ProviderView
	providerByCapability map[CapabilityID]PluginID
	aliases              []CapabilityAliasView
	aliasIndex           map[CapabilityID]int
	canonicalJSON        []byte
	digest               string
}

var _ GenerationContext = Context{}

// NewContext validates and canonicalizes one complete generation input.
func NewContext(input Input) (Context, error) {
	plugins, pluginIndex, err := normalizePlugins(input.Plugins)
	if err != nil {
		return Context{}, err
	}
	capabilities, capabilityIndex, err := normalizeCapabilities(input.Capabilities)
	if err != nil {
		return Context{}, err
	}
	requirements, requirementSet, err := normalizeRequirements(input.Requirements, capabilityIndex)
	if err != nil {
		return Context{}, err
	}
	providers, providerByCapability, err := normalizeProviders(input.Providers, plugins, pluginIndex, capabilities, capabilityIndex, requirementSet)
	if err != nil {
		return Context{}, err
	}
	aliases, aliasIndex, err := normalizeAliases(input.CapabilityAliases, pluginIndex, capabilities, capabilityIndex, requirementSet)
	if err != nil {
		return Context{}, err
	}
	if err := validateResolvedPluginRequirements(plugins, capabilities, capabilityIndex, requirementSet, aliasIndex); err != nil {
		return Context{}, err
	}
	if err := validateRequiredProviders(requirements, capabilities, capabilityIndex, providerByCapability); err != nil {
		return Context{}, err
	}

	canonical, err := encodeContext(plugins, capabilities, requirements, providers, aliases)
	if err != nil {
		return Context{}, invalidContext("encode canonical input: %v", err)
	}
	return Context{
		plugins:              plugins,
		pluginIndex:          pluginIndex,
		capabilities:         capabilities,
		capabilityIndex:      capabilityIndex,
		requirements:         requirements,
		providers:            providers,
		providerByCapability: providerByCapability,
		aliases:              aliases,
		aliasIndex:           aliasIndex,
		canonicalJSON:        canonical,
		digest:               sha256Digest(canonical),
	}, nil
}

// APIVersion returns the exact generation protocol version.
func (Context) APIVersion() string { return Version }

// Plugins returns defensive view copies sorted by Plugin ID.
func (c Context) Plugins() []PluginView { return append([]PluginView(nil), c.plugins...) }

// Plugin returns one selected plugin by exact ID.
func (c Context) Plugin(id PluginID) (PluginView, bool) {
	index, ok := c.pluginIndex[id]
	if !ok {
		return PluginView{}, false
	}
	return c.plugins[index], true
}

// Capabilities returns defensive view copies sorted by canonical ID.
func (c Context) Capabilities() []CapabilityView {
	return append([]CapabilityView(nil), c.capabilities...)
}

// Capability returns one canonical Capability view by exact ID. Alias IDs are
// never resolved by this method.
func (c Context) Capability(id CapabilityID) (CapabilityView, bool) {
	index, ok := c.capabilityIndex[id]
	if !ok {
		return CapabilityView{}, false
	}
	return c.capabilities[index], true
}

// Requirements returns exact canonical requirements sorted by ID.
func (c Context) Requirements() []CapabilityID {
	return append([]CapabilityID(nil), c.requirements...)
}

// Providers returns selected ordinary canonical providers sorted by Capability.
func (c Context) Providers() []ProviderView {
	return append([]ProviderView(nil), c.providers...)
}

// SelectedProvider returns the selected ordinary plugin provider. Intrinsic
// Capabilities and Alias IDs have no selected plugin provider.
func (c Context) SelectedProvider(id CapabilityID) (PluginID, bool) {
	provider, ok := c.providerByCapability[id]
	return provider, ok
}

// CapabilityAliases returns application-local Alias views sorted by Alias ID.
func (c Context) CapabilityAliases() []CapabilityAliasView {
	return append([]CapabilityAliasView(nil), c.aliases...)
}

// CapabilityAlias returns one application-local Alias by exact ID.
func (c Context) CapabilityAlias(id CapabilityID) (CapabilityAliasView, bool) {
	index, ok := c.aliasIndex[id]
	if !ok {
		return CapabilityAliasView{}, false
	}
	return c.aliases[index], true
}

// CanonicalJSON returns a defensive copy of the deterministic supported input.
func (c Context) CanonicalJSON() []byte { return append([]byte(nil), c.canonicalJSON...) }

// Digest returns the sha256 digest of CanonicalJSON with a sha256: prefix.
func (c Context) Digest() string { return c.digest }

// ModuleView contains public Go Module provenance without a local filesystem
// path. An empty version identifies the current local development module.
type ModuleView struct {
	path    string
	version string
}

// Path returns the canonical Go Module path.
func (m ModuleView) Path() string { return m.path }

// Version returns the selected Go Module version, or empty for local source.
func (m ModuleView) Version() string { return m.version }

// PluginView is immutable public selected-plugin metadata.
type PluginView struct {
	id                PluginID
	module            ModuleView
	provides          []CapabilityID
	requires          []CapabilityID
	buildMetadataJSON []byte
}

// ID returns the exact Plugin ID.
func (p PluginView) ID() PluginID { return p.id }

// Module returns public Go Module provenance.
func (p PluginView) Module() ModuleView { return p.module }

// Provides returns canonical Capabilities declared by this plugin, sorted by ID.
func (p PluginView) Provides() []CapabilityID {
	return append([]CapabilityID(nil), p.provides...)
}

// Requires returns non-inferable canonical requirements, sorted by ID.
func (p PluginView) Requires() []CapabilityID {
	return append([]CapabilityID(nil), p.requires...)
}

// BuildMetadataJSON returns a defensive copy of canonical explicitly
// build-visible, non-secret plugin metadata.
func (p PluginView) BuildMetadataJSON() []byte {
	return append([]byte(nil), p.buildMetadataJSON...)
}

// CapabilityView is one immutable exact canonical contract and application
// exposure view.
type CapabilityView struct {
	id             CapabilityID
	contractJSON   []byte
	contractDigest string
	extensions     []ExtensionView
	intrinsic      bool
	exposure       Exposure
}

// ID returns the exact canonical Capability ID.
func (c CapabilityView) ID() CapabilityID { return c.id }

// ContractJSON returns the complete canonical normalized contract.
func (c CapabilityView) ContractJSON() []byte {
	return append([]byte(nil), c.contractJSON...)
}

// ContractDigest returns the sha256 digest of ContractJSON.
func (c CapabilityView) ContractDigest() string { return c.contractDigest }

// Extensions returns normalized values sorted by namespace.
func (c CapabilityView) Extensions() []ExtensionView {
	return append([]ExtensionView(nil), c.extensions...)
}

// Extension returns one normalized namespace value.
func (c CapabilityView) Extension(namespace string) (ExtensionView, bool) {
	index := sort.Search(len(c.extensions), func(index int) bool {
		return c.extensions[index].namespace >= namespace
	})
	if index >= len(c.extensions) || c.extensions[index].namespace != namespace {
		return ExtensionView{}, false
	}
	return c.extensions[index], true
}

// Intrinsic reports whether the Kernel owns this canonical Capability.
func (c CapabilityView) Intrinsic() bool { return c.intrinsic }

// Exposure returns the normalized generated application surfaces.
func (c CapabilityView) Exposure() Exposure { return c.exposure }

// ExtensionView is one immutable normalized namespaced contract value.
type ExtensionView struct {
	namespace string
	valueJSON []byte
}

// Namespace returns the canonical lower-kebab namespace.
func (e ExtensionView) Namespace() string { return e.namespace }

// ValueJSON returns a defensive copy of the canonical JSON-compatible value.
func (e ExtensionView) ValueJSON() []byte {
	return append([]byte(nil), e.valueJSON...)
}

// ProviderView records one selected ordinary canonical provider.
type ProviderView struct {
	capability CapabilityID
	plugin     PluginID
}

// Capability returns the exact canonical Capability ID.
func (p ProviderView) Capability() CapabilityID { return p.capability }

// Plugin returns the selected Plugin ID.
func (p ProviderView) Plugin() PluginID { return p.plugin }

// CapabilityAliasView is one immutable direct application-local forwarding
// name. It is never a provider or Kernel registry entry.
type CapabilityAliasView struct {
	id         CapabilityID
	target     CapabilityID
	exposure   Exposure
	deprecated string
	sources    []AliasSourceView
}

// ID returns the application-local Alias ID.
func (a CapabilityAliasView) ID() CapabilityID { return a.id }

// Target returns the direct canonical target ID.
func (a CapabilityAliasView) Target() CapabilityID { return a.target }

// Exposure returns the normalized inherited or narrowed surfaces.
func (a CapabilityAliasView) Exposure() Exposure { return a.exposure }

// Deprecated returns the application-local deprecation message, if any.
func (a CapabilityAliasView) Deprecated() string { return a.deprecated }

// Sources returns deterministic all-source provenance.
func (a CapabilityAliasView) Sources() []AliasSourceView {
	return append([]AliasSourceView(nil), a.sources...)
}

// AliasSourceView is one immutable Alias provenance record.
type AliasSourceView struct {
	kind AliasSourceKind
	id   string
}

// Kind returns the normalized source kind.
func (s AliasSourceView) Kind() AliasSourceKind { return s.kind }

// ID returns "application" or the contributing selected Plugin ID.
func (s AliasSourceView) ID() string { return s.id }

func normalizePlugins(inputs []PluginInput) ([]PluginView, map[PluginID]int, error) {
	plugins := make([]PluginView, 0, len(inputs))
	seen := make(map[PluginID]struct{}, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("plugins[%d]", index)
		id, err := contextPluginID(field+".id", input.ID)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, invalidContext("%s.id duplicates plugin %q", field, id.String())
		}
		moduleView, err := normalizeModule(field, input.ModulePath, input.ModuleVersion)
		if err != nil {
			return nil, nil, err
		}
		provides, err := normalizeCapabilityIDs(field+".provides", input.Provides)
		if err != nil {
			return nil, nil, err
		}
		requires, err := normalizeCapabilityIDs(field+".requires", input.Requires)
		if err != nil {
			return nil, nil, err
		}
		metadata, _, err := normalizeJSONObject(input.BuildMetadataJSON, true)
		if err != nil {
			return nil, nil, invalidContext("%s.build_metadata: %v", field, err)
		}
		seen[id] = struct{}{}
		plugins = append(plugins, PluginView{
			id:                id,
			module:            moduleView,
			provides:          provides,
			requires:          requires,
			buildMetadataJSON: metadata,
		})
	}
	sort.Slice(plugins, func(left, right int) bool {
		return plugins[left].id.String() < plugins[right].id.String()
	})
	index := make(map[PluginID]int, len(plugins))
	for position, plugin := range plugins {
		index[plugin.id] = position
	}
	return plugins, index, nil
}

func normalizeModule(field, modulePath, version string) (ModuleView, error) {
	var pathErr error
	if version == "" {
		pathErr = modulepath.CheckProject(modulePath)
	} else {
		pathErr = module.CheckPath(modulePath)
	}
	if pathErr != nil {
		return ModuleView{}, invalidContext("%s.module_path %q is not canonical: %v", field, modulePath, pathErr)
	}
	if version != "" {
		if err := module.Check(modulePath, version); err != nil {
			return ModuleView{}, invalidContext("%s.module_version %q is not valid for %q: %v", field, version, modulePath, err)
		}
	}
	return ModuleView{path: modulePath, version: version}, nil
}

func normalizeCapabilities(inputs []CapabilityInput) ([]CapabilityView, map[CapabilityID]int, error) {
	capabilities := make([]CapabilityView, 0, len(inputs))
	seen := make(map[CapabilityID]struct{}, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("capabilities[%d].contract", index)
		contract, id, extensions, err := normalizeCapabilityContract(field, input.ContractJSON)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, invalidContext("%s.id duplicates canonical Capability %q", field, id.String())
		}
		reservedIntrinsic := strings.HasPrefix(id.Name(), "kernel.")
		if input.Intrinsic != reservedIntrinsic {
			return nil, nil, invalidContext("%s.id %q must be intrinsic exactly when it uses the reserved kernel.* namespace", field, id.String())
		}
		seen[id] = struct{}{}
		capabilities = append(capabilities, CapabilityView{
			id:             id,
			contractJSON:   contract,
			contractDigest: sha256Digest(contract),
			extensions:     extensions,
			intrinsic:      input.Intrinsic,
			exposure:       input.Exposure,
		})
	}
	sort.Slice(capabilities, func(left, right int) bool {
		return capabilities[left].id.String() < capabilities[right].id.String()
	})
	index := make(map[CapabilityID]int, len(capabilities))
	for position, capability := range capabilities {
		index[capability.id] = position
	}
	return capabilities, index, nil
}

func normalizeCapabilityContract(field string, input []byte) ([]byte, CapabilityID, []ExtensionView, error) {
	normalized, err := capabilitymeta.NormalizeSchema(input)
	if err != nil {
		return nil, CapabilityID{}, nil, invalidContext("%s is not a valid exact contract: %v", field, err)
	}
	if !bytes.Equal(normalized, input) {
		return nil, CapabilityID{}, nil, invalidContext("%s must already use the CLI canonical contract encoding", field)
	}
	manifest, err := capabilitymeta.Parse(normalized)
	if err != nil {
		return nil, CapabilityID{}, nil, invalidContext("%s cannot inspect normalized metadata: %v", field, err)
	}
	id, err := contextCapabilityID(field+".id", manifest.ID().String())
	if err != nil {
		return nil, CapabilityID{}, nil, err
	}
	values := manifest.Extensions().Values()
	extensions := make([]ExtensionView, len(values))
	for index, extension := range values {
		extensions[index] = ExtensionView{
			namespace: extension.Namespace(),
			valueJSON: extension.ValueJSON(),
		}
	}
	return append([]byte(nil), normalized...), id, extensions, nil
}

func normalizeRequirements(inputs []string, capabilities map[CapabilityID]int) ([]CapabilityID, map[CapabilityID]struct{}, error) {
	requirements, err := normalizeCapabilityIDs("requirements", inputs)
	if err != nil {
		return nil, nil, err
	}
	set := make(map[CapabilityID]struct{}, len(requirements))
	for _, requirement := range requirements {
		if _, ok := capabilities[requirement]; !ok {
			return nil, nil, invalidContext("requirements contains unknown canonical Capability %q", requirement.String())
		}
		set[requirement] = struct{}{}
	}
	return requirements, set, nil
}

func normalizeProviders(inputs []ProviderInput, plugins []PluginView, pluginIndex map[PluginID]int, capabilities []CapabilityView, capabilityIndex map[CapabilityID]int, requirements map[CapabilityID]struct{}) ([]ProviderView, map[CapabilityID]PluginID, error) {
	providers := make([]ProviderView, 0, len(inputs))
	byCapability := make(map[CapabilityID]PluginID, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("providers[%d]", index)
		capability, err := contextCapabilityID(field+".capability", input.Capability)
		if err != nil {
			return nil, nil, err
		}
		plugin, err := contextPluginID(field+".plugin", input.Plugin)
		if err != nil {
			return nil, nil, err
		}
		capabilityPosition, exists := capabilityIndex[capability]
		if !exists {
			return nil, nil, invalidContext("%s.capability %q is not a canonical Capability", field, capability.String())
		}
		if capabilities[capabilityPosition].intrinsic {
			return nil, nil, invalidContext("%s.capability %q is intrinsic and has no plugin provider", field, capability.String())
		}
		if _, required := requirements[capability]; !required {
			return nil, nil, invalidContext("%s.capability %q is not a current requirement", field, capability.String())
		}
		pluginPosition, exists := pluginIndex[plugin]
		if !exists {
			return nil, nil, invalidContext("%s.plugin %q is not selected", field, plugin.String())
		}
		if !containsCapabilityID(plugins[pluginPosition].provides, capability) {
			return nil, nil, invalidContext("%s.plugin %q does not provide %q", field, plugin.String(), capability.String())
		}
		if previous, duplicate := byCapability[capability]; duplicate {
			return nil, nil, invalidContext("%s.capability duplicates provider mapping for %q to %q", field, capability.String(), previous.String())
		}
		byCapability[capability] = plugin
		providers = append(providers, ProviderView{capability: capability, plugin: plugin})
	}
	sort.Slice(providers, func(left, right int) bool {
		return providers[left].capability.String() < providers[right].capability.String()
	})
	return providers, byCapability, nil
}

func normalizeAliases(inputs []CapabilityAliasInput, plugins map[PluginID]int, capabilities []CapabilityView, capabilityIndex map[CapabilityID]int, requirements map[CapabilityID]struct{}) ([]CapabilityAliasView, map[CapabilityID]int, error) {
	aliases := make([]CapabilityAliasView, 0, len(inputs))
	seen := make(map[CapabilityID]struct{}, len(inputs))
	for index, input := range inputs {
		field := fmt.Sprintf("capability_aliases[%d]", index)
		id, err := contextCapabilityID(field+".id", input.ID)
		if err != nil {
			return nil, nil, err
		}
		target, err := contextCapabilityID(field+".target", input.Target)
		if err != nil {
			return nil, nil, err
		}
		if _, collision := capabilityIndex[id]; collision {
			return nil, nil, invalidContext("%s.id %q collides with a canonical Capability", field, id.String())
		}
		if strings.HasPrefix(id.Name(), "kernel.") {
			return nil, nil, invalidContext("%s.id %q uses the reserved kernel.* canonical namespace", field, id.String())
		}
		targetPosition, exists := capabilityIndex[target]
		if !exists {
			return nil, nil, invalidContext("%s.target %q is not a canonical Capability", field, target.String())
		}
		if id.Major() != target.Major() {
			return nil, nil, invalidContext("%s.id %q and target %q must use the same major version", field, id.String(), target.String())
		}
		if _, required := requirements[target]; !required {
			return nil, nil, invalidContext("%s.target %q is not a current canonical requirement", field, target.String())
		}
		if !exposureSubset(input.Exposure, capabilities[targetPosition].exposure) {
			return nil, nil, invalidContext("%s.exposure broadens target %q exposure", field, target.String())
		}
		if err := validateDeprecation(field, input.Deprecated); err != nil {
			return nil, nil, err
		}
		sources, err := normalizeAliasSources(field, input.Sources, plugins)
		if err != nil {
			return nil, nil, err
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, nil, invalidContext("%s.id duplicates Alias %q", field, id.String())
		}
		seen[id] = struct{}{}
		aliases = append(aliases, CapabilityAliasView{
			id:         id,
			target:     target,
			exposure:   input.Exposure,
			deprecated: input.Deprecated,
			sources:    sources,
		})
	}
	sort.Slice(aliases, func(left, right int) bool {
		return aliases[left].id.String() < aliases[right].id.String()
	})
	index := make(map[CapabilityID]int, len(aliases))
	for position, alias := range aliases {
		index[alias.id] = position
	}
	return aliases, index, nil
}

func normalizeAliasSources(field string, inputs []AliasSourceInput, plugins map[PluginID]int) ([]AliasSourceView, error) {
	if len(inputs) == 0 {
		return nil, invalidContext("%s.sources must not be empty", field)
	}
	sources := make([]AliasSourceView, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))
	for index, input := range inputs {
		path := fmt.Sprintf("%s.sources[%d]", field, index)
		switch input.Kind {
		case AliasSourceApplication:
			if input.ID != "application" {
				return nil, invalidContext("%s.id must be %q for source kind %q", path, "application", input.Kind)
			}
		case AliasSourceGenerationExtension:
			plugin, err := contextPluginID(path+".id", input.ID)
			if err != nil {
				return nil, err
			}
			if _, selected := plugins[plugin]; !selected {
				return nil, invalidContext("%s.id %q is not a selected plugin", path, input.ID)
			}
		default:
			return nil, invalidContext("%s.kind %q is not supported", path, input.Kind)
		}
		key := string(input.Kind) + "\x00" + input.ID
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidContext("%s duplicates Alias source %q %q", path, input.Kind, input.ID)
		}
		seen[key] = struct{}{}
		sources = append(sources, AliasSourceView{kind: input.Kind, id: input.ID})
	}
	sort.Slice(sources, func(left, right int) bool {
		if sources[left].kind != sources[right].kind {
			return sources[left].kind < sources[right].kind
		}
		return sources[left].id < sources[right].id
	})
	return sources, nil
}

func validateResolvedPluginRequirements(plugins []PluginView, capabilityViews []CapabilityView, capabilities map[CapabilityID]int, requirements map[CapabilityID]struct{}, aliases map[CapabilityID]int) error {
	for _, plugin := range plugins {
		for _, provided := range plugin.provides {
			position, exists := capabilities[provided]
			if !exists {
				return invalidContext("plugin %q provides unknown canonical Capability %q", plugin.id.String(), provided.String())
			}
			if capabilityViews[position].intrinsic {
				return invalidContext("plugin %q cannot provide intrinsic Capability %q", plugin.id.String(), provided.String())
			}
		}
		for _, required := range plugin.requires {
			if _, alias := aliases[required]; alias {
				return invalidContext("plugin %q requires application-local Alias %q", plugin.id.String(), required.String())
			}
			if _, exists := capabilities[required]; !exists {
				return invalidContext("plugin %q requires unknown canonical Capability %q", plugin.id.String(), required.String())
			}
			if _, resolved := requirements[required]; !resolved {
				return invalidContext("plugin %q requirement %q is absent from the resolved requirement set", plugin.id.String(), required.String())
			}
		}
	}
	return nil
}

func validateRequiredProviders(requirements []CapabilityID, capabilities []CapabilityView, capabilityIndex map[CapabilityID]int, providers map[CapabilityID]PluginID) error {
	for _, requirement := range requirements {
		capability := capabilities[capabilityIndex[requirement]]
		_, selected := providers[requirement]
		if capability.intrinsic && selected {
			return invalidContext("intrinsic Capability %q must not have a plugin provider", requirement.String())
		}
		if !capability.intrinsic && !selected {
			return invalidContext("required canonical Capability %q has no selected provider", requirement.String())
		}
	}
	return nil
}

func normalizeCapabilityIDs(field string, values []string) ([]CapabilityID, error) {
	identifiers := make([]CapabilityID, 0, len(values))
	seen := make(map[CapabilityID]struct{}, len(values))
	for index, value := range values {
		identifier, err := contextCapabilityID(fmt.Sprintf("%s[%d]", field, index), value)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[identifier]; duplicate {
			return nil, invalidContext("%s contains duplicate Capability %q", field, identifier.String())
		}
		seen[identifier] = struct{}{}
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left].String() < identifiers[right].String()
	})
	return identifiers, nil
}

func contextCapabilityID(field, value string) (CapabilityID, error) {
	id, err := ParseCapabilityID(value)
	if err != nil {
		return CapabilityID{}, invalidContext("%s %q is not canonical: %v", field, value, err)
	}
	return id, nil
}

func contextPluginID(field, value string) (PluginID, error) {
	id, err := ParsePluginID(value)
	if err != nil {
		return PluginID{}, invalidContext("%s %q is not canonical: %v", field, value, err)
	}
	return id, nil
}

func containsCapabilityID(values []CapabilityID, target CapabilityID) bool {
	index := sort.Search(len(values), func(index int) bool {
		return values[index].String() >= target.String()
	})
	return index < len(values) && values[index] == target
}

func exposureSubset(alias, target Exposure) bool {
	return (!alias.Go || target.Go) && (!alias.HTTP || target.HTTP) && (!alias.JavaScript || target.JavaScript)
}

func validateDeprecation(field, message string) error {
	if message == "" {
		return nil
	}
	if len(message) > 1024 || strings.ContainsRune(message, '\x00') {
		return invalidContext("%s.deprecated must be at most 1024 bytes and contain no NUL", field)
	}
	return nil
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func invalidContext(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidContext, fmt.Sprintf(format, arguments...))
}
