package generationresolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestResolveExtensionsBuildsStableGeneratedRequirementClosure(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)

	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{
					Requirements: []generation.Requirement{{
						RuleID:     "authn.require-audit",
						Namespace:  "authn",
						Source:     extensionTestCapabilityID(t, "order.create/v1"),
						Capability: extensionTestCapabilityID(t, "audit.write/v1"),
					}},
					Diagnostics: []generation.Diagnostic{{
						Code:      "authn.verified",
						Severity:  generation.DiagnosticInfo,
						Message:   "verified context will be reused",
						Namespace: "authn",
						Source:    extensionTestCapabilityID(t, "order.create/v1"),
						RuleID:    "authn.require-audit",
					}},
					Contributions: []generation.Contribution{{
						ID:        "authn.verify",
						Namespace: "authn",
						Source:    extensionTestCapabilityID(t, "order.create/v1"),
						Point:     generation.GenerationPointInvocationPrepare,
						Provides:  []generation.ContributionToken{"verified-authn-context"},
					}},
					AliasContributions: []generation.CapabilityAliasContribution{{
						ID:        "authn.order-shortcut",
						Namespace: "authn",
						Source:    extensionTestCapabilityID(t, "order.create/v1"),
						Alias:     extensionTestCapabilityID(t, "orders.submit/v1"),
						Target:    extensionTestCapabilityID(t, "order.create/v1"),
					}},
				}, nil
			},
		},
	})

	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if result.Passes() != 3 {
		t.Fatalf("Passes = %d, want 3", result.Passes())
	}
	if got := extensionResolvedCapabilityIDs(result.ActivationResolution().ProviderResolution()); !slices.Equal(got, []string{
		"audit.write/v1",
		"authn.session.verify/v1",
		"order.create/v1",
	}) {
		t.Fatalf("resolved Capabilities = %v", got)
	}
	if got := extensionSelectedProviderStrings(result.ActivationResolution().ProviderResolution()); !slices.Equal(got, []string{
		"audit.write/v1=example.audit",
		"authn.session.verify/v1=example.authn",
		"order.create/v1=example.business",
	}) {
		t.Fatalf("selected providers = %v", got)
	}
	if got := generationRequirementIDs(result.Context()); !slices.Equal(got, []string{
		"audit.write/v1",
		"authn.session.verify/v1",
		"order.create/v1",
	}) {
		t.Fatalf("context requirements = %v", got)
	}
	generated := result.GeneratedRequirements()
	if len(generated) != 1 || generated[0].PluginID() != "example.authn" || generated[0].Namespace() != "authn" || generated[0].Source().String() != "order.create/v1" || generated[0].RuleID() != "authn.require-audit" || generated[0].Capability().String() != "audit.write/v1" {
		t.Fatalf("GeneratedRequirements = %#v", generated)
	}
	outputs := result.Outputs()
	if len(outputs) != 1 || outputs[0].PluginID() != "example.authn" || outputs[0].API() != "v1" || outputs[0].Package() != "./generation" || !slices.Equal(outputs[0].Namespaces(), []string{"authn"}) || len(outputs[0].Output().Diagnostics()) != 1 {
		t.Fatalf("Outputs = %#v", outputs)
	}
	contributions := result.Contributions()
	if len(contributions) != 1 || contributions[0].PluginID() != "example.authn" || contributions[0].ID() != "authn.verify" || contributions[0].Namespace() != "authn" || contributions[0].Source().String() != "order.create/v1" || contributions[0].Point() != generation.GenerationPointInvocationPrepare || !slices.Equal(contributions[0].Provides(), []generation.ContributionToken{"verified-authn-context"}) {
		t.Fatalf("Contributions = %#v", contributions)
	}
	aliases := result.AliasResolution().Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "orders.submit/v1" || aliases[0].Target().String() != "order.create/v1" || len(aliases[0].Sources()) != 1 || aliases[0].Sources()[0].ID() != "example.authn" || aliases[0].Sources()[0].ContributionID() != "authn.order-shortcut" {
		t.Fatalf("AliasResolution = %#v", aliases)
	}
	if got := builder.BuiltPluginIDs(); !slices.Equal(got, []string{"example.authn"}) {
		t.Fatalf("built helpers = %v", got)
	}
	helper := builder.helpers["example.authn"]
	if helper.calls != 3 || !helper.closed {
		t.Fatalf("authn helper calls=%d closed=%t", helper.calls, helper.closed)
	}
	build := builder.builds[0]
	if build.spec.ModulePath != "example.com/application" || build.spec.PluginPath != "authn" || build.options.ModuleRoot != "authn-module-root" || !slices.Equal(build.spec.Namespaces, []string{"authn"}) {
		t.Fatalf("helper build mapping = %#v", build)
	}

	outputs[0] = ExtensionOutput{}
	generated[0] = GeneratedRequirement{}
	provided := contributions[0].Provides()
	provided[0] = "changed"
	contributions[0] = ResolvedContribution{}
	aliases[0] = aliasresolution.Alias{}
	if result.Outputs()[0].PluginID() != "example.authn" || result.GeneratedRequirements()[0].PluginID() != "example.authn" || result.Contributions()[0].PluginID() != "example.authn" || result.Contributions()[0].Provides()[0] != "verified-authn-context" {
		t.Fatal("ExtensionResult exposed mutable result storage")
	}
	if result.AliasResolution().Aliases()[0].ID().String() != "orders.submit/v1" {
		t.Fatal("ExtensionResult exposed mutable Alias resolution storage")
	}
}

