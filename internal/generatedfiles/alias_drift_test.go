package generatedfiles_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/httpgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
)

const (
	aliasDriftModulePath = "example.com/acme/application"
	aliasDriftPackage    = "@acme/application-sdk"
	aliasDriftID         = "compat.status/v1"
	aliasClientPath      = "generated/go/clients/compat/status/v1/client_gen.go"
	aliasHTTPPath        = "generated/go/adapters/http/compat/status/v1/handler_gen.go"
	aliasJavaScriptPath  = "generated/sdk/javascript/src/operations/compat/status/v1.ts"
)

var fullAliasExposure = generation.Exposure{Go: true, HTTP: true, JavaScript: true}

func TestAliasGeneratedOutputAppearanceAndDisappearance(t *testing.T) {
	t.Parallel()

	withoutAlias := renderAliasOutput(t, aliasRenderOptions{})
	withAlias := renderAliasOutput(t, aliasRenderOptions{aliases: []aliasSpec{{
		id:         aliasDriftID,
		target:     "kernel.health/v1",
		exposure:   fullAliasExposure,
		deprecated: "Use health.status/v1 instead.",
	}}})
	root := t.TempDir()
	writeOutput(t, root, withoutAlias)

	appearance, err := generatedfiles.Check(root, withAlias)
	if err != nil {
		t.Fatalf("Check appearance: %v", err)
	}
	assertPaths(t, "appearance missing", appearance.Missing(), []string{
		aliasHTTPPath,
		aliasClientPath,
		aliasJavaScriptPath,
	})
	assertContainsPaths(t, "appearance changed", appearance.Changed(), []string{
		generatedfiles.ManifestPath,
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/manifest.json",
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/src/index.ts",
	})
	if len(appearance.Unexpected()) != 0 || len(appearance.Obsolete()) != 0 {
		t.Fatalf("appearance extra drift = %#v", appearance.Changes())
	}
	if report, err := generatedfiles.Install(root, withAlias, func(string) error { return nil }); err != nil || !report.Clean() {
		t.Fatalf("Install appearance = %#v, %v", report.Changes(), err)
	}
	for _, filePath := range []string{aliasClientPath, aliasHTTPPath, aliasJavaScriptPath} {
		if _, exists := outputFile(withAlias, filePath); !exists {
			t.Fatalf("rendered Alias output omits %s", filePath)
		}
		assertFileBytes(t, root, filePath, mustOutputFile(t, withAlias, filePath))
	}

	disappearance, err := generatedfiles.Check(root, withoutAlias)
	if err != nil {
		t.Fatalf("Check disappearance: %v", err)
	}
	assertPaths(t, "disappearance obsolete", disappearance.Obsolete(), []string{
		aliasHTTPPath,
		aliasClientPath,
		aliasJavaScriptPath,
	})
	assertContainsPaths(t, "disappearance changed", disappearance.Changed(), []string{
		generatedfiles.ManifestPath,
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/manifest.json",
		"generated/sdk/javascript/README.md",
		"generated/sdk/javascript/src/index.ts",
	})
	if len(disappearance.Missing()) != 0 || len(disappearance.Unexpected()) != 0 {
		t.Fatalf("disappearance extra drift = %#v", disappearance.Changes())
	}
	if report, err := generatedfiles.Install(root, withoutAlias, func(string) error { return nil }); err != nil || !report.Clean() {
		t.Fatalf("Install disappearance = %#v, %v", report.Changes(), err)
	}
	for _, filePath := range []string{aliasClientPath, aliasHTTPPath, aliasJavaScriptPath} {
		assertMissing(t, root, filePath)
	}
	if report, err := generatedfiles.Check(root, withoutAlias); err != nil || !report.Clean() {
		t.Fatalf("Check after cleanup = %#v, %v", report.Changes(), err)
	}
	assertNoTransaction(t, root)
}

