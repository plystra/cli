package applicationmeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/interfaceid"
)

// ErrApplyOverlay reports an invalid typed current-project overlay.
var ErrApplyOverlay = errors.New("apply current-project configuration overlay")

// ApplyOverlay applies one sparse higher-precedence current-project Manifest
// over the root current-project Manifest. It preserves explicit removals until
// the effective current-project model is finalized. Resource deltas without a
// local provider retain their sparse layers until Compose supplies schema
// context. Use Composition.Manifest for effective Resource state and
// Composition.CurrentLayers for authored evidence.
func ApplyOverlay(base, overlay Manifest, schemas SchemaLookup) (_ Manifest, applyErr error) {
	defer func() {
		if errors.Is(applyErr, ErrApplyOverlay) {
			applyErr = environmentOverlayError(overlay, applyErr)
		}
	}()

	if schemas == nil {
		return Manifest{}, fmt.Errorf("%w: schema lookup is nil", ErrApplyOverlay)
	}
	projectModule := base.modulePath
	if projectModule == "" {
		projectModule = overlay.modulePath
	} else if overlay.modulePath != "" && overlay.modulePath != projectModule {
		return Manifest{}, fmt.Errorf("%w: base and overlay belong to different Project modules", ErrApplyOverlay)
	}

	result, err := applyManifestLayer(base, overlay, schemas)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	result.modulePath = projectModule
	result.layers = append(append([]Manifest(nil), manifestLayers(base)...), manifestLayers(overlay)...)
	return result, nil
}

func manifestLayers(manifest Manifest) []Manifest {
	if len(manifest.layers) != 0 {
		return manifest.layers
	}
	return []Manifest{manifest}
}

func applyManifestLayer(base, overlay Manifest, schemas SchemaLookup) (Manifest, error) {
	httpAddress, hasHTTPAddress, removeHTTPAddress := base.httpAddress, base.hasHTTPAddress, base.removeHTTPAddress
	if overlay.removeHTTPAddress {
		httpAddress, hasHTTPAddress, removeHTTPAddress = "", false, true
	} else if overlay.hasHTTPAddress {
		httpAddress, hasHTTPAddress, removeHTTPAddress = overlay.httpAddress, true, false
	}
	httpCORS, err := overlayHTTPCORS(base.httpCORS, overlay.httpCORS)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	startupTimeout, hasStartupTimeout, removeStartupTimeout := base.startupTimeout, base.hasStartupTimeout, base.removeStartupTimeout
	if overlay.removeStartupTimeout {
		startupTimeout, hasStartupTimeout, removeStartupTimeout = DefaultStartupTimeout, false, true
	} else if overlay.hasStartupTimeout {
		startupTimeout, hasStartupTimeout, removeStartupTimeout = overlay.startupTimeout, true, false
	}

	exposures, removedExposures := overlayExposureSet(base, overlay)
	requirements, removedRequirements := overlayRequirementSet(base, overlay)
	choices, removedChoices := overlayProviderChoices(base, overlay)
	interfaceRequirements, removedInterfaceRequirements := overlayInterfaceRequirementSet(base, overlay)
	implementationChoices, removedImplementationChoices := overlayImplementationChoices(base, overlay)
	interfacePolicies, removedInterfacePolicies := overlayInterfacePolicies(base, overlay)
	aliases, removedAliases := overlayAliases(base, overlay)
	configurations, removedConfigurations, err := overlayConstructorConfigurations(base, overlay, schemas)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	resourceInstances, removedResourceInstances, resourceBindings, removedResourceBindings, err := overlayResources(base, overlay, schemas)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrApplyOverlay, err)
	}
	dataMembers, removedDataMembers := overlayDataMembers(base, overlay)

	return Manifest{
		modulePath:                    overlay.modulePath,
		source:                        base.source,
		httpAddress:                   httpAddress,
		hasHTTPAddress:                hasHTTPAddress,
		removeHTTPAddress:             removeHTTPAddress,
		httpCORS:                      httpCORS,
		httpExposures:                 exposures,
		removedHTTPExposures:          removedExposures,
		requirements:                  requirements,
		removedRequirements:           removedRequirements,
		providerChoices:               choices,
		removedProviderChoices:        removedChoices,
		interfaceRequirements:         interfaceRequirements,
		completeInterfaceRequirements: base.completeInterfaceRequirements || overlay.completeInterfaceRequirements,
		removedInterfaceReqs:          removedInterfaceRequirements,
		implementationChoices:         implementationChoices,
		removedImplementationChoices:  removedImplementationChoices,
		interfacePolicies:             interfacePolicies,
		removedInterfacePolicies:      removedInterfacePolicies,
		aliases:                       aliases,
		removedAliases:                removedAliases,
		configurations:                configurations,
		removedConfigurations:         removedConfigurations,
		resourceInstances:             resourceInstances,
		removedResourceInstances:      removedResourceInstances,
		resourceBindings:              resourceBindings,
		removedResourceBindings:       removedResourceBindings,
		dataMembers:                   dataMembers,
		removedDataMembers:            removedDataMembers,
		startupTimeout:                startupTimeout,
		hasStartupTimeout:             hasStartupTimeout,
		removeStartupTimeout:          removeStartupTimeout,
	}, nil
}

