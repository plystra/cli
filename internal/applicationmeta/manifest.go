// Package applicationmeta parses the bounded CLI-owned configuration envelope
// of a Plystra Project's root plystra.yaml.
package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginid"
	"go.yaml.in/yaml/v3"
)

const (
	// MaximumSize is the largest application declaration inspected by the CLI.
	MaximumSize = 1 << 20
	// DefaultStartupTimeout is the runtime provider-startup bound used when
	// timeouts.startup is omitted.
	DefaultStartupTimeout = 2 * time.Minute
	// DefaultInvocationTimeout bounds one raw canonical Kernel dispatch when
	// the caller and generated application path provide no earlier deadline.
	DefaultInvocationTimeout = 30 * time.Second
	// DefaultConnectTransport enables the initial external transport when
	// http.transports.connect is omitted.
	DefaultConnectTransport = true
	// DefaultRESTTransport keeps the optional REST projection disabled when
	// http.transports.rest is omitted.
	DefaultRESTTransport = false
)

// ErrInvalidManifest reports unsafe or invalid plystra.yaml metadata.
var ErrInvalidManifest = errors.New("invalid application manifest metadata")

// Alias is one immutable explicit application-local Capability Alias
// declaration. Canonical target existence and exposure subset validation occur
// after provider resolution supplies the target catalog.
type Alias struct {
	id          capabilityid.Identifier
	target      capabilityid.Identifier
	exposure    generation.Exposure
	hasExposure bool
	deprecated  string
	source      string
}

// ID returns the canonical application-local Alias ID.
func (a Alias) ID() capabilityid.Identifier { return a.id }

// Target returns the direct canonical target ID declared by the application.
func (a Alias) Target() capabilityid.Identifier { return a.target }

// Exposure returns explicit requested surfaces. A false result means inherit
// all application surfaces available to the canonical target.
func (a Alias) Exposure() (generation.Exposure, bool) {
	if !a.hasExposure {
		return generation.Exposure{}, false
	}
	return a.exposure, true
}

// Deprecated returns the application-local deprecation message, if any.
func (a Alias) Deprecated() string { return a.deprecated }

// Source returns stable configuration-path provenance for diagnostics.
func (a Alias) Source() string { return a.source }

// HTTPExposure is one explicit canonical Capability selected for generated
// HTTP and browser-facing application surfaces.
type HTTPExposure struct {
	id     capabilityid.Identifier
	source string
}

// ID returns the exact canonical Capability ID declared under http.expose.
func (e HTTPExposure) ID() capabilityid.Identifier { return e.id }

// Source returns stable configuration-path provenance for diagnostics.
func (e HTTPExposure) Source() string { return e.source }

// HTTPTransports is the closed selected-current-project external transport
// choice. The zero value is not the schema default; callers obtain resolved
// defaults through Manifest.HTTPTransports.
type HTTPTransports struct {
	Connect bool
	REST    bool
}

type httpTransportLayer struct {
	connect       bool
	hasConnect    bool
	removeConnect bool
	rest          bool
	hasREST       bool
	removeREST    bool
}

// HTTPCORS is one selected-current-project cross-origin policy. Values returned
// by Manifest.HTTPCORS are normalized, sorted, deduplicated defensive copies.
type HTTPCORS struct {
	AllowedOrigins   []string
	AllowCredentials bool
}

// NormalizeHTTPCORS returns one canonical defensive copy of a selected CORS
// policy. It is also the validation boundary for synthetic generation input.
func NormalizeHTTPCORS(cors HTTPCORS) (HTTPCORS, error) {
	origins, err := normalizeHTTPCORSOrigins(cors.AllowedOrigins)
	if err != nil {
		return HTTPCORS{}, err
	}
	if cors.AllowCredentials && slices.Contains(origins, "*") {
		return HTTPCORS{}, invalid("http.cors cannot combine wildcard origin %q with allow_credentials: true", "*")
	}
	return HTTPCORS{
		AllowedOrigins:   origins,
		AllowCredentials: cors.AllowCredentials,
	}, nil
}

type httpCORSLayer struct {
	allowedOrigins         []string
	hasAllowedOrigins      bool
	allowCredentials       bool
	hasAllowCredentials    bool
	removeAllowCredentials bool
	present                bool
	remove                 bool
}

// CapabilityRequirement is one explicit canonical Capability requirement.
type CapabilityRequirement struct {
	id     capabilityid.Identifier
	source string
}

// ID returns the exact canonical Capability ID declared under
// capabilities.require.
func (r CapabilityRequirement) ID() capabilityid.Identifier { return r.id }

// Source returns stable configuration-path provenance for diagnostics.
func (r CapabilityRequirement) Source() string { return r.source }

// capabilityRemoval is one typed null or sparse-set tombstone retained on a
// parsed configuration layer until schema-aware composition applies it.
type capabilityRemoval struct {
	id     capabilityid.Identifier
	source string
}

