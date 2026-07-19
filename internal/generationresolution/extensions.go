package generationresolution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/providerresolution"
)

var (
	// ErrResolveExtensions reports failure to reach one stable generation-derived
	// requirement closure and contribution plan after activation providers have
	// been selected.
	ErrResolveExtensions = errors.New("resolve generation extension requirements")
	// ErrApplicationContext reports incomplete or inconsistent normalized input
	// used to construct the immutable extension context.
	ErrApplicationContext = errors.New("build generation extension context")
	// ErrExtensionExecution reports failure to build or run a selected helper.
	ErrExtensionExecution = errors.New("run selected generation extension")
	// ErrExtensionProvenance reports output for a namespace or source Capability
	// that the selected provider extension was not activated to interpret.
	ErrExtensionProvenance = errors.New("invalid generation extension provenance")
	// ErrExtensionDiagnostic reports a structured error diagnostic returned by a
	// selected extension.
	ErrExtensionDiagnostic = errors.New("generation extension reported an error")
	// ErrDependencyCycle reports a cycle containing activation or generated
	// canonical Capability requirements.
	ErrDependencyCycle = errors.New("generation requirement cycle")
	// ErrRepeatedState reports different outputs for the same immutable context.
	ErrRepeatedState = errors.New("generation extension repeated input state with different output")
	// ErrExtensionConvergence reports exhaustion of the finite monotonic pass
	// bound before a stable context and output state was confirmed.
	ErrExtensionConvergence = errors.New("generation extension requirements did not converge")
	// ErrAliasResolution reports failure to merge explicit and selected-extension
	// Alias candidates after requirement and provider closure stabilized.
	ErrAliasResolution = errors.New("resolve final generation Capability Aliases")
)

// Plugin supplies one visible plugin's supported public generation context
// together with CLI-private selection and filesystem provenance. Local marks a
// root-level application plugin, which is included independently of provider
// selection. Local, ModuleRoot, and PluginPath never enter generation.Context.
type Plugin struct {
	Context    generation.PluginInput
	Local      bool
	ModuleRoot string
	PluginPath string
}

// ExtensionInput contains the activation-resolution inputs plus the complete
// visible canonical catalog, public plugin metadata, and parsed application
// HTTP exposure and Alias declarations needed to build each immutable
// extension context.
type ExtensionInput struct {
	Input
	Plugins                  []Plugin
	Capabilities             []generation.CapabilityInput
	ApplicationHTTPExposures []applicationmeta.HTTPExposure
	ApplicationAliases       []applicationmeta.Alias
	BuildOptions             generationexec.BuildOptions
}

// GeneratedRequirement records one exact ordinary requirement and complete
// selected-extension provenance.
type GeneratedRequirement struct {
	pluginID   string
	namespace  string
	source     capabilityid.Identifier
	ruleID     string
	capability capabilityid.Identifier
}

// PluginID returns the selected extension owner.
func (r GeneratedRequirement) PluginID() string { return r.pluginID }

// Namespace returns the interpreted extension namespace.
func (r GeneratedRequirement) Namespace() string { return r.namespace }

// Source returns the required Capability whose metadata matched the rule.
func (r GeneratedRequirement) Source() capabilityid.Identifier { return r.source }

// RuleID returns the stable extension rule identifier.
func (r GeneratedRequirement) RuleID() string { return r.ruleID }

// Capability returns the exact generated canonical requirement.
func (r GeneratedRequirement) Capability() capabilityid.Identifier { return r.capability }

// ExtensionOutput records one selected helper's normalized final-pass output.
type ExtensionOutput struct {
	pluginID    string
	api         string
	packagePath string
	namespaces  []string
	output      generation.NormalizedOutput
}

// PluginID returns the selected extension owner.
func (o ExtensionOutput) PluginID() string { return o.pluginID }

// API returns the exact generation protocol version.
func (o ExtensionOutput) API() string { return o.api }

// Package returns the selected plugin-relative generation package.
func (o ExtensionOutput) Package() string { return o.packagePath }

// Namespaces returns the activated namespaces in canonical order.
func (o ExtensionOutput) Namespaces() []string {
	return append([]string(nil), o.namespaces...)
}

// Output returns the immutable normalized helper output.
func (o ExtensionOutput) Output() generation.NormalizedOutput { return o.output }

// ExtensionResult is one immutable stable activation, provider, extension
// context, generation-derived requirement closure, semantic contribution plan,
// and final application Alias map.
type ExtensionResult struct {
	activation            Result
	generationContext     generation.Context
	outputs               []ExtensionOutput
	generatedRequirements []GeneratedRequirement
	contributions         []ResolvedContribution
	aliases               aliasresolution.Result
	passes                int
}

// ActivationResolution returns the final activation and provider closure.
func (r ExtensionResult) ActivationResolution() Result { return r.activation }

// Context returns the final immutable input supplied to extensions. Their
// Alias proposals are merged separately in AliasResolution.
func (r ExtensionResult) Context() generation.Context { return r.generationContext }

