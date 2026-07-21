package applicationresolve_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/resolutionevidence"
	kernelconfiguration "github.com/plystra/kernel/configuration"
)

func TestMain(main *testing.M) {
	if mode := os.Getenv("PLYSTRA_APPLICATION_RESOLVE_HELPER"); mode != "" {
		os.Exit(runResolveHelper(mode))
	}
	os.Exit(main.Run())
}

func TestResolveEmptyApplicationDeterministicallyWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/empty")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := snapshotTree(t, root)
	options := applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.Module().Path() != root || first.Module().ModulePath() != "example.com/empty" {
		t.Fatalf("Module = %#v", first.Module())
	}
	assertResolvedConfigurationProvenance(t, first)
	if _, exists := first.Manifest().HTTPAddress(); exists || len(first.Manifest().Requirements()) != 0 || len(first.Manifest().Aliases()) != 0 {
		t.Fatalf("Manifest is not empty: %#v", first.Manifest())
	}
	if transports := first.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("default HTTP transports = %#v", transports)
	}
	if cors, exists := first.Manifest().HTTPCORS(); exists {
		t.Fatalf("default HTTPCORS = %#v, %t", cors, exists)
	}
	if len(first.Inventory().Plugins()) != 0 {
		t.Fatalf("Inventory = %#v", first.Inventory().Plugins())
	}
	if len(first.Dependencies().Modules()) != 0 {
		t.Fatalf("Dependencies = %#v", first.Dependencies().Modules())
	}
	resolved := first.Resolution()
	if resolved.Passes() != 1 || len(resolved.Context().Plugins()) != 0 || len(resolved.Context().Requirements()) != 0 || len(resolved.Context().Providers()) != 0 {
		t.Fatalf("empty resolution = passes %d, plugins %#v, requirements %#v, providers %#v", resolved.Passes(), resolved.Context().Plugins(), resolved.Context().Requirements(), resolved.Context().Providers())
	}
	capabilities := resolved.Context().Capabilities()
	if len(capabilities) != 2 || capabilities[0].ID().String() != "kernel.health/v1" || capabilities[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("intrinsic catalog = %#v", capabilities)
	}
	if len(resolved.AliasResolution().Aliases()) != 0 {
		t.Fatalf("Aliases = %#v", resolved.AliasResolution().Aliases())
	}
	if !first.Configurations().Valid() || len(first.Configurations().Bindings()) != 0 || first.Configurations().Digest() == "" {
		t.Fatalf("Configurations = %#v", first.Configurations())
	}
	evidence := first.ResolutionEvidence()
	if !evidence.Valid() || evidence.SelectedModelDigest() != resolved.Context().Digest() || evidence.BuildModelDigest() != resolved.Context().BuildModelDigest() {
		t.Fatalf("ResolutionEvidence = valid %t selected %q build %q", evidence.Valid(), evidence.SelectedModelDigest(), evidence.BuildModelDigest())
	}
	if evidence.DiscoveredPluginCount() != 0 || evidence.SelectedPluginCount() != 0 || evidence.CanonicalCapabilityCount() != 2 || evidence.RequirementCount() != 0 || evidence.SelectedProviderCount() != 0 || evidence.CapabilityAliasCount() != 0 {
		t.Fatalf("ResolutionEvidence counts = discovered %d selected %d capabilities %d requirements %d providers %d aliases %d", evidence.DiscoveredPluginCount(), evidence.SelectedPluginCount(), evidence.CanonicalCapabilityCount(), evidence.RequirementCount(), evidence.SelectedProviderCount(), evidence.CapabilityAliasCount())
	}
	modules := evidence.Modules()
	if evidence.ParticipatingModuleCount() != 1 || len(modules) != 1 || modules[0].Path() != "example.com/empty" || modules[0].Role() != resolutionevidence.ModuleRoleCurrent || modules[0].Source().Module() != "example.com/empty" || modules[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("ResolutionEvidence modules = %#v", modules)
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || resolved.Context().Digest() != second.Resolution().Context().Digest() || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() || !bytes.Equal(evidence.CanonicalJSON(), second.ResolutionEvidence().CanonicalJSON()) || evidence.Digest() != second.ResolutionEvidence().Digest() {
		t.Fatal("repeated empty resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveUsesCompleteSelectedConfigurationAboveDependencyProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/platform v1.0.0

replace example.com/platform => ../platform
`)
	rootConfiguration := "http: {address: \":8080\", transports: {connect: false, rest: false}, cors: {allowed_origins: ['*']}}\ncapabilities: {require: [kernel.health/v1]}\n"
	selectedConfiguration := "# selected file remains independently authored\nhttp: {address: \":9090\", transports: {rest: true}, cors: {allowed_origins: [https://customer.example], allow_credentials: true}}\ncapabilities: {require: [kernel.info/v1]}\n"
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), rootConfiguration)
	writeFile(t, filepath.Join(appRoot, "deploy", "customer.yaml"), selectedConfiguration)
	before := snapshotTree(t, appRoot)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:             filepath.Join(appRoot, "deploy"),
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "ignored.yaml"}),
	})
	if err != nil {
		t.Fatalf("Resolve explicit configuration: %v", err)
	}
	selection := result.ConfigurationSelection()
	if selection.Mode() != "explicit-config" || selection.Path() != "deploy/customer.yaml" || !strings.HasPrefix(selection.Digest(), "sha256:") {
		t.Fatalf("ConfigurationSelection = mode %q path %q digest %q", selection.Mode(), selection.Path(), selection.Digest())
	}
	assertResolvedConfigurationProvenance(t, result)
	if address, exists := result.Manifest().HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("effective HTTP address = %q, %t; root replacement leaked", address, exists)
	}
	if transports := result.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true, REST: true}) {
		t.Fatalf("effective replacement HTTP transports = %#v; root replacement leaked", transports)
	}
	cors, exists := result.Manifest().HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://customer.example"}) || !cors.AllowCredentials {
		t.Fatalf("effective replacement HTTPCORS = %#v, %t; root replacement leaked", cors, exists)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	if !result.ConfigurationMaintenance().Changed() || !bytes.Contains(result.ConfigurationMaintenance().Data(), []byte("kernel.health/v1")) {
		t.Fatalf("selected maintenance = changed %t, data %q", result.ConfigurationMaintenance().Changed(), result.ConfigurationMaintenance().Data())
	}
	if !bytes.Equal(result.RootConfigurationData(), []byte(rootConfiguration)) || !bytes.Equal(result.ConfigurationSource(), []byte(selectedConfiguration)) {
		t.Fatal("root or selected source provenance was not preserved independently")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated selected configuration:\nbefore: %#v\nafter:  %#v", before, after)
	}

	ambient, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "deploy/customer.yaml"}),
	})
	if err != nil || ambient.ConfigurationSelection().Path() != "deploy/customer.yaml" {
		t.Fatalf("Resolve ambient configuration = path %q, error %v", ambient.ConfigurationSelection().Path(), err)
	}
}

func TestResolveRequiresRootMarkerAndSelectedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "deploy.yaml"), "{}\n")
	options := applicationresolve.Options{
		Start:             root,
		ConfigurationPath: "deploy.yaml",
		Environment:       goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}
	if _, err := applicationresolve.Resolve(t.Context(), options); err == nil || !errors.Is(err, projectlocate.ErrNotFound) {
		t.Fatalf("Resolve without root marker error = %v", err)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	options.ConfigurationPath = "missing.yaml"
	if _, err := applicationresolve.Resolve(t.Context(), options); err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("Resolve missing selected file error = %v", err)
	}
}

func TestResolveAppliesOneEnvironmentOverlayAboveRootAndDependencies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyARoot := filepath.Join(root, "platform-a")
	dependencyBRoot := filepath.Join(root, "platform-b")
	writeModule(t, dependencyARoot, "example.com/platform-a")
	writeModule(t, dependencyBRoot, "example.com/platform-b")
	writeFile(t, filepath.Join(dependencyARoot, "plystra.yaml"), "http: {cors: {allowed_origins: ['*']}, expose: [kernel.info/v1]}\ncapabilities: {require: [kernel.health/v1]}\n")
	writeFile(t, filepath.Join(dependencyARoot, "plystra.production.yaml"), "capabilities: {require: [kernel.info/v1]}\n")
	writeFile(t, filepath.Join(dependencyBRoot, "plystra.yaml"), "capabilities: {require: {remove: [kernel.health/v1]}}\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/platform-a v1.0.0
	example.com/platform-b v1.0.0
)

replace example.com/platform-a => ../platform-a

replace example.com/platform-b => ../platform-b
`)
	rootConfiguration := "# shared root\nhttp: {address: \":8080\", transports: {connect: false, rest: true}, cors: {allowed_origins: [https://shared.example], allow_credentials: true}}\ncapabilities: {require: [kernel.info/v1]}\n"
	overlayConfiguration := "# sparse production overlay\nhttp: {address: \":9090\", transports: {connect: true, rest: null}, cors: {allow_credentials: null}}\ncapabilities:\n  require: {add: [kernel.health/v1], remove: [kernel.info/v1]}\n"
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), rootConfiguration)
	writeFile(t, filepath.Join(appRoot, "plystra.production.yaml"), overlayConfiguration)
	before := snapshotTree(t, appRoot)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           appRoot,
		EnvironmentName: "production",
		Environment:     goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "ignored.yaml", "PLYSTRA_ENV": "ignored"}),
	})
	if err != nil {
		t.Fatalf("Resolve environment: %v", err)
	}
	selection := result.ConfigurationSelection()
	if selection.Mode() != applicationgen.ConfigurationModeEnvironment || selection.Environment() != "production" || selection.Path() != "plystra.production.yaml" || !strings.HasPrefix(selection.Digest(), "sha256:") {
		t.Fatalf("ConfigurationSelection = mode %q environment %q path %q digest %q", selection.Mode(), selection.Environment(), selection.Path(), selection.Digest())
	}
	assertResolvedConfigurationProvenance(t, result)
	if address, exists := result.Manifest().HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("effective HTTP address = %q, %t", address, exists)
	}
	if transports := result.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("effective environment HTTP transports = %#v", transports)
	}
	cors, exists := result.Manifest().HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://shared.example"}) || cors.AllowCredentials {
		t.Fatalf("effective environment HTTPCORS = %#v, %t", cors, exists)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	if !result.ConfigurationMaintenance().Changed() || result.ConfigurationMaintenancePath() != "plystra.yaml" || !bytes.Equal(result.ConfigurationMaintenanceSource(), []byte(rootConfiguration)) {
		t.Fatalf("root maintenance = changed %t path %q source %q", result.ConfigurationMaintenance().Changed(), result.ConfigurationMaintenancePath(), result.ConfigurationMaintenanceSource())
	}
	if !bytes.Contains(result.RootConfigurationData(), []byte("expose:")) || !bytes.Equal(result.ConfigurationSource(), []byte(overlayConfiguration)) {
		t.Fatal("root or overlay provenance was not preserved independently")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve environment mutated Project:\nbefore: %#v\nafter: %#v", before, after)
	}

	ambient, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_ENV": "production"}),
	})
	if err != nil || ambient.ConfigurationSelection().Environment() != "production" {
		t.Fatalf("Resolve PLYSTRA_ENV = environment %q, %v", ambient.ConfigurationSelection().Environment(), err)
	}
}

func TestResolveRequiresSelectedEnvironmentOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           root,
		EnvironmentName: "production",
		Environment:     goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err == nil || !strings.Contains(err.Error(), "plystra.production.yaml") {
		t.Fatalf("Resolve missing environment overlay error = %v", err)
	}
}

func TestResolveDerivesExposureFromEverySelectedConfigurationMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		mode         string
		rootData     string
		selectedPath string
		selectedData string
		configure    func(*applicationresolve.Options)
	}{
		{
			name:         "default",
			mode:         applicationgen.ConfigurationModeDefault,
			rootData:     "http: {expose: [kernel.health/v1]}\n",
			selectedPath: "plystra.yaml",
		},
		{
			name:         "environment overlay",
			mode:         applicationgen.ConfigurationModeEnvironment,
			rootData:     "http: {expose: [kernel.info/v1]}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "http:\n  expose: {add: [kernel.health/v1], remove: [kernel.info/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			mode:         applicationgen.ConfigurationModeExplicit,
			rootData:     "http: {expose: [kernel.info/v1]}\n",
			selectedPath: "deploy/customer.yaml",
			selectedData: "http: {expose: [kernel.health/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.ConfigurationPath = "deploy/customer.yaml"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeModule(t, root, "example.com/selected-exposure/"+strings.ReplaceAll(test.name, " ", "-"))
			writeFile(t, filepath.Join(root, "plystra.yaml"), test.rootData)
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, filepath.Join(root, filepath.FromSlash(test.selectedPath)), test.selectedData)
			}
			before := snapshotTree(t, root)
			options := applicationresolve.Options{
				Start:       filepath.Join(root, filepath.Dir(filepath.FromSlash(test.selectedPath))),
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
			}
			if test.configure != nil {
				test.configure(&options)
			}

			result, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			selection := result.ConfigurationSelection()
			if selection.Mode() != test.mode || selection.Path() != test.selectedPath {
				t.Fatalf("selection = mode %q path %q", selection.Mode(), selection.Path())
			}
			if got := applicationExposureIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1"}) {
				t.Fatalf("effective exposures = %v", got)
			}
			if got := applicationRequirementIDs(result.Manifest()); len(got) != 0 {
				t.Fatalf("authored requirements = %v", got)
			}
			if requirements := result.Resolution().Context().Requirements(); len(requirements) != 1 || requirements[0].String() != "kernel.health/v1" {
				t.Fatalf("resolved exposure requirements = %v", requirements)
			}
			healthID := parseGenerationCapability(t, "kernel.health/v1")
			health, exists := result.Resolution().Context().Capability(healthID)
			if !exists || health.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
				t.Fatalf("health exposure = %#v, %t", health.Exposure(), exists)
			}
			infoID := parseGenerationCapability(t, "kernel.info/v1")
			info, exists := result.Resolution().Context().Capability(infoID)
			if !exists || info.Exposure() != (generation.Exposure{Go: true}) {
				t.Fatalf("unselected info exposure = %#v, %t", info.Exposure(), exists)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("Resolve mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestResolveRejectsSelectedExposureWithoutHTTPTransport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		rootData  string
		path      string
		exposeKey string
		selected  string
		configure func(*applicationresolve.Options)
	}{
		{
			name:      "default",
			rootData:  "http: {transports: {connect: false, rest: false}, expose: [kernel.health/v1]}\n",
			path:      "plystra.yaml",
			exposeKey: "http.expose",
		},
		{
			name:      "environment overlay",
			rootData:  "{}\n",
			path:      "plystra.production.yaml",
			exposeKey: "http.expose.add",
			selected:  "http: {transports: {connect: false, rest: false}, expose: {add: [kernel.health/v1]}}\n",
			configure: func(options *applicationresolve.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:      "full replacement",
			rootData:  "{}\n",
			path:      "deploy/customer.yaml",
			exposeKey: "http.expose",
			selected:  "http: {transports: {connect: false, rest: false}, expose: [kernel.health/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.ConfigurationPath = "deploy/customer.yaml"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeModule(t, root, "example.com/no-http-transport/"+strings.ReplaceAll(test.name, " ", "-"))
			writeFile(t, filepath.Join(root, "plystra.yaml"), test.rootData)
			if test.path != "plystra.yaml" {
				writeFile(t, filepath.Join(root, filepath.FromSlash(test.path)), test.selected)
			}
			before := snapshotTree(t, root)
			options := applicationresolve.Options{
				Start:       root,
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
			}
			if test.configure != nil {
				test.configure(&options)
			}

			result, err := applicationresolve.Resolve(t.Context(), options)
			if !errors.Is(err, applicationmeta.ErrHTTPTransportSelection) || result.Module().Path() != "" {
				t.Fatalf("Resolve = %#v, %v", result, err)
			}
			for _, want := range []string{
				"http.expose is nonempty",
				"http.transports.connect and http.transports.rest are both false",
				`kernel.health/v1 at ` + test.path + ` ` + test.exposeKey + `["kernel.health/v1"]`,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Resolve error %q does not contain %q", err, want)
				}
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("Resolve mutated rejected Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestResolveClosesLocalRequirementsThroughDependencyProvidersAndAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), `http:
  address: ":9090"
  expose: [email.send/v1]
timeouts: {startup: 1s}
capabilities:
  use: {email.send/v1: example.smtp}
  aliases:
    mail.send/v1: email.send/v1
config:
  example.smtp:
    host: private.smtp.example.com
    password: {env: PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET}
`)
	writeFile(t, filepath.Join(providerRoot, "plystra.production.yaml"), "not: [a valid dependency overlay\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig: {host: {type: string, required: true}, password: {type: secret, required: true}}\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/providers v1.2.3

replace example.com/providers => ../providers
`)
	writePlugin(t, appRoot, "local", "id: example.local\nrequires: [email.send/v1]\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "http: {address: \":8080\"}\n")
	before := snapshotTree(t, appRoot)
	dependencyBefore := snapshotTree(t, providerRoot)
	options := applicationresolve.Options{
		Start:       filepath.Join(appRoot, "local"),
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET": "resolved-private-secret"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertResolvedConfigurationProvenance(t, first)
	plugins := first.Inventory().Plugins()
	dependencies := first.Dependencies().Modules()
	if len(dependencies) != 1 || dependencies[0].Path() != "example.com/providers" || dependencies[0].SelectedVersion() != "v1.2.3" {
		t.Fatalf("Dependencies = %#v", dependencies)
	}
	if !first.Composition().Valid() || first.Composition().DependencyDigest() == "" || len(first.Composition().Provenance()) == 0 {
		t.Fatalf("Composition = %#v", first.Composition())
	}
	if address, exists := first.Manifest().HTTPAddress(); !exists || address != ":8080" || first.Manifest().StartupTimeout() != applicationmeta.DefaultStartupTimeout || len(first.CurrentManifest().HTTPExposures()) != 1 || len(first.Manifest().HTTPExposures()) != 1 || !first.ConfigurationMaintenance().Changed() {
		t.Fatalf("composed/current manifests = effective %#v, current %#v", first.Manifest(), first.CurrentManifest())
	}
	if got := pluginSummaries(plugins); !reflect.DeepEqual(got, []string{
		"example.local:example.com/app@local:local:true",
		"example.smtp:example.com/providers@v1.2.3:smtp:false",
	}) {
		t.Fatalf("Inventory = %v", got)
	}
	resolved := first.Resolution()
	capability := parseGenerationCapability(t, "email.send/v1")
	provider, exists := resolved.Context().SelectedProvider(capability)
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("SelectedProvider(email.send/v1) = %s, %t", provider, exists)
	}
	if requirements := resolved.Context().Requirements(); len(requirements) != 1 || requirements[0] != capability {
		t.Fatalf("Requirements = %v", requirements)
	}
	target, exists := resolved.Context().Capability(capability)
	if !exists || target.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
		t.Fatalf("target exposure = %#v, %t", target.Exposure(), exists)
	}
	aliases := resolved.AliasResolution().Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "mail.send/v1" || aliases[0].Target().String() != "email.send/v1" || aliases[0].Exposure() != target.Exposure() {
		t.Fatalf("Aliases = %#v", aliases)
	}
	if got := configurationBindingIDs(first.Configurations().Bindings()); !reflect.DeepEqual(got, []string{"example.local", "example.smtp"}) {
		t.Fatalf("configuration bindings = %v", got)
	}
	for _, forbidden := range []string{"private.smtp.example.com", "PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET", "resolved-private-secret", appRoot, providerRoot} {
		if bytes.Contains(resolved.Context().CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("generation context exposed private configuration %q: %s", forbidden, resolved.Context().CanonicalJSON())
		}
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() {
		t.Fatal("repeated dependency resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if after := snapshotTree(t, providerRoot); !reflect.DeepEqual(after, dependencyBefore) {
		t.Fatalf("Resolve mutated dependency Project:\nbefore: %#v\nafter:  %#v", dependencyBefore, after)
	}
}

func TestResolveComposesDirectAndTransitiveDependencyProjectDeclarations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	directRoot := filepath.Join(root, "direct")
	transitiveRoot := filepath.Join(root, "transitive")
	ordinaryRoot := filepath.Join(root, "ordinary")

	writeModule(t, transitiveRoot, "example.com/transitive")
	writeFile(t, filepath.Join(transitiveRoot, "plystra.yaml"), "capabilities: {require: [audit.write/v1]}\n")
	writePlugin(t, transitiveRoot, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, transitiveRoot, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeFile(t, filepath.Join(directRoot, "go.mod"), "module example.com/direct\n\ngo 1.26\n\nrequire example.com/transitive v1.4.0\n")
	writeFile(t, filepath.Join(directRoot, "plystra.yaml"), `http:
  expose: [email.send/v1]
capabilities:
  use: {email.send/v1: example.smtp}
`)
	writePlugin(t, directRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, directRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writePlugin(t, directRoot, "unused", "id: example.unused\n")

	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writeFile(t, filepath.Join(ordinaryRoot, "looks-like-plugin", "plugin.yaml"), "this is deliberately not a valid Plugin declaration\n")

	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/direct v1.2.0
	example.com/ordinary v1.0.0
)

replace example.com/direct => ../direct
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, appRoot, "app", "id: example.app\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	projects := result.Dependencies().Projects()
	if len(projects) != 2 || projects[0].Path() != "example.com/direct" || projects[0].SelectedVersion() != "v1.2.0" || projects[1].Path() != "example.com/transitive" || projects[1].SelectedVersion() != "v1.4.0" || projects[1].Direct() {
		t.Fatalf("dependency Projects = %#v", projects)
	}
	if got := pluginSummaries(result.Inventory().Plugins()); !reflect.DeepEqual(got, []string{
		"example.app:example.com/app@local:app:true",
		"example.audit:example.com/transitive@v1.4.0:audit:false",
		"example.smtp:example.com/direct@v1.2.0:smtp:false",
		"example.unused:example.com/direct@v1.2.0:unused:false",
	}) {
		t.Fatalf("visible Plugins = %v", got)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"audit.write/v1"}) {
		t.Fatalf("composed requirements = %v", got)
	}
	if got := applicationExposureIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("composed exposures = %v", got)
	}
	contextRequirements := result.Resolution().Context().Requirements()
	if len(contextRequirements) != 2 || contextRequirements[0].String() != "audit.write/v1" || contextRequirements[1].String() != "email.send/v1" {
		t.Fatalf("resolved requirements = %v", contextRequirements)
	}
	if provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1")); !exists || provider.String() != "example.smtp" {
		t.Fatalf("email Provider = %s, %t", provider, exists)
	}
	evidenceModules := result.ResolutionEvidence().Modules()
	if len(evidenceModules) != 3 || evidenceModules[0].Path() != "example.com/app" || evidenceModules[0].Role() != resolutionevidence.ModuleRoleCurrent || evidenceModules[1].Path() != "example.com/direct" || !evidenceModules[1].Direct() || evidenceModules[1].RequiredVersion() != "v1.2.0" || evidenceModules[1].SelectedVersion() != "v1.2.0" || evidenceModules[2].Path() != "example.com/transitive" || evidenceModules[2].Direct() || evidenceModules[2].RequiredVersion() != "" || evidenceModules[2].SelectedVersion() != "v1.4.0" {
		t.Fatalf("resolution evidence modules = %#v", evidenceModules)
	}
	for _, module := range evidenceModules[1:] {
		replacement, exists := module.Replacement()
		if !exists || replacement.Kind() != resolutionevidence.ReplacementLocal || replacement.ModulePath() != module.Path() || replacement.Version() != "" || module.Source().Module() != module.Path() || module.Source().Path() != "plystra.yaml" {
			t.Fatalf("resolution evidence replacement for %s = %#v/%t source %#v", module.Path(), replacement, exists, module.Source())
		}
	}
	evidenceCandidates := result.ResolutionEvidence().PluginCandidates()
	if result.ResolutionEvidence().DiscoveredPluginCount() != 4 || result.ResolutionEvidence().SelectedPluginCount() != 3 || len(evidenceCandidates) != 4 || evidenceCandidates[0].ID() != "example.app" || evidenceCandidates[0].ModulePath() != "example.com/app" || evidenceCandidates[0].ModuleRole() != resolutionevidence.ModuleRoleCurrent || !evidenceCandidates[0].Local() || evidenceCandidates[0].Source().Module() != "example.com/app" || evidenceCandidates[0].Source().Path() != "app/plugin.yaml" || evidenceCandidates[1].ID() != "example.audit" || evidenceCandidates[1].ModulePath() != "example.com/transitive" || evidenceCandidates[2].ID() != "example.smtp" || evidenceCandidates[2].ModulePath() != "example.com/direct" || evidenceCandidates[3].ID() != "example.unused" || evidenceCandidates[3].ModulePath() != "example.com/direct" || evidenceCandidates[3].Local() {
		t.Fatalf("resolution evidence Plugin candidates = %#v", evidenceCandidates)
	}
	for _, candidate := range evidenceCandidates {
		if candidate.Source().Kind() != "plugin-declaration" || candidate.Source().Line() != 1 || candidate.Source().Column() != 1 {
			t.Fatalf("resolution evidence Plugin candidate source = %#v", candidate.Source())
		}
	}
	if bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(filepath.ToSlash(root))) || bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(root)) || bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte("example.com/ordinary")) {
		t.Fatalf("resolution evidence contains an absolute root or ordinary dependency: %s", result.ResolutionEvidence().CanonicalJSON())
	}
	provenance := result.Composition().Provenance()
	for path, source := range map[string]string{
		`http.expose["email.send/v1"]`:           "example.com/direct@v1.2.0/plystra.yaml",
		`capabilities.require["audit.write/v1"]`: "example.com/transitive@v1.4.0/plystra.yaml",
		`capabilities.use["email.send/v1"]`:      "example.com/direct@v1.2.0/plystra.yaml",
	} {
		records := compositionProvenance(provenance, path)
		if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.HasPrefix(records[0].Sources()[0], source) {
			t.Fatalf("provenance for %s = %#v", path, records)
		}
	}
}