// ProviderChoice is one explicit canonical Capability-to-Plugin selection.
type ProviderChoice struct {
	capability capabilityid.Identifier
	pluginID   string
	source     string
}

// Capability returns the exact canonical Capability selected under
// capabilities.use.
func (c ProviderChoice) Capability() capabilityid.Identifier { return c.capability }

// PluginID returns the canonical selected Plugin ID.
func (c ProviderChoice) PluginID() string { return c.pluginID }

// Source returns stable configuration-path provenance for diagnostics.
func (c ProviderChoice) Source() string { return c.source }

// PluginConfiguration is one immutable plugin-owned runtime configuration
// mapping. Its values and Secret reference targets remain redacted from
// formatting and are never part of generation-extension input.
type PluginConfiguration struct {
	pluginID string
	source   string
	yaml     []byte
}

type pluginConfigurationRemoval struct {
	pluginID string
	source   string
}

// PluginID returns the canonical configured Plugin ID.
func (c PluginConfiguration) PluginID() string { return c.pluginID }

// Source returns stable configuration-path provenance for diagnostics.
func (c PluginConfiguration) Source() string { return c.source }

// YAML returns defensive bytes for CLI-owned validation and generated runtime
// binding. Callers must not copy them into diagnostics or extension input.
func (c PluginConfiguration) YAML() []byte { return append([]byte(nil), c.yaml...) }

// String returns only a redaction marker.
func (PluginConfiguration) String() string { return "<redacted-plugin-configuration>" }

// GoString prevents Go-syntax formatting from exposing configuration values.
func (PluginConfiguration) GoString() string { return "<redacted-plugin-configuration>" }

// Format redacts configuration values for every fmt verb.
func (PluginConfiguration) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-plugin-configuration>"))
}

// LogValue redacts configuration values for structured standard-library
// logging.
func (PluginConfiguration) LogValue() slog.Value {
	return slog.StringValue("<redacted-plugin-configuration>")
}

// Manifest is the immutable normalized application metadata used by canonical
// provider, HTTP exposure, and Capability Alias resolution.
type Manifest struct {
	httpAddress            string
	hasHTTPAddress         bool
	removeHTTPAddress      bool
	httpTransports         httpTransportLayer
	httpCORS               httpCORSLayer
	httpExposures          []HTTPExposure
	removedHTTPExposures   []capabilityRemoval
	requirements           []CapabilityRequirement
	removedRequirements    []capabilityRemoval
	providerChoices        []ProviderChoice
	removedProviderChoices []capabilityRemoval
	aliases                []Alias
	removedAliases         []capabilityRemoval
	configurations         []PluginConfiguration
	removedConfigurations  []pluginConfigurationRemoval
	startupTimeout         time.Duration
	hasStartupTimeout      bool
	removeStartupTimeout   bool
}

// String returns only a redaction marker because the manifest can contain
// private runtime configuration and Secret reference targets.
func (Manifest) String() string { return "<redacted-application-manifest>" }

// GoString prevents Go-syntax formatting from exposing private configuration.
func (Manifest) GoString() string { return "<redacted-application-manifest>" }

// Format redacts the complete manifest for every fmt verb.
func (Manifest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-application-manifest>"))
}

// LogValue redacts the complete manifest for structured standard-library
// logging.
func (Manifest) LogValue() slog.Value {
	return slog.StringValue("<redacted-application-manifest>")
}

// HTTPAddress returns the explicitly configured listener address. A false
// result means the http section or address field was omitted.
func (m Manifest) HTTPAddress() (string, bool) { return m.httpAddress, m.hasHTTPAddress }

// HTTPTransports returns the selected closed transport values after applying
// the schema defaults for omitted or explicitly removed fields.
func (m Manifest) HTTPTransports() HTTPTransports {
	result := HTTPTransports{
		Connect: DefaultConnectTransport,
		REST:    DefaultRESTTransport,
	}
	if m.httpTransports.hasConnect {
		result.Connect = m.httpTransports.connect
	}
	if m.httpTransports.hasREST {
		result.REST = m.httpTransports.rest
	}
	return result
}

// HTTPCORS returns the normalized optional selected-current-project CORS
// declaration. The returned origins do not alias Manifest storage.
func (m Manifest) HTTPCORS() (HTTPCORS, bool) {
	if !m.httpCORS.present || m.httpCORS.remove {
		return HTTPCORS{}, false
	}
	return HTTPCORS{
		AllowedOrigins:   append([]string(nil), m.httpCORS.allowedOrigins...),
		AllowCredentials: m.httpCORS.allowCredentials,
	}, true
}

// StartupTimeout returns the normalized positive provider-startup timeout.
// Omitted timeouts.startup uses DefaultStartupTimeout.
func (m Manifest) StartupTimeout() time.Duration { return m.startupTimeout }