func overlayHTTPCORS(base, overlay httpCORSLayer) (httpCORSLayer, error) {
	if overlay.remove {
		return httpCORSLayer{remove: true}, nil
	}
	result := cloneHTTPCORSLayer(base)
	if !overlay.present {
		return result, nil
	}
	if !result.present || result.remove {
		result = httpCORSLayer{present: true}
	} else {
		result.present = true
		result.remove = false
	}
	if overlay.removeAllowedOrigins {
		result.allowedOrigins = nil
		result.hasAllowedOrigins = false
		result.removeAllowedOrigins = true
	} else if overlay.hasAllowedOrigins {
		result.allowedOrigins = append([]string(nil), overlay.allowedOrigins...)
		result.hasAllowedOrigins = true
		result.removeAllowedOrigins = false
	}
	if overlay.removeAllowCredentials {
		result.allowCredentials = false
		result.hasAllowCredentials = false
		result.removeAllowCredentials = true
	} else if overlay.hasAllowCredentials {
		result.allowCredentials = overlay.allowCredentials
		result.hasAllowCredentials = true
		result.removeAllowCredentials = false
	}
	return result, nil
}

func overlayExposureSet(base, overlay Manifest) ([]HTTPExposure, []interfaceRemoval) {
	values := make(map[interfaceid.Identifier]HTTPExposure)
	removals := make(map[interfaceid.Identifier]interfaceRemoval)
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
	return result, sortedInterfaceRemovals(removals)
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

func overlayInterfaceRequirementSet(base, overlay Manifest) ([]InterfaceRequirement, []interfaceRemoval) {
	if overlay.completeInterfaceRequirements {
		return overlay.InterfaceRequirements(), nil
	}
	values := make(map[interfaceid.Identifier]InterfaceRequirement)
	removals := make(map[interfaceid.Identifier]interfaceRemoval)
	for _, value := range base.interfaceRequirements {
		values[value.id] = value
	}
	for _, removal := range base.removedInterfaceReqs {
		removals[removal.id] = removal
	}
	for _, value := range overlay.interfaceRequirements {
		values[value.id] = value
		delete(removals, value.id)
	}
	for _, removal := range overlay.removedInterfaceReqs {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]InterfaceRequirement, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result, sortedInterfaceRemovals(removals)
}

func overlayImplementationChoices(base, overlay Manifest) ([]ImplementationChoice, []interfaceRemoval) {
	values := make(map[interfaceid.Identifier]ImplementationChoice)
	removals := make(map[interfaceid.Identifier]interfaceRemoval)
	for _, value := range base.implementationChoices {
		values[value.interfaceID] = value
	}
	for _, removal := range base.removedImplementationChoices {
		removals[removal.id] = removal
	}
	for _, value := range overlay.implementationChoices {
		values[value.interfaceID] = value
		delete(removals, value.interfaceID)
	}
	for _, removal := range overlay.removedImplementationChoices {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]ImplementationChoice, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].interfaceID.String() < result[right].interfaceID.String()
	})
	return result, sortedInterfaceRemovals(removals)
}

func overlayInterfacePolicies(base, overlay Manifest) ([]InterfacePolicy, []interfaceRemoval) {
	values := make(map[interfaceid.Identifier]InterfacePolicy)
	removals := make(map[interfaceid.Identifier]interfaceRemoval)
	for _, value := range base.interfacePolicies {
		values[value.interfaceID] = value
	}
	for _, removal := range base.removedInterfacePolicies {
		removals[removal.id] = removal
	}
	for _, value := range overlay.interfacePolicies {
		values[value.interfaceID] = value
		delete(removals, value.interfaceID)
	}
	for _, removal := range overlay.removedInterfacePolicies {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	result := make([]InterfacePolicy, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].interfaceID.String() < result[right].interfaceID.String()
	})
	return result, sortedInterfaceRemovals(removals)
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

func sortedInterfaceRemovals(values map[interfaceid.Identifier]interfaceRemoval) []interfaceRemoval {
	result := make([]interfaceRemoval, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].id.String() < result[right].id.String() })
	return result
}

func overlayConstructorConfigurations(base, overlay Manifest, schemas SchemaLookup) ([]ConstructorConfiguration, []constructorConfigurationRemoval, error) {
	lower, err := manifestConfigDecisions(base, schemas)
	if err != nil {
		return nil, nil, err
	}
	upper, err := manifestConfigDecisions(overlay, schemas)
	if err != nil {
		return nil, nil, err
	}
	selected := constructorConfigDecisionsByPath(lower)
	for _, decision := range upper {
		path := constructorConfigPath(decision.constructor, decision.segments)
		if decision.kind != constructorConfigObject {
			for candidate := range selected {
				if strings.HasPrefix(candidate, path+"[") {
					delete(selected, candidate)
				}
			}
		}
		selected[path] = cloneConstructorConfigDecision(decision)
	}
	return renderConstructorConfigurationLayer(selected)
}

func constructorConfigDecisionsByPath(values []constructorConfigDecision) map[string]constructorConfigDecision {
	result := make(map[string]constructorConfigDecision, len(values))
	for _, value := range values {
		result[constructorConfigPath(value.constructor, value.segments)] = cloneConstructorConfigDecision(value)
	}
	return result
}