func TestResolveReportsInheritedProviderConflictAndAcceptsExactCurrentReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	moduleRoots := map[string]string{
		"example.com/a": filepath.Join(root, "a"),
		"example.com/b": filepath.Join(root, "b"),
	}
	providers := map[string]string{"example.com/a": "example.smtp-a", "example.com/b": "example.smtp-b"}
	for modulePath, moduleRoot := range moduleRoots {
		writeModule(t, moduleRoot, modulePath)
		provider := providers[modulePath]
		writeFile(t, filepath.Join(moduleRoot, "plystra.yaml"), fmt.Sprintf("capabilities: {use: {email.send/v1: %s}}\n", provider))
		writePlugin(t, moduleRoot, "smtp", fmt.Sprintf("id: %s\nprovides: [email.send/v1]\n", provider))
		writeCapability(t, moduleRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.0.0
)

replace example.com/a => ../a
replace example.com/b => ../b
`)
	manifestPath := filepath.Join(appRoot, "plystra.yaml")
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\n")
	options := applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}

	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("Resolve conflict error = %v", err)
	}
	for _, required := range []string{
		`capabilities.use["email.send/v1"]`,
		"example.smtp-a",
		"example.smtp-b",
		"example.com/a@v1.0.0/plystra.yaml",
		"example.com/b@v1.0.0/plystra.yaml",
	} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("conflict error omits %q: %v", required, err)
		}
	}

	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1], use: {email.send/v1: example.smtp-a}}\n")
	result, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve with current replacement: %v", err)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp-a" {
		t.Fatalf("selected replacement Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 2 {
		t.Fatalf("inherited conflict provenance = %#v", records)
	}
}

func TestResolveCurrentProviderRemovalRestoresUniqueProviderSelection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeModule(t, dependencyRoot, "example.com/smtp")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/smtp v1.0.0

replace example.com/smtp => ../smtp
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: null}}\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve with Provider removal: %v", err)
	}
	if len(result.Manifest().ProviderChoices()) != 0 {
		t.Fatalf("effective explicit Provider choices = %#v", result.Manifest().ProviderChoices())
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("automatic unique Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.Contains(records[0].Sources()[0], "example.com/smtp@v1.0.0/plystra.yaml") {
		t.Fatalf("inherited Provider provenance = %#v", records)
	}
}

func TestResolveConfigurationRemovalStillRunsFinalRequiredFieldValidation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeModule(t, dependencyRoot, "example.com/smtp")
	privateHost := "private-dependency.example"
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\nconfig: {example.smtp: {host: "+privateHost+"}}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig:\n  host: {type: string, required: true}\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/smtp v1.0.0

replace example.com/smtp => ../smtp
`)
	manifestPath := filepath.Join(appRoot, "plystra.yaml")
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\nconfig: {example.smtp: {host: null}}\n")
	options := applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, kernelconfiguration.ErrMissingField) || strings.Contains(err.Error(), privateHost) {
		t.Fatalf("Resolve removed required field error = %v", err)
	}
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\nconfig: {example.smtp: {host: current.example}}\n")
	result, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve replacement field: %v", err)
	}
	configured, exists := result.Manifest().Configuration("example.smtp")
	if !exists || !bytes.Contains(configured.YAML(), []byte("host: current.example")) {
		t.Fatalf("effective replacement configuration = %s, %t", configured.YAML(), exists)
	}
}

