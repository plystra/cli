package resolutionevidence

import (
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestValidateStaticAssemblyStateRejectsCorruption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		mutate func(*StaticAssembly, *[]SelectedPlugin, *[]SelectedProvider, *[]CapabilityRequirement)
	}{
		{
			name: "missing binding",
			want: "assembly bindings",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings = assembly.bindings[1:]
			},
		},
		{
			name: "binding order",
			want: "not in unique canonical order",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings[0], assembly.bindings[1] = assembly.bindings[1], assembly.bindings[0]
			},
		},
		{
			name: "intrinsic contract",
			want: "invalid intrinsic membership",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings[assemblyBindingIndex(assembly.bindings, "kernel.health/v1")].contractDigest = testDigest("f")
			},
		},
		{
			name: "intrinsic source",
			want: "invalid intrinsic membership",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings[assemblyBindingIndex(assembly.bindings, "kernel.info/v1")].providerSource.path = "other.yaml"
			},
		},
		{
			name: "required marker",
			want: "inconsistent requirement marker",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings[assemblyBindingIndex(assembly.bindings, "email.send/v1")].required = false
			},
		},
		{
			name: "ordinary Provider",
			want: "does not match its selected Provider",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.bindings[assemblyBindingIndex(assembly.bindings, "email.send/v1")].pluginID = "example.other"
			},
		},
		{
			name: "Plugin order",
			want: "does not match selected Plugin",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[0], assembly.plugins[1] = assembly.plugins[1], assembly.plugins[0]
			},
		},
		{
			name: "constructor order",
			want: "does not match selected Plugin",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[0].constructorOrder = 2
			},
		},
		{
			name: "lifecycle probe",
			want: "does not match selected Plugin",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[0].lifecycleProbe = false
			},
		},
		{
			name: "constructor import",
			want: "does not match selected Plugin",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[1].importPath = "example.com/smtp/other"
			},
		},
		{
			name: "constructor source",
			want: "does not match selected Plugin",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[1].source.path = "other/plugin.yaml"
			},
		},
		{
			name: "required clients",
			want: "required clients do not match",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[0].requiredClients = nil
			},
		},
		{
			name: "Provider bindings",
			want: "Provider bindings do not match",
			mutate: func(assembly *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, _ *[]CapabilityRequirement) {
				assembly.plugins[1].providerBindings = nil
			},
		},
		{
			name: "Plugin requirement source",
			want: "required clients do not match",
			mutate: func(_ *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, requirements *[]CapabilityRequirement) {
				(*requirements)[1].sources[0].pluginID = "example.smtp"
			},
		},
		{
			name: "unselected Plugin requirement source",
			want: `references unselected Plugin "example.unselected"`,
			mutate: func(_ *StaticAssembly, _ *[]SelectedPlugin, _ *[]SelectedProvider, requirements *[]CapabilityRequirement) {
				extra := (*requirements)[1].sources[0]
				extra.pluginID = "example.unselected"
				(*requirements)[1].sources = append((*requirements)[1].sources, extra)
			},
		},
		{
			name: "selected Provider",
			want: "does not match its selected Provider",
			mutate: func(_ *StaticAssembly, _ *[]SelectedPlugin, providers *[]SelectedProvider, _ *[]CapabilityRequirement) {
				(*providers)[1].pluginID = "example.other"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assembly, plugins, providers, requirements := validStaticAssemblyFixture()
			test.mutate(&assembly, &plugins, &providers, &requirements)
			err := validateStaticAssemblyState(assembly, true, plugins, providers, requirements)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateStaticAssemblyState = %v; want %q", err, test.want)
			}
		})
	}
}

func TestValidateStaticAssemblyStateAcceptsCompleteMembership(t *testing.T) {
	t.Parallel()

	assembly, plugins, providers, requirements := validStaticAssemblyFixture()
	if err := validateStaticAssemblyState(assembly, true, plugins, providers, requirements); err != nil {
		t.Fatalf("validateStaticAssemblyState: %v", err)
	}
	if err := validateStaticAssemblyState(StaticAssembly{}, false, nil, nil, nil); err != nil {
		t.Fatalf("absent static assembly: %v", err)
	}
	if err := validateStaticAssemblyState(assembly, false, plugins, providers, requirements); err == nil || !strings.Contains(err.Error(), "absent static assembly carries membership state") {
		t.Fatalf("absent populated static assembly = %v", err)
	}
}