func TestAliasRetargetDeprecationExposureAndTargetContractDrift(t *testing.T) {
	t.Parallel()

	baselineOptions := aliasRenderOptions{aliases: []aliasSpec{{
		id:       aliasDriftID,
		target:   "kernel.health/v1",
		exposure: fullAliasExposure,
	}}}
	baseline := renderAliasOutput(t, baselineOptions)
	tests := []struct {
		name            string
		options         aliasRenderOptions
		changed         []string
		obsolete        []string
		forbiddenChange []string
	}{
		{
			name: "retarget",
			options: aliasRenderOptions{aliases: []aliasSpec{{
				id:       aliasDriftID,
				target:   "kernel.info/v1",
				exposure: fullAliasExposure,
			}}},
			changed: []string{
				aliasClientPath,
				aliasHTTPPath,
				aliasJavaScriptPath,
				"generated/docs/api.md",
				"generated/docs/openapi.json",
			},
		},
		{
			name: "deprecation",
			options: aliasRenderOptions{aliases: []aliasSpec{{
				id:         aliasDriftID,
				target:     "kernel.health/v1",
				exposure:   fullAliasExposure,
				deprecated: "Use health.status/v1 instead.",
			}}},
			changed: []string{
				aliasClientPath,
				aliasHTTPPath,
				aliasJavaScriptPath,
				"generated/docs/api.md",
				"generated/docs/openapi.json",
			},
		},
		{
			name: "exposure narrowing",
			options: aliasRenderOptions{aliases: []aliasSpec{{
				id:       aliasDriftID,
				target:   "kernel.health/v1",
				exposure: generation.Exposure{HTTP: true},
			}}},
			changed:         []string{"generated/docs/api.md", "generated/sdk/javascript/src/index.ts"},
			obsolete:        []string{aliasClientPath, aliasJavaScriptPath},
			forbiddenChange: []string{aliasHTTPPath},
		},
		{
			name: "target contract digest",
			options: aliasRenderOptions{
				healthSchema: `{"id":"kernel.health/v1","request":{},"response":{"healthy":{"type":"boolean","required":true},"status":{"type":"string","required":true}},"errors":[]}`,
				aliases:      baselineOptions.aliases,
			},
			changed: []string{
				aliasJavaScriptPath,
				"generated/docs/api.md",
				"generated/docs/openapi.json",
			},
			forbiddenChange: []string{
				aliasClientPath,
				aliasHTTPPath,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeOutput(t, root, baseline)
			desired := renderAliasOutput(t, test.options)
			report, err := generatedfiles.Check(root, desired)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			assertContainsPaths(t, "common changed", report.Changed(), []string{
				generatedfiles.ManifestPath,
				"generated/manifest.json",
				"generated/sdk/javascript/README.md",
			})
			assertContainsPaths(t, "surface changed", report.Changed(), test.changed)
			for _, filePath := range test.forbiddenChange {
				if slices.Contains(report.Changed(), filePath) {
					t.Fatalf("%s unexpectedly changed unchanged forwarding surface %s: %v", test.name, filePath, report.Changed())
				}
			}
			assertPaths(t, "obsolete", report.Obsolete(), test.obsolete)
			if len(report.Missing()) != 0 || len(report.Unexpected()) != 0 {
				t.Fatalf("%s extra drift = %#v", test.name, report.Changes())
			}
			if report.Clean() {
				t.Fatalf("%s Alias drift was not detected", test.name)
			}
		})
	}
}

type aliasRenderOptions struct {
	healthSchema string
	aliases      []aliasSpec
}

type aliasSpec struct {
	id         string
	target     string
	exposure   generation.Exposure
	deprecated string
}

type noAliasExtensionOutput struct{}

func (noAliasExtensionOutput) PluginID() string { return "" }
func (noAliasExtensionOutput) Output() generation.NormalizedOutput {
	return generation.NormalizedOutput{}
}

