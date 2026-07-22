package resolutionevidence_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestBuildRecordsDeterministicStaticAssemblyMembership(t *testing.T) {
	t.Parallel()

	firstInput := staticAssemblyEvidenceInput(t, false)
	secondInput := staticAssemblyEvidenceInput(t, true)
	first, err := resolutionevidence.Build(firstInput)
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(secondInput)
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("static assembly input permutation changed evidence:\nfirst:  %s\nsecond: %s", first.CanonicalJSON(), second.CanonicalJSON())
	}

	assembly, exists := first.StaticAssembly()
	if !exists || first.AssemblyPluginCount() != 2 || first.AssemblyBindingCount() != len(intrinsiccatalog.Definitions())+2 {
		t.Fatalf("static assembly = %#v, %t; counts %d Plugins, %d bindings", assembly, exists, first.AssemblyPluginCount(), first.AssemblyBindingCount())
	}
	plugins := assembly.Plugins()
	if len(plugins) != 2 || plugins[0].PluginID() != "example.local" || plugins[0].ProjectModule() != "example.com/app" || plugins[0].ModuleVersion() != "" || plugins[0].ImportPath() != "example.com/app/local" || plugins[0].ConstructorOrder() != 1 || !plugins[0].LifecycleProbe() || plugins[0].Source().Module() != "example.com/app" || plugins[0].Source().Path() != "local/plugin.yaml" || !slices.Equal(plugins[0].RequiredClients(), []string{"email.send/v1"}) || !slices.Equal(plugins[0].ProviderBindings(), []string{"audit.write/v1"}) {
		t.Fatalf("local assembly Plugin = %#v", plugins[0])
	}
	if plugins[1].PluginID() != "example.smtp" || plugins[1].ProjectModule() != "example.com/smtp" || plugins[1].ModuleVersion() != "v1.3.0" || plugins[1].ImportPath() != "example.com/smtp/smtp" || plugins[1].ConstructorOrder() != 2 || !plugins[1].LifecycleProbe() || plugins[1].Source().Module() != "corp.example/smtp" || plugins[1].Source().Path() != "smtp/plugin.yaml" || !slices.Equal(plugins[1].RequiredClients(), []string{"kernel.health/v1"}) || !slices.Equal(plugins[1].ProviderBindings(), []string{"email.send/v1"}) {
		t.Fatalf("dependency assembly Plugin = %#v", plugins[1])
	}

	bindings := assembly.Bindings()
	byCapability := make(map[string]resolutionevidence.AssemblyBinding, len(bindings))
	for index, binding := range bindings {
		if index > 0 && bindings[index-1].Capability() >= binding.Capability() {
			t.Fatalf("assembly bindings are not in canonical order: %#v", bindings)
		}
		byCapability[binding.Capability()] = binding
	}
	for _, definition := range intrinsiccatalog.Definitions() {
		binding, found := byCapability[definition.ID().String()]
		if !found || !binding.Intrinsic() || binding.ContractDigest() != definition.ContractDigest() || binding.PluginID() != "" || binding.ProjectModule() != "" || binding.SelectionReason() != resolutionevidence.ProviderSelectionIntrinsic || binding.ProviderSource().Module() != "github.com/plystra/kernel" || binding.ProviderSource().Kind() != "intrinsic-provider" || binding.Required() != (definition.ID().String() == "kernel.health/v1") {
			t.Fatalf("intrinsic assembly binding %s = %#v, %t", definition.ID(), binding, found)
		}
	}
	for capability, want := range map[string]struct {
		plugin string
		module string
	}{
		"audit.write/v1": {plugin: "example.local", module: "example.com/app"},
		"email.send/v1":  {plugin: "example.smtp", module: "example.com/smtp"},
	} {
		binding, found := byCapability[capability]
		if !found || binding.Intrinsic() || !binding.Required() || binding.PluginID() != want.plugin || binding.ProjectModule() != want.module || binding.SelectionReason() != resolutionevidence.ProviderSelectionSoleProvider || binding.ProviderSource().Kind() != "provider-declaration" {
			t.Fatalf("ordinary assembly binding %s = %#v, %t", capability, binding, found)
		}
	}

	var document struct {
		StaticAssembly struct {
			Plugins []struct {
				PluginID         string   `json:"plugin_id"`
				ConstructorOrder int      `json:"constructor_order"`
				LifecycleProbe   bool     `json:"lifecycle_probe"`
				RequiredClients  []string `json:"required_clients"`
				ProviderBindings []string `json:"provider_bindings"`
			} `json:"plugins"`
			Bindings []struct {
				Capability string `json:"capability"`
				Intrinsic  bool   `json:"intrinsic"`
				Required   bool   `json:"required"`
			} `json:"bindings"`
		} `json:"static_assembly"`
		Counts struct {
			Plugins  int `json:"assembly_plugins"`
			Bindings int `json:"assembly_bindings"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(first.CanonicalJSON(), &document); err != nil || document.Counts.Plugins != 2 || document.Counts.Bindings != len(intrinsiccatalog.Definitions())+2 || len(document.StaticAssembly.Plugins) != 2 || document.StaticAssembly.Plugins[0].PluginID != "example.local" || document.StaticAssembly.Plugins[0].ConstructorOrder != 1 || !document.StaticAssembly.Plugins[0].LifecycleProbe || !slices.Equal(document.StaticAssembly.Plugins[0].RequiredClients, []string{"email.send/v1"}) || !slices.Equal(document.StaticAssembly.Plugins[0].ProviderBindings, []string{"audit.write/v1"}) || len(document.StaticAssembly.Bindings) != len(intrinsiccatalog.Definitions())+2 {
		t.Fatalf("canonical static assembly = %#v, %v", document, err)
	}

	plugins[0] = resolutionevidence.AssemblyPlugin{}
	required := assembly.Plugins()[0].RequiredClients()
	required[0] = "changed/v1"
	bindings[0] = resolutionevidence.AssemblyBinding{}
	fresh, freshExists := first.StaticAssembly()
	if !freshExists || fresh.Plugins()[0].PluginID() != "example.local" || !slices.Equal(fresh.Plugins()[0].RequiredClients(), []string{"email.send/v1"}) || fresh.Bindings()[0].Capability() == "" {
		t.Fatal("StaticAssembly returned mutable state")
	}
	for _, forbidden := range []string{`C:\\private`, "/private", "diagnostic-only-source", "resolved-secret"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("static assembly contains forbidden value %q: %s", forbidden, first.CanonicalJSON())
		}
	}
}

func TestBuildRejectsInconsistentStaticAssemblyInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		mutate func(*resolutionevidence.StaticAssemblyInput)
	}{
		{
			name: "missing selected Plugin",
			want: "constructor inputs 1 do not match selected Plugins 2",
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins = input.Plugins[:1]
			},
		},
		{
			name: "duplicate Plugin",
			want: `duplicates Plugin "example.local"`,
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins[1] = input.Plugins[0]
			},
		},
		{
			name: "invalid Plugin ID",
			want: `plugin_id "Example.Invalid" is invalid`,
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins[0].PluginID = "Example.Invalid"
			},
		},
		{
			name: "wrong module",
			want: "does not match",
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins[0].ModulePath = "example.com/other"
			},
		},
		{
			name: "wrong module version",
			want: "does not match",
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins[1].ModuleVersion = "v9.9.9"
			},
		},
		{
			name: "wrong import path",
			want: "does not match",
			mutate: func(input *resolutionevidence.StaticAssemblyInput) {
				input.Plugins[1].ImportPath = "example.com/smtp/other"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := staticAssemblyEvidenceInput(t, false)
			test.mutate(input.StaticAssembly)
			evidence, err := resolutionevidence.Build(input)
			if !strings.Contains(errString(err), "static assembly membership") || !strings.Contains(errString(err), test.want) || evidence.Valid() {
				t.Fatalf("Build = %#v, %v; want %q", evidence, err, test.want)
			}
		})
	}
}

func TestBuildKeepsSyntheticEvidenceAssemblyOptional(t *testing.T) {
	t.Parallel()

	input := staticAssemblyEvidenceInput(t, false)
	input.StaticAssembly = nil
	evidence, err := resolutionevidence.Build(input)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if assembly, exists := evidence.StaticAssembly(); exists || len(assembly.Plugins()) != 0 || len(assembly.Bindings()) != 0 || evidence.AssemblyPluginCount() != 0 || evidence.AssemblyBindingCount() != 0 || bytes.Contains(evidence.CanonicalJSON(), []byte(`"static_assembly"`)) {
		t.Fatalf("optional static assembly = %#v, %t; %s", assembly, exists, evidence.CanonicalJSON())
	}
}

func staticAssemblyEvidenceInput(t testing.TB, reverse bool) resolutionevidence.Input {
	t.Helper()
	audit := queryContract(t, "audit.write/v1")
	email := queryContract(t, "email.send/v1")
	health := intrinsicContract(t, "kernel.health/v1")
	plugins := []generation.PluginInput{
		{ID: "example.local", ModulePath: "example.com/app", Provides: []string{"audit.write/v1"}, Requires: []string{"email.send/v1"}, BuildMetadataJSON: []byte("{}")},
		{ID: "example.smtp", ModulePath: "example.com/smtp", ModuleVersion: "v1.3.0", Provides: []string{"email.send/v1"}, Requires: []string{"kernel.health/v1"}, BuildMetadataJSON: []byte("{}")},
	}
	capabilities := []generation.CapabilityInput{
		{ContractJSON: audit},
		{ContractJSON: email},
		{ContractJSON: health, Intrinsic: true},
	}
	requirementIDs := []string{"audit.write/v1", "email.send/v1", "kernel.health/v1"}
	providers := []generation.ProviderInput{
		{Capability: "audit.write/v1", Plugin: "example.local"},
		{Capability: "email.send/v1", Plugin: "example.smtp"},
	}
	modules := []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/smtp", Role: resolutionevidence.ModuleRoleDependency, RequiredVersion: "v1.2.0", SelectedVersion: "v1.3.0", Direct: true, SourceModulePath: "corp.example/smtp", Replacement: &resolutionevidence.ReplacementInput{Kind: resolutionevidence.ReplacementModule, ModulePath: "corp.example/smtp", Version: "v1.3.0"}},
	}
	candidates := []resolutionevidence.PluginCandidateInput{
		{ID: "example.local", ModulePath: "example.com/app", Path: "local"},
		{ID: "example.smtp", ModulePath: "example.com/smtp", Path: "smtp"},
	}
	assemblyPlugins := []resolutionevidence.AssemblyPluginInput{
		{PluginID: "example.local", ModulePath: "example.com/app", ImportPath: "example.com/app/local"},
		{PluginID: "example.smtp", ModulePath: "example.com/smtp", ModuleVersion: "v1.3.0", ImportPath: "example.com/smtp/smtp"},
	}
	if reverse {
		slices.Reverse(plugins)
		slices.Reverse(capabilities)
		slices.Reverse(requirementIDs)
		slices.Reverse(providers)
		slices.Reverse(modules)
		slices.Reverse(candidates)
		slices.Reverse(assemblyPlugins)
	}
	context, err := generation.NewContext(generation.Input{
		Plugins:      plugins,
		Capabilities: capabilities,
		Requirements: requirementIDs,
		Providers:    providers,
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	requirements := []providerresolution.Requirement{
		{Contract: audit, Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementDeclaration, Reference: "diagnostic-only-source", ModulePath: "example.com/app", Path: "plystra.yaml", Line: 1, Column: 1}},
		{Contract: email, Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementPlugin, Reference: "diagnostic-only-source", ModulePath: "example.com/app", Path: "local/plugin.yaml", Line: 1, Column: 1, PluginID: "example.local"}},
		{Contract: health, Source: providerresolution.RequirementSource{Kind: providerresolution.RequirementPlugin, Reference: "diagnostic-only-source", ModulePath: "example.com/smtp", Path: "smtp/plugin.yaml", Line: 1, Column: 1, PluginID: "example.smtp"}},
	}
	providerCandidates := []providerresolution.Candidate{
		{PluginID: "example.local", Contract: audit, Source: "diagnostic-only-source"},
		{PluginID: "example.smtp", Contract: email, Source: "diagnostic-only-source"},
	}
	if reverse {
		slices.Reverse(requirements)
		slices.Reverse(providerCandidates)
	}
	providerResult, err := providerresolution.Resolve(providerresolution.Input{Requirements: requirements, Candidates: providerCandidates})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	assembly := resolutionevidence.StaticAssemblyInput{Plugins: assemblyPlugins}
	return resolutionevidence.Input{
		Context:            context,
		ProviderResolution: providerResult,
		AliasResolution:    resolveApplicationAliases(t, context),
		Modules:            modules,
		PluginCandidates:   candidates,
		StaticAssembly:     &assembly,
	}
}

func intrinsicContract(t testing.TB, capability string) []byte {
	t.Helper()
	for _, definition := range intrinsiccatalog.Definitions() {
		if definition.ID().String() == capability {
			return definition.ContractJSON()
		}
	}
	t.Fatalf("intrinsic Capability %s is absent", capability)
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
