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
	manifest, err := applicationmeta.ParseSource("deploy/selected.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/current")
	if err != nil {
		t.Fatal(err)
	}
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
		if binding.ParameterName() == "Database" && (binding.DeclarationSource().Line() != 10 || binding.DeclarationSource().Column() != 9 || binding.Target() != "database.primary") {
			t.Fatal("binding span/target lost")
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

func TestResourceClosedSchemaRejectsInvalidAndContainerRemovals(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		"resources: null", "resources: []", "resources: {$remove: true}", "resources: {PRIVATE_KEY: PRIVATE_VALUE}",
		"resources: {instances: null}", "resources: {instances: []}", "resources: {instances: {$remove: true}}",
		"resources: {instances: {Database: {}}}", "resources: {instances: {'database..primary': {}}}", "resources: {instances: {'database_primary': {}}}",
		"resources: {instances: {primary: null}}", "resources: {instances: {primary: []}}", "resources: {instances: {primary: {$remove: false}}}",
		"resources: {instances: {primary: {$remove: true, use: example.com/db.New}}}",
		"resources: {instances: {primary: {use: null}}}", "resources: {instances: {primary: {use: {$remove: true}}}}", "resources: {instances: {primary: {use: PRIVATE_VALUE}}}",
		"resources: {instances: {primary: {use: example.com/db.New, PRIVATE_KEY: PRIVATE_VALUE}}}", "resources: {instances: {primary: {config: null}}}",
		"resources: {instances: {primary: {config: {$remove: true, PRIVATE_KEY: PRIVATE_VALUE}}}}",
		"resources: {instances: {primary: {config: {PRIVATE_KEY: one, PRIVATE_KEY: two}}}}",
		"resources: {instances: {primary: {config: {value: !!null PRIVATE_VALUE}}}}",
		"resources: {instances: {primary: {}, primary: {}}}",
		"resources: {bind: null}", "resources: {bind: {$remove: true}}", "resources: {bind: {example.com/service.New: {database: primary}}}",
		"resources: {bind: {implementations: null}}", "resources: {bind: {instances: {$remove: true}}}",
		"resources: {bind: {implementations: {PRIVATE_VALUE: {database: primary}}}}",
		"resources: {bind: {implementations: {example.com/service.New: null}}}",
		"resources: {bind: {implementations: {example.com/service.New: {$remove: true}}}}",
		"resources: {bind: {instances: {primary: {$remove: true}}}}",
		"resources: {bind: {instances: {Primary: {database: primary}}}}",
		"resources: {bind: {instances: {primary: {'': primary}}}}", "resources: {bind: {instances: {primary: {_: primary}}}}",
		"resources: {bind: {instances: {primary: {for: primary}}}}", "resources: {bind: {instances: {primary: {'bad-name': primary}}}}",
		"resources: {bind: {instances: {primary: {database: null}}}}", "resources: {bind: {instances: {primary: {database: Primary}}}}",
		"resources: {bind: {instances: {primary: {database: {$remove: false}}}}}",
		"resources: {bind: {instances: {primary: {database: {$remove: true, PRIVATE_KEY: PRIVATE_VALUE}}}}}",
		"resources: {bind: {instances: {primary: {database: primary, database: primary}}}}",
	} {
		t.Run(entry, func(t *testing.T) {
			_, err := applicationmeta.ParseSource("deploy/selected.yaml", []byte(entry))
			var detail *applicationmeta.ResourceMetadataError
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !errors.As(err, &detail) || detail.Source().Path() != "deploy/selected.yaml" || detail.Source().Line() < 1 || detail.Source().Column() < 1 {
				t.Fatalf("invalid resource shape missing source/error: %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("invalid shape leaked values: %v", err)
			}
		})
	}
	for _, valid := range []string{"resources: {}", "resources: {instances: {}, bind: {implementations: {}, instances: {}}}", resourceDocument("{}"), resourceDocument("{config: {}}"), resourceDocument("{$remove: true}")} {
		if _, err := applicationmeta.Parse([]byte(valid)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResourcesComposeTemplatesCurrentAndEnvironmentWithTypedShapes(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Settings struct { First string; Second string }; Pointer *struct { First string; Second string }; Mapping map[string]string; List []string; Array [1]string; Token configuration.Secret")
	oldest := applicationmeta.Dependency{ModulePath: "example.com/oldest", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, resourceDocument("{use: "+resourceProvider+", config: {settings: {first: inherited, second: inherited}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited], token: {env: PRIVATE_TARGET}}}"))}
	for _, test := range []struct{ name, middle, root, overlay, want string }{
		{"fixed inherits", "{}", "{}", "{settings: {first: current}}", "{settings: {first: current, second: inherited}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited], token: {env: PRIVATE_TARGET}}"},
		{"atomic replaces", "{}", "{}", "{pointer: {first: current}, mapping: {}, list: [], array: [current]}", "{settings: {first: inherited, second: inherited}, pointer: {first: current}, mapping: {}, list: [], array: [current], token: {env: PRIVATE_TARGET}}"},
		{"null values", "{}", "{}", "{pointer: null, mapping: null, list: null}", "{settings: {first: inherited, second: inherited}, pointer: null, mapping: null, list: null, array: [inherited], token: {env: PRIVATE_TARGET}}"},
		{"field removed", "{}", "{settings: {first: {$remove: true}}}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited], token: {env: PRIVATE_TARGET}}"},
		{"object removed", "{}", "{settings: {$remove: true}}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited], token: {env: PRIVATE_TARGET}}"},
		{"template barrier", "{settings: {$remove: true}}", "{}", "{settings: {second: current}}", "{settings: {second: current}, pointer: {first: inherited, second: inherited}, mapping: {old: inherited}, list: [inherited], array: [inherited], token: {env: PRIVATE_TARGET}}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			middle := applicationmeta.Dependency{ModulePath: "example.com/nearest", Manifest: composeManifest(t, resourceDocument("{config: "+test.middle+"}"))}
			root := resourceManifest(t, "plystra.yaml", resourceDocument("{config: "+test.root+"}"))
			overlay := resourceManifest(t, "plystra.production.yaml", resourceDocument("{config: "+test.overlay+"}"))
			beforeRoot := root.ResourceInstances()[0].ConfigurationYAML()
			selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
			if err != nil {
				t.Fatal(err)
			}
			composition, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest, middle}, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			instance := resourceConfig(t, composition.Manifest(), "database.primary")
			if instance.Provider().String() != resourceProvider || !instance.HasConfiguration() {
				t.Fatal("inherited provider/config missing")
			}
			assertResourceYAML(t, instance, test.want)
			if instance.DeclarationSource().Path() != "plystra.production.yaml" || instance.DeclarationSource().ModulePath() != "example.com/current" {
				t.Fatal("current declaration ownership lost")
			}
			if instance.ProviderDeclarationSource().ModulePath() != "example.com/oldest" || instance.ProviderDeclarationSource().Path() != "plystra.yaml" || instance.ProviderSource() != `example.com/oldest@v1.0.0/plystra.yaml resources.instances["database.primary"].use` {
				t.Fatal("config-only update reassigned inherited provider selection")
			}
			layers := composition.CurrentLayers()
			if len(layers) != 2 || len(composition.TemplateLayers()) != 2 {
				t.Fatal("layer history missing")
			}
			for _, layer := range layers {
				decisions, err := applicationmeta.ConfigurationDecisions(layer, lookup)
				if err != nil {
					t.Fatal(err)
				}
				for _, decision := range decisions {
					if strings.HasSuffix(decision.Path(), ".use") {
						t.Fatal("inherited provider became local authored use")
					}
				}
			}
			layers[0] = applicationmeta.Manifest{}
			if len(composition.CurrentLayers()[0].ResourceInstances()) != 1 || !bytes.Equal(root.ResourceInstances()[0].ConfigurationYAML(), beforeRoot) {
				t.Fatal("composition mutated an input or exposed its layer storage")
			}
		})
	}
}

func TestResourceProviderReplacementAndRemovalBoundaries(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Shared string; Old string; Settings struct { First string; Second string }")
	oldest := applicationmeta.Dependency{ModulePath: "example.com/oldest", Manifest: composeManifest(t, resourceDocument("{use: "+resourceProvider+", config: {shared: inherited, old: inherited, settings: {first: inherited}}}"))}
	for _, test := range []struct {
		name, root, overlay, provider, want string
		hasConfig                           bool
	}{
		{"same provider", "{use: " + resourceProvider + ", config: {settings: {second: current}}}", "{}", resourceProvider, "{shared: inherited, old: inherited, settings: {first: inherited, second: current}}", true},
		{"changed provider", "{use: " + replacementResourceProvider + ", config: {shared: replacement}}", "{}", replacementResourceProvider, "{shared: replacement}", true},
		{"changed without config", "{use: " + replacementResourceProvider + "}", "{}", replacementResourceProvider, "", false},
		{"replace then switch back", "{use: " + replacementResourceProvider + ", config: {shared: replacement}}", "{use: " + resourceProvider + ", config: {settings: {second: current}}}", resourceProvider, "{settings: {second: current}}", true},
		{"config removed", "{config: {$remove: true}}", "{}", resourceProvider, "", false},
		{"config remove then recreate", "{config: {$remove: true}}", "{config: {settings: {second: current}}}", resourceProvider, "{settings: {second: current}}", true},
		{"instance remove then recreate", "{$remove: true}", "{use: " + resourceProvider + ", config: {shared: replacement}}", resourceProvider, "{shared: replacement}", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := resourceManifest(t, "plystra.yaml", resourceDocument(test.root))
			overlay := resourceManifest(t, "plystra.production.yaml", resourceDocument(test.overlay))
			selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
			if err != nil {
				t.Fatal(err)
			}
			composition, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest}, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			instance := resourceConfig(t, composition.Manifest(), "database.primary")
			if instance.Provider().String() != test.provider || instance.HasConfiguration() != test.hasConfig {
				t.Fatal("provider or presence boundary failed")
			}
			if test.hasConfig {
				assertResourceYAML(t, instance, test.want)
			} else if len(instance.ConfigurationYAML()) != 0 {
				t.Fatal("removed Config still present")
			}
			if test.name != "same provider" {
				for _, source := range composition.ResolutionSources() {
					if strings.HasPrefix(source.Path(), `resources.instances["database.primary"].config`) {
						t.Fatalf("discarded old config provenance survived: %s", source.Path())
					}
				}
			}
		})
	}
	for _, entry := range []string{"{}", "{config: {shared: value}}"} {
		if _, err := applicationmeta.Compose(nil, composeManifest(t, resourceDocument(entry)), lookup); err == nil {
			t.Fatal("missing provider accepted after composition")
		}
	}
}