// Outputs returns defensive selected-extension output views in Plugin ID order.
func (r ExtensionResult) Outputs() []ExtensionOutput {
	return append([]ExtensionOutput(nil), r.outputs...)
}

// GeneratedRequirements returns complete provenance in canonical order.
func (r ExtensionResult) GeneratedRequirements() []GeneratedRequirement {
	return append([]GeneratedRequirement(nil), r.generatedRequirements...)
}

// Contributions returns selected-extension contributions in semantic graph
// order with complete selected-plugin provenance.
func (r ExtensionResult) Contributions() []ResolvedContribution {
	return append([]ResolvedContribution(nil), r.contributions...)
}

// AliasResolution returns the immutable final multi-source application Alias
// map. It is computed even when no generation extension is selected.
func (r ExtensionResult) AliasResolution() aliasresolution.Result { return r.aliases }

// Passes returns the number of extension execution passes. Applications with
// no selected extensions stabilize without a confirmation pass.
func (r ExtensionResult) Passes() int { return r.passes }

type extensionHelper interface {
	Generate(context.Context, generation.Context) (generation.NormalizedOutput, error)
	Close() error
}

type extensionHelperBuilder func(context.Context, generationexec.Spec, generationexec.BuildOptions) (extensionHelper, error)

// ResolveExtensions executes only extensions owned by selected activation
// providers, feeds their exact requirements back through ordinary provider
// resolution until stable, and validates the final semantic contribution plan.
func ResolveExtensions(ctx context.Context, input ExtensionInput) (ExtensionResult, error) {
	return resolveExtensions(ctx, input, func(ctx context.Context, spec generationexec.Spec, options generationexec.BuildOptions) (extensionHelper, error) {
		return generationexec.Build(ctx, spec, options)
	})
}

