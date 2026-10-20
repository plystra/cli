package implementationselect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/resourcename"
)

// PlanResult is a read-only, validated selection plan. It contains authored
// bytes only for the caller's private transaction boundary; public projections
// must use the identity accessors and ConfigurationImpact.
type PlanResult struct {
	inputs          applicationresolve.SelectionInputs
	target          string
	kind            string
	constructor     constructorsymbol.Symbol
	moduleRoot      string
	manifestPath    string
	original        []byte
	updated         []byte
	impact          configurationimpact.Impact
	compositionHash string
}

// Plan discovers and validates one selection without writing configuration,
// module metadata, generated output, or any other Project file.
func Plan(ctx context.Context, options Options) (PlanResult, error) {
	return plan(ctx, options, false)
}

// Preview returns a non-mutating selection plan even when the candidate lacks
// one newly required constructor value that the impact can identify exactly.
func Preview(ctx context.Context, options Options) (PlanResult, error) {
	return plan(ctx, options, true)
}

func plan(ctx context.Context, options Options, allowMissingRequired bool) (PlanResult, error) {
	if ctx == nil {
		return PlanResult{}, fmt.Errorf("%w: context is nil", ErrSelect)
	}
	id, idErr := interfaceid.Parse(options.Target)
	if idErr != nil && resourcename.Check(options.Target) != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, ErrInvalidTarget)
	}
	constructor, err := constructorsymbol.Parse(options.Constructor)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w: parse fully qualified Implementation constructor: %w", ErrSelect, ErrInvalidConstructor, err)
	}
	inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
		GoCommand: options.GoCommand, Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	updated, kind, impact, err := planSelection(inputs, id, options.Target, constructor, allowMissingRequired)
	if err != nil {
		var missing *interfaceresolution.UnknownInterfaceError
		if errors.As(err, &missing) && missing.InterfaceID() == id {
			err = fmt.Errorf("%w: %w", ErrTargetNotFound, err)
		}
		return PlanResult{}, fmt.Errorf("%w: %w: %w", ErrSelect, ErrConfigurationWrite, err)
	}
	original := inputs.SelectedSnapshot().Data()
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return PlanResult{
		inputs: inputs, target: options.Target, kind: kind, constructor: constructor,
		moduleRoot: inputs.Module().Path(), manifestPath: filepath.Join(inputs.Module().Path(), filepath.FromSlash(inputs.ConfigurationSelection().Path())),
		original: original, updated: updated, impact: impact, compositionHash: candidate.CompositionDigest(),
	}, nil
}

func (p PlanResult) Inputs() applicationresolve.SelectionInputs      { return p.inputs }
func (p PlanResult) Target() string                                  { return p.target }
func (p PlanResult) Kind() string                                    { return p.kind }
func (p PlanResult) Constructor() constructorsymbol.Symbol           { return p.constructor }
func (p PlanResult) ModuleRoot() string                              { return p.moduleRoot }
func (p PlanResult) ManifestPath() string                            { return p.manifestPath }
func (p PlanResult) Original() []byte                                { return append([]byte(nil), p.original...) }
func (p PlanResult) Updated() []byte                                 { return append([]byte(nil), p.updated...) }
func (p PlanResult) Changed() bool                                   { return !bytes.Equal(p.original, p.updated) }
func (p PlanResult) ConfigurationImpact() configurationimpact.Impact { return p.impact }
func (p PlanResult) CompositionDigest() string                       { return p.compositionHash }

