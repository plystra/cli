package implementationselect

import (
	"context"
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
)

// PreviewResourceConfigurations plans one instance/provider change and disjoint
// typed fields together. Every final obligation must validate; no intermediate
// Config or identity projection grants apply authority.
func PreviewResourceConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var selection changerequest.ResourceSelection
	var changes []changerequest.Configuration
	for _, operation := range request.Operations() {
		if change, ok := operation.Configuration(); ok {
			changes = append(changes, change)
		} else if choice, ok := operation.ResourceSelection(); ok && selection.Instance() == "" {
			selection = choice
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if selection.Instance() == "" || len(changes) == 0 {
		return PlanResult{}, ErrSelect
	}
	remove := selection.Action() == changerequest.SelectionRemove
	var provider constructorsymbol.Symbol
	if !remove {
		name, _ := selection.Provider()
		var err error
		provider, err = constructorsymbol.Parse(name)
		if err != nil || selection.Action() != changerequest.SelectionSet {
			return PlanResult{}, ErrSelect
		}
	}
	inputs, err := discoverPlanInputs(ctx, options)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	instances, beforeBindings, err := applicationmeta.ResourceSelectionIdentities(inputs.CurrentLayers())
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	var discard bool
	var bindings []applicationmeta.ResourceBindingRemoval
	if remove {
		for _, instance := range instances {
			if instance.Name() != selection.Instance() {
				continue
			}
			for _, binding := range beforeBindings {
				if binding.Namespace() == "instances" && binding.Consumer() == selection.Instance() {
					bindings = append(bindings, bindingRemoval(binding))
				}
			}
			break
		}
	} else {
		discard, bindings, err = resourceProviderChange(inputs, instances, beforeBindings, selection.Instance(), provider)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
	}
	identities, err := inputs.ProjectResourceSelection(selection.Instance(), provider, remove)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	edits, writeRequired, err := prepareConfigurationEdits(ctx, inputs, identities, changes)
	if err != nil {
		return PlanResult{}, err
	}
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	if overlay {
		markInheritedBindingRemovals(inputs.RootManifest(), bindings)
	}
	updated, _, err := applicationmeta.EditResourceConfiguration(inputs.SelectedSnapshot().Data(), applicationmeta.ResourceConfigurationEdit{
		Instance: selection.Instance(), Provider: provider, Remove: remove, DiscardConfiguration: discard,
		Bindings: bindings, Values: edits,
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
	if !remove {
		input.NewlyRequiredFields, err = newlyRequiredResourceFields(inputs, selection.Instance(), provider)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
	}
	impact, err := configurationimpact.CompareBindings(beforeBindings, candidate.Manifest().ResourceBindings(), input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	result, err := selectionPlanResult(inputs, selection.Instance(), "resource", provider, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