func renderAliasOutput(t testing.TB, options aliasRenderOptions) generatedfiles.Output {
	t.Helper()
	healthSchema := options.healthSchema
	if healthSchema == "" {
		healthSchema = `{"id":"kernel.health/v1","request":{},"response":{"healthy":{"type":"boolean","required":true}},"errors":[]}`
	}
	targetSchemas := []string{
		healthSchema,
		`{"id":"kernel.info/v1","request":{},"response":{"version":{"type":"string","required":true}},"errors":[]}`,
	}
	capabilities := make([]generation.CapabilityInput, len(targetSchemas))
	requirements := make([]string, len(targetSchemas))
	for index, schema := range targetSchemas {
		canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
		if err != nil {
			t.Fatalf("NormalizeSchema: %v", err)
		}
		var identity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(canonical, &identity); err != nil {
			t.Fatalf("Unmarshal canonical identity: %v", err)
		}
		capabilities[index] = generation.CapabilityInput{
			ContractJSON: canonical,
			Intrinsic:    true,
			Exposure:     fullAliasExposure,
		}
		requirements[index] = identity.ID
	}
	aliases := make([]generation.CapabilityAliasInput, len(options.aliases))
	for index, alias := range options.aliases {
		aliases[index] = generation.CapabilityAliasInput{
			ID:         alias.id,
			Target:     alias.target,
			Exposure:   alias.exposure,
			Deprecated: alias.deprecated,
			Sources: []generation.AliasSourceInput{{
				Kind: generation.AliasSourceApplication,
				ID:   "application",
			}},
		}
	}
	context, err := generation.NewContext(generation.Input{
		Capabilities:      capabilities,
		Requirements:      requirements,
		CapabilityAliases: aliases,
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	resolved, err := aliasresolution.Resolve[noAliasExtensionOutput](context, nil)
	if err != nil {
		t.Fatalf("Resolve Aliases: %v", err)
	}

	capabilityViews := context.Capabilities()
	sdkTargets := make([]sdkmodel.CanonicalTargetView, len(capabilityViews))
	for index := range capabilityViews {
		sdkTargets[index] = capabilityViews[index]
	}
	resolvedAliases := resolved.Aliases()
	sdkAliases := make([]sdkmodel.AliasView, len(resolvedAliases))
	docAliases := make([]apidocgen.AliasView, len(resolvedAliases))
	for index := range resolvedAliases {
		sdkAliases[index] = resolvedAliases[index]
		docAliases[index] = resolvedAliases[index]
	}
	model, err := sdkmodel.Build(sdkTargets, sdkAliases)
	if err != nil {
		t.Fatalf("Build SDK model: %v", err)
	}
	configurationProvenance := aliasConfigurationProvenance(t)

	var files []generatedfiles.File
	add := func(filePath string, data []byte) {
		files = append(files, managedFile(t, filePath, data))
	}
	manifestJSON := append(resolved.CanonicalJSON(), '\n')
	add("generated/manifest.json", manifestJSON)
	for _, target := range capabilityViews {
		contract, err := contractgen.Render(target.ContractJSON())
		if err != nil {
			t.Fatalf("Render contract %s: %v", target.ID(), err)
		}
		add(contract.Path(), contract.Data())
		client, err := clientgen.Render(aliasDriftModulePath, target.ContractJSON())
		if err != nil {
			t.Fatalf("Render client %s: %v", target.ID(), err)
		}
		add(client.Path(), client.Data())
		handler, err := httpgen.Render(aliasDriftModulePath, target, configurationProvenance)
		if err != nil {
			t.Fatalf("Render HTTP %s: %v", target.ID(), err)
		}
		add(handler.Path(), handler.Data())
	}
	for _, alias := range resolvedAliases {
		target, exists := context.Capability(alias.Target())
		if !exists {
			t.Fatalf("Alias %s target %s disappeared", alias.ID(), alias.Target())
		}
		if alias.Exposure().Go {
			client, err := clientgen.RenderAlias(aliasDriftModulePath, alias, target)
			if err != nil {
				t.Fatalf("Render Alias client %s: %v", alias.ID(), err)
			}
			add(client.Path(), client.Data())
		}
		if alias.Exposure().HTTP {
			handler, err := httpgen.RenderAlias(aliasDriftModulePath, alias, target, configurationProvenance)
			if err != nil {
				t.Fatalf("Render Alias HTTP %s: %v", alias.ID(), err)
			}
			add(handler.Path(), handler.Data())
		}
	}
	protobufTargets := make([]protobufmodel.CanonicalTargetView, len(capabilityViews))
	for index, target := range capabilityViews {
		protobufTargets[index] = sourcedAliasCapabilityView{CapabilityView: target}
	}
	protobufAliases := make([]protobufmodel.AliasView, len(resolvedAliases))
	for index, alias := range resolvedAliases {
		protobufAliases[index] = alias
	}
	projection, err := protobufmodel.Build(true, protobufTargets, protobufAliases)
	if err != nil {
		t.Fatalf("Build Protobuf projection: %v", err)
	}
	wireMap, err := protobufwiremap.Build(projection, nil, false, "")
	if err != nil {
		t.Fatalf("Build Protobuf wire map: %v", err)
	}
	descriptors, err := protobufdescriptor.Build(projection, wireMap)
	if err != nil {
		t.Fatalf("Build Protobuf descriptors: %v", err)
	}
	javaScript, err := javascriptgen.Render(javascriptgen.Options{
		PackageName:             aliasDriftPackage,
		ConfigurationProvenance: configurationProvenance,
		Transport: javascriptgen.TransportOptions{
			Projection:    projection,
			WireMap:       wireMap,
			DescriptorSet: descriptors.DescriptorSet(),
		},
	}, model)
	if err != nil {
		t.Fatalf("Render JavaScript: %v", err)
	}
	for _, file := range javaScript {
		add(file.Path(), file.Data())
	}
	docs, err := apidocgen.Render(model, docAliases, configurationProvenance)
	if err != nil {
		t.Fatalf("Render API docs: %v", err)
	}
	for _, file := range docs {
		add(file.Path(), file.Data())
	}
	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	return output
}

func aliasConfigurationProvenance(t testing.TB) transportprovenance.Provenance {
	t.Helper()
	rootDigest := "sha256:" + strings.Repeat("1", 64)
	provenance, err := transportprovenance.New(transportprovenance.Input{
		Mode:                        generation.ConfigurationModeDefault,
		RootPath:                    "plystra.yaml",
		RootDigest:                  rootDigest,
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              rootDigest,
		DependencyCompositionDigest: "sha256:" + strings.Repeat("2", 64),
		ApplicationModelDigest:      "sha256:" + strings.Repeat("3", 64),
	})
	if err != nil {
		t.Fatalf("transportprovenance.New: %v", err)
	}
	return provenance
}

type sourcedAliasCapabilityView struct {
	generation.CapabilityView
}

func (v sourcedAliasCapabilityView) Sources() []string {
	return []string{"test@local/" + v.ID().String() + "/capability.yaml"}
}

func assertContainsPaths(t testing.TB, name string, got, want []string) {
	t.Helper()
	for _, filePath := range want {
		if !slices.Contains(got, filePath) {
			t.Fatalf("%s = %v, want path %s", name, got, filePath)
		}
	}
}

func outputFile(output generatedfiles.Output, filePath string) ([]byte, bool) {
	for _, file := range output.Files() {
		if file.Path() == filePath {
			return file.Data(), true
		}
	}
	return nil, false
}

func mustOutputFile(t testing.TB, output generatedfiles.Output, filePath string) []byte {
	t.Helper()
	data, exists := outputFile(output, filePath)
	if !exists {
		t.Fatalf("output file %s is absent", filePath)
	}
	return data
}
