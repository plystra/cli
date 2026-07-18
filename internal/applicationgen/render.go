// Package applicationgen renders one complete currently supported application
// generated tree from the stable generation-resolution result.
package applicationgen

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/httpgen"
	"github.com/plystra/cli/internal/invocationgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/providergen"
	"github.com/plystra/cli/internal/sdkmodel"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

const (
	aliasManifestPath         = "generated/manifest.json"
	assemblyCompatibilityPath = "generated/go/assembly/compatibility_gen.go"
)

var (
	// ErrRender reports an incomplete or inconsistent resolved application or
	// failure in one deterministic renderer.
	ErrRender = errors.New("render generated application")
	// ErrResolution reports absent or internally inconsistent final resolution.
	ErrResolution = errors.New("invalid generation resolution result")
)

// Options carries application-owned generated package identities.
type Options struct {
	ModulePath          string
	JavaScriptPackage   string
	KernelModuleVersion string
	KernelBuildIdentity string
	Configurations      []configurationgen.Input
	Providers           []assemblygen.ProviderInput
}

// Render lowers final selected contributions once and renders the Kernel
// assembly compatibility handshake, runtime bootstrap, contracts, providers,
// canonical and Alias clients, canonical invocation paths, HTTP adapters, the
// JavaScript SDK, API documentation, and the current Alias manifest into one
// managed output model.
func Render(options Options, resolution generationresolution.ExtensionResult) (generatedfiles.Output, error) {
	context := resolution.Context()
	if !validContext(context) {
		return generatedfiles.Output{}, fmt.Errorf("%w: %w: final generation context is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	aliases := resolution.AliasResolution()
	if !validAliases(aliases) {
		return generatedfiles.Output{}, fmt.Errorf("%w: %w: final Alias map is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	providers, err := assemblygen.RenderProviders(options.Providers)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: selected providers: %w", ErrRender, err)
	}
	if err := validateAssemblyClosure(options, context); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: %w: %v", ErrRender, ErrResolution, err)
	}
	plan, err := generationlowering.Lower(options.ModulePath, resolution.Contributions())
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: lower contributions: %w", ErrRender, err)
	}

	files := make([]generatedfiles.File, 0)
	add := func(filePath string, data []byte) error {
		file, err := generatedfiles.NewFile(filePath, data)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	}
	configurationInputs := append([]configurationgen.Input(nil), options.Configurations...)
	sort.Slice(configurationInputs, func(left, right int) bool {
		if configurationInputs[left].PluginID != configurationInputs[right].PluginID {
			return configurationInputs[left].PluginID < configurationInputs[right].PluginID
		}
		return configurationInputs[left].PluginName < configurationInputs[right].PluginName
	})
	configurationTypes := make(map[string]string, len(configurationInputs))
	for _, input := range configurationInputs {
		configuration, err := configurationgen.Render(input)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: configuration for plugin %q: %w", ErrRender, input.PluginID, err)
		}
		if previous, collision := configurationTypes[configuration.TypeName()]; collision {
			return generatedfiles.Output{}, fmt.Errorf("%w: configurations for plugins %q and %q both generate Go type %s", ErrRender, previous, input.PluginID, configuration.TypeName())
		}
		configurationTypes[configuration.TypeName()] = input.PluginID
		if err := add(configuration.Path(), configuration.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: configuration for plugin %q: %w", ErrRender, input.PluginID, err)
		}
	}
	aliasManifest := append(aliases.CanonicalJSON(), '\n')
	if err := add(aliasManifestPath, aliasManifest); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Alias manifest: %w", ErrRender, err)
	}
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Kernel assembly compatibility: %w", ErrRender, err)
	}
	if err := add(assemblyCompatibilityPath, compatibility); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Kernel assembly compatibility: %w", ErrRender, err)
	}
	if err := add(assemblygen.ProvidersPath, providers); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: selected providers: %w", ErrRender, err)
	}
	bootstrap, err := bootstrapgen.Render(bootstrapgen.Options{
		ModulePath:            options.ModulePath,
		DefaultStartupTimeout: applicationmeta.DefaultStartupTimeout,
	})
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: runtime bootstrap: %w", ErrRender, err)
	}
	if err := add(bootstrapgen.Path, bootstrap); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: runtime bootstrap: %w", ErrRender, err)
	}

	requirements := context.Requirements()
	developerSurfaceIDs := make(map[string]generation.CapabilityID, len(requirements))
	for _, id := range requirements {
		developerSurfaceIDs[id.String()] = id
	}
	for _, plugin := range context.Plugins() {
		if plugin.Module().Path() != options.ModulePath {
			continue
		}
		for _, id := range plugin.Provides() {
			developerSurfaceIDs[id.String()] = id
		}
	}
	developerSurfaceNames := make([]string, 0, len(developerSurfaceIDs))
	for id := range developerSurfaceIDs {
		developerSurfaceNames = append(developerSurfaceNames, id)
	}
	sort.Strings(developerSurfaceNames)
	for _, name := range developerSurfaceNames {
		id := developerSurfaceIDs[name]
		target, exists := context.Capability(id)
		if !exists {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: canonical Capability %s selected for a module-owned developer surface is absent from the final context", ErrRender, ErrResolution, id)
		}
		var contract contractgen.File
		if target.Intrinsic() {
			contract, err = contractgen.RenderIntrinsic(target.ContractJSON())
		} else {
			contract, err = contractgen.Render(target.ContractJSON())
		}
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrRender, id, err)
		}
		if err := add(contract.Path(), contract.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrRender, id, err)
		}
		if target.Intrinsic() {
			continue
		}
		provider, err := providergen.Render(options.ModulePath, target.ContractJSON())
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrRender, id, err)
		}
		if err := add(provider.Path(), provider.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrRender, id, err)
		}
	}
	if len(requirements) != 0 {
		invocationContext, err := invocationgen.RenderContext()
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation context: %w", ErrRender, err)
		}
		if err := add(invocationContext.Path(), invocationContext.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation context: %w", ErrRender, err)
		}
	}

	targets := make(map[generation.CapabilityID]generation.CapabilityView, len(requirements))
	invocationInputs := make([]assemblygen.InvocationInput, 0, len(requirements))
	javaScriptTargets := make([]sdkmodel.CanonicalTargetView, 0)
	httpTargets := 0
	for _, id := range requirements {
		target, exists := context.Capability(id)
		if !exists {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: required canonical Capability %s is absent from the final context", ErrRender, ErrResolution, id)
		}
		targets[id] = target
		if target.Exposure().JavaScript {
			javaScriptTargets = append(javaScriptTargets, target)
		}
		if target.Exposure().HTTP {
			httpTargets++
		}

		client, err := clientgen.Render(options.ModulePath, target.ContractJSON())
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrRender, id, err)
		}
		if err := add(client.Path(), client.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrRender, id, err)
		}
		invocation, err := invocationgen.RenderPlan(options.ModulePath, target.ContractJSON(), plan)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation %s: %w", ErrRender, id, err)
		}
		if err := add(invocation.Path(), invocation.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation %s: %w", ErrRender, id, err)
		}
		identifier, err := capabilityid.Parse(id.String())
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: required canonical Capability %s cannot enter runtime assembly", ErrRender, ErrResolution, id)
		}
		invocationInput := assemblygen.InvocationInput{
			ContractJSON: target.ContractJSON(),
			Intrinsic:    target.Intrinsic(),
			Dependencies: invocation.Dependencies(),
		}
		if target.Intrinsic() {
			invocationInput.SelectionReason = kernelinvocation.SelectionReasonIntrinsic
		} else {
			selection, exists := resolution.ActivationResolution().ProviderResolution().SelectedProvider(identifier)
			if !exists {
				return generatedfiles.Output{}, fmt.Errorf("%w: %w: required ordinary Capability %s has no selected provider", ErrRender, ErrResolution, id)
			}
			reason := kernelinvocation.SelectionReasonSoleProvider
			if selection.Explicit() {
				reason = kernelinvocation.SelectionReasonExplicit
			}
			invocationInput.ProviderID = selection.PluginID()
			invocationInput.SelectionReason = reason
		}
		invocationInputs = append(invocationInputs, invocationInput)
		if target.Exposure().HTTP {
			handler, err := httpgen.RenderPlan(options.ModulePath, target, plan)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: HTTP adapter %s: %w", ErrRender, id, err)
			}
			if err := add(handler.Path(), handler.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: HTTP adapter %s: %w", ErrRender, id, err)
			}
		}
	}
	invocations, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               options.ModulePath,
		ApplicationBuildIdentity: context.Digest(),
		KernelModuleVersion:      options.KernelModuleVersion,
		KernelBuildIdentity:      options.KernelBuildIdentity,
		DefaultTimeout:           applicationmeta.DefaultInvocationTimeout,
		Providers:                options.Providers,
		Invocations:              invocationInputs,
	})
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: canonical invocation assembly: %w", ErrRender, err)
	}
	if err := add(assemblygen.InvocationsPath, invocations); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: canonical invocation assembly: %w", ErrRender, err)
	}

	resolvedAliases := aliases.Aliases()
	aliasViews := make([]sdkmodel.AliasView, len(resolvedAliases))
	docAliasViews := make([]apidocgen.AliasView, len(resolvedAliases))
	httpAliases := 0
	for index, alias := range resolvedAliases {
		aliasViews[index] = alias
		docAliasViews[index] = alias
		target, exists := targets[alias.Target()]
		if !exists {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: Alias %s target %s is not a generated requirement", ErrRender, ErrResolution, alias.ID(), alias.Target())
		}
		if alias.Exposure().Go {
			client, err := clientgen.RenderAlias(options.ModulePath, alias, target)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias client %s: %w", ErrRender, alias.ID(), err)
			}
			if err := add(client.Path(), client.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias client %s: %w", ErrRender, alias.ID(), err)
			}
		}
		if alias.Exposure().HTTP {
			httpAliases++
			handler, err := httpgen.RenderAlias(options.ModulePath, alias, target)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias HTTP adapter %s: %w", ErrRender, alias.ID(), err)
			}
			if err := add(handler.Path(), handler.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias HTTP adapter %s: %w", ErrRender, alias.ID(), err)
			}
		}
	}

	model, err := sdkmodel.Build(javaScriptTargets, aliasViews)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: SDK model: %w", ErrRender, err)
	}
	if len(javaScriptTargets) != 0 {
		javaScript, err := javascriptgen.Render(javascriptgen.Options{PackageName: options.JavaScriptPackage}, model)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: JavaScript SDK: %w", ErrRender, err)
		}
		for _, file := range javaScript {
			if err := add(file.Path(), file.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: JavaScript SDK: %w", ErrRender, err)
			}
		}
	}
	if httpTargets != 0 || httpAliases != 0 {
		docs, err := apidocgen.Render(model, docAliasViews)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: API documentation: %w", ErrRender, err)
		}
		for _, file := range docs {
			if err := add(file.Path(), file.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: API documentation: %w", ErrRender, err)
			}
		}
	}

	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: finalize managed output: %w", ErrRender, err)
	}
	return output, nil
}