func TestResolveRejectsMalformedAndUnsafeDependencyProjectManifest(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "unknown: true\n")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency@v1.2.3") || !strings.Contains(err.Error(), `unknown key "unknown"`) {
			t.Fatalf("Resolve malformed dependency error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		target := filepath.Join(root, "outside.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(dependencyRoot, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency") {
			t.Fatalf("Resolve unsafe dependency error = %v", err)
		}
	})
}

func TestResolveUsesActiveGoWorkspaceDependencySource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/workspace-app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n")
	goWork := filepath.Join(root, "go.work")
	writeFile(t, goWork, "go 1.26\n\nuse (\n\t./app\n\t./providers\n)\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": goWork, "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plugins := result.Inventory().Plugins()
	if len(plugins) != 1 || plugins[0].ID() != "example.smtp" || plugins[0].ModuleRoot() != providerRoot || plugins[0].ModuleVersion() != "" || plugins[0].Source() != "example.com/providers@local/smtp/plugin.yaml" {
		t.Fatalf("workspace plugin = %#v, summaries %v", plugins, pluginSummaries(plugins))
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("workspace provider = %s, %t", provider, exists)
	}
	modules := result.ResolutionEvidence().Modules()
	if len(modules) != 2 || modules[0].Path() != "example.com/workspace-app" || modules[0].Role() != resolutionevidence.ModuleRoleCurrent || modules[1].Path() != "example.com/providers" || modules[1].Role() != resolutionevidence.ModuleRoleDependency || !modules[1].Workspace() || modules[1].SelectedVersion() != "" || modules[1].Direct() || modules[1].Source().Module() != "example.com/providers" {
		t.Fatalf("workspace resolution evidence modules = %#v", modules)
	}
	if _, exists := modules[1].Replacement(); exists {
		t.Fatalf("workspace resolution evidence has replacement provenance: %#v", modules[1])
	}
	candidates := result.ResolutionEvidence().PluginCandidates()
	if len(candidates) != 1 || candidates[0].ID() != "example.smtp" || candidates[0].ModulePath() != "example.com/providers" || candidates[0].ModuleRole() != resolutionevidence.ModuleRoleDependency || candidates[0].Path() != "smtp" || candidates[0].Local() || candidates[0].Source().Module() != "example.com/providers" || candidates[0].Source().Path() != "smtp/plugin.yaml" {
		t.Fatalf("workspace resolution evidence Plugin candidates = %#v", candidates)
	}
}

