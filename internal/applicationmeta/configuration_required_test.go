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
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestRequiredConstructorConfigurationPresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, fields, entry, missing string
	}{
		{"scalar absent", "Value string `plystra:\"required\"`", "{}", `["value"]`},
		{"scalar zero", "Value string `plystra:\"required\"`", `{value: ""}`, ""},
		{"boolean zero", "Value bool `plystra:\"required\"`", `{value: false}`, ""},
		{"integer zero", "Value int `plystra:\"required\"`", `{value: 0}`, ""},
		{"default remains absent", "Value string `plystra-default:\"PRIVATE_DEFAULT\"`", `{}`, ""},
		{"pointer absent", "Value *string `plystra:\"required\"`", `{}`, `["value"]`},
		{"pointer nil", "Value *string `plystra:\"required\"`", `{value: null}`, ""},
		{"slice nil", "Value []string `plystra:\"required\"`", `{value: null}`, ""},
		{"slice empty", "Value []string `plystra:\"required\"`", `{value: []}`, ""},
		{"map nil", "Value map[string]string `plystra:\"required\"`", `{value: null}`, ""},
		{"map empty", "Value map[string]string `plystra:\"required\"`", `{value: {}}`, ""},
		{"required object absent", "Value struct { Child string } `plystra:\"required\"`", `{}`, `["value"]`},
		{"required object empty", "Value struct { Child string } `plystra:\"required\"`", `{value: {}}`, ""},
		{"nested absent", "Value struct { Child string `plystra:\"required\"` }", `{}`, `["value"]["child"]`},
		{"nested empty", "Value struct { Child string `plystra:\"required\"` }", `{value: {}}`, `["value"]["child"]`},
		{"nested removed", "Value struct { Child string `plystra:\"required\"` }", `{value: {$remove: true}}`, `["value"]["child"]`},
		{"nested child removed", "Value struct { Child string `plystra:\"required\"` }", `{value: {child: {$remove: true}}}`, `["value"]["child"]`},
		{"nested zero", "Value struct { Child string `plystra:\"required\"` }", `{value: {child: ""}}`, ""},
		{"optional pointer absent", "Value *struct { Child string `plystra:\"required\"` }", `{}`, ""},
		{"optional pointer nil", "Value *struct { Child string `plystra:\"required\"` }", `{value: null}`, ""},
		{"pointer empty", "Value *struct { Child string `plystra:\"required\"` }", `{value: {}}`, `["value"]["child"]`},
		{"double pointer empty", "Value **struct { Child string `plystra:\"required\"` }", `{value: {}}`, `["value"]["child"]`},
		{"optional slice absent", "Value []struct { Child string `plystra:\"required\"` }", `{}`, ""},
		{"optional slice empty", "Value []struct { Child string `plystra:\"required\"` }", `{value: []}`, ""},
		{"slice element", "Value []struct { Child string `plystra:\"required\"` }", `{value: [{}]}`, `["value"]`},
		{"slice later element", "Value []struct { Child string `plystra:\"required\"` }", `{value: [{child: PRIVATE_VALUE}, {}]}`, `["value"]`},
		{"slice pointer nil", "Value []*struct { Child string `plystra:\"required\"` }", `{value: [null]}`, ""},
		{"map element", "Value map[string]struct { Child string `plystra:\"required\"` }", `{value: {PRIVATE_KEY: {}}}`, `["value"]`},
		{"map zero child", "Value map[string]struct { Child string `plystra:\"required\"` }", `{value: {PRIVATE_KEY: {child: ""}}}`, ""},
		{"array absent", "Value [2]struct { Child string `plystra:\"required\"` }", `{}`, `["value"]`},
		{"array empty element", "Value [1]struct { Child string `plystra:\"required\"` }", `{value: [{}]}`, `["value"]`},
		{"empty array absent", "Value [0]struct { Child string `plystra:\"required\"` }", `{}`, ""},
		{"huge implicit array", "Value [1000000000]struct { Child string `plystra:\"required\"` }", `{}`, `["value"]`},
		{"secret absent", "Value configuration.Secret `plystra:\"required\"`", `{}`, `["value"]`},
		{"secret reference", "Value configuration.Secret `plystra:\"required\"`", `{value: {env: PRIVATE_SECRET}}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
				constructorConfigurationSymbol: composeSchema(t, test.fields),
			})
			manifest, err := applicationmeta.WithProjectModule(composeManifest(t, "config: {"+constructorConfigurationSymbol+": "+test.entry+"}\n"), "example.com/current")
			if err != nil {
				t.Fatal(err)
			}
			composition, err := applicationmeta.Compose(nil, manifest, lookup)
			if err != nil {
				t.Fatalf("partial composition rejected: %v", err)
			}
			constructor := mustConstructorSymbol(t, constructorConfigurationSymbol)
			before, _ := composition.Manifest().Configuration(constructor)
			for _, active := range [][]constructorsymbol.Symbol{nil, {constructor, constructor}} {
				err := composition.ValidateRequiredConfiguration(lookup, active, "deploy/customer.yaml")
				if test.missing == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					assertMissingConstructorField(t, err, test.missing, "deploy/customer.yaml")
					var detail *applicationmeta.ConstructorConfigurationValueError
					if !errors.As(err, &detail) || detail.ModulePath() != "example.com/current" {
						t.Fatalf("missing selected module ownership: %v", err)
					}
				}
				after, _ := composition.Manifest().Configuration(constructor)
				if !bytes.Equal(before.YAML(), after.YAML()) {
					t.Fatal("requiredness validation changed authored values or inserted defaults")
				}
			}
		})
	}
}

func TestRequiredConstructorConfigurationAfterComposition(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Endpoint string `plystra:\"required\"`\nSettings struct { Region string `plystra:\"required\"` }\nPointer *struct { Region string `plystra:\"required\"` }"),
	})
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/alpha", ExportName: "first", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {endpoint: PRIVATE_ENDPOINT}}\n")},
		{ModulePath: "example.com/beta", ExportName: "second", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {region: PRIVATE_REGION}, pointer: {region: PRIVATE_POINTER}}}\n")},
	}
	for _, test := range []struct{ name, fields, missing string }{
		{"partial exports", "", ""},
		{"empty fixed struct inherits", "settings: {}", ""},
		{"empty pointer replaces", "pointer: {}", `["pointer"]["region"]`},
		{"nil pointer replaces", "pointer: null", ""},
		{"required field removed", "endpoint: {$remove: true}", `["endpoint"]`},
		{"required child removed", "settings: {region: {$remove: true}}", `["settings"]["region"]`},
		{"object removed", "settings: {$remove: true}", `["settings"]["region"]`},
		{"zero replacement", `endpoint: ""`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, ordered := range [][]applicationmeta.Dependency{dependencies, {dependencies[1], dependencies[0]}} {
				composition, err := applicationmeta.Compose(ordered, composeManifest(t, "config: {"+constructorConfigurationSymbol+": {"+test.fields+"}}\n"), lookup)
				if err != nil {
					t.Fatal(err)
				}
				before := composition.Provenance()
				err = composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml")
				if test.missing != "" {
					assertMissingConstructorField(t, err, test.missing, "plystra.production.yaml")
				} else if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, composition.Provenance()) {
					t.Fatal("validation changed provenance")
				}
			}
		})
	}
	// An incomplete export can be completed by the selected layer, not rejected
	// while the export or sparse environment override is still being normalized.
	root := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {}}\n")
	overlay := composeManifest(t, "config: {"+constructorConfigurationSymbol+": {settings: {region: current}}}\n")
	current, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose(dependencies[:1], current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredConstructorConfigurationActiveAbsence(t *testing.T) {
	t.Parallel()
	constructor := mustConstructorSymbol(t, constructorConfigurationSymbol)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Endpoint string `plystra:\"required\"`"),
	})
	for _, source := range []string{"{}\n", "config: {" + constructorConfigurationSymbol + ": {$remove: true}}\n"} {
		composition, err := applicationmeta.Compose(nil, composeManifest(t, source), lookup)
		if err != nil {
			t.Fatal(err)
		}
		if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.yaml"); err != nil {
			t.Fatalf("unconfigured dormant constructor: %v", err)
		}
		assertMissingConstructorField(t, composition.ValidateRequiredConfiguration(lookup, []constructorsymbol.Symbol{constructor}, "plystra.yaml"), `["endpoint"]`, "plystra.yaml")
	}
	composition, err := applicationmeta.Compose(nil, composeManifest(t, "{}\n"), lookup)
	if err != nil {
		t.Fatal(err)
	}
	withoutConfig := mustConstructorSymbol(t, "example.com/acme/plain.New")
	if err := composition.ValidateRequiredConfiguration(lookup, []constructorsymbol.Symbol{withoutConfig}, "plystra.yaml"); err != nil {
		t.Fatalf("constructor without Config: %v", err)
	}
	if err := (applicationmeta.Composition{}).ValidateRequiredConfiguration(lookup, nil, "plystra.yaml"); !errors.Is(err, applicationmeta.ErrCompose) {
		t.Fatalf("invalid composition: %v", err)
	}
}

