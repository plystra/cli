package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"go.yaml.in/yaml/v3"
)

func TestSetResourceProviderPreservesExactUnrelatedIntent(t *testing.T) {
	t.Parallel()
	const input = `# Project choices.
interfaces:
  require: {add: [app.run/v1], remove: [old.run/v1]}
  use: {app.run/v1: example.com/service.New}
config:
  example.com/service.New: {token: {env: PRIVATE_SECRET}, optional: null}
http: {expose: {app.run/v1: {transport: connect}}}
resources:
  instances:
    database.primary: # selected instance
      use: example.com/database.New # provider choice
      config: {value: PRIVATE_OLD, nested: {value: {$remove: true}}}
    database.replica: # preserve sibling
      use: example.com/database.New
      config: {value: PRIVATE_REPLICA}
    excluded: {$remove: true} # preserve exclusion
  bind:
    instances:
      database.primary:
        local: database.replica
        inherited: database.replica # preserve binding comment
        retained: database.replica # no retarget
        Retained: database.primary
        excluded: {$remove: true}
      database.replica: {local: database.primary}
    implementations:
      example.com/service.New: {local: database.primary}
`
	provider := mustImplementationChoiceConstructor(t, replacementResourceProvider)
	removals := []applicationmeta.ResourceParameterRemoval{{ParameterName: "local"}, {ParameterName: "inherited", Tombstone: true}}
	for _, overlay := range []bool{true} {
		t.Run(fmt.Sprint(overlay), func(t *testing.T) {
			data := []byte(input)
			if overlay {
				data = []byte(strings.ReplaceAll(input, "template: example.com/base\n", ""))
			}
			original := bytes.Clone(data)
			edit := applicationmeta.SetResourceProvider
			if overlay {
				edit = applicationmeta.SetResourceProviderOverlay
			}
			updated, changed, err := edit(data, "database.primary", provider, true, removals)
			if err != nil || !changed {
				t.Fatalf("set provider: changed=%t, %v", changed, err)
			}
			want := selectionYAML(t, data)
			instance := selectionMap(t, want, "resources", "instances", "database.primary")
			instance["use"] = replacementResourceProvider
			delete(instance, "config")
			bindings := selectionMap(t, want, "resources", "bind", "instances", "database.primary")
			delete(bindings, "local")
			bindings["inherited"] = map[string]any{"$remove": true}
			if !reflect.DeepEqual(selectionYAML(t, updated), want) {
				t.Fatal("provider edit changed unrelated semantics")
			}
			for _, comment := range []string{"# Project choices.", "# selected instance", "# provider choice", "# preserve sibling", "# preserve exclusion", "# preserve binding comment", "# no retarget"} {
				if !bytes.Contains(updated, []byte(comment)) {
					t.Fatalf("lost comment %q", comment)
				}
			}
			if !bytes.Equal(data, original) {
				t.Fatal("provider edit mutated input bytes")
			}
			again, changed, err := edit(updated, "database.primary", provider, true, removals)
			if err != nil || changed || !bytes.Equal(updated, again) {
				t.Fatalf("provider edit is not idempotent: %t, %v", changed, err)
			}
		})
	}
}

func TestSetResourceProviderPreservesSameProviderAndSparseConfiguration(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	for _, config := range []string{"{}", "{value: PRIVATE_VALUE, nested: {value: {$remove: true}}}", "{$remove: true}"} {
		input := []byte("# exact bytes\nresources: {instances: {database.primary: {use: " + resourceProvider + ", config: " + config + "}}}\n")
		updated, changed, err := applicationmeta.SetResourceProvider(input, "database.primary", provider, false, nil)
		if err != nil || changed || !bytes.Equal(input, updated) {
			t.Fatalf("same-provider no-op: %t, %v", changed, err)
		}
		updated[0] = '!'
		if input[0] != '#' {
			t.Fatal("no-op returned aliased bytes")
		}
		input = []byte(resourceDocument("{config: " + config + "}"))
		updated, changed, err = applicationmeta.SetResourceProviderOverlay(input, "database.primary", provider, false, nil)
		if err != nil || !changed {
			t.Fatalf("config-only selection: %t, %v", changed, err)
		}
		want := selectionYAML(t, input)
		selectionMap(t, want, "resources", "instances", "database.primary")["use"] = resourceProvider
		if !reflect.DeepEqual(selectionYAML(t, updated), want) {
			t.Fatal("config-only selection lost local configuration")
		}
	}
}