func TestResolveExtensionsExecutesSelectedHelperProcess(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)
	moduleRoot := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := extensionTestRepositoryRoot(t)
	goMod := fmt.Sprintf(
		"module example.com/generationresolutiontest\n\ngo 1.26\n\nrequire github.com/plystra/cli v0.0.0\n\nrequire (\n\tgithub.com/plystra/kernel v0.0.0-20260721165653-c7bd8ea1247f // indirect\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/cli => %s\n",
		strconv.Quote(filepath.ToSlash(cliRoot)),
	)
	extensionWriteTestFile(t, filepath.Join(moduleRoot, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	extensionWriteTestFile(t, filepath.Join(moduleRoot, "go.sum"), string(goSum))
	extensionWriteTestFile(t, filepath.Join(moduleRoot, "authn", "generation", "generate.go"), realExtensionSource)
	for index := range input.Plugins {
		input.Plugins[index].Context.ModulePath = "example.com/generationresolutiontest"
		input.Plugins[index].ModuleRoot = moduleRoot
	}
	input.BuildOptions = generationexec.BuildOptions{
		BuildEnvironment: extensionReplaceEnvironment(os.Environ(), "GOWORK", "off"),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
	}

	result, err := ResolveExtensions(t.Context(), input)
	if err != nil {
		t.Fatalf("ResolveExtensions: %v", err)
	}
	if result.Passes() != 3 || len(result.GeneratedRequirements()) != 1 || result.GeneratedRequirements()[0].Capability().String() != "audit.write/v1" {
		t.Fatalf("real helper result = passes %d generated %#v", result.Passes(), result.GeneratedRequirements())
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil {
		t.Fatalf("ReadDir(temporary parent): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary helper artifacts remain: %v", entries)
	}
}

func TestResolveExtensionsSkipsHelpersWhenNoExtensionIsSelected(t *testing.T) {
	plain := extensionTestContract(t, "order.read/v1", "")
	health := extensionTestContract(t, "kernel.health/v1", "")
	local := extensionTestPlugin("example.local", "local")
	local.Local = true
	input := ExtensionInput{
		ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
			Mode:                        generation.ConfigurationModeExplicit,
			RootPath:                    "plystra.yaml",
			RootDigest:                  "sha256:" + strings.Repeat("1", 64),
			SelectedPath:                "deploy/customer.yaml",
			SelectedDigest:              "sha256:" + strings.Repeat("2", 64),
			DependencyCompositionDigest: "sha256:" + strings.Repeat("3", 64),
		},
		Input: Input{
			Candidates: []providerresolution.Candidate{{PluginID: "example.business", Contract: plain, Source: "business/order.read"}},
		},
		Plugins: []Plugin{extensionTestPlugin("example.business", "business", "order.read/v1"), local},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: plain, Exposure: generation.Exposure{Go: true}},
			{ContractJSON: health, Intrinsic: true, Exposure: generation.Exposure{Go: true}},
		},
		ApplicationAliases: extensionTestApplicationAliases(t, `    orders.read/v1: order.read/v1
    health.status/v1: kernel.health/v1
`),
	}
	builder := newFakeExtensionBuilder(nil)
	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if result.Passes() != 1 || len(result.Outputs()) != 0 || len(result.GeneratedRequirements()) != 0 || len(result.Contributions()) != 0 || len(builder.builds) != 0 {
		t.Fatalf("empty extension result = passes %d, outputs %#v, generated %#v, contributions %#v, builds %#v", result.Passes(), result.Outputs(), result.GeneratedRequirements(), result.Contributions(), builder.builds)
	}
	if aliases := result.AliasResolution().Aliases(); len(aliases) != 2 || aliases[0].ID().String() != "health.status/v1" || aliases[0].Target().String() != "kernel.health/v1" || aliases[1].ID().String() != "orders.read/v1" || aliases[1].Target().String() != "order.read/v1" || aliases[1].Sources()[0].Kind() != generation.AliasSourceApplication {
		t.Fatalf("explicit Alias resolution = %#v", aliases)
	}
	resolved := result.ActivationResolution().ProviderResolution().Capabilities()
	if got := extensionResolvedCapabilityIDs(result.ActivationResolution().ProviderResolution()); !slices.Equal(got, []string{"kernel.health/v1", "order.read/v1"}) {
		t.Fatalf("Alias target requirements = %v", got)
	}
	if !slices.Equal(resolved[0].Sources(), []string{`plystra.yaml capabilities.aliases["health.status/v1"] target`}) || !slices.Equal(resolved[1].Sources(), []string{`plystra.yaml capabilities.aliases["orders.read/v1"] target`}) {
		t.Fatalf("Alias target requirement sources = %v, %v", resolved[0].Sources(), resolved[1].Sources())
	}
	if _, exists := result.Context().Plugin(extensionTestPluginID(t, "example.local")); !exists {
		t.Fatal("root-level local plugin is absent from the normalized application context")
	}
	provenance, exists := result.Context().ConfigurationProvenance()
	if !exists || provenance.Mode() != generation.ConfigurationModeExplicit || provenance.SelectedPath() != "deploy/customer.yaml" || provenance.SelectedDigest() != "sha256:"+strings.Repeat("2", 64) {
		t.Fatalf("configuration provenance = %#v, %t", provenance, exists)
	}
}