func TestResolveExecutesSelectedFilesystemGenerationExtension(t *testing.T) {
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(
		"module example.com/extension-app\n\ngo 1.26\n\nrequire (\n\tgithub.com/plystra/cli v0.0.0\n\tgithub.com/plystra/kernel v0.0.0\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/cli => %s\n\nreplace github.com/plystra/kernel => %s\n",
		strconv.Quote(filepath.ToSlash(cliRoot)),
		strconv.Quote(filepath.ToSlash(kernelRoot)),
	)
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n")
	writePlugin(t, root, "business", "id: example.business\nprovides: [order.create/v1]\n")
	writePlugin(t, root, "authn", `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writePlugin(t, root, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "order.create/v1", `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCapability(t, root, "authn", "authn.session.verify/v1", "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), realExtensionSource)
	before := snapshotTree(t, root)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOWORK": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertResolvedConfigurationProvenance(t, result)
	generated := result.Resolution().GeneratedRequirements()
	if result.Resolution().Passes() != 3 || len(generated) != 1 || generated[0].PluginID() != "example.authn" || generated[0].Capability().String() != "audit.write/v1" {
		t.Fatalf("extension resolution = passes %d, generated %#v", result.Resolution().Passes(), generated)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "audit.write/v1"))
	if !exists || provider.String() != "example.audit" {
		t.Fatalf("generated audit provider = %s, %t", provider, exists)
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension artifacts = %v, %v", entries, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("extension resolution mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveRejectsMissingUnsafeAndChangingManifest(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/missing")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrNotFound) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/directory")
		if err := os.Mkdir(filepath.Join(root, "plystra.yaml"), 0o755); err != nil {
			t.Fatalf("Mkdir(plystra.yaml): %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/symbolic")
		target := filepath.Join(t.TempDir(), "application.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(root, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/oversized")
		writeFile(t, filepath.Join(root, "plystra.yaml"), strings.Repeat(" ", applicationmeta.MaximumSize+1))
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationresolve.ErrUnsafeManifest) || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("changed before completion", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/changing\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n")
		manifestPath := filepath.Join(appRoot, "plystra.yaml")
		writeFile(t, manifestPath, "{}\n")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start:     appRoot,
			GoCommand: os.Args[0],
			Environment: goEnvironment(map[string]string{
				"GOWORK":                             "off",
				"PLYSTRA_APPLICATION_RESOLVE_HELPER": "change-manifest",
				"PLYSTRA_APPLICATION_MANIFEST":       manifestPath,
				"PLYSTRA_APPLICATION_MODULE_ROOT":    dependencyRoot,
			}),
		})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrConcurrentChange) || !strings.Contains(err.Error(), "plystra.yaml") {
			t.Fatalf("Resolve error = %v", err)
		}
	})
}

