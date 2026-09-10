package command

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/dependencyadd"
	"github.com/plystra/cli/internal/dependencyremove"
	"github.com/plystra/cli/internal/dependencyupdate"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationcreate"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/implementationselect"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacecreate"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/providerresolution"
)

type recoveryContext struct {
	configurationPath string
	environmentName   string
	environment       []string
}

type actionableDiagnostic struct {
	code     string
	recovery string
}

type diagnosticSourceLocation interface {
	ModulePath() string
	SourcePath() string
	SourceKind() string
	Line() int
	Column() int
}

const (
	diagnosticTemplateInvalid                    = diagnosticcode.TemplateInvalid
	diagnosticCapabilityRequirementConflict      = diagnosticcode.CapabilityRequirementConflict
	diagnosticProviderContractConflict           = diagnosticcode.ProviderContractConflict
	diagnosticProviderContractMismatch           = diagnosticcode.ProviderContractMismatch
	diagnosticCapabilityContractConflict         = diagnosticcode.CapabilityContractConflict
	diagnosticCapabilitySchemaConflict           = diagnosticcode.CapabilitySchemaConflict
	diagnosticProviderSelectionInvalid           = diagnosticcode.ProviderSelectionInvalid
	diagnosticProviderMissing                    = diagnosticcode.ProviderMissing
	diagnosticProviderAmbiguous                  = diagnosticcode.ProviderAmbiguous
	diagnosticProjectManifestInvalid             = diagnosticcode.ProjectManifestInvalid
	diagnosticConfigurationInheritedConflict     = diagnosticcode.ConfigurationInheritedConflict
	diagnosticConfigurationOwnershipAmbiguous    = diagnosticcode.ConfigurationOwnershipAmbiguous
	diagnosticHTTPTransportSelectionInvalid      = diagnosticcode.HTTPTransportSelectionInvalid
	diagnosticEnvironmentOverlayInvalid          = diagnosticcode.EnvironmentOverlayInvalid
	diagnosticConfigurationInvalid               = diagnosticcode.ConfigurationInvalid
	diagnosticPluginConfigurationUnselected      = diagnosticcode.PluginConfigurationUnselected
	diagnosticPluginConfigurationPluginMissing   = diagnosticcode.PluginConfigurationPluginMissing
	diagnosticConfigurationSelectionInvalid      = diagnosticcode.ConfigurationSelectionInvalid
	diagnosticApplicationDependencyDrift         = diagnosticcode.ApplicationDependencyDrift
	diagnosticProjectNotFound                    = diagnosticcode.ProjectNotFound
	diagnosticGoModuleNotFound                   = diagnosticcode.GoModuleNotFound
	diagnosticGoModuleInvalid                    = diagnosticcode.GoModuleInvalid
	diagnosticGoModuleUnavailable                = diagnosticcode.GoModuleUnavailable
	diagnosticGoCommandFailed                    = diagnosticcode.GoCommandFailed
	diagnosticPluginTargetAmbiguous              = diagnosticcode.PluginTargetAmbiguous
	diagnosticPluginTargetNotFound               = diagnosticcode.PluginTargetNotFound
	diagnosticPluginTargetInvalid                = diagnosticcode.PluginTargetInvalid
	diagnosticGenerationActivationConflict       = diagnosticcode.GenerationActivationConflict
	diagnosticGenerationActivationMissing        = diagnosticcode.GenerationActivationMissing
	diagnosticGenerationProviderExtensionMissing = diagnosticcode.GenerationProviderExtensionMissing
	diagnosticGenerationActivationCycle          = diagnosticcode.GenerationActivationCycle
	diagnosticGenerationDependencyCycle          = diagnosticcode.GenerationDependencyCycle
	diagnosticGenerationContributionCycle        = diagnosticcode.GenerationContributionCycle
	diagnosticGenerationContributionsUnordered   = diagnosticcode.GenerationContributionsUnordered
	diagnosticGenerationStateRepeated            = diagnosticcode.GenerationStateRepeated
	diagnosticGenerationNonconvergent            = diagnosticcode.GenerationNonconvergent
	diagnosticGenerationAPIUnsupported           = diagnosticcode.GenerationAPIUnsupported
	diagnosticGenerationPackageInvalid           = diagnosticcode.GenerationPackageInvalid
	diagnosticGenerationCompileFailed            = diagnosticcode.GenerationCompileFailed
	diagnosticGenerationExecutionFailed          = diagnosticcode.GenerationExecutionFailed
	diagnosticGenerationExtensionFailed          = diagnosticcode.GenerationExtensionFailed
	diagnosticGenerationCrashed                  = diagnosticcode.GenerationCrashed
	diagnosticGenerationTimeout                  = diagnosticcode.GenerationTimeout
	diagnosticGenerationRequestTooLarge          = diagnosticcode.GenerationRequestTooLarge
	diagnosticGenerationOutputTooLarge           = diagnosticcode.GenerationOutputTooLarge
	diagnosticGenerationOutputMalformed          = diagnosticcode.GenerationOutputMalformed
	diagnosticGenerationOutputInvalid            = diagnosticcode.GenerationOutputInvalid
	diagnosticGenerationExtensionDiagnostic      = diagnosticcode.GenerationExtensionDiagnostic
	diagnosticAliasConflict                      = diagnosticcode.AliasConflict
	diagnosticAliasApplicationInvalid            = diagnosticcode.AliasApplicationInvalid
	diagnosticAliasExtensionOutputInvalid        = diagnosticcode.AliasExtensionOutputInvalid
	diagnosticAliasResolutionFailed              = diagnosticcode.AliasResolutionFailed
	diagnosticProtobufWireHistoryInvalid         = diagnosticcode.ProtobufWireHistoryInvalid
	diagnosticProtobufIdentityCollision          = diagnosticcode.ProtobufIdentityCollision
	diagnosticProtobufOperationKindUnsupported   = diagnosticcode.ProtobufOperationKindUnsupported
	diagnosticGeneratedOwnershipConflict         = diagnosticcode.GeneratedOwnershipConflict
	diagnosticGeneratedUnexpectedOutput          = diagnosticcode.GeneratedUnexpectedOutput
	diagnosticGeneratedManifestInvalid           = diagnosticcode.GeneratedManifestInvalid
	diagnosticCapabilityManifestInvalid          = diagnosticcode.CapabilityManifestInvalid
	diagnosticProjectConcurrentChange            = diagnosticcode.ProjectConcurrentChange
	diagnosticConfigurationCompositionDrift      = diagnosticcode.ConfigurationCompositionDrift
	diagnosticGeneratedDrift                     = diagnosticcode.GeneratedDrift
	diagnosticResolveUnknownInterface            = diagnosticcode.ResolveUnknownInterface
	diagnosticResolveUnknownImplementation       = diagnosticcode.ResolveUnknownImplementation
	diagnosticResolveIncompatibleImplementation  = diagnosticcode.ResolveIncompatibleImplementation
	diagnosticResolveMultipleImplementations     = diagnosticcode.ResolveMultipleImplementations
	diagnosticResolveMissingImplementation       = diagnosticcode.ResolveMissingImplementation
	diagnosticResolveConstructorCycle            = diagnosticcode.ResolveConstructorCycle
	diagnosticResolveReservedInterface           = diagnosticcode.ResolveReservedInterface
	diagnosticResolveIntrinsicInterfaceSelection = diagnosticcode.ResolveIntrinsicInterfaceSelection
	diagnosticImplementationDeclarationInvalid   = diagnosticcode.ImplementationDeclarationInvalid
	diagnosticImplementationConfigInvalid        = diagnosticcode.ImplementationConfigInvalid
	diagnosticImplementationRequiredInvalid      = diagnosticcode.ImplementationRequiredInvalid
	diagnosticImplementationOptionalInvalid      = diagnosticcode.ImplementationOptionalInvalid
	diagnosticImplementationResultInvalid        = diagnosticcode.ImplementationResultInvalid
	diagnosticImplementationConformanceInvalid   = diagnosticcode.ImplementationConformanceInvalid
	diagnosticInterfaceDeclarationInvalid        = diagnosticcode.InterfaceDeclarationInvalid
	diagnosticInterfaceContractInvalid           = diagnosticcode.InterfaceContractInvalid
	diagnosticInterfaceMetadataInvalid           = diagnosticcode.InterfaceMetadataInvalid
	diagnosticInterfaceIDDuplicate               = diagnosticcode.InterfaceIDDuplicate
	diagnosticAuthoredPackageInvalid             = diagnosticcode.AuthoredPackageInvalid
)