func TestResolveExtensionsClosesSelectedPluginRequirementsTransitively(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	storage := extensionTestContract(t, "storage.write/v1", "")
	unused := extensionTestContract(t, "unused.call/v1", "")
	business := extensionTestPlugin("example.business", "business", "order.create/v1")
	business.Context.Requires = []string{"audit.write/v1"}
	auditPlugin := extensionTestPlugin("example.audit", "audit", "audit.write/v1")
	auditPlugin.Context.Requires = []string{"storage.write/v1"}
	unusedPlugin := extensionTestPlugin("example.unused", "unused", "unused.call/v1")
	unusedPlugin.Context.Requires = []string{"missing.call/v1"}
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.audit", Contract: audit, Source: "audit/audit.write"},
				{PluginID: "example.storage", Contract: storage, Source: "storage/storage.write"},
				{PluginID: "example.unused", Contract: unused, Source: "unused/unused.call"},
			},
		},
		Plugins: []Plugin{
			business,
			auditPlugin,
			extensionTestPlugin("example.storage", "storage", "storage.write/v1"),
			unusedPlugin,
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: order},
			{ContractJSON: audit},
			{ContractJSON: storage},
			{ContractJSON: unused},
		},
	}
	result, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if result.Passes() != 3 {
		t.Fatalf("Passes = %d, want 3", result.Passes())
	}
	if got := generationRequirementIDs(result.Context()); !slices.Equal(got, []string{"audit.write/v1", "order.create/v1", "storage.write/v1"}) {
		t.Fatalf("context requirements = %v", got)
	}
	if _, exists := result.Context().Plugin(extensionTestPluginID(t, "example.unused")); exists {
		t.Fatal("unselected dependency plugin entered the normalized context")
	}
	resolved := result.ActivationResolution().ProviderResolution()
	auditCapability, exists := resolved.Capability(extensionTestInternalCapabilityID(t, "audit.write/v1"))
	if !exists || !slices.Equal(auditCapability.Sources(), []string{"plugin example.business at example.com/application@local/business/plugin.yaml requires audit.write/v1"}) {
		t.Fatalf("audit requirement sources = %v, %t", auditCapability.Sources(), exists)
	}
	storageCapability, exists := resolved.Capability(extensionTestInternalCapabilityID(t, "storage.write/v1"))
	if !exists || !slices.Equal(storageCapability.Sources(), []string{"plugin example.audit at example.com/application@local/audit/plugin.yaml requires storage.write/v1"}) {
		t.Fatalf("storage requirement sources = %v, %t", storageCapability.Sources(), exists)
	}
}

func TestResolveExtensionsAddsLocalPluginRequirementsBeforeSelection(t *testing.T) {
	audit := extensionTestContract(t, "audit.write/v1", "")
	local := extensionTestPlugin("example.local", "local")
	local.Local = true
	local.Context.Requires = []string{"audit.write/v1"}
	input := ExtensionInput{
		Input: Input{Candidates: []providerresolution.Candidate{{PluginID: "example.audit", Contract: audit, Source: "audit/audit.write"}}},
		Plugins: []Plugin{
			local,
			extensionTestPlugin("example.audit", "audit", "audit.write/v1"),
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: audit}},
	}
	result, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if result.Passes() != 1 || !slices.Equal(generationRequirementIDs(result.Context()), []string{"audit.write/v1"}) {
		t.Fatalf("result = passes %d, requirements %v", result.Passes(), generationRequirementIDs(result.Context()))
	}
	if provider, exists := result.Context().SelectedProvider(extensionTestCapabilityID(t, "audit.write/v1")); !exists || provider.String() != "example.audit" {
		t.Fatalf("audit provider = %s, %t", provider, exists)
	}
}

func TestResolveExtensionsRetainsAllPluginRequirementProvenance(t *testing.T) {
	audit := extensionTestContract(t, "audit.write/v1", "")
	local := extensionTestPlugin("example.local", "local")
	local.Local = true
	local.Context.Requires = []string{"audit.write/v1"}
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: audit, Source: "plystra.yaml capabilities.require[0]"}},
			Candidates:   []providerresolution.Candidate{{PluginID: "example.audit", Contract: audit, Source: "audit/audit.write"}},
		},
		Plugins:      []Plugin{local, extensionTestPlugin("example.audit", "audit", "audit.write/v1")},
		Capabilities: []generation.CapabilityInput{{ContractJSON: audit}},
	}
	result, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	capability, exists := result.ActivationResolution().ProviderResolution().Capability(extensionTestInternalCapabilityID(t, "audit.write/v1"))
	want := []string{
		"plugin example.local at example.com/application@local/local/plugin.yaml requires audit.write/v1",
		"plystra.yaml capabilities.require[0]",
	}
	if !exists || !slices.Equal(capability.Sources(), want) {
		t.Fatalf("audit sources = %v, %t; want %v", capability.Sources(), exists, want)
	}
}

func TestResolveExtensionsRejectsDuplicateSelectedPluginRequirements(t *testing.T) {
	audit := extensionTestContract(t, "audit.write/v1", "")
	local := extensionTestPlugin("example.local", "local")
	local.Local = true
	local.Context.Requires = []string{"audit.write/v1", "audit.write/v1"}
	input := ExtensionInput{
		Plugins:      []Plugin{local},
		Capabilities: []generation.CapabilityInput{{ContractJSON: audit}},
	}
	_, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if !errors.Is(err, ErrApplicationContext) || !strings.Contains(err.Error(), "duplicates required Capability audit.write/v1") {
		t.Fatalf("duplicate plugin requirement error = %v", err)
	}
}