func validateAssemblyClosure(options Options, context generation.Context) error {
	selected := context.Plugins()
	if len(options.Providers) != len(selected) {
		return fmt.Errorf("selected plugin count %d does not match provider input count %d", len(selected), len(options.Providers))
	}
	providers := make(map[string]assemblygen.ProviderInput, len(options.Providers))
	for _, provider := range options.Providers {
		if _, duplicate := providers[provider.PluginID]; duplicate {
			return fmt.Errorf("provider input duplicates Plugin ID %q", provider.PluginID)
		}
		providers[provider.PluginID] = provider
	}
	for _, plugin := range selected {
		pluginID := plugin.ID().String()
		provider, exists := providers[pluginID]
		if !exists {
			return fmt.Errorf("selected plugin %q has no provider input", pluginID)
		}
		if provider.ModulePath != plugin.Module().Path() {
			return fmt.Errorf("selected plugin %q module %q does not match provider input module %q", pluginID, plugin.Module().Path(), provider.ModulePath)
		}
		if provider.ModuleVersion != plugin.Module().Version() {
			return fmt.Errorf("selected plugin %q module version %q does not match provider input version %q", pluginID, plugin.Module().Version(), provider.ModuleVersion)
		}
	}

	configurations := make(map[string]configurationgen.Input, len(options.Configurations))
	for _, configuration := range options.Configurations {
		if _, duplicate := configurations[configuration.PluginID]; duplicate {
			return fmt.Errorf("configuration input duplicates Plugin ID %q", configuration.PluginID)
		}
		provider, exists := providers[configuration.PluginID]
		if !exists {
			return fmt.Errorf("configuration input for Plugin ID %q has no selected provider", configuration.PluginID)
		}
		if provider.ModulePath != options.ModulePath {
			return fmt.Errorf("configuration input for Plugin ID %q belongs to non-local module %q", configuration.PluginID, provider.ModulePath)
		}
		pluginName, found := strings.CutPrefix(provider.ImportPath, options.ModulePath+"/")
		if !found || pluginName != configuration.PluginName {
			return fmt.Errorf("configuration input for Plugin ID %q names plugin %q instead of selected import %q", configuration.PluginID, configuration.PluginName, provider.ImportPath)
		}
		configurations[configuration.PluginID] = configuration
	}
	for _, provider := range options.Providers {
		if provider.ModulePath != options.ModulePath {
			continue
		}
		if _, exists := configurations[provider.PluginID]; !exists {
			return fmt.Errorf("local selected plugin %q has no generated configuration input", provider.PluginID)
		}
	}
	return nil
}

func validContext(context generation.Context) bool {
	canonical := context.CanonicalJSON()
	return context.APIVersion() == generation.Version && len(canonical) != 0 && context.Digest() == digest(canonical)
}

func validAliases(aliases aliasresolution.Result) bool {
	canonical := aliases.CanonicalJSON()
	return len(canonical) != 0 && aliases.Digest() == digest(canonical)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