func TestResourceBindingsComposeOnlyAtExactLeavesWithoutCascading(t *testing.T) {
	t.Parallel()
	base := composeManifest(t, `resources:
  instances:
    primary: {use: example.com/database.New}
    replica: {use: example.com/database.New}
  bind:
    implementations:
      example.com/service.New: {Database: primary, database: replica}
    instances:
      replica: {upstream: primary, cache: primary}
`)
	current := resourceManifest(t, "plystra.production.yaml", `resources:
  instances:
    primary: {$remove: true}
    absent: {$remove: true}
  bind:
    implementations:
      example.com/service.New: {database: primary, missing: {$remove: true}}
    instances:
      replica: {cache: {$remove: true}}
`)
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "example.com/template", Manifest: base}}, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	bindings := composition.Manifest().ResourceBindings()
	if len(composition.Manifest().ResourceInstances()) != 1 || len(bindings) != 3 {
		t.Fatal("instance removal cascaded or binding leaf removal lost")
	}
	for _, binding := range bindings {
		if binding.Target() != "primary" {
			t.Fatal("composition inferred another binding or lost leaf replacement")
		}
		if binding.ParameterName() == "database" {
			if binding.DeclarationSource().ModulePath() != "example.com/current" || binding.DeclarationSource().Path() != "plystra.production.yaml" {
				t.Fatal("replacement binding lost source")
			}
		} else if binding.DeclarationSource().ModulePath() != "example.com/template" || !strings.HasPrefix(binding.Source(), "example.com/template@workspace/") {
			t.Fatal("inherited binding lost source")
		}
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composition.CurrentLayers()[0], composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	var removed []string
	for _, decision := range decisions {
		if decision.Removed() {
			removed = append(removed, decision.Path())
		}
	}
	if len(removed) != 4 {
		t.Fatalf("absent and effective tombstone intent lost: %v", removed)
	}
}