func TestResolveExtensionsAddsCanonicalHTTPExposureRequirements(t *testing.T) {
	plain := extensionTestContract(t, "order.read/v1", "")
	health := extensionTestContract(t, "kernel.health/v1", "")
	input := ExtensionInput{
		Input: Input{
			Candidates: []providerresolution.Candidate{{PluginID: "example.business", Contract: plain, Source: "business/order.read"}},
		},
		Plugins: []Plugin{extensionTestPlugin("example.business", "business", "order.read/v1")},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: plain, Exposure: generation.Exposure{Go: true}},
			{ContractJSON: health, Intrinsic: true},
		},
		ApplicationHTTPExposures: extensionTestHTTPExposures(t, "order.read/v1", "kernel.health/v1"),
	}
	builder := newFakeExtensionBuilder(nil)
	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if result.Passes() != 1 || len(result.Outputs()) != 0 || len(builder.builds) != 0 {
		t.Fatalf("HTTP-only result = passes %d, outputs %#v, builds %#v", result.Passes(), result.Outputs(), builder.builds)
	}
	resolved := result.ActivationResolution().ProviderResolution().Capabilities()
	if got := extensionResolvedCapabilityIDs(result.ActivationResolution().ProviderResolution()); !slices.Equal(got, []string{"kernel.health/v1", "order.read/v1"}) {
		t.Fatalf("HTTP requirements = %v", got)
	}
	if !slices.Equal(resolved[0].Sources(), []string{`plystra.yaml http.expose["kernel.health/v1"]`}) || !slices.Equal(resolved[1].Sources(), []string{`plystra.yaml http.expose["order.read/v1"]`}) {
		t.Fatalf("HTTP requirement sources = %v, %v", resolved[0].Sources(), resolved[1].Sources())
	}
	for _, id := range []string{"kernel.health/v1", "order.read/v1"} {
		view, exists := result.Context().Capability(extensionTestCapabilityID(t, id))
		if !exists || !view.Exposure().HTTP || !view.Exposure().JavaScript {
			t.Fatalf("HTTP exposure for %s = %#v, %t", id, view.Exposure(), exists)
		}
		if id == "order.read/v1" && !view.Exposure().Go {
			t.Fatalf("HTTP exposure erased existing Go surface for %s", id)
		}
	}
	if input.Capabilities[0].Exposure != (generation.Exposure{Go: true}) || input.Capabilities[1].Exposure != (generation.Exposure{}) {
		t.Fatalf("ResolveExtensions mutated input exposure: %#v", input.Capabilities)
	}
}

func TestResolveExtensionsRejectsUnknownCanonicalHTTPExposure(t *testing.T) {
	plain := extensionTestContract(t, "order.read/v1", "")
	input := ExtensionInput{
		Capabilities:             []generation.CapabilityInput{{ContractJSON: plain}},
		ApplicationHTTPExposures: extensionTestHTTPExposures(t, "missing.operation/v1"),
	}
	result, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if !errors.Is(err, ErrResolveExtensions) || !errors.Is(err, ErrApplicationContext) || !strings.Contains(err.Error(), `plystra.yaml http.expose["missing.operation/v1"]`) || !strings.Contains(err.Error(), "absent from the visible canonical catalog") {
		t.Fatalf("unknown HTTP exposure error = %v", err)
	}
	if result.Passes() != 0 || len(result.Context().CanonicalJSON()) != 0 {
		t.Fatalf("unknown HTTP exposure returned partial result %#v", result)
	}
}

func TestResolveExtensionsRejectsFinalAliasConflict(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)
	input.ApplicationAliases = extensionTestApplicationAliases(t, "    orders.submit/v1: order.create/v1\n")
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{AliasContributions: []generation.CapabilityAliasContribution{{
					ID:        "authn.order-shortcut",
					Namespace: "authn",
					Source:    extensionTestCapabilityID(t, "order.create/v1"),
					Alias:     extensionTestCapabilityID(t, "orders.submit/v1"),
					Target:    extensionTestCapabilityID(t, "authn.session.verify/v1"),
				}}}, nil
			},
		},
	})
	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, ErrResolveExtensions) || !errors.Is(err, ErrAliasResolution) || !errors.Is(err, aliasresolution.ErrConflict) {
		t.Fatalf("Alias conflict error = %v", err)
	}
	for _, detail := range []string{"orders.submit/v1", "order.create/v1", "authn.session.verify/v1", "application plystra.yaml", "example.authn", "authn.order-shortcut", "no source priority"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Alias conflict error omits %q: %v", detail, err)
		}
	}
	if result.Passes() != 0 || len(result.AliasResolution().Aliases()) != 0 || len(result.Outputs()) != 0 {
		t.Fatalf("Alias conflict returned partial result %#v", result)
	}
}

