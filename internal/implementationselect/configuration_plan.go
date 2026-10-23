package implementationselect

import (
	"context"
	"fmt"
	"slices"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

// PreviewConfiguration edits one declared field in memory and validates the
// complete final graph and configuration. Private values remain behind the
// result's explicit byte accessors and never enter its public identity.
func PreviewConfiguration(ctx context.Context, options Options, change changerequest.Configuration) (PlanResult, error) {
	if ctx == nil || change.Owner() == "" || options.Constructor != "" {
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
	namespace := applicationmeta.ConfigurationNamespaceResource
	var schema implementationinventory.Configuration
	var exists bool
	if constructor, parseErr := constructorsymbol.Parse(change.Owner()); parseErr == nil {
		namespace = applicationmeta.ConfigurationNamespaceImplementation
		owners, ownerErr := inputs.CandidateOwners(before)
		if ownerErr != nil || !slices.Contains(owners, constructor) {
			return PlanResult{}, ErrSelect
		}
		if declaration, found := inputs.Declarations().Implementations().BySymbol(constructor); found {
			schema, exists = declaration.Configuration()
		}
	} else {
		for _, instance := range before.Manifest().ResourceInstances() {
			if instance.Name() == change.Owner() {
				if provider, found := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider()); found {
					schema, exists = provider.Configuration()
				}
				break
			}
		}
	}
	if !exists {
		return PlanResult{}, ErrSelect
	}
	value, hasValue := change.Value()
	if change.Action() != changerequest.ConfigurationSet && change.Action() != changerequest.ConfigurationRemove || hasValue != (change.Action() == changerequest.ConfigurationSet) {
		return PlanResult{}, ErrSelect
	}
	updated, _, err := applicationmeta.EditConfigurationValue(original, namespace, change.Owner(), change.Path(), schema, value, inputs.ConfigurationSelection().Mode() == "environment")
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
	result, err := selectionPlanResult(inputs, change.Owner(), "configuration", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	// A set declares a write independently of private value equality, including
	// when encoding happens to reproduce the same bytes.
	result.writeRequired = change.Action() == changerequest.ConfigurationSet
	return result, nil
}
