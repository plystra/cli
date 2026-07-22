package resolutionevidence

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/providerresolution"
)

func staticAssemblyFromInput(
	input *StaticAssemblyInput,
	context generation.Context,
	selectedPlugins []SelectedPlugin,
	selectedProviders []SelectedProvider,
	requirements []CapabilityRequirement,
) (StaticAssembly, bool, error) {
	if input == nil {
		return StaticAssembly{}, false, nil
	}
	bindings, err := assemblyBindingsFromModel(selectedProviders, requirements)
	if err != nil {
		return StaticAssembly{}, false, err
	}
	plugins, err := assemblyPluginsFromInput(input.Plugins, context, selectedPlugins, bindings, requirements)
	if err != nil {
		return StaticAssembly{}, false, err
	}
	assembly := StaticAssembly{plugins: plugins, bindings: bindings}
	if err := validateStaticAssemblyState(assembly, true, selectedPlugins, selectedProviders, requirements); err != nil {
		return StaticAssembly{}, false, err
	}
	return assembly, true, nil
}

func assemblyBindingsFromModel(selected []SelectedProvider, requirements []CapabilityRequirement) ([]AssemblyBinding, error) {
	requirementByCapability := make(map[string]CapabilityRequirement, len(requirements))
	for _, requirement := range requirements {
		requirementByCapability[requirement.capability] = requirement
	}
	selectedByCapability := make(map[string]SelectedProvider, len(selected))
	for _, provider := range selected {
		selectedByCapability[provider.capability] = provider
	}

	definitions := intrinsiccatalog.Definitions()
	bindings := make([]AssemblyBinding, 0, len(definitions)+len(selected))
	for _, definition := range definitions {
		id := definition.ID()
		requirement, required := requirementByCapability[id.String()]
		if required && (!requirement.intrinsic || requirement.contractDigest != definition.ContractDigest()) {
			return nil, fmt.Errorf("intrinsic assembly binding %s disagrees with its requirement", id)
		}
		if required {
			provider, exists := selectedByCapability[id.String()]
			if !exists || provider.selectionReason != ProviderSelectionIntrinsic || provider.contractDigest != definition.ContractDigest() {
				return nil, fmt.Errorf("intrinsic assembly binding %s has no matching selected implementation", id)
			}
		}
		bindings = append(bindings, AssemblyBinding{
			capability:      id.String(),
			contractDigest:  definition.ContractDigest(),
			intrinsic:       true,
			required:        required,
			selectionReason: ProviderSelectionIntrinsic,
			providerSource: Source{
				module: "github.com/plystra/kernel",
				path:   intrinsicProviderPath(id),
				kind:   "intrinsic-provider",
				line:   1,
				column: 1,
			},
		})
	}
	for _, provider := range selected {
		if provider.selectionReason == ProviderSelectionIntrinsic {
			continue
		}
		requirement, exists := requirementByCapability[provider.capability]
		if !exists || requirement.intrinsic || requirement.contractDigest != provider.contractDigest {
			return nil, fmt.Errorf("ordinary assembly binding %s disagrees with its requirement", provider.capability)
		}
		bindings = append(bindings, AssemblyBinding{
			capability:      provider.capability,
			contractDigest:  provider.contractDigest,
			required:        true,
			pluginID:        provider.pluginID,
			projectModule:   provider.projectModule,
			selectionReason: provider.selectionReason,
			providerSource:  provider.providerSource,
		})
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].capability < bindings[right].capability })
	return bindings, nil
}