const (
	diagnosticProjectCreateNameInvalid              = diagnosticcode.ProjectCreateNameInvalid
	diagnosticProjectCreateModuleInvalid            = diagnosticcode.ProjectCreateModuleInvalid
	diagnosticProjectCreateTemplateInvalid          = diagnosticcode.ProjectCreateTemplateInvalid
	diagnosticProjectCreatePluginNameInvalid        = diagnosticcode.ProjectCreatePluginNameInvalid
	diagnosticProjectCreatePluginIDInvalid          = diagnosticcode.ProjectCreatePluginIDInvalid
	diagnosticProjectCreateTargetExists             = diagnosticcode.ProjectCreateTargetExists
	diagnosticProjectCreateGitInitializationFailed  = diagnosticcode.ProjectCreateGitInitializationFailed
	diagnosticProjectCreateChoiceRequired           = diagnosticcode.ProjectCreateChoiceRequired
	diagnosticPluginCreateNameInvalid               = diagnosticcode.PluginCreateNameInvalid
	diagnosticPluginCreateIDInvalid                 = diagnosticcode.PluginCreateIDInvalid
	diagnosticPluginCreateTargetExists              = diagnosticcode.PluginCreateTargetExists
	diagnosticInterfaceCreateNameInvalid            = diagnosticcode.InterfaceCreateNameInvalid
	diagnosticInterfaceCreateTargetExists           = diagnosticcode.InterfaceCreateTargetExists
	diagnosticImplementationCreateInterfaceInvalid  = diagnosticcode.ImplementationCreateInterfaceInvalid
	diagnosticImplementationCreatePackageInvalid    = diagnosticcode.ImplementationCreatePackageInvalid
	diagnosticImplementationCreateInterfaceNotFound = diagnosticcode.ImplementationCreateInterfaceNotFound
	diagnosticImplementationCreateTargetExists      = diagnosticcode.ImplementationCreateTargetExists
)

const (
	diagnosticUseInterfaceInvalid   = diagnosticcode.UseInterfaceInvalid
	diagnosticUseConstructorInvalid = diagnosticcode.UseConstructorInvalid
)

const (
	diagnosticDependencyAddQueryInvalid    = diagnosticcode.DependencyAddQueryInvalid
	diagnosticDependencyRemovePathInvalid  = diagnosticcode.DependencyRemovePathInvalid
	diagnosticDependencyRemoveNotSelected  = diagnosticcode.DependencyRemoveNotSelected
	diagnosticDependencyUpdateQueryInvalid = diagnosticcode.DependencyUpdateQueryInvalid
	diagnosticDependencyUpdateNotSelected  = diagnosticcode.DependencyUpdateNotSelected
)

const (
	diagnosticCapabilityCreateReferenceInvalid        = diagnosticcode.CapabilityCreateReferenceInvalid
	diagnosticCapabilityCreateAlreadyVisible          = diagnosticcode.CapabilityCreateAlreadyVisible
	diagnosticCapabilityCreateConfirmationRequired    = diagnosticcode.CapabilityCreateConfirmationRequired
	diagnosticCapabilityCreateVersionExhausted        = diagnosticcode.CapabilityCreateVersionExhausted
	diagnosticCapabilityCreateIntentProfileRequired   = diagnosticcode.CapabilityCreateIntentProfileRequired
	diagnosticCapabilityCreateIntentProfileNotAllowed = diagnosticcode.CapabilityCreateIntentProfileNotAllowed
	diagnosticCapabilityImplementReferenceInvalid     = diagnosticcode.CapabilityImplementReferenceInvalid
	diagnosticCapabilityImplementNotVisible           = diagnosticcode.CapabilityImplementNotVisible
	diagnosticCapabilityExposeReferenceInvalid        = diagnosticcode.CapabilityExposeReferenceInvalid
	diagnosticCapabilityExposeNotVisible              = diagnosticcode.CapabilityExposeNotVisible
)

const (
	diagnosticConstructorConfigurationSchemaInvalid = diagnosticcode.ConstructorConfigurationSchemaInvalid
	diagnosticConstructorConfigurationValuesInvalid = diagnosticcode.ConstructorConfigurationValuesInvalid
	diagnosticConstructorConfigurationUnselected    = diagnosticcode.ConstructorConfigurationUnselected
)

func commandRecoveryContext(configurationPath, environmentName string, environment []string) recoveryContext {
	return recoveryContext{
		configurationPath: configurationPath,
		environmentName:   environmentName,
		environment:       append([]string(nil), environment...),
	}
}

func rejectConflictingConfigurationSelectors(writer io.Writer, configurationPath, environmentName string) bool {
	if configurationPath == "" || environmentName == "" {
		return false
	}
	err := fmt.Errorf("%w: --config and --env cannot be used together", applicationresolve.ErrConfigurationSelection)
	writeCommandFailure(writer, "", err, commandRecoveryContext(configurationPath, environmentName, nil))
	return true
}

func writeCommandFailure(writer io.Writer, prefix string, err error, context recoveryContext) {
	if err == nil {
		return
	}
	diagnostic, actionable := primaryActionableDiagnostic(err, context)
	message := err.Error()
	if actionable {
		message = primaryFailureMessage(err)
	}
	if prefix == "" {
		_, _ = fmt.Fprintln(writer, message)
	} else {
		_, _ = fmt.Fprintf(writer, "%s: %s\n", prefix, message)
	}
	if actionable {
		for index, source := range actionableDiagnosticSources(err, diagnostic.code) {
			if index == 0 {
				_, _ = fmt.Fprintln(writer)
			}
			_, _ = fmt.Fprintf(writer, "Source: %s\n", explainSourceSummary(source))
		}
		_, _ = fmt.Fprintf(writer, "\nRecovery:\n%s\n\nDiagnostic: %s\n", diagnostic.recovery, diagnostic.code)
	}
}

