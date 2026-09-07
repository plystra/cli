package applicationgenerate

import (
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/resolutionevidence"
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

func dormantConstructorConfigurations(
	resolved applicationresolve.Result,
	selections []applicationgen.DormantImplementationSelection,
) ([]applicationgen.DormantConstructorConfiguration, error) {
	activeConstructors := make(map[string]struct{})
	for _, node := range resolved.InterfaceResolution().Graph().ConstructionOrder() {
		activeConstructors[node.Symbol().String()] = struct{}{}
	}

	configurationFields := resolved.ResolutionEvidence().ConfigurationFields()
	recorded := make(map[string]struct{}, len(selections))
	result := make([]applicationgen.DormantConstructorConfiguration, 0, len(selections))
	for _, selection := range selections {
		constructor := selection.Constructor()
		if _, active := activeConstructors[constructor]; active {
			continue
		}
		if _, duplicate := recorded[constructor]; duplicate {
			continue
		}
		recorded[constructor] = struct{}{}

		root := fmt.Sprintf("config[%q]", constructor)
		fields := make([]resolutionevidence.ConfigurationField, 0)
		for _, field := range configurationFields {
			if field.Path() == root || strings.HasPrefix(field.Path(), root+`["`) {
				fields = append(fields, field)
			}
		}
		if len(fields) == 0 {
			continue
		}
		configuration, err := applicationgen.NewDormantConstructorConfiguration(selection, fields)
		if err != nil {
			return nil, fmt.Errorf("construct dormant constructor %s configuration provenance: %w", constructor, err)
		}
		result = append(result, configuration)
	}
	return result, nil
}
