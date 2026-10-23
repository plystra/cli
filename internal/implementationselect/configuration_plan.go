package implementationselect

import (
	"context"
	"fmt"
	"slices"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

// PreviewConfiguration edits one declared field in memory and validates the
// complete final graph and configuration. Private values remain behind the
// result's explicit byte accessors and never enter its public identity.
func PreviewConfiguration(ctx context.Context, options Options, change changerequest.Configuration) (PlanResult, error) {
	return previewConfigurations(ctx, options, []changerequest.Configuration{change})
}

// PreviewConfigurations plans a configuration-only request against one captured
// input snapshot. Only the complete candidate, not intermediate edits, must pass
// graph, ownership and requiredness validation.
func PreviewConfigurations(ctx context.Context, options Options, request changerequest.Request) (PlanResult, error) {
	if !request.Valid() {
		return PlanResult{}, ErrSelect
	}
	var changes []changerequest.Configuration
	for _, operation := range request.Operations() {
		change, ok := operation.Configuration()
		if !ok {
			return PlanResult{}, ErrSelect
		}
		changes = append(changes, change)
	}
	return previewConfigurations(ctx, options, changes)
}

func previewConfigurations(ctx context.Context, options Options, changes []changerequest.Configuration) (PlanResult, error) {
	if ctx == nil || len(changes) == 0 || options.Constructor != "" {
		return PlanResult{}, ErrSelect
	}
	for _, change := range changes {
		if change.Owner() == "" {
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
	edits, writeRequired, err := prepareConfigurationEdits(ctx, inputs, before, changes)
	if err != nil {
		return PlanResult{}, err
	}
	updated, _, err := applicationmeta.EditConfigurationValues(original, edits, inputs.ConfigurationSelection().Mode() == "environment")
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
	impact, err := configurationimpact.New(input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	var target string
	if len(changes) == 1 {
		target = changes[0].Owner()
	}
	result, err := selectionPlanResult(inputs, target, "configuration", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	// Sets retain write intent independently of private value equality.
	result.writeRequired = writeRequired
	return result, nil
}

func prepareConfigurationEdits(ctx context.Context, inputs applicationresolve.SelectionInputs, identities applicationmeta.Composition, changes []changerequest.Configuration) ([]applicationmeta.ConfigurationValueEdit, bool, error) {
	owners, err := inputs.CandidateOwners(identities)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	type ownerSchema struct {
		namespace applicationmeta.ConfigurationNamespace
		schema    implementationinventory.Configuration
	}
	schemas := make(map[string]ownerSchema)
	var edits []applicationmeta.ConfigurationValueEdit
	writeRequired := false
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return nil, false, fmt.Errorf("%w: %w", ErrSelect, err)
		}
		owner, known := schemas[change.Owner()]
		if !known {
			owner.namespace = applicationmeta.ConfigurationNamespaceResource
			var exists bool
			if constructor, parseErr := constructorsymbol.Parse(change.Owner()); parseErr == nil {
				owner.namespace = applicationmeta.ConfigurationNamespaceImplementation
				if !slices.Contains(owners, constructor) {
					return nil, false, ErrSelect
				}
				if declaration, found := inputs.Declarations().Implementations().BySymbol(constructor); found {
					owner.schema, exists = declaration.Configuration()
				}
			} else {
				for _, instance := range identities.Manifest().ResourceInstances() {
					if instance.Name() == change.Owner() {
						if provider, found := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider()); found {
							owner.schema, exists = provider.Configuration()
						}
						break
					}
				}
			}
			if !exists {
				return nil, false, ErrSelect
			}
			schemas[change.Owner()] = owner
		}
		value, hasValue := change.Value()
		if change.Action() != changerequest.ConfigurationSet && change.Action() != changerequest.ConfigurationRemove || hasValue != (change.Action() == changerequest.ConfigurationSet) {
			return nil, false, ErrSelect
		}
		edit, err := applicationmeta.PrepareConfigurationValueEdit(owner.namespace, change.Owner(), change.Path(), owner.schema, value)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %w", ErrSelect, err)
		}
		edits = append(edits, edit)
		writeRequired = writeRequired || change.Action() == changerequest.ConfigurationSet
	}
	return edits, writeRequired, nil
}