func actionableDiagnosticSources(err error, code string) []diagnosticjson.Source {
	var sources []diagnosticjson.Source
	switch code {
	case diagnosticCapabilityRequirementConflict:
		var conflict *providerresolution.RequirementConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, variant := range conflict.Variants() {
			for _, source := range variant.RequirementSources() {
				sources = append(sources, diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   string(source.Kind),
					Line:   source.Line,
					Column: source.Column,
				})
			}
		}
	case diagnosticCapabilityContractConflict:
		var conflict *applicationinput.ContractConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, variant := range conflict.Variants() {
			for _, source := range variant.ProviderSources() {
				sources = append(sources, diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   "provider-declaration",
					Line:   source.Line,
					Column: source.Column,
				})
			}
		}
	case diagnosticCapabilitySchemaConflict:
		var conflict *capabilitycreate.SchemaConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, source := range []providerresolution.ProviderSource{
			conflict.BaselineDeclarationSource(),
			conflict.ConflictingDeclarationSource(),
		} {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "provider-declaration",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticProjectConcurrentChange:
		sources = append(sources, concurrentDiagnosticSources(err)...)
	case diagnosticConfigurationInheritedConflict:
		var conflict *applicationmeta.InheritedConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, source := range conflict.Sources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath(),
				Path:   source.Path(),
				Kind:   "configuration-declaration",
				Line:   source.Line(),
				Column: source.Column(),
			})
		}
	case diagnosticConfigurationOwnershipAmbiguous:
		var conflict *applicationmeta.AmbiguousConfigurationOwnershipError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, source := range conflict.Sources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath(),
				Path:   source.Path(),
				Kind:   "configuration-declaration",
				Line:   source.Line(),
				Column: source.Column(),
			})
		}
	case diagnosticHTTPTransportSelectionInvalid:
		var invalid *applicationmeta.HTTPTransportSelectionError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		seen := make(map[diagnosticjson.Source]struct{})
		for _, exposure := range invalid.Exposures() {
			source := exposure.DeclarationSource()
			candidate := diagnosticjson.Source{
				Module: source.ModulePath(),
				Path:   source.Path(),
				Kind:   "exposure",
				Line:   source.Line(),
				Column: source.Column(),
			}
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			sources = append(sources, candidate)
		}
	case diagnosticEnvironmentOverlayInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "configuration-declaration" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticConfigurationSelectionInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "configuration-selection" || located.Line() != 0 || located.Column() != 0 {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
		})
	case diagnosticConfigurationInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "configuration-declaration" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticApplicationDependencyDrift, diagnosticGoModuleInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "module-dependency" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticPluginTargetAmbiguous:
		var ambiguous *plugintarget.AmbiguousError
		if !errors.As(err, &ambiguous) || ambiguous == nil {
			return nil
		}
		for _, candidate := range ambiguous.Candidates() {
			sources = append(sources, diagnosticjson.Source{
				Module: candidate.ModulePath(),
				Path:   candidate.SourcePath(),
				Kind:   "plugin-declaration",
				Line:   candidate.Line(),
				Column: candidate.Column(),
			})
		}
	case diagnosticGenerationActivationConflict:
		var conflict *generationactivation.AssociationConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		seen := make(map[diagnosticjson.Source]struct{})
		for _, candidate := range conflict.Candidates() {
			source := diagnosticjson.Source{
				Module: candidate.ModulePath(),
				Path:   candidate.SourcePath(),
				Kind:   "plugin-declaration",
				Line:   candidate.Line(),
				Column: candidate.Column(),
			}
			if _, exists := seen[source]; exists {
				continue
			}
			seen[source] = struct{}{}
			sources = append(sources, source)
		}
	case diagnosticGenerationActivationMissing:
		var missing *generationactivation.MissingAssociationError
		if !errors.As(err, &missing) || missing == nil {
			return nil
		}
		seen := make(map[diagnosticjson.Source]struct{})
		for _, use := range missing.Uses() {
			for _, source := range use.RequirementSources() {
				candidate := diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   string(source.Kind),
					Line:   source.Line,
					Column: source.Column,
				}
				if _, exists := seen[candidate]; exists {
					continue
				}
				seen[candidate] = struct{}{}
				sources = append(sources, candidate)
			}
		}
	case diagnosticGenerationProviderExtensionMissing:
		var failures []*generationresolution.SelectedProviderExtensionError
		var closure *generationresolution.ClosureError
		if errors.As(err, &closure) && closure != nil {
			for _, issue := range closure.Issues() {
				var missing *generationresolution.SelectedProviderExtensionError
				if errors.As(issue, &missing) && missing != nil {
					failures = append(failures, missing)
				}
			}
		}
		if len(failures) == 0 {
			var missing *generationresolution.SelectedProviderExtensionError
			if !errors.As(err, &missing) || missing == nil {
				return nil
			}
			failures = append(failures, missing)
		}
		seen := make(map[diagnosticjson.Source]struct{})
		appendSource := func(source diagnosticjson.Source) {
			if _, exists := seen[source]; exists {
				return
			}
			seen[source] = struct{}{}
			sources = append(sources, source)
		}
		for _, missing := range failures {
			if source, available := missing.ProviderDeclarationSource(); available {
				appendSource(diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   "provider-declaration",
					Line:   source.Line,
					Column: source.Column,
				})
			}
			for _, source := range missing.ChoiceSources() {
				appendSource(diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   "provider-selection",
					Line:   source.Line,
					Column: source.Column,
				})
			}
		}
	case diagnosticGenerationActivationCycle:
		var cycle *generationresolution.ActivationCycleError
		if !errors.As(err, &cycle) || cycle == nil {
			return nil
		}
		seen := make(map[diagnosticjson.Source]struct{})
		for _, edge := range cycle.Edges() {
			for _, source := range edge.RequirementSourceDetails() {
				candidate := diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   string(source.Kind),
					Line:   source.Line,
					Column: source.Column,
				}
				if _, exists := seen[candidate]; exists {
					continue
				}
				seen[candidate] = struct{}{}
				sources = append(sources, candidate)
			}
		}
	case diagnosticGenerationDependencyCycle:
		var cycle *generationresolution.DependencyCycleError
		if !errors.As(err, &cycle) || cycle == nil {
			return nil
		}
		seen := make(map[diagnosticjson.Source]struct{})
		for _, edge := range cycle.Edges() {
			for _, source := range edge.RequirementSourceDetails() {
				candidate := diagnosticjson.Source{
					Module: source.ModulePath,
					Path:   source.Path,
					Kind:   string(source.Kind),
					Line:   source.Line,
					Column: source.Column,
				}
				if _, exists := seen[candidate]; exists {
					continue
				}
				seen[candidate] = struct{}{}
				sources = append(sources, candidate)
			}
		}
	case diagnosticGeneratedOwnershipConflict:
		var conflict *applicationgenerate.OwnershipConflictSourceError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: conflict.ModulePath(),
			Path:   conflict.SourcePath(),
			Kind:   conflict.SourceKind(),
		})
	case diagnosticGeneratedUnexpectedOutput:
		var unexpected *applicationgenerate.UnexpectedOutputSourceError
		if !errors.As(err, &unexpected) || unexpected == nil {
			return nil
		}
		for _, sourcePath := range unexpected.Paths() {
			sources = append(sources, diagnosticjson.Source{
				Module: unexpected.ModulePath(),
				Path:   sourcePath,
				Kind:   unexpected.SourceKind(),
			})
		}
	case diagnosticGeneratedManifestInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "generated-artifact" || located.SourcePath() != generatedfiles.ManifestPath {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
		})
	case diagnosticProtobufWireHistoryInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "generated-artifact" || located.SourcePath() != protobufwiremap.Path {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
		})
	case diagnosticProtobufIdentityCollision:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "interface-contract" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticProtobufOperationKindUnsupported:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "exposure" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticCapabilityManifestInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "provider-declaration" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticConstructorConfigurationUnselected:
		var unowned *applicationresolve.UnownedConstructorConfigurationError
		if !errors.As(err, &unowned) || unowned == nil {
			return nil
		}
		for _, source := range unowned.Sources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "configuration-declaration",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticConstructorConfigurationSchemaInvalid, diagnosticConstructorConfigurationValuesInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "configuration-declaration" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticProjectManifestInvalid:
		var located diagnosticSourceLocation
		if !errors.As(err, &located) || located == nil || located.SourceKind() != "project-marker" {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticProviderSelectionInvalid:
		var invalid *providerresolution.ChoiceError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		for _, source := range invalid.ChoiceSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "provider-selection",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticProviderContractConflict:
		var conflict *providerresolution.ProviderContractConflictError
		if !errors.As(err, &conflict) || conflict == nil {
			return nil
		}
		for _, source := range conflict.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
		for _, provider := range conflict.Providers() {
			source, available := provider.DeclarationSource()
			if !available {
				continue
			}
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "provider-declaration",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticProviderContractMismatch:
		var mismatch *providerresolution.ProviderContractError
		if !errors.As(err, &mismatch) || mismatch == nil {
			return nil
		}
		for _, source := range mismatch.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
		for _, provider := range mismatch.Providers() {
			source, available := provider.DeclarationSource()
			if !available {
				continue
			}
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "provider-declaration",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticProviderAmbiguous:
		var ambiguous *providerresolution.AmbiguousProviderError
		if !errors.As(err, &ambiguous) || ambiguous == nil {
			return nil
		}
		for _, source := range ambiguous.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
		for _, provider := range ambiguous.Providers() {
			source, available := provider.DeclarationSource()
			if !available {
				continue
			}
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "provider-declaration",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticProviderMissing:
		var missing *providerresolution.MissingProviderError
		if !errors.As(err, &missing) || missing == nil {
			return nil
		}
		for _, source := range missing.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticResolveUnknownInterface:
		var missing *interfaceresolution.UnknownInterfaceError
		if !errors.As(err, &missing) || missing == nil {
			return nil
		}
		for _, source := range missing.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
		for _, source := range missing.ChoiceSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "implementation-selection",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticResolveReservedInterface:
		var reserved *interfaceresolution.ReservedInterfaceError
		if !errors.As(err, &reserved) || reserved == nil {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: reserved.ModulePath(),
			Path:   reserved.SourcePath(),
			Kind:   "interface-declaration",
			Line:   reserved.Line(),
			Column: reserved.Column(),
		})
	case diagnosticResolveMissingImplementation:
		var missing *constructorgraph.MissingBindingError
		if !errors.As(err, &missing) || missing == nil {
			return nil
		}
		for _, source := range missing.RequirementSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   string(source.Kind),
				Line:   source.Line,
				Column: source.Column,
			})
		}
		for _, step := range missing.Steps() {
			sources = append(sources, diagnosticjson.Source{
				Module: step.RequiringModulePath(),
				Path:   step.RequiringSourcePath(),
				Kind:   "implementation-constructor",
				Line:   step.RequiringLine(),
				Column: step.RequiringColumn(),
			})
		}
	case diagnosticResolveConstructorCycle:
		var cycle *constructorgraph.CycleError
		if !errors.As(err, &cycle) || cycle == nil {
			return nil
		}
		for _, step := range cycle.Steps() {
			sources = append(sources, diagnosticjson.Source{
				Module: step.RequiringModulePath(),
				Path:   step.RequiringSourcePath(),
				Kind:   "implementation-constructor",
				Line:   step.RequiringLine(),
				Column: step.RequiringColumn(),
			})
		}
	case diagnosticResolveUnknownImplementation:
		var invalid *interfaceresolution.UnknownConstructorError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		for _, source := range invalid.ChoiceSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "implementation-selection",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticResolveIncompatibleImplementation:
		var invalid *interfaceresolution.IncompatibleChoiceError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		for _, source := range invalid.ChoiceSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "implementation-selection",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticResolveIntrinsicInterfaceSelection:
		var invalid *interfaceresolution.IntrinsicChoiceError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		for _, source := range invalid.ChoiceSources() {
			sources = append(sources, diagnosticjson.Source{
				Module: source.ModulePath,
				Path:   source.Path,
				Kind:   "implementation-selection",
				Line:   source.Line,
				Column: source.Column,
			})
		}
	case diagnosticResolveMultipleImplementations:
		var ambiguous *interfaceresolution.AmbiguousImplementationError
		if !errors.As(err, &ambiguous) || ambiguous == nil {
			return nil
		}
		for _, candidate := range ambiguous.Candidates() {
			sources = append(sources, diagnosticjson.Source{
				Module: candidate.ModulePath(),
				Path:   candidate.SourcePath(),
				Kind:   "implementation-constructor",
				Line:   candidate.Line(),
				Column: candidate.Column(),
			})
		}
	case diagnosticImplementationDeclarationInvalid,
		diagnosticInterfaceDeclarationInvalid,
		diagnosticInterfaceContractInvalid,
		diagnosticInterfaceMetadataInvalid,
		diagnosticAuthoredPackageInvalid:
		expectedKind := map[string]string{
			diagnosticImplementationDeclarationInvalid: "implementation-declaration",
			diagnosticInterfaceDeclarationInvalid:      "interface-declaration",
			diagnosticInterfaceContractInvalid:         "interface-contract",
			diagnosticInterfaceMetadataInvalid:         "interface-metadata",
			diagnosticAuthoredPackageInvalid:           "authored-package",
		}[code]
		var located *interfaceinventory.SourceError
		if !errors.As(err, &located) || located == nil || located.SourceKind() != expectedKind {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: located.ModulePath(),
			Path:   located.SourcePath(),
			Kind:   located.SourceKind(),
			Line:   located.Line(),
			Column: located.Column(),
		})
	case diagnosticImplementationConfigInvalid,
		diagnosticImplementationRequiredInvalid,
		diagnosticImplementationOptionalInvalid,
		diagnosticImplementationResultInvalid,
		diagnosticImplementationConformanceInvalid:
		var invalid *implementationinventory.ValidationError
		if !errors.As(err, &invalid) || invalid == nil {
			return nil
		}
		sources = append(sources, diagnosticjson.Source{
			Module: invalid.ModulePath(),
			Path:   invalid.SourcePath(),
			Kind:   "implementation-constructor",
			Line:   invalid.Line(),
			Column: invalid.Column(),
		})
	case diagnosticInterfaceIDDuplicate:
		var duplicate *interfaceinventory.DuplicateIDError
		if !errors.As(err, &duplicate) || duplicate == nil {
			return nil
		}
		for _, definition := range duplicate.Definitions() {
			position := definition.Declaration().Position()
			sources = append(sources, diagnosticjson.Source{
				Module: definition.ModulePath(),
				Path:   definition.SourcePath(),
				Kind:   "interface-declaration",
				Line:   position.Line,
				Column: position.Column,
			})
		}
	default:
		return nil
	}
	canonical, canonicalErr := diagnosticjson.CanonicalizeSources(sources)
	if canonicalErr != nil {
		return nil
	}
	return canonical
}