func assertMissingConstructorField(t testing.TB, err error, suffix, path string) {
	t.Helper()
	var detail *applicationmeta.ConstructorConfigurationValueError
	if !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.Is(err, applicationmeta.ErrConfigurationRequired) || !errors.As(err, &detail) {
		t.Fatalf("requiredness error = %v", err)
	}
	if detail.Field() != fmt.Sprintf("config[%q]", constructorConfigurationSymbol)+suffix || detail.SourcePath() != path || detail.Line() != 1 || detail.Column() != 1 {
		t.Fatalf("requiredness error field/source = %s, %s:%d:%d", detail.Field(), detail.SourcePath(), detail.Line(), detail.Column())
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", err, detail, detail), "PRIVATE_") {
		t.Fatal("requiredness error disclosed a value, default, Secret target, or dynamic map key")
	}
}

func FuzzRequiredConstructorConfiguration(f *testing.F) {
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(f, "Endpoint string `plystra:\"required\"`\nSettings struct { Region string `plystra:\"required\"` }\nValues map[string]*struct { Name string `plystra:\"required\"` }"),
	})
	constructor := mustConstructorSymbol(f, constructorConfigurationSymbol)
	for _, seed := range []string{"{}", "{$remove: true}", "{endpoint: '', settings: {region: ''}}", "{endpoint: {$remove: true}}", "{values: {PRIVATE_KEY: {}}}", "{values: {PRIVATE_KEY: null}}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, entry string) {
		if len(entry) > 32_768 {
			t.Skip()
		}
		manifest, err := applicationmeta.Parse([]byte("config: {" + constructorConfigurationSymbol + ": " + entry + "}\n"))
		if err != nil {
			return
		}
		composition, err := applicationmeta.Compose(nil, manifest, lookup)
		if err != nil {
			return
		}
		configured, _ := composition.Manifest().Configuration(constructor)
		before := configured.YAML()
		active := []constructorsymbol.Symbol{constructor}
		first := composition.ValidateRequiredConfiguration(lookup, active, "plystra.yaml")
		second := composition.ValidateRequiredConfiguration(lookup, active, "plystra.yaml")
		if fmt.Sprint(first) != fmt.Sprint(second) {
			t.Fatal("requiredness is nondeterministic")
		}
		configured, _ = composition.Manifest().Configuration(constructor)
		if !bytes.Equal(before, configured.YAML()) {
			t.Fatal("requiredness mutated configuration")
		}
	})
}
