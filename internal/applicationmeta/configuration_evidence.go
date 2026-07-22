package applicationmeta

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ConfigurationDecisionSummary is a redacted description of one typed
// configuration decision. It intentionally never contains the configured
// value or a Secret reference target.
type ConfigurationDecisionSummary string

const (
	ConfigurationSummaryRemoval    ConfigurationDecisionSummary = "removal"
	ConfigurationSummaryObject     ConfigurationDecisionSummary = "object"
	ConfigurationSummaryCapability ConfigurationDecisionSummary = "capability"
	ConfigurationSummaryProvider   ConfigurationDecisionSummary = "provider"
	ConfigurationSummaryAlias      ConfigurationDecisionSummary = "alias"
	ConfigurationSummaryString     ConfigurationDecisionSummary = "string"
	ConfigurationSummaryBoolean    ConfigurationDecisionSummary = "boolean"
	ConfigurationSummaryDuration   ConfigurationDecisionSummary = "duration"
	ConfigurationSummaryArray      ConfigurationDecisionSummary = "array"
	ConfigurationSummarySecret     ConfigurationDecisionSummary = "secret-reference"
	ConfigurationSummaryValue      ConfigurationDecisionSummary = "value"
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
// composition. Process-local settings deliberately return false.
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
			case maintenanceHTTPExposure, maintenanceRequirement:
				summary = ConfigurationSummaryCapability
			case maintenanceProvider:
				summary = ConfigurationSummaryProvider
			case maintenanceAlias:
				summary = ConfigurationSummaryAlias
			case maintenancePluginConfig:
				summary = configurationDecisionSummary(decision.config)
			}
		}
		result = append(result, ConfigurationDecision{
			path:                 decision.path,
			digest:               decision.digest,
			summary:              summary,
			removed:              decision.removed,
			source:               source,
			dependencyComposable: true,
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

func configurationDecisionSummary(decision pluginConfigDecision) ConfigurationDecisionSummary {
	if decision.kind == pluginConfigObject {
		return ConfigurationSummaryObject
	}
	if decision.kind == pluginConfigRemoval {
		return ConfigurationSummaryRemoval
	}
	if decision.valueType == string(ConfigurationSummarySecret) || decision.valueType == "secret" {
		return ConfigurationSummarySecret
	}
	if decision.valueType == "array" || strings.HasPrefix(decision.valueType, "array:") {
		return ConfigurationSummaryArray
	}
	switch decision.valueType {
	case "string", "url", "email":
		return ConfigurationSummaryString
	case "boolean":
		return ConfigurationSummaryBoolean
	case "duration":
		return ConfigurationSummaryDuration
	default:
		// The machine-readable schema is extensible within the closed Kernel
		// vocabulary. Keep an unknown future value redacted rather than leaking
		// a raw schema token into diagnostics.
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