func concurrentDiagnosticSources(err error) []diagnosticjson.Source {
	var sources []diagnosticjson.Source
	seen := make(map[diagnosticjson.Source]struct{})
	appendSource := func(source diagnosticjson.Source) {
		if _, exists := seen[source]; exists {
			return
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		if concurrent, ok := current.(*applicationgenerate.ConcurrentChangeSourceError); ok {
			for _, source := range concurrent.Sources() {
				appendSource(diagnosticjson.Source{
					Module: source.ModulePath(),
					Path:   source.SourcePath(),
					Kind:   source.SourceKind(),
					Line:   source.Line(),
					Column: source.Column(),
				})
			}
		}
		if located, ok := current.(diagnosticSourceLocation); ok && concurrentFailure(current) {
			appendSource(diagnosticjson.Source{
				Module: located.ModulePath(),
				Path:   located.SourcePath(),
				Kind:   located.SourceKind(),
				Line:   located.Line(),
				Column: located.Column(),
			})
		}
		switch current := current.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range current.Unwrap() {
				walk(child)
			}
		case interface{ Unwrap() error }:
			walk(current.Unwrap())
		}
	}
	walk(err)
	return sources
}

func concurrentFailure(err error) bool {
	return errors.Is(err, atomicfs.ErrConcurrentChange) ||
		errors.Is(err, moduledependency.ErrConcurrentChange) ||
		errors.Is(err, applicationresolve.ErrConcurrentChange) ||
		errors.Is(err, applicationgenerate.ErrConcurrentChange)
}

func primaryFailureMessage(err error) string {
	if errors.Is(err, newproject.ErrInvalidTemplate) {
		if problem, _, found := splitEmbeddedRecovery(err.Error()); found {
			return problem
		}
		return err.Error()
	}
	var requirementConflict *providerresolution.RequirementConflictError
	if errors.As(err, &requirementConflict) && requirementConflict != nil {
		return trimEmbeddedRecovery(requirementConflict.Error())
	}
	var providerContractConflict *providerresolution.ProviderContractConflictError
	if errors.As(err, &providerContractConflict) && providerContractConflict != nil {
		return trimEmbeddedRecovery(providerContractConflict.Error())
	}
	var providerContract *providerresolution.ProviderContractError
	if errors.As(err, &providerContract) && providerContract != nil {
		return trimEmbeddedRecovery(providerContract.Error())
	}
	var visibleContractConflict *applicationinput.ContractConflictError
	if errors.As(err, &visibleContractConflict) && visibleContractConflict != nil {
		return trimEmbeddedRecovery(visibleContractConflict.Error())
	}
	var authoredContractConflict *capabilitycreate.SchemaConflictError
	if errors.As(err, &authoredContractConflict) && authoredContractConflict != nil {
		return trimEmbeddedRecovery(authoredContractConflict.Error())
	}
	var invalidChoice *providerresolution.ChoiceError
	if errors.As(err, &invalidChoice) && invalidChoice != nil {
		return invalidChoice.Error()
	}
	var missingProvider *providerresolution.MissingProviderError
	if errors.As(err, &missingProvider) && missingProvider != nil {
		return trimEmbeddedRecovery(missingProvider.Error())
	}
	var ambiguousProvider *providerresolution.AmbiguousProviderError
	if errors.As(err, &ambiguousProvider) && ambiguousProvider != nil {
		return trimEmbeddedRecovery(ambiguousProvider.Error())
	}
	if errors.Is(err, applicationgenerate.ErrKernelDependency) || errors.Is(err, applicationgenerate.ErrRuntimeDependency) {
		if problem, _, found := strings.Cut(err.Error(), "; run plystra generate"); found {
			return strings.TrimSpace(problem)
		}
	}
	return trimEmbeddedRecovery(err.Error())
}

