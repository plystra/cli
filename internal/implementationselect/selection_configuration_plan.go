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

// PreviewSelectionConfigurations plans roots, choices and optional typed fields
// against their shared final ownership. No intermediate candidate is installed.
func PreviewSelectionConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var roots []applicationmeta.InterfaceRootEdit
	var selections []applicationmeta.ImplementationSelectionEdit
	var resources []applicationmeta.ResourceSelectionEdit
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
		} else if change, ok := operation.ResourceSelection(); ok {
			resource := applicationmeta.ResourceSelectionEdit{Instance: change.Instance(), Remove: change.Action() == changerequest.SelectionRemove}
			if !resource.Remove {
				name, _ := change.Provider()
				var err error
				resource.Provider, err = constructorsymbol.Parse(name)
				if err != nil || change.Action() != changerequest.SelectionSet {
					return PlanResult{}, ErrSelect
				}
			}
			resources = append(resources, resource)
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if len(request.Operations()) < 2 || len(roots)+len(selections)+len(resources) == 0 {
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
	identities, err := inputs.ProjectSelectionChanges(roots, selections, resources)
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
	for index := range resources {
		resource := &resources[index]
		if resource.Remove {
			for _, instance := range before.Manifest().ResourceInstances() {
				if instance.Name() != resource.Instance {
					continue
				}
				for _, binding := range before.Manifest().ResourceBindings() {
					if binding.Namespace() == "instances" && binding.Consumer() == resource.Instance {
						bindings = append(bindings, bindingRemoval(binding))
					}
				}
				break
			}
		} else {
			var cleanup []applicationmeta.ResourceBindingRemoval
			resource.DiscardConfiguration, cleanup, err = resourceProviderChange(inputs, before.Manifest().ResourceInstances(), before.Manifest().ResourceBindings(), resource.Instance, resource.Provider)
			if err != nil {
				return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
			}
			bindings = append(bindings, cleanup...)
		}
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
	updated, _, err := applicationmeta.EditSelectionConfiguration(original, applicationmeta.SelectionConfigurationEdit{
		Roots: roots, Selections: selections, Resources: resources,
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
	for _, resource := range resources {
		if resource.Remove {
			continue
		}
		fields, err := newlyRequiredResourceFields(inputs, resource.Instance, resource.Provider)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
		input.NewlyRequiredFields = append(input.NewlyRequiredFields, fields...)
	}
	impact, err := configurationimpact.Compare(applicationmeta.WithoutConstructorConfiguration(before.Manifest()), applicationmeta.WithoutConstructorConfiguration(candidate.Manifest()), input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	result, err := selectionPlanResult(inputs, "", "selection_configuration", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
