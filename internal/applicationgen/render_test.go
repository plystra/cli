package applicationgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/kernel/plugin/manifest"
)

const (
	applicationModulePath = "example.com/acme/application"
	applicationSDKPackage = "@acme/application-sdk"
	businessModulePath    = "example.com/acme/business"
)

func TestRenderProducesOneDeterministicCanonicalAndAliasTree(t *testing.T) {
	t.Parallel()

	resolution := resolvedApplication(t, `capabilities:
  aliases:
    compat.send/v1:
      target: email.send/v1
      deprecated:
        message: Use email.send/v1 instead.
    health.status/v1: kernel.health/v1
`)
	options := resolvedOptions()
	output, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPaths := []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/go/adapters/http/compat/send/v1/handler_gen.go",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/adapters/http/kernel/health/v1/handler_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/clients/compat/send/v1/client_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/clients/health/status/v1/client_gen.go",
		"generated/go/clients/kernel/health/v1/client_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/contracts/kernel/health/v1/contract_gen.go",
		"generated/go/internal/invocationcontext/context_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/index.ts",
		"generated/sdk/javascript/src/operations/compat/send/v1.ts",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
		"generated/sdk/javascript/src/operations/health/status/v1.ts",
		"generated/sdk/javascript/src/operations/kernel/health/v1.ts",
		"generated/sdk/javascript/src/runtime.ts",
		"generated/sdk/javascript/tsconfig.json",
	}
	if got := outputPaths(output); !slices.Equal(got, wantPaths) {
		t.Fatalf("generated paths =\n%v\nwant:\n%v", got, wantPaths)
	}
	if slices.Contains(outputPaths(output), "generated/go/providers/kernel/health/v1/provider_gen.go") {
		t.Fatal("intrinsic Kernel Capability received an ordinary provider surface")
	}
	manifest := string(outputData(t, output, "generated/manifest.json"))
	for _, required := range []string{
		`"id":"compat.send/v1"`,
		`"target":"email.send/v1"`,
		`"deprecated":"Use email.send/v1 instead."`,
		`"id":"health.status/v1"`,
		`"target":"kernel.health/v1"`,
		`"kind":"application"`,
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("Alias manifest omits %q:\n%s", required, manifest)
		}
	}
	for _, forbidden := range []string{"acme.business", "secret", "password"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("Alias manifest contains forbidden value %q:\n%s", forbidden, manifest)
		}
	}
	for _, file := range output.Files() {
		if filepath.Ext(file.Path()) != ".go" {
			continue
		}
		if _, err := parser.ParseFile(token.NewFileSet(), file.Path(), file.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse %s: %v", file.Path(), err)
		}
	}

	repeated, err := applicationgen.Render(options, resolution)
	if err != nil || !sameOutput(output, repeated) {
		t.Fatalf("repeated Render is not deterministic: %v", err)
	}
	root := t.TempDir()
	report, err := generatedfiles.Install(root, output, func(string) error { return nil })
	if err != nil || !report.Clean() {
		t.Fatalf("Install = %#v, %v", report.Changes(), err)
	}
	if checked, err := generatedfiles.Check(root, output); err != nil || !checked.Clean() {
		t.Fatalf("Check installed output = %#v, %v", checked.Changes(), err)
	}
}

func TestRenderRemovesAliasSurfacesWhenFinalMapChanges(t *testing.T) {
	t.Parallel()

	options := resolvedOptions()
	withAliases, err := applicationgen.Render(options, resolvedApplication(t, `capabilities:
  aliases:
    compat.send/v1: email.send/v1
    health.status/v1: kernel.health/v1
`))
	if err != nil {
		t.Fatalf("Render with Aliases: %v", err)
	}
	withoutAliases, err := applicationgen.Render(options, resolvedApplication(t, ""))
	if err != nil {
		t.Fatalf("Render without Aliases: %v", err)
	}
	root := t.TempDir()
	if report, err := generatedfiles.Install(root, withAliases, func(string) error { return nil }); err != nil || !report.Clean() {
		t.Fatalf("Install with Aliases = %#v, %v", report.Changes(), err)
	}
	report, err := generatedfiles.Check(root, withoutAliases)
	if err != nil {
		t.Fatalf("Check without Aliases: %v", err)
	}
	wantObsolete := []string{
		"generated/go/adapters/http/compat/send/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/clients/compat/send/v1/client_gen.go",
		"generated/go/clients/health/status/v1/client_gen.go",
		"generated/sdk/javascript/src/operations/compat/send/v1.ts",
		"generated/sdk/javascript/src/operations/health/status/v1.ts",
	}
	if !slices.Equal(report.Obsolete(), wantObsolete) || len(report.Missing()) != 0 || len(report.Unexpected()) != 0 {
		t.Fatalf("Alias removal drift = %#v", report.Changes())
	}
	if installed, err := generatedfiles.Install(root, withoutAliases, func(string) error { return nil }); err != nil || !installed.Clean() {
		t.Fatalf("Install without Aliases = %#v, %v", installed.Changes(), err)
	}
	for _, filePath := range wantObsolete {
		if _, exists := outputFile(withoutAliases, filePath); exists {
			t.Fatalf("Alias file %s remains in desired output", filePath)
		}
	}
}