// HTTPExposures returns defensive declarations sorted by canonical ID.
func (m Manifest) HTTPExposures() []HTTPExposure {
	return append([]HTTPExposure(nil), m.httpExposures...)
}

// Requirements returns defensive declarations sorted by canonical ID.
func (m Manifest) Requirements() []CapabilityRequirement {
	return append([]CapabilityRequirement(nil), m.requirements...)
}

// ProviderChoices returns defensive declarations sorted by canonical
// Capability ID.
func (m Manifest) ProviderChoices() []ProviderChoice {
	return append([]ProviderChoice(nil), m.providerChoices...)
}

// Aliases returns defensive declarations sorted by Alias ID.
func (m Manifest) Aliases() []Alias { return append([]Alias(nil), m.aliases...) }

// Configurations returns defensive Plugin-ID-sorted runtime configuration
// declarations. Each value retains its own defensive YAML accessor.
func (m Manifest) Configurations() []PluginConfiguration {
	return append([]PluginConfiguration(nil), m.configurations...)
}

// Configuration returns one exact Plugin ID's runtime configuration.
func (m Manifest) Configuration(pluginID string) (PluginConfiguration, bool) {
	index := sort.Search(len(m.configurations), func(index int) bool {
		return m.configurations[index].pluginID >= pluginID
	})
	if index >= len(m.configurations) || m.configurations[index].pluginID != pluginID {
		return PluginConfiguration{}, false
	}
	return m.configurations[index], true
}

// Parse reads root plystra.yaml and normalizes canonical provider inputs, HTTP
// exposure, and concise or expanded capabilities.aliases declarations.
func Parse(data []byte) (Manifest, error) {
	return ParseSource("plystra.yaml", data)
}

// ParseSource reads one strict bounded current-project document and retains
// its stable Project-relative source name for diagnostics.
func ParseSource(source string, data []byte) (Manifest, error) {
	return parseSource(source, data, false)
}

// ParseOverlaySource reads one sparse bounded environment-overlay document.
// Required effective fields may be omitted here and are validated after typed
// application over the complete root current-project document.
func ParseOverlaySource(source string, data []byte) (Manifest, error) {
	return parseSource(source, data, true)
}

func parseSource(source string, data []byte, sparseOverlay bool) (Manifest, error) {
	if source == "" || strings.TrimSpace(source) == "" || strings.IndexFunc(source, unicode.IsControl) >= 0 {
		return Manifest{}, invalid("configuration source must be non-empty and contain no control characters")
	}
	root, err := decodeDocument(data)
	if err != nil {
		return Manifest{}, err
	}
	values, err := mapping(root, "document")
	if err != nil {
		return Manifest{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "http", "timeouts", "capabilities", "config":
		default:
			return Manifest{}, invalid("unknown key %q", key)
		}
	}
	address, hasAddress, removeAddress, transports, cors, exposures, removedExposures, err := parseHTTP(values["http"], sparseOverlay)
	if err != nil {
		return Manifest{}, err
	}
	startupTimeout, hasStartupTimeout, removeStartupTimeout, err := parseTimeouts(values["timeouts"])
	if err != nil {
		return Manifest{}, err
	}
	requirements, removedRequirements, choices, removedChoices, aliases, removedAliases, err := parseCapabilities(values["capabilities"])
	if err != nil {
		return Manifest{}, err
	}
	configurations, removedConfigurations, err := parseConfigurations(values["config"])
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		httpAddress:            address,
		hasHTTPAddress:         hasAddress,
		removeHTTPAddress:      removeAddress,
		httpTransports:         transports,
		httpCORS:               cors,
		httpExposures:          exposures,
		removedHTTPExposures:   removedExposures,
		requirements:           requirements,
		removedRequirements:    removedRequirements,
		providerChoices:        choices,
		removedProviderChoices: removedChoices,
		aliases:                aliases,
		removedAliases:         removedAliases,
		configurations:         configurations,
		removedConfigurations:  removedConfigurations,
		startupTimeout:         startupTimeout,
		hasStartupTimeout:      hasStartupTimeout,
		removeStartupTimeout:   removeStartupTimeout,
	}
	rewriteManifestSource(&manifest, source)
	return manifest, nil
}

func parseTimeouts(node *yaml.Node) (time.Duration, bool, bool, error) {
	if node == nil {
		return DefaultStartupTimeout, false, false, nil
	}
	values, err := mapping(node, "timeouts")
	if err != nil {
		return 0, false, false, err
	}
	for _, key := range sortedNodeKeys(values) {
		if key != "startup" {
			return 0, false, false, invalid("timeouts contains unknown key %q", key)
		}
	}
	startupNode, exists := values["startup"]
	if !exists {
		return DefaultStartupTimeout, false, false, nil
	}
	if isNull(startupNode) {
		return DefaultStartupTimeout, false, true, nil
	}
	startup, err := strictString(startupNode)
	if err != nil || startup == "" || len(startup) > 64 || strings.TrimSpace(startup) != startup || strings.ContainsRune(startup, '\x00') {
		return 0, false, false, invalid("timeouts.startup must be a non-empty trimmed Go duration string of at most 64 bytes with no NUL or null")
	}
	duration, err := time.ParseDuration(startup)
	if err != nil || duration <= 0 {
		return 0, false, false, invalid("timeouts.startup must be a positive Go duration")
	}
	return duration, true, false, nil
}

