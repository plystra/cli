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

// PreviewImplementationConfigurations plans one Implementation selection and
// typed configuration edits together. Every final obligation must validate;
// intermediate ownership projections have no configuration or apply authority.
func PreviewImplementationConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var selection changerequest.ImplementationSelection
	var changes []changerequest.Configuration
	for _, operation := range request.Operations() {
		if change, ok := operation.Configuration(); ok {
			changes = append(changes, change)
		} else if choice, ok := operation.ImplementationSelection(); ok && selection.Interface() == "" {
			selection = choice
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if selection.Interface() == "" || len(changes) == 0 {
		return PlanResult{}, ErrSelect
	}
	id, err := interfaceid.Parse(selection.Interface())
	if err != nil {
		return PlanResult{}, ErrSelect
	}
	remove := selection.Action() == changerequest.SelectionRemove
	var constructor constructorsymbol.Symbol
	if !remove {
		name, _ := selection.Constructor()
		constructor, err = constructorsymbol.Parse(name)
		if err != nil || selection.Action() != changerequest.SelectionSet {
			return PlanResult{}, ErrSelect
		}
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
	identities, err := inputs.ProjectImplementationSelection(id, constructor, remove)
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
		// Only the captured root can reintroduce these exact leaves. Reading its
		// raw membership avoids validating an unfinished typed candidate here.
		lower := inputs.RootManifest()
		for index := range configurations {
			_, configurations[index].Tombstone = lower.Configuration(configurations[index].Constructor)
		}
		markInheritedBindingRemovals(lower, bindings)
	}
	updated, _, err := applicationmeta.EditImplementationConfiguration(original, applicationmeta.ImplementationConfigurationEdit{
		Interface: id, Constructor: constructor, Remove: remove,
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
	result, err := selectionPlanResult(inputs, selection.Interface(), "interface", constructor, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
