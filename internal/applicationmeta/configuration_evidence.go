package applicationmeta

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ConfigurationDecisionSummary is a redacted description of one typed
// configuration decision. It intentionally never contains the configured
// value or a Secret reference target.
type ConfigurationDecisionSummary string

const (
	ConfigurationSummaryRemoval        ConfigurationDecisionSummary = "removal"
	ConfigurationSummaryObject         ConfigurationDecisionSummary = "object"
	ConfigurationSummaryCapability     ConfigurationDecisionSummary = "capability"
	ConfigurationSummaryProvider       ConfigurationDecisionSummary = "provider"
	ConfigurationSummaryInterface      ConfigurationDecisionSummary = "interface"
	ConfigurationSummaryImplementation ConfigurationDecisionSummary = "implementation"
	ConfigurationSummaryAlias          ConfigurationDecisionSummary = "alias"
	ConfigurationSummaryString         ConfigurationDecisionSummary = "string"
	ConfigurationSummaryBoolean        ConfigurationDecisionSummary = "boolean"
	ConfigurationSummaryDuration       ConfigurationDecisionSummary = "duration"
	ConfigurationSummaryArray          ConfigurationDecisionSummary = "array"
	ConfigurationSummarySecret         ConfigurationDecisionSummary = "secret-reference"
	ConfigurationSummaryValue          ConfigurationDecisionSummary = "value"
)

// ConfigurationDecision is one typed, non-secret declaration from a single
// configuration document. Decisions are construction evidence; they are not
// a second configuration resolver.
type ConfigurationDecision struct {
	path                 string
	digest               string
	summary              ConfigurationDecisionSummary
	removed              bool
	source               string
	dependencyComposable bool
}

// Path returns the canonical schema path represented by the decision.
func (d ConfigurationDecision) Path() string { return d.path }

// Digest returns the normalized non-secret decision digest.
func (d ConfigurationDecision) Digest() string { return d.digest }

// Summary returns a bounded redacted type description.
func (d ConfigurationDecision) Summary() ConfigurationDecisionSummary { return d.summary }

// Removed reports whether this decision is an explicit typed tombstone.
func (d ConfigurationDecision) Removed() bool { return d.removed }

// Source returns the stable Project-relative configuration document path.
func (d ConfigurationDecision) Source() string { return d.source }

// DependencyComposable reports whether this field participates in dependency
// composition. Current-Project-owned process and public-surface settings
// deliberately return false.
func (d ConfigurationDecision) DependencyComposable() bool { return d.dependencyComposable }

// ConfigurationDecisions returns deterministic typed decisions for one parsed
// configuration layer. Values are represented only by a digest and a bounded
// summary; Secret values and Secret reference targets are never returned.
func ConfigurationDecisions(manifest Manifest, schemas SchemaLookup) ([]ConfigurationDecision, error) {
	if schemas == nil {
		return nil, fmt.Errorf("configuration decision schema lookup is nil")
	}
	maintenance, err := maintenanceDecisions(manifest, schemas)
	if err != nil {
		return nil, err
	}
	result := make([]ConfigurationDecision, 0, len(maintenance)+8)
	source := manifest.source
	if source == "" {
		source = "plystra.yaml"
	}
	for _, decision := range maintenance {
		summary := ConfigurationSummaryRemoval
		if !decision.removed {
			switch decision.field {
			case maintenanceRequirement:
				summary = ConfigurationSummaryCapability
			case maintenanceHTTPExposure, maintenanceInterfaceRequirement:
				summary = ConfigurationSummaryInterface
			case maintenanceProvider:
				summary = ConfigurationSummaryProvider
			case maintenanceImplementationChoice:
				summary = ConfigurationSummaryImplementation
			case maintenanceInterfacePolicy:
				summary = ConfigurationSummaryDuration
			case maintenanceAlias:
				summary = ConfigurationSummaryAlias
			case maintenanceConstructorConfig:
				summary = configurationDecisionSummary(decision.config)
			}
		}
		result = append(result, ConfigurationDecision{
			path:                 decision.path,
			digest:               decision.digest,
			summary:              summary,
			removed:              decision.removed,
			source:               source,
			dependencyComposable: decision.field != maintenanceHTTPExposure,
		})
	}
	result = append(result, processConfigurationDecisions(manifest)...)
	// maintenanceDecisions and the process decision builder are both typed and
	// deterministic, but sort again at this public boundary so future fields do
	// not accidentally inherit map ordering.
	for index := range result {
		if result[index].path == "" || result[index].digest == "" || result[index].summary == "" || result[index].source == "" {
			return nil, fmt.Errorf("configuration decision %q is incomplete", result[index].path)
		}
	}
	// A path must have at most one decision in one layer. This catches malformed
	// synthetic manifests before evidence construction.
	sortConfigurationDecisions(result)
	for index := 1; index < len(result); index++ {
		if result[index-1].path == result[index].path {
			return nil, fmt.Errorf("configuration path %s has duplicate decisions", result[index].path)
		}
	}
	return result, nil
}