func rewriteManifestSource(manifest *Manifest, source string) {
	rewrite := func(value string) string {
		if value == "plystra.yaml" {
			return source
		}
		return source + strings.TrimPrefix(value, "plystra.yaml")
	}
	for index := range manifest.httpExposures {
		manifest.httpExposures[index].source = rewrite(manifest.httpExposures[index].source)
	}
	for index := range manifest.removedHTTPExposures {
		manifest.removedHTTPExposures[index].source = rewrite(manifest.removedHTTPExposures[index].source)
	}
	for index := range manifest.requirements {
		manifest.requirements[index].source = rewrite(manifest.requirements[index].source)
	}
	for index := range manifest.removedRequirements {
		manifest.removedRequirements[index].source = rewrite(manifest.removedRequirements[index].source)
	}
	for index := range manifest.providerChoices {
		manifest.providerChoices[index].source = rewrite(manifest.providerChoices[index].source)
	}
	for index := range manifest.removedProviderChoices {
		manifest.removedProviderChoices[index].source = rewrite(manifest.removedProviderChoices[index].source)
	}
	for index := range manifest.aliases {
		manifest.aliases[index].source = rewrite(manifest.aliases[index].source)
	}
	for index := range manifest.removedAliases {
		manifest.removedAliases[index].source = rewrite(manifest.removedAliases[index].source)
	}
	for index := range manifest.configurations {
		manifest.configurations[index].source = rewrite(manifest.configurations[index].source)
	}
	for index := range manifest.removedConfigurations {
		manifest.removedConfigurations[index].source = rewrite(manifest.removedConfigurations[index].source)
	}
}

func parseConfigurations(node *yaml.Node) ([]PluginConfiguration, []pluginConfigurationRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	values, err := mapping(node, "config")
	if err != nil {
		return nil, nil, err
	}
	configurations := make([]PluginConfiguration, 0, len(values))
	removals := make([]pluginConfigurationRemoval, 0, len(values))
	for _, pluginID := range sortedNodeKeys(values) {
		if err := pluginid.Validate(pluginID); err != nil {
			return nil, nil, invalid("config key %q is not a canonical Plugin ID", pluginID)
		}
		source := fmt.Sprintf("plystra.yaml config[%q]", pluginID)
		if isNull(values[pluginID]) {
			removals = append(removals, pluginConfigurationRemoval{pluginID: pluginID, source: source})
			continue
		}
		if values[pluginID].Kind != yaml.MappingNode {
			return nil, nil, invalid("config[%q] must be a mapping or null", pluginID)
		}
		data, err := yaml.Marshal(values[pluginID])
		if err != nil {
			return nil, nil, invalid("config[%q] cannot be normalized", pluginID)
		}
		configurations = append(configurations, PluginConfiguration{
			pluginID: pluginID,
			source:   source,
			yaml:     append([]byte(nil), data...),
		})
	}
	return configurations, removals, nil
}

func parseHTTP(node *yaml.Node, sparseOverlay bool) (string, bool, bool, httpTransportLayer, httpCORSLayer, []HTTPExposure, []capabilityRemoval, error) {
	if node == nil {
		return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, nil
	}
	values, err := mapping(node, "http")
	if err != nil {
		return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "address", "transports", "cors", "expose":
		default:
			return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, invalid("http contains unknown key %q", key)
		}
	}
	address := ""
	hasAddress := false
	removeAddress := false
	if addressNode, exists := values["address"]; exists {
		if isNull(addressNode) {
			removeAddress = true
		} else {
			address, err = strictString(addressNode)
			if err != nil || address == "" || len(address) > 4096 || strings.TrimSpace(address) != address || strings.ContainsRune(address, '\x00') {
				return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, invalid("http.address must be a non-empty trimmed string of at most 4096 bytes with no NUL or null")
			}
			hasAddress = true
		}
	}
	transports, err := parseHTTPTransports(values["transports"])
	if err != nil {
		return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, err
	}
	cors, err := parseHTTPCORS(values["cors"], sparseOverlay)
	if err != nil {
		return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, err
	}
	exposeNode, exists := values["expose"]
	if !exists {
		return address, hasAddress, removeAddress, transports, cors, nil, nil, nil
	}
	exposures, removals, err := parseCapabilitySet(exposeNode, "http.expose", func(id capabilityid.Identifier, source string) HTTPExposure {
		return HTTPExposure{id: id, source: source}
	})
	if err != nil {
		return "", false, false, httpTransportLayer{}, httpCORSLayer{}, nil, nil, err
	}
	return address, hasAddress, removeAddress, transports, cors, exposures, removals, nil
}