func primaryActionableDiagnostic(err error, context recoveryContext) (actionableDiagnostic, bool) {
	if concurrentFailure(err) {
		return recoveryDiagnostic(diagnosticProjectConcurrentChange, "Stop concurrent Project edits, then rerun the command against the unchanged authored inputs.")
	}
	if errors.Is(err, errNewChoiceRequired) {
		return recoveryDiagnostic(diagnosticProjectCreateChoiceRequired, "Rerun `plystra new <project-name> [options]` with exactly one of `--git` or `--no-git`, one of `--github-ci` or `--no-github-ci`, and one of `--skills` or `--no-skills`.")
	}
	if errors.Is(err, newproject.ErrInvalidProjectName) {
		return recoveryDiagnostic(diagnosticProjectCreateNameInvalid, "Rerun `plystra new <project-name> [options]` with one lower-case ASCII kebab-case child directory name; put any independent Go Module identity in `--module <go-module-path>`.")
	}
	if errors.Is(err, newproject.ErrInvalidModulePath) {
		return recoveryDiagnostic(diagnosticProjectCreateModuleInvalid, "Rerun `plystra new <project-name> --module <go-module-path> [options]` with one valid Go Module path and every required choice flag.")
	}
	if errors.Is(err, newproject.ErrInvalidTemplateQuery) {
		return recoveryDiagnostic(diagnosticProjectCreateTemplateInvalid, "Rerun `plystra new <project-name> --template <go-module-query> [options]` with one valid non-removal Go Module query and every required choice flag.")
	}
	if errors.Is(err, newproject.ErrInvalidPluginName) {
		return recoveryDiagnostic(diagnosticProjectCreatePluginNameInvalid, "Rerun `plystra new <project-name> --plugin <plugin-name> [options]` with one lower-case ASCII kebab-case initial Plugin name that is not reserved and every required choice flag.")
	}
	if errors.Is(err, newproject.ErrInvalidPluginID) {
		return recoveryDiagnostic(diagnosticProjectCreatePluginIDInvalid, "Rerun `plystra new <project-name> --module <go-module-path> --plugin <plugin-name> [options]` with values that derive one canonical Plugin ID and every required choice flag.")
	}
	if errors.Is(err, newproject.ErrTargetExists) {
		return recoveryDiagnostic(diagnosticProjectCreateTargetExists, "Rerun `plystra new <project-name> [options]` with a different canonical Project name whose target does not exist, or run it from a different parent directory.")
	}
	if errors.Is(err, newproject.ErrGitInitialization) {
		return recoveryDiagnostic(diagnosticProjectCreateGitInitializationFailed, "Correct the reported Git installation or initialization failure, then rerun `plystra new <project-name> [options]` with `--git`; use `--no-git` only when the Project intentionally needs no repository.")
	}
	if errors.Is(err, newproject.ErrInvalidTemplate) {
		if _, action, found := splitEmbeddedRecovery(err.Error()); found {
			return recoveryDiagnostic(diagnosticTemplateInvalid, action)
		}
		return recoveryDiagnostic(diagnosticTemplateInvalid, "Use a corrected published template version whose clean Project passes generation, check, build, and lifecycle validation.")
	}
	var requirementConflict *providerresolution.RequirementConflictError
	if errors.As(err, &requirementConflict) && requirementConflict != nil {
		return recoveryDiagnostic(diagnosticCapabilityRequirementConflict, identicalContractRecovery(requirementConflict.Capability().String()))
	}
	var providerContractConflict *providerresolution.ProviderContractConflictError
	if errors.As(err, &providerContractConflict) && providerContractConflict != nil {
		return recoveryDiagnostic(diagnosticProviderContractConflict, identicalContractRecovery(providerContractConflict.Capability().String()))
	}
	var providerContract *providerresolution.ProviderContractError
	if errors.As(err, &providerContract) && providerContract != nil {
		return recoveryDiagnostic(diagnosticProviderContractMismatch, identicalContractRecovery(providerContract.Capability().String()))
	}
	var visibleContractConflict *applicationinput.ContractConflictError
	if errors.As(err, &visibleContractConflict) && visibleContractConflict != nil {
		return recoveryDiagnostic(diagnosticCapabilityContractConflict, identicalContractRecovery(visibleContractConflict.ID().String()))
	}
	var authoredContractConflict *capabilitycreate.SchemaConflictError
	if errors.As(err, &authoredContractConflict) && authoredContractConflict != nil {
		return recoveryDiagnostic(diagnosticCapabilitySchemaConflict, identicalContractRecovery(authoredContractConflict.Capability().String()))
	}
	var invalidChoice *providerresolution.ChoiceError
	if errors.As(err, &invalidChoice) && invalidChoice != nil {
		command := "plystra use " + invalidChoice.Capability().String() + " <plugin-id>" + context.selectorSuffix()
		return recoveryDiagnostic(diagnosticProviderSelectionInvalid, "Replace the invalid Provider choice with one visible compatible Plugin by running `"+command+"`.")
	}
	var missingProvider *providerresolution.MissingProviderError
	if errors.As(err, &missingProvider) && missingProvider != nil {
		return recoveryDiagnostic(diagnosticProviderMissing, "Add an intended dependency with `plystra add <go-module-query>` whose Plugin provides "+missingProvider.Capability().String()+".")
	}
	var ambiguousProvider *providerresolution.AmbiguousProviderError
	if errors.As(err, &ambiguousProvider) && ambiguousProvider != nil {
		command := "plystra use " + ambiguousProvider.Capability().String() + " <plugin-id>" + context.selectorSuffix()
		return recoveryDiagnostic(diagnosticProviderAmbiguous, "Select one compatible Provider explicitly by running `"+command+"`.")
	}
	var ambiguousImplementation *interfaceresolution.AmbiguousImplementationError
	if errors.As(err, &ambiguousImplementation) && ambiguousImplementation != nil {
		command := "plystra use " + ambiguousImplementation.InterfaceID().String() + " <constructor-symbol>" + context.selectorSuffix()
		return recoveryDiagnostic(diagnosticResolveMultipleImplementations, "Select one compatible Implementation by running `"+command+"`.")
	}
	var missingImplementation *constructorgraph.MissingBindingError
	if errors.As(err, &missingImplementation) && missingImplementation != nil {
		command := "plystra implement " + missingImplementation.InterfaceID().String() + " --package <project-relative-package>"
		return recoveryDiagnostic(diagnosticResolveMissingImplementation, "Create one compatible local Implementation by running `"+command+"`.")
	}

	switch {
	case errors.Is(err, capabilityexpose.ErrExpose) && !errors.Is(err, capabilitycreate.ErrCreate) && !errors.Is(err, capabilitycreate.ErrImplement) && !errors.Is(err, capabilityexpose.ErrInvalidReference) && errors.Is(err, capabilityexpose.ErrNotVisible) && errors.Is(err, interfaceresolution.ErrUnknownInterface):
		return recoveryDiagnostic(diagnosticCapabilityExposeNotVisible, "Rerun `plystra capability expose <capability-name>/vN"+context.selectorSuffix()+"` with one exact Capability visible in the selected Go Module graph.")
	case errors.Is(err, interfaceresolution.ErrUnknownInterface):
		return recoveryDiagnostic(diagnosticResolveUnknownInterface, "Correct the reported Interface ID in "+context.configurationTarget()+" to one canonical Interface visible in the selected Go Module graph, then rerun the command.")
	case errors.Is(err, interfaceresolution.ErrUnknownConstructor):
		return recoveryDiagnostic(diagnosticResolveUnknownImplementation, implementationSelectionRecovery(context))
	case errors.Is(err, interfaceresolution.ErrIncompatibleChoice):
		return recoveryDiagnostic(diagnosticResolveIncompatibleImplementation, implementationSelectionRecovery(context))
	case errors.Is(err, interfaceresolution.ErrAmbiguousImplementation):
		return recoveryDiagnostic(diagnosticResolveMultipleImplementations, "Select one compatible Implementation by running `plystra use <interface-id> <constructor-symbol>"+context.selectorSuffix()+"`.")
	case errors.Is(err, constructorgraph.ErrMissingBinding):
		return recoveryDiagnostic(diagnosticResolveMissingImplementation, "Create one compatible local Implementation by running `plystra implement <interface-id> --package <project-relative-package>`.")
	case errors.Is(err, constructorgraph.ErrCycle):
		return recoveryDiagnostic(diagnosticResolveConstructorCycle, "Remove one required Interface parameter from the reported constructor cycle, then rerun the command.")
	case errors.Is(err, interfaceresolution.ErrReservedInterface):
		return recoveryDiagnostic(diagnosticResolveReservedInterface, "Remove the reported local kernel.* Interface declaration and import the canonical Kernel Interface package instead.")
	case errors.Is(err, interfaceresolution.ErrIntrinsicChoice):
		return recoveryDiagnostic(diagnosticResolveIntrinsicInterfaceSelection, "Set the reported interfaces.use entry to null in "+context.configurationTarget()+" to remove the effective selection; Kernel supplies that Interface intrinsically.")
	case errors.Is(err, implementationdecl.ErrInvalid):
		return recoveryDiagnostic(diagnosticImplementationDeclarationInvalid, "Correct the reported //plystra:implements directive so it immediately documents one exported package-level constructor and names canonical Interface IDs, then rerun the command.")
	case errors.Is(err, implementationinventory.ErrInvalidConfiguration):
		return recoveryDiagnostic(diagnosticImplementationConfigInvalid, "Correct the reported constructor's first Config parameter and exported Config fields to use the supported typed configuration schema, then rerun the command.")
	case errors.Is(err, implementationinventory.ErrInvalidRequiredInterface):
		return recoveryDiagnostic(diagnosticImplementationRequiredInvalid, "Replace the reported required constructor parameter with one visible canonical Interface type, then rerun the command.")
	case errors.Is(err, implementationinventory.ErrInvalidOptionalInterface):
		return recoveryDiagnostic(diagnosticImplementationOptionalInvalid, "Replace the reported optional constructor parameter with the exact plystra.Optional[T] value type around one visible canonical Interface, then rerun the command.")
	case errors.Is(err, implementationinventory.ErrInvalidResult):
		return recoveryDiagnostic(diagnosticImplementationResultInvalid, "Change the reported constructor to return exactly one concrete value plus error, then rerun the command.")
	case errors.Is(err, implementationinventory.ErrInvalidConformance):
		return recoveryDiagnostic(diagnosticImplementationConformanceInvalid, "Implement every reported canonical Interface method on the constructor's concrete result type, then rerun the command.")
	case errors.Is(err, interfacedecl.ErrInvalid):
		return recoveryDiagnostic(diagnosticInterfaceDeclarationInvalid, "Correct the reported //plystra:interface directive so it immediately documents the exported defined type Interface and names one canonical Interface ID, then rerun the command.")
	case errors.Is(err, interfacecontract.ErrInvalid):
		return recoveryDiagnostic(diagnosticInterfaceContractInvalid, "Correct the reported Interface Go package to the canonical single-operation method, request, response, field, and error shape, then rerun the command.")
	case errors.Is(err, interfacemeta.ErrInvalid):
		return recoveryDiagnostic(diagnosticInterfaceMetadataInvalid, "Correct the reported module-relative interface.yaml field to match the closed Interface metadata schema, then rerun the command.")
	case errors.Is(err, interfaceinventory.ErrDuplicateID):
		return recoveryDiagnostic(diagnosticInterfaceIDDuplicate, "Make the reported visible Go packages declare distinct canonical Interface IDs, then rerun the command.")
	case errors.Is(err, interfaceinventory.ErrPackage):
		return recoveryDiagnostic(diagnosticAuthoredPackageInvalid, "Correct the reported authored Go package in its owning Project so ordinary Go tooling can load it, then rerun the command.")
	case errors.Is(err, plugincreate.ErrInvalidName):
		return recoveryDiagnostic(diagnosticPluginCreateNameInvalid, "Run `plystra plugin create <plugin-name>` with one lower-case ASCII kebab-case name that is not reserved at the Project root.")
	case errors.Is(err, plugincreate.ErrDeriveID):
		return recoveryDiagnostic(diagnosticPluginCreateIDInvalid, "Correct the current Project module path in go.mod or choose a shorter canonical Plugin name so their derived identity is valid, then rerun `plystra plugin create <plugin-name>`.")
	case errors.Is(err, plugincreate.ErrTargetExists):
		return recoveryDiagnostic(diagnosticPluginCreateTargetExists, "Rerun `plystra plugin create <plugin-name>` with a different canonical name whose root-level directory does not exist.")
	case errors.Is(err, interfacecreate.ErrInvalidName):
		return recoveryDiagnostic(diagnosticInterfaceCreateNameInvalid, "Run `plystra interface create <domain.operation>` with one unversioned canonical lower-case name containing at least two dot-separated segments.")
	case errors.Is(err, interfacecreate.ErrTargetExists):
		return recoveryDiagnostic(diagnosticInterfaceCreateTargetExists, "Choose a different unversioned Interface name whose v1 package and visible ID do not already exist.")
	case errors.Is(err, implementationcreate.ErrInvalidInterface):
		return recoveryDiagnostic(diagnosticImplementationCreateInterfaceInvalid, "Rerun `plystra implement <interface-name>/vN --package <project-relative-package>` with one canonical versioned Interface ID.")
	case errors.Is(err, implementationcreate.ErrInvalidPackage):
		return recoveryDiagnostic(diagnosticImplementationCreatePackageInvalid, "Rerun with `--package ./<safe-project-relative-go-package>` naming one canonical child package.")
	case errors.Is(err, implementationcreate.ErrInterfaceNotFound):
		return recoveryDiagnostic(diagnosticImplementationCreateInterfaceNotFound, "Replace the reported Interface ID with one canonical Interface visible in the effective Plystra Project graph, then rerun the command.")
	case errors.Is(err, implementationcreate.ErrTargetExists):
		return recoveryDiagnostic(diagnosticImplementationCreateTargetExists, "Rerun with a different `--package ./<project-relative-go-package>` whose target directory does not exist.")
	case errors.Is(err, dependencyadd.ErrAdd) && errors.Is(err, moduleargument.ErrInvalidQuery):
		return recoveryDiagnostic(diagnosticDependencyAddQueryInvalid, "Rerun `plystra add <go-module-query>` with one valid non-removal Go Module query.")
	case errors.Is(err, dependencyremove.ErrRemove) && errors.Is(err, moduleargument.ErrInvalidPath):
		return recoveryDiagnostic(diagnosticDependencyRemovePathInvalid, "Rerun `plystra remove <go-module-path>` with one valid Go Module path without a version query.")
	case errors.Is(err, dependencyremove.ErrRemove) && errors.Is(err, dependencyremove.ErrNotSelected):
		return recoveryDiagnostic(diagnosticDependencyRemoveNotSelected, "Rerun `plystra remove <go-module-path>` with one exact Go Module path already selected in go.mod.")
	case errors.Is(err, dependencyupdate.ErrUpdate) && errors.Is(err, moduleargument.ErrInvalidQuery):
		return recoveryDiagnostic(diagnosticDependencyUpdateQueryInvalid, "Rerun `plystra update <go-module-query>` with one valid non-removal Go Module query.")
	case errors.Is(err, dependencyupdate.ErrUpdate) && errors.Is(err, dependencyupdate.ErrNotSelected):
		return recoveryDiagnostic(diagnosticDependencyUpdateNotSelected, "Rerun `plystra update <go-module-query>` with one query whose module path is already selected in go.mod.")
	case errors.Is(err, capabilitycreate.ErrCreate) && errors.Is(err, capabilitycreate.ErrInvalidReference):
		return recoveryDiagnostic(diagnosticCapabilityCreateReferenceInvalid, "Rerun `plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]` with one canonical lower-case Capability name containing at least two dot-separated segments and an optional positive `/vN` major.")
	case errors.Is(err, capabilitycreate.ErrCreate) && errors.Is(err, capabilitycreate.ErrActionMismatch) && errors.Is(err, capabilitycreate.ErrCreateAlreadyVisible) && !errors.Is(err, capabilitycreate.ErrImplementNotVisible):
		return recoveryDiagnostic(diagnosticCapabilityCreateAlreadyVisible, "Rerun `plystra capability implement <capability-name>/vN [--plugin <plugin>]` for the existing exact contract.")
	case errors.Is(err, capabilitycreate.ErrCreate) && !errors.Is(err, capabilitycreate.ErrImplement) && errors.Is(err, capabilitycreate.ErrConfirmationRequired):
		return recoveryDiagnostic(diagnosticCapabilityCreateConfirmationRequired, "Review the visible Capability versions, then rerun the same `plystra capability create` command with `--confirm`.")
	case errors.Is(err, capabilitycreate.ErrCreate) && !errors.Is(err, capabilitycreate.ErrImplement) && errors.Is(err, capabilitycreate.ErrVersionExhausted) && errors.Is(err, capabilityversion.ErrOverflow):
		return recoveryDiagnostic(diagnosticCapabilityCreateVersionExhausted, "Rerun `plystra capability create <new-capability-name> --query [--plugin <plugin>] [--expose]` with a new canonical Capability identity; the existing identity has no higher major version.")
	case errors.Is(err, capabilitycreate.ErrCreate) && errors.Is(err, capabilitycreate.ErrIntentProfile) && errors.Is(err, capabilitycreate.ErrIntentProfileRequired) && !errors.Is(err, capabilitycreate.ErrIntentProfileNotAllowed):
		return recoveryDiagnostic(diagnosticCapabilityCreateIntentProfileRequired, "Rerun `plystra capability create <capability-name> --query [--plugin <plugin>] [--confirm] [--expose]` with the explicit query intent profile required for a new Capability identity.")
	case errors.Is(err, capabilitycreate.ErrCreate) && errors.Is(err, capabilitycreate.ErrIntentProfile) && errors.Is(err, capabilitycreate.ErrIntentProfileNotAllowed) && !errors.Is(err, capabilitycreate.ErrIntentProfileRequired):
		return recoveryDiagnostic(diagnosticCapabilityCreateIntentProfileNotAllowed, "Rerun `plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]` without `--query`; a later version copies the highest visible contract's semantics.")
	case errors.Is(err, capabilitycreate.ErrImplement) && errors.Is(err, capabilitycreate.ErrInvalidReference):
		return recoveryDiagnostic(diagnosticCapabilityImplementReferenceInvalid, "Rerun `plystra capability implement <capability-name>/vN [--plugin <plugin>]` with one canonical lower-case Capability ID containing at least two dot-separated segments and a positive major.")
	case errors.Is(err, capabilitycreate.ErrImplement) && errors.Is(err, capabilitycreate.ErrActionMismatch) && errors.Is(err, capabilitycreate.ErrImplementNotVisible) && !errors.Is(err, capabilitycreate.ErrCreateAlreadyVisible):
		return recoveryDiagnostic(diagnosticCapabilityImplementNotVisible, "Rerun `plystra capability create <capability-name>/vN [--query] [--plugin <plugin>] [--confirm] [--expose]` to author the missing exact contract.")
	case errors.Is(err, capabilityexpose.ErrExpose) && errors.Is(err, capabilityexpose.ErrInvalidReference):
		return recoveryDiagnostic(diagnosticCapabilityExposeReferenceInvalid, "Rerun `plystra capability expose <capability-name>/vN"+context.selectorSuffix()+"` with one canonical lower-case Capability ID containing at least two dot-separated segments and a positive major.")
	case errors.Is(err, implementationselect.ErrInvalidInterfaceID):
		return recoveryDiagnostic(diagnosticUseInterfaceInvalid, "Rerun `plystra use <interface-id> <constructor-symbol>"+context.selectorSuffix()+"` with one canonical versioned Interface ID.")
	case errors.Is(err, implementationselect.ErrInvalidConstructor):
		return recoveryDiagnostic(diagnosticUseConstructorInvalid, "Rerun `plystra use <interface-id> <constructor-symbol>"+context.selectorSuffix()+"` with one visible fully qualified exported constructor symbol.")
	case errors.Is(err, applicationresolve.ErrManifest) && !errors.Is(err, applicationresolve.ErrConfigurationSelection):
		return recoveryDiagnostic(diagnosticProjectManifestInvalid, "Correct the reported root or dependency Project plystra.yaml, then rerun the command.")
	case errors.Is(err, applicationmeta.ErrInheritedConflict):
		return recoveryDiagnostic(diagnosticConfigurationInheritedConflict, "Set or remove the conflicting field explicitly in "+context.configurationTarget()+", then rerun the command.")
	case errors.Is(err, applicationmeta.ErrAmbiguousConfigurationOwnership):
		return recoveryDiagnostic(diagnosticConfigurationOwnershipAmbiguous, "Make the inherited field intent explicit in "+context.configurationTarget()+" by restoring it or writing its typed removal.")
	case errors.Is(err, applicationmeta.ErrHTTPTransportSelection):
		return recoveryDiagnostic(diagnosticHTTPTransportSelectionInvalid, "Enable a supported transport in "+context.configurationTarget()+" or remove the public exposure, then regenerate.")
	case errors.Is(err, applicationmeta.ErrConfigurationSchema):
		return recoveryDiagnostic(diagnosticConstructorConfigurationSchemaInvalid, "Correct the reported owning Project document by using the fully qualified symbol of a discovered constructor with a compiled Go Config schema, or remove that constructor configuration entry, then rerun the command.")
	case errors.Is(err, applicationmeta.ErrConfigurationValues):
		return recoveryDiagnostic(diagnosticConstructorConfigurationValuesInvalid, "Correct the reported constructor configuration field in the owning Project document to match its compiled Go Config field type, then rerun the command.")
	case errors.Is(err, applicationresolve.ErrUnownedConstructorConfiguration):
		return recoveryDiagnostic(diagnosticConstructorConfigurationUnselected, "Name the reported constructor in an effective interfaces.use entry, make it reachable through an Interface requirement, or remove its configuration from "+context.configurationTarget()+", then rerun the command.")
	case errors.Is(err, applicationmeta.ErrApplyOverlay):
		return recoveryDiagnostic(diagnosticEnvironmentOverlayInvalid, invalidConfigurationRecovery(context))
	case errors.Is(err, applicationmeta.ErrInvalidManifest), errors.Is(err, configurationresolve.ErrInvalidConfiguration):
		return recoveryDiagnostic(diagnosticConfigurationInvalid, invalidConfigurationRecovery(context))
	case errors.Is(err, configurationresolve.ErrUnselectedConfiguration):
		return recoveryDiagnostic(diagnosticPluginConfigurationUnselected, invalidConfigurationRecovery(context))
	case errors.Is(err, configurationresolve.ErrMissingPlugin):
		return recoveryDiagnostic(diagnosticPluginConfigurationPluginMissing, invalidConfigurationRecovery(context))
	case errors.Is(err, applicationresolve.ErrConfigurationSelection):
		return recoveryDiagnostic(diagnosticConfigurationSelectionInvalid, "Select exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.")
	case errors.Is(err, projectlocate.ErrInvalidManifest):
		return recoveryDiagnostic(diagnosticProjectManifestInvalid, "Restore a valid root plystra.yaml in the current Go Module, then rerun the command.")
	case errors.Is(err, applicationgenerate.ErrKernelDependency), errors.Is(err, applicationgenerate.ErrRuntimeDependency):
		return recoveryDiagnostic(diagnosticApplicationDependencyDrift, "Run `plystra generate"+context.selectorSuffix()+"` to repair the required direct application runtime dependencies.")
	case errors.Is(err, projectlocate.ErrNotFound):
		return recoveryDiagnostic(diagnosticProjectNotFound, "Run the command inside a Go Module whose root contains plystra.yaml.")
	case errors.Is(err, modulelocate.ErrNotFound):
		return recoveryDiagnostic(diagnosticGoModuleNotFound, "Run the command inside the intended Go Module.")
	case errors.Is(err, modulelocate.ErrInvalidGoMod), errors.Is(err, moduledependency.ErrInvalidGoMod):
		return recoveryDiagnostic(diagnosticGoModuleInvalid, "Correct the reported go.mod entry with standard Go Module syntax, then rerun the command.")
	case errors.Is(err, moduledependency.ErrModuleUnavailable):
		return recoveryDiagnostic(diagnosticGoModuleUnavailable, goToolingRecovery())
	case errors.Is(err, gocommand.ErrRun):
		return recoveryDiagnostic(diagnosticGoCommandFailed, goToolingRecovery())
	case errors.Is(err, plugintarget.ErrAmbiguous):
		return recoveryDiagnostic(diagnosticPluginTargetAmbiguous, pluginTargetRecovery())
	case errors.Is(err, plugintarget.ErrNotFound):
		return recoveryDiagnostic(diagnosticPluginTargetNotFound, pluginTargetRecovery())
	case errors.Is(err, plugintarget.ErrSelection):
		return recoveryDiagnostic(diagnosticPluginTargetInvalid, pluginTargetRecovery())
	case errors.Is(err, generationactivation.ErrAssociationConflict):
		return recoveryDiagnostic(diagnosticGenerationActivationConflict, "Edit plugin.yaml generation.activations so the reported namespace uses one exact activation Capability.")
	case errors.Is(err, generationactivation.ErrMissingAssociation):
		return recoveryDiagnostic(diagnosticGenerationActivationMissing, "Add the missing generation.activations entry to the intended Plugin's plugin.yaml.")
	case errors.Is(err, generationactivation.ErrSelectedProviderExtension):
		return recoveryDiagnostic(diagnosticGenerationProviderExtensionMissing, "Add a compatible generation declaration to the selected activation Provider's plugin.yaml.")
	case errors.Is(err, generationresolution.ErrActivationCycle):
		return recoveryDiagnostic(diagnosticGenerationActivationCycle, generationGraphRecovery())
	case errors.Is(err, generationresolution.ErrDependencyCycle):
		return recoveryDiagnostic(diagnosticGenerationDependencyCycle, generationGraphRecovery())
	case errors.Is(err, generationresolution.ErrContributionCycle):
		return recoveryDiagnostic(diagnosticGenerationContributionCycle, generationGraphRecovery())
	case errors.Is(err, generationresolution.ErrUnorderedContributions):
		return recoveryDiagnostic(diagnosticGenerationContributionsUnordered, generationGraphRecovery())
	case errors.Is(err, generationresolution.ErrRepeatedState):
		return recoveryDiagnostic(diagnosticGenerationStateRepeated, generationConvergenceRecovery())
	case errors.Is(err, generationresolution.ErrExtensionConvergence):
		return recoveryDiagnostic(diagnosticGenerationNonconvergent, generationConvergenceRecovery())
	case errors.Is(err, generationexec.ErrUnsupportedAPI), errors.Is(err, pluginmeta.ErrUnsupportedGenerationAPI):
		return recoveryDiagnostic(diagnosticGenerationAPIUnsupported, generationDeclarationRecovery())
	case errors.Is(err, pluginindex.ErrInvalidGenerationPackage):
		return recoveryDiagnostic(diagnosticGenerationPackageInvalid, generationDeclarationRecovery())
	case errors.Is(err, generationexec.ErrTimeout):
		return recoveryDiagnostic(diagnosticGenerationTimeout, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrRequestTooLarge):
		return recoveryDiagnostic(diagnosticGenerationRequestTooLarge, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrOutputTooLarge):
		return recoveryDiagnostic(diagnosticGenerationOutputTooLarge, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrMalformedOutput):
		return recoveryDiagnostic(diagnosticGenerationOutputMalformed, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrInvalidOutput):
		return recoveryDiagnostic(diagnosticGenerationOutputInvalid, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrExtension):
		return recoveryDiagnostic(diagnosticGenerationExtensionFailed, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrCrash):
		return recoveryDiagnostic(diagnosticGenerationCrashed, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrCompile):
		return recoveryDiagnostic(diagnosticGenerationCompileFailed, generationExecutionRecovery())
	case errors.Is(err, generationexec.ErrExecute), errors.Is(err, generationresolution.ErrExtensionExecution):
		return recoveryDiagnostic(diagnosticGenerationExecutionFailed, generationExecutionRecovery())
	case errors.Is(err, generationresolution.ErrExtensionDiagnostic):
		return recoveryDiagnostic(diagnosticGenerationExtensionDiagnostic, generationExecutionRecovery())
	case errors.Is(err, aliasresolution.ErrConflict):
		return recoveryDiagnostic(diagnosticAliasConflict, aliasRecovery())
	case errors.Is(err, aliasresolution.ErrInvalidApplicationAlias):
		return recoveryDiagnostic(diagnosticAliasApplicationInvalid, aliasRecovery())
	case errors.Is(err, aliasresolution.ErrInvalidExtensionOutput):
		return recoveryDiagnostic(diagnosticAliasExtensionOutputInvalid, aliasRecovery())
	case errors.Is(err, generationresolution.ErrAliasResolution):
		return recoveryDiagnostic(diagnosticAliasResolutionFailed, aliasRecovery())
	case errors.Is(err, protobufwiremap.ErrHistory):
		return recoveryDiagnostic(diagnosticProtobufWireHistoryInvalid, "Restore generated/proto/wire-map.json from its last known-good generated state, then run `plystra generate"+context.selectorSuffix()+"`.")
	case errors.Is(err, protobufidentity.ErrCollision):
		return recoveryDiagnostic(diagnosticProtobufIdentityCollision, "Rename one conflicting authored field or enum member in the owning Interface contract, then run `plystra generate"+context.selectorSuffix()+"`.")
	case errors.Is(err, protobufmodel.ErrOperationKind):
		return recoveryDiagnostic(diagnosticProtobufOperationKindUnsupported, "Remove the unsupported Capability from http.expose in "+context.configurationTarget()+", then run `plystra generate"+context.selectorSuffix()+"`.")
	case errors.Is(err, generatedfiles.ErrConflict):
		return recoveryDiagnostic(diagnosticGeneratedOwnershipConflict, generatedOwnershipRecovery(context))
	case errors.Is(err, generatedfiles.ErrUnexpected):
		return recoveryDiagnostic(diagnosticGeneratedUnexpectedOutput, generatedOwnershipRecovery(context))
	case errors.Is(err, generatedfiles.ErrManifest):
		return recoveryDiagnostic(diagnosticGeneratedManifestInvalid, "Restore generated/.plystra-manifest.json from a known-good generated state, then run `plystra generate"+context.selectorSuffix()+"`.")
	case errors.Is(err, capabilitymeta.ErrInvalidManifest):
		return recoveryDiagnostic(diagnosticCapabilityManifestInvalid, "Correct the reported authored capability.yaml, then rerun the command.")
	default:
		return actionableDiagnostic{}, false
	}
}