func TestRenderSupportsEmptyApplicationWithoutSDKOrDocumentation(t *testing.T) {
	t.Parallel()

	resolution := emptyApplication(t)
	output, err := applicationgen.Render(applicationgen.Options{ModulePath: applicationModulePath}, resolution)
	if err != nil {
		t.Fatalf("Render empty: %v", err)
	}
	if got := outputPaths(output); !slices.Equal(got, []string{"generated/go/assembly/compatibility_gen.go", "generated/go/assembly/invocations_gen.go", "generated/go/assembly/providers_gen.go", "generated/go/bootstrap/bootstrap_gen.go", "generated/manifest.json"}) {
		t.Fatalf("empty output paths = %v", got)
	}
	if string(outputData(t, output, "generated/manifest.json")) != "{\"capability_aliases\":[]}\n" {
		t.Fatalf("empty Alias manifest = %s", outputData(t, output, "generated/manifest.json"))
	}
}

func TestRenderManagesSchemaOnlyLocalPluginConfiguration(t *testing.T) {
	t.Parallel()

	schema, err := manifest.ParseConfig([]byte(`
host: {type: string, required: true}
password: {type: secret, required: true}
timeout: {type: duration, default: 5s}
`))
	if err != nil {
		t.Fatalf("manifest.ParseConfig: %v", err)
	}
	resolution := resolvedApplication(t, "")
	options := applicationgen.Options{
		ModulePath:        businessModulePath,
		JavaScriptPackage: applicationSDKPackage,
		Providers:         selectedProviderInputs(),
		Configurations: []configurationgen.Input{{
			PluginName: "business",
			PluginID:   "acme.business",
			Schema:     schema,
		}},
	}
	withConfiguration, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render with configuration: %v", err)
	}
	const configurationPath = "generated/go/configuration/business_gen.go"
	configuration := outputData(t, withConfiguration, configurationPath)
	for _, required := range []string{"type BusinessConfig struct", "DecodeBusiness", "kernelconfiguration.Decode", "Password kernelconfiguration.Secret", "Timeout time.Duration"} {
		if !bytes.Contains(configuration, []byte(required)) {
			t.Fatalf("configuration source omits %q:\n%s", required, configuration)
		}
	}
	for _, forbidden := range []string{"runtime-private-host", "APPLICATION_SECRET_TARGET"} {
		if bytes.Contains(configuration, []byte(forbidden)) {
			t.Fatalf("configuration source contains private value %q:\n%s", forbidden, configuration)
		}
	}

	withoutConfiguration, err := applicationgen.Render(applicationgen.Options{ModulePath: businessModulePath}, emptyApplication(t))
	if err != nil {
		t.Fatalf("Render without configuration: %v", err)
	}
	root := t.TempDir()
	if report, err := generatedfiles.Install(root, withConfiguration, func(string) error { return nil }); err != nil || !report.Clean() {
		t.Fatalf("Install with configuration = %#v, %v", report.Changes(), err)
	}
	report, err := generatedfiles.Check(root, withoutConfiguration)
	if err != nil || !slices.Contains(report.Obsolete(), configurationPath) {
		t.Fatalf("configuration cleanup = %#v, %v", report.Changes(), err)
	}
}

