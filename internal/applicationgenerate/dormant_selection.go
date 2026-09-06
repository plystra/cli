package applicationgenerate

import (
	"fmt"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
)

func dormantImplementationSelections(resolved applicationresolve.Result) ([]applicationgen.DormantImplementationSelection, error) {
	active := make(map[string]struct{})
	for _, selection := range resolved.InterfaceResolution().Selections() {
		active[selection.InterfaceID.String()] = struct{}{}
	}
	fields := make(map[string]int)
	configurationFields := resolved.ResolutionEvidence().ConfigurationFields()
	for index, field := range configurationFields {
		fields[field.Path()] = index
	}

	choices := resolved.Manifest().ImplementationChoices()
	result := make([]applicationgen.DormantImplementationSelection, 0, len(choices))
	for _, choice := range choices {
		if _, reachable := active[choice.InterfaceID().String()]; reachable {
			continue
		}
		implementation, exists := resolved.Implementations().BySymbol(choice.Constructor())
		if !exists {
			return nil, fmt.Errorf("dormant Interface %s selects constructor %s absent from the validated inventory", choice.InterfaceID(), choice.Constructor())
		}
		path := fmt.Sprintf("interfaces.use[%q]", choice.InterfaceID().String())
		fieldIndex, exists := fields[path]
		if !exists {
			return nil, fmt.Errorf("dormant Interface %s has no configuration composition evidence", choice.InterfaceID())
		}
		selection, err := applicationgen.NewDormantImplementationSelection(choice, implementation, configurationFields[fieldIndex])
		if err != nil {
			return nil, fmt.Errorf("construct dormant Interface %s provenance: %w", choice.InterfaceID(), err)
		}
		result = append(result, selection)
	}
	return result, nil
}