func resolveExtensions(ctx context.Context, input ExtensionInput, build extensionHelperBuilder) (result ExtensionResult, resolveErr error) {
	if ctx == nil {
		return ExtensionResult{}, fmt.Errorf("%w: context is nil", ErrResolveExtensions)
	}
	if build == nil {
		return ExtensionResult{}, fmt.Errorf("%w: helper builder is nil", ErrResolveExtensions)
	}
	plugins, err := indexPlugins(input.Plugins)
	if err != nil {
		return ExtensionResult{}, fmt.Errorf("%w: %w", ErrResolveExtensions, err)
	}

	helpers := make(map[string]extensionHelper)
	helperSpecs := make(map[string]string)
	defer func() {
		pluginIDs := make([]string, 0, len(helpers))
		for pluginID := range helpers {
			pluginIDs = append(pluginIDs, pluginID)
		}
		sort.Strings(pluginIDs)
		var cleanupErrors []error
		for _, pluginID := range pluginIDs {
			if err := helpers[pluginID].Close(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("plugin %q: %w", pluginID, err))
			}
		}
		if len(cleanupErrors) != 0 {
			result = ExtensionResult{}
			cleanupErr := fmt.Errorf("%w: %w", ErrExtensionExecution, errors.Join(cleanupErrors...))
			if resolveErr == nil {
				resolveErr = fmt.Errorf("%w: %w", ErrResolveExtensions, cleanupErr)
			} else {
				resolveErr = errors.Join(resolveErr, cleanupErr)
			}
		}
	}()

	requirements := cloneRequirements(input.Requirements)
	requirements, err = addApplicationHTTPRequirements(requirements, input.ApplicationHTTPExposures, input.Capabilities)
	if err != nil {
		return ExtensionResult{}, fmt.Errorf("%w: %w: %w", ErrResolveExtensions, ErrApplicationContext, err)
	}
	requirements, err = addApplicationAliasRequirements(requirements, input.ApplicationAliases, input.Capabilities)
	if err != nil {
		return ExtensionResult{}, fmt.Errorf("%w: %w: %w", ErrResolveExtensions, ErrApplicationContext, err)
	}
	pluginRequirements := make(map[pluginRequirementKey]struct{})
	localPlugins := make(map[string]struct{})
	for pluginID, plugin := range plugins {
		if plugin.Local {
			localPlugins[pluginID] = struct{}{}
		}
	}
	requirements, _, err = addSelectedPluginRequirements(requirements, plugins, localPlugins, pluginRequirements)
	if err != nil {
		return ExtensionResult{}, fmt.Errorf("%w: %w: %w", ErrResolveExtensions, ErrApplicationContext, err)
	}
	generatedByKey := make(map[string]GeneratedRequirement)
	observedOutputs := make(map[string]string)
	maximumPasses := 2*(len(input.Capabilities)+1) + 1
	for pass := 1; pass <= maximumPasses; pass++ {
		if err := ctx.Err(); err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
		}
		activation, err := Resolve(Input{
			Requirements: requirements,
			Candidates:   input.Candidates,
			Choices:      input.Choices,
			Activations:  input.Activations,
		})
		if err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
		}
		selectedPlugins := make(map[string]struct{}, len(localPlugins)+len(activation.ProviderResolution().Selections()))
		for pluginID := range localPlugins {
			selectedPlugins[pluginID] = struct{}{}
		}
		for _, selection := range activation.ProviderResolution().Selections() {
			selectedPlugins[selection.PluginID()] = struct{}{}
		}
		var addedPluginRequirements int
		requirements, addedPluginRequirements, err = addSelectedPluginRequirements(requirements, plugins, selectedPlugins, pluginRequirements)
		if err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w: %w", ErrResolveExtensions, pass, ErrApplicationContext, err)
		}
		if addedPluginRequirements != 0 {
			continue
		}
		generationContext, err := buildGenerationContext(input, plugins, activation.ProviderResolution())
		if err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w: %w", ErrResolveExtensions, pass, ErrApplicationContext, err)
		}
		selected := activation.Extensions()
		if len(selected) == 0 {
			if err := validateFinalProviderChoices(activation.ProviderResolution(), input.Candidates, input.Choices); err != nil {
				return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
			}
			aliases, err := aliasresolution.Resolve(generationContext, []ExtensionOutput{})
			if err != nil {
				return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w: %w", ErrResolveExtensions, pass, ErrAliasResolution, err)
			}
			return ExtensionResult{
				activation:        activation,
				generationContext: generationContext,
				aliases:           aliases,
				passes:            pass,
			}, nil
		}

		outputs, err := runExtensions(ctx, selected, generationContext, plugins, input.BuildOptions, helpers, helperSpecs, build)
		if err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
		}
		if err := rejectErrorDiagnostics(outputs); err != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
		}

		added := 0
		for _, output := range outputs {
			for _, requirement := range output.output.Requirements() {
				record, err := generatedRequirement(output.pluginID, requirement)
				if err != nil {
					return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
				}
				key := generatedRequirementIdentity(record)
				if _, exists := generatedByKey[key]; exists {
					continue
				}
				generatedByKey[key] = record
				requirements = append(requirements, providerresolution.Requirement{
					Capability: record.capability.String(),
					Source:     generatedRequirementSource(record),
				})
				added++
			}
		}

		generated := generatedRequirementValues(generatedByKey)
		if cycle := findDependencyCycle(activation, generated); cycle != nil {
			return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, cycle)
		}

		outputDigest := extensionOutputDigest(outputs)
		contextDigest := generationContext.Digest()
		if previous, seen := observedOutputs[contextDigest]; seen {
			if previous != outputDigest {
				return ExtensionResult{}, fmt.Errorf(
					"%w: pass %d: %w: context %s first produced %s and then %s",
					ErrResolveExtensions,
					pass,
					ErrRepeatedState,
					contextDigest,
					previous,
					outputDigest,
				)
			}
			if added == 0 {
				if err := validateFinalProviderChoices(activation.ProviderResolution(), input.Candidates, input.Choices); err != nil {
					return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
				}
				contributions, err := resolveContributionGraph(outputs)
				if err != nil {
					return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w", ErrResolveExtensions, pass, err)
				}
				aliases, err := aliasresolution.Resolve(generationContext, outputs)
				if err != nil {
					return ExtensionResult{}, fmt.Errorf("%w: pass %d: %w: %w", ErrResolveExtensions, pass, ErrAliasResolution, err)
				}
				return ExtensionResult{
					activation:            activation,
					generationContext:     generationContext,
					outputs:               outputs,
					generatedRequirements: generated,
					contributions:         contributions,
					aliases:               aliases,
					passes:                pass,
				}, nil
			}
		} else {
			observedOutputs[contextDigest] = outputDigest
		}
	}
	return ExtensionResult{}, fmt.Errorf(
		"%w: %w after %d passes across %d visible canonical Capabilities",
		ErrResolveExtensions,
		ErrExtensionConvergence,
		maximumPasses,
		len(input.Capabilities),
	)
}

// validateFinalProviderChoices rejects explicit choices that never became
// applicable after the complete activation and Generation Extension closure.
// Choices remain dormant during intermediate passes because a later derived
// requirement may activate them, but no stale or invented choice may survive
// the final application model.
func validateFinalProviderChoices(resolution providerresolution.Result, candidates []providerresolution.Candidate, choices []providerresolution.Choice) error {
	capabilities := resolution.Capabilities()
	required := make(map[capabilityid.Identifier]struct{}, len(capabilities))
	for _, capability := range capabilities {
		required[capability.ID()] = struct{}{}
	}
	applicable := 0
	for _, choice := range choices {
		id, err := capabilityid.Parse(choice.Capability)
		if err != nil {
			// Invalid identifiers are retained by choicesForRequirements and have
			// already failed the provider-resolution pass.
			applicable++
			continue
		}
		if _, exists := required[id]; exists {
			applicable++
		}
	}
	if applicable == len(choices) {
		return nil
	}
	requirements := make([]providerresolution.Requirement, 0, len(capabilities))
	for _, capability := range capabilities {
		sources := capability.Sources()
		if len(sources) == 0 {
			sources = []string{"final application requirement " + capability.ID().String()}
		}
		for _, source := range sources {
			requirements = append(requirements, providerresolution.Requirement{
				Contract: capability.ContractJSON(),
				Source:   source,
			})
		}
	}
	if _, err := providerresolution.Resolve(providerresolution.Input{
		Requirements: requirements,
		Candidates:   candidates,
		Choices:      choices,
	}); err != nil {
		return err
	}
	return fmt.Errorf("%w: dormant explicit Provider choice survived final requirement closure", ErrApplicationContext)
}