func TestResourceRequirednessIsFinalAndPerInstance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, fields, config, missing string }{
		{"required no config", "Value string `plystra:\"required\"`", "", `["value"]`},
		{"required empty config", "Value string `plystra:\"required\"`", ", config: {}", `["value"]`},
		{"required zero", "Value int `plystra:\"required\"`", ", config: {value: 0}", ""},
		{"required nil", "Value *string `plystra:\"required\"`", ", config: {value: null}", ""},
		{"required removed", "Value string `plystra:\"required\"`", ", config: {value: {$remove: true}}", `["value"]`},
		{"required config removed", "Value string `plystra:\"required\"`", ", config: {$remove: true}", `["value"]`},
		{"private default", "Value string `plystra-default:\"PRIVATE_DEFAULT\"`", "", ""},
		{"implicit fixed", "Value struct { Child string `plystra:\"required\"` }", "", `["value"]["child"]`},
		{"implicit array", "Value [3]struct { Child string `plystra:\"required\"` }", "", `["value"]`},
		{"nil pointer", "Value *struct { Child string `plystra:\"required\"` }", ", config: {value: null}", ""},
		{"pointer replaces", "Value *struct { Child string `plystra:\"required\"` }", ", config: {value: {}}", `["value"]["child"]`},
		{"list element", "Value []struct { Child string `plystra:\"required\"` }", ", config: {value: [{}]}", `["value"]`},
		{"map element", "Value map[string]struct { Child string `plystra:\"required\"` }", ", config: {value: {PRIVATE_KEY: {}}}", `["value"]`},
		{"secret reference", "Value configuration.Secret `plystra:\"required\"`", ", config: {value: {env: PRIVATE_TARGET}}", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := resourceLookup(t, test.fields)
			current := resourceManifest(t, "deploy/selected.yaml", resourceDocument("{use: "+resourceProvider+test.config+"}"))
			composition, err := applicationmeta.Compose(nil, current, lookup)
			if err != nil {
				t.Fatalf("requiredness ran before final validation: %v", err)
			}
			before := composition.Manifest().ResourceInstances()[0].ConfigurationYAML()
			err = composition.ValidateRequiredConfiguration(lookup, nil, "deploy/selected.yaml")
			if test.missing == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var detail *applicationmeta.ResourceConfigurationError
				if !errors.Is(err, applicationmeta.ErrConfigurationRequired) || !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.As(err, &detail) {
					t.Fatalf("missing resource requiredness contract: %v", err)
				}
				if detail.Field() != `resources.instances["database.primary"].config`+test.missing || detail.InstanceName() != "database.primary" || detail.Provider().String() != resourceProvider || detail.ModulePath() != "example.com/current" || detail.SourcePath() != "deploy/selected.yaml" || detail.SourceKind() != "configuration-declaration" || detail.Line() != 1 || detail.Column() != 1 {
					t.Fatalf("required diagnostic lost instance/selected-document context: %v", err)
				}
				if strings.Contains(err.Error(), "PRIVATE_") {
					t.Fatal("required diagnostic leaked private data")
				}
			}
			if !bytes.Equal(before, composition.Manifest().ResourceInstances()[0].ConfigurationYAML()) {
				t.Fatal("requiredness inserted defaults or changed source")
			}
		})
	}
	lookup := resourceLookup(t, "Endpoint string `plystra:\"required\"`; Settings struct { Region string `plystra:\"required\"` }")
	base := applicationmeta.Dependency{ModulePath: "example.com/base", Manifest: composeManifest(t, resourceDocument("{use: "+resourceProvider+", config: {endpoint: PRIVATE_ENDPOINT}}"))}
	current := resourceManifest(t, "plystra.yaml", resourceDocument("{config: {settings: {region: PRIVATE_REGION}}}"))
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{base}, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.yaml"); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{
		"resources: {instances: {database.replica: {use: " + resourceProvider + "}}}",
		resourceDocument("{use: " + replacementResourceProvider + ", config: {settings: {region: PRIVATE_REGION}}}"),
	} {
		selected, err := applicationmeta.ApplyOverlay(current, composeManifest(t, extra), lookup)
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose([]applicationmeta.Dependency{base}, selected, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml"); !errors.Is(err, applicationmeta.ErrConfigurationRequired) {
			t.Fatalf("requiredness collapsed instances or inherited across replacement: %v", err)
		}
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
		{"atomic removal", resourceProvider, "{pointer: {count: {$remove: true}}}", `["pointer"]`, applicationmeta.ErrConfigurationInvalidValue},
		{"map removal", resourceProvider, "{labels: {PRIVATE_KEY: {$remove: true}}}", `["labels"]`, applicationmeta.ErrConfigurationInvalidValue},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := "resources:\n  instances:\n    database.primary:\n      use: " + test.use + "\n      config: " + test.config + "\n"
			for _, inherited := range []bool{false, true} {
				current := resourceManifest(t, "deploy/selected.yaml", data)
				var dependencies []applicationmeta.Dependency
				if inherited {
					dependencies = []applicationmeta.Dependency{{ModulePath: "example.com/template", Manifest: composeManifest(t, data)}}
					current = resourceManifest(t, "deploy/selected.yaml", "{}")
				}
				_, err := applicationmeta.Compose(dependencies, current, lookup)
				var detail *applicationmeta.ResourceConfigurationError
				if !errors.Is(err, test.want) || !errors.As(err, &detail) {
					t.Fatalf("missing Config failure class: %v", err)
				}
				module, path := "example.com/current", "deploy/selected.yaml"
				if inherited {
					module, path = "example.com/template", "plystra.yaml"
				}
				if detail.InstanceName() != "database.primary" || detail.Provider().String() != test.use || detail.Field() != `resources.instances["database.primary"].config`+test.field || detail.ModulePath() != module || detail.SourcePath() != path || detail.Line() != 5 || detail.Column() != 7 {
					t.Fatalf("wrong resource failure context: %v %#v", err, detail.Source())
				}
				if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, detail), "PRIVATE_") {
					t.Fatal("error formatting leaked private values or keys")
				}
			}
		})
	}
}