func assemblyPluginsFromInput(
	inputs []AssemblyPluginInput,
	context generation.Context,
	selected []SelectedPlugin,
	bindings []AssemblyBinding,
	requirements []CapabilityRequirement,
) ([]AssemblyPlugin, error) {
	inputByPlugin := make(map[string]AssemblyPluginInput, len(inputs))
	for index, input := range inputs {
		if err := pluginid.Validate(input.PluginID); err != nil {
			return nil, fmt.Errorf("assembly plugins[%d].plugin_id %q is invalid", index, input.PluginID)
		}
		if _, duplicate := inputByPlugin[input.PluginID]; duplicate {
			return nil, fmt.Errorf("assembly plugins[%d] duplicates Plugin %q", index, input.PluginID)
		}
		inputByPlugin[input.PluginID] = input
	}
	if len(inputByPlugin) != len(selected) {
		return nil, fmt.Errorf("assembly constructor inputs %d do not match selected Plugins %d", len(inputByPlugin), len(selected))
	}
	viewByPlugin := make(map[string]generation.PluginView, len(context.Plugins()))
	for _, plugin := range context.Plugins() {
		viewByPlugin[plugin.ID().String()] = plugin
	}
	bindingByCapability := make(map[string]AssemblyBinding, len(bindings))
	providerBindings := make(map[string][]string)
	for _, binding := range bindings {
		bindingByCapability[binding.capability] = binding
		if !binding.intrinsic {
			providerBindings[binding.pluginID] = append(providerBindings[binding.pluginID], binding.capability)
		}
	}

	plugins := make([]AssemblyPlugin, len(selected))
	for index, plugin := range selected {
		input, exists := inputByPlugin[plugin.id]
		if !exists {
			return nil, fmt.Errorf("selected Plugin %q has no assembly constructor input", plugin.id)
		}
		expectedImport := path.Join(plugin.modulePath, plugin.path)
		if input.ModulePath != plugin.modulePath || input.ModuleVersion != plugin.moduleVersion || input.ImportPath != expectedImport {
			return nil, fmt.Errorf("selected Plugin %q assembly package %q@%q/%q does not match %q@%q/%q", plugin.id, input.ModulePath, input.ModuleVersion, input.ImportPath, plugin.modulePath, plugin.moduleVersion, expectedImport)
		}
		view, exists := viewByPlugin[plugin.id]
		if !exists || view.Module().Path() != plugin.modulePath || view.Module().Version() != plugin.moduleVersion {
			return nil, fmt.Errorf("selected Plugin %q is absent or inconsistent in the normalized model", plugin.id)
		}
		requiredClients := capabilityViewIDs(view.Requires())
		for _, capability := range requiredClients {
			if _, exists := bindingByCapability[capability]; !exists {
				return nil, fmt.Errorf("selected Plugin %q required client %s is not an assembled canonical binding", plugin.id, capability)
			}
		}
		bound := append([]string(nil), providerBindings[plugin.id]...)
		sort.Strings(bound)
		provided := make(map[string]struct{}, len(view.Provides()))
		for _, capability := range view.Provides() {
			provided[capability.String()] = struct{}{}
		}
		for _, capability := range bound {
			if _, exists := provided[capability]; !exists {
				return nil, fmt.Errorf("selected Plugin %q assembly binding %s is not declared under provides", plugin.id, capability)
			}
		}
		plugins[index] = AssemblyPlugin{
			pluginID:         plugin.id,
			projectModule:    plugin.modulePath,
			moduleVersion:    plugin.moduleVersion,
			importPath:       input.ImportPath,
			source:           plugin.source,
			constructorOrder: index + 1,
			lifecycleProbe:   true,
			requiredClients:  requiredClients,
			providerBindings: bound,
		}
	}
	if err := validateAssemblyPlugins(plugins, selected, bindings, requirements); err != nil {
		return nil, err
	}
	return plugins, nil
}

func capabilityViewIDs(values []generation.CapabilityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	sort.Strings(result)
	return result
}

func validateStaticAssemblyState(
	assembly StaticAssembly,
	exists bool,
	selectedPlugins []SelectedPlugin,
	selectedProviders []SelectedProvider,
	requirements []CapabilityRequirement,
) error {
	if !exists {
		if len(assembly.plugins) != 0 || len(assembly.bindings) != 0 {
			return errors.New("absent static assembly carries membership state")
		}
		return nil
	}
	if err := validateAssemblyBindings(assembly.bindings, selectedProviders, requirements); err != nil {
		return err
	}
	return validateAssemblyPlugins(assembly.plugins, selectedPlugins, assembly.bindings, requirements)
}