func recoveryDiagnostic(code, recovery string) (actionableDiagnostic, bool) {
	return actionableDiagnostic{code: code, recovery: recovery}, true
}

func invalidConfigurationRecovery(context recoveryContext) string {
	return "Edit " + context.configurationTarget() + " so every value matches a selected Plugin's closed typed schema, then rerun the command."
}

func goToolingRecovery() string {
	return "Make the reported module or Go command resolve successfully with ordinary Go tooling, then rerun the Plystra command."
}

func pluginTargetRecovery() string {
	return "Rerun with `--plugin <plugin-directory-or-id>` to select one exact local Plugin."
}

func implementationSelectionRecovery(context recoveryContext) string {
	return "Replace the reported choice with one visible compatible constructor by running `plystra use <interface-id> <constructor-symbol>" + context.selectorSuffix() + "`."
}

func generationGraphRecovery() string {
	return "Edit the reported generation declarations to remove the dependency cycle or unordered token flow."
}

func generationConvergenceRecovery() string {
	return "Make the selected generation extensions deterministic and convergent for identical normalized input."
}

func generationDeclarationRecovery() string {
	return "Edit the Plugin generation declaration to use a supported API and a safe existing package, then rerun the command."
}

func generationExecutionRecovery() string {
	return "Fix the selected generation package reported above, then rerun the command."
}

