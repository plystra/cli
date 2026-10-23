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

// PreviewRootConfigurations plans roots and optional typed field edits against final
// ownership. Only the complete candidate must validate; no bytes are installed.
func PreviewRootConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var roots []applicationmeta.InterfaceRootEdit
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
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if len(roots) == 0 {
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
	identities, err := inputs.ProjectInterfaceRoots(roots)
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
	updated, _, err := applicationmeta.EditRootConfiguration(original, applicationmeta.RootConfigurationEdit{
		Roots:          roots,
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
	var target string
	if len(roots) == 1 {
		target = roots[0].Interface.String()
	}
	result, err := selectionPlanResult(inputs, target, "interface_root", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
