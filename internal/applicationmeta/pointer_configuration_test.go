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
			name: "fixed struct", fields: "Settings struct { First string; Second string }",
			lower: "settings: {first: lower, second: inherited}", upper: "settings: {first: upper}",
			want: "settings: {first: upper, second: inherited}",
		},
		{
			name: "pointer", fields: "Settings *struct { First string; Second string }",
			lower: "settings: {first: lower, second: inherited}", upper: "settings: {first: upper}",
			want: "settings: {first: upper}",
		},
		{
			name: "multiple pointers", fields: "Settings **struct { First string; Second string }",
			lower: "settings: {first: lower, second: inherited}", upper: "settings: {first: upper}",
			want: "settings: {first: upper}",
		},
		{
			name: "empty pointer object", fields: "Settings *struct { First string; Second string }",
			lower: "settings: {first: lower, second: inherited}", upper: "settings: {}",
			want: "settings: {}",
		},
		{
			name: "omitted pointer", fields: "Settings *struct { First string; Second string }",
			lower: "settings: {first: lower, second: inherited}", upper: "",
			want: "settings: {first: lower, second: inherited}",
		},
		{
			name: "pointer inside fixed struct", fields: "Container struct { Retained string; Settings *struct { First string; Second string } }",
			lower: "container: {retained: inherited, settings: {first: lower, second: inherited}}",
			upper: "container: {settings: {first: upper}}",
			want:  "container: {retained: inherited, settings: {first: upper}}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, test.fields),
			})
			lower := composeManifest(t, fmt.Sprintf("config: {%s: {%s}}\n", constructorConfigurationSymbol, test.lower))
			upperData := "{}\n"
			if test.upper != "" {
				upperData = fmt.Sprintf("config: {%s: {%s}}\n", constructorConfigurationSymbol, test.upper)
			}
			upper, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(upperData))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := applicationmeta.ApplyOverlay(lower, upper, lookup)
			if err != nil {
				t.Fatal(err)
			}
			composed, err := applicationmeta.Compose(nil, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			configured, exists := composed.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
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
				t.Fatalf("composed configuration = %#v, want %#v", got, want)
			}
		})
	}
}

func TestConstructorPointerConfigurationRetainsCurrentProjectSource(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Settings *struct { First string; Second string }"),
	})
	manifest := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {second: two, first: one}}}\n")
	composed, err := applicationmeta.Compose(nil, manifest, lookup)
	if err != nil {
		t.Fatal(err)
	}
	path := "config[\"" + constructorConfigurationSymbol + "\"][\"settings\"]"
	records := findProvenance(t, composed.Provenance(), path)
	if len(records) != 1 || len(records[0].Sources()) != 1 || records[0].Sources()[0] != "plystra.yaml" {
		t.Fatalf("pointer configuration source = %#v", provenanceStrings(records))
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composed.Manifest(), lookup)
	if err != nil || len(decisions) != 2 || decisions[1].Summary() != applicationmeta.ConfigurationSummaryValue {
		t.Fatalf("pointer was decomposed into field decisions: %v, %v", decisions, err)
	}
}
