package applicationresolve

import (
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func resolveInterfaces(manifest applicationmeta.Manifest, composition applicationmeta.Composition, interfaces interfaceinventory.Index, implementations implementationinventory.Index) (interfaceresolution.Result, error) {
	provenance := make(map[string][]string)
	for _, record := range composition.ResolutionSources() {
		provenance[record.Path()] = append(provenance[record.Path()], record.Sources()...)
	}
	requirements := manifest.InterfaceRequirements()
	rootRequirements := make([]interfaceresolution.Requirement, 0, len(requirements))
	for _, requirement := range requirements {
		path := fmt.Sprintf("interfaces.require[%q]", requirement.ID().String())
		sources := uniqueSortedStrings(append([]string{requirement.Source()}, provenance[path]...))
		for _, source := range sources {
			rootRequirements = append(rootRequirements, interfaceresolution.Requirement{
				InterfaceID: requirement.ID(),
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