func planSelection(inputs applicationresolve.SelectionInputs, id interfaceid.Identifier, target string, constructor constructorsymbol.Symbol, allowMissingRequired bool) ([]byte, string, configurationimpact.Impact, error) {
	original := inputs.SelectedSnapshot().Data()
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	var updated []byte
	var err error
	var before applicationmeta.Composition
	kind := "interface"
	if id.String() != "" {
		set := applicationmeta.SetImplementationChoice
		if overlay {
			set = applicationmeta.SetImplementationChoiceOverlay
		}
		updated, _, err = set(original, id, constructor)
	} else {
		kind = "resource"
		updated, err = selectResourceProvider(inputs, original, target, constructor, overlay)
	}
	if err != nil {
		return nil, kind, configurationimpact.Impact{}, err
	}
	if kind == "interface" {
		before, err = inputs.ComposeSelectionCandidate(original)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
		candidate, err := inputs.ComposeSelectionCandidate(updated)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
		updated, err = cleanupImplementationOwners(inputs, before, candidate, updated, overlay)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
	}
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return nil, kind, configurationimpact.Impact{}, err
	}
	// Planning checks the complete remaining binding graph before any live write.
	// Generation repeats resolution and owns compilation and rollback validation.
	validationErr := inputs.ValidateCandidate(candidate)
	var missing *applicationmeta.ConstructorConfigurationValueError
	if validationErr != nil && (!allowMissingRequired || kind != "interface" || !errors.Is(validationErr, applicationmeta.ErrConfigurationRequired) || !errors.As(validationErr, &missing)) {
		return nil, kind, configurationimpact.Impact{}, validationErr
	}
	impactBefore := before
	impactInput := configurationimpact.Input{}
	if composed, composeErr := inputs.ComposeCandidate(original); composeErr == nil {
		impactBefore = composed
	} else if kind == "resource" {
		if ownership, ownershipErr := inputs.ComposeResourceOwnershipCandidate(original); ownershipErr == nil {
			impactBefore = ownership
		}
	}
	if kind == "resource" {
		impactInput = authoredResourceConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), inputs.ConfigurationSelection().Path())
	}
	if kind == "interface" {
		impactInput.NewlyRequiredFields, err = newlyRequiredImplementationFields(inputs, before, candidate)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
	}
	if missing != nil {
		var owners []constructorsymbol.Symbol
		for _, field := range impactInput.NewlyRequiredFields {
			owner, parseErr := constructorsymbol.Parse(field.Owner)
			if parseErr != nil {
				return nil, kind, configurationimpact.Impact{}, parseErr
			}
			owners = append(owners, owner)
		}
		missingPaths, missingErr := inputs.MissingRequiredImplementationFields(candidate, owners)
		if missingErr != nil {
			return nil, kind, configurationimpact.Impact{}, missingErr
		}
		absent := make(map[string]bool, len(missingPaths))
		for _, path := range missingPaths {
			absent[path] = true
		}
		for _, field := range impactInput.NewlyRequiredFields {
			if !absent[field.Path] {
				continue
			}
			sources := []string{field.SchemaSource, missing.SourcePath()}
			sort.Strings(sources)
			if sources[0] == sources[1] {
				sources = sources[:1]
			}
			impactInput.UnresolvedDecisions = append(impactInput.UnresolvedDecisions, configurationimpact.UnresolvedDecision{
				Target: field.Path, Reason: "required-configuration-value-missing",
				Alternatives: []string{"provide-required-field", "retain-previous-constructor"},
				Sources:      sources,
			})
		}
		if len(impactInput.UnresolvedDecisions) == 0 {
			return nil, kind, configurationimpact.Impact{}, validationErr
		}
	}
	beforeManifest, afterManifest := impactBefore.Manifest(), candidate.Manifest()
	if kind == "resource" {
		beforeManifest = applicationmeta.WithoutResourceConfiguration(beforeManifest)
		afterManifest = applicationmeta.WithoutResourceConfiguration(afterManifest)
	}
	impact, err := configurationimpact.Compare(beforeManifest, afterManifest, impactInput)
	if err != nil {
		return nil, kind, configurationimpact.Impact{}, err
	}
	return updated, kind, impact, nil
}

type authoredResourceConfiguration struct {
	name, provider, source, document string
}

func resourceConfigurationContributions(layers []applicationmeta.Manifest) []authoredResourceConfiguration {
	type owner struct {
		provider      constructorsymbol.Symbol
		contributions []authoredResourceConfiguration
	}
	owners := make(map[string]owner)
	for _, layer := range layers {
		for _, instance := range layer.ResourceInstances() {
			current := owners[instance.Name()]
			if provider := instance.Provider(); provider.String() != "" {
				if current.provider != provider {
					current.contributions = nil
				}
				current.provider = provider
			}
			if instance.ConfigurationRemoved() {
				current.contributions = nil
			} else if instance.HasConfiguration() {
				current.contributions = append(current.contributions, authoredResourceConfiguration{
					name: instance.Name(), provider: current.provider.String(), source: instance.Source() + ".config", document: instance.DeclarationSource().Path(),
				})
			}
			owners[instance.Name()] = current
		}
		for _, instance := range layer.RemovedResourceInstances() {
			delete(owners, instance.Name())
		}
	}
	var result []authoredResourceConfiguration
	for _, owner := range owners {
		result = append(result, owner.contributions...)
	}
	return result
}

