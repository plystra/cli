package applicationmeta_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

const resourceProvider = "example.com/database.New"
const replacementResourceProvider = "example.com/alternative.New"

func resourceManifest(t testing.TB, source, data string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.ParseSource(source, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/current")
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func resourceOverlayManifest(t testing.TB, source, data string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.ParseOverlaySource(source, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/current")
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func resourceDocument(entry string) string {
	return "resources: {instances: {database.primary: " + entry + "}}"
}

func resourceLookup(t testing.TB, fields string) applicationmeta.SchemaLookup {
	t.Helper()
	return namespacedSchemaLookup(applicationmeta.ConfigurationNamespaceResource, map[string]implementationinventory.Configuration{
		resourceProvider:            composeSchema(t, fields),
		replacementResourceProvider: composeSchema(t, fields),
	})
}

func resourceConfig(t testing.TB, manifest applicationmeta.Manifest, name string) applicationmeta.ResourceInstance {
	t.Helper()
	for _, instance := range manifest.ResourceInstances() {
		if instance.Name() == name {
			return instance
		}
	}
	t.Fatalf("missing Resource instance %s", name)
	return applicationmeta.ResourceInstance{}
}

func assertResourceYAML(t testing.TB, instance applicationmeta.ResourceInstance, expected string) {
	t.Helper()
	var got, want any
	if err := yaml.Unmarshal(instance.ConfigurationYAML(), &got); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resource configuration differs: got %s, want %s", instance.ConfigurationYAML(), expected)
	}
	if bytes.Contains(instance.ConfigurationYAML(), []byte("$remove")) {
		t.Fatal("tombstone became a provider value")
	}
}

func TestResourceParserRetainsExactBindingsSourcesAndPrivacy(t *testing.T) {
	t.Parallel()
	data := []byte("resources:\n  instances:\n    database.primary:\n      use: " + resourceProvider + "\n      config: {password: {env: PRIVATE_TARGET}}\n    database.replica: {use: " + resourceProvider + "}\n  bind:\n    implementations:\n      example.com/service.New:\n        Database: database.primary\n        database: database.replica\n        _cache: database.primary\n        \u6570\u636e\u5e93: database.primary\n    instances:\n      database.replica:\n        upstream: database.primary\n")
	before := append([]byte(nil), data...)
	manifest := resourceManifest(t, "deploy/selected.yaml", string(data))
	instances := manifest.ResourceInstances()
	if len(instances) != 2 || instances[0].Name() != "database.primary" || instances[0].Provider().String() != resourceProvider || !instances[0].HasConfiguration() || instances[1].HasConfiguration() {
		t.Fatal("instance identity/configuration presence lost")
	}
	location := instances[0].DeclarationSource()
	if location.ModulePath() != "example.com/current" || location.Path() != "deploy/selected.yaml" || location.Line() != 3 || location.Column() != 5 || instances[0].Source() != `deploy/selected.yaml resources.instances["database.primary"]` {
		t.Fatalf("instance source = %#v, %s", location, instances[0].Source())
	}
	providerLocation := instances[0].ProviderDeclarationSource()
	if providerLocation.ModulePath() != "example.com/current" || providerLocation.Path() != "deploy/selected.yaml" || providerLocation.Line() != 4 || providerLocation.Column() != 7 || instances[0].ProviderSource() != `deploy/selected.yaml resources.instances["database.primary"].use` {
		t.Fatalf("provider source = %#v, %s", providerLocation, instances[0].ProviderSource())
	}
	bindings := manifest.ResourceBindings()
	if len(bindings) != 5 {
		t.Fatal("binding parameters collapsed")
	}
	var parameters []string
	for _, binding := range bindings {
		if binding.Namespace() == "implementations" {
			parameters = append(parameters, binding.ParameterName())
			if binding.Consumer() != "example.com/service.New" || binding.DeclarationSource().ModulePath() != "example.com/current" {
				t.Fatal("binding consumer/source lost")
			}
		}
	}
	if !reflect.DeepEqual(parameters, []string{"Database", "_cache", "database", "\u6570\u636e\u5e93"}) {
		t.Fatalf("parameter identities = %#v", parameters)
	}
	private := instances[0].ConfigurationYAML()
	private[0] = '!'
	instances[0] = applicationmeta.ResourceInstance{}
	bindings[0] = applicationmeta.ResourceBinding{}
	if manifest.ResourceInstances()[0].Name() == "" || manifest.ResourceInstances()[0].ConfigurationYAML()[0] == '!' || manifest.ResourceBindings()[0].Target() == "" {
		t.Fatal("mutable public slice/YAML")
	}
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("instance", "value", manifest.ResourceInstances()[0], "manifest", manifest)
	encoded, err := json.Marshal(manifest.ResourceInstances())
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{fmt.Sprintf("%v %+v %#v %s %q", manifest, manifest.ResourceInstances(), manifest.ResourceInstances(), manifest.ResourceInstances()[0], manifest.ResourceInstances()[0]), log.String(), string(encoded)} {
		if strings.Contains(output, "PRIVATE_TARGET") || strings.Contains(output, "password") {
			t.Fatal("configuration leaked through formatting")
		}
	}
	if !bytes.Equal(data, before) {
		t.Fatal("parsing changed source bytes")
	}
}

func TestResourcesComposeCurrentAndEnvironmentIgnoresDependencies(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Settings struct { First string; Second string }")
	root := resourceManifest(t, "plystra.yaml", resourceDocument("{use: "+resourceProvider+", config: {settings: {first: root, second: root}}}"))
	overlay := resourceOverlayManifest(t, "plystra.production.yaml", resourceDocument("{config: {settings: {second: overlay}}}"))
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	dependency := applicationmeta.Dependency{
		ModulePath: "example.com/ordinary-dependency",
		Manifest:   resourceManifest(t, "plystra.yaml", resourceDocument("{use: "+resourceProvider+", config: {settings: {first: dependency}}}")),
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{dependency}, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	assertResourceYAML(t, resourceConfig(t, composition.Manifest(), "database.primary"), "{settings: {first: root, second: overlay}}")
	if len(composition.CurrentLayers()) != 2 {
		t.Fatalf("current layer count = %d, want 2", len(composition.CurrentLayers()))
	}
	for _, source := range composition.ResolutionSources() {
		for _, reference := range source.Sources() {
			if strings.Contains(reference, "ordinary-dependency") {
				t.Fatalf("ordinary dependency configuration entered provenance: %v", source)
			}
		}
	}
}

func TestResourceProviderReplacementAndRemovalBoundaries(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Shared string; Settings struct { First string; Second string }")
	root := resourceManifest(t, "plystra.yaml", resourceDocument("{use: "+resourceProvider+", config: {shared: root, settings: {first: root}}}"))
	for _, test := range []struct {
		name, overlay, wantProvider, wantConfig string
	}{
		{"same provider", resourceDocument("{config: {settings: {second: overlay}}}"), resourceProvider, "{shared: root, settings: {first: root, second: overlay}}"},
		{"changed provider", resourceDocument("{use: " + replacementResourceProvider + ", config: {shared: replacement}}"), replacementResourceProvider, "{shared: replacement}"},
		{"removed instance", resourceDocument("{$remove: true}"), "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			overlay := resourceOverlayManifest(t, "plystra.production.yaml", test.overlay)
			selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
			if err != nil {
				t.Fatal(err)
			}
			composition, err := applicationmeta.Compose(nil, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			instances := composition.Manifest().ResourceInstances()
			if test.wantProvider == "" {
				if len(instances) != 0 {
					t.Fatal("removed instance remained effective")
				}
				return
			}
			instance := resourceConfig(t, composition.Manifest(), "database.primary")
			if instance.Provider().String() != test.wantProvider {
				t.Fatalf("provider = %s, want %s", instance.Provider(), test.wantProvider)
			}
			assertResourceYAML(t, instance, test.wantConfig)
		})
	}
	if _, err := applicationmeta.ParseSource("plystra.yaml", []byte(resourceDocument("{$remove: true}"))); err == nil {
		t.Fatal("root accepted an overlay-only Resource tombstone")
	}
}

func TestResourceRootAndReplacementRejectOverlayRemovalForms(t *testing.T) {
	for _, document := range []string{
		resourceDocument("{$remove: true}"),
		"interfaces: {require: {remove: [example.com/missing/v1]}}",
	} {
		if _, err := applicationmeta.ParseSource("plystra.yaml", []byte(document)); err == nil {
			t.Fatalf("root accepted overlay-only form: %s", document)
		}
		if _, err := applicationmeta.ParseCompleteSource("deploy/replacement.yaml", []byte(document)); err == nil {
			t.Fatalf("replacement accepted overlay-only form: %s", document)
		}
	}
	if _, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(resourceDocument("{$remove: true}"))); err != nil {
		t.Fatalf("overlay rejected a valid Resource tombstone: %v", err)
	}
}

func TestResourceConfigurationErrorsUseInstanceAndOwningSource(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Count int8; Password configuration.Secret; Pointer *struct { Count int }; Labels map[string]string")
	for _, test := range []struct {
		name, use, config, field string
		want                     error
	}{
		{"unknown schema", "example.com/absent.New", "{}", "", applicationmeta.ErrConfigurationSchema},
		{"unknown field", resourceProvider, "{PRIVATE_KEY: PRIVATE_VALUE}", "", applicationmeta.ErrConfigurationUnknownField},
		{"integer overflow", resourceProvider, "{count: 128}", `["count"]`, applicationmeta.ErrConfigurationInvalidValue},
		{"invalid secret", resourceProvider, "{password: {env: 'PRIVATE INVALID'}}", `["password"]`, applicationmeta.ErrConfigurationInvalidValue},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := "resources:\n  instances:\n    database.primary:\n      use: " + test.use + "\n      config: " + test.config + "\n"
			_, err := applicationmeta.Compose(nil, resourceManifest(t, "deploy/selected.yaml", data), lookup)
			var detail *applicationmeta.ResourceConfigurationError
			if !errors.Is(err, test.want) || !errors.As(err, &detail) {
				t.Fatalf("missing Config failure class: %v", err)
			}
			if detail.InstanceName() != "database.primary" || detail.Provider().String() != test.use || detail.Field() != `resources.instances["database.primary"].config`+test.field || detail.ModulePath() != "example.com/current" || detail.SourcePath() != "deploy/selected.yaml" {
				t.Fatalf("wrong resource failure context: %v", err)
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, detail), "PRIVATE_") {
				t.Fatal("error formatting leaked private values or keys")
			}
		})
	}
}
