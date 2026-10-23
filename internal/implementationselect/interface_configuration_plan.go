package implementationselect

import (
	"context"
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

// PreviewInterfaceConfigurations plans roots, choices and optional typed fields
// against their shared final ownership. No intermediate candidate is installed.
func PreviewInterfaceConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var roots []applicationmeta.InterfaceRootEdit
	var selections []applicationmeta.ImplementationSelectionEdit
	var changes []changerequest.Configuration
	for _, operation := range request.Operations() {
		if change, ok := operation.Configuration(); ok {
			changes = append(changes, change)
		} else if change, ok := operation.InterfaceRoot(); ok {
			id, err := interfaceid.Parse(change.Interface())
			if err != nil {
				return PlanResult{}, ErrSelect
			}
			roots = append(roots, applicationmeta.InterfaceRootEdit{Interface: id, Root: string(change.Root()), Present: change.Action() == changerequest.RootPresent})
		} else if change, ok := operation.ImplementationSelection(); ok {
			id, err := interfaceid.Parse(change.Interface())
			if err != nil {
				return PlanResult{}, ErrSelect
			}
			selection := applicationmeta.ImplementationSelectionEdit{Interface: id, Remove: change.Action() == changerequest.SelectionRemove}
			if !selection.Remove {
				name, _ := change.Constructor()
				selection.Constructor, err = constructorsymbol.Parse(name)
				if err != nil || change.Action() != changerequest.SelectionSet {
					return PlanResult{}, ErrSelect
				}
			}
			selections = append(selections, selection)
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if len(roots)+len(selections) == 0 {
		return PlanResult{}, ErrSelect
	}
	inputs, err := discoverPlanInputs(ctx, options)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	original := inputs.SelectedSnapshot().Data()
	before, err := inputs.ComposeSelectionCandidate(original)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	identities, err := inputs.ProjectInterfaceChanges(roots, selections)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	edits, writeRequired, err := prepareConfigurationEdits(ctx, inputs, identities, changes)
	if err != nil {
		return PlanResult{}, err
	}
	configurations, bindings, err := implementationOwnerRemovals(inputs, before, identities)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	if overlay {
		lower := inputs.RootManifest()
		for index := range configurations {
			_, configurations[index].Tombstone = lower.Configuration(configurations[index].Constructor)
		}
		markInheritedBindingRemovals(lower, bindings)
	}
	for index := range roots {
		roots[index].PreserveRoot = roots[index].Present && before.Manifest().HasInterfaceRoot(roots[index].Interface, roots[index].Root)
	}
	updated, _, err := applicationmeta.EditInterfaceConfiguration(original, applicationmeta.InterfaceConfigurationEdit{
		Roots: roots, Selections: selections,
		Configurations: configurations, Bindings: bindings, Values: edits,
	}, overlay)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	if err := inputs.ValidateCandidate(candidate); err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	input := authoredResourceConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), inputs.ConfigurationSelection().Path())
	input = authoredImplementationConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), input)
	input.NewlyRequiredFields, err = newlyRequiredImplementationFields(inputs, before, candidate)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	impact, err := configurationimpact.Compare(applicationmeta.WithoutConstructorConfiguration(before.Manifest()), applicationmeta.WithoutConstructorConfiguration(candidate.Manifest()), input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	result, err := selectionPlanResult(inputs, "", "interface_configuration", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
