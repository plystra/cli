package applicationresolve

import (
	"fmt"

	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/intrinsicinterface"
	"github.com/plystra/cli/internal/plugininventory"
)

func resolveInterfaces(manifest applicationmeta.Manifest, composition applicationmeta.Composition, interfaces interfaceinventory.Index, implementations implementationinventory.Index, legacyPlugins plugininventory.Index, sourceContext applicationinput.SourceContext) (interfaceresolution.Result, error) {
	choiceProvenance := make(map[string][]string)
	for _, record := range composition.ResolutionSources() {
		choiceProvenance[record.Path()] = append(choiceProvenance[record.Path()], record.Sources()...)
	}
	requirements := manifest.InterfaceRequirements()
	exposures := manifest.HTTPExposures()
	rootRequirements := make([]interfaceresolution.Requirement, 0, len(requirements)+len(exposures))
	for _, requirement := range requirements {
		path := fmt.Sprintf("interfaces.require[%q]", requirement.ID().String())
		sources, err := applicationinput.ConfigurationSources(sourceContext, requirement.Source(), path)
		if err != nil {
			return interfaceresolution.Result{}, fmt.Errorf("interface requirement %s provenance: %w", requirement.ID(), err)
		}
		for _, source := range sources {
			rootRequirements = append(rootRequirements, interfaceresolution.Requirement{
				InterfaceID: requirement.ID(),
				Source: interfaceresolution.RequirementSource{
					Kind:       interfaceresolution.RequirementDeclaration,
					Reference:  source.Reference,
					ModulePath: source.ModulePath,
					Path:       source.Path,
					Line:       source.Line,
					Column:     source.Column,
				},
			})
		}
	}
	visibleInterfaces := make(map[string]struct{})
	for _, definition := range interfaces.Interfaces() {
		visibleInterfaces[definition.ID()] = struct{}{}
	}
	legacyCapabilities := legacyCapabilityIDs(legacyPlugins)
	intrinsicInterfaces := intrinsicInterfaceIDs()
	for _, exposure := range exposures {
		identifier := exposure.ID().String()
		_, intrinsic := intrinsicInterfaces[identifier]
		if _, visible := visibleInterfaces[identifier]; !visible && !intrinsic {
			if _, legacy := legacyCapabilities[identifier]; legacy {
				continue
			}
		}
		path := fmt.Sprintf("http.expose[%q]", identifier)
		sources, err := applicationinput.ConfigurationSources(sourceContext, exposure.Source(), path)
		if err != nil {
			return interfaceresolution.Result{}, fmt.Errorf("HTTP exposure %s provenance: %w", exposure.ID(), err)
		}
		for _, source := range sources {
			rootRequirements = append(rootRequirements, interfaceresolution.Requirement{
				InterfaceID: exposure.ID(),
				Source: interfaceresolution.RequirementSource{
					Kind:       interfaceresolution.RequirementExposure,
					Reference:  source.Reference,
					ModulePath: source.ModulePath,
					Path:       source.Path,
					Line:       source.Line,
					Column:     source.Column,
				},
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
			Sources:     uniqueSortedStrings(append([]string{choice.Source()}, choiceProvenance[path]...)),
		}
	}
	return interfaceresolution.Resolve(interfaceresolution.Input{
		Interfaces:      interfaces,
		Implementations: implementations,
		Requirements:    rootRequirements,
		Choices:         explicitChoices,
	})
}

func validateConstructorConfigurationOwners(manifest applicationmeta.Manifest, resolution interfaceresolution.Result) error {
	owners := make(map[string]struct{})
	for _, choice := range manifest.ImplementationChoices() {
		owners[choice.Constructor().String()] = struct{}{}
	}
	for _, node := range resolution.Graph().ConstructionOrder() {
		owners[node.Symbol().String()] = struct{}{}
	}
	for _, configured := range manifest.Configurations() {
		constructor := configured.Constructor().String()
		if _, owned := owners[constructor]; owned {
			continue
		}
		return fmt.Errorf("%w: config[%q] at %s", ErrUnownedConstructorConfiguration, constructor, configured.Source())
	}
	return nil
}

func intrinsicInterfaceIDs() map[string]struct{} {
	result := make(map[string]struct{})
	for _, definition := range intrinsicinterface.Definitions() {
		result[definition.ID().String()] = struct{}{}
	}
	return result
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