// ConfigurationLayerDigest returns the canonical identity of one parsed
// configuration document after current-layer validation. YAML presentation,
// declaration order for schema-defined sets, equivalent typed scalar
// spellings, and the source filename do not enter the digest. Explicit
// removals and ordered values do. Constructor configuration without a
// discoverable valid schema is structurally normalized only so explicit-config
// mode can record its mandatory but excluded root document without granting
// that document current-project authority.
func ConfigurationLayerDigest(manifest Manifest, schemas SchemaLookup) (string, error) {
	decisions, err := ConfigurationDecisions(manifest, schemas)
	if err != nil {
		if !errors.Is(err, ErrConfigurationSchema) && !errors.Is(err, ErrConfigurationValues) {
			return "", err
		}
		decisions, err = configurationLayerDigestDecisions(manifest, schemas)
		if err != nil {
			return "", err
		}
	}
	values := make([]string, 1, 1+len(decisions)*5)
	values[0] = "plystra.configuration-layer/v1"
	for _, decision := range decisions {
		values = append(values,
			decision.path,
			decision.digest,
			string(decision.summary),
			strconv.FormatBool(decision.removed),
			strconv.FormatBool(decision.dependencyComposable),
		)
	}
	return digestStrings(values...), nil
}

func configurationLayerDigestDecisions(manifest Manifest, schemas SchemaLookup) ([]ConfigurationDecision, error) {
	withoutConstructorConfiguration := manifest
	withoutConstructorConfiguration.configurations = nil
	withoutConstructorConfiguration.removedConfigurations = nil
	result, err := ConfigurationDecisions(withoutConstructorConfiguration, schemas)
	if err != nil {
		return nil, err
	}
	for _, configured := range manifest.configurations {
		decisions, normalizeErr := normalizeConstructorConfigDecisions(configured, schemas)
		if normalizeErr == nil {
			for _, decision := range decisions {
				result = append(result, constructorConfigurationDecision(decision))
			}
			continue
		}
		digest, digestErr := untypedConstructorConfigurationDigest(configured)
		if digestErr != nil {
			return nil, digestErr
		}
		result = append(result, ConfigurationDecision{
			path:                 constructorConfigPath(configured.constructor, nil),
			digest:               digest,
			summary:              ConfigurationSummaryObject,
			source:               configured.source,
			dependencyComposable: true,
		})
	}
	for _, removal := range manifest.removedConfigurations {
		result = append(result, constructorConfigurationDecision(newConstructorConfigDecision(
			removal.constructor,
			nil,
			constructorConfigRemoval,
			"",
			nil,
			removal.source,
		)))
	}
	sortConfigurationDecisions(result)
	for index := 1; index < len(result); index++ {
		if result[index-1].path == result[index].path {
			return nil, fmt.Errorf("configuration path %s has duplicate decisions", result[index].path)
		}
	}
	return result, nil
}