func validateAssemblyBindings(bindings []AssemblyBinding, selected []SelectedProvider, requirements []CapabilityRequirement) error {
	requirementByCapability := make(map[string]CapabilityRequirement, len(requirements))
	for _, requirement := range requirements {
		requirementByCapability[requirement.capability] = requirement
	}
	selectedByCapability := make(map[string]SelectedProvider, len(selected))
	ordinarySelected := 0
	for _, provider := range selected {
		selectedByCapability[provider.capability] = provider
		if provider.selectionReason != ProviderSelectionIntrinsic {
			ordinarySelected++
		}
	}
	definitions := intrinsiccatalog.Definitions()
	definitionByCapability := make(map[string]intrinsiccatalog.Definition, len(definitions))
	for _, definition := range definitions {
		definitionByCapability[definition.ID().String()] = definition
	}
	if len(bindings) != len(definitions)+ordinarySelected {
		return fmt.Errorf("assembly bindings %d do not match %d intrinsics plus %d ordinary selections", len(bindings), len(definitions), ordinarySelected)
	}
	seen := make(map[string]struct{}, len(bindings))
	for index, binding := range bindings {
		id, err := capabilityid.Parse(binding.capability)
		if err != nil || !validDigest(binding.contractDigest) {
			return fmt.Errorf("assembly bindings[%d] has an invalid Capability or contract digest", index)
		}
		if index > 0 && bindings[index-1].capability >= binding.capability {
			return fmt.Errorf("assembly bindings are not in unique canonical order at %s", id)
		}
		if _, duplicate := seen[binding.capability]; duplicate {
			return fmt.Errorf("assembly bindings repeat %s", id)
		}
		seen[binding.capability] = struct{}{}
		requirement, required := requirementByCapability[binding.capability]
		if binding.required != required {
			return fmt.Errorf("assembly binding %s has an inconsistent requirement marker", id)
		}
		if binding.intrinsic {
			definition, exists := definitionByCapability[binding.capability]
			expectedSource := Source{module: "github.com/plystra/kernel", path: intrinsicProviderPath(id), kind: "intrinsic-provider", line: 1, column: 1}
			if !exists || definition.ContractDigest() != binding.contractDigest || binding.pluginID != "" || binding.projectModule != "" || binding.selectionReason != ProviderSelectionIntrinsic || binding.providerSource != expectedSource {
				return fmt.Errorf("assembly binding %s has invalid intrinsic membership", id)
			}
			if required && (!requirement.intrinsic || selectedByCapability[binding.capability].selectionReason != ProviderSelectionIntrinsic) {
				return fmt.Errorf("assembly binding %s disagrees with its intrinsic requirement", id)
			}
			continue
		}
		if strings.HasPrefix(id.Name(), "kernel.") || !required || requirement.intrinsic {
			return fmt.Errorf("assembly binding %s has invalid ordinary membership", id)
		}
		provider, exists := selectedByCapability[binding.capability]
		if !exists || provider.selectionReason == ProviderSelectionIntrinsic || provider.contractDigest != binding.contractDigest || provider.pluginID != binding.pluginID || provider.projectModule != binding.projectModule || provider.selectionReason != binding.selectionReason || provider.providerSource != binding.providerSource {
			return fmt.Errorf("assembly binding %s does not match its selected Provider", id)
		}
		if err := pluginid.Validate(binding.pluginID); err != nil || binding.projectModule == "" {
			return fmt.Errorf("assembly binding %s has invalid ordinary Provider identity", id)
		}
	}
	for _, definition := range definitions {
		if _, exists := seen[definition.ID().String()]; !exists {
			return fmt.Errorf("assembly bindings omit intrinsic %s", definition.ID())
		}
	}
	for _, provider := range selected {
		if provider.selectionReason != ProviderSelectionIntrinsic {
			if _, exists := seen[provider.capability]; !exists {
				return fmt.Errorf("assembly bindings omit selected Provider %s", provider.capability)
			}
		}
	}
	return nil
}

