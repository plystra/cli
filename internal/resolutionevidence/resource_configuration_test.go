package resolutionevidence_test

import (
	"bytes"
	"testing"

	"github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestResourceConfigurationEvidenceComposition(t *testing.T) {
	t.Parallel()
	const instance = `resources.instances["database.primary"]`
	for _, test := range []struct {
		name, root, overlay string
		sources             int
		oldConfig           bool
	}{
		{name: "config-only layers", root: `resources: {instances: {database.primary: {config: {host: ROOT_PRIVATE}}}}`, overlay: `resources: {instances: {database.primary: {config: {settings: {nested: {root: OVERLAY_PRIVATE}}}}}}`, sources: 4, oldConfig: true},
		{name: "unchanged provider", root: `resources: {instances: {database.primary: {use: example.com/acme/smtp.New}}}`, sources: 2, oldConfig: true},
		{name: "replacement", root: `resources: {instances: {database.primary: {use: example.com/acme/smtp.Other, config: {host: REPLACEMENT_PRIVATE}}}}`, sources: 1},
		{name: "replacement without config", root: `resources: {instances: {database.primary: {use: example.com/acme/smtp.Other}}}`},
		{name: "configuration removal", root: `resources: {instances: {database.primary: {config: {$remove: true}}}}`, sources: 1},
		{name: "instance removal", root: `resources: {instances: {database.primary: {$remove: true}}}`},
		{name: "revival after removal", root: `resources: {instances: {database.primary: {$remove: true}}}`, overlay: `resources: {instances: {database.primary: {use: example.com/acme/smtp.New, config: {host: REVIVED_PRIVATE}}}}`, sources: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseLookup := configurationSchemaLookup(t)
			original, err := constructorsymbol.Parse("example.com/acme/smtp.New")
			if err != nil {
				t.Fatal(err)
			}
			lookup := func(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
				if namespace != applicationmeta.ConfigurationNamespaceResource {
					return implementationinventory.Configuration{}, false
				}
				if symbol.String() == "example.com/acme/smtp.Other" {
					symbol = original
				}
				return baseLookup(applicationmeta.ConfigurationNamespaceImplementation, symbol)
			}
			dependencies := []applicationmeta.Dependency{
				{ModulePath: "example.com/oldest", ModuleVersion: "v1.0.0", Manifest: configurationManifest(t, "plystra.yaml", `resources:
  instances:
    database.primary:
      use: example.com/acme/smtp.New
      config:
        password: {env: PRIVATE_SECRET_TARGET}
        host: TEMPLATE_PRIVATE
  bind:
    implementations:
      example.com/app/service.New: {Database: database.primary}
    instances:
      view.main: {upstream: database.primary}
`)},
				{ModulePath: "example.com/nearest", ModuleVersion: "v1.0.0", Manifest: configurationManifest(t, "plystra.yaml", `resources: {instances: {database.primary: {config: {settings: {nested: {dependency: NEAREST_PRIVATE}}}}}}`)},
			}
			root := configurationManifest(t, "plystra.yaml", test.root)
			current := root
			mode, environment, selected := generation.ConfigurationModeDefault, "", "plystra.yaml"
			if test.overlay != "" {
				overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(test.overlay))
				if err != nil {
					t.Fatal(err)
				}
				current, err = applicationmeta.ApplyOverlay(root, overlay, lookup)
				if err != nil {
					t.Fatal(err)
				}
				mode, environment, selected = generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml"
			}
			composition, err := applicationmeta.Compose(dependencies, current, lookup)
			if err != nil {
				t.Fatal(err)
			}
			context, err := generation.NewContext(generation.Input{ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
				Mode: mode, Environment: environment, RootPath: "plystra.yaml", SelectedPath: selected,
				RootDigest: configurationDigest("1"), SelectedDigest: configurationDigest("1"), DependencyCompositionDigest: composition.DependencyDigest(),
			}})
			if err != nil {
				t.Fatal(err)
			}
			input := resolutionEvidenceInput(t, context, nil, nil)
			input.Modules = []resolutionevidence.ModuleInput{
				{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
				{Path: "example.com/oldest", Role: resolutionevidence.ModuleRoleDependency, SourceModulePath: "example.com/oldest", SelectedVersion: "v1.0.0"},
				{Path: "example.com/nearest", Role: resolutionevidence.ModuleRoleDependency, SourceModulePath: "example.com/nearest", SelectedVersion: "v1.0.0"},
			}
			input.Configuration = &resolutionevidence.ConfigurationInput{Templates: composition.TemplateLayers(), DependencyBaseline: composition.DependencyBaseline(), Effective: configurationDecisions(t, composition.Manifest(), lookup)}
			for i, layer := range composition.CurrentLayers() {
				owner := resolutionevidence.ConfigurationOwnerRoot
				if i > 0 {
					owner = resolutionevidence.ConfigurationOwnerEnvironment
				}
				input.Configuration.Layers = append(input.Configuration.Layers, resolutionevidence.ConfigurationLayerInput{Owner: owner, Decisions: configurationDecisions(t, layer, lookup)})
			}
			evidence, err := resolutionevidence.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			if !evidence.Valid() {
				t.Fatal("invalid evidence")
			}
			sources := evidence.ResourceConfigurationSources("database.primary")
			if len(sources) != test.sources {
				t.Fatalf("configuration sources = %#v; want %d", sources, test.sources)
			}
			field := configurationField(t, evidence, instance+`.config["password"]`)
			if field.Effective() != test.oldConfig {
				t.Fatalf("old provider config effective = %t", field.Effective())
			}
			for _, private := range []string{"PRIVATE_SECRET_TARGET", "TEMPLATE_PRIVATE", "NEAREST_PRIVATE", "ROOT_PRIVATE", "OVERLAY_PRIVATE", "REPLACEMENT_PRIVATE", "REVIVED_PRIVATE"} {
				if bytes.Contains(evidence.CanonicalJSON(), []byte(private)) {
					t.Fatalf("private value leaked: %s", private)
				}
			}
			if len(sources) > 0 {
				sources[0].Path = "changed.yaml"
				if evidence.ResourceConfigurationSources("database.primary")[0].Path == "changed.yaml" {
					t.Fatal("mutable sources escaped")
				}
			}
		})
	}
}