func TestResolveExtensionsRunsOnlyExplicitlySelectedActivationProvider(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	catalog := extensionTestCatalog(t,
		extensionTestDeclaration(t, "example.authn-a", "authn", "authn.session.verify/v1"),
		extensionTestDeclaration(t, "example.authn-b", "authn", "authn.session.verify/v1"),
	)
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.authn-a", Contract: verify, Source: "authn-a/session.verify"},
				{PluginID: "example.authn-b", Contract: verify, Source: "authn-b/session.verify"},
			},
			Choices:     []providerresolution.Choice{{Capability: "authn.session.verify/v1", PluginID: "example.authn-b", Source: "plystra.yaml capabilities.use.authn.session.verify/v1"}},
			Activations: catalog,
		},
		Plugins: []Plugin{
			extensionTestPlugin("example.business", "business", "order.create/v1"),
			extensionTestPlugin("example.authn-a", "authn-a", "authn.session.verify/v1"),
			extensionTestPlugin("example.authn-b", "authn-b", "authn.session.verify/v1"),
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: order}, {ContractJSON: verify}},
	}
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn-b": {output: emptyExtensionOutput},
	})
	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if err != nil {
		t.Fatalf("resolveExtensions: %v", err)
	}
	if got := builder.BuiltPluginIDs(); !slices.Equal(got, []string{"example.authn-b"}) {
		t.Fatalf("built helpers = %v", got)
	}
	provider, ok := result.Context().SelectedProvider(extensionTestCapabilityID(t, "authn.session.verify/v1"))
	if !ok || provider.String() != "example.authn-b" {
		t.Fatalf("SelectedProvider = %s, %t", provider.String(), ok)
	}
	if _, selected := result.Context().Plugin(extensionTestPluginID(t, "example.authn-a")); selected {
		t.Fatal("unselected activation provider entered generation context")
	}
}

func TestResolveExtensionsRejectsProviderChoiceThatNeverBecomesRequired(t *testing.T) {
	email := extensionTestContract(t, "email.send/v1", "")
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: email, Source: "email workflow"}},
			Candidates: []providerresolution.Candidate{{
				PluginID: "example.email",
				Contract: email,
				Source:   "email/capability.yaml",
			}},
			Choices: []providerresolution.Choice{{
				Capability: "missing.operation/v1",
				PluginID:   "example.email",
				Source:     "plystra.yaml capabilities.use.missing.operation/v1",
			}},
		},
		Plugins:      []Plugin{extensionTestPlugin("example.email", "email", "email.send/v1")},
		Capabilities: []generation.CapabilityInput{{ContractJSON: email}},
	}
	result, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if !errors.Is(err, providerresolution.ErrInvalidChoice) || !strings.Contains(err.Error(), "missing.operation/v1") || !strings.Contains(err.Error(), "no canonical requirement or visible provider declares this Capability") {
		t.Fatalf("dormant Provider choice error = %v", err)
	}
	if result.Passes() != 0 || len(result.Context().CanonicalJSON()) != 0 {
		t.Fatalf("invalid dormant choice returned partial result %#v", result)
	}
}

func TestResolveExtensionsUsesOrdinaryProviderRulesForGeneratedRequirements(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	base := extensionTestInput(t, order, verify, audit)
	base.Candidates = append(base.Candidates,
		providerresolution.Candidate{PluginID: "example.audit-alt", Contract: audit, Source: "audit-alt/audit.write"},
	)
	base.Plugins = append(base.Plugins, extensionTestPlugin("example.audit-alt", "audit-alt", "audit.write/v1"))

	newBuilder := func() *fakeExtensionBuilder {
		return newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
			"example.authn": {output: requireCapabilityOutput(t, "authn", "order.create/v1", "authn.require-audit", "audit.write/v1")},
		})
	}
	_, err := resolveExtensions(t.Context(), base, newBuilder().Build)
	if !errors.Is(err, providerresolution.ErrAmbiguousProvider) {
		t.Fatalf("ambiguous generated requirement error = %v", err)
	}
	for _, detail := range []string{"audit.write/v1", "example.audit", "example.audit-alt", "example.authn", "authn.require-audit", "extensions.authn", "order.create/v1"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("ambiguous error omits %q: %v", detail, err)
		}
	}

	base.Choices = append(base.Choices, providerresolution.Choice{
		Capability: "audit.write/v1",
		PluginID:   "example.audit-alt",
		Source:     "plystra.yaml capabilities.use.audit.write/v1",
	})
	result, err := resolveExtensions(t.Context(), base, newBuilder().Build)
	if err != nil {
		t.Fatalf("resolveExtensions(explicit generated provider): %v", err)
	}
	selected, ok := result.ActivationResolution().ProviderResolution().SelectedProvider(extensionTestInternalCapabilityID(t, "audit.write/v1"))
	if !ok || selected.PluginID() != "example.audit-alt" || !selected.Explicit() {
		t.Fatalf("generated provider selection = %#v, %t", selected, ok)
	}
}

func TestResolveExtensionsReportsMissingGeneratedRequirementProvenance(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)
	input.Candidates = slices.DeleteFunc(input.Candidates, func(candidate providerresolution.Candidate) bool {
		return candidate.PluginID == "example.audit"
	})
	input.Plugins = slices.DeleteFunc(input.Plugins, func(plugin Plugin) bool {
		return plugin.Context.ID == "example.audit"
	})
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {output: requireCapabilityOutput(t, "authn", "order.create/v1", "authn.require-audit", "audit.write/v1")},
	})
	_, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, providerresolution.ErrMissingProvider) {
		t.Fatalf("missing generated requirement error = %v", err)
	}
	for _, detail := range []string{"audit.write/v1", "example.authn", "authn.require-audit", "extensions.authn", "order.create/v1"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("missing-provider error omits %q: %v", detail, err)
		}
	}
}

