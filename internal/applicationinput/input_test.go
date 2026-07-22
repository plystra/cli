package applicationinput_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestBuildLoadsDeterministicFilesystemResolutionInput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providersRoot := filepath.Join(root, "providers")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, providersRoot, "example.com/providers")
	writeFile(t, filepath.Join(providersRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, appRoot, "local", "id: example.local\nrequires: [order.create/v1]\n")
	writePlugin(t, providersRoot, "business", "id: example.business\nprovides: [order.create/v1]\nrequires: [audit.write/v1]\ngeneration: {api: v1, package: ./generation, activations: [{namespace: authz, capability: order.create/v1}]}\n")
	if err := os.MkdirAll(filepath.Join(providersRoot, "business", "generation"), 0o755); err != nil {
		t.Fatalf("MkdirAll(generation): %v", err)
	}
	writePlugin(t, providersRoot, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	orderSource := "id: order.create/v1\nrequest: {}\nresponse: {}\nerrors: []\nextensions:\n  authz: {permission: order.create}\n"
	auditSource := "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n"
	writeCapability(t, providersRoot, "business", "order.create/v1", orderSource)
	writeCapability(t, providersRoot, "audit", "audit.write/v1", auditSource)
	inventory := configureInventory(t, appRoot, dependency{path: "example.com/providers", version: "v1.2.0", root: providersRoot})
	manifest := parseManifest(t, `http:
  expose: [order.create/v1]
capabilities:
  require: [kernel.info/v1]
  use:
    order.create/v1: example.business
  aliases:
    orders.submit/v1: order.create/v1
`)
	environment := []string{"GOENV=off", "GOWORK=off"}
	provenance := &generation.ConfigurationProvenanceInput{
		Mode:                        generation.ConfigurationModeDefault,
		RootPath:                    "plystra.yaml",
		RootDigest:                  "sha256:" + strings.Repeat("1", 64),
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              "sha256:" + strings.Repeat("1", 64),
		DependencyCompositionDigest: "sha256:" + strings.Repeat("2", 64),
	}
	input, err := applicationinput.Build(manifest, inventory, applicationInputSourceContext(dependency{path: "example.com/providers", version: "v1.2.0"}), provenance, generationexec.BuildOptions{BuildEnvironment: environment})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantProvenance := *provenance
	provenance.RootDigest = "changed"
	if input.ConfigurationProvenance == nil || input.ConfigurationProvenance == provenance || *input.ConfigurationProvenance != wantProvenance {
		t.Fatalf("ConfigurationProvenance = %#v", input.ConfigurationProvenance)
	}
	if got := capabilityInputIDs(t, input.Capabilities); !reflect.DeepEqual(got, []string{"audit.write/v1", "kernel.health/v1", "kernel.info/v1", "order.create/v1"}) {
		t.Fatalf("Capabilities = %v", got)
	}
	for _, capability := range input.Capabilities {
		if !capability.Exposure.Go || capability.Exposure.HTTP || capability.Exposure.JavaScript {
			t.Fatalf("base exposure = %#v", capability.Exposure)
		}
		if len(capability.Sources) == 0 {
			t.Fatalf("Capability contract has no provenance: %#v", capability)
		}
	}
	if got := candidateStrings(t, input.Candidates); !reflect.DeepEqual(got, []string{
		"example.audit:audit.write/v1:example.com/providers@v1.2.0/audit/capabilities/audit.write/v1/capability.yaml",
		"example.business:order.create/v1:example.com/providers@v1.2.0/business/capabilities/order.create/v1/capability.yaml",
	}) {
		t.Fatalf("Candidates = %v", got)
	}
	if len(input.Requirements) != 1 || input.Requirements[0].Capability != "kernel.info/v1" || len(input.Requirements[0].Contract) == 0 || input.Requirements[0].Source.String() != `plystra.yaml capabilities.require["kernel.info/v1"]` || input.Requirements[0].Source.Kind != providerresolution.RequirementDeclaration || input.Requirements[0].Source.ModulePath != "example.com/app" || input.Requirements[0].Source.Path != "plystra.yaml" {
		t.Fatalf("Requirements = %#v", input.Requirements)
	}
	if len(input.Choices) != 1 || input.Choices[0].Capability != "order.create/v1" || input.Choices[0].PluginID != "example.business" || len(input.Choices[0].Sources) != 1 || input.Choices[0].Sources[0].Kind != providerresolution.ChoiceSourceCurrentProject || input.Choices[0].Sources[0].ModulePath != "example.com/app" || input.Choices[0].Sources[0].Path != "plystra.yaml" {
		t.Fatalf("Choices = %#v", input.Choices)
	}
	if got := resolutionPluginStrings(input.Plugins); !reflect.DeepEqual(got, []string{
		"example.audit:example.com/providers@v1.2.0:audit:false:audit.write/v1:",
		"example.business:example.com/providers@v1.2.0:business:false:order.create/v1:audit.write/v1",
		"example.local:example.com/app@local:local:true::order.create/v1",
	}) {
		t.Fatalf("Plugins = %v", got)
	}
	for _, plugin := range input.Plugins {
		if string(plugin.Context.BuildMetadataJSON) != "{}" {
			t.Fatalf("plugin %s build metadata = %q", plugin.Context.ID, plugin.Context.BuildMetadataJSON)
		}
	}
	association, exists := input.Activations.Association("authz")
	if !exists || association.Capability().String() != "order.create/v1" || len(association.Extensions()) != 1 || association.Extensions()[0].PluginID() != "example.business" {
		t.Fatalf("authz association = %#v, %t", association, exists)
	}
	if len(input.ApplicationHTTPExposures) != 1 || input.ApplicationHTTPExposures[0].Exposure.ID().String() != "order.create/v1" || len(input.ApplicationHTTPExposures[0].Sources) != 1 || input.ApplicationHTTPExposures[0].Sources[0].Kind != providerresolution.RequirementExposure || len(input.ApplicationAliases) != 1 || input.ApplicationAliases[0].Alias.ID().String() != "orders.submit/v1" || len(input.ApplicationAliases[0].Sources) != 1 || input.ApplicationAliases[0].Sources[0].Kind != providerresolution.RequirementAliasTarget || input.ApplicationAliases[0].Sources[0].Alias != "orders.submit/v1" {
		t.Fatalf("application declarations = HTTP %#v, aliases %#v", input.ApplicationHTTPExposures, input.ApplicationAliases)
	}
	environment[0] = "changed"
	if input.BuildOptions.BuildEnvironment[0] != "GOENV=off" {
		t.Fatal("Build exposed caller BuildEnvironment storage")
	}
	wantOrder, err := capabilitymeta.NormalizeSchema([]byte(withQuerySemantics(orderSource)))
	if err != nil {
		t.Fatalf("NormalizeSchema(order): %v", err)
	}
	for _, candidate := range input.Candidates {
		if candidate.PluginID == "example.business" && !bytes.Equal(candidate.Contract, wantOrder) {
			t.Fatalf("business contract = %s, want %s", candidate.Contract, wantOrder)
		}
	}
}

func TestBuildPreservesEffectiveDependencySourcesAndSparseOverlayLocations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	inventory := configureInventory(t, root)
	manifest, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`http:
  expose:
    add: [kernel.health/v1]
capabilities:
  require:
    add: [kernel.info/v1]
  use:
    email.send/v1: example.email
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource: %v", err)
	}
	sourceContext := applicationinput.SourceContext{
		CurrentModulePath: "example.com/app",
		Dependencies: []applicationinput.DependencySource{
			{ModulePath: "example.com/a", Version: "v1.0.0"},
			{ModulePath: "example.com/b", Version: "v2.0.0"},
		},
		DependencyProvenance: []applicationinput.DependencyProvenance{{
			Path: "capabilities.require[\"kernel.info/v1\"]",
			Sources: []string{
				`example.com/a@v1.0.0/plystra.yaml capabilities.require["kernel.info/v1"]`,
				`example.com/b@v2.0.0/plystra.yaml capabilities.require["kernel.info/v1"]`,
			},
		}},
		CurrentProjectPaths: []string{`capabilities.require["kernel.info/v1"]`, `capabilities.use["email.send/v1"]`},
	}
	input, err := applicationinput.Build(manifest, inventory, sourceContext, nil, generationexec.BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(input.Requirements) != 3 {
		t.Fatalf("Requirements = %#v", input.Requirements)
	}
	localSource := input.Requirements[0].Source
	if localSource.Kind != providerresolution.RequirementDeclaration || localSource.ModulePath != "example.com/app" || localSource.Path != "plystra.production.yaml" || localSource.String() != `plystra.production.yaml capabilities.require.add["kernel.info/v1"]` {
		t.Fatalf("local overlay requirement source = %#v", localSource)
	}
	for index, modulePath := range []string{"example.com/a", "example.com/b"} {
		index++
		source := input.Requirements[index].Source
		if source.Kind != providerresolution.RequirementDeclaration || source.ModulePath != modulePath || source.Path != "plystra.yaml" || source.Line != 1 || source.Column != 1 {
			t.Fatalf("Requirements[%d].Source = %#v", index, source)
		}
	}
	if len(input.ApplicationHTTPExposures) != 1 || len(input.ApplicationHTTPExposures[0].Sources) != 1 {
		t.Fatalf("ApplicationHTTPExposures = %#v", input.ApplicationHTTPExposures)
	}
	exposureSource := input.ApplicationHTTPExposures[0].Sources[0]
	if exposureSource.Kind != providerresolution.RequirementExposure || exposureSource.ModulePath != "example.com/app" || exposureSource.Path != "plystra.production.yaml" || exposureSource.String() != `plystra.production.yaml http.expose.add["kernel.health/v1"]` {
		t.Fatalf("sparse overlay exposure source = %#v", exposureSource)
	}
	if len(input.Choices) != 1 || len(input.Choices[0].Sources) != 1 || input.Choices[0].Sources[0].Kind != providerresolution.ChoiceSourceCurrentProject || input.Choices[0].Sources[0].ModulePath != "example.com/app" || input.Choices[0].Sources[0].Path != "plystra.production.yaml" {
		t.Fatalf("overlay Provider choice sources = %#v", input.Choices)
	}

	invalidContext := sourceContext
	invalidContext.DependencyProvenance = []applicationinput.DependencyProvenance{{
		Path:    `capabilities.require["kernel.info/v1"]`,
		Sources: []string{`example.com/unlisted@v1.0.0/plystra.yaml capabilities.require["kernel.info/v1"]`},
	}}
	invalid, err := applicationinput.Build(manifest, inventory, invalidContext, nil, generationexec.BuildOptions{})
	if !errors.Is(err, applicationinput.ErrBuild) || !strings.Contains(err.Error(), "does not identify a discovered dependency Project") || len(invalid.Requirements) != 0 {
		t.Fatalf("Build(unlisted dependency source) = %#v, %v", invalid, err)
	}
}

func TestBuildPreservesEveryCompatibleInheritedProviderChoiceSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	inventory := configureInventory(t, root)
	manifest, err := applicationmeta.ParseSource("example.com/a@v1.0.0/plystra.yaml", []byte(`capabilities:
  use:
    email.send/v1: example.email
`))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	context := applicationinput.SourceContext{
		CurrentModulePath: "example.com/app",
		Dependencies: []applicationinput.DependencySource{
			{ModulePath: "example.com/a", Version: "v1.0.0"},
			{ModulePath: "example.com/b", Version: ""},
		},
		DependencyProvenance: []applicationinput.DependencyProvenance{{
			Path: `capabilities.use["email.send/v1"]`,
			Sources: []string{
				`example.com/b@workspace/plystra.yaml capabilities.use["email.send/v1"]`,
				`example.com/a@v1.0.0/plystra.yaml capabilities.use["email.send/v1"]`,
			},
		}},
	}
	input, err := applicationinput.Build(manifest, inventory, context, nil, generationexec.BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(input.Choices) != 1 || len(input.Choices[0].Sources) != 2 {
		t.Fatalf("Choices = %#v", input.Choices)
	}
	sources := input.Choices[0].Sources
	if sources[0].Kind != providerresolution.ChoiceSourceDependencyProject || sources[0].ModulePath != "example.com/a" || sources[0].Path != "plystra.yaml" || sources[1].Kind != providerresolution.ChoiceSourceDependencyProject || sources[1].ModulePath != "example.com/b" || sources[1].Path != "plystra.yaml" {
		t.Fatalf("inherited Provider choice sources = %#v", sources)
	}

	invalidContext := context
	invalidContext.DependencyProvenance = []applicationinput.DependencyProvenance{{
		Path:    `capabilities.use["email.send/v1"]`,
		Sources: []string{`example.com/unlisted@v1.0.0/plystra.yaml capabilities.use["email.send/v1"]`},
	}}
	invalid, err := applicationinput.Build(manifest, inventory, invalidContext, nil, generationexec.BuildOptions{})
	if !errors.Is(err, applicationinput.ErrBuild) || !strings.Contains(err.Error(), "does not identify a discovered dependency Project") || len(invalid.Choices) != 0 {
		t.Fatalf("Build(unlisted Provider source) = %#v, %v", invalid, err)
	}
}

func TestBuildAllowsEmptyPluginApplicationWithIntrinsicCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	inventory := configureInventory(t, root)
	input, err := applicationinput.Build(parseManifest(t, "{}\n"), inventory, applicationInputSourceContext(), nil, generationexec.BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(input.Plugins) != 0 || len(input.Candidates) != 0 || len(input.Requirements) != 0 || !reflect.DeepEqual(capabilityInputIDs(t, input.Capabilities), []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("empty input = plugins %#v, candidates %#v, requirements %#v, capabilities %v", input.Plugins, input.Candidates, input.Requirements, capabilityInputIDs(t, input.Capabilities))
	}
}

func TestBuildRejectsMissingAndMismatchedCapabilitySources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		write     bool
		content   string
		wantError error
	}{
		{name: "missing", wantError: os.ErrNotExist},
		{name: "mismatch", write: true, content: "id: email.receive/v1\nrequest: {}\nresponse: {}\nerrors: []\n", wantError: capabilitysource.ErrIdentityMismatch},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeModule(t, root, "example.com/app")
			writePlugin(t, root, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
			if test.write {
				writeCapability(t, root, "smtp", "email.send/v1", test.content)
			}
			inventory := configureInventory(t, root)
			input, err := applicationinput.Build(parseManifest(t, "{}\n"), inventory, applicationInputSourceContext(), nil, generationexec.BuildOptions{})
			if !errors.Is(err, applicationinput.ErrBuild) || !errors.Is(err, test.wantError) || len(input.Capabilities) != 0 {
				t.Fatalf("Build = %#v, %v; want ErrBuild and %v", input, err, test.wantError)
			}
		})
	}
}

func TestBuildRejectsConflictingVisibleProviderContracts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writePlugin(t, root, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writePlugin(t, root, "mock", "id: example.mock\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {to: {type: string}}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "mock", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	inventory := configureInventory(t, root)
	input, err := applicationinput.Build(parseManifest(t, "{}\n"), inventory, applicationInputSourceContext(), nil, generationexec.BuildOptions{})
	if !errors.Is(err, applicationinput.ErrBuild) || !errors.Is(err, applicationinput.ErrContractConflict) || len(input.Capabilities) != 0 {
		t.Fatalf("Build = %#v, %v; want contract conflict", input, err)
	}
	var conflict *applicationinput.ContractConflictError
	if !errors.As(err, &conflict) || conflict.ID().String() != "email.send/v1" || len(conflict.Variants()) != 2 {
		t.Fatalf("Build error = %v, want typed email.send/v1 conflict", err)
	}
	var sources []string
	for _, variant := range conflict.Variants() {
		if len(variant.Digest()) != len("sha256:")+64 || len(variant.Sources()) != 1 {
			t.Fatalf("variant = digest %q, sources %v", variant.Digest(), variant.Sources())
		}
		sources = append(sources, variant.Sources()[0])
	}
	sort.Strings(sources)
	wantSources := []string{
		"example.com/app@local/mock/capabilities/email.send/v1/capability.yaml",
		"example.com/app@local/smtp/capabilities/email.send/v1/capability.yaml",
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("conflict sources = %v, want %v", sources, wantSources)
	}
	variants := conflict.Variants()
	returned := variants[0].Sources()
	returned[0] = "changed"
	if reflect.DeepEqual(returned, conflict.Variants()[0].Sources()) {
		t.Fatal("ContractConflictError exposed mutable source storage")
	}
}

func TestBuildMergesIdenticalContractsFromSeveralProviders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writePlugin(t, root, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writePlugin(t, root, "mock", "id: example.mock\nprovides: [email.send/v1]\n")
	contract := "id: email.send/v1\nrequest: {to: {type: string}}\nresponse: {}\nerrors: []\n"
	writeCapability(t, root, "smtp", "email.send/v1", contract)
	writeCapability(t, root, "mock", "email.send/v1", "errors: []\nresponse: {}\nrequest: {to: {type: string}}\nid: email.send/v1\n")
	inventory := configureInventory(t, root)
	input, err := applicationinput.Build(parseManifest(t, "{}\n"), inventory, applicationInputSourceContext(), nil, generationexec.BuildOptions{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(input.Candidates) != 2 || !reflect.DeepEqual(capabilityInputIDs(t, input.Capabilities), []string{"email.send/v1", "kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("input = candidates %#v, capabilities %v", input.Candidates, capabilityInputIDs(t, input.Capabilities))
	}
	if !bytes.Equal(input.Candidates[0].Contract, input.Candidates[1].Contract) {
		t.Fatalf("provider contracts differ: %s != %s", input.Candidates[0].Contract, input.Candidates[1].Contract)
	}
	if got := input.Capabilities[0].Sources; !reflect.DeepEqual(got, []string{
		"example.com/app@local/mock/capabilities/email.send/v1/capability.yaml",
		"example.com/app@local/smtp/capabilities/email.send/v1/capability.yaml",
	}) {
		t.Fatalf("merged contract sources = %v", got)
	}
}

func TestBuildRejectsPluginProvidedIntrinsicCapability(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writePlugin(t, root, "health", "id: example.health\nprovides: [kernel.health/v1]\n")
	inventory := configureInventory(t, root)
	input, err := applicationinput.Build(parseManifest(t, "{}\n"), inventory, applicationInputSourceContext(), nil, generationexec.BuildOptions{})
	if !errors.Is(err, applicationinput.ErrBuild) || !errors.Is(err, applicationinput.ErrIntrinsicProvider) || len(input.Capabilities) != 0 || !strings.Contains(err.Error(), "example.health") {
		t.Fatalf("Build = %#v, %v; want intrinsic provider failure", input, err)
	}
}

type dependency struct {
	path    string
	version string
	root    string
}

func applicationInputSourceContext(dependencies ...dependency) applicationinput.SourceContext {
	values := make([]applicationinput.DependencySource, len(dependencies))
	for index, dependency := range dependencies {
		values[index] = applicationinput.DependencySource{ModulePath: dependency.path, Version: dependency.version}
	}
	return applicationinput.SourceContext{CurrentModulePath: "example.com/app", Dependencies: values}
}

func configureInventory(t *testing.T, appRoot string, dependencies ...dependency) plugininventory.Index {
	t.Helper()
	sort.Slice(dependencies, func(left, right int) bool { return dependencies[left].path < dependencies[right].path })
	data, err := os.ReadFile(filepath.Join(appRoot, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	var goMod strings.Builder
	goMod.Write(data)
	if len(dependencies) != 0 {
		goMod.WriteString("\nrequire (\n")
		for _, dependency := range dependencies {
			goMod.WriteString("\t" + dependency.path + " " + dependency.version + "\n")
		}
		goMod.WriteString(")\n")
		for _, dependency := range dependencies {
			relative, err := filepath.Rel(appRoot, dependency.root)
			if err != nil {
				t.Fatalf("Rel: %v", err)
			}
			goMod.WriteString("\nreplace " + dependency.path + " => " + filepath.ToSlash(relative) + "\n")
		}
		writeFile(t, filepath.Join(appRoot, "go.mod"), goMod.String())
	}
	application, err := modulelocate.Find(appRoot)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{Environment: isolatedGoEnvironment()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	inventory, err := plugininventory.Build(application, resolved)
	if err != nil {
		t.Fatalf("plugininventory.Build: %v", err)
	}
	return inventory
}

func parseManifest(t *testing.T, source string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(source))
	if err != nil {
		t.Fatalf("applicationmeta.Parse: %v", err)
	}
	return manifest
}

func isolatedGoEnvironment() []string {
	overrides := map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func writeModule(t *testing.T, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "plugin.yaml"), manifest)
}

func writeCapability(t *testing.T, moduleRoot, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(moduleRoot, plugin, "capabilities", identifier.Name(), "v1", "capability.yaml"), withQuerySemantics(source))
}

func withQuerySemantics(source string) string {
	if strings.Contains(source, "\nsemantics:") {
		return source
	}
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + querySemanticsYAML
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

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func capabilityInputIDs(t *testing.T, inputs []generation.CapabilityInput) []string {
	t.Helper()
	result := make([]string, len(inputs))
	for index, input := range inputs {
		identifier, err := capabilitymeta.ParseID(input.ContractJSON)
		if err != nil {
			t.Fatalf("ParseID(capabilities[%d]): %v", index, err)
		}
		result[index] = identifier.String()
	}
	return result
}

func candidateStrings(t *testing.T, candidates []providerresolution.Candidate) []string {
	t.Helper()
	result := make([]string, len(candidates))
	for index, candidate := range candidates {
		identifier, err := capabilitymeta.ParseID(candidate.Contract)
		if err != nil {
			t.Fatalf("ParseID(candidate %d): %v", index, err)
		}
		result[index] = candidate.PluginID + ":" + identifier.String() + ":" + candidate.Source
	}
	return result
}

func resolutionPluginStrings(plugins []generationresolution.Plugin) []string {
	result := make([]string, len(plugins))
	for index, plugin := range plugins {
		version := plugin.Context.ModuleVersion
		if version == "" {
			version = "local"
		}
		result[index] = plugin.Context.ID + ":" + plugin.Context.ModulePath + "@" + version + ":" + plugin.PluginPath + ":" + boolString(plugin.Local) + ":" + strings.Join(plugin.Context.Provides, ",") + ":" + strings.Join(plugin.Context.Requires, ",")
	}
	return result
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
