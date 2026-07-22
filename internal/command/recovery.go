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
	action, actionable := primaryRecoveryAction(err, context)
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
		_, _ = fmt.Fprintf(writer, "\nRecovery:\n%s\n", action)
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

func primaryRecoveryAction(err error, context recoveryContext) (string, bool) {
	if errors.Is(err, newproject.ErrInvalidTemplate) {
		if _, action, found := splitEmbeddedRecovery(err.Error()); found {
			return action, true
		}
		return "Use a corrected published template version whose clean Project passes generation, check, build, and lifecycle validation.", true
	}
	var requirementConflict *providerresolution.RequirementConflictError
	if errors.As(err, &requirementConflict) && requirementConflict != nil {
		return identicalContractRecovery(requirementConflict.Capability().String()), true
	}
	var providerContractConflict *providerresolution.ProviderContractConflictError
	if errors.As(err, &providerContractConflict) && providerContractConflict != nil {
		return identicalContractRecovery(providerContractConflict.Capability().String()), true
	}
	var providerContract *providerresolution.ProviderContractError
	if errors.As(err, &providerContract) && providerContract != nil {
		return identicalContractRecovery(providerContract.Capability().String()), true
	}
	var visibleContractConflict *applicationinput.ContractConflictError
	if errors.As(err, &visibleContractConflict) && visibleContractConflict != nil {
		return identicalContractRecovery(visibleContractConflict.ID().String()), true
	}
	var authoredContractConflict *capabilitycreate.SchemaConflictError
	if errors.As(err, &authoredContractConflict) && authoredContractConflict != nil {
		return identicalContractRecovery(authoredContractConflict.Capability().String()), true
	}
	var invalidChoice *providerresolution.ChoiceError
	if errors.As(err, &invalidChoice) && invalidChoice != nil {
		command := "plystra use " + invalidChoice.Capability().String() + " <plugin-id>" + context.selectorSuffix()
		return "Replace the invalid Provider choice with one visible compatible Plugin by running `" + command + "`.", true
	}
	var missingProvider *providerresolution.MissingProviderError
	if errors.As(err, &missingProvider) && missingProvider != nil {
		return "Add an intended dependency with `plystra add <go-module-query>` whose Plugin provides " + missingProvider.Capability().String() + ".", true
	}
	var ambiguousProvider *providerresolution.AmbiguousProviderError
	if errors.As(err, &ambiguousProvider) && ambiguousProvider != nil {
		command := "plystra use " + ambiguousProvider.Capability().String() + " <plugin-id>" + context.selectorSuffix()
		return "Select one compatible Provider explicitly by running `" + command + "`.", true
	}

	switch {
	case errors.Is(err, applicationresolve.ErrManifest) && !errors.Is(err, applicationresolve.ErrConfigurationSelection):
		return "Correct the reported root or dependency Project plystra.yaml, then rerun the command.", true
	case errors.Is(err, applicationmeta.ErrInheritedConflict):
		return "Set or remove the conflicting field explicitly in " + context.configurationTarget() + ", then rerun the command.", true
	case errors.Is(err, applicationmeta.ErrAmbiguousConfigurationOwnership):
		return "Make the inherited field intent explicit in " + context.configurationTarget() + " by restoring it or writing its typed removal.", true
	case errors.Is(err, applicationmeta.ErrHTTPTransportSelection):
		return "Enable a supported transport in " + context.configurationTarget() + " or remove the public exposure, then regenerate.", true
	case errors.Is(err, applicationmeta.ErrConfigurationSchema):
		return "Declare the reported Plugin configuration field in plugin.yaml, then rerun the command.", true
	case errors.Is(err, applicationmeta.ErrApplyOverlay), errors.Is(err, applicationmeta.ErrInvalidManifest), errors.Is(err, configurationresolve.ErrInvalidConfiguration), errors.Is(err, configurationresolve.ErrUnselectedConfiguration), errors.Is(err, configurationresolve.ErrMissingPlugin):
		return "Edit " + context.configurationTarget() + " so every value matches a selected Plugin's closed typed schema, then rerun the command.", true
	case errors.Is(err, applicationresolve.ErrConfigurationSelection):
		return "Select exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.", true
	case errors.Is(err, projectlocate.ErrInvalidManifest):
		return "Restore a valid root plystra.yaml in the current Go Module, then rerun the command.", true
	case errors.Is(err, applicationgenerate.ErrKernelDependency), errors.Is(err, applicationgenerate.ErrRuntimeDependency):
		return "Run `plystra generate" + context.selectorSuffix() + "` to repair the required direct application runtime dependencies.", true
	case errors.Is(err, projectlocate.ErrNotFound):
		return "Run the command inside a Go Module whose root contains plystra.yaml.", true
	case errors.Is(err, modulelocate.ErrNotFound):
		return "Run the command inside the intended Go Module.", true
	case errors.Is(err, modulelocate.ErrInvalidGoMod), errors.Is(err, moduledependency.ErrInvalidGoMod):
		return "Correct the reported go.mod entry with standard Go Module syntax, then rerun the command.", true
	case errors.Is(err, moduledependency.ErrModuleUnavailable), errors.Is(err, gocommand.ErrRun):
		return "Make the reported module or Go command resolve successfully with ordinary Go tooling, then rerun the Plystra command.", true
	case errors.Is(err, plugintarget.ErrAmbiguous), errors.Is(err, plugintarget.ErrNotFound), errors.Is(err, plugintarget.ErrSelection):
		return "Rerun with `--plugin <plugin-directory-or-id>` to select one exact local Plugin.", true
	case errors.Is(err, generationactivation.ErrAssociationConflict):
		return "Edit plugin.yaml generation.activations so the reported namespace uses one exact activation Capability.", true
	case errors.Is(err, generationactivation.ErrMissingAssociation):
		return "Add the missing generation.activations entry to the intended Plugin's plugin.yaml.", true
	case errors.Is(err, generationactivation.ErrSelectedProviderExtension):
		return "Add a compatible generation declaration to the selected activation Provider's plugin.yaml.", true
	case errors.Is(err, generationresolution.ErrActivationCycle), errors.Is(err, generationresolution.ErrDependencyCycle), errors.Is(err, generationresolution.ErrContributionCycle), errors.Is(err, generationresolution.ErrUnorderedContributions):
		return "Edit the reported generation declarations to remove the dependency cycle or unordered token flow.", true
	case errors.Is(err, generationresolution.ErrRepeatedState), errors.Is(err, generationresolution.ErrExtensionConvergence):
		return "Make the selected generation extensions deterministic and convergent for identical normalized input.", true
	case errors.Is(err, generationexec.ErrUnsupportedAPI), errors.Is(err, pluginmeta.ErrUnsupportedGenerationAPI), errors.Is(err, pluginindex.ErrInvalidGenerationPackage):
		return "Edit the Plugin generation declaration to use a supported API and a safe existing package, then rerun the command.", true
	case generationExecutionFailure(err):
		return "Fix the selected generation package reported above, then rerun the command.", true
	case errors.Is(err, aliasresolution.ErrConflict), errors.Is(err, aliasresolution.ErrInvalidApplicationAlias), errors.Is(err, aliasresolution.ErrInvalidExtensionOutput), errors.Is(err, generationresolution.ErrAliasResolution):
		return "Edit the reported Alias declaration or generation contribution so it maps directly to one compatible canonical target.", true
	case errors.Is(err, protobufwiremap.ErrHistory):
		return "Restore generated/proto/wire-map.json from its last known-good generated state, then regenerate.", true
	case errors.Is(err, protobufidentity.ErrCollision):
		return "Rename one conflicting authored field or enum member in capability.yaml, then regenerate.", true
	case errors.Is(err, protobufmodel.ErrOperationKind):
		return "Remove the unsupported Capability from http.expose in " + context.configurationTarget() + ", then regenerate.", true
	case errors.Is(err, generatedfiles.ErrConflict), errors.Is(err, generatedfiles.ErrUnexpected):
		return "Move the reported unowned path outside generated/, then run `plystra generate" + context.selectorSuffix() + "`.", true
	case errors.Is(err, generatedfiles.ErrManifest):
		return "Restore generated/.plystra-manifest.json from a known-good generated state, then run `plystra generate" + context.selectorSuffix() + "`.", true
	case errors.Is(err, capabilitycreate.ErrConfirmationRequired):
		return "Review the visible Capability versions, then rerun the create command with `--confirm`.", true
	case errors.Is(err, capabilitymeta.ErrInvalidManifest):
		return "Correct the reported authored capability.yaml, then rerun the command.", true
	case errors.Is(err, atomicfs.ErrConcurrentChange), errors.Is(err, applicationresolve.ErrConcurrentChange), errors.Is(err, applicationgenerate.ErrConcurrentChange):
		return "Stop concurrent Project edits, then rerun the command against the unchanged authored inputs.", true
	default:
		return "", false
	}
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

func generationExecutionFailure(err error) bool {
	return errors.Is(err, generationexec.ErrCompile) ||
		errors.Is(err, generationexec.ErrExecute) ||
		errors.Is(err, generationexec.ErrExtension) ||
		errors.Is(err, generationexec.ErrCrash) ||
		errors.Is(err, generationexec.ErrTimeout) ||
		errors.Is(err, generationexec.ErrRequestTooLarge) ||
		errors.Is(err, generationexec.ErrOutputTooLarge) ||
		errors.Is(err, generationexec.ErrMalformedOutput) ||
		errors.Is(err, generationexec.ErrInvalidOutput) ||
		errors.Is(err, generationresolution.ErrExtensionExecution) ||
		errors.Is(err, generationresolution.ErrExtensionDiagnostic)
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
