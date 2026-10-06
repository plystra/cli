package applicationmeta_test

import (
	"bytes"
	"errors"
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
			removed, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("config: {"+constructorConfigurationSymbol+": {field: {$remove: true}}}\n"))
			if err != nil {
				t.Fatal(err)
			}
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
		constructorConfigurationSymbol: composeSchema(t, "Text string\nSettings struct { Keep string; Remove string }\nPointer *string\nItems []string\nLabels map[string]string\nToken configuration.Secret\n"),
	})
	lower := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {text: lower, settings: {keep: retained, remove: lower}, pointer: lower, items: [lower], labels: {lower: value}, token: {env: PRIVATE_TOKEN}}}\n")
	upper, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("config: {"+constructorConfigurationSymbol+": {text: {$remove: true}, settings: {remove: {$remove: true}}, pointer: null, items: null, labels: null, token: {$remove: true}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ApplyOverlay(lower, upper, lookup)
	if err != nil {
		t.Fatal(err)
	}
	selectedConfig, _ := selected.Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	if bytes.Count(selectedConfig.YAML(), []byte("$remove: true")) != 3 {
		t.Fatalf("overlay did not preserve nested tombstones: %s", selectedConfig.YAML())
	}
	composed, err := applicationmeta.Compose(nil, selected, lookup)
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
		{"map[string]bool", "{$remove: \"true\"}"},
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