func validStaticAssemblyFixture() (StaticAssembly, []SelectedPlugin, []SelectedProvider, []CapabilityRequirement) {
	localSource := Source{module: "example.com/app", path: "local/plugin.yaml", kind: "plugin-declaration", line: 1, column: 1}
	smtpSource := Source{module: "corp.example/smtp", path: "smtp/plugin.yaml", kind: "plugin-declaration", line: 1, column: 1}
	localProviderSource := Source{module: "example.com/app", path: "local/capabilities/audit.write/v1/capability.yaml", kind: "provider-declaration", line: 1, column: 1}
	smtpProviderSource := Source{module: "corp.example/smtp", path: "smtp/capabilities/email.send/v1/capability.yaml", kind: "provider-declaration", line: 1, column: 1}
	plugins := []SelectedPlugin{
		{id: "example.local", modulePath: "example.com/app", moduleRole: ModuleRoleCurrent, path: "local", source: localSource},
		{id: "example.smtp", modulePath: "example.com/smtp", moduleVersion: "v1.3.0", moduleRole: ModuleRoleDependency, path: "smtp", source: smtpSource},
	}
	healthDigest := ""
	for _, definition := range intrinsiccatalog.Definitions() {
		if definition.ID().String() == "kernel.health/v1" {
			healthDigest = definition.ContractDigest()
			break
		}
	}
	if healthDigest == "" {
		panic("kernel.health/v1 is absent from the intrinsic catalog")
	}
	requirements := []CapabilityRequirement{
		{capability: "audit.write/v1", contractDigest: testDigest("a"), sources: []RequirementSource{{kind: providerresolution.RequirementDeclaration, projectModule: "example.com/app", source: Source{module: "example.com/app", path: "plystra.yaml", kind: "declaration", line: 1, column: 1}}}},
		{capability: "email.send/v1", contractDigest: testDigest("b"), sources: []RequirementSource{{kind: providerresolution.RequirementPlugin, projectModule: "example.com/app", pluginID: "example.local", source: localSource}}},
		{capability: "kernel.health/v1", contractDigest: healthDigest, intrinsic: true, sources: []RequirementSource{{kind: providerresolution.RequirementPlugin, projectModule: "example.com/smtp", pluginID: "example.smtp", source: smtpSource}}},
	}
	providers := []SelectedProvider{
		{capability: "audit.write/v1", pluginID: "example.local", projectModule: "example.com/app", contractDigest: testDigest("a"), providerSource: localProviderSource, selectionReason: ProviderSelectionSoleProvider},
		{capability: "email.send/v1", pluginID: "example.smtp", projectModule: "example.com/smtp", contractDigest: testDigest("b"), providerSource: smtpProviderSource, selectionReason: ProviderSelectionSoleProvider},
		{capability: "kernel.health/v1", contractDigest: healthDigest, providerSource: Source{module: "github.com/plystra/kernel", path: "capability/catalog/definitions/kernel.health/v1/capability.yaml", kind: "intrinsic-provider", line: 1, column: 1}, selectionReason: ProviderSelectionIntrinsic},
	}
	bindings := []AssemblyBinding{
		{capability: "audit.write/v1", contractDigest: testDigest("a"), required: true, pluginID: "example.local", projectModule: "example.com/app", selectionReason: ProviderSelectionSoleProvider, providerSource: localProviderSource},
		{capability: "email.send/v1", contractDigest: testDigest("b"), required: true, pluginID: "example.smtp", projectModule: "example.com/smtp", selectionReason: ProviderSelectionSoleProvider, providerSource: smtpProviderSource},
	}
	for _, definition := range intrinsiccatalog.Definitions() {
		bindings = append(bindings, AssemblyBinding{
			capability:      definition.ID().String(),
			contractDigest:  definition.ContractDigest(),
			intrinsic:       true,
			required:        definition.ID().String() == "kernel.health/v1",
			selectionReason: ProviderSelectionIntrinsic,
			providerSource:  Source{module: "github.com/plystra/kernel", path: intrinsicProviderPath(definition.ID()), kind: "intrinsic-provider", line: 1, column: 1},
		})
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].capability < bindings[right].capability })
	assemblyPlugins := []AssemblyPlugin{
		{pluginID: "example.local", projectModule: "example.com/app", importPath: "example.com/app/local", source: localSource, constructorOrder: 1, lifecycleProbe: true, requiredClients: []string{"email.send/v1"}, providerBindings: []string{"audit.write/v1"}},
		{pluginID: "example.smtp", projectModule: "example.com/smtp", moduleVersion: "v1.3.0", importPath: "example.com/smtp/smtp", source: smtpSource, constructorOrder: 2, lifecycleProbe: true, requiredClients: []string{"kernel.health/v1"}, providerBindings: []string{"email.send/v1"}},
	}
	return StaticAssembly{plugins: assemblyPlugins, bindings: bindings}, plugins, providers, requirements
}

func assemblyBindingIndex(bindings []AssemblyBinding, capability string) int {
	for index, binding := range bindings {
		if binding.capability == capability {
			return index
		}
	}
	panic("missing assembly binding " + capability)
}