func parseHTTPTransports(node *yaml.Node) (httpTransportLayer, error) {
	if node == nil {
		return httpTransportLayer{}, nil
	}
	values, err := mapping(node, "http.transports")
	if err != nil {
		return httpTransportLayer{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "connect", "rest":
		default:
			return httpTransportLayer{}, invalid("http.transports contains unknown key %q", key)
		}
	}
	result := httpTransportLayer{}
	if connect, exists := values["connect"]; exists {
		if isNull(connect) {
			result.removeConnect = true
		} else {
			result.connect, err = strictBool(connect)
			if err != nil {
				return httpTransportLayer{}, invalid("http.transports.connect must be true, false, or null")
			}
			result.hasConnect = true
		}
	}
	if rest, exists := values["rest"]; exists {
		if isNull(rest) {
			result.removeREST = true
		} else {
			result.rest, err = strictBool(rest)
			if err != nil {
				return httpTransportLayer{}, invalid("http.transports.rest must be true, false, or null")
			}
			result.hasREST = true
		}
	}
	return result, nil
}

func parseHTTPCORS(node *yaml.Node, sparseOverlay bool) (httpCORSLayer, error) {
	if node == nil {
		return httpCORSLayer{}, nil
	}
	if isNull(node) {
		return httpCORSLayer{remove: true}, nil
	}
	values, err := mapping(node, "http.cors")
	if err != nil {
		return httpCORSLayer{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "allowed_origins", "allow_credentials":
		default:
			return httpCORSLayer{}, invalid("http.cors contains unknown key %q", key)
		}
	}
	originsNode, exists := values["allowed_origins"]
	if !exists && !sparseOverlay {
		return httpCORSLayer{}, invalid("http.cors.allowed_origins is required when http.cors is present")
	}
	var origins []string
	if exists {
		origins, err = parseCORSOrigins(originsNode)
		if err != nil {
			return httpCORSLayer{}, err
		}
	}
	result := httpCORSLayer{
		allowedOrigins:    origins,
		hasAllowedOrigins: exists,
		present:           true,
	}
	if credentialsNode, exists := values["allow_credentials"]; exists {
		if isNull(credentialsNode) {
			result.removeAllowCredentials = true
		} else {
			result.allowCredentials, err = strictBool(credentialsNode)
			if err != nil {
				return httpCORSLayer{}, invalid("http.cors.allow_credentials must be true, false, or null")
			}
			result.hasAllowCredentials = true
		}
	}
	if result.hasAllowedOrigins {
		if err := validateHTTPCORSLayer(result); err != nil {
			return httpCORSLayer{}, err
		}
	} else if !sparseOverlay {
		return httpCORSLayer{}, invalid("http.cors.allowed_origins is required when http.cors is present")
	}
	return result, nil
}

func parseCORSOrigins(node *yaml.Node) ([]string, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, invalid("http.cors.allowed_origins must be a nonempty sequence of origins")
	}
	if len(node.Content) == 0 {
		return nil, invalid("http.cors.allowed_origins must be a nonempty sequence of origins")
	}
	origins := make([]string, len(node.Content))
	for index, item := range node.Content {
		raw, err := strictString(item)
		if err != nil {
			return nil, invalid("http.cors.allowed_origins[%d] must be an origin string", index)
		}
		origins[index] = raw
	}
	return normalizeHTTPCORSOrigins(origins)
}