func TestResolveExtensionsDetectsMixedActivationGeneratedCycle(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "extensions:\n  audit: {record: true}\n")
	catalog := extensionTestCatalog(t,
		extensionTestDeclaration(t, "example.authn", "authn", "authn.session.verify/v1"),
		extensionTestDeclaration(t, "example.business", "audit", "order.create/v1"),
	)
	input := extensionTestInput(t, order, verify, audit)
	input.Activations = catalog
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn":    {output: requireCapabilityOutput(t, "authn", "order.create/v1", "authn.require-audit", "audit.write/v1")},
		"example.business": {output: emptyExtensionOutput},
	})
	_, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("mixed cycle error = %v", err)
	}
	var cycle *DependencyCycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("mixed cycle type = %T", err)
	}
	edges := cycle.Edges()
	if len(edges) != 2 || edges[0].Kind() != DependencyActivation || edges[0].Source().String() != "audit.write/v1" || edges[0].Target().String() != "order.create/v1" || edges[1].Kind() != DependencyGenerated || edges[1].Source().String() != "order.create/v1" || edges[1].Target().String() != "audit.write/v1" {
		t.Fatalf("mixed cycle edges = %#v", edges)
	}
	for _, detail := range []string{"order.create/v1", "audit.write/v1", "example.authn", "authn.require-audit", "extensions.authn", "example.business", "extensions.audit", "correction:"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("cycle error omits %q: %v", detail, err)
		}
	}
	edges[0] = DependencyEdge{}
	if cycle.Edges()[0].Source().String() != "audit.write/v1" {
		t.Fatal("DependencyCycleError exposed mutable edge storage")
	}
}

func TestResolveExtensionsRejectsDifferentOutputForRepeatedContext(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.authn", Contract: verify, Source: "authn/session.verify"},
			},
			Activations: extensionTestCatalog(t, extensionTestDeclaration(t, "example.authn", "authn", "authn.session.verify/v1")),
		},
		Plugins: []Plugin{
			extensionTestPlugin("example.business", "business", "order.create/v1"),
			extensionTestPlugin("example.authn", "authn", "authn.session.verify/v1"),
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: order}, {ContractJSON: verify}},
	}
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(call int, _ generation.Context) (generation.Output, error) {
				return generation.Output{Diagnostics: []generation.Diagnostic{{
					Code:      "authn.state",
					Severity:  generation.DiagnosticInfo,
					Message:   fmt.Sprintf("pass %d", call),
					Namespace: "authn",
					Source:    extensionTestCapabilityID(t, "order.create/v1"),
					RuleID:    "authn.observe",
				}}}, nil
			},
		},
	})
	_, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, ErrRepeatedState) || !strings.Contains(err.Error(), "first produced sha256:") || !strings.Contains(err.Error(), "and then sha256:") {
		t.Fatalf("repeated state error = %v", err)
	}
}

func TestResolveExtensionsRejectsOutputOutsideSelectedActivationInputs(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n  authz: {permission: order.create}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	check := extensionTestContract(t, "authz.check/v1", "")
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.authn", Contract: verify, Source: "authn/session.verify"},
				{PluginID: "example.authz", Contract: check, Source: "authz/check"},
			},
			Activations: extensionTestCatalog(t,
				extensionTestDeclaration(t, "example.authn", "authn", "authn.session.verify/v1"),
				extensionTestDeclaration(t, "example.authz", "authz", "authz.check/v1"),
			),
		},
		Plugins: []Plugin{
			extensionTestPlugin("example.business", "business", "order.create/v1"),
			extensionTestPlugin("example.authn", "authn", "authn.session.verify/v1"),
			extensionTestPlugin("example.authz", "authz", "authz.check/v1"),
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: order}, {ContractJSON: verify}, {ContractJSON: check}},
	}
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{Requirements: []generation.Requirement{{
					RuleID:     "authn.cross-namespace",
					Namespace:  "authz",
					Source:     extensionTestCapabilityID(t, "order.create/v1"),
					Capability: extensionTestCapabilityID(t, "authz.check/v1"),
				}}}, nil
			},
		},
		"example.authz": {output: emptyExtensionOutput},
	})
	_, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, ErrExtensionProvenance) {
		t.Fatalf("cross-namespace output error = %v", err)
	}
	for _, detail := range []string{"example.authn", "authn.cross-namespace", "authz.check/v1", "extensions.authz", "order.create/v1", "not one of its selected activation inputs"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("provenance error omits %q: %v", detail, err)
		}
	}

	contributionBuilder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{Contributions: []generation.Contribution{{
					ID:        "authn.cross-namespace",
					Namespace: "authz",
					Source:    extensionTestCapabilityID(t, "order.create/v1"),
					Point:     generation.GenerationPointInvocationPrepare,
					Provides:  []generation.ContributionToken{"authorization-approved"},
				}}}, nil
			},
		},
		"example.authz": {output: emptyExtensionOutput},
	})
	_, err = resolveExtensions(t.Context(), input, contributionBuilder.Build)
	if !errors.Is(err, ErrExtensionProvenance) {
		t.Fatalf("cross-namespace contribution error = %v", err)
	}
	for _, detail := range []string{"example.authn", "authn.cross-namespace", "extensions.authz", "order.create/v1", "not one of its selected activation inputs"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("contribution provenance error omits %q: %v", detail, err)
		}
	}

	aliasBuilder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{AliasContributions: []generation.CapabilityAliasContribution{{
					ID:        "authn.cross-namespace",
					Namespace: "authz",
					Source:    extensionTestCapabilityID(t, "order.create/v1"),
					Alias:     extensionTestCapabilityID(t, "orders.submit/v1"),
					Target:    extensionTestCapabilityID(t, "order.create/v1"),
				}}}, nil
			},
		},
		"example.authz": {output: emptyExtensionOutput},
	})
	_, err = resolveExtensions(t.Context(), input, aliasBuilder.Build)
	if !errors.Is(err, ErrExtensionProvenance) {
		t.Fatalf("cross-namespace Alias contribution error = %v", err)
	}
	for _, detail := range []string{"example.authn", "authn.cross-namespace", "orders.submit/v1", "order.create/v1", "extensions.authz", "not one of its selected activation inputs"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Alias contribution provenance error omits %q: %v", detail, err)
		}
	}
}

