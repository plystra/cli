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
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/resourcename"
)

const maximumResourceBindingPreviewCandidates = 64

// PlanResult is a read-only root, selection or configuration preview. It contains authored
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
	writeRequired   bool
}

// Plan discovers and validates one selection without writing configuration,
// module metadata, generated output, or any other Project file.
func Plan(ctx context.Context, options Options) (PlanResult, error) {
	return plan(ctx, options, false, false)
}

// Preview returns a non-mutating selection plan with bounded required-value or
// validated Resource-binding decisions when final validation cannot yet succeed.
func Preview(ctx context.Context, options Options) (PlanResult, error) {
	return plan(ctx, options, true, false)
}

// PreviewRemoval plans the exact removal of one authored Interface selection.
func PreviewRemoval(ctx context.Context, options Options) (PlanResult, error) {
	return plan(ctx, options, true, true)
}

// PreviewResourceRemoval removes a whole named instance and only that consumer's
// outgoing binding leaves. The remaining final graph must validate completely.
func PreviewResourceRemoval(ctx context.Context, options Options) (PlanResult, error) {
	if ctx == nil || resourcename.Check(options.Target) != nil || options.Constructor != "" {
		return PlanResult{}, fmt.Errorf("%w: invalid Resource removal request", ErrSelect)
	}
	inputs, err := discoverPlanInputs(ctx, options)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	original := inputs.SelectedSnapshot().Data()
	remove := applicationmeta.RemoveResourceInstance
	if inputs.ConfigurationSelection().Mode() == "environment" {
		remove = applicationmeta.RemoveResourceInstanceOverlay
	}
	updated, _, err := remove(original, options.Target)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	before, err := inputs.ComposeResourceOwnershipCandidate(original)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	var bindings []applicationmeta.ResourceBindingRemoval
	for _, instance := range before.Manifest().ResourceInstances() {
		if instance.Name() != options.Target {
			continue
		}
		for _, binding := range before.Manifest().ResourceBindings() {
			if binding.Namespace() == "instances" && binding.Consumer() == options.Target {
				bindings = append(bindings, bindingRemoval(binding))
			}
		}
		break
	}
	updated, err = removeOwnedPaths(inputs, updated, nil, bindings, inputs.ConfigurationSelection().Mode() == "environment")
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	if err := inputs.ValidateExecutableCandidate(ctx, candidate); err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	input := authoredResourceConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), inputs.ConfigurationSelection().Path())
	impact, err := configurationimpact.Compare(applicationmeta.WithoutResourceConfiguration(before.Manifest()), applicationmeta.WithoutResourceConfiguration(candidate.Manifest()), input)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return selectionPlanResult(inputs, options.Target, "resource", constructorsymbol.Symbol{}, updated, impact)
}

// PreviewInterfaceRoot plans one exact require or expose root change without
// installing configuration or weakening final ownership validation.
func PreviewInterfaceRoot(ctx context.Context, options Options, root string, present bool) (PlanResult, error) {
	id, err := interfaceid.Parse(options.Target)
	if ctx == nil || err != nil || options.Constructor != "" || root != "require" && root != "expose" {
		return PlanResult{}, fmt.Errorf("%w: invalid Interface root request", ErrSelect)
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
	updated := original
	if !present || !before.Manifest().HasInterfaceRoot(id, root) {
		edit := applicationmeta.SetInterfaceRoot
		if inputs.ConfigurationSelection().Mode() == "environment" {
			edit = applicationmeta.SetInterfaceRootOverlay
		}
		updated, _, err = edit(original, id, root, present)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
		}
	}
	updated, kind, impact, err := planEditedSelection(ctx, inputs, updated, "interface_root", options.Target, constructorsymbol.Symbol{}, true)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return selectionPlanResult(inputs, options.Target, kind, constructorsymbol.Symbol{}, updated, impact)
}

func discoverPlanInputs(ctx context.Context, options Options) (applicationresolve.SelectionInputs, error) {
	return applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		ObservePolicySupport: options.ObservePolicySupport,
		Start:                options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
		GoCommand: options.GoCommand, Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
}

