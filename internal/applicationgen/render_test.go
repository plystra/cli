package applicationgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfacecompatibility"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/transportprovenance"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/kernel/plugin/manifest"
)

const (
	applicationModulePath = "example.com/acme/application"
	applicationSDKPackage = "@acme/application-sdk"
	businessModulePath    = "example.com/acme/business"
)

func TestRenderProducesOneDeterministicCanonicalAndAliasTree(t *testing.T) {
	t.Parallel()

	composition := dependencyComposition(t)
	resolution := resolvedApplicationWithComposition(t, `capabilities:
  aliases:
    compat.send/v1:
      target: email.send/v1
      deprecated:
        message: Use email.send/v1 instead.
    health.status/v1: kernel.health/v1
`, composition)
	options := resolvedOptions()
	options.Composition = composition
	options.HTTPCORS = &applicationmeta.HTTPCORS{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
	}
	options = withManifestProvenance(t, options, resolution)
	output, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertBootstrapMatchesManifestProvenance(t, output, options)
	wantPaths := []string{
		"generated/compatibility/interface-documentation.json",
		"generated/compatibility/interface-javascript.json",
		"generated/compatibility/interface-metadata.json",
		"generated/compatibility/interface-transport.json",
		"generated/compatibility/interfaces.json",
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
		"generated/go/assembly/interfaces_gen.go",
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
		"generated/proto/plystra/generated/transport/v1/error.proto",
		"generated/proto/wire-map.json",
		"generated/sdk/javascript/.npmrc",
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/descriptors.ts",
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
		"type Request = kernelinterface.Request",
		"type Response = kernelinterface.Response",
		"type ResponseStatus = string",
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
	for _, required := range []string{"applicationinvocation.Handle", "return target.Invoke(ctx, request)", "connect.NewUnaryHandler(", `case "https://app.example.com":`, "const plystraCORSAllowCredentials = true"} {
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
	if !strings.Contains(connectAlias, "canonicaladapter.Handler") || !strings.Contains(connectAlias, `case "https://app.example.com":`) || strings.Contains(connectAlias, "applicationinvocation") {
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
		`"configuration":{"version":5,"mode":"default"`,
		`"root":{"path":"plystra.yaml","digest":"sha256:`,
		`"dependency_composition_digest":"sha256:`,
		`"application_model_digest":"` + options.ManifestProvenance.ApplicationModelDigest() + `"`,
		`"protobuf_wire_map_digest":"` + options.ManifestProvenance.ProtobufWireMapDigest() + `"`,
		`"path":"http.expose[\"diagnostics.internal/v1\"]"`,
		`"removed":true`,
		`"path":"config[\"example.com/acme/business.New\"][\"password\"]"`,
		`"path":"config[\"example.com/acme/business.New\"][\"legacy\"]"`,
		`example.com/platform@v1.2.3/plystra.yaml config[\"example.com/acme/business.New\"][\"password\"]`,
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
	wantRetired := []string{
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
	for _, filePath := range wantRetired {
		if !slices.Contains(report.Stale(), filePath) {
			t.Fatalf("Alias removal stale paths omit %s: %#v", filePath, report.Changes())
		}
	}
	if len(report.Missing()) != 0 || len(report.Unexpected()) != 0 || len(report.ManuallyModified()) != 0 {
		t.Fatalf("Alias removal drift = %#v", report.Changes())
	}
	if installed, err := generatedfiles.Install(root, withoutAliases, func(string) error { return nil }); err != nil || !installed.Clean() {
		t.Fatalf("Install without Aliases = %#v, %v", installed.Changes(), err)
	}
	for _, filePath := range wantRetired {
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
	if got := outputPaths(output); !slices.Equal(got, []string{"generated/compatibility/interface-documentation.json", "generated/compatibility/interface-javascript.json", "generated/compatibility/interface-metadata.json", "generated/compatibility/interface-transport.json", "generated/compatibility/interfaces.json", "generated/go/application/main_gen.go", "generated/go/assembly/compatibility_gen.go", "generated/go/assembly/interfaces_gen.go", "generated/go/assembly/invocations_gen.go", "generated/go/assembly/providers_gen.go", "generated/go/bootstrap/bootstrap_gen.go", "generated/manifest.json", "generated/proto/descriptor-set.pb", "generated/proto/wire-map.json"}) {
		t.Fatalf("empty output paths = %v", got)
	}
	wantManifest, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), resolution.Context(), options.ManifestProvenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	if !bytes.Equal(outputData(t, output, "generated/manifest.json"), wantManifest) {
		t.Fatalf("empty application manifest = %s", outputData(t, output, "generated/manifest.json"))
	}
	documentation, err := interfacecompatibility.DecodeDocumentation(
		outputData(t, output, interfacecompatibility.DocumentationPath),
	)
	if err != nil || len(documentation.Artifacts()) != 0 {
		t.Fatalf("empty documentation baseline = %#v, %v", documentation.Artifacts(), err)
	}
}

func TestRenderDocumentsHTTPOnlyCanonicalTargets(t *testing.T) {
	t.Parallel()

	resolution := resolvedHTTPOnlyApplication(t)
	options := withManifestProvenance(t, resolvedOptions(), resolution)
	output, err := applicationgen.Render(options, resolution)
	if err != nil {
		t.Fatalf("Render HTTP-only application: %v", err)
	}
	if _, exists := outputFile(output, "generated/sdk/javascript/package.json"); exists {
		t.Fatal("HTTP-only application unexpectedly generated a JavaScript package")
	}
	for _, path := range []string{
		apidocgen.InterfaceReferencePath,
		apidocgen.OpenAPIPath,
	} {
		data, exists := outputFile(output, path)
		if !exists {
			t.Fatalf("HTTP-only application omits %s", path)
		}
		for _, id := range []string{"email.send/v1", "kernel.health/v1"} {
			if !bytes.Contains(data, []byte(id)) {
				t.Fatalf("%s omits HTTP-only target %s:\n%s", path, id, data)
			}
		}
	}
	documentation, err := interfacecompatibility.DecodeDocumentation(
		outputData(t, output, interfacecompatibility.DocumentationPath),
	)
	if err != nil || len(documentation.Artifacts()) != 2 {
		t.Fatalf("HTTP-only documentation baseline = %#v, %v", documentation.Artifacts(), err)
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
	if err != nil || !slices.Contains(report.Stale(), configurationPath) {
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
	invalidModule.ImplementationAssembly.ModulePath = applicationModulePath
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
	missingTransportBaseline := withManifestProvenance(t, resolvedOptions(), resolution)
	missingTransportBaseline.InterfaceTransport = interfacecompatibility.TransportBaseline{}
	if _, err := applicationgen.Render(missingTransportBaseline, resolution); !errors.Is(err, applicationgen.ErrRender) ||
		!errors.Is(err, applicationgen.ErrResolution) ||
		!strings.Contains(err.Error(), "transport compatibility baseline is absent or invalid") {
		t.Fatalf("missing Interface transport baseline error = %v", err)
	}
	missingJavaScriptBaseline := withManifestProvenance(t, resolvedOptions(), resolution)
	missingJavaScriptBaseline.InterfaceJavaScript = interfacecompatibility.JavaScriptBaseline{}
	if _, err := applicationgen.Render(missingJavaScriptBaseline, resolution); !errors.Is(err, applicationgen.ErrRender) ||
		!errors.Is(err, applicationgen.ErrResolution) ||
		!strings.Contains(err.Error(), "JavaScript compatibility baseline is absent or invalid") {
		t.Fatalf("missing Interface JavaScript baseline error = %v", err)
	}
}

func TestRenderRequiresMatchingTransportConfigurationProvenance(t *testing.T) {
	t.Parallel()

	missing := resolvedApplicationWithConfigurationProvenance(t, "", nil)
	missingOptions := withManifestProvenance(t, resolvedOptions(), missing)
	if _, err := applicationgen.Render(missingOptions, missing); !errors.Is(err, applicationgen.ErrResolution) || !strings.Contains(err.Error(), "omits selected configuration identity") {
		t.Fatalf("Render(missing context provenance) error = %v", err)
	}

	resolution := resolvedApplication(t, "")
	environmentManifest := withManifestProvenanceSelection(t, resolvedOptions(), resolution, applicationgen.ConfigurationModeEnvironment, "production", "plystra.production.yaml", []byte("{}\n"))
	if _, err := applicationgen.Render(environmentManifest, resolution); !errors.Is(err, applicationgen.ErrResolution) || !strings.Contains(err.Error(), "selection mode disagrees") {
		t.Fatalf("Render(context/manifest selection mismatch) error = %v", err)
	}

	composition := dependencyComposition(t)
	dependencyManifest := resolvedOptions()
	dependencyManifest.Composition = composition
	dependencyManifest = withManifestProvenance(t, dependencyManifest, resolution)
	if _, err := applicationgen.Render(dependencyManifest, resolution); !errors.Is(err, applicationgen.ErrResolution) || !strings.Contains(err.Error(), "dependency-composition digest disagrees") {
		t.Fatalf("Render(context/composition mismatch) error = %v", err)
	}
}

func TestRenderSelectionDriftsManifestButKeepsEqualModelTransportSourceStable(t *testing.T) {
	t.Parallel()

	composition := testComposition()
	defaultResolution := resolvedApplicationWithConfigurationProvenance(t, "", defaultConfigurationProvenance(t, composition))
	defaultOptions := withManifestProvenance(t, resolvedOptions(), defaultResolution)
	defaultOutput, err := applicationgen.Render(defaultOptions, defaultResolution)
	if err != nil {
		t.Fatalf("Render(default): %v", err)
	}

	environmentInput := selectedConfigurationProvenance(t, composition, generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml", []byte("{}\n"))
	environmentResolution := resolvedApplicationWithConfigurationProvenance(t, "", environmentInput)
	environmentOptions := withManifestProvenanceSelection(t, resolvedOptions(), environmentResolution, applicationgen.ConfigurationModeEnvironment, "production", "plystra.production.yaml", []byte("{}\n"))
	environmentOutput, err := applicationgen.Render(environmentOptions, environmentResolution)
	if err != nil {
		t.Fatalf("Render(environment): %v", err)
	}

	explicitInput := selectedConfigurationProvenance(t, composition, generation.ConfigurationModeExplicit, "", "deploy/customer-a.yaml", []byte("{}\n"))
	explicitResolution := resolvedApplicationWithConfigurationProvenance(t, "", explicitInput)
	explicitOptions := withManifestProvenanceSelection(t, resolvedOptions(), explicitResolution, applicationgen.ConfigurationModeExplicit, "", "deploy/customer-a.yaml", []byte("{}\n"))
	explicitOutput, err := applicationgen.Render(explicitOptions, explicitResolution)
	if err != nil {
		t.Fatalf("Render(explicit): %v", err)
	}
	assertBootstrapMatchesManifestProvenance(t, defaultOutput, defaultOptions)
	assertBootstrapMatchesManifestProvenance(t, environmentOutput, environmentOptions)
	assertBootstrapMatchesManifestProvenance(t, explicitOutput, explicitOptions)

	defaultManifest := outputData(t, defaultOutput, aliasManifestPathForTest)
	environmentManifest := outputData(t, environmentOutput, aliasManifestPathForTest)
	explicitManifest := outputData(t, explicitOutput, aliasManifestPathForTest)
	if bytes.Equal(defaultManifest, environmentManifest) || bytes.Equal(defaultManifest, explicitManifest) || bytes.Equal(environmentManifest, explicitManifest) {
		t.Fatal("configuration selection did not produce generated-manifest drift")
	}
	defaultBootstrap := outputData(t, defaultOutput, "generated/go/bootstrap/bootstrap_gen.go")
	environmentBootstrap := outputData(t, environmentOutput, "generated/go/bootstrap/bootstrap_gen.go")
	explicitBootstrap := outputData(t, explicitOutput, "generated/go/bootstrap/bootstrap_gen.go")
	if bytes.Equal(defaultBootstrap, environmentBootstrap) || bytes.Equal(defaultBootstrap, explicitBootstrap) || bytes.Equal(environmentBootstrap, explicitBootstrap) {
		t.Fatal("configuration selection did not produce generated-bootstrap provenance drift")
	}
	for name, output := range map[string]generatedfiles.Output{"environment": environmentOutput, "explicit": explicitOutput} {
		if !sameTransportOutput(defaultOutput, output) {
			t.Fatalf("%s selection changed transport source for an equal effective build model", name)
		}
		for _, file := range output.Files() {
			if !transportPath(file.Path()) {
				continue
			}
			for _, forbidden := range []string{"production", "plystra.production.yaml", "deploy/customer-a.yaml", "PRIVATE_APPLICATION_TOKEN", "resolved-secret-value"} {
				if bytes.Contains(file.Data(), []byte(forbidden)) {
					t.Fatalf("%s contains selected configuration detail %q", file.Path(), forbidden)
				}
			}
		}
	}
}

const aliasManifestPathForTest = "generated/manifest.json"

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
	return withManifestProvenanceSelection(t, options, resolution, applicationgen.ConfigurationModeDefault, "", "plystra.yaml", []byte("{}\n"))
}

func withManifestProvenanceSelection(t testing.TB, options applicationgen.Options, resolution generationresolution.ExtensionResult, mode, environment, selectedPath string, selectedData []byte) applicationgen.Options {
	t.Helper()
	if !options.InterfaceCompatibility.Valid() {
		baseline, err := interfacecompatibility.New(nil)
		if err != nil {
			t.Fatalf("interfacecompatibility.New: %v", err)
		}
		options.InterfaceCompatibility = baseline
	}
	if !options.InterfaceMetadata.Valid() {
		baseline, err := interfacecompatibility.NewMetadata(nil)
		if err != nil {
			t.Fatalf("interfacecompatibility.NewMetadata: %v", err)
		}
		options.InterfaceMetadata = baseline
	}
	projection, err := applicationgen.ProtobufProjection(options.HTTPTransports, resolution)
	if err != nil {
		t.Fatalf("ProtobufProjection: %v", err)
	}
	interfaceProjection := emptyInterfaceWireProjection(t, projection)
	wireMap, err := protobufwiremap.Build(projection, interfaceProjection, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	options.ProtobufWireMap = wireMap
	descriptorEvidence, err := protobufdescriptor.BuildWithInterfaces(projection, wireMap, interfaceProjection)
	if err != nil {
		t.Fatalf("protobufdescriptor.BuildWithInterfaces: %v", err)
	}
	transportBaseline, err := interfacecompatibility.BuildTransport(wireMap, descriptorEvidence)
	if err != nil {
		t.Fatalf("interfacecompatibility.BuildTransport: %v", err)
	}
	options.InterfaceTransport = transportBaseline
	javaScriptModel, err := applicationgen.JavaScriptModel(resolution)
	if err != nil {
		t.Fatalf("applicationgen.JavaScriptModel: %v", err)
	}
	javaScriptAPI, err := javascriptgen.BuildPublicAPI(
		options.JavaScriptPackage,
		javaScriptModel,
		interfaceProjection,
	)
	if err != nil {
		t.Fatalf("javascriptgen.BuildPublicAPI: %v", err)
	}
	javaScriptBaseline, err := interfacecompatibility.NewJavaScript(javaScriptAPI)
	if err != nil {
		t.Fatalf("interfacecompatibility.NewJavaScript: %v", err)
	}
	options.InterfaceJavaScript = javaScriptBaseline
	modelDigest, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:             options.ModulePath,
		JavaScriptPackage:      options.JavaScriptPackage,
		KernelModuleVersion:    options.KernelModuleVersion,
		KernelBuildIdentity:    options.KernelBuildIdentity,
		HTTPTransports:         options.HTTPTransports,
		HTTPCORS:               options.HTTPCORS,
		Configurations:         options.Configurations,
		Providers:              options.Providers,
		InterfaceProxies:       options.InterfaceProxies,
		ImplementationAdapters: options.ImplementationAdapters,
		ImplementationAssembly: options.ImplementationAssembly,
		InterfacePolicies:      options.Composition.Manifest().InterfacePolicies(),
		Resolution:             resolution,
		ProtobufWireMap:        wireMap,
	})
	if err != nil {
		t.Fatalf("ApplicationModelDigest: %v", err)
	}
	rootDigest := testConfigurationLayerDigest(t, []byte("{}\n"), false)
	selectedDigest := testConfigurationLayerDigest(t, selectedData, mode == applicationgen.ConfigurationModeEnvironment)
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   mode,
		Environment:            environment,
		RootPath:               "plystra.yaml",
		RootDigest:             rootDigest,
		SelectedPath:           selectedPath,
		SelectedDigest:         selectedDigest,
		Composition:            options.Composition,
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: modelDigest,
		InterfaceProvenance:    emptyInterfaceProvenance(t),
		TransportToolchain:     currentTransportToolchain(t),
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	options.ManifestProvenance = provenance
	return options
}

func emptyInterfaceProvenance(t testing.TB) interfaceprovenance.Provenance {
	t.Helper()
	provenance, err := interfaceprovenance.New(interfaceprovenance.Input{})
	if err != nil {
		t.Fatalf("interfaceprovenance.New: %v", err)
	}
	return provenance
}

func currentTransportToolchain(t testing.TB) transporttoolchain.Identity {
	t.Helper()
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("transporttoolchain.Current: %v", err)
	}
	return toolchain
}

func defaultConfigurationProvenance(t testing.TB, composition applicationmeta.Composition) *generation.ConfigurationProvenanceInput {
	return selectedConfigurationProvenance(t, composition, generation.ConfigurationModeDefault, "", "plystra.yaml", []byte("{}\n"))
}

func selectedConfigurationProvenance(t testing.TB, composition applicationmeta.Composition, mode generation.ConfigurationMode, environment, selectedPath string, selectedData []byte) *generation.ConfigurationProvenanceInput {
	t.Helper()
	rootDigest := testConfigurationLayerDigest(t, []byte("{}\n"), false)
	selectedDigest := testConfigurationLayerDigest(t, selectedData, mode == generation.ConfigurationModeEnvironment)
	return &generation.ConfigurationProvenanceInput{
		Mode:                        mode,
		Environment:                 environment,
		RootPath:                    "plystra.yaml",
		RootDigest:                  rootDigest,
		SelectedPath:                selectedPath,
		SelectedDigest:              selectedDigest,
		DependencyCompositionDigest: composition.DependencyDigest(),
	}
}

func testConfigurationLayerDigest(t testing.TB, data []byte, overlay bool) string {
	t.Helper()
	var (
		configuration applicationmeta.Manifest
		err           error
	)
	if overlay {
		configuration, err = applicationmeta.ParseOverlaySource("plystra.environment.yaml", data)
	} else {
		configuration, err = applicationmeta.Parse(data)
	}
	if err != nil {
		t.Fatalf("parse typed configuration layer: %v", err)
	}
	digest, err := applicationmeta.ConfigurationLayerDigest(configuration, func(constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	})
	if err != nil {
		t.Fatalf("ConfigurationLayerDigest: %v", err)
	}
	return digest
}

func assertBootstrapMatchesManifestProvenance(t testing.TB, output generatedfiles.Output, options applicationgen.Options) {
	t.Helper()
	manifest := options.ManifestProvenance
	provenance, err := transportprovenance.New(transportprovenance.Input{
		Mode:                        generation.ConfigurationMode(manifest.Mode()),
		Environment:                 manifest.Environment(),
		RootPath:                    manifest.RootPath(),
		RootDigest:                  manifest.RootDigest(),
		SelectedPath:                manifest.SelectedPath(),
		SelectedDigest:              manifest.SelectedDigest(),
		DependencyCompositionDigest: options.Composition.DependencyDigest(),
		ApplicationModelDigest:      manifest.ApplicationModelDigest(),
	})
	if err != nil {
		t.Fatalf("transportprovenance.New from manifest: %v", err)
	}
	bootstrap := outputData(t, output, "generated/go/bootstrap/bootstrap_gen.go")
	for _, required := range []string{
		strconv.Quote(string(provenance.CanonicalJSON())),
		strconv.Quote(provenance.Digest()),
	} {
		if !bytes.Contains(bootstrap, []byte(required)) {
			t.Fatalf("generated bootstrap disagrees with manifest provenance %q:\n%s", required, bootstrap)
		}
	}
}

func applicationModelDigest(t testing.TB, options applicationgen.ApplicationModelOptions) (string, error) {
	t.Helper()
	projection, err := applicationgen.ProtobufProjection(options.HTTPTransports, options.Resolution)
	if err != nil {
		return "", err
	}
	wireMap, err := protobufwiremap.Build(projection, emptyInterfaceWireProjection(t, projection), nil, false, "")
	if err != nil {
		return "", err
	}
	options.ProtobufWireMap = wireMap
	return applicationgen.ApplicationModelDigest(options)
}

func testComposition() applicationmeta.Composition {
	composition, err := applicationmeta.Compose(nil, applicationmeta.Manifest{}, func(constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	})
	if err != nil {
		panic(err)
	}
	return composition
}

func dependencyComposition(t testing.TB) applicationmeta.Composition {
	t.Helper()
	schema := applicationConfigurationSchema(t)
	dependency, err := applicationmeta.Parse([]byte("http: {expose: {remove: [diagnostics.internal/v1]}}\nconfig: {example.com/acme/business.New: {legacy: null, password: {env: PRIVATE_APPLICATION_TOKEN}}}\n"))
	if err != nil {
		t.Fatalf("applicationmeta.Parse dependency: %v", err)
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependency,
	}}, applicationmeta.Manifest{}, func(constructor constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return schema, constructor.String() == businessModulePath+".New"
	})
	if err != nil {
		t.Fatalf("applicationmeta.Compose: %v", err)
	}
	return composition
}

func applicationConfigurationSchema(t testing.TB) implementationinventory.Configuration {
	t.Helper()
	compiled := types.NewPackage(businessModulePath, "business")
	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secret := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)
	secretPackage.MarkComplete()

	configName := types.NewTypeName(token.NoPos, compiled, "Config", nil)
	config := types.NewNamed(configName, types.NewStruct([]*types.Var{
		types.NewVar(token.NoPos, compiled, "Legacy", types.Typ[types.String]),
		types.NewVar(token.NoPos, compiled, "Password", secret),
	}, []string{`yaml:"legacy"`, `yaml:"password"`}), nil)
	compiled.Scope().Insert(configName)
	parameters := types.NewTuple(types.NewVar(token.NoPos, compiled, "configuration", config))
	constructor := types.NewFunc(token.NoPos, compiled, "New", types.NewSignatureType(nil, nil, nil, parameters, nil, false))
	compiled.Scope().Insert(constructor)

	schema, exists, err := implementationinventory.CompileConfiguration(compiled, constructor)
	if err != nil || !exists {
		t.Fatalf("CompileConfiguration = %#v, %t, %v", schema, exists, err)
	}
	return schema
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
		Input:                   generationresolution.Input{Activations: catalog},
		ConfigurationProvenance: defaultConfigurationProvenance(t, testComposition()),
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
		ConfigurationProvenance: defaultConfigurationProvenance(t, testComposition()),
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
	return resolvedApplicationWithComposition(t, applicationYAML, testComposition())
}

func resolvedHTTPOnlyApplication(t testing.TB) generationresolution.ExtensionResult {
	return resolvedApplicationWithEmailExposure(
		t,
		"",
		`id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`,
		defaultConfigurationProvenance(t, testComposition()),
		false,
	)
}

func resolvedApplicationWithComposition(t testing.TB, applicationYAML string, composition applicationmeta.Composition) generationresolution.ExtensionResult {
	return resolvedApplicationWithConfigurationProvenance(t, applicationYAML, defaultConfigurationProvenance(t, composition))
}

func resolvedApplicationWithConfigurationProvenance(t testing.TB, applicationYAML string, configurationProvenance *generation.ConfigurationProvenanceInput) generationresolution.ExtensionResult {
	return resolvedApplicationWithEmail(t, applicationYAML, `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`, configurationProvenance)
}

func resolvedApplicationWithEmail(t testing.TB, applicationYAML, emailSource string, configurationProvenance *generation.ConfigurationProvenanceInput) generationresolution.ExtensionResult {
	return resolvedApplicationWithEmailExposure(
		t,
		applicationYAML,
		emailSource,
		configurationProvenance,
		true,
	)
}

func resolvedApplicationWithEmailExposure(t testing.TB, applicationYAML, emailSource string, configurationProvenance *generation.ConfigurationProvenanceInput, javaScript bool) generationresolution.ExtensionResult {
	t.Helper()
	email := normalizedContract(t, emailSource)
	health := normalizedContract(t, `id: kernel.health/v1
description: Reports intrinsic Kernel liveness.
request: {}
response:
  status:
    type: string
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
	applicationAliases := make([]generationresolution.ApplicationAlias, len(aliases))
	for index, alias := range aliases {
		applicationAliases[index] = generationresolution.ApplicationAlias{
			Alias: alias,
			Sources: []providerresolution.RequirementSource{{
				Kind:       providerresolution.RequirementAliasTarget,
				Reference:  alias.Source() + " target",
				ModulePath: businessModulePath,
				Path:       "plystra.yaml",
				Line:       1,
				Column:     1,
				Alias:      alias.ID().String(),
			}},
		}
	}
	catalog, err := generationactivation.New(nil)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	resolution, err := generationresolution.ResolveExtensions(t.Context(), generationresolution.ExtensionInput{
		ConfigurationProvenance: configurationProvenance,
		Input: generationresolution.Input{
			Requirements: []providerresolution.Requirement{
				{Contract: email, Source: applicationGenerationRequirementSource("application client email.send/v1")},
				{Contract: health, Source: applicationGenerationRequirementSource("application health")},
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
			{ContractJSON: email, Sources: []string{"business-module/business/capabilities/email.send/v1/capability.yaml"}, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: javaScript}},
			{ContractJSON: health, Sources: []string{"github.com/plystra/kernel/capability/catalog kernel.health/v1"}, Intrinsic: true, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: javaScript}},
		},
		ApplicationAliases: applicationAliases,
	})
	if err != nil {
		t.Fatalf("ResolveExtensions: %v", err)
	}
	return resolution
}

func applicationGenerationRequirementSource(reference string) providerresolution.RequirementSource {
	return providerresolution.RequirementSource{
		Kind:       providerresolution.RequirementDeclaration,
		Reference:  reference,
		ModulePath: businessModulePath,
		Path:       "plystra.yaml",
		Line:       1,
		Column:     1,
	}
}

func normalizedContract(t testing.TB, source string) []byte {
	t.Helper()
	if !strings.Contains(source, "\nsemantics:") {
		source += querySemanticsYAML
	}
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}

const querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

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

func sameTransportOutput(left, right generatedfiles.Output) bool {
	leftFiles := make(map[string][]byte)
	for _, file := range left.Files() {
		if transportPath(file.Path()) {
			leftFiles[file.Path()] = file.Data()
		}
	}
	rightFiles := make(map[string][]byte)
	for _, file := range right.Files() {
		if transportPath(file.Path()) {
			rightFiles[file.Path()] = file.Data()
		}
	}
	if len(leftFiles) != len(rightFiles) {
		return false
	}
	for filePath, leftData := range leftFiles {
		if !bytes.Equal(leftData, rightFiles[filePath]) {
			return false
		}
	}
	return true
}

func transportPath(filePath string) bool {
	return strings.HasPrefix(filePath, "generated/go/adapters/connect/") ||
		strings.HasPrefix(filePath, "generated/go/adapters/http/") ||
		strings.HasPrefix(filePath, "generated/sdk/javascript/") ||
		strings.HasPrefix(filePath, "generated/docs/")
}

func emptyInterfaceWireProjection(t testing.TB, legacy protobufmodel.Model) protobufmodel.InterfaceModel {
	t.Helper()
	model, err := protobufmodel.BuildInterfaces(legacy.Enabled(), nil)
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
	}
	return model
}
