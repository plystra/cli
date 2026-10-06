package applicationmeta

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	ConfigurationSummaryCompleteSet    ConfigurationDecisionSummary = "complete-set"
	ConfigurationSummarySecret         ConfigurationDecisionSummary = "secret-reference"
	ConfigurationSummaryValue          ConfigurationDecisionSummary = "value"
)

// ConfigurationDecision is one typed, non-secret declaration from a single
// configuration document. Decisions are construction evidence; they are not
// a second configuration resolver.
type ConfigurationDecision struct {
	path               string
	digest             string
	summary            ConfigurationDecisionSummary
	removed            bool
	source             string
	resolutionRelevant bool
}

// Path returns the canonical schema path represented by the decision.
func (d ConfigurationDecision) Path() string { return d.path }

// Digest returns the normalized public decision digest. Runtime-only contents
// and Secret reference kinds and targets never contribute to this identity.
func (d ConfigurationDecision) Digest() string { return d.digest }

// Summary returns a bounded redacted type description.
func (d ConfigurationDecision) Summary() ConfigurationDecisionSummary { return d.summary }

// Removed reports whether this decision is an explicit typed tombstone.
func (d ConfigurationDecision) Removed() bool { return d.removed }

// Source returns the stable Project-relative configuration document path.
func (d ConfigurationDecision) Source() string { return d.source }

// ResolutionRelevant reports whether this decision contributes to the
// application-resolution evidence. Runtime-only process settings return false.
func (d ConfigurationDecision) ResolutionRelevant() bool { return d.resolutionRelevant }

// ConfigurationDecisions returns deterministic typed decisions for one parsed
// configuration layer. Values are represented only by a digest and a bounded
// summary; runtime-only contents and Secret targets are excluded.
func ConfigurationDecisions(manifest Manifest, schemas SchemaLookup) ([]ConfigurationDecision, error) {
	if schemas == nil {
		return nil, fmt.Errorf("configuration decision schema lookup is nil")
	}
	declarations, err := configurationDecisions(manifest, schemas)
	if err != nil {
		return nil, err
	}
	result := make([]ConfigurationDecision, 0, len(declarations)+9)
	source := manifest.source
	if source == "" {
		source = "plystra.yaml"
	}
	if manifest.completeInterfaceRequirements {
		result = append(result, ConfigurationDecision{
			path:               "interfaces.require",
			digest:             digestStrings("interfaces.require", "complete-set"),
			summary:            ConfigurationSummaryCompleteSet,
			source:             source,
			resolutionRelevant: true,
		})
	}
	for _, decision := range declarations {
		digest := decision.digest
		if decision.field == configurationConstructorConfig {
			digest = constructorConfigPublicDigest(decision.config)
		}
		summary := ConfigurationSummaryRemoval
		if !decision.removed {
			switch decision.field {
			case configurationRequirement:
				summary = ConfigurationSummaryCapability
			case configurationHTTPExposure, configurationInterfaceRequirement:
				summary = ConfigurationSummaryInterface
			case configurationProvider:
				summary = ConfigurationSummaryProvider
			case configurationImplementationChoice:
				summary = ConfigurationSummaryImplementation
			case configurationInterfacePolicy:
				summary = ConfigurationSummaryObject
			case configurationAlias:
				summary = ConfigurationSummaryAlias
			case configurationConstructorConfig:
				summary = configurationDecisionSummary(decision.config)
			}
		}
		result = append(result, ConfigurationDecision{
			path:               decision.path,
			digest:             digest,
			summary:            summary,
			removed:            decision.removed,
			source:             source,
			resolutionRelevant: true,
		})
	}
	result = append(result, processConfigurationDecisions(manifest)...)
	resources, err := resourceConfigurationDecisions(manifest, schemas, false)
	if err != nil {
		return nil, err
	}
	result = append(result, resources...)
	// configurationDecisions and the process decision builder are both typed and
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
// removals and build-visible typed projections do. Unvalidated constructor
// objects in excluded documents contribute only their constructor and an
// opaque object marker. Without typed validation no field, value, or reference
// target is known to be safe for public identity. This fallback grants no
// current-project authority and does not replace selected-model validation.
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
			strconv.FormatBool(decision.resolutionRelevant),
		)
	}
	return digestStrings(values...), nil
}