func TestResourceEvidenceRedactsPrivateValuesAndDefaults(t *testing.T) {
	t.Parallel()
	makeLookup := func(value string) applicationmeta.SchemaLookup {
		return resourceLookup(t, "Private string `plystra-default:\""+value+"\"`; Public string `plystra:\"build-visible\"`; Password configuration.Secret; Settings struct { First string; Second string }")
	}
	lookup := makeLookup("PRIVATE_DEFAULT_ONE")
	first := composeManifest(t, resourceDocument("{use: "+resourceProvider+", config: {private: PRIVATE_ONE, public: stable, password: {env: PRIVATE_ENV}}}"))
	second := composeManifest(t, resourceDocument("{config: {password: {file: /PRIVATE_FILE}, public: stable, private: PRIVATE_TWO}, use: "+resourceProvider+"}"))
	for _, manifest := range []applicationmeta.Manifest{first, second} {
		if _, err := applicationmeta.ConfigurationDecisions(manifest, lookup); err != nil {
			t.Fatal(err)
		}
	}
	digest := func(manifest applicationmeta.Manifest, lookup applicationmeta.SchemaLookup) string {
		t.Helper()
		result, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if digest(first, lookup) != digest(second, lookup) || digest(first, lookup) != digest(first, makeLookup("PRIVATE_DEFAULT_TWO")) {
		t.Fatal("private value, Secret kind/target, or private default changed public identity")
	}
	for _, document := range []string{
		resourceDocument("{use: " + resourceProvider + ", config: {private: PRIVATE_ONE, public: changed, password: {env: PRIVATE_ENV}}}"),
		resourceDocument("{use: " + replacementResourceProvider + ", config: {private: PRIVATE_ONE, public: stable, password: {env: PRIVATE_ENV}}}"),
		"resources: {instances: {database.replica: {use: " + resourceProvider + ", config: {private: PRIVATE_ONE, public: stable, password: {env: PRIVATE_ENV}}}}}",
	} {
		if digest(first, lookup) == digest(composeManifest(t, document), lookup) {
			t.Fatal("build-visible value, provider, or instance name lost from identity")
		}
	}
	dependencies := []applicationmeta.Dependency{{ModulePath: "example.com/base", Manifest: first}}
	current := resourceManifest(t, "plystra.yaml", resourceDocument("{config: {private: PRIVATE_ONE}}"))
	composition, err := applicationmeta.Compose(dependencies, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range composition.ResolutionSources() {
		if record.Path() == `resources.instances["database.primary"].config["private"]` {
			t.Fatal("equal locally authored value remained inherited")
		}
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composition.CurrentLayers()[0], lookup)
	if err != nil {
		t.Fatal(err)
	}
	output := fmt.Sprintf("%v %+v %#v %v %v", composition, composition.TemplateLayers(), composition.Provenance(), decisions, composition.ResolutionSources())
	var log bytes.Buffer
	slog.New(slog.NewJSONHandler(&log, nil)).Info("configuration", "composition", composition)
	output += fmt.Sprintf("%+v %#v %q %s", composition, composition, composition, composition) + log.String()
	// Composition, like its embedded Manifest values, must never expose private bytes.
	if strings.Contains(output, "PRIVATE_") || strings.Contains(output, strings.Trim(fmt.Sprint([]byte("PRIVATE_ONE")), "[]")) {
		t.Fatal("evidence contains private material")
	}
	data := []byte("# retained comment\n" + resourceDocument("{config: {private: PRIVATE_ONE}}") + "\n")
	before := append([]byte(nil), data...)
	maintenance, err := applicationmeta.MaintainDependencyConfiguration(data, composition.DependencyBaseline(), nil, dependencies, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.Changed() || !bytes.Equal(maintenance.Data(), data) || !bytes.Equal(data, before) {
		t.Fatal("maintenance materialized template values or mutated authored intent")
	}
	if !containsResourcePath(maintenance.LocalPaths(), `resources.instances["database.primary"].config["private"]`) || containsResourcePath(maintenance.LocalPaths(), `resources.instances["database.primary"].use`) {
		t.Fatalf("incorrect local Resource paths: %v", maintenance.LocalPaths())
	}
	for _, unresolved := range []string{"PRIVATE_ONE", "PRIVATE_TWO"} {
		manifest := composeManifest(t, resourceDocument("{config: {PRIVATE_KEY: "+unresolved+"}}"))
		if _, err := applicationmeta.ConfigurationDecisions(manifest, lookup); !errors.Is(err, applicationmeta.ErrConfigurationSchema) {
			t.Fatalf("unbound delta fabricated schema: %v", err)
		}
		if digest(manifest, lookup) != digest(composeManifest(t, resourceDocument("{config: {OTHER_PRIVATE_KEY: hidden}}")), lookup) {
			t.Fatal("unvalidated excluded delta exposed keys/values")
		}
	}
}

func containsResourcePath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func TestResourceProviderSourcesAndDistinctSchemas(t *testing.T) {
	t.Parallel()
	lookup := namespacedSchemaLookup(applicationmeta.ConfigurationNamespaceResource, map[string]implementationinventory.Configuration{
		resourceProvider:            composeSchema(t, "Shared int; Old string"),
		replacementResourceProvider: composeSchema(t, "Shared string; Current string"),
	})
	oldest := applicationmeta.Dependency{ModulePath: "example.com/oldest", Manifest: composeManifest(t, "resources:\n  instances:\n    database.primary:\n      use: "+resourceProvider+"\n      config: {shared: 1, old: old-provider}\n    database.replica:\n      use: "+resourceProvider+"\n      config: {shared: 2}\n")}
	nearest := applicationmeta.Dependency{ModulePath: "example.com/nearest", ModuleVersion: "v1.2.3", Manifest: composeManifest(t, "resources:\n  instances:\n    database.primary:\n      use: "+replacementResourceProvider+"\n      config: {shared: new-provider}\n")}
	root := resourceManifest(t, "plystra.yaml", resourceDocument("{config: {current: root}}"))
	overlay := resourceManifest(t, "plystra.production.yaml", resourceDocument("{config: {current: environment}}"))
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest, nearest}, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	instance := resourceConfig(t, composition.Manifest(), "database.primary")
	assertResourceYAML(t, instance, "{shared: new-provider, current: environment}")
	assertResourceYAML(t, resourceConfig(t, composition.Manifest(), "database.replica"), "{shared: 2}")
	provider := instance.ProviderDeclarationSource()
	if provider.ModulePath() != nearest.ModulePath || provider.Path() != "plystra.yaml" || provider.Line() != 4 || provider.Column() != 7 || instance.ProviderSource() != `example.com/nearest@v1.2.3/plystra.yaml resources.instances["database.primary"].use` {
		t.Fatal("provider selection lost exact inherited use span")
	}
	if instance.DeclarationSource().ModulePath() != "example.com/current" || instance.DeclarationSource().Path() != "plystra.production.yaml" {
		t.Fatal("instance entry lost highest source")
	}
	for _, record := range composition.ResolutionSources() {
		if strings.HasPrefix(record.Path(), `resources.instances["database.primary"]`) {
			for _, source := range record.Sources() {
				if strings.Contains(source, oldest.ModulePath) {
					t.Fatal("replaced provider retained an old contributing source")
				}
			}
		}
	}
	if len(composition.TemplateLayers()[0].Decisions) == 0 {
		t.Fatal("replacement deleted historical template evidence")
	}
	same := resourceManifest(t, "deploy/selected.yaml", resourceDocument("{use: "+replacementResourceProvider+"}"))
	composition, err = applicationmeta.Compose([]applicationmeta.Dependency{oldest, nearest}, same, lookup)
	if err != nil {
		t.Fatal(err)
	}
	instance = resourceConfig(t, composition.Manifest(), "database.primary")
	assertResourceYAML(t, instance, "{shared: new-provider}")
	if instance.ProviderDeclarationSource().ModulePath() != "example.com/current" || !strings.HasPrefix(instance.ProviderSource(), "deploy/selected.yaml ") {
		t.Fatal("equal explicit provider choice remained inherited")
	}
	for _, invalid := range []string{"{shared: 1}", "{old: PRIVATE_VALUE}"} {
		_, err := applicationmeta.Compose([]applicationmeta.Dependency{oldest, nearest}, resourceManifest(t, "plystra.yaml", resourceDocument("{config: "+invalid+"}")), lookup)
		if !errors.Is(err, applicationmeta.ErrConfigurationValues) {
			t.Fatalf("config-only delta used old provider schema: %v", err)
		}
	}
}

func TestResourceMissingProviderRequirednessWaitsForFinalComposition(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string `plystra:\"required\"`")
	root := resourceManifest(t, "plystra.yaml", resourceDocument("{config: {PRIVATE_UNBOUND_KEY: PRIVATE_UNBOUND_VALUE}}"))
	for _, entry := range []string{"{$remove: true}", "{use: " + resourceProvider + ", config: {value: final}}"} {
		selected, err := applicationmeta.ApplyOverlay(root, resourceManifest(t, "plystra.production.yaml", resourceDocument(entry)), lookup)
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose(nil, selected, lookup)
		if err != nil {
			t.Fatalf("required provider checked before final composition: %v", err)
		}
		if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml"); err != nil {
			t.Fatal(err)
		}
		for _, layer := range composition.CurrentLayers() {
			decisions, err := applicationmeta.ConfigurationDecisions(layer, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(decisions), "PRIVATE_") {
				t.Fatal("unbound historical layer leaked private keys or values")
			}
		}
	}
	_, err := applicationmeta.Compose(nil, root, lookup)
	var missing *applicationmeta.ResourceMetadataError
	if !errors.As(err, &missing) || missing.Field() != `resources.instances["database.primary"].use` {
		t.Fatalf("missing final provider not identified: %v", err)
	}
	// An available lower schema must still validate even if a later provider replaces it.
	invalid := resourceManifest(t, "plystra.yaml", resourceDocument("{use: "+resourceProvider+", config: {value: 1}}"))
	upper := resourceManifest(t, "plystra.production.yaml", resourceDocument("{use: "+replacementResourceProvider+", config: {value: final}}"))
	selected, err := applicationmeta.ApplyOverlay(invalid, upper, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applicationmeta.Compose(nil, selected, lookup); !errors.Is(err, applicationmeta.ErrConfigurationValues) {
		t.Fatalf("replacement bypassed validation of a typed lower layer: %v", err)
	}
}

func FuzzResourceCompositionDeterminism(f *testing.F) {
	lookup := resourceLookup(f, "Value string; Settings struct { First string; Second string }; Pointer *struct { First string; Second string }; Token configuration.Secret")
	for _, seed := range [][2]string{
		{"{config: {settings: {first: current}}}", "{config: {settings: {second: overlay}}}"},
		{"{config: {settings: {$remove: true}}}", "{config: {settings: {second: overlay}}}"},
		{"{$remove: true}", "{use: " + resourceProvider + ", config: {value: new}}"},
		{"{use: " + replacementResourceProvider + "}", "{config: {value: other}}"},
		{"{config: {pointer: null}}", "{config: {token: {env: PRIVATE_TARGET}}}"},
	} {
		f.Add(seed[0], seed[1])
	}
	base := applicationmeta.Dependency{ModulePath: "example.com/template", Manifest: composeManifest(f, resourceDocument("{use: "+resourceProvider+", config: {settings: {first: inherited, second: inherited}}}"))}
	f.Fuzz(func(t *testing.T, lower, upper string) {
		if len(lower)+len(upper) > 8192 {
			return
		}
		root, err := applicationmeta.Parse([]byte(resourceDocument(lower)))
		if err != nil {
			return
		}
		overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(resourceDocument(upper)))
		if err != nil {
			return
		}
		selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
		if err != nil {
			return
		}
		first, firstErr := applicationmeta.Compose([]applicationmeta.Dependency{base}, selected, lookup)
		second, secondErr := applicationmeta.Compose([]applicationmeta.Dependency{base}, selected, lookup)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatal("composition success changed on repeat")
		}
		if firstErr != nil {
			if firstErr.Error() != secondErr.Error() {
				t.Fatal("composition failure was nondeterministic")
			}
			return
		}
		if first.DependencyDigest() != second.DependencyDigest() || !reflect.DeepEqual(first.ResolutionSources(), second.ResolutionSources()) || !reflect.DeepEqual(first.Manifest().ResourceInstances(), second.Manifest().ResourceInstances()) {
			t.Fatal("Resource normalization or evidence was nondeterministic")
		}
		for _, instance := range first.Manifest().ResourceInstances() {
			var document yaml.Node
			if err := yaml.Unmarshal(instance.ConfigurationYAML(), &document); err != nil {
				t.Fatal(err)
			}
			var check func(*yaml.Node)
			check = func(node *yaml.Node) {
				if node.Kind == yaml.MappingNode && len(node.Content) == 2 && node.Content[0].Value == "$remove" {
					t.Fatal("tombstone escaped to provider configuration")
				}
				for _, child := range node.Content {
					check(child)
				}
			}
			check(&document)
		}
	})
}

