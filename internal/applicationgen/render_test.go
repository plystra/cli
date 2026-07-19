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
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufwiremap"
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
	options.Composition = dependencyComposition(t)
	options = withManifestProvenance(t, options, resolution)
	output, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPaths := []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/go/adapters/connect/compat/send/v1/handler_gen.go",
		"generated/go/adapters/connect/email/send/v1/handler_gen.go",
		"generated/go/adapters/connect/health/status/v1/handler_gen.go",
		"generated/go/adapters/connect/kernel/health/v1/handler_gen.go",
		"generated/go/adapters/http/compat/send/v1/handler_gen.go",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/adapters/http/kernel/health/v1/handler_gen.go",
		"generated/go/application/main_gen.go",
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
		"generated/go/internal/connectschema/schema_gen.go",
		"generated/go/internal/invocationcontext/context_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/plystra/generated/compat/send/v1/capability.proto",
		"generated/proto/plystra/generated/email/send/v1/capability.proto",
		"generated/proto/plystra/generated/health/status/v1/capability.proto",
		"generated/proto/plystra/generated/kernel/health/v1/capability.proto",
		"generated/proto/wire-map.json",
		"generated/sdk/javascript/.npmrc",
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
	healthContract := string(outputData(t, output, "generated/go/contracts/kernel/health/v1/contract_gen.go"))
	for _, required := range []string{
		"type Request = kernelintrinsic.HealthRequest",
		"type Response = kernelintrinsic.HealthResponse",
		"type ResponseStatus = kernelintrinsic.HealthStatus",
	} {
		if !strings.Contains(healthContract, required) {
			t.Fatalf("intrinsic contract omits %q:\n%s", required, healthContract)
		}
	}
	assembly := string(outputData(t, output, "generated/go/assembly/invocations_gen.go"))
	for _, required := range []string{"kernelintrinsic.NewBindings", "kernelintrinsic.HealthContract()", "func (i Invocations) KernelHealthV1()"} {
		if !strings.Contains(assembly, required) {
			t.Fatalf("intrinsic assembly omits %q:\n%s", required, assembly)
		}
	}
	connectHandler := string(outputData(t, output, "generated/go/adapters/connect/email/send/v1/handler_gen.go"))
	for _, required := range []string{"applicationinvocation.Handle", "return target.Invoke(ctx, request)", "connect.NewUnaryHandler("} {
		if !strings.Contains(connectHandler, required) {
			t.Fatalf("canonical Connect handler omits %q:\n%s", required, connectHandler)
		}
	}
	for _, forbidden := range []string{"generated/go/providers", "kernelinvocation.Handle", "Provider"} {
		if strings.Contains(connectHandler, forbidden) {
			t.Fatalf("canonical Connect handler contains forbidden provider boundary %q:\n%s", forbidden, connectHandler)
		}
	}
	connectAlias := string(outputData(t, output, "generated/go/adapters/connect/compat/send/v1/handler_gen.go"))
	if !strings.Contains(connectAlias, "canonicaladapter.Handler") || strings.Contains(connectAlias, "applicationinvocation") {
		t.Fatalf("Alias Connect handler does not reuse the canonical handler:\n%s", connectAlias)
	}
	manifest := string(outputData(t, output, "generated/manifest.json"))
	for _, required := range []string{
		`"id":"compat.send/v1"`,
		`"target":"email.send/v1"`,
		`"deprecated":"Use email.send/v1 instead."`,
		`"id":"health.status/v1"`,
		`"target":"kernel.health/v1"`,
		`"kind":"application"`,
		`"configuration":{"version":4,"mode":"default"`,
		`"root":{"path":"plystra.yaml","digest":"sha256:`,
		`"dependency_composition_digest":"sha256:`,
		`"application_model_digest":"` + options.ManifestProvenance.ApplicationModelDigest() + `"`,
		`"protobuf_wire_map_digest":"` + options.ManifestProvenance.ProtobufWireMapDigest() + `"`,
		`"path":"http.expose[\"diagnostics.internal/v1\"]"`,
		`"removed":true`,
		`"path":"config[\"acme.business\"][\"password\"]"`,
		`"path":"config[\"acme.business\"][\"legacy\"]"`,
		`example.com/platform@v1.2.3/plystra.yaml config[\"acme.business\"][\"password\"]`,
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("Alias manifest omits %q:\n%s", required, manifest)
		}
	}
	for _, forbidden := range []string{"PRIVATE_APPLICATION_TOKEN", "private-runtime-value"} {
		if strings.Contains(manifest, forbidden) {
			t.Fatalf("Alias manifest contains forbidden value %q:\n%s", forbidden, manifest)
		}
	}
	provenance, err := applicationgen.DecodeManifestProvenance([]byte(manifest))
	baseline := provenance.DependencyBaseline()
	if err != nil || !baseline.Valid() || baseline.Digest() != options.Composition.DependencyDigest() || len(baseline.Records()) != len(options.Composition.Provenance()) {
		t.Fatalf("DecodeManifestProvenance = %#v, %v", baseline.Records(), err)
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

	withAliasResolution := resolvedApplication(t, `capabilities:
  aliases:
    compat.send/v1: email.send/v1
    health.status/v1: kernel.health/v1
`)
	options := withManifestProvenance(t, resolvedOptions(), withAliasResolution)
	withAliases, err := applicationgen.Render(options, withAliasResolution)
	if err != nil {
		t.Fatalf("Render with Aliases: %v", err)
	}
	withoutAliasResolution := resolvedApplication(t, "")
	withoutAliases, err := applicationgen.Render(withManifestProvenance(t, resolvedOptions(), withoutAliasResolution), withoutAliasResolution)
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
		"generated/go/adapters/connect/compat/send/v1/handler_gen.go",
		"generated/go/adapters/connect/health/status/v1/handler_gen.go",
		"generated/go/adapters/http/compat/send/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/clients/compat/send/v1/client_gen.go",
		"generated/go/clients/health/status/v1/client_gen.go",
		"generated/proto/plystra/generated/compat/send/v1/capability.proto",
		"generated/proto/plystra/generated/health/status/v1/capability.proto",
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
	options := withManifestProvenance(t, emptyOptions(applicationModulePath), resolution)
	output, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render empty: %v", err)
	}
	if got := outputPaths(output); !slices.Equal(got, []string{"generated/go/application/main_gen.go", "generated/go/assembly/compatibility_gen.go", "generated/go/assembly/invocations_gen.go", "generated/go/assembly/providers_gen.go", "generated/go/bootstrap/bootstrap_gen.go", "generated/manifest.json", "generated/proto/descriptor-set.pb", "generated/proto/wire-map.json"}) {
		t.Fatalf("empty output paths = %v", got)
	}
	wantManifest, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), options.ManifestProvenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	if !bytes.Equal(outputData(t, output, "generated/manifest.json"), wantManifest) {
		t.Fatalf("empty application manifest = %s", outputData(t, output, "generated/manifest.json"))
	}
}