func normalizeHTTPCORSOrigins(origins []string) ([]string, error) {
	if len(origins) == 0 {
		return nil, invalid("http.cors.allowed_origins must be a nonempty sequence of origins")
	}
	set := make(map[string]struct{}, len(origins))
	for index, raw := range origins {
		origin, err := normalizeCORSOrigin(raw)
		if err != nil {
			return nil, invalid("http.cors.allowed_origins[%d] %v", index, err)
		}
		set[origin] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for origin := range set {
		result = append(result, origin)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeCORSOrigin(raw string) (string, error) {
	if raw == "*" {
		return raw, nil
	}
	if raw == "" || len(raw) > 4096 || strings.TrimSpace(raw) != raw || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errors.New("must be a nonempty trimmed origin of at most 4096 bytes with no control characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("must contain only an http or https scheme, host, and optional port")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("must use the http or https scheme")
	}
	host := strings.ToLower(parsed.Hostname())
	nonASCIIHost := strings.IndexFunc(host, func(value rune) bool { return value > unicode.MaxASCII }) >= 0
	if host == "" || strings.IndexFunc(host, unicode.IsControl) >= 0 || nonASCIIHost || strings.ContainsAny(host, " /\\%") {
		return "", errors.New("must contain a valid ASCII host")
	}
	if parsedIP := net.ParseIP(host); parsedIP != nil {
		host = parsedIP.String()
	}
	port := parsed.Port()
	if strings.HasSuffix(parsed.Host, ":") {
		return "", errors.New("must contain a valid optional port")
	}
	if port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return "", errors.New("must contain an optional port from 1 through 65535")
		}
		if (scheme == "http" && value == 80) || (scheme == "https" && value == 443) {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + host, nil
}

func validateHTTPCORSLayer(cors httpCORSLayer) error {
	if !cors.present || cors.remove {
		return nil
	}
	if !cors.hasAllowedOrigins || len(cors.allowedOrigins) == 0 {
		return invalid("http.cors.allowed_origins is required when http.cors is present")
	}
	if cors.allowCredentials && slices.Contains(cors.allowedOrigins, "*") {
		return invalid("http.cors cannot combine wildcard origin %q with allow_credentials: true", "*")
	}
	return nil
}

func cloneHTTPCORSLayer(cors httpCORSLayer) httpCORSLayer {
	cors.allowedOrigins = append([]string(nil), cors.allowedOrigins...)
	return cors
}

func equalHTTPCORSLayers(left, right httpCORSLayer) bool {
	return left.hasAllowedOrigins == right.hasAllowedOrigins &&
		left.allowCredentials == right.allowCredentials &&
		left.hasAllowCredentials == right.hasAllowCredentials &&
		left.removeAllowCredentials == right.removeAllowCredentials &&
		left.present == right.present &&
		left.remove == right.remove &&
		slices.Equal(left.allowedOrigins, right.allowedOrigins)
}

func parseCapabilitySet[T any](node *yaml.Node, path string, makeValue func(capabilityid.Identifier, string) T) ([]T, []capabilityRemoval, error) {
	var addNode, removeNode *yaml.Node
	addPath := path
	removePath := path + ".remove"
	switch node.Kind {
	case yaml.SequenceNode:
		addNode = node
	case yaml.MappingNode:
		values, err := mapping(node, path)
		if err != nil {
			return nil, nil, err
		}
		for _, key := range sortedNodeKeys(values) {
			switch key {
			case "add", "remove":
			default:
				return nil, nil, invalid("%s contains unknown sparse-edit key %q", path, key)
			}
		}
		addNode = values["add"]
		removeNode = values["remove"]
		addPath = path + ".add"
	default:
		return nil, nil, invalid("%s must be a sequence or sparse {add, remove} mapping of canonical Capability IDs", path)
	}

	adds, err := parseCapabilityIDs(addNode, addPath)
	if err != nil {
		return nil, nil, err
	}
	removes, err := parseCapabilityIDs(removeNode, removePath)
	if err != nil {
		return nil, nil, err
	}
	addSet := make(map[capabilityid.Identifier]struct{}, len(adds))
	for _, id := range adds {
		addSet[id] = struct{}{}
	}
	for _, id := range removes {
		if _, ambiguous := addSet[id]; ambiguous {
			return nil, nil, invalid("%s cannot both add and remove Capability %q", path, id.String())
		}
	}

	values := make([]T, len(adds))
	for index, id := range adds {
		values[index] = makeValue(id, fmt.Sprintf("plystra.yaml %s[%q]", addPath, id.String()))
	}
	removals := make([]capabilityRemoval, len(removes))
	for index, id := range removes {
		removals[index] = capabilityRemoval{id: id, source: fmt.Sprintf("plystra.yaml %s[%q]", removePath, id.String())}
	}
	return values, removals, nil
}

func parseCapabilityIDs(node *yaml.Node, path string) ([]capabilityid.Identifier, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalid("%s must be a sequence of canonical Capability IDs", path)
	}
	values := make([]capabilityid.Identifier, 0, len(node.Content))
	seen := make(map[capabilityid.Identifier]int, len(node.Content))
	for index, item := range node.Content {
		value, err := strictString(item)
		if err != nil || value == "" {
			return nil, invalid("%s[%d] must be a canonical Capability ID string", path, index)
		}
		id, err := capabilityid.Parse(value)
		if err != nil {
			return nil, invalid("%s[%d] %q is not a canonical Capability ID", path, index, value)
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, invalid("%s[%d] duplicates Capability %q from %s[%d]", path, index, id.String(), path, previous)
		}
		seen[id] = index
		values = append(values, id)
	}
	sort.Slice(values, func(left, right int) bool { return values[left].String() < values[right].String() })
	return values, nil
}

func parseCapabilities(node *yaml.Node) ([]CapabilityRequirement, []capabilityRemoval, []ProviderChoice, []capabilityRemoval, []Alias, []capabilityRemoval, error) {
	if node == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}
	values, err := mapping(node, "capabilities")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "require", "use", "aliases":
		default:
			return nil, nil, nil, nil, nil, nil, invalid("capabilities contains unknown key %q", key)
		}
	}
	requirements, removedRequirements, err := parseRequirements(values["require"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	choices, removedChoices, err := parseProviderChoices(values["use"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	aliases, removedAliases, err := parseAliases(values["aliases"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if err := rejectAliasResolutionInputs(requirements, choices, aliases); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	return requirements, removedRequirements, choices, removedChoices, aliases, removedAliases, nil
}

func parseRequirements(node *yaml.Node) ([]CapabilityRequirement, []capabilityRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	return parseCapabilitySet(node, "capabilities.require", func(id capabilityid.Identifier, source string) CapabilityRequirement {
		return CapabilityRequirement{id: id, source: source}
	})
}

func parseProviderChoices(node *yaml.Node) ([]ProviderChoice, []capabilityRemoval, error) {
	if node == nil {
		return nil, nil, nil
	}
	values, err := mapping(node, "capabilities.use")
	if err != nil {
		return nil, nil, err
	}
	choices := make([]ProviderChoice, 0, len(values))
	removals := make([]capabilityRemoval, 0, len(values))
	for _, value := range sortedNodeKeys(values) {
		capability, err := capabilityid.Parse(value)
		if err != nil {
			return nil, nil, invalid("capabilities.use key %q is not a canonical Capability ID", value)
		}
		if strings.HasPrefix(capability.Name(), "kernel.") {
			return nil, nil, invalid("capabilities.use key %q selects an intrinsic kernel.* Capability", value)
		}
		source := fmt.Sprintf("plystra.yaml capabilities.use[%q]", capability.String())
		if isNull(values[value]) {
			removals = append(removals, capabilityRemoval{id: capability, source: source})
			continue
		}
		selected, err := strictString(values[value])
		if err != nil || pluginid.Validate(selected) != nil {
			return nil, nil, invalid("capabilities.use[%q] must be a canonical Plugin ID string or null", value)
		}
		choices = append(choices, ProviderChoice{
			capability: capability,
			pluginID:   selected,
			source:     source,
		})
	}
	return choices, removals, nil
}

func parseAliases(aliasesNode *yaml.Node) ([]Alias, []capabilityRemoval, error) {
	if aliasesNode == nil {
		return nil, nil, nil
	}
	aliasValues, err := mapping(aliasesNode, "capabilities.aliases")
	if err != nil {
		return nil, nil, err
	}
	aliases := make([]Alias, 0, len(aliasValues))
	removals := make([]capabilityRemoval, 0, len(aliasValues))
	for _, aliasValue := range sortedNodeKeys(aliasValues) {
		id, err := capabilityid.Parse(aliasValue)
		if err != nil {
			return nil, nil, invalid("capabilities.aliases key %q is not a canonical Capability ID", aliasValue)
		}
		if strings.HasPrefix(id.Name(), "kernel.") {
			return nil, nil, invalid("capabilities.aliases key %q uses the reserved kernel.* canonical namespace", aliasValue)
		}
		path := fmt.Sprintf("capabilities.aliases[%q]", aliasValue)
		if isNull(aliasValues[aliasValue]) {
			removals = append(removals, capabilityRemoval{id: id, source: "plystra.yaml " + path})
			continue
		}
		alias, err := parseAlias(path, id, aliasValues[aliasValue])
		if err != nil {
			return nil, nil, err
		}
		aliases = append(aliases, alias)
	}
	if err := rejectAliasChains(aliases); err != nil {
		return nil, nil, err
	}
	return aliases, removals, nil
}

func rejectAliasResolutionInputs(requirements []CapabilityRequirement, choices []ProviderChoice, aliases []Alias) error {
	aliasIDs := make(map[capabilityid.Identifier]struct{}, len(aliases))
	for _, alias := range aliases {
		aliasIDs[alias.id] = struct{}{}
	}
	for _, requirement := range requirements {
		if _, isAlias := aliasIDs[requirement.id]; isAlias {
			return invalid("capabilities.require contains application-local Alias %q; requirements must name canonical Capabilities", requirement.id.String())
		}
	}
	for _, choice := range choices {
		if _, isAlias := aliasIDs[choice.capability]; isAlias {
			return invalid("capabilities.use contains application-local Alias %q; provider choices must name canonical Capabilities", choice.capability.String())
		}
	}
	return nil
}

func parseAlias(path string, id capabilityid.Identifier, node *yaml.Node) (Alias, error) {
	alias := Alias{id: id, source: "plystra.yaml " + path}
	var targetValue string
	var err error
	switch {
	case node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str":
		targetValue = node.Value
	case node != nil && node.Kind == yaml.MappingNode:
		values, mappingErr := mapping(node, path)
		if mappingErr != nil {
			return Alias{}, mappingErr
		}
		for _, key := range sortedNodeKeys(values) {
			switch key {
			case "target", "expose", "deprecated":
			default:
				return Alias{}, invalid("%s contains unknown key %q", path, key)
			}
		}
		targetNode, exists := values["target"]
		if !exists {
			return Alias{}, invalid("%s.target is required", path)
		}
		targetValue, err = strictString(targetNode)
		if err != nil || targetValue == "" {
			return Alias{}, invalid("%s.target must be a non-empty string", path)
		}
		if exposeNode, exists := values["expose"]; exists {
			alias.exposure, err = parseExposure(path+".expose", exposeNode)
			if err != nil {
				return Alias{}, err
			}
			alias.hasExposure = true
		}
		if deprecatedNode, exists := values["deprecated"]; exists {
			alias.deprecated, err = parseDeprecation(path+".deprecated", deprecatedNode)
			if err != nil {
				return Alias{}, err
			}
		}
	default:
		return Alias{}, invalid("%s must be a canonical target string or expanded mapping", path)
	}

	alias.target, err = capabilityid.Parse(targetValue)
	if err != nil {
		return Alias{}, invalid("%s target %q is not a canonical Capability ID", path, targetValue)
	}
	if alias.id.Major() != alias.target.Major() {
		return Alias{}, invalid("%s Alias %q and target %q must use the same major version", path, alias.id.String(), alias.target.String())
	}
	if alias.id == alias.target {
		return Alias{}, invalid("%s Alias %q cannot target itself", path, alias.id.String())
	}
	return alias, nil
}

func parseExposure(path string, node *yaml.Node) (generation.Exposure, error) {
	values, err := mapping(node, path)
	if err != nil {
		return generation.Exposure{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "go", "http", "javascript":
		default:
			return generation.Exposure{}, invalid("%s contains unknown key %q", path, key)
		}
	}
	result := generation.Exposure{}
	fields := []struct {
		name  string
		value *bool
	}{
		{name: "go", value: &result.Go},
		{name: "http", value: &result.HTTP},
		{name: "javascript", value: &result.JavaScript},
	}
	for _, field := range fields {
		node, exists := values[field.name]
		if !exists {
			return generation.Exposure{}, invalid("%s.%s is required when expose is present", path, field.name)
		}
		*field.value, err = strictBool(node)
		if err != nil {
			return generation.Exposure{}, invalid("%s.%s must be true or false", path, field.name)
		}
	}
	return result, nil
}

func parseDeprecation(path string, node *yaml.Node) (string, error) {
	values, err := mapping(node, path)
	if err != nil {
		return "", err
	}
	for _, key := range sortedNodeKeys(values) {
		if key != "message" {
			return "", invalid("%s contains unknown key %q", path, key)
		}
	}
	messageNode, exists := values["message"]
	if !exists {
		return "", invalid("%s.message is required", path)
	}
	message, err := strictString(messageNode)
	if err != nil || message == "" || len(message) > 1024 || strings.ContainsRune(message, '\x00') {
		return "", invalid("%s.message must be a non-empty string of at most 1024 bytes with no NUL", path)
	}
	return message, nil
}

func rejectAliasChains(aliases []Alias) error {
	byID := make(map[capabilityid.Identifier]Alias, len(aliases))
	for _, alias := range aliases {
		byID[alias.id] = alias
	}
	for _, start := range aliases {
		path := []capabilityid.Identifier{start.id}
		seen := map[capabilityid.Identifier]struct{}{start.id: {}}
		current := start
		for {
			path = append(path, current.target)
			next, isAlias := byID[current.target]
			if !isAlias {
				break
			}
			if _, cycle := seen[next.id]; cycle {
				return invalid("capabilities.aliases contains forbidden Alias cycle %s; every Alias must directly target a canonical Capability", renderAliasPath(path))
			}
			seen[next.id] = struct{}{}
			current = next
		}
		if len(path) > 2 {
			return invalid("capabilities.aliases contains forbidden Alias chain %s; every Alias must directly target a canonical Capability", renderAliasPath(path))
		}
	}
	return nil
}

func renderAliasPath(values []capabilityid.Identifier) string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return strings.Join(result, " -> ")
}

func decodeDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return nil, invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, invalid("multiple YAML documents are not allowed")
		}
		return nil, invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func rejectReferences(root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return invalid("YAML anchors and aliases are not allowed")
		}
		stack = append(stack, node.Content...)
	}
	return nil
}

func mapping(node *yaml.Node, path string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, invalid("%s must be a mapping", path)
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictString(node.Content[index])
		if err != nil {
			return nil, invalid("%s contains a non-string key", path)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, invalid("%s contains duplicate key %q", path, key)
		}
		result[key] = node.Content[index+1]
	}
	return result, nil
}

func strictString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", errors.New("must be a string")
	}
	return node.Value, nil
}

func strictBool(node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, errors.New("must be a boolean")
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("must be true or false")
	}
}

func isNull(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func sortedNodeKeys(values map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, arguments...))
}