func TestResolveExtensionsFailsOnStructuredErrorDiagnostic(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.authn", Contract: verify, Source: "authn/session.verify"},
			},
			Activations: extensionTestCatalog(t, extensionTestDeclaration(t, "example.authn", "authn", "authn.session.verify/v1")),
		},
		Plugins: []Plugin{
			extensionTestPlugin("example.business", "business", "order.create/v1"),
			extensionTestPlugin("example.authn", "authn", "authn.session.verify/v1"),
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: order}, {ContractJSON: verify}},
	}
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{Diagnostics: []generation.Diagnostic{{
					Code:      "authn.unsupported",
					Severity:  generation.DiagnosticError,
					Message:   "authentication metadata is unsupported",
					Namespace: "authn",
					Source:    extensionTestCapabilityID(t, "order.create/v1"),
					RuleID:    "authn.validate",
				}}}, nil
			},
		},
	})
	_, err := resolveExtensions(t.Context(), input, builder.Build)
	if !errors.Is(err, ErrExtensionDiagnostic) {
		t.Fatalf("error diagnostic result = %v", err)
	}
	for _, detail := range []string{"example.authn", "authn.unsupported", "authn.validate", "extensions.authn", "order.create/v1", "authentication metadata is unsupported"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("diagnostic error omits %q: %v", detail, err)
		}
	}
}

func TestResolveExtensionsFailsClosedWhenHelperCleanupFails(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)
	cleanupFailure := errors.New("cleanup failed")
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {output: emptyExtensionOutput, closeErr: cleanupFailure},
	})
	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if result.Passes() != 0 || !errors.Is(err, ErrResolveExtensions) || !errors.Is(err, ErrExtensionExecution) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("cleanup result = %#v, %v", result, err)
	}
}

func TestResolveExtensionsRejectsContextCatalogDrift(t *testing.T) {
	required := extensionTestContract(t, "order.read/v1", "")
	drifted := extensionTestContract(t, "order.read/v1", "extensions:\n  audit: {record: true}\n")
	input := ExtensionInput{
		Input: Input{
			Requirements: []providerresolution.Requirement{{Contract: required, Source: "order route"}},
			Candidates:   []providerresolution.Candidate{{PluginID: "example.business", Contract: required, Source: "business/order.read"}},
		},
		Plugins:      []Plugin{extensionTestPlugin("example.business", "business", "order.read/v1")},
		Capabilities: []generation.CapabilityInput{{ContractJSON: drifted}},
	}
	_, err := resolveExtensions(t.Context(), input, newFakeExtensionBuilder(nil).Build)
	if !errors.Is(err, ErrApplicationContext) || !strings.Contains(err.Error(), "differs from the visible generation catalog contract") {
		t.Fatalf("catalog drift error = %v", err)
	}
}

type fakeExtensionBuild struct {
	spec    generationexec.Spec
	options generationexec.BuildOptions
}

type fakeExtensionBuilder struct {
	helpers map[string]*fakeExtensionHelper
	builds  []fakeExtensionBuild
}

func newFakeExtensionBuilder(helpers map[string]*fakeExtensionHelper) *fakeExtensionBuilder {
	if helpers == nil {
		helpers = make(map[string]*fakeExtensionHelper)
	}
	return &fakeExtensionBuilder{helpers: helpers}
}

func (b *fakeExtensionBuilder) Build(_ context.Context, spec generationexec.Spec, options generationexec.BuildOptions) (extensionHelper, error) {
	spec.Namespaces = append([]string(nil), spec.Namespaces...)
	options.BuildEnvironment = append([]string(nil), options.BuildEnvironment...)
	b.builds = append(b.builds, fakeExtensionBuild{spec: spec, options: options})
	helper, exists := b.helpers[spec.PluginID]
	if !exists {
		return nil, fmt.Errorf("unexpected helper build for %s", spec.PluginID)
	}
	return helper, nil
}

func (b *fakeExtensionBuilder) BuiltPluginIDs() []string {
	result := make([]string, len(b.builds))
	for index, build := range b.builds {
		result[index] = build.spec.PluginID
	}
	return result
}

type fakeExtensionHelper struct {
	output   func(int, generation.Context) (generation.Output, error)
	calls    int
	closed   bool
	closeErr error
}

func (h *fakeExtensionHelper) Generate(_ context.Context, generationContext generation.Context) (generation.NormalizedOutput, error) {
	if h.closed {
		return generation.NormalizedOutput{}, errors.New("fake helper used after close")
	}
	h.calls++
	output, err := h.output(h.calls, generationContext)
	if err != nil {
		return generation.NormalizedOutput{}, err
	}
	return generation.NormalizeOutput(generationContext, output)
}

func (h *fakeExtensionHelper) Close() error {
	h.closed = true
	return h.closeErr
}

func emptyExtensionOutput(_ int, _ generation.Context) (generation.Output, error) {
	return generation.Output{}, nil
}

func requireCapabilityOutput(t testing.TB, namespace, source, rule, capability string) func(int, generation.Context) (generation.Output, error) {
	t.Helper()
	sourceID := extensionTestCapabilityID(t, source)
	capabilityID := extensionTestCapabilityID(t, capability)
	return func(_ int, _ generation.Context) (generation.Output, error) {
		return generation.Output{Requirements: []generation.Requirement{{
			RuleID:     rule,
			Namespace:  namespace,
			Source:     sourceID,
			Capability: capabilityID,
		}}}, nil
	}
}