func TestRenderRequiresConnectForJavaScriptSDK(t *testing.T) {
	t.Parallel()

	resolution := resolvedApplication(t, `capabilities:
  aliases:
    compat.send/v1: email.send/v1
`)
	options := resolvedOptions()
	options.HTTPTransports = applicationmeta.HTTPTransports{REST: true}
	options = withManifestProvenance(t, options, resolution)
	output, err := applicationgen.Render(options, resolution)
	if !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, applicationgen.ErrJavaScriptTransport) || len(output.Files()) != 0 || len(output.ManifestJSON()) != 0 {
		t.Fatalf("Render = %#v, %v", output, err)
	}
	for _, want := range []string{
		`http.transports.connect is false for selected configuration "plystra.yaml"`,
		"official generated JavaScript SDK requires Connect",
		"Alias compat.send/v1 -> email.send/v1",
		"Capability email.send/v1",
		"Capability kernel.health/v1",
		"enable http.transports.connect",
		"http.expose and capabilities.aliases",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Render error %q does not contain %q", err, want)
		}
	}

	emptyResolution := emptyApplication(t)
	emptyOptions := emptyOptions("example.com/acme/rest-only-internal")
	emptyOptions.HTTPTransports = applicationmeta.HTTPTransports{REST: true}
	emptyOptions = withManifestProvenance(t, emptyOptions, emptyResolution)
	if output, err := applicationgen.Render(emptyOptions, emptyResolution); err != nil || len(output.Files()) == 0 || len(output.ManifestJSON()) == 0 {
		t.Fatalf("Render REST-only internal application = %#v, %v", output, err)
	}
}