func constructorConfigurationDecision(decision constructorConfigDecision) ConfigurationDecision {
	summary := configurationDecisionSummary(decision)
	removed := decision.kind == constructorConfigRemoval
	if removed {
		summary = ConfigurationSummaryRemoval
	}
	return ConfigurationDecision{
		path:                 constructorConfigPath(decision.constructor, decision.segments),
		digest:               decision.digest,
		summary:              summary,
		removed:              removed,
		source:               decision.source,
		dependencyComposable: true,
	}
}

func untypedConstructorConfigurationDigest(configured ConstructorConfiguration) (string, error) {
	root, err := decodeNormalizedConfigNode(configured.yaml)
	if err != nil {
		return "", fmt.Errorf("normalize excluded constructor configuration %q: %w", configured.constructor, err)
	}
	values, err := appendUntypedConfigurationTokens(
		[]string{"plystra.untyped-constructor-configuration/v1", configured.constructor.String()},
		root,
		&constructorConfigNormalizeState{},
		0,
	)
	if err != nil {
		return "", fmt.Errorf("normalize excluded constructor configuration %q: %w", configured.constructor, err)
	}
	return digestStrings(values...), nil
}

func appendUntypedConfigurationTokens(values []string, node *yaml.Node, state *constructorConfigNormalizeState, depth int) ([]string, error) {
	if err := enterConstructorConfigNode(node, state, depth); err != nil || node.Alias != nil || node.Anchor != "" {
		return nil, ErrConfigurationInvalidValue
	}
	switch node.Kind {
	case yaml.MappingNode:
		mapping, err := safeConstructorConfigMapping(node)
		if err != nil {
			return nil, err
		}
		keys := sortedNodeKeys(mapping)
		values = append(values, "mapping", strconv.Itoa(len(keys)))
		for _, key := range keys {
			values = append(values, "key", key)
			values, err = appendUntypedConfigurationTokens(values, mapping[key], state, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	case yaml.SequenceNode:
		values = append(values, "sequence", strconv.Itoa(len(node.Content)))
		var err error
		for _, child := range node.Content {
			values, err = appendUntypedConfigurationTokens(values, child, state, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return values, nil
	case yaml.ScalarNode:
		normalized, err := normalizedUntypedConfigurationScalar(node)
		if err != nil {
			return nil, err
		}
		return append(values, "scalar", node.Tag, normalized), nil
	default:
		return nil, ErrConfigurationInvalidValue
	}
}

func normalizedUntypedConfigurationScalar(node *yaml.Node) (string, error) {
	switch node.Tag {
	case "!!null":
		return "null", nil
	case "!!bool":
		var value bool
		if err := node.Decode(&value); err != nil {
			return "", ErrConfigurationInvalidValue
		}
		return strconv.FormatBool(value), nil
	case "!!int":
		var value any
		if err := node.Decode(&value); err != nil {
			return "", ErrConfigurationInvalidValue
		}
		return fmt.Sprint(value), nil
	case "!!float":
		var value float64
		if err := node.Decode(&value); err != nil {
			return "", ErrConfigurationInvalidValue
		}
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	case "!!timestamp":
		var value time.Time
		if err := node.Decode(&value); err != nil {
			return "", ErrConfigurationInvalidValue
		}
		return value.UTC().Format(time.RFC3339Nano), nil
	default:
		return node.Value, nil
	}
}

func configurationDecisionSummary(decision constructorConfigDecision) ConfigurationDecisionSummary {
	if decision.kind == constructorConfigObject {
		return ConfigurationSummaryObject
	}
	if decision.kind == constructorConfigRemoval {
		return ConfigurationSummaryRemoval
	}
	if strings.HasPrefix(decision.valueType, "secret:") {
		return ConfigurationSummarySecret
	}
	if strings.HasPrefix(decision.valueType, "list:") {
		return ConfigurationSummaryArray
	}
	switch {
	case strings.HasPrefix(decision.valueType, "string:"), strings.HasPrefix(decision.valueType, "url:"):
		return ConfigurationSummaryString
	case strings.HasPrefix(decision.valueType, "boolean:"):
		return ConfigurationSummaryBoolean
	case strings.HasPrefix(decision.valueType, "duration:"):
		return ConfigurationSummaryDuration
	default:
		// Compiled Go Config schemas may gain additional supported value kinds.
		// Keep an unknown future kind redacted rather than leaking a raw type
		// descriptor into diagnostics.
		return ConfigurationSummaryValue
	}
}

func processConfigurationDecisions(manifest Manifest) []ConfigurationDecision {
	result := make([]ConfigurationDecision, 0, 8)
	source := manifest.source
	if source == "" {
		source = "plystra.yaml"
	}
	add := func(path, digest string, summary ConfigurationDecisionSummary, removed bool) {
		if removed {
			summary = ConfigurationSummaryRemoval
		}
		result = append(result, ConfigurationDecision{
			path:                 path,
			digest:               digest,
			summary:              summary,
			removed:              removed,
			source:               source,
			dependencyComposable: false,
		})
	}
	if manifest.hasHTTPAddress || manifest.removeHTTPAddress {
		digest := digestStrings("http.address", "removed")
		if manifest.hasHTTPAddress {
			digest = digestStrings("http.address", manifest.httpAddress)
		}
		add("http.address", digest, ConfigurationSummaryString, manifest.removeHTTPAddress)
	}
	if manifest.httpTransports.hasConnect || manifest.httpTransports.removeConnect {
		digest := digestStrings("http.transports.connect", "removed")
		if manifest.httpTransports.hasConnect {
			digest = digestStrings("http.transports.connect", strconv.FormatBool(manifest.httpTransports.connect))
		}
		add("http.transports.connect", digest, ConfigurationSummaryBoolean, manifest.httpTransports.removeConnect)
	}
	if manifest.httpTransports.hasREST || manifest.httpTransports.removeREST {
		digest := digestStrings("http.transports.rest", "removed")
		if manifest.httpTransports.hasREST {
			digest = digestStrings("http.transports.rest", strconv.FormatBool(manifest.httpTransports.rest))
		}
		add("http.transports.rest", digest, ConfigurationSummaryBoolean, manifest.httpTransports.removeREST)
	}
	if manifest.httpCORS.present || manifest.httpCORS.remove {
		if manifest.httpCORS.remove {
			add("http.cors", digestStrings("http.cors", "removed"), ConfigurationSummaryRemoval, true)
		} else {
			add("http.cors", digestStrings("http.cors", "object"), ConfigurationSummaryObject, false)
			if manifest.httpCORS.hasAllowedOrigins {
				origins := append([]string(nil), manifest.httpCORS.allowedOrigins...)
				add("http.cors.allowed_origins", digestStrings("http.cors.allowed_origins", strings.Join(origins, "\x00")), ConfigurationSummaryArray, false)
			}
			if manifest.httpCORS.hasAllowCredentials || manifest.httpCORS.removeAllowCredentials {
				digest := digestStrings("http.cors.allow_credentials", "removed")
				if manifest.httpCORS.hasAllowCredentials {
					digest = digestStrings("http.cors.allow_credentials", strconv.FormatBool(manifest.httpCORS.allowCredentials))
				}
				add("http.cors.allow_credentials", digest, ConfigurationSummaryBoolean, manifest.httpCORS.removeAllowCredentials)
			}
		}
	}
	if manifest.hasStartupTimeout || manifest.removeStartupTimeout {
		digest := digestStrings("timeouts.startup", "removed")
		if manifest.hasStartupTimeout {
			digest = digestStrings("timeouts.startup", manifest.startupTimeout.String())
		}
		add("timeouts.startup", digest, ConfigurationSummaryDuration, manifest.removeStartupTimeout)
	}
	return result
}

func sortConfigurationDecisions(values []ConfigurationDecision) {
	sort.Slice(values, func(left, right int) bool { return values[left].path < values[right].path })
}