func TestRenderRejectsInvalidResolutionModuleAndPackage(t *testing.T) {
	t.Parallel()

	if _, err := applicationgen.Render(applicationgen.Options{ModulePath: applicationModulePath}, generationresolution.ExtensionResult{}); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, applicationgen.ErrResolution) {
		t.Fatalf("Render zero resolution error = %v", err)
	}
	resolution := resolvedApplication(t, "")
	invalidModule := resolvedOptions()
	invalidModule.ModulePath = "not a module path"
	if _, err := applicationgen.Render(invalidModule, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, generationlowering.ErrLower) {
		t.Fatalf("Render invalid module error = %v", err)
	}
	missingPackage := resolvedOptions()
	missingPackage.JavaScriptPackage = ""
	if _, err := applicationgen.Render(missingPackage, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, javascriptgen.ErrRender) {
		t.Fatalf("Render missing JavaScript package error = %v", err)
	}
	missingProvider := resolvedOptions()
	missingProvider.Providers = nil
	if _, err := applicationgen.Render(missingProvider, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, applicationgen.ErrResolution) {
		t.Fatalf("missing selected provider error = %v", err)
	}
}

func resolvedOptions() applicationgen.Options {
	return applicationgen.Options{
		ModulePath:        applicationModulePath,
		JavaScriptPackage: applicationSDKPackage,
		Providers:         selectedProviderInputs(),
	}
}

func selectedProviderInputs() []assemblygen.ProviderInput {
	return []assemblygen.ProviderInput{{
		PluginID:   "acme.business",
		ModulePath: businessModulePath,
		ImportPath: businessModulePath + "/business",
	}}
}

func emptyApplication(t testing.TB) generationresolution.ExtensionResult {
	t.Helper()
	catalog, err := generationactivation.New(nil)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	resolution, err := generationresolution.ResolveExtensions(t.Context(), generationresolution.ExtensionInput{
		Input: generationresolution.Input{Activations: catalog},
	})
	if err != nil {
		t.Fatalf("ResolveExtensions: %v", err)
	}
	return resolution
}

func resolvedApplication(t testing.TB, applicationYAML string) generationresolution.ExtensionResult {
	t.Helper()
	email := normalizedContract(t, `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	health := normalizedContract(t, `id: kernel.health/v1
response:
  healthy: {type: boolean, required: true}
`)
	var aliases []applicationmeta.Alias
	if applicationYAML != "" {
		manifest, err := applicationmeta.Parse([]byte(applicationYAML))
		if err != nil {
			t.Fatalf("applicationmeta.Parse: %v", err)
		}
		aliases = manifest.Aliases()
	}
	catalog, err := generationactivation.New(nil)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	resolution, err := generationresolution.ResolveExtensions(t.Context(), generationresolution.ExtensionInput{
		Input: generationresolution.Input{
			Requirements: []providerresolution.Requirement{
				{Contract: email, Source: "application client email.send/v1"},
				{Contract: health, Source: "application health"},
			},
			Candidates: []providerresolution.Candidate{{
				PluginID: "acme.business",
				Contract: email,
				Source:   "business/capabilities/email.send/v1/capability.yaml",
			}},
			Activations: catalog,
		},
		Plugins: []generationresolution.Plugin{{
			Context: generation.PluginInput{
				ID:                "acme.business",
				ModulePath:        businessModulePath,
				Provides:          []string{"email.send/v1"},
				BuildMetadataJSON: []byte("{}"),
			},
			ModuleRoot: "business-module",
			PluginPath: "business",
		}},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: email, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
			{ContractJSON: health, Intrinsic: true, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
		},
		ApplicationAliases: aliases,
	})
	if err != nil {
		t.Fatalf("ResolveExtensions: %v", err)
	}
	return resolution
}

func normalizedContract(t testing.TB, source string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}

func outputPaths(output generatedfiles.Output) []string {
	files := output.Files()
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path()
	}
	return paths
}

func outputData(t testing.TB, output generatedfiles.Output, filePath string) []byte {
	t.Helper()
	data, exists := outputFile(output, filePath)
	if !exists {
		t.Fatalf("generated output omits %s", filePath)
	}
	return data
}

func outputFile(output generatedfiles.Output, filePath string) ([]byte, bool) {
	for _, file := range output.Files() {
		if file.Path() == filePath {
			return file.Data(), true
		}
	}
	return nil, false
}

func sameOutput(left, right generatedfiles.Output) bool {
	leftFiles, rightFiles := left.Files(), right.Files()
	if len(leftFiles) != len(rightFiles) || !bytes.Equal(left.ManifestJSON(), right.ManifestJSON()) {
		return false
	}
	for index := range leftFiles {
		if leftFiles[index].Path() != rightFiles[index].Path() || !bytes.Equal(leftFiles[index].Data(), rightFiles[index].Data()) {
			return false
		}
	}
	return true
}