func runResolveHelper(mode string) int {
	if mode != "change-manifest" {
		return 9
	}
	want := []string{"list", "-m", "-json", "-mod=readonly", "all"}
	if len(os.Args) != len(want)+1 {
		return 10
	}
	for index, value := range want {
		if os.Args[index+1] != value {
			return 11
		}
	}
	if err := os.WriteFile(os.Getenv("PLYSTRA_APPLICATION_MANIFEST"), []byte("timeouts: {}\n"), 0o644); err != nil {
		return 12
	}
	applicationRoot, err := os.Getwd()
	if err != nil {
		return 13
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{
		"Path":  "example.com/changing",
		"Main":  true,
		"Dir":   applicationRoot,
		"GoMod": filepath.Join(applicationRoot, "go.mod"),
	}); err != nil {
		return 14
	}
	root := os.Getenv("PLYSTRA_APPLICATION_MODULE_ROOT")
	if err := encoder.Encode(map[string]any{
		"Path":    "example.com/dependency",
		"Version": "v1.2.3",
		"Dir":     root,
		"GoMod":   filepath.Join(root, "go.mod"),
	}); err != nil {
		return 15
	}
	return 0
}

func writeModule(t testing.TB, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writePlugin(t testing.TB, moduleRoot, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(moduleRoot, name, "plugin.yaml"), manifest)
}