func authoredResourceConfigurationImpact(beforeLayers, afterLayers []applicationmeta.Manifest, selectedPath string) configurationimpact.Input {
	before := resourceConfigurationContributions(beforeLayers)
	after := resourceConfigurationContributions(afterLayers)
	retained := make(map[authoredResourceConfiguration]bool, len(after))
	retainedBySource := make(map[string]bool, len(after))
	for _, value := range after {
		retained[value] = true
		retainedBySource[value.name+"\x00"+value.source] = true
	}
	var input configurationimpact.Input
	retainedNames := make(map[string]bool)
	for _, value := range before {
		path := fmt.Sprintf("resources.instances[%q].config", value.name)
		if retained[value] || value.provider == "" && retainedBySource[value.name+"\x00"+value.source] {
			if !retainedNames[value.name] {
				input.RetainedOwners = append(input.RetainedOwners, configurationimpact.RetainedOwner{Owner: value.name, Path: path, Reason: "resource-instance-remains-effective"})
				retainedNames[value.name] = true
			}
		} else {
			input.NewlyUnownedPaths = append(input.NewlyUnownedPaths, configurationimpact.NewlyUnownedPath{Owner: value.name, Path: path, Source: value.source})
			if value.document == selectedPath {
				input.DerivedOperations = append(input.DerivedOperations, configurationimpact.DerivedOperation{Kind: "resource_configuration", Action: "remove", Owner: value.name, Path: path, Source: value.source})
			}
		}
	}
	return input
}

func newlyRequiredImplementationFields(inputs applicationresolve.SelectionInputs, before, after applicationmeta.Composition) ([]configurationimpact.NewlyRequiredField, error) {
	previous, err := inputs.ResolveCandidateInterfaces(before)
	if err != nil {
		return nil, nil
	}
	current, err := inputs.ResolveCandidateInterfaces(after)
	if err != nil {
		return nil, err
	}
	oldOwners := make(map[constructorsymbol.Symbol]bool)
	for _, node := range previous.Graph().ConstructionOrder() {
		oldOwners[node.Symbol()] = true
	}
	var fields []configurationimpact.NewlyRequiredField
	for _, node := range current.Graph().ConstructionOrder() {
		if oldOwners[node.Symbol()] {
			continue
		}
		implementation, exists := inputs.Declarations().Implementations().BySymbol(node.Symbol())
		if !exists {
			continue
		}
		configuration, exists := implementation.Configuration()
		if !exists {
			continue
		}
		fields = appendRequiredFixedObjectFields(fields, implementation.Symbol().String(), implementation.Source(),
			fmt.Sprintf("config[%q]", implementation.Symbol().String()), configuration.Fields())
	}
	return fields, nil
}

func appendRequiredFixedObjectFields(result []configurationimpact.NewlyRequiredField, owner, source, prefix string, fields []implementationinventory.ConfigurationField) []configurationimpact.NewlyRequiredField {
	for _, field := range fields {
		path := prefix + fmt.Sprintf("[%q]", field.Name())
		if field.Required() {
			result = append(result, configurationimpact.NewlyRequiredField{
				Owner: owner, Path: path, SchemaSource: source,
				RequirednessReason: "required-field-on-newly-reachable-constructor",
				DefaultSatisfies:   field.HasDefault(),
			})
		}
		if value := field.Value(); value.Kind() == implementationinventory.ConfigurationValueObject {
			result = appendRequiredFixedObjectFields(result, owner, source, path, value.Fields())
		}
	}
	return result
}

