package applicationmeta

import (
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/capabilityid"
)

// ErrApplyOverlay reports an invalid typed current-project overlay.
var ErrApplyOverlay = errors.New("apply current-project configuration overlay")

// ApplyOverlay applies one sparse higher-precedence current-project Manifest
// over a lower current-project Manifest. It preserves explicit removals so the
// result can still suppress dependency-derived declarations during Compose.
func ApplyOverlay(base, overlay Manifest, schemas SchemaLookup) (Manifest, error) {
	if schemas == nil {
		return Manifest{}, fmt.Errorf("%w: schema lookup is nil", ErrApplyOverlay)
	}

	httpAddress, hasHTTPAddress, removeHTTPAddress := base.httpAddress, base.hasHTTPAddress, base.removeHTTPAddress
	if overlay.removeHTTPAddress {
		httpAddress, hasHTTPAddress, removeHTTPAddress = "", false, true
	} else if overlay.hasHTTPAddress {
		httpAddress, hasHTTPAddress, removeHTTPAddress = overlay.httpAddress, true, false
	}
	httpTransports := overlayHTTPTransports(base.httpTransports, overlay.httpTransports)
	startupTimeout, hasStartupTimeout, removeStartupTimeout := base.startupTimeout, base.hasStartupTimeout, base.removeStartupTimeout
	if overlay.removeStartupTimeout {
		startupTimeout, hasStartupTimeout, removeStartupTimeout = DefaultStartupTimeout, false, true
	} else if overlay.hasStartupTimeout {
		startupTimeout, hasStartupTimeout, removeStartupTimeout = overlay.startupTimeout, true, false
	}

	exposures, removedExposures := overlayExposureSet(base, overlay)
	requirements, removedRequirements := overlayRequirementSet(base, overlay)
	choices, removedChoices := overlayProviderChoices(base, overlay)
	aliases, removedAliases := overlayAliases(base, overlay)
	configurations, removedConfigurations, err := overlayPluginConfigurations(base, overlay, schemas)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	if err := rejectAliasResolutionInputs(requirements, choices, aliases); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	if err := rejectAliasChains(aliases); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}

	return Manifest{
		httpAddress:            httpAddress,
		hasHTTPAddress:         hasHTTPAddress,
		removeHTTPAddress:      removeHTTPAddress,
		httpTransports:         httpTransports,
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
	}, nil
}

func overlayHTTPTransports(base, overlay httpTransportLayer) httpTransportLayer {
	result := base
	if overlay.removeConnect {
		result.connect, result.hasConnect, result.removeConnect = false, false, true
	} else if overlay.hasConnect {
		result.connect, result.hasConnect, result.removeConnect = overlay.connect, true, false
	}
	if overlay.removeREST {
		result.rest, result.hasREST, result.removeREST = false, false, true
	} else if overlay.hasREST {
		result.rest, result.hasREST, result.removeREST = overlay.rest, true, false
	}
	return result
}

func overlayExposureSet(base, overlay Manifest) ([]HTTPExposure, []capabilityRemoval) {
	values := make(map[capabilityid.Identifier]HTTPExposure)
	removals := make(map[capabilityid.Identifier]capabilityRemoval)
	for _, value := range base.httpExposures {
		values[value.id] = value
	}
	for _, removal := range base.removedHTTPExposures {
		removals[removal.id] = removal
	}
	for _, value := range overlay.httpExposures {
		values[value.id] = value
		delete(removals, value.id)
	}
	for _, removal := range overlay.removedHTTPExposures {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]HTTPExposure, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, sortedCapabilityRemovals(removals)
}

func overlayRequirementSet(base, overlay Manifest) ([]CapabilityRequirement, []capabilityRemoval) {
	values := make(map[capabilityid.Identifier]CapabilityRequirement)
	removals := make(map[capabilityid.Identifier]capabilityRemoval)
	for _, value := range base.requirements {
		values[value.id] = value
	}
	for _, removal := range base.removedRequirements {
		removals[removal.id] = removal
	}
	for _, value := range overlay.requirements {
		values[value.id] = value
		delete(removals, value.id)
	}
	for _, removal := range overlay.removedRequirements {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]CapabilityRequirement, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, sortedCapabilityRemovals(removals)
}

