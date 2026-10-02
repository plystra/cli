package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

func TestConstructorConfigurationNullRequiresNullableCompiledType(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		typeName string
		nullable bool
	}{
		{"*string", true}, {"**string", true}, {"*struct { Value string }", true},
		{"[]string", true}, {"map[string]string", true},
		{"[0]string", false}, {"[1]string", false}, {"string", false},
		{"bool", false}, {"int", false}, {"float64", false},
		{"time.Duration", false}, {"url.URL", false}, {"configuration.Secret", false},
		{"struct { Value string }", false},
	} {
		t.Run(test.typeName, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, "Field "+test.typeName),
			})
			removed := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {field: {$remove: true}}}\n")
			removedDecisions, err := applicationmeta.ConfigurationDecisions(removed, lookup)
			if err != nil || len(removedDecisions) != 2 || !removedDecisions[1].Removed() {
				t.Fatalf("declared field rejected removal: %v, %v", removedDecisions, err)
			}
			var nilDigest string
			for _, spelling := range []string{"null", "~", ""} {
				current := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {field: "+spelling+"}}\n")
				composed, err := applicationmeta.Compose(nil, current, lookup)
				if !test.nullable {
					if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) {
						t.Fatalf("non-nullable type accepted null %q: %v", spelling, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				configured, exists := composed.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
				if !exists || strings.TrimSpace(string(configured.YAML())) != "field: null" {
					t.Fatalf("literal nil was not retained for %s: %s", test.typeName, configured.YAML())
				}
				decisions, err := applicationmeta.ConfigurationDecisions(current, lookup)
				if err != nil || len(decisions) != 2 || decisions[1].Removed() {
					t.Fatalf("literal nil became removal intent: %v, %v", decisions, err)
				}
				if decisions[1].Digest() == removedDecisions[1].Digest() || nilDigest != "" && nilDigest != decisions[1].Digest() {
					t.Fatal("nil spelling changed identity or collided with removal")
				}
				nilDigest = decisions[1].Digest()
			}
		})
	}
}

func TestConstructorConfigurationSeparatesNestedRemovalFromLiteralNil(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, `
		Text string
		Settings struct { Keep string; Remove string }
		Pointer *string
		Items []string
		Labels map[string]string
		Token configuration.Secret
		`),
	})
	lower := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {text: lower, settings: {keep: retained, remove: lower}, pointer: lower, items: [lower], labels: {lower: value}, token: {env: PRIVATE_TOKEN}}}\n")
	upper := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {text: {$remove: true}, settings: {remove: {$remove: true}}, pointer: null, items: null, labels: null, token: {$remove: true}}}\n")
	selected, err := applicationmeta.ApplyOverlay(lower, upper, lookup)
	if err != nil {
		t.Fatal(err)
	}
	selectedConfig, _ := selected.Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	if bytes.Count(selectedConfig.YAML(), []byte("$remove: true")) != 3 {
		t.Fatalf("overlay did not preserve nested tombstones: %s", selectedConfig.YAML())
	}
	composed, err := applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "example.com/platform", ExportName: "defaults", Manifest: lower}}, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	configured, exists := composed.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	var got map[string]any
	if !exists {
		t.Fatal("composed configuration is absent")
	}
	if err := yaml.Unmarshal(configured.YAML(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"settings": map[string]any{"keep": "retained"}, "pointer": nil, "items": nil, "labels": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composed removal and nil values = %#v, want %#v", got, want)
	}
}

func TestConstructorAtomicValuesRejectReservedRemovalMappings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ typeName, value string }{
		{"map[string]bool", "{$remove: false}"},
		{"struct { Name string }", "{$remove: false}"},
		{"*string", "{$remove: null}"},
		{"[]string", "{$remove: 1}"},
		{"map[string]bool", `{$remove: "true"}`},
		{"map[string]string", "{$remove: private-value}"},
		{"*struct { Name string }", "{name: {$remove: true}}"},
		{"[]map[string]bool", "[{$remove: true}]"},
		{"[1]map[string]bool", "[{$remove: true}]"},
		{"map[string]map[string]bool", "{entry: {$remove: true}}"},
		{"string", "{$remove: true, private_key: private_value}"},
	} {
		t.Run(test.typeName+test.value, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, "Field "+test.typeName),
			})
			_, err := applicationmeta.Compose(nil, composeManifest(t, "config: {"+constructorConfigurationSymbol+": {field: "+test.value+"}}\n"), lookup)
			if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || strings.Contains(err.Error(), "private_") || strings.Contains(err.Error(), "private-") {
				t.Fatalf("invalid nested marker accepted or exposed: %v", err)
			}
		})
	}
}

