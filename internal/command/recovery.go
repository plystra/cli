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
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/newproject"
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
	diagnosticPluginConfigurationSchemaInvalid   = diagnosticcode.PluginConfigurationSchemaInvalid
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
	diagnosticCapabilityConfirmationRequired     = diagnosticcode.CapabilityConfirmationRequired
	diagnosticCapabilityManifestInvalid          = diagnosticcode.CapabilityManifestInvalid
	diagnosticProjectConcurrentChange            = diagnosticcode.ProjectConcurrentChange
	diagnosticConfigurationCompositionDrift      = diagnosticcode.ConfigurationCompositionDrift
	diagnosticGeneratedDrift                     = diagnosticcode.GeneratedDrift
)

func commandRecoveryContext(configurationPath, environmentName string, environment []string) recoveryContext {
	return recoveryContext{
		configurationPath: configurationPath,
		environmentName:   environmentName,
		environment:       append([]string(nil), environment...),
	}
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
		_, _ = fmt.Fprintf(writer, "\nRecovery:\n%s\n\nDiagnostic: %s\n", diagnostic.recovery, diagnostic.code)
	}
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

	switch {
	case errors.Is(err, applicationresolve.ErrManifest) && !errors.Is(err, applicationresolve.ErrConfigurationSelection):
		return recoveryDiagnostic(diagnosticProjectManifestInvalid, "Correct the reported root or dependency Project plystra.yaml, then rerun the command.")
	case errors.Is(err, applicationmeta.ErrInheritedConflict):
		return recoveryDiagnostic(diagnosticConfigurationInheritedConflict, "Set or remove the conflicting field explicitly in "+context.configurationTarget()+", then rerun the command.")
	case errors.Is(err, applicationmeta.ErrAmbiguousConfigurationOwnership):
		return recoveryDiagnostic(diagnosticConfigurationOwnershipAmbiguous, "Make the inherited field intent explicit in "+context.configurationTarget()+" by restoring it or writing its typed removal.")
	case errors.Is(err, applicationmeta.ErrHTTPTransportSelection):
		return recoveryDiagnostic(diagnosticHTTPTransportSelectionInvalid, "Enable a supported transport in "+context.configurationTarget()+" or remove the public exposure, then regenerate.")
	case errors.Is(err, applicationmeta.ErrConfigurationSchema):
		return recoveryDiagnostic(diagnosticPluginConfigurationSchemaInvalid, "Declare the reported Plugin configuration field in plugin.yaml, then rerun the command.")
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
		return recoveryDiagnostic(diagnosticProtobufWireHistoryInvalid, "Restore generated/proto/wire-map.json from its last known-good generated state, then regenerate.")
	case errors.Is(err, protobufidentity.ErrCollision):
		return recoveryDiagnostic(diagnosticProtobufIdentityCollision, "Rename one conflicting authored field or enum member in capability.yaml, then regenerate.")
	case errors.Is(err, protobufmodel.ErrOperationKind):
		return recoveryDiagnostic(diagnosticProtobufOperationKindUnsupported, "Remove the unsupported Capability from http.expose in "+context.configurationTarget()+", then regenerate.")
	case errors.Is(err, generatedfiles.ErrConflict):
		return recoveryDiagnostic(diagnosticGeneratedOwnershipConflict, generatedOwnershipRecovery(context))
	case errors.Is(err, generatedfiles.ErrUnexpected):
		return recoveryDiagnostic(diagnosticGeneratedUnexpectedOutput, generatedOwnershipRecovery(context))
	case errors.Is(err, generatedfiles.ErrManifest):
		return recoveryDiagnostic(diagnosticGeneratedManifestInvalid, "Restore generated/.plystra-manifest.json from a known-good generated state, then run `plystra generate"+context.selectorSuffix()+"`.")
	case errors.Is(err, capabilitycreate.ErrConfirmationRequired):
		return recoveryDiagnostic(diagnosticCapabilityConfirmationRequired, "Review the visible Capability versions, then rerun the create command with `--confirm`.")
	case errors.Is(err, capabilitymeta.ErrInvalidManifest):
		return recoveryDiagnostic(diagnosticCapabilityManifestInvalid, "Correct the reported authored capability.yaml, then rerun the command.")
	case errors.Is(err, atomicfs.ErrConcurrentChange), errors.Is(err, applicationresolve.ErrConcurrentChange), errors.Is(err, applicationgenerate.ErrConcurrentChange):
		return recoveryDiagnostic(diagnosticProjectConcurrentChange, "Stop concurrent Project edits, then rerun the command against the unchanged authored inputs.")
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