func overlayProviderChoices(base, overlay Manifest) ([]ProviderChoice, []capabilityRemoval) {
	values := make(map[capabilityid.Identifier]ProviderChoice)
	removals := make(map[capabilityid.Identifier]capabilityRemoval)
	for _, value := range base.providerChoices {
		values[value.capability] = value
	}
	for _, removal := range base.removedProviderChoices {
		removals[removal.id] = removal
	}
	for _, value := range overlay.providerChoices {
		values[value.capability] = value
		delete(removals, value.capability)
	}
	for _, removal := range overlay.removedProviderChoices {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]ProviderChoice, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].capability.String() < result[right].capability.String()
	})
	return result, sortedCapabilityRemovals(removals)
}

func overlayAliases(base, overlay Manifest) ([]Alias, []capabilityRemoval) {
	values := make(map[capabilityid.Identifier]Alias)
	removals := make(map[capabilityid.Identifier]capabilityRemoval)
	for _, value := range base.aliases {
		values[value.id] = value
	}
	for _, removal := range base.removedAliases {
		removals[removal.id] = removal
	}
	for _, value := range overlay.aliases {
		values[value.id] = value
		delete(removals, value.id)
	}
	for _, removal := range overlay.removedAliases {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]Alias, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, sortedCapabilityRemovals(removals)
}

func sortedCapabilityRemovals(values map[capabilityid.Identifier]capabilityRemoval) []capabilityRemoval {
	result := make([]capabilityRemoval, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result
}

func overlayPluginConfigurations(base, overlay Manifest, schemas SchemaLookup) ([]PluginConfiguration, []pluginConfigurationRemoval, error) {
	lower, err := manifestConfigDecisions(base, schemas)
	if err != nil {
		return nil, nil, err
	}
	upper, err := manifestConfigDecisions(overlay, schemas)
	if err != nil {
		return nil, nil, err
	}
	lowerByPath := pluginConfigDecisionsByPath(lower)
	upperByPath := pluginConfigDecisionsByPath(upper)
	paths := make(map[string]struct{}, len(lowerByPath)+len(upperByPath))
	for path := range lowerByPath {
		paths[path] = struct{}{}
	}
	for path := range upperByPath {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)

	selected := make(map[string]pluginConfigDecision, len(ordered))
	for _, path := range ordered {
		upperDecision, hasUpper := upperByPath[path]
		prototype := upperDecision
		if !hasUpper {
			prototype = lowerByPath[path]
		}
		if hasUpper {
			for length := 0; length < len(prototype.segments); length++ {
				ancestorPath := pluginConfigPath(prototype.pluginID, prototype.segments[:length])
				if ancestor, exists := selected[ancestorPath]; exists && ancestor.kind != pluginConfigObject {
					return nil, nil, fmt.Errorf("%w: %s has incompatible lower %s and overlay %s types from %s and %s", ErrInheritedConflict, path, pluginConfigDecisionDescription(ancestor), pluginConfigDecisionDescription(prototype), ancestor.source, prototype.source)
				}
			}
			candidates := map[string]*pluginConfigCandidate{}
			if lowerDecision, exists := lowerByPath[path]; exists {
				candidates[pluginConfigCandidateKey(lowerDecision)] = &pluginConfigCandidate{decision: lowerDecision, sources: map[string]struct{}{lowerDecision.source: {}}}
			}
			if err := validateCurrentConfigDecision(path, upperDecision, candidates); err != nil {
				return nil, nil, err
			}
			selected[path] = clonePluginConfigDecision(upperDecision)
			continue
		}
		if suppressedByConfigAncestor(selected, prototype) {
			continue
		}
		selected[path] = clonePluginConfigDecision(prototype)
	}
	return renderPluginConfigurationLayer(selected)
}

func pluginConfigDecisionsByPath(values []pluginConfigDecision) map[string]pluginConfigDecision {
	result := make(map[string]pluginConfigDecision, len(values))
	for _, value := range values {
		result[pluginConfigPath(value.pluginID, value.segments)] = clonePluginConfigDecision(value)
	}
	return result
}