func plan(ctx context.Context, options Options, allowIncomplete, remove bool) (PlanResult, error) {
	if ctx == nil {
		return PlanResult{}, fmt.Errorf("%w: context is nil", ErrSelect)
	}
	id, idErr := interfaceid.Parse(options.Target)
	if idErr != nil && resourcename.Check(options.Target) != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, ErrInvalidTarget)
	}
	var constructor constructorsymbol.Symbol
	if remove {
		if idErr != nil || options.Constructor != "" {
			return PlanResult{}, fmt.Errorf("%w: Interface removal requires an exact Interface and no constructor", ErrSelect)
		}
	} else {
		var err error
		constructor, err = constructorsymbol.Parse(options.Constructor)
		if err != nil {
			return PlanResult{}, fmt.Errorf("%w: %w: parse fully qualified Implementation constructor: %w", ErrSelect, ErrInvalidConstructor, err)
		}
	}
	inputs, err := discoverPlanInputs(ctx, options)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	updated, kind, impact, err := planSelection(ctx, inputs, id, options.Target, constructor, allowIncomplete, remove)
	if err != nil {
		var missing *interfaceresolution.UnknownInterfaceError
		if errors.As(err, &missing) && missing.InterfaceID() == id {
			err = fmt.Errorf("%w: %w", ErrTargetNotFound, err)
		}
		return PlanResult{}, fmt.Errorf("%w: %w: %w", ErrSelect, ErrConfigurationWrite, err)
	}
	return selectionPlanResult(inputs, options.Target, kind, constructor, updated, impact)
}