func configurationLayerDigestDecisions(manifest Manifest, schemas SchemaLookup) ([]ConfigurationDecision, error) {
	withoutConstructorConfiguration := manifest
	withoutConstructorConfiguration.configurations = nil
	withoutConstructorConfiguration.removedConfigurations = nil
	withoutConstructorConfiguration.resourceInstances = nil
	withoutConstructorConfiguration.removedResourceInstances = nil
	withoutConstructorConfiguration.resourceBindings = nil
	withoutConstructorConfiguration.removedResourceBindings = nil
	result, err := ConfigurationDecisions(withoutConstructorConfiguration, schemas)
	if err != nil {
		return nil, err
	}
	for _, configured := range manifest.configurations {
		decisions, normalizeErr := normalizeConstructorConfigDecisions(configured, schemas, ConfigurationNamespaceImplementation)
		if normalizeErr == nil {
			for _, decision := range decisions {
				result = append(result, constructorConfigurationDecision(decision))
			}
			continue
		}
		root, err := decodeNormalizedConfigNode(configured.yaml)
		if err != nil || validateUntypedConfigurationNode(root, &constructorConfigNormalizeState{}, 0) != nil {
			return nil, fmt.Errorf("normalize excluded constructor configuration %q: %w", configured.constructor, ErrConfigurationInvalidValue)
		}
		result = append(result, ConfigurationDecision{
			path:               constructorConfigPath(configured.constructor, nil),
			digest:             digestStrings("plystra.unvalidated-constructor-configuration/v2", configured.constructor.String()),
			summary:            ConfigurationSummaryObject,
			source:             configured.source,
			resolutionRelevant: true,
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
	resources, err := resourceConfigurationDecisions(manifest, schemas, true)
	if err != nil {
		return nil, err
	}
	result = append(result, resources...)
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
		path:               constructorConfigPath(decision.constructor, decision.segments),
		digest:             constructorConfigPublicDigest(decision),
		summary:            summary,
		removed:            removed,
		source:             decision.source,
		resolutionRelevant: true,
	}
}

func validateUntypedConfigurationNode(node *yaml.Node, state *constructorConfigNormalizeState, depth int) error {
	if err := enterConstructorConfigNode(node, state, depth); err != nil || node.Alias != nil || node.Anchor != "" {
		return ErrConfigurationInvalidValue
	}
	start, step := 0, 1
	switch node.Kind {
	case yaml.MappingNode:
		if _, err := safeConstructorConfigMapping(node); err != nil {
			return ErrConfigurationInvalidValue
		}
		start, step = 1, 2
	case yaml.SequenceNode:
	case yaml.ScalarNode:
		var value any
		if err := node.Decode(&value); err != nil {
			return ErrConfigurationInvalidValue
		}
		return nil
	default:
		return ErrConfigurationInvalidValue
	}
	for index := start; index < len(node.Content); index += step {
		if err := validateUntypedConfigurationNode(node.Content[index], state, depth+1); err != nil {
			return err
		}
	}
	return nil
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
			path:               path,
			digest:             digest,
			summary:            summary,
			removed:            removed,
			source:             source,
			resolutionRelevant: strings.HasPrefix(path, "http.cors"),
		})
	}
	if manifest.hasHTTPAddress || manifest.removeHTTPAddress {
		digest := digestStrings("http.address", "removed")
		if manifest.hasHTTPAddress {
			digest = digestStrings("process.runtime-value/v1", "http.address", "string")
		}
		add("http.address", digest, ConfigurationSummaryString, manifest.removeHTTPAddress)
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
			if manifest.httpCORS.removeAllowedOrigins {
				add("http.cors.allowed_origins", digestStrings("http.cors.allowed_origins", "removed"), ConfigurationSummaryRemoval, true)
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
			digest = digestStrings("process.runtime-value/v1", "timeouts.startup", "duration")
		}
		add("timeouts.startup", digest, ConfigurationSummaryDuration, manifest.removeStartupTimeout)
	}
	return result
}

func sortConfigurationDecisions(values []ConfigurationDecision) {
	sort.Slice(values, func(left, right int) bool { return values[left].path < values[right].path })
}
