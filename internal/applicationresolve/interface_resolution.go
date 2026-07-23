package applicationresolve

import (
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/plugininventory"
)

func resolveInterfaces(manifest applicationmeta.Manifest, composition applicationmeta.Composition, interfaces interfaceinventory.Index, implementations implementationinventory.Index, legacyPlugins plugininventory.Index, currentProjectPaths []string) (interfaceresolution.Result, error) {
	provenance := make(map[string][]string)
	for _, record := range composition.ResolutionSources() {
		provenance[record.Path()] = append(provenance[record.Path()], record.Sources()...)
	}
	current := make(map[string]struct{}, len(currentProjectPaths))
	for _, path := range currentProjectPaths {
		current[path] = struct{}{}
	}
	requirements := manifest.InterfaceRequirements()
	exposures := manifest.HTTPExposures()
	rootRequirements := make([]interfaceresolution.Requirement, 0, len(requirements)+len(exposures))
	for _, requirement := range requirements {
		path := fmt.Sprintf("interfaces.require[%q]", requirement.ID().String())
		sources := interfaceRequirementSources(requirement.Source(), path, provenance, current)
		for _, source := range sources {
			rootRequirements = append(rootRequirements, interfaceresolution.Requirement{
				InterfaceID: requirement.ID(),
				Source:      source,
			})
		}
	}
	visibleInterfaces := make(map[string]struct{})
	for _, definition := range interfaces.Interfaces() {
		visibleInterfaces[definition.ID()] = struct{}{}
	}
	legacyCapabilities := legacyCapabilityIDs(legacyPlugins)
	for _, exposure := range exposures {
		identifier := exposure.ID().String()
		if _, visible := visibleInterfaces[identifier]; !visible {
			if _, legacy := legacyCapabilities[identifier]; legacy {
				continue
			}
		}
		path := fmt.Sprintf("http.expose[%q]", identifier)
		sources := interfaceRequirementSources(exposure.Source(), path, provenance, current)
		for _, source := range sources {
			rootRequirements = append(rootRequirements, interfaceresolution.Requirement{
				InterfaceID: exposure.ID(),
				Source:      source,
			})
		}
	}
	choices := manifest.ImplementationChoices()
	explicitChoices := make([]interfaceresolution.Choice, len(choices))
	for index, choice := range choices {
		path := fmt.Sprintf("interfaces.use[%q]", choice.InterfaceID().String())
		explicitChoices[index] = interfaceresolution.Choice{
			InterfaceID: choice.InterfaceID(),
			Constructor: choice.Constructor(),
			Sources:     uniqueSortedStrings(append([]string{choice.Source()}, provenance[path]...)),
		}
	}
	return interfaceresolution.Resolve(interfaceresolution.Input{
		Interfaces:      interfaces,
		Implementations: implementations,
		Requirements:    rootRequirements,
		Choices:         explicitChoices,
	})
}

func interfaceRequirementSources(source, path string, provenance map[string][]string, current map[string]struct{}) []string {
	values := append([]string(nil), provenance[path]...)
	if _, explicit := current[path]; explicit || len(values) == 0 {
		values = append(values, source)
	}
	return uniqueSortedStrings(values)
}

// legacyCapabilityIDs isolates the pre-Gate-14 exposure path. An exposure
// backed only by that catalog remains owned by legacy resolution; every other
// exposure is an Interface root and is validated before generation.
func legacyCapabilityIDs(plugins plugininventory.Index) map[string]struct{} {
	result := make(map[string]struct{})
	for _, definition := range intrinsiccatalog.Definitions() {
		result[definition.ID().String()] = struct{}{}
	}
	for _, plugin := range plugins.Plugins() {
		for _, provided := range plugin.Provides() {
			result[provided.String()] = struct{}{}
		}
	}
	return result
}
