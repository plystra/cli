package command

import (
	"errors"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

const (
	diagnosticResourceInstanceInvalid  = diagnosticcode.ResourceInstanceInvalid
	diagnosticResourceBindingInvalid   = diagnosticcode.ResourceBindingInvalid
	diagnosticResourceBindingMissing   = diagnosticcode.ResourceBindingMissing
	diagnosticResourceBindingAmbiguous = diagnosticcode.ResourceBindingAmbiguous
)

func resourceResolutionDiagnosticCode(err error) string {
	var metadata *applicationmeta.ResourceMetadataError
	if errors.As(err, &metadata) && metadata != nil {
		return diagnosticcode.ResourceMetadataInvalid
	}
	var configuration *applicationmeta.ResourceConfigurationError
	if errors.As(err, &configuration) && configuration != nil {
		if errors.Is(configuration, applicationmeta.ErrConfigurationSchema) {
			return diagnosticcode.ResourceConfigurationSchemaInvalid
		}
		return diagnosticcode.ResourceConfigurationValuesInvalid
	}
	var cycle *constructorgraph.ResourceCycleError
	if errors.As(err, &cycle) && cycle != nil {
		return diagnosticcode.ResolveConstructorCycle
	}
	switch {
	case errors.Is(err, constructorgraph.ErrInvalidResourceInstance):
		return diagnosticResourceInstanceInvalid
	case errors.Is(err, constructorgraph.ErrInvalidResourceBinding):
		return diagnosticResourceBindingInvalid
	case errors.Is(err, constructorgraph.ErrMissingResourceBinding):
		return diagnosticResourceBindingMissing
	case errors.Is(err, constructorgraph.ErrAmbiguousResourceBinding):
		return diagnosticResourceBindingAmbiguous
	default:
		return ""
	}
}

func resourceResolutionRecovery(err error, context recoveryContext) (actionableDiagnostic, bool) {
	code := resourceResolutionDiagnosticCode(err)
	switch code {
	case diagnosticcode.ResourceMetadataInvalid:
		return recoveryDiagnostic(code, "Correct the reported Resource declaration in the owning Project document to use a valid named instance, exact provider, and exact consumer binding address, then rerun the command.")
	case diagnosticcode.ResourceConfigurationSchemaInvalid:
		return recoveryDiagnostic(code, "Select a discovered Resource provider with a compiled Go Config schema or remove the instance configuration object in the reported owning Project document, then rerun the command.")
	case diagnosticcode.ResourceConfigurationValuesInvalid:
		return recoveryDiagnostic(code, "Correct the reported instance configuration field in its owning Project document to match the selected provider's compiled Go Config schema, then rerun the command.")
	case diagnosticResourceInstanceInvalid:
		return recoveryDiagnostic(code, "Correct the named resources.instances entry in the reported owning Project document to select one visible Resource provider, then rerun the command.")
	case diagnosticResourceBindingInvalid:
		return recoveryDiagnostic(code, "Correct or remove the reported resources.bind entry so its exact consumer parameter targets an existing compatible instance, then rerun the command.")
	case diagnosticResourceBindingMissing:
		return recoveryDiagnostic(code, "Select a compatible named instance under resources.instances in "+context.configurationTarget()+", then rerun the command. Bind the exact consumer parameter explicitly when more than one compatible instance is selected.")
	case diagnosticResourceBindingAmbiguous:
		return recoveryDiagnostic(code, "Bind the reported parameter explicitly under resources.bind.implementations or resources.bind.instances in "+context.configurationTarget()+" to one of the compatible named instances, then rerun the command.")
	case diagnosticcode.ResolveConstructorCycle:
		return recoveryDiagnostic(code, "Remove one Resource dependency from the reported cycle or bind it to an acyclic compatible instance, then rerun the command.")
	default:
		return actionableDiagnostic{}, false
	}
}

func resourceResolutionSources(err error) []diagnosticjson.Source {
	var sources []diagnosticjson.Source
	var metadata *applicationmeta.ResourceMetadataError
	var configuration *applicationmeta.ResourceConfigurationError
	if errors.As(err, &metadata) && metadata != nil {
		location := metadata.Source()
		module := location.ModulePath()
		if module == "" {
			var outer diagnosticSourceLocation
			if errors.As(err, &outer) && outer != nil {
				module = outer.ModulePath()
			}
		}
		sources = append(sources, diagnosticjson.Source{Module: module, Path: location.Path(), Kind: "configuration-declaration", Line: location.Line(), Column: location.Column()})
	} else if errors.As(err, &configuration) && configuration != nil {
		sources = append(sources, diagnosticjson.Source{Module: configuration.ModulePath(), Path: configuration.SourcePath(), Kind: configuration.SourceKind(), Line: configuration.Line(), Column: configuration.Column()})
	}
	appendSources := func(values []constructorgraph.ResourceSource, kind string) {
		for _, source := range values {
			if source.ModulePath != "" && source.Path != "" {
				sources = append(sources, diagnosticjson.Source{Module: source.ModulePath, Path: source.Path, Kind: kind, Line: source.Line, Column: source.Column})
			}
		}
	}
	appendDependency := func(dependency constructorgraph.ResourceDependency) {
		kind := "implementation-constructor"
		if dependency.Namespace() == constructorgraph.ResourceConsumerInstance {
			kind = "resource-provider-constructor"
		}
		appendSources([]constructorgraph.ResourceSource{dependency.DeclarationSource()}, kind)
		appendSources(dependency.Sources(), "resource-binding")
		appendSources(dependency.SelectionSources(), "resource-selection")
		appendSources(dependency.ConsumerSelectionSources(), "resource-selection")
	}
	var instance *constructorgraph.ResourceInstanceError
	var binding *constructorgraph.ResourceBindingError
	var cycle *constructorgraph.ResourceCycleError
	switch {
	case errors.As(err, &instance) && instance != nil:
		appendSources(instance.Sources(), "resource-selection")
	case errors.As(err, &binding) && binding != nil:
		appendDependency(binding.Dependency())
		for _, source := range binding.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{Module: source.ModulePath, Path: source.Path, Kind: string(source.Kind), Line: source.Line, Column: source.Column})
		}
		for _, step := range binding.Steps() {
			sources = append(sources, diagnosticjson.Source{Module: step.RequiringModulePath(), Path: step.RequiringSourcePath(), Kind: "implementation-constructor", Line: step.RequiringLine(), Column: step.RequiringColumn()})
		}
		for _, candidate := range binding.Candidates() {
			appendSources(candidate.Sources(), "resource-selection")
			provider := candidate.Provider()
			position := provider.Declaration().Position()
			sources = append(sources, diagnosticjson.Source{Module: provider.ModulePath(), Path: position.Path, Kind: "resource-provider-constructor", Line: position.Line, Column: position.Column})
		}
	case errors.As(err, &cycle) && cycle != nil:
		for _, step := range cycle.Steps() {
			appendDependency(step)
		}
	}
	unique := make(map[diagnosticjson.Source]bool, len(sources))
	deduplicated := make([]diagnosticjson.Source, 0, len(sources))
	for _, source := range sources {
		if !unique[source] {
			deduplicated = append(deduplicated, source)
			unique[source] = true
		}
	}
	canonical, canonicalErr := diagnosticjson.CanonicalizeSources(deduplicated)
	if canonicalErr != nil {
		return nil
	}
	return canonical
}