func extensionTestInput(t testing.TB, order, verify, audit []byte) ExtensionInput {
	t.Helper()
	return ExtensionInput{
		Input: Input{
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.business", Contract: order, Source: "business/order.create"},
				{PluginID: "example.authn", Contract: verify, Source: "authn/session.verify"},
				{PluginID: "example.audit", Contract: audit, Source: "audit/audit.write"},
			},
			Activations: extensionTestCatalog(t, extensionTestDeclaration(t, "example.authn", "authn", "authn.session.verify/v1")),
		},
		Plugins: []Plugin{
			extensionTestPlugin("example.business", "business", "order.create/v1"),
			extensionTestPlugin("example.authn", "authn", "authn.session.verify/v1"),
			extensionTestPlugin("example.audit", "audit", "audit.write/v1"),
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: order},
			{ContractJSON: verify},
			{ContractJSON: audit},
		},
		ApplicationHTTPExposures: extensionTestHTTPExposures(t, "order.create/v1"),
	}
}

func extensionTestApplicationAliases(t testing.TB, aliases string) []applicationmeta.Alias {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte("capabilities:\n  aliases:\n" + aliases))
	if err != nil {
		t.Fatalf("applicationmeta.Parse: %v", err)
	}
	return manifest.Aliases()
}

func extensionTestHTTPExposures(t testing.TB, capabilities ...string) []applicationmeta.HTTPExposure {
	t.Helper()
	var source strings.Builder
	source.WriteString("http:\n  expose:\n")
	for _, capability := range capabilities {
		source.WriteString("    - ")
		source.WriteString(capability)
		source.WriteByte('\n')
	}
	manifest, err := applicationmeta.Parse([]byte(source.String()))
	if err != nil {
		t.Fatalf("applicationmeta.Parse HTTP exposure: %v", err)
	}
	return manifest.HTTPExposures()
}

func extensionTestPlugin(id, path string, provides ...string) Plugin {
	return Plugin{
		Context: generation.PluginInput{
			ID:                id,
			ModulePath:        "example.com/application",
			Provides:          append([]string(nil), provides...),
			BuildMetadataJSON: []byte("{}"),
		},
		ModuleRoot: id[strings.LastIndex(id, ".")+1:] + "-module-root",
		PluginPath: path,
	}
}

func extensionTestContract(t testing.TB, id, extra string) []byte {
	t.Helper()
	source := []byte("id: " + id + "\nrequest: {}\nresponse: {}\nerrors: []\n" + extensionQuerySemanticsYAML + extra)
	canonical, err := capabilitymeta.NormalizeSchema(source)
	if err != nil {
		t.Fatalf("NormalizeSchema(%s): %v", id, err)
	}
	return canonical
}

const extensionQuerySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func extensionTestDeclaration(t testing.TB, pluginID, namespace, capability string) generationactivation.Declaration {
	t.Helper()
	data := []byte("id: " + pluginID + "\nprovides: [" + capability + "]\ngeneration:\n  api: v1\n  package: ./generation\n  activations:\n    - namespace: " + namespace + "\n      capability: " + capability + "\n")
	manifest, err := pluginmeta.Parse(data)
	if err != nil {
		t.Fatalf("pluginmeta.Parse(%s): %v", pluginID, err)
	}
	declaration, exists := manifest.Generation()
	if !exists {
		t.Fatalf("plugin %s has no generation declaration", pluginID)
	}
	return generationactivation.Declaration{
		PluginID:   pluginID,
		Source:     pluginID + "/plugin.yaml",
		Generation: declaration,
	}
}

func extensionTestCatalog(t testing.TB, declarations ...generationactivation.Declaration) generationactivation.Catalog {
	t.Helper()
	catalog, err := generationactivation.New(declarations)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	return catalog
}

func extensionTestCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("generation.ParseCapabilityID(%s): %v", value, err)
	}
	return id
}

func extensionTestPluginID(t testing.TB, value string) generation.PluginID {
	t.Helper()
	id, err := generation.ParsePluginID(value)
	if err != nil {
		t.Fatalf("generation.ParsePluginID(%s): %v", value, err)
	}
	return id
}

func extensionTestInternalCapabilityID(t testing.TB, value string) capabilityid.Identifier {
	t.Helper()
	parsed, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	return parsed
}

func generationRequirementIDs(context generation.Context) []string {
	values := context.Requirements()
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func extensionTestRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func extensionWriteTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func extensionReplaceEnvironment(environment []string, name, value string) []string {
	prefix := strings.ToUpper(name) + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if strings.HasPrefix(strings.ToUpper(item), prefix) {
			continue
		}
		result = append(result, item)
	}
	return append(result, name+"="+value)
}

func extensionResolvedCapabilityIDs(result providerresolution.Result) []string {
	values := result.Capabilities()
	identifiers := make([]string, len(values))
	for index, value := range values {
		identifiers[index] = value.ID().String()
	}
	return identifiers
}

func extensionSelectedProviderStrings(result providerresolution.Result) []string {
	values := result.Selections()
	providers := make([]string, len(values))
	for index, value := range values {
		providers[index] = value.Capability().String() + "=" + value.PluginID()
	}
	return providers
}

const realExtensionSource = `package extension

import generation "github.com/plystra/cli/generation/v1"

func Generate(context generation.GenerationContext) (generation.Output, error) {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{Requirements: []generation.Requirement{{
		RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit,
	}}}, nil
}
`
