package applicationmeta_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
	"go.yaml.in/yaml/v3"
)

func TestConstructorConfigurationComposesOnlyNonPointerStructFields(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, fields, lower, upper, want string
	}{
		{
			name:   "fixed struct",
			fields: "Settings struct { First string; Second string }",
			lower:  "settings: {first: lower, second: inherited}",
			upper:  "settings: {first: upper}",
			want:   "settings: {first: upper, second: inherited}",
		},
		{
			name:   "pointer",
			fields: "Settings *struct { First string; Second string }",
			lower:  "settings: {first: lower, second: inherited}",
			upper:  "settings: {first: upper}",
			want:   "settings: {first: upper}",
		},
		{
			name:   "multiple pointers",
			fields: "Settings **struct { First string; Second string }",
			lower:  "settings: {first: lower, second: inherited}",
			upper:  "settings: {first: upper}",
			want:   "settings: {first: upper}",
		},
		{
			name:   "empty pointer object",
			fields: "Settings *struct { First string; Second string }",
			lower:  "settings: {first: lower, second: inherited}",
			upper:  "settings: {}",
			want:   "settings: {}",
		},
		{
			name:   "omitted pointer",
			fields: "Settings *struct { First string; Second string }",
			lower:  "settings: {first: lower, second: inherited}",
			upper:  "",
			want:   "settings: {first: lower, second: inherited}",
		},
		{
			name:   "pointer inside fixed struct",
			fields: "Container struct { Retained string; Settings *struct { First string; Second string } }",
			lower:  "container: {retained: inherited, settings: {first: lower, second: inherited}}",
			upper:  "container: {settings: {first: upper}}",
			want:   "container: {retained: inherited, settings: {first: upper}}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, test.fields),
			})
			lower := composeManifest(t, fmt.Sprintf("config: {%s: {%s}}\n", constructorConfigurationSymbol, test.lower))
			upper := composeManifest(t, fmt.Sprintf("config: {%s: {%s}}\n", constructorConfigurationSymbol, test.upper))
			for _, overlay := range []bool{false, true} {
				var selected applicationmeta.Manifest
				if overlay {
					var err error
					selected, err = applicationmeta.ApplyOverlay(lower, upper, lookup)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					composed, err := applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "example.com/platform", Manifest: lower}}, upper, lookup)
					if err != nil {
						t.Fatal(err)
					}
					selected = composed.Manifest()
				}
				configured, exists := selected.Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
				if !exists {
					t.Fatal("composed configuration is absent")
				}
				var got, want any
				if err := yaml.Unmarshal(configured.YAML(), &got); err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal([]byte(test.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("overlay %t composed configuration = %#v, want %#v", overlay, got, want)
				}
			}
		})
	}
}

func TestConstructorPointerConfigurationDeduplicatesNormalizedValues(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Settings *struct { First string; Second string }"),
	})
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/alpha", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {first: one, second: two}}}\n")},
		{ModulePath: "example.com/beta", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {second: two, first: one}}}\n")},
	}
	for _, ordered := range [][]applicationmeta.Dependency{dependencies, {dependencies[1], dependencies[0]}} {
		composed, err := applicationmeta.Compose(ordered, composeManifest(t, "{}\n"), lookup)
		if err != nil {
			t.Fatal(err)
		}
		records := findProvenance(t, composed.Provenance(), `config["`+constructorConfigurationSymbol+`"]["settings"]`)
		if len(records) != 1 || len(records[0].Sources()) != 2 {
			t.Fatalf("identical whole-pointer values did not retain both sources: %#v", provenanceStrings(records))
		}
		decisions, err := applicationmeta.ConfigurationDecisions(composed.Manifest(), lookup)
		if err != nil || len(decisions) != 2 || decisions[1].Summary() != applicationmeta.ConfigurationSummaryValue {
			t.Fatalf("pointer was decomposed into field decisions: %v, %v", decisions, err)
		}
	}
}