type pluginRequirementKey struct {
	pluginID   string
	capability capabilityid.Identifier
}

func addSelectedPluginRequirements(
	requirements []providerresolution.Requirement,
	plugins map[string]Plugin,
	selected map[string]struct{},
	added map[pluginRequirementKey]struct{},
) ([]providerresolution.Requirement, int, error) {
	pluginIDs := make([]string, 0, len(selected))
	for pluginID := range selected {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	result := requirements
	count := 0
	for _, pluginID := range pluginIDs {
		plugin, exists := plugins[pluginID]
		if !exists {
			return nil, 0, fmt.Errorf("selected plugin %q has no normalized plugin provenance", pluginID)
		}
		declared := append([]string(nil), plugin.Context.Requires...)
		sort.Strings(declared)
		for index, value := range declared {
			capability, err := capabilityid.Parse(value)
			if err != nil {
				return nil, 0, fmt.Errorf("plugin %q requires non-canonical Capability %q", pluginID, value)
			}
			if index > 0 && declared[index-1] == value {
				return nil, 0, fmt.Errorf("plugin %q duplicates required Capability %s", pluginID, capability)
			}
			key := pluginRequirementKey{pluginID: pluginID, capability: capability}
			if _, exists := added[key]; exists {
				continue
			}
			added[key] = struct{}{}
			result = append(result, providerresolution.Requirement{
				Capability: capability.String(),
				Source:     selectedPluginRequirementSource(pluginID, plugin, capability),
			})
			count++
		}
	}
	return result, count, nil
}

func selectedPluginRequirementSource(pluginID string, plugin Plugin, capability capabilityid.Identifier) string {
	version := plugin.Context.ModuleVersion
	if version == "" {
		version = "local"
	}
	source := plugin.Context.ModulePath + "@" + version
	if plugin.PluginPath != "" {
		source = path.Join(source, plugin.PluginPath, "plugin.yaml")
	}
	value := "plugin " + pluginID + " at " + source + " requires " + capability.String()
	if len(value) <= maximumRequirementSourceSize {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "...#sha256:" + hex.EncodeToString(sum[:])
	return value[:maximumRequirementSourceSize-len(suffix)] + suffix
}

func indexPlugins(inputs []Plugin) (map[string]Plugin, error) {
	plugins := make(map[string]Plugin, len(inputs))
	for index, input := range inputs {
		id, err := generation.ParsePluginID(input.Context.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: plugins[%d].context.id %q is not canonical", ErrApplicationContext, index, input.Context.ID)
		}
		if _, duplicate := plugins[id.String()]; duplicate {
			return nil, fmt.Errorf("%w: plugins[%d] duplicates plugin %q", ErrApplicationContext, index, id.String())
		}
		input.Context.Provides = append([]string(nil), input.Context.Provides...)
		input.Context.Requires = append([]string(nil), input.Context.Requires...)
		input.Context.BuildMetadataJSON = append([]byte(nil), input.Context.BuildMetadataJSON...)
		plugins[id.String()] = input
	}
	return plugins, nil
}

func buildGenerationContext(input ExtensionInput, plugins map[string]Plugin, resolution providerresolution.Result) (generation.Context, error) {
	selectedPluginIDs := make(map[string]struct{})
	for pluginID, plugin := range plugins {
		if plugin.Local {
			selectedPluginIDs[pluginID] = struct{}{}
		}
	}
	providerInputs := make([]generation.ProviderInput, 0, len(resolution.Selections()))
	for _, selection := range resolution.Selections() {
		selectedPluginIDs[selection.PluginID()] = struct{}{}
		providerInputs = append(providerInputs, generation.ProviderInput{
			Capability: selection.Capability().String(),
			Plugin:     selection.PluginID(),
		})
	}
	pluginIDs := make([]string, 0, len(selectedPluginIDs))
	for pluginID := range selectedPluginIDs {
		pluginIDs = append(pluginIDs, pluginID)
	}
	sort.Strings(pluginIDs)
	pluginInputs := make([]generation.PluginInput, len(pluginIDs))
	for index, pluginID := range pluginIDs {
		plugin, exists := plugins[pluginID]
		if !exists {
			return generation.Context{}, fmt.Errorf("selected provider plugin %q has no normalized plugin provenance", pluginID)
		}
		pluginInputs[index] = clonePluginInput(plugin.Context)
	}

	capabilities := resolution.Capabilities()
	requirementIDs := make([]string, len(capabilities))
	for index, capability := range capabilities {
		requirementIDs[index] = capability.ID().String()
	}
	capabilityInputs, err := applyApplicationHTTPExposure(input.Capabilities, input.ApplicationHTTPExposures)
	if err != nil {
		return generation.Context{}, err
	}
	contextInput := generation.Input{
		Plugins:      pluginInputs,
		Capabilities: capabilityInputs,
		Requirements: requirementIDs,
		Providers:    providerInputs,
	}
	generationContext, err := generation.NewContext(contextInput)
	if err != nil {
		return generation.Context{}, err
	}
	for _, capability := range capabilities {
		id, err := generation.ParseCapabilityID(capability.ID().String())
		if err != nil {
			return generation.Context{}, fmt.Errorf("resolved Capability %s cannot enter the generation API: %v", capability.ID(), err)
		}
		view, exists := generationContext.Capability(id)
		if !exists {
			return generation.Context{}, fmt.Errorf("resolved Capability %s is absent from the visible generation catalog", capability.ID())
		}
		if view.Intrinsic() != capability.Intrinsic() || !bytes.Equal(view.ContractJSON(), capability.ContractJSON()) {
			return generation.Context{}, fmt.Errorf("resolved Capability %s differs from the visible generation catalog contract", capability.ID())
		}
	}
	aliases, err := aliasresolution.NormalizeApplication(generationContext, input.ApplicationAliases)
	if err != nil {
		return generation.Context{}, err
	}
	if len(aliases) == 0 {
		return generationContext, nil
	}
	contextInput.CapabilityAliases = aliases
	generationContext, err = generation.NewContext(contextInput)
	if err != nil {
		return generation.Context{}, err
	}
	return generationContext, nil
}

func addApplicationHTTPRequirements(
	requirements []providerresolution.Requirement,
	exposures []applicationmeta.HTTPExposure,
	capabilities []generation.CapabilityInput,
) ([]providerresolution.Requirement, error) {
	if len(exposures) == 0 {
		return requirements, nil
	}
	catalog, err := generation.NewContext(generation.Input{Capabilities: cloneCapabilityInputs(capabilities)})
	if err != nil {
		return nil, err
	}
	result := append([]providerresolution.Requirement(nil), requirements...)
	for _, exposure := range exposures {
		id, err := generation.ParseCapabilityID(exposure.ID().String())
		if err != nil {
			return nil, fmt.Errorf("%s ID %q is not canonical", exposure.Source(), exposure.ID())
		}
		capability, exists := catalog.Capability(id)
		if !exists {
			return nil, fmt.Errorf("%s Capability %s is absent from the visible canonical catalog", exposure.Source(), id)
		}
		result = append(result, providerresolution.Requirement{
			Capability: id.String(),
			Contract:   capability.ContractJSON(),
			Source:     exposure.Source(),
		})
	}
	return result, nil
}

func applyApplicationHTTPExposure(
	capabilities []generation.CapabilityInput,
	exposures []applicationmeta.HTTPExposure,
) ([]generation.CapabilityInput, error) {
	result := cloneCapabilityInputs(capabilities)
	if len(exposures) == 0 {
		return result, nil
	}
	byID := make(map[string]int, len(result))
	for index, capability := range result {
		metadata, err := capabilitymeta.Parse(capability.ContractJSON)
		if err != nil {
			return nil, fmt.Errorf("capabilities[%d] cannot supply HTTP exposure: %v", index, err)
		}
		byID[metadata.ID().String()] = index
	}
	for _, exposure := range exposures {
		index, exists := byID[exposure.ID().String()]
		if !exists {
			return nil, fmt.Errorf("%s Capability %s is absent from the visible canonical catalog", exposure.Source(), exposure.ID())
		}
		result[index].Exposure.HTTP = true
		result[index].Exposure.JavaScript = true
	}
	return result, nil
}

func addApplicationAliasRequirements(
	requirements []providerresolution.Requirement,
	aliases []applicationmeta.Alias,
	capabilities []generation.CapabilityInput,
) ([]providerresolution.Requirement, error) {
	if len(aliases) == 0 {
		return requirements, nil
	}
	catalog, err := generation.NewContext(generation.Input{Capabilities: cloneCapabilityInputs(capabilities)})
	if err != nil {
		return nil, err
	}
	result := append([]providerresolution.Requirement(nil), requirements...)
	for _, alias := range aliases {
		targetID, err := generation.ParseCapabilityID(alias.Target().String())
		if err != nil {
			return nil, fmt.Errorf("%s target %q is not canonical", alias.Source(), alias.Target())
		}
		requirement := providerresolution.Requirement{
			Capability: targetID.String(),
			Source:     alias.Source() + " target",
		}
		if target, exists := catalog.Capability(targetID); exists {
			requirement.Contract = target.ContractJSON()
		}
		result = append(result, requirement)
	}
	return result, nil
}

func clonePluginInput(input generation.PluginInput) generation.PluginInput {
	input.Provides = append([]string(nil), input.Provides...)
	input.Requires = append([]string(nil), input.Requires...)
	input.BuildMetadataJSON = append([]byte(nil), input.BuildMetadataJSON...)
	return input
}

func cloneCapabilityInputs(inputs []generation.CapabilityInput) []generation.CapabilityInput {
	result := make([]generation.CapabilityInput, len(inputs))
	for index, input := range inputs {
		result[index] = input
		result[index].ContractJSON = append([]byte(nil), input.ContractJSON...)
		result[index].Sources = append([]string(nil), input.Sources...)
	}
	return result
}

func runExtensions(
	ctx context.Context,
	selected []SelectedExtension,
	generationContext generation.Context,
	plugins map[string]Plugin,
	baseOptions generationexec.BuildOptions,
	helpers map[string]extensionHelper,
	helperSpecs map[string]string,
	build extensionHelperBuilder,
) ([]ExtensionOutput, error) {
	outputs := make([]ExtensionOutput, 0, len(selected))
	for _, extension := range selected {
		plugin, exists := plugins[extension.PluginID()]
		if !exists {
			return nil, fmt.Errorf("%w: plugin %q selected from %q has no module provenance", ErrExtensionExecution, extension.PluginID(), extension.Source())
		}
		spec := generationexec.Spec{
			PluginID:   extension.PluginID(),
			API:        extension.API(),
			ModulePath: plugin.Context.ModulePath,
			PluginPath: plugin.PluginPath,
			Package:    extension.Package(),
			Namespaces: extension.Namespaces(),
		}
		key := extensionSpecKey(spec)
		helper, exists := helpers[extension.PluginID()]
		if exists && helperSpecs[extension.PluginID()] != key {
			return nil, fmt.Errorf("%w: selected plugin %q changed generation helper identity between passes", ErrExtensionExecution, extension.PluginID())
		}
		if !exists {
			options := baseOptions
			if plugin.ModuleRoot != "" {
				options.ModuleRoot = plugin.ModuleRoot
			}
			var err error
			helper, err = build(ctx, spec, options)
			if err != nil {
				return nil, fmt.Errorf("%w: plugin %q declaration %q: %w", ErrExtensionExecution, extension.PluginID(), extension.Source(), err)
			}
			if helper == nil {
				return nil, fmt.Errorf("%w: plugin %q declaration %q: helper builder returned nil", ErrExtensionExecution, extension.PluginID(), extension.Source())
			}
			helpers[extension.PluginID()] = helper
			helperSpecs[extension.PluginID()] = key
		}
		normalized, err := helper.Generate(ctx, generationContext)
		if err != nil {
			return nil, fmt.Errorf("%w: plugin %q declaration %q: %w", ErrExtensionExecution, extension.PluginID(), extension.Source(), err)
		}
		if err := validateExtensionProvenance(extension, normalized); err != nil {
			return nil, err
		}
		outputs = append(outputs, ExtensionOutput{
			pluginID:    extension.PluginID(),
			api:         extension.API(),
			packagePath: extension.Package(),
			namespaces:  extension.Namespaces(),
			output:      normalized,
		})
	}
	return outputs, nil
}

func extensionSpecKey(spec generationexec.Spec) string {
	return strings.Join([]string{
		spec.PluginID,
		spec.API,
		spec.ModulePath,
		spec.PluginPath,
		spec.Package,
		strings.Join(spec.Namespaces, ","),
	}, "\x00")
}

func validateExtensionProvenance(extension SelectedExtension, output generation.NormalizedOutput) error {
	allowed := make(map[string]map[string]struct{})
	for _, activation := range extension.Activations() {
		sources := allowed[activation.Namespace()]
		if sources == nil {
			sources = make(map[string]struct{})
			allowed[activation.Namespace()] = sources
		}
		for _, use := range activation.Uses() {
			sources[use.SourceCapability().String()] = struct{}{}
		}
	}
	for _, requirement := range output.Requirements() {
		if !allowedOutputSource(allowed, requirement.Namespace, requirement.Source.String()) {
			return fmt.Errorf(
				"%w: plugin %q rule %q returned requirement %s for extensions.%s on %s, which was not one of its selected activation inputs",
				ErrExtensionProvenance,
				extension.PluginID(),
				requirement.RuleID,
				requirement.Capability.String(),
				requirement.Namespace,
				requirement.Source.String(),
			)
		}
	}
	for _, diagnostic := range output.Diagnostics() {
		if !allowedOutputSource(allowed, diagnostic.Namespace, diagnostic.Source.String()) {
			return fmt.Errorf(
				"%w: plugin %q diagnostic %q rule %q names extensions.%s on %s, which was not one of its selected activation inputs",
				ErrExtensionProvenance,
				extension.PluginID(),
				diagnostic.Code,
				diagnostic.RuleID,
				diagnostic.Namespace,
				diagnostic.Source.String(),
			)
		}
	}
	for _, contribution := range output.Contributions() {
		if !allowedOutputSource(allowed, contribution.Namespace(), contribution.Source().String()) {
			return fmt.Errorf(
				"%w: plugin %q contribution %q names extensions.%s on %s, which was not one of its selected activation inputs",
				ErrExtensionProvenance,
				extension.PluginID(),
				contribution.ID(),
				contribution.Namespace(),
				contribution.Source().String(),
			)
		}
	}
	for _, contribution := range output.AliasContributions() {
		if !allowedOutputSource(allowed, contribution.Namespace(), contribution.Source().String()) {
			return fmt.Errorf(
				"%w: plugin %q Alias contribution %q for %s -> %s names extensions.%s on %s, which was not one of its selected activation inputs",
				ErrExtensionProvenance,
				extension.PluginID(),
				contribution.ID(),
				contribution.Alias().String(),
				contribution.Target().String(),
				contribution.Namespace(),
				contribution.Source().String(),
			)
		}
	}
	return nil
}

func allowedOutputSource(allowed map[string]map[string]struct{}, namespace, source string) bool {
	sources, exists := allowed[namespace]
	if !exists {
		return false
	}
	_, exists = sources[source]
	return exists
}

func rejectErrorDiagnostics(outputs []ExtensionOutput) error {
	var diagnostics []string
	for _, output := range outputs {
		for _, diagnostic := range output.output.Diagnostics() {
			if diagnostic.Severity != generation.DiagnosticError {
				continue
			}
			diagnostics = append(diagnostics, fmt.Sprintf(
				"plugin %q diagnostic %q rule %q extensions.%s on %s: %s",
				output.pluginID,
				diagnostic.Code,
				diagnostic.RuleID,
				diagnostic.Namespace,
				diagnostic.Source.String(),
				diagnostic.Message,
			))
		}
	}
	if len(diagnostics) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrExtensionDiagnostic, strings.Join(diagnostics, "; "))
}

func generatedRequirement(pluginID string, requirement generation.Requirement) (GeneratedRequirement, error) {
	source, err := capabilityid.Parse(requirement.Source.String())
	if err != nil {
		return GeneratedRequirement{}, fmt.Errorf("%w: plugin %q rule %q has invalid source %q", ErrExtensionProvenance, pluginID, requirement.RuleID, requirement.Source.String())
	}
	capability, err := capabilityid.Parse(requirement.Capability.String())
	if err != nil {
		return GeneratedRequirement{}, fmt.Errorf("%w: plugin %q rule %q has invalid requirement %q", ErrExtensionProvenance, pluginID, requirement.RuleID, requirement.Capability.String())
	}
	return GeneratedRequirement{
		pluginID:   pluginID,
		namespace:  requirement.Namespace,
		source:     source,
		ruleID:     requirement.RuleID,
		capability: capability,
	}, nil
}

func generatedRequirementIdentity(requirement GeneratedRequirement) string {
	return strings.Join([]string{
		requirement.pluginID,
		requirement.namespace,
		requirement.source.String(),
		requirement.ruleID,
		requirement.capability.String(),
	}, "\x00")
}

func generatedRequirementValues(values map[string]GeneratedRequirement) []GeneratedRequirement {
	result := make([]GeneratedRequirement, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return generatedRequirementIdentity(result[left]) < generatedRequirementIdentity(result[right])
	})
	return result
}