func writeCapability(t testing.TB, moduleRoot, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(moduleRoot, plugin, "capabilities", filepath.FromSlash(identifier.Name()), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml"), withQuerySemantics(source))
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

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func parseGenerationCapability(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	identifier, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("generation.ParseCapabilityID(%s): %v", value, err)
	}
	return identifier
}

func configurationBindingIDs(bindings []configurationresolve.Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = binding.PluginID()
	}
	return result
}

func applicationRequirementIDs(manifest applicationmeta.Manifest) []string {
	requirements := manifest.Requirements()
	result := make([]string, len(requirements))
	for index, requirement := range requirements {
		result[index] = requirement.ID().String()
	}
	return result
}

func applicationExposureIDs(manifest applicationmeta.Manifest) []string {
	exposures := manifest.HTTPExposures()
	result := make([]string, len(exposures))
	for index, exposure := range exposures {
		result[index] = exposure.ID().String()
	}
	return result
}

func compositionProvenance(values []applicationmeta.Provenance, path string) []applicationmeta.Provenance {
	var result []applicationmeta.Provenance
	for _, value := range values {
		if value.Path() == path {
			result = append(result, value)
		}
	}
	return result
}

func pluginSummaries(plugins []plugininventory.Plugin) []string {
	result := make([]string, len(plugins))
	for index, plugin := range plugins {
		version := plugin.ModuleVersion()
		if version == "" {
			version = "local"
		}
		result[index] = fmt.Sprintf("%s:%s@%s:%s:%t", plugin.ID(), plugin.ModulePath(), version, plugin.Path(), plugin.Local())
	}
	return result
}