func TestSetResourceProviderRevivesOnlySelectedInstance(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	for _, input := range []string{"{}\n", "resources: {}\n", "resources: {instances: {}}\n", "resources: {instances: {database.primary: {$remove: true}}}\n"} {
		updated, changed, err := applicationmeta.SetResourceProviderOverlay([]byte(input), "database.primary", provider, true, []applicationmeta.ResourceParameterRemoval{
			{ParameterName: "local"}, {ParameterName: "inherited", Tombstone: true},
		})
		if err != nil || !changed {
			t.Fatalf("sparse provider selection: %t, %v", changed, err)
		}
		want := selectionYAML(t, []byte("resources: {instances: {database.primary: {use: "+resourceProvider+"}}, bind: {instances: {database.primary: {inherited: {$remove: true}}}}}\n"))
		if !reflect.DeepEqual(selectionYAML(t, updated), want) {
			t.Fatal("sparse selection materialized configuration, values, or a container removal")
		}
	}
}

func TestSetResourceProviderComposesReplacementWithoutOldConfiguration(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string; Other string")
	rootData := []byte(`resources:
  instances:
    database.primary: {config: {value: local}}
    database.replica: {use: example.com/database.New, config: {value: sibling}}
  bind:
    instances:
      database.primary: {old: database.replica, retained: database.replica}
`)
	for _, replacement := range []bool{false, true} {
		symbol := resourceProvider
		if replacement {
			symbol = replacementResourceProvider
		}
		updated, _, err := applicationmeta.SetResourceProvider(rootData, "database.primary", mustImplementationChoiceConstructor(t, symbol), replacement, []applicationmeta.ResourceParameterRemoval{{ParameterName: "old"}})
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose(nil, resourceManifest(t, "plystra.yaml", string(updated)), lookup)
		if err != nil {
			t.Fatal(err)
		}
		primary := resourceConfig(t, composition.Manifest(), "database.primary")
		if replacement {
			if primary.HasConfiguration() {
				t.Fatal("old provider configuration crossed replacement boundary")
			}
		} else {
			assertResourceYAML(t, primary, "{value: local}")
		}
		assertResourceYAML(t, resourceConfig(t, composition.Manifest(), "database.replica"), "{value: sibling}")
		bindings := composition.Manifest().ResourceBindings()
		if len(bindings) != 1 || bindings[0].ParameterName() != "retained" || bindings[0].Target() != "database.replica" {
			t.Fatal("cleanup did not suppress exactly the obsolete inherited binding")
		}
	}
}

func TestSetResourceProviderRejectsInvalidPlansAndDocumentsPrivately(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	for _, test := range []struct {
		name     string
		data     string
		instance string
		provider constructorsymbol.Symbol
		removals []applicationmeta.ResourceParameterRemoval
	}{
		{"invalid instance", "{}", "PRIVATE_INPUT", provider, nil},
		{"empty instance", "{}", "", provider, nil},
		{"empty provider", "{}", "primary", constructorsymbol.Symbol{}, nil},
		{"blank parameter", "{}", "primary", provider, []applicationmeta.ResourceParameterRemoval{{ParameterName: "_"}}},
		{"invalid parameter", "{}", "primary", provider, []applicationmeta.ResourceParameterRemoval{{ParameterName: "PRIVATE-INPUT"}}},
		{"duplicate parameter", "{}", "primary", provider, []applicationmeta.ResourceParameterRemoval{{ParameterName: "old"}, {ParameterName: "old", Tombstone: true}}},
		{"unknown field", "PRIVATE_INPUT: true", "primary", provider, nil},
		{"invalid mapping", "resources: {instances: {primary: {use: PRIVATE_INPUT}}}", "primary", provider, nil},
		{"invalid config", "resources: {instances: {primary: {config: {value: !!null PRIVATE_INPUT}}}}", "primary", provider, nil},
		{"container removal", "resources: {bind: {instances: {primary: {$remove: true}}}}", "primary", provider, nil},
		{"anchor", "resources: &PRIVATE_INPUT {}", "primary", provider, nil},
		{"multiple documents", "{}\n---\nPRIVATE_INPUT: true", "primary", provider, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(test.data)
			before := bytes.Clone(input)
			updated, changed, err := applicationmeta.SetResourceProvider(input, test.instance, test.provider, true, test.removals)
			if !errors.Is(err, applicationmeta.ErrSetResourceProvider) || updated != nil || changed || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "PRIVATE_INPUT") {
				t.Fatalf("invalid edit accepted or leaked input: %t, %v", changed, err)
			}
			if !bytes.Equal(before, input) {
				t.Fatal("rejection changed input")
			}
		})
	}
	_, _, err := applicationmeta.SetResourceProviderOverlay([]byte("template: example.com/base\n"), "primary", provider, false, nil)
	if !errors.Is(err, applicationmeta.ErrSetResourceProvider) || !errors.Is(err, applicationmeta.ErrInvalidManifest) {
		t.Fatalf("overlay accepted root metadata: %v", err)
	}
}

func selectionYAML(t testing.TB, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := yaml.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func selectionMap(t testing.TB, value map[string]any, path ...string) map[string]any {
	t.Helper()
	for _, key := range path {
		next, ok := value[key].(map[string]any)
		if !ok {
			t.Fatalf("missing mapping %s", key)
		}
		value = next
	}
	return value
}