func aliasRecovery() string {
	return "Edit the reported Alias declaration or generation contribution so it maps directly to one compatible canonical target."
}

func generatedOwnershipRecovery(context recoveryContext) string {
	return "Move the reported unowned path outside generated/, then run `plystra generate" + context.selectorSuffix() + "`."
}

func splitEmbeddedRecovery(message string) (string, string, bool) {
	const marker = "; correction:"
	first := strings.Index(message, marker)
	last := strings.LastIndex(message, marker)
	if first < 0 || last < 0 {
		return "", "", false
	}
	problem := strings.TrimSpace(message[:first])
	action := strings.TrimSpace(message[last+len(marker):])
	if problem == "" || action == "" {
		return "", "", false
	}
	return problem, action, true
}

func identicalContractRecovery(capability string) string {
	return "Make every Provider of " + capability + " carry one identical provider-independent capability.yaml."
}

func trimEmbeddedRecovery(message string) string {
	lines := strings.Split(message, "\n")
	kept := lines[:0]
	for _, line := range lines {
		for _, marker := range []string{"; correction:", " correction:"} {
			if problem, _, found := strings.Cut(line, marker); found {
				line = problem
				break
			}
		}
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func (c recoveryContext) selectorSuffix() string {
	mode, value, valid := c.selector()
	if !valid {
		return ""
	}
	switch mode {
	case "environment":
		if !safeEnvironmentHint(value) {
			return " --env <environment>"
		}
		return " --env " + strconv.Quote(value)
	case "config":
		path, safe := safeConfigurationHint(value)
		if !safe {
			return " --config <yaml-path>"
		}
		return " --config " + strconv.Quote(path)
	default:
		return ""
	}
}

func (c recoveryContext) configurationTarget() string {
	mode, value, valid := c.selector()
	if !valid {
		return "the selected current-Project configuration"
	}
	switch mode {
	case "environment":
		if safeEnvironmentHint(value) {
			return "plystra." + value + ".yaml"
		}
		return "the selected environment overlay"
	case "config":
		if path, safe := safeConfigurationHint(value); safe {
			return path
		}
		return "the selected full-replacement configuration"
	default:
		return "plystra.yaml"
	}
}

func (c recoveryContext) selector() (string, string, bool) {
	if c.configurationPath != "" && c.environmentName != "" {
		return "", "", false
	}
	if c.configurationPath != "" {
		return "config", c.configurationPath, true
	}
	if c.environmentName != "" {
		return "environment", c.environmentName, true
	}
	configuration, hasConfiguration, configurationValid := recoveryEnvironmentValue(c.environment, "PLYSTRA_CONFIG")
	environment, hasEnvironment, environmentValid := recoveryEnvironmentValue(c.environment, "PLYSTRA_ENV")
	if !configurationValid || !environmentValid || hasConfiguration && hasEnvironment {
		return "", "", false
	}
	if hasConfiguration {
		return "config", configuration, true
	}
	if hasEnvironment {
		return "environment", environment, true
	}
	return "default", "", true
}

func recoveryEnvironmentValue(environment []string, name string) (string, bool, bool) {
	var value string
	found := false
	for _, entry := range environment {
		key, current, exists := strings.Cut(entry, "=")
		if !exists || key != name {
			continue
		}
		if found {
			return "", false, false
		}
		value = current
		found = true
	}
	return value, found, true
}

func safeEnvironmentHint(value string) bool {
	return value != "" && len(value) <= 200 && value != "." && value != ".." &&
		!filepath.IsAbs(value) && filepath.VolumeName(value) == "" &&
		!strings.ContainsAny(value, `/\\<>:"|?*`) &&
		strings.IndexFunc(value, unicode.IsControl) < 0 && filepath.Clean(value) == value &&
		safeRecoveryToken(value, false)
}

func safeConfigurationHint(value string) (string, bool) {
	if value == "" || len(value) > 500 || strings.IndexFunc(value, unicode.IsControl) >= 0 || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return "", false
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	clean = filepath.ToSlash(clean)
	if !safeRecoveryToken(clean, true) {
		return "", false
	}
	return clean, true
}

func safeRecoveryToken(value string, allowSlash bool) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", rune(character)) || allowSlash && character == '/' {
			continue
		}
		return false
	}
	return value != ""
}