func selectResourceProvider(inputs applicationresolve.SelectionInputs, original []byte, target string, constructor constructorsymbol.Symbol, overlay bool) ([]byte, error) {
	instances, previousBindings, err := applicationmeta.ResourceSelectionIdentities(inputs.CurrentLayers())
	if err != nil {
		return nil, err
	}
	var instance applicationmeta.ResourceInstance
	found := false
	for _, value := range instances {
		if value.Name() == target {
			instance, found = value, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: named Resource instance %s", ErrTargetNotFound, target)
	}
	provider, visible := inputs.Declarations().ResourceProviders().BySymbol(constructor)
	previous, previousVisible := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider())
	if !visible || previousVisible && previous.ID() != provider.ID() {
		return nil, fmt.Errorf("%w: %s for %s", ErrProviderIncompatible, constructor, target)
	}
	changedProvider := instance.Provider().String() != "" && instance.Provider() != constructor
	set := applicationmeta.SetResourceProvider
	if overlay {
		set = applicationmeta.SetResourceProviderOverlay
	}
	updated, _, err := set(original, target, constructor, changedProvider, nil)
	if err != nil || !changedProvider || !previousVisible {
		return updated, err
	}
	oldParameters, newParameters := make(map[string]bool), make(map[string]bool)
	for _, dependency := range previous.Dependencies() {
		oldParameters[dependency.ParameterName()] = true
	}
	for _, dependency := range provider.Dependencies() {
		newParameters[dependency.ParameterName()] = true
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, binding := range previousBindings {
		if binding.Namespace() == "instances" && binding.Consumer() == target && oldParameters[binding.ParameterName()] && !newParameters[binding.ParameterName()] {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return removeOwnedPaths(inputs, updated, nil, bindings, overlay)
}

func cleanupImplementationOwners(inputs applicationresolve.SelectionInputs, before, candidate applicationmeta.Composition, updated []byte, overlay bool) ([]byte, error) {
	finalOwners, err := inputs.CandidateOwners(candidate)
	if err != nil {
		return nil, err
	}
	retained := make(map[constructorsymbol.Symbol]bool, len(finalOwners))
	for _, constructor := range finalOwners {
		retained[constructor] = true
	}
	previousOwners := make(map[constructorsymbol.Symbol]bool)
	// An invalid old graph cannot block a repair. Its explicit choices still
	// establish exact owners even when the former reachable closure is unknown.
	for _, choice := range before.Manifest().ImplementationChoices() {
		previousOwners[choice.Constructor()] = true
	}
	owners, _ := inputs.CandidateOwners(before)
	for _, constructor := range owners {
		previousOwners[constructor] = true
	}
	var configurations []applicationmeta.ConstructorConfigurationRemoval
	seen := make(map[constructorsymbol.Symbol]bool)
	layers := inputs.CurrentLayers()
	for _, layer := range layers {
		for _, configured := range layer.Configurations() {
			constructor := configured.Constructor()
			if previousOwners[constructor] && !retained[constructor] && !seen[constructor] {
				configurations = append(configurations, applicationmeta.ConstructorConfigurationRemoval{Constructor: constructor})
				seen[constructor] = true
			}
		}
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, binding := range candidate.Manifest().ResourceBindings() {
		if binding.Namespace() != "implementations" {
			continue
		}
		constructor, err := constructorsymbol.Parse(binding.Consumer())
		if err != nil {
			return nil, err
		}
		if previousOwners[constructor] && !retained[constructor] {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return removeOwnedPaths(inputs, updated, configurations, bindings, overlay)
}

func bindingRemoval(binding applicationmeta.ResourceBinding) applicationmeta.ResourceBindingRemoval {
	return applicationmeta.ResourceBindingRemoval{Namespace: binding.Namespace(), Consumer: binding.Consumer(), ParameterName: binding.ParameterName()}
}

func removeOwnedPaths(inputs applicationresolve.SelectionInputs, updated []byte, configurations []applicationmeta.ConstructorConfigurationRemoval, bindings []applicationmeta.ResourceBindingRemoval, overlay bool) ([]byte, error) {
	if len(configurations) == 0 && len(bindings) == 0 {
		return updated, nil
	}
	cleanup := applicationmeta.CleanupSelectionOwnership
	if overlay {
		cleanup = applicationmeta.CleanupSelectionOwnershipOverlay
	}
	// Existing selected-layer exclusions remain authored intent even when a
	// lower layer also suppresses the same path. Probe only local values.
	localConfigurations := make([]applicationmeta.ConstructorConfigurationRemoval, 0, len(configurations))
	for _, removal := range configurations {
		if _, exists := inputs.SelectedManifest().Configuration(removal.Constructor); exists {
			localConfigurations = append(localConfigurations, removal)
		}
	}
	deleted, _, err := cleanup(updated, localConfigurations, bindings)
	if err != nil {
		return nil, err
	}
	// Test the exact local deletions against the lower layers. Only paths that
	// would reappear need tombstones; unrelated lower-layer values stay unmaterialized.
	lower, err := inputs.ComposeCandidate(deleted)
	if err != nil {
		return nil, err
	}
	finalConfigurations := make([]applicationmeta.ConstructorConfigurationRemoval, 0, len(configurations))
	for _, removal := range configurations {
		_, removal.Tombstone = lower.Manifest().Configuration(removal.Constructor)
		_, local := inputs.SelectedManifest().Configuration(removal.Constructor)
		if removal.Tombstone || local {
			finalConfigurations = append(finalConfigurations, removal)
		}
	}
	for index := range bindings {
		for _, binding := range lower.Manifest().ResourceBindings() {
			if binding.Namespace() == bindings[index].Namespace && binding.Consumer() == bindings[index].Consumer && binding.ParameterName() == bindings[index].ParameterName {
				bindings[index].Tombstone = true
				break
			}
		}
	}
	result, _, err := cleanup(updated, finalConfigurations, bindings)
	return result, err
}