func TestConstructorAtomicValuesPreserveNestedNilAndEmptyCollections(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, `
		Pointer *struct { Items []string; Labels map[string]string; Next *string }
		Items []map[string]string
		Labels map[string][]string
		Literal map[string]bool
		`),
	})
	current := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {pointer: {items: null, labels: {}, next: null}, items: [null, {}], labels: {nil: null, empty: []}, literal: {$remove: true, keep: false}}}\n")
	composed, err := applicationmeta.Compose(nil, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	configured, _ := composed.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	var got map[string]any
	if err := yaml.Unmarshal(configured.YAML(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"pointer": map[string]any{"items": nil, "labels": map[string]any{}, "next": nil},
		"items":   []any{nil, map[string]any{}},
		"labels":  map[string]any{"nil": nil, "empty": []any{}},
		"literal": map[string]any{"$remove": true, "keep": false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("atomic typed values = %#v, want %#v", got, want)
	}
}

func TestNestedConstructorTombstonesRemainAuthoredAcrossDependencyChanges(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Settings struct { Removed string }; Optional *string"),
	})
	data := []byte("config: {" + constructorConfigurationSymbol + ": {settings: {removed: {$remove: true}}, optional: null}}\n")
	current := composeManifest(t, string(data))
	before, err := applicationmeta.Compose(nil, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "changed"} {
		dependencies := []applicationmeta.Dependency{{ModulePath: "example.com/platform", ExportName: "defaults", Manifest: composeManifest(t, fmt.Sprintf("config: {%s: {settings: {removed: %s}, optional: %s}}\n", constructorConfigurationSymbol, value, value))}}
		maintained, err := applicationmeta.MaintainDependencyConfiguration(data, before.DependencyBaseline(), nil, dependencies, lookup)
		if err != nil || maintained.Changed() || !bytes.Equal(maintained.Data(), data) {
			t.Fatalf("maintenance changed nested exclusion or nil: %v\n%s", err, maintained.Data())
		}
	}
}

func TestConstructorNullableExportsDeduplicateAndConflictWithEmptyValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ typeName, empty string }{
		{"*struct { Value string }", "{}"}, {"[]string", "[]"}, {"map[string]string", "{}"},
	} {
		t.Run(test.typeName, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, "Field "+test.typeName),
			})
			exported := func(value string) applicationmeta.Manifest {
				return composeManifest(t, "composition: {exports: {defaults: {config: {"+constructorConfigurationSymbol+": {field: "+value+"}}}}}\n").Exports()[0].Manifest()
			}
			dependencies := []applicationmeta.Dependency{
				{ModulePath: "example.com/alpha", ExportName: "defaults", Manifest: exported("")},
				{ModulePath: "example.com/beta", ExportName: "defaults", Manifest: exported("~")},
			}
			composed, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), lookup)
			if err != nil {
				t.Fatal(err)
			}
			records := findProvenance(t, composed.Provenance(), `config["`+constructorConfigurationSymbol+`"]["field"]`)
			if len(records) != 1 || records[0].Removed() || len(records[0].Sources()) != 2 {
				t.Fatalf("equivalent nil exports did not deduplicate: %#v", provenanceStrings(records))
			}
			dependencies[1].Manifest = exported(test.empty)
			var conflict string
			for _, ordered := range [][]applicationmeta.Dependency{dependencies, {dependencies[1], dependencies[0]}} {
				_, err := applicationmeta.Compose(ordered, composeManifest(t, "{}\n"), lookup)
				if !errors.Is(err, applicationmeta.ErrInheritedConflict) || conflict != "" && err.Error() != conflict {
					t.Fatalf("nil/empty conflict was lost or depended on order: %v", err)
				}
				conflict = err.Error()
				for _, value := range []string{"null", test.empty, "{$remove: true}"} {
					current := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {field: "+value+"}}\n")
					if _, err := applicationmeta.Compose(ordered, current, lookup); err != nil {
						t.Fatalf("current %s did not resolve conflict: %v", value, err)
					}
				}
			}
		})
	}
}

func TestNestedConstructorMaintenanceWritesCanonicalTombstone(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Settings struct { Removed string }; Optional *string"),
	})
	dependencies := []applicationmeta.Dependency{{ModulePath: "example.com/legacy", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {removed: {$remove: true}}, optional: null}}\n")}}
	maintained, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, nil, dependencies, lookup)
	if err != nil || !bytes.Contains(maintained.Data(), []byte("removed: {$remove: true}")) || !bytes.Contains(maintained.Data(), []byte("optional: null")) {
		t.Fatalf("maintained removal/nil = %v\n%s", err, maintained.Data())
	}
	if _, err := applicationmeta.ConfigurationDecisions(composeManifest(t, string(maintained.Data())), lookup); err != nil {
		t.Fatal(err)
	}
}