func assertResolvedConfigurationProvenance(t testing.TB, result applicationresolve.Result) {
	t.Helper()
	provenance, exists := result.Resolution().Context().ConfigurationProvenance()
	selection := result.ConfigurationSelection()
	if !exists {
		t.Fatal("filesystem-backed resolution omitted configuration provenance")
	}
	rootDigest, err := applicationgen.ConfigurationDigest(result.RootConfigurationData())
	if err != nil {
		t.Fatalf("ConfigurationDigest(root): %v", err)
	}
	if provenance.Mode() != generation.ConfigurationMode(selection.Mode()) || provenance.Environment() != selection.Environment() || provenance.RootPath() != "plystra.yaml" || provenance.RootDigest() != rootDigest || provenance.SelectedPath() != selection.Path() || provenance.SelectedDigest() != selection.Digest() || provenance.DependencyCompositionDigest() != result.Composition().DependencyDigest() {
		t.Fatalf("configuration provenance = mode %q environment %q root %q/%q selected %q/%q dependency %q; selection = mode %q environment %q path %q digest %q", provenance.Mode(), provenance.Environment(), provenance.RootPath(), provenance.RootDigest(), provenance.SelectedPath(), provenance.SelectedDigest(), provenance.DependencyCompositionDigest(), selection.Mode(), selection.Environment(), selection.Path(), selection.Digest())
	}
	if result.Resolution().Context().Digest() == result.Resolution().Context().BuildModelDigest() {
		t.Fatal("filesystem configuration provenance did not enter the extension context digest")
	}
}

type treeEntry struct {
	path     string
	mode     fs.FileMode
	modified time.Time
	data     []byte
}

func snapshotTree(t testing.TB, root string) []treeEntry {
	t.Helper()
	var result []treeEntry
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		state := treeEntry{path: filepath.ToSlash(relative), mode: info.Mode()}
		if info.Mode().IsRegular() {
			state.modified = info.ModTime()
			state.data, err = os.ReadFile(name)
			if err != nil {
				return err
			}
		}
		result = append(result, state)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}

func goEnvironment(overrides map[string]string) []string {
	defaults := map[string]string{
		"GOENV":       "off",
		"GOTOOLCHAIN": "local",
	}
	for key, value := range overrides {
		defaults[strings.ToUpper(key)] = value
	}
	environment := make([]string, 0, len(os.Environ())+len(defaults))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := defaults[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+defaults[key])
	}
	return environment
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}

const realExtensionSource = `package extension

import (
	"fmt"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(context generation.GenerationContext) (generation.Output, error) {
	provenance, exists := context.ConfigurationProvenance()
	if !exists || provenance.Mode() != generation.ConfigurationModeDefault || provenance.Environment() != "" || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "plystra.yaml" || provenance.RootDigest() == "" || provenance.SelectedDigest() != provenance.RootDigest() || provenance.DependencyCompositionDigest() == "" {
		return generation.Output{}, fmt.Errorf("invalid configuration provenance: present=%t mode=%s environment=%q root=%q selected=%q", exists, provenance.Mode(), provenance.Environment(), provenance.RootPath(), provenance.SelectedPath())
	}
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
