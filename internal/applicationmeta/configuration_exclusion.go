package applicationmeta

import (
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
)

// CurrentLayerDecisions retains authored layer evidence in this composition's
// exclusion context. A whole-object tombstone can suppress an unavailable old
// schema; its lower object then contributes only value-free owner provenance.
// ConfigurationDecisions on that lower layer alone remains strict.
func (c Composition) CurrentLayerDecisions(schemas SchemaLookup) ([][]ConfigurationDecision, error) {
	if !c.Valid() || schemas == nil {
		return nil, fmt.Errorf("configuration composition or schema lookup is unavailable")
	}
	result := make([][]ConfigurationDecision, len(c.currentLayers))
	for index, layer := range c.currentLayers {
		filtered, excluded, err := excludeUnavailableConfigurations(layer, c.currentLayers[index+1:], schemas)
		if err != nil {
			return nil, err
		}
		decisions, err := ConfigurationDecisions(filtered, schemas)
		if err != nil {
			return nil, err
		}
		result[index] = append(decisions, excluded...)
		sortConfigurationDecisions(result[index])
	}
	return result, nil
}

func excludeUnavailableConfigurations(layer Manifest, higher []Manifest, schemas SchemaLookup) (Manifest, []ConfigurationDecision, error) {
	removed := make(map[constructorsymbol.Symbol]bool)
	for _, upper := range higher {
		for _, removal := range upper.removedConfigurations {
			removed[removal.constructor] = true
		}
	}
	filtered := layer
	filtered.configurations = nil
	source := layer.source
	if source == "" {
		source = "plystra.yaml"
	}
	var excluded []ConfigurationDecision
	for _, configured := range layer.configurations {
		_, known := schemas(ConfigurationNamespaceImplementation, configured.constructor)
		if !removed[configured.constructor] || known {
			filtered.configurations = append(filtered.configurations, configured)
			continue
		}
		// The final graph cannot type this obsolete object. Validate its YAML
		// structure, but never reinterpret its private fields through a new schema.
		root, err := decodeNormalizedConfigNode(configured.yaml)
		if err != nil || validateUntypedConfigurationNode(root, &constructorConfigNormalizeState{}, 0) != nil {
			return Manifest{}, nil, constructorConfigValueError(configured.constructor, configured.source, configured.declarationSource, nil, ErrConfigurationInvalidValue)
		}
		excluded = append(excluded, ConfigurationDecision{
			path: constructorConfigPath(configured.constructor, nil), digest: digestStrings("plystra.unvalidated-constructor-configuration/v2", configured.constructor.String()),
			summary: ConfigurationSummaryObject, source: source, resolutionRelevant: true,
		})
	}
	return filtered, excluded, nil
}
