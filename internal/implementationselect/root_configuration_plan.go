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

// PreviewRootConfigurations plans one root and typed field edits against final
// ownership. Only the complete candidate must validate; no bytes are installed.
func PreviewRootConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || options.Target != "" || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	var root changerequest.InterfaceRoot
	var changes []changerequest.Configuration
	for _, operation := range request.Operations() {
		if change, ok := operation.Configuration(); ok {
			changes = append(changes, change)
		} else if change, ok := operation.InterfaceRoot(); ok && root.Interface() == "" {
			root = change
		} else {
			return PlanResult{}, ErrSelect
		}
	}
	if root.Interface() == "" || len(changes) == 0 {
		return PlanResult{}, ErrSelect
	}
	id, err := interfaceid.Parse(root.Interface())
	if err != nil {
		return PlanResult{}, ErrSelect
	}
	present := root.Action() == changerequest.RootPresent
	inputs, err := discoverPlanInputs(ctx, options)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	original := inputs.SelectedSnapshot().Data()
	before, err := inputs.ComposeSelectionCandidate(original)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	identities, err := inputs.ProjectInterfaceRoot(id, string(root.Root()), present)
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
	updated, _, err := applicationmeta.EditRootConfiguration(original, applicationmeta.RootConfigurationEdit{
		Interface: id, Root: string(root.Root()), Present: present,
		PreserveRoot:   present && before.Manifest().HasInterfaceRoot(id, string(root.Root())),
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
	result, err := selectionPlanResult(inputs, root.Interface(), "interface_root", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}