func selectionPlanResult(inputs applicationresolve.SelectionInputs, target, kind string, constructor constructorsymbol.Symbol, updated []byte, impact configurationimpact.Impact) (PlanResult, error) {
	original := inputs.SelectedSnapshot().Data()
	candidate, err := inputs.ComposeCandidate(updated)
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return PlanResult{
		inputs: inputs, target: target, kind: kind, constructor: constructor,
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
func (p PlanResult) Changed() bool                                   { return p.writeRequired || !bytes.Equal(p.original, p.updated) }
func (p PlanResult) ConfigurationImpact() configurationimpact.Impact { return p.impact }
func (p PlanResult) CompositionDigest() string                       { return p.compositionHash }

func planSelection(ctx context.Context, inputs applicationresolve.SelectionInputs, id interfaceid.Identifier, target string, constructor constructorsymbol.Symbol, allowIncomplete, remove bool) ([]byte, string, configurationimpact.Impact, error) {
	original := inputs.SelectedSnapshot().Data()
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	var updated []byte
	var err error
	kind := "interface"
	if id.String() != "" {
		if remove {
			removeChoice := applicationmeta.RemoveImplementationChoice
			if overlay {
				removeChoice = applicationmeta.RemoveImplementationChoiceOverlay
			}
			updated, _, err = removeChoice(original, id)
		} else {
			set := applicationmeta.SetImplementationChoice
			if overlay {
				set = applicationmeta.SetImplementationChoiceOverlay
			}
			updated, _, err = set(original, id, constructor)
		}
	} else {
		if remove {
			return nil, kind, configurationimpact.Impact{}, ErrInvalidTarget
		}
		kind = "resource"
		updated, err = selectResourceProvider(inputs, original, target, constructor, overlay)
	}
	if err != nil {
		return nil, kind, configurationimpact.Impact{}, err
	}
	return planEditedSelection(ctx, inputs, updated, kind, target, constructor, allowIncomplete)
}

func planEditedSelection(ctx context.Context, inputs applicationresolve.SelectionInputs, updated []byte, kind, target string, constructor constructorsymbol.Symbol, allowIncomplete bool) ([]byte, string, configurationimpact.Impact, error) {
	original := inputs.SelectedSnapshot().Data()
	overlay := inputs.ConfigurationSelection().Mode() == "environment"
	var before applicationmeta.Composition
	var err error
	if kind != "resource" {
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
	// Preview retains only bounded, final-state-validated decisions.
	validationErr := inputs.ValidateExecutableCandidate(ctx, candidate)
	var missingImplementation *applicationmeta.ConstructorConfigurationValueError
	var missingResource *applicationmeta.ResourceConfigurationError
	var unresolvedBinding *constructorgraph.ResourceBindingError
	if validationErr != nil {
		if !allowIncomplete {
			return nil, kind, configurationimpact.Impact{}, validationErr
		}
		if kind == "resource" && errors.Is(validationErr, constructorgraph.ErrAmbiguousResourceBinding) {
			if !errors.As(validationErr, &unresolvedBinding) || unresolvedBinding.Namespace() != constructorgraph.ResourceConsumerInstance || unresolvedBinding.Consumer() != target {
				return nil, kind, configurationimpact.Impact{}, validationErr
			}
		} else if errors.Is(validationErr, applicationmeta.ErrConfigurationRequired) {
			switch kind {
			case "interface", "interface_root":
				if !errors.As(validationErr, &missingImplementation) {
					return nil, kind, configurationimpact.Impact{}, validationErr
				}
			case "resource":
				if !errors.As(validationErr, &missingResource) || missingResource.InstanceName() != target || missingResource.Provider() != constructor {
					return nil, kind, configurationimpact.Impact{}, validationErr
				}
			}
		} else {
			return nil, kind, configurationimpact.Impact{}, validationErr
		}
	}
	impactBefore := before
	impactInput := authoredResourceConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), inputs.ConfigurationSelection().Path())
	previous, previousComposeErr := inputs.ComposeCandidate(original)
	if previousComposeErr == nil {
		impactBefore = previous
	} else if kind == "resource" {
		if ownership, ownershipErr := inputs.ComposeResourceOwnershipCandidate(original); ownershipErr == nil {
			impactBefore = ownership
		}
	}
	if kind == "resource" {
		impactInput.NewlyRequiredFields, err = newlyRequiredResourceFields(inputs, target, constructor)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
		if unresolvedBinding != nil {
			// New assignments prove final viability, not validity of the old provider.
			decisions, missing, decisionErr := viableResourceBindingDecisions(ctx, inputs, updated, target, constructor)
			if decisionErr != nil {
				return nil, kind, configurationimpact.Impact{}, decisionErr
			}
			if len(decisions) == 0 {
				return nil, kind, configurationimpact.Impact{}, validationErr
			}
			impactInput.UnresolvedDecisions = append(impactInput.UnresolvedDecisions, decisions...)
			missingResource = missing
		}
	}
	previousStateValid := false
	if allowIncomplete && previousComposeErr == nil && (missingImplementation != nil || missingResource != nil) {
		previousErr := inputs.ValidateExecutableCandidate(ctx, previous)
		if errors.Is(previousErr, applicationresolve.ErrPolicySupportUnverified) || errors.Is(previousErr, context.Canceled) || errors.Is(previousErr, context.DeadlineExceeded) || errors.Is(previousErr, applicationresolve.ErrConcurrentChange) {
			return nil, kind, configurationimpact.Impact{}, previousErr
		}
		previousStateValid = previousErr == nil
	}
	if missingResource != nil {
		if err := inputs.ValidateOtherResourceRequiredConfiguration(candidate, target); err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
	}
	if kind != "resource" {
		impactInput = authoredImplementationConfigurationImpact(inputs.CurrentLayers(), candidate.CurrentLayers(), impactInput)
		impactInput.NewlyRequiredFields, err = newlyRequiredImplementationFields(inputs, before, candidate)
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, err
		}
	}
	if missingImplementation != nil || missingResource != nil {
		var missingPaths []string
		var missingSource string
		alternatives := []string{"provide-required-field"}
		if previousStateValid {
			alternatives = append(alternatives, "retain-previous-constructor")
		}
		if kind == "interface_root" {
			alternatives = []string{"provide-required-field"}
			if previousStateValid {
				alternatives = append(alternatives, "retain-previous-root-state")
			}
		}
		if missingResource != nil {
			missingPaths, err = inputs.MissingRequiredResourceFields(candidate, target)
			missingSource = missingResource.SourcePath()
			alternatives = []string{"provide-required-field", "retain-previous-provider"}
			if !previousStateValid {
				alternatives = []string{"provide-required-field"}
			}
		} else {
			missingPaths, err = boundedMissingImplementationFields(inputs, candidate, impactInput.NewlyRequiredFields)
			missingSource = missingImplementation.SourcePath()
		}
		if err != nil {
			return nil, kind, configurationimpact.Impact{}, validationErr
		}
		absent := make(map[string]bool, len(missingPaths))
		for _, path := range missingPaths {
			absent[path] = true
		}
		for _, field := range impactInput.NewlyRequiredFields {
			if !absent[field.Path] {
				continue
			}
			sources := []string{field.SchemaSource, missingSource}
			sort.Strings(sources)
			if sources[0] == sources[1] {
				sources = sources[:1]
			}
			impactInput.UnresolvedDecisions = append(impactInput.UnresolvedDecisions, configurationimpact.UnresolvedDecision{
				Target: field.Path, Reason: "required-configuration-value-missing",
				Alternatives: alternatives,
				Sources:      sources,
			})
		}
		if len(impactInput.UnresolvedDecisions) == 0 {
			return nil, kind, configurationimpact.Impact{}, validationErr
		}
	}
	beforeManifest := applicationmeta.WithoutResourceConfiguration(impactBefore.Manifest())
	afterManifest := applicationmeta.WithoutResourceConfiguration(candidate.Manifest())
	if kind != "resource" {
		beforeManifest = applicationmeta.WithoutImplementationConfiguration(beforeManifest)
		afterManifest = applicationmeta.WithoutImplementationConfiguration(afterManifest)
	}
	impact, err := configurationimpact.Compare(beforeManifest, afterManifest, impactInput)
	if err != nil {
		return nil, kind, configurationimpact.Impact{}, err
	}
	return updated, kind, impact, nil
}