func TestRenderGeneratesOnlyDeveloperSurfacesForUnrequiredLocalCapability(t *testing.T) {
	t.Parallel()

	schema, err := manifest.ParseConfig([]byte("{}\n"))
	if err != nil {
		t.Fatalf("manifest.ParseConfig: %v", err)
	}
	resolution := unrequiredLocalApplication(t)
	options := applicationgen.Options{
		ModulePath:          applicationModulePath,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Composition:         testComposition(),
		Providers: []assemblygen.ProviderInput{{
			PluginID:   "acme.business",
			ModulePath: applicationModulePath,
			ImportPath: applicationModulePath + "/business",
		}},
		Configurations: []configurationgen.Input{{
			PluginName: "business",
			PluginID:   "acme.business",
			Schema:     schema,
		}},
	}
	output, err := applicationgen.Render(withManifestProvenance(t, options, resolution), resolution)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, filePath := range []string{
		"generated/go/configuration/business_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		if _, exists := outputFile(output, filePath); !exists {
			t.Fatalf("module-owned developer surface %s is absent", filePath)
		}
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/internal/invocationcontext/context_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/sdk/javascript/package.json",
	} {
		if _, exists := outputFile(output, filePath); exists {
			t.Fatalf("unrequired local Capability received application surface %s", filePath)
		}
	}
	if assembly := outputData(t, output, "generated/go/assembly/invocations_gen.go"); bytes.Contains(assembly, []byte("email.send/v1")) {
		t.Fatalf("unrequired local Capability entered runtime assembly:\n%s", assembly)
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
		ModulePath:          businessModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Composition:         testComposition(),
		Providers:           selectedProviderInputs(),
		Configurations: []configurationgen.Input{{
			PluginName: "business",
			PluginID:   "acme.business",
			Schema:     schema,
		}},
	}
	options = withManifestProvenance(t, options, resolution)
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

	emptyResolution := emptyApplication(t)
	withoutConfiguration, err := applicationgen.Render(withManifestProvenance(t, emptyOptions(businessModulePath), emptyResolution), emptyResolution)
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
	invalidModule = withManifestProvenance(t, invalidModule, resolution)
	if _, err := applicationgen.Render(invalidModule, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, assemblygen.ErrRenderProviders) {
		t.Fatalf("Render invalid module error = %v", err)
	}
	missingPackage := resolvedOptions()
	missingPackage.JavaScriptPackage = ""
	missingPackage = withManifestProvenance(t, missingPackage, resolution)
	if _, err := applicationgen.Render(missingPackage, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, javascriptgen.ErrRender) {
		t.Fatalf("Render missing JavaScript package error = %v", err)
	}
	missingProvider := withManifestProvenance(t, resolvedOptions(), resolution)
	missingProvider.Providers = nil
	if _, err := applicationgen.Render(missingProvider, resolution); !errors.Is(err, applicationgen.ErrRender) || !errors.Is(err, applicationgen.ErrResolution) {
		t.Fatalf("missing selected provider error = %v", err)
	}
}

func resolvedOptions() applicationgen.Options {
	return applicationgen.Options{
		ModulePath:          applicationModulePath,
		JavaScriptPackage:   applicationSDKPackage,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Composition:         testComposition(),
		Providers:           selectedProviderInputs(),
	}
}

func emptyOptions(modulePath string) applicationgen.Options {
	return applicationgen.Options{
		ModulePath:          modulePath,
		KernelModuleVersion: "v0.0.0",
		KernelBuildIdentity: "application-render-test",
		HTTPTransports:      applicationmeta.HTTPTransports{Connect: true},
		Composition:         testComposition(),
	}
}