func TestResourceReplacementDocumentAndPrivateTemplateBaseline(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "First string; Second string")
	input := []byte("template: example.com/ancestor\nhttp: {address: ':9000'}\ntimeouts: {startup: 1s}\nresources: {instances: {database.primary: {use: " + resourceProvider + ", config: {first: PRIVATE_TEMPLATE, second: PRIVATE_TEMPLATE}}}, bind: {implementations: {example.com/service.New: {database: database.primary}}}}\n")
	before := append([]byte(nil), input...)
	private, err := applicationmeta.PrivateTemplateYAML(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(private, []byte("PRIVATE_TEMPLATE")) || !bytes.Contains(private, []byte("implementations:")) || bytes.Contains(private, []byte("template:")) || bytes.Contains(private, []byte("address:")) || bytes.Contains(private, []byte("timeouts:")) {
		t.Fatal("private baseline lost Resource inputs or retained process ownership")
	}
	repeated, err := applicationmeta.PrivateTemplateYAML(private)
	if err != nil || !bytes.Equal(repeated, private) || !bytes.Equal(input, before) {
		t.Fatal("private baseline is not immutable and idempotent")
	}
	base := applicationmeta.Dependency{ModulePath: "example.com/base", Manifest: composeManifest(t, string(private))}
	root := resourceManifest(t, "plystra.yaml", "template: example.com/base\n"+resourceDocument("{config: {first: PRIVATE_ROOT}}"))
	replacement := resourceManifest(t, "deploy/replacement.yaml", resourceDocument("{config: {second: PRIVATE_REPLACEMENT}}"))
	selected := applicationmeta.WithRootMetadata(replacement, root)
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{base}, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	assertResourceYAML(t, resourceConfig(t, composition.Manifest(), "database.primary"), "{first: PRIVATE_TEMPLATE, second: PRIVATE_REPLACEMENT}")
	if composition.Manifest().Template() != "example.com/base" || len(composition.CurrentLayers()) != 1 {
		t.Fatal("replacement changed root identity or included root values")
	}
	layer := composition.CurrentLayers()[0]
	decisions, err := applicationmeta.ConfigurationDecisions(layer, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if strings.HasPrefix(decision.Path(), "resources.") && !strings.HasPrefix(decision.Source(), "deploy/replacement.yaml ") {
			t.Fatal("replacement evidence has wrong owner")
		}
	}
}