func generatedRequirementSource(requirement GeneratedRequirement) string {
	value := fmt.Sprintf(
		"generation plugin %q rule %q extensions.%s on %s",
		requirement.pluginID,
		requirement.ruleID,
		requirement.namespace,
		requirement.source,
	)
	if len(value) <= maximumRequirementSourceSize {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	suffix := "...#sha256:" + hex.EncodeToString(sum[:])
	return value[:maximumRequirementSourceSize-len(suffix)] + suffix
}

func extensionOutputDigest(outputs []ExtensionOutput) string {
	hash := sha256.New()
	for _, output := range outputs {
		_, _ = hash.Write([]byte(output.pluginID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(output.output.Digest()))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// DependencyEdgeKind identifies one edge in a mixed activation/generated
// requirement path.
type DependencyEdgeKind string

const (
	// DependencyActivation is introduced by extensions.<namespace> metadata.
	DependencyActivation DependencyEdgeKind = "activation"
	// DependencyGenerated is returned by a selected extension rule.
	DependencyGenerated DependencyEdgeKind = "generated"
)

// DependencyEdge is one deterministic canonical Capability dependency.
type DependencyEdge struct {
	kind               DependencyEdgeKind
	source             capabilityid.Identifier
	target             capabilityid.Identifier
	namespace          string
	pluginID           string
	ruleID             string
	requirementSources []string
}

// Kind returns activation or generated.
func (e DependencyEdge) Kind() DependencyEdgeKind { return e.kind }

// Source returns the metadata-bearing or rule-source Capability.
func (e DependencyEdge) Source() capabilityid.Identifier { return e.source }

// Target returns the required activation or generated Capability.
func (e DependencyEdge) Target() capabilityid.Identifier { return e.target }

// Namespace returns the interpreted extension namespace.
func (e DependencyEdge) Namespace() string { return e.namespace }

// PluginID returns the selected extension provider.
func (e DependencyEdge) PluginID() string { return e.pluginID }

// RuleID returns the generation rule for generated edges.
func (e DependencyEdge) RuleID() string { return e.ruleID }

// RequirementSources returns root/source provenance for activation edges.
func (e DependencyEdge) RequirementSources() []string {
	return append([]string(nil), e.requirementSources...)
}

// DependencyCycleError contains one complete closed mixed dependency path.
type DependencyCycleError struct {
	edges []DependencyEdge
}

// Edges returns defensive path entries in traversal order.
func (e *DependencyCycleError) Edges() []DependencyEdge {
	if e == nil {
		return nil
	}
	return append([]DependencyEdge(nil), e.edges...)
}

func (e *DependencyCycleError) Error() string {
	if e == nil {
		return ErrDependencyCycle.Error()
	}
	var message strings.Builder
	message.WriteString(ErrDependencyCycle.Error())
	message.WriteString(": ")
	for index, edge := range e.edges {
		if index == 0 {
			message.WriteString(edge.source.String())
		}
		switch edge.kind {
		case DependencyActivation:
			fmt.Fprintf(
				&message,
				" --activation extensions.%s via selected plugin %q from [%s]--> %s",
				edge.namespace,
				edge.pluginID,
				strings.Join(edge.requirementSources, ", "),
				edge.target,
			)
		case DependencyGenerated:
			fmt.Fprintf(
				&message,
				" --generated by plugin %q rule %q extensions.%s--> %s",
				edge.pluginID,
				edge.ruleID,
				edge.namespace,
				edge.target,
			)
		}
	}
	message.WriteString("; correction: remove or version the generation dependency cycle; execution order cannot make a canonical requirement cycle valid")
	return message.String()
}

// Unwrap supports errors.Is with ErrDependencyCycle.
func (*DependencyCycleError) Unwrap() error { return ErrDependencyCycle }

func findDependencyCycle(activation Result, generated []GeneratedRequirement) *DependencyCycleError {
	edges := make([]DependencyEdge, 0)
	resolution := activation.ProviderResolution()
	for _, requirement := range activation.ActivationRequirements().Requirements() {
		provider, _ := resolution.SelectedProvider(requirement.Capability())
		for _, use := range requirement.Uses() {
			edges = append(edges, DependencyEdge{
				kind:               DependencyActivation,
				source:             use.SourceCapability(),
				target:             requirement.Capability(),
				namespace:          use.Namespace(),
				pluginID:           provider.PluginID(),
				requirementSources: use.RequirementSources(),
			})
		}
	}
	for _, requirement := range generated {
		edges = append(edges, DependencyEdge{
			kind:      DependencyGenerated,
			source:    requirement.source,
			target:    requirement.capability,
			namespace: requirement.namespace,
			pluginID:  requirement.pluginID,
			ruleID:    requirement.ruleID,
		})
	}

	adjacency := make(map[capabilityid.Identifier][]DependencyEdge)
	nodeSet := make(map[capabilityid.Identifier]struct{})
	seen := make(map[string]struct{})
	for _, edge := range edges {
		key := dependencyEdgeKey(edge)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		adjacency[edge.source] = append(adjacency[edge.source], edge)
		nodeSet[edge.source] = struct{}{}
		nodeSet[edge.target] = struct{}{}
	}
	nodes := make([]capabilityid.Identifier, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].String() < nodes[right].String() })
	for node, nodeEdges := range adjacency {
		sort.Slice(nodeEdges, func(left, right int) bool {
			return dependencyEdgeKey(nodeEdges[left]) < dependencyEdgeKey(nodeEdges[right])
		})
		adjacency[node] = nodeEdges
	}

	const (
		unvisited uint8 = iota
		visiting
		visited
	)
	state := make(map[capabilityid.Identifier]uint8, len(nodes))
	stackNodes := make([]capabilityid.Identifier, 0, len(nodes))
	stackEdges := make([]DependencyEdge, 0, len(nodes))
	var cycle []DependencyEdge
	var visit func(capabilityid.Identifier) bool
	visit = func(node capabilityid.Identifier) bool {
		state[node] = visiting
		stackNodes = append(stackNodes, node)
		for _, edge := range adjacency[node] {
			switch state[edge.target] {
			case unvisited:
				stackEdges = append(stackEdges, edge)
				if visit(edge.target) {
					return true
				}
				stackEdges = stackEdges[:len(stackEdges)-1]
			case visiting:
				position := len(stackNodes) - 1
				for position >= 0 && stackNodes[position] != edge.target {
					position--
				}
				cycle = append([]DependencyEdge(nil), stackEdges[position:]...)
				cycle = append(cycle, edge)
				return true
			}
		}
		stackNodes = stackNodes[:len(stackNodes)-1]
		state[node] = visited
		return false
	}
	for _, node := range nodes {
		if state[node] == unvisited && visit(node) {
			return &DependencyCycleError{edges: cycle}
		}
	}
	return nil
}

func dependencyEdgeKey(edge DependencyEdge) string {
	return strings.Join([]string{
		edge.source.String(),
		edge.target.String(),
		string(edge.kind),
		edge.namespace,
		edge.pluginID,
		edge.ruleID,
		strings.Join(edge.requirementSources, "\x01"),
	}, "\x00")
}