func withManifestProvenance(t testing.TB, options applicationgen.Options, resolution generationresolution.ExtensionResult) applicationgen.Options {
	t.Helper()
	projection, err := applicationgen.ProtobufProjection(options.HTTPTransports, resolution)
	if err != nil {
		t.Fatalf("ProtobufProjection: %v", err)
	}
	wireMap, err := protobufwiremap.Build(projection, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	options.ProtobufWireMap = wireMap
	modelDigest, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:          options.ModulePath,
		JavaScriptPackage:   options.JavaScriptPackage,
		KernelModuleVersion: options.KernelModuleVersion,
		KernelBuildIdentity: options.KernelBuildIdentity,
		HTTPTransports:      options.HTTPTransports,
		Configurations:      options.Configurations,
		Providers:           options.Providers,
		Resolution:          resolution,
		ProtobufWireMap:     wireMap,
	})
	if err != nil {
		t.Fatalf("ApplicationModelDigest: %v", err)
	}
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootData:               []byte("{}\n"),
		SelectedPath:           "plystra.yaml",
		SelectedData:           []byte("{}\n"),
		Composition:            options.Composition,
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: modelDigest,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	options.ManifestProvenance = provenance
	return options
}

func applicationModelDigest(t testing.TB, options applicationgen.ApplicationModelOptions) (string, error) {
	t.Helper()
	projection, err := applicationgen.ProtobufProjection(options.HTTPTransports, options.Resolution)
	if err != nil {
		return "", err
	}
	wireMap, err := protobufwiremap.Build(projection, nil, false, "")
	if err != nil {
		return "", err
	}
	options.ProtobufWireMap = wireMap
	return applicationgen.ApplicationModelDigest(options)
}

func testComposition() applicationmeta.Composition {
	composition, err := applicationmeta.Compose(nil, applicationmeta.Manifest{}, func(string) (manifest.Config, bool) {
		return manifest.Config{}, false
	})
	if err != nil {
		panic(err)
	}
	return composition
}

func dependencyComposition(t testing.TB) applicationmeta.Composition {
	t.Helper()
	schema, err := manifest.ParseConfig([]byte("legacy: {type: string}\npassword: {type: secret}\n"))
	if err != nil {
		t.Fatalf("manifest.ParseConfig: %v", err)
	}
	dependency, err := applicationmeta.Parse([]byte("http: {expose: {remove: [diagnostics.internal/v1]}}\nconfig: {acme.business: {legacy: null, password: {env: PRIVATE_APPLICATION_TOKEN}}}\n"))
	if err != nil {
		t.Fatalf("applicationmeta.Parse dependency: %v", err)
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependency,
	}}, applicationmeta.Manifest{}, func(pluginID string) (manifest.Config, bool) {
		return schema, pluginID == "acme.business"
	})
	if err != nil {
		t.Fatalf("applicationmeta.Compose: %v", err)
	}
	return composition
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

func unrequiredLocalApplication(t testing.TB) generationresolution.ExtensionResult {
	t.Helper()
	email := normalizedContract(t, `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	catalog, err := generationactivation.New(nil)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	resolution, err := generationresolution.ResolveExtensions(t.Context(), generationresolution.ExtensionInput{
		Input: generationresolution.Input{
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
				ModulePath:        applicationModulePath,
				Provides:          []string{"email.send/v1"},
				BuildMetadataJSON: []byte("{}"),
			},
			Local:      true,
			ModuleRoot: "application-module",
			PluginPath: "business",
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: email, Sources: []string{"application-module/business/capabilities/email.send/v1/capability.yaml"}, Exposure: generation.Exposure{Go: true}}},
	})
	if err != nil {
		t.Fatalf("ResolveExtensions: %v", err)
	}
	return resolution
}

func resolvedApplication(t testing.TB, applicationYAML string) generationresolution.ExtensionResult {
	return resolvedApplicationWithEmail(t, applicationYAML, `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
}

func resolvedApplicationWithEmail(t testing.TB, applicationYAML, emailSource string) generationresolution.ExtensionResult {
	t.Helper()
	email := normalizedContract(t, emailSource)
	health := normalizedContract(t, `id: kernel.health/v1
description: Reports intrinsic Kernel liveness.
request: {}
response:
  status:
    type: string
    enum: [healthy]
    required: true
errors: []
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
			{ContractJSON: email, Sources: []string{"business-module/business/capabilities/email.send/v1/capability.yaml"}, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
			{ContractJSON: health, Sources: []string{"github.com/plystra/kernel/capability/catalog kernel.health/v1"}, Intrinsic: true, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
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
