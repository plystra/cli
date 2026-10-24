package implementationselect

import (
	"context"
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

// PreviewDependencyConfigurations plans a complete final selection against
// already captured pre- and post-dependency declarations. A zero previous
// snapshot means unknown old declarations, never proof of owner absence.
// Dependency and source operations must already be materialized in current.
func PreviewDependencyConfigurations(ctx context.Context, previous, current applicationresolve.SelectionInputs, request changerequest.Request) (PlanResult, error) {
	if ctx == nil || !request.Valid() || current.Module().Path() == "" {
		return PlanResult{}, ErrSelect
	}
	if err := ctx.Err(); err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	var edit applicationmeta.SelectionConfigurationEdit
	var changes []changerequest.Configuration
	hasDependency := false
	for _, operation := range request.Operations() {
		switch operation.Kind() {
		case changerequest.KindDependency:
			hasDependency = true
		case changerequest.KindInterfaceCreate, changerequest.KindImplementationCreate:
		case changerequest.KindConfiguration:
			change, _ := operation.Configuration()
			changes = append(changes, change)
		case changerequest.KindInterfaceRoot:
			change, _ := operation.InterfaceRoot()
			id, err := interfaceid.Parse(change.Interface())
			if err != nil {
				return PlanResult{}, ErrSelect
			}
			edit.Roots = append(edit.Roots, applicationmeta.InterfaceRootEdit{Interface: id, Root: string(change.Root()), Present: change.Action() == changerequest.RootPresent})
		case changerequest.KindImplementationSelection:
			change, _ := operation.ImplementationSelection()
			id, err := interfaceid.Parse(change.Interface())
			if err != nil {
				return PlanResult{}, ErrSelect
			}
			selection := applicationmeta.ImplementationSelectionEdit{Interface: id, Remove: change.Action() == changerequest.SelectionRemove}
			if !selection.Remove {
				name, _ := change.Constructor()
				selection.Constructor, err = constructorsymbol.Parse(name)
				if err != nil {
					return PlanResult{}, ErrSelect
				}
			}
			edit.Selections = append(edit.Selections, selection)
		case changerequest.KindResourceSelection:
			change, _ := operation.ResourceSelection()
			resource := applicationmeta.ResourceSelectionEdit{Instance: change.Instance(), Remove: change.Action() == changerequest.SelectionRemove}
			if !resource.Remove {
				name, _ := change.Provider()
				var err error
				resource.Provider, err = constructorsymbol.Parse(name)
				if err != nil {
					return PlanResult{}, ErrSelect
				}
			}
			edit.Resources = append(edit.Resources, resource)
		default:
			return PlanResult{}, ErrSelect
		}
	}
	if !hasDependency {
		return PlanResult{}, ErrSelect
	}
	original := current.SelectedSnapshot().Data()
	before, err := current.ComposeSelectionCandidate(original)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	old := before
	oldLayers := current.CurrentLayers()
	if previous.Module().Path() != "" {
		old, err = previous.ComposeSelectionCandidate(previous.SelectedSnapshot().Data())
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
		oldLayers = previous.CurrentLayers()
	}
	identities := before
	if len(edit.Roots)+len(edit.Selections)+len(edit.Resources) > 0 {
		identities, err = current.ProjectSelectionChanges(edit.Roots, edit.Selections, edit.Resources)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
	}
	writeRequired := false
	edit.Values, writeRequired, err = prepareConfigurationEdits(ctx, current, identities, changes)
	if err != nil {
		return PlanResult{}, err
	}
	edit.Configurations, edit.Bindings, err = dependencyOwnershipRemovals(previous, current, old, before, identities, edit.Resources)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	overlay := current.ConfigurationSelection().Mode() == "environment"
	if overlay {
		lower := current.RootManifest()
		for index := range edit.Configurations {
			_, edit.Configurations[index].Tombstone = lower.Configuration(edit.Configurations[index].Constructor)
		}
		markInheritedBindingRemovals(lower, edit.Bindings)
	}
	for index := range edit.Roots {
		root := &edit.Roots[index]
		root.PreserveRoot = root.Present && before.Manifest().HasInterfaceRoot(root.Interface, root.Root)
	}
	updated, _, err := applicationmeta.EditSelectionConfiguration(original, edit, overlay)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	candidate, err := current.ComposeCandidate(updated)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	if err := current.ValidateCandidate(candidate); err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	input := authoredResourceConfigurationImpact(oldLayers, candidate.CurrentLayers(), current.ConfigurationSelection().Path())
	input = authoredImplementationConfigurationImpact(oldLayers, candidate.CurrentLayers(), input)
	input.NewlyRequiredFields, err = dependencyRequiredFields(previous, current, old, candidate)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	impact, err := configurationimpact.CompareBindings(old.Manifest().ResourceBindings(), candidate.Manifest().ResourceBindings(), input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	if len(impact.DerivedOperations()) > changerequest.MaximumOperations {
		return PlanResult{}, ErrSelect
	}
	if err := ctx.Err(); err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	result, err := selectionPlanResult(current, "", "dependency_configuration", constructorsymbol.Symbol{}, updated, impact)
	if err != nil {
		return PlanResult{}, err
	}
	result.writeRequired = writeRequired
	return result, nil
}

func dependencyOwnershipRemovals(previous, current applicationresolve.SelectionInputs, old, before, final applicationmeta.Composition, resources []applicationmeta.ResourceSelectionEdit) ([]applicationmeta.ConstructorConfigurationRemoval, []applicationmeta.ResourceBindingRemoval, error) {
	owners, err := current.CandidateOwners(final)
	if err != nil {
		return nil, nil, err
	}
	retained := make(map[constructorsymbol.Symbol]bool)
	for _, owner := range owners {
		retained[owner] = true
	}
	oldOwners := make(map[constructorsymbol.Symbol]bool)
	for _, choice := range old.Manifest().ImplementationChoices() {
		oldOwners[choice.Constructor()] = true
	}
	// Partial old closure evidence proves only the owners it positively found.
	owners, _ = previous.CandidateOwners(old)
	for _, owner := range owners {
		oldOwners[owner] = true
	}
	var configurations []applicationmeta.ConstructorConfigurationRemoval
	for _, configured := range applicationmeta.ConstructorConfigurationIdentities(current.CurrentLayers()) {
		owner := configured.Constructor()
		if oldOwners[owner] && !retained[owner] {
			configurations = append(configurations, applicationmeta.ConstructorConfigurationRemoval{Constructor: owner})
		}
	}
	oldInstances := make(map[string]applicationmeta.ResourceInstance)
	finalInstances := make(map[string]applicationmeta.ResourceInstance)
	for _, instance := range old.Manifest().ResourceInstances() {
		oldInstances[instance.Name()] = instance
	}
	for _, instance := range final.Manifest().ResourceInstances() {
		finalInstances[instance.Name()] = instance
	}
	for index := range resources {
		resource := &resources[index]
		if resource.Remove {
			continue
		}
		var instance applicationmeta.ResourceInstance
		found := false
		for _, value := range before.Manifest().ResourceInstances() {
			if value.Name() == resource.Instance {
				instance, found = value, true
				break
			}
		}
		if !found {
			return nil, nil, ErrTargetNotFound
		}
		provider, visible := current.Declarations().ResourceProviders().BySymbol(resource.Provider)
		oldProvider, oldVisible := previous.Declarations().ResourceProviders().BySymbol(instance.Provider())
		if !visible || oldVisible && oldProvider.ID() != provider.ID() {
			return nil, nil, ErrProviderIncompatible
		}
		resource.DiscardConfiguration = instance.Provider().String() != "" && instance.Provider() != resource.Provider
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, binding := range before.Manifest().ResourceBindings() {
		remove := false
		switch binding.Namespace() {
		case "implementations":
			owner, err := constructorsymbol.Parse(binding.Consumer())
			if err != nil {
				return nil, nil, err
			}
			if oldOwners[owner] && !retained[owner] {
				remove = true
			} else if retained[owner] {
				oldDeclaration, oldKnown := previous.Declarations().Implementations().BySymbol(owner)
				declaration, known := current.Declarations().Implementations().BySymbol(owner)
				remove = oldKnown && known && implementationParameter(oldDeclaration, binding.ParameterName()) && !implementationParameter(declaration, binding.ParameterName())
			}
		case "instances":
			oldInstance, owned := oldInstances[binding.Consumer()]
			instance, remains := finalInstances[binding.Consumer()]
			if owned && !remains {
				remove = true
			} else if owned && remains {
				oldProvider, oldKnown := previous.Declarations().ResourceProviders().BySymbol(oldInstance.Provider())
				provider, known := current.Declarations().ResourceProviders().BySymbol(instance.Provider())
				if oldKnown && known {
					oldParameter, parameter := false, false
					for _, dependency := range oldProvider.Dependencies() {
						oldParameter = oldParameter || dependency.ParameterName() == binding.ParameterName()
					}
					for _, dependency := range provider.Dependencies() {
						parameter = parameter || dependency.ParameterName() == binding.ParameterName()
					}
					remove = oldParameter && !parameter
				}
			}
		}
		if remove {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return configurations, bindings, nil
}

func implementationParameter(declaration implementationinventory.Implementation, name string) bool {
	for _, dependency := range declaration.RequiredResources() {
		if dependency.ParameterName() == name {
			return true
		}
	}
	return false
}

func dependencyRequiredFields(previous, current applicationresolve.SelectionInputs, before, after applicationmeta.Composition) ([]configurationimpact.NewlyRequiredField, error) {
	oldReachable, oldErr := previous.CandidateReachableConstructors(before)
	reachable, err := current.CandidateReachableConstructors(after)
	if err != nil {
		return nil, err
	}
	wasReachable := make(map[constructorsymbol.Symbol]bool)
	for _, owner := range oldReachable {
		wasReachable[owner] = true
	}
	active := make(map[constructorsymbol.Symbol]bool)
	for _, owner := range reachable {
		active[owner] = true
	}
	// A dormant explicit choice with an authored object also has required
	// obligations. An unconfigured dormant choice does not acquire them yet.
	for _, configured := range after.Manifest().Configurations() {
		if !active[configured.Constructor()] {
			reachable = append(reachable, configured.Constructor())
		}
	}
	var fields []configurationimpact.NewlyRequiredField
	for _, owner := range reachable {
		declaration, _ := current.Declarations().Implementations().BySymbol(owner)
		schema, hasSchema := declaration.Configuration()
		if !hasSchema {
			continue
		}
		oldDeclaration, known := previous.Declarations().Implementations().BySymbol(owner)
		oldSchema, _ := oldDeclaration.Configuration()
		reason := "newly-required-field-in-constructor-schema"
		if oldErr == nil && active[owner] && !wasReachable[owner] {
			oldSchema = implementationinventory.Configuration{}
			reason = "required-field-on-newly-reachable-constructor"
		} else if !known {
			continue
		}
		fields = appendDependencyRequiredFields(fields, owner.String(), declaration.Source(), fmt.Sprintf("config[%q]", owner.String()), reason, oldSchema.Fields(), schema.Fields())
	}
	oldInstances := make(map[string]applicationmeta.ResourceInstance)
	for _, instance := range before.Manifest().ResourceInstances() {
		oldInstances[instance.Name()] = instance
	}
	for _, instance := range after.Manifest().ResourceInstances() {
		provider, _ := current.Declarations().ResourceProviders().BySymbol(instance.Provider())
		schema, hasSchema := provider.Configuration()
		if !hasSchema {
			continue
		}
		oldInstance, existed := oldInstances[instance.Name()]
		oldProvider, known := previous.Declarations().ResourceProviders().BySymbol(oldInstance.Provider())
		oldSchema, _ := oldProvider.Configuration()
		reason := "newly-required-field-in-resource-provider-schema"
		if existed && oldInstance.Provider() != instance.Provider() || !existed && previous.Module().Path() != "" {
			oldSchema = implementationinventory.Configuration{}
			reason = "required-field-on-newly-selected-resource-provider"
		} else if !known {
			continue
		}
		fields = appendDependencyRequiredFields(fields, instance.Name(), provider.Source(), fmt.Sprintf("resources.instances[%q].config", instance.Name()), reason, oldSchema.Fields(), schema.Fields())
	}
	return fields, nil
}

func appendDependencyRequiredFields(result []configurationimpact.NewlyRequiredField, owner, source, prefix, reason string, oldFields, fields []implementationinventory.ConfigurationField) []configurationimpact.NewlyRequiredField {
	previous := make(map[string]implementationinventory.ConfigurationField, len(oldFields))
	for _, field := range oldFields {
		previous[field.Name()] = field
	}
	for _, field := range fields {
		old := previous[field.Name()]
		path := prefix + fmt.Sprintf("[%q]", field.Name())
		if field.Required() && (!old.Required() || old.HasDefault() && !field.HasDefault()) {
			result = append(result, configurationimpact.NewlyRequiredField{Owner: owner, Path: path, SchemaSource: source, RequirednessReason: reason, DefaultSatisfies: field.HasDefault()})
		}
		if field.Value().Kind() == implementationinventory.ConfigurationValueObject {
			var children []implementationinventory.ConfigurationField
			if old.Value().Kind() == implementationinventory.ConfigurationValueObject {
				children = old.Value().Fields()
			}
			result = appendDependencyRequiredFields(result, owner, source, path, reason, children, field.Value().Fields())
		}
	}
	return result
}