func boundedMissingImplementationFields(inputs applicationresolve.SelectionInputs, candidate applicationmeta.Composition, fields []configurationimpact.NewlyRequiredField) ([]string, error) {
	resolution, err := inputs.ResolveCandidateInterfaces(candidate)
	if err != nil {
		return nil, err
	}
	var owners []constructorsymbol.Symbol
	for _, node := range resolution.Graph().ConstructionOrder() {
		owners = append(owners, node.Symbol())
	}
	for _, configured := range candidate.Manifest().Configurations() {
		owners = append(owners, configured.Constructor())
	}
	// Enumerate all final requiredness obligations, not just constructors with
	// supported new fields. A decision must not hide another owner's failure.
	paths, err := inputs.MissingRequiredImplementationFields(candidate, owners)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field.Path] = true
	}
	for _, path := range paths {
		if !allowed[path] {
			return nil, applicationmeta.ErrConfigurationRequired
		}
	}
	for _, instance := range candidate.Manifest().ResourceInstances() {
		missing, err := inputs.MissingRequiredResourceFields(candidate, instance.Name())
		if err != nil {
			return nil, err
		}
		if len(missing) != 0 {
			return nil, applicationmeta.ErrConfigurationRequired
		}
	}
	return paths, nil
}

func authoredImplementationConfigurationImpact(beforeLayers, afterLayers []applicationmeta.Manifest, input configurationimpact.Input) configurationimpact.Input {
	retained := make(map[constructorsymbol.Symbol]bool)
	for _, configured := range applicationmeta.ConstructorConfigurationIdentities(afterLayers) {
		retained[configured.Constructor()] = true
	}
	for _, configured := range applicationmeta.ConstructorConfigurationIdentities(beforeLayers) {
		owner := configured.Constructor().String()
		path := "config." + owner
		if retained[configured.Constructor()] {
			input.RetainedOwners = append(input.RetainedOwners, configurationimpact.RetainedOwner{Owner: owner, Path: path, Reason: "owner-remains-effective"})
		} else {
			input.NewlyUnownedPaths = append(input.NewlyUnownedPaths, configurationimpact.NewlyUnownedPath{Owner: owner, Path: path, Source: configured.Source()})
			input.DerivedOperations = append(input.DerivedOperations, configurationimpact.DerivedOperation{Kind: "configuration", Action: "remove", Owner: owner, Path: path, Source: configured.Source()})
		}
	}
	return input
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
	previous, err := inputs.CandidateReachableConstructors(before)
	if err != nil {
		return nil, nil
	}
	current, err := inputs.CandidateReachableConstructors(after)
	if err != nil {
		return nil, err
	}
	oldOwners := make(map[constructorsymbol.Symbol]bool)
	for _, constructor := range previous {
		oldOwners[constructor] = true
	}
	var fields []configurationimpact.NewlyRequiredField
	for _, constructor := range current {
		if oldOwners[constructor] {
			continue
		}
		implementation, exists := inputs.Declarations().Implementations().BySymbol(constructor)
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

func newlyRequiredResourceFields(inputs applicationresolve.SelectionInputs, target string, constructor constructorsymbol.Symbol) ([]configurationimpact.NewlyRequiredField, error) {
	instances, _, err := applicationmeta.ResourceSelectionIdentities(inputs.CurrentLayers())
	if err != nil {
		return nil, err
	}
	var previous constructorsymbol.Symbol
	for _, instance := range instances {
		if instance.Name() == target {
			previous = instance.Provider()
			break
		}
	}
	if previous == constructor {
		return nil, nil
	}
	provider, exists := inputs.Declarations().ResourceProviders().BySymbol(constructor)
	if !exists {
		return nil, ErrProviderIncompatible
	}
	configuration, exists := provider.Configuration()
	if !exists {
		return nil, nil
	}
	fields := appendRequiredFixedObjectFields(nil, target, provider.Source(), fmt.Sprintf("resources.instances[%q].config", target), configuration.Fields())
	for index := range fields {
		fields[index].RequirednessReason = "required-field-on-newly-selected-resource-provider"
	}
	return fields, nil
}

func selectResourceProvider(inputs applicationresolve.SelectionInputs, original []byte, target string, constructor constructorsymbol.Symbol, overlay bool) ([]byte, error) {
	instances, previousBindings, err := applicationmeta.ResourceSelectionIdentities(inputs.CurrentLayers())
	if err != nil {
		return nil, err
	}
	changedProvider, bindings, err := resourceProviderChange(inputs, instances, previousBindings, target, constructor)
	if err != nil {
		return nil, err
	}
	set := applicationmeta.SetResourceProvider
	if overlay {
		set = applicationmeta.SetResourceProviderOverlay
	}
	updated, _, err := set(original, target, constructor, changedProvider, nil)
	if err != nil || len(bindings) == 0 {
		return updated, err
	}
	return removeOwnedPaths(inputs, updated, nil, bindings, overlay)
}

func resourceProviderChange(inputs applicationresolve.SelectionInputs, instances []applicationmeta.ResourceInstance, previousBindings []applicationmeta.ResourceBinding, target string, constructor constructorsymbol.Symbol) (bool, []applicationmeta.ResourceBindingRemoval, error) {
	var instance applicationmeta.ResourceInstance
	found := false
	for _, value := range instances {
		if value.Name() == target {
			instance, found = value, true
			break
		}
	}
	if !found {
		return false, nil, fmt.Errorf("%w: named Resource instance %s", ErrTargetNotFound, target)
	}
	provider, visible := inputs.Declarations().ResourceProviders().BySymbol(constructor)
	previous, previousVisible := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider())
	if !visible || previousVisible && previous.ID() != provider.ID() {
		return false, nil, fmt.Errorf("%w: %s for %s", ErrProviderIncompatible, constructor, target)
	}
	changedProvider := instance.Provider().String() != "" && instance.Provider() != constructor
	if !changedProvider || !previousVisible {
		return changedProvider, nil, nil
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
	return changedProvider, bindings, nil
}

func markInheritedBindingRemovals(root applicationmeta.Manifest, removals []applicationmeta.ResourceBindingRemoval) {
	inherited := make(map[[3]string]bool)
	for _, binding := range root.ResourceBindings() {
		inherited[[3]string{binding.Namespace(), binding.Consumer(), binding.ParameterName()}] = true
	}
	for index := range removals {
		removal := &removals[index]
		removal.Tombstone = inherited[[3]string{removal.Namespace, removal.Consumer, removal.ParameterName}]
	}
}

func cleanupImplementationOwners(inputs applicationresolve.SelectionInputs, before, candidate applicationmeta.Composition, updated []byte, overlay bool) ([]byte, error) {
	configurations, bindings, err := implementationOwnerRemovals(inputs, before, candidate)
	if err != nil {
		return nil, err
	}
	return removeOwnedPaths(inputs, updated, configurations, bindings, overlay)
}

func implementationOwnerRemovals(inputs applicationresolve.SelectionInputs, before, candidate applicationmeta.Composition) ([]applicationmeta.ConstructorConfigurationRemoval, []applicationmeta.ResourceBindingRemoval, error) {
	finalOwners, err := inputs.CandidateOwners(candidate)
	if err != nil {
		return nil, nil, err
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
			return nil, nil, err
		}
		if previousOwners[constructor] && !retained[constructor] {
			bindings = append(bindings, bindingRemoval(binding))
		}
	}
	return configurations, bindings, nil
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