func validateAssemblyPlugins(plugins []AssemblyPlugin, selected []SelectedPlugin, bindings []AssemblyBinding, requirements []CapabilityRequirement) error {
	if len(plugins) != len(selected) {
		return fmt.Errorf("assembly plugins %d do not match selected Plugins %d", len(plugins), len(selected))
	}
	expectedClients := make(map[string][]string)
	for _, requirement := range requirements {
		for _, source := range requirement.sources {
			if source.kind == providerresolution.RequirementPlugin {
				expectedClients[source.pluginID] = append(expectedClients[source.pluginID], requirement.capability)
			}
		}
	}
	for plugin := range expectedClients {
		sort.Strings(expectedClients[plugin])
		expectedClients[plugin] = uniqueStrings(expectedClients[plugin])
	}
	expectedBindings := make(map[string][]string)
	for _, binding := range bindings {
		if !binding.intrinsic {
			expectedBindings[binding.pluginID] = append(expectedBindings[binding.pluginID], binding.capability)
		}
	}
	for plugin := range expectedBindings {
		sort.Strings(expectedBindings[plugin])
	}
	selectedIDs := make(map[string]struct{}, len(selected))
	for _, plugin := range selected {
		selectedIDs[plugin.id] = struct{}{}
	}
	referencedPlugins := make(map[string]struct{}, len(expectedClients)+len(expectedBindings))
	for plugin := range expectedClients {
		referencedPlugins[plugin] = struct{}{}
	}
	for plugin := range expectedBindings {
		referencedPlugins[plugin] = struct{}{}
	}
	referencedIDs := make([]string, 0, len(referencedPlugins))
	for plugin := range referencedPlugins {
		referencedIDs = append(referencedIDs, plugin)
	}
	sort.Strings(referencedIDs)
	for _, plugin := range referencedIDs {
		if _, exists := selectedIDs[plugin]; !exists {
			return fmt.Errorf("assembly membership references unselected Plugin %q", plugin)
		}
	}
	for index, plugin := range plugins {
		selectedPlugin := selected[index]
		expectedImport := path.Join(selectedPlugin.modulePath, selectedPlugin.path)
		if plugin.pluginID != selectedPlugin.id || plugin.projectModule != selectedPlugin.modulePath || plugin.moduleVersion != selectedPlugin.moduleVersion || plugin.importPath != expectedImport || plugin.source != selectedPlugin.source || plugin.constructorOrder != index+1 || !plugin.lifecycleProbe {
			return fmt.Errorf("assembly plugins[%d] does not match selected Plugin %q", index, selectedPlugin.id)
		}
		if index > 0 && plugins[index-1].pluginID >= plugin.pluginID {
			return fmt.Errorf("assembly plugins are not in unique canonical order at %q", plugin.pluginID)
		}
		if !equalStringLists(plugin.requiredClients, expectedClients[plugin.pluginID]) {
			return fmt.Errorf("assembly Plugin %q required clients do not match declared Plugin requirements", plugin.pluginID)
		}
		if !equalStringLists(plugin.providerBindings, expectedBindings[plugin.pluginID]) {
			return fmt.Errorf("assembly Plugin %q Provider bindings do not match selected Providers", plugin.pluginID)
		}
		if err := validateCanonicalCapabilityList(plugin.requiredClients); err != nil {
			return fmt.Errorf("assembly Plugin %q required clients: %w", plugin.pluginID, err)
		}
		if err := validateCanonicalCapabilityList(plugin.providerBindings); err != nil {
			return fmt.Errorf("assembly Plugin %q Provider bindings: %w", plugin.pluginID, err)
		}
	}
	return nil
}

func validateCanonicalCapabilityList(values []string) error {
	for index, value := range values {
		if _, err := capabilityid.Parse(value); err != nil {
			return fmt.Errorf("item %d %q is invalid", index, value)
		}
		if index > 0 && values[index-1] >= value {
			return errors.New("items are not in unique canonical order")
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	write := 0
	for _, value := range values {
		if write != 0 && values[write-1] == value {
			continue
		}
		values[write] = value
		write++
	}
	return values[:write]
}

func equalStringLists(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
