package constructorconfig_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorconfig"
	"go.yaml.in/yaml/v3"
)

func TestOrderedConfigurationAlgebra(t *testing.T) {
	text := constructorconfig.Schema{Kind: "string"}
	nested := object(constructorconfig.Field{Name: "a", Value: text}, constructorconfig.Field{Name: "b", Value: text})
	schema := object(
		constructorconfig.Field{Name: "object", Value: nested},
		constructorconfig.Field{Name: "pointer", Value: constructorconfig.Schema{Kind: "pointer", Element: &nested}},
		constructorconfig.Field{Name: "map", Value: constructorconfig.Schema{Kind: "map", Element: &text}},
		constructorconfig.Field{Name: "number", Value: constructorconfig.Schema{Kind: "number", Bits: 32}},
		constructorconfig.Field{Name: "secret", Value: constructorconfig.Schema{Kind: "secret"}},
	)
	for _, tc := range []struct {
		name, first, second, root, overlay, want, rule string
	}{
		{name: "partial structs", first: "object: {a: private-a}", second: "object: {b: private-b}", want: "object: {a: private-a, b: private-b}"},
		{name: "normalized equality", first: "number: 1.0", second: "number: 1", want: "number: 1.0"},
		{name: "map ordering", first: "map: {x: a, y: b}", second: "map: {y: b, x: a}", want: "map: {x: a, y: b}"},
		{name: "private replacement", first: "object: {a: private-a}", second: "object: {a: private-b}", want: "object: {a: private-b}"},
		{name: "secret replacement", first: "secret: {env: PRIVATE_FIRST}", second: "secret: {env: PRIVATE_SECOND}", want: "secret: {env: PRIVATE_SECOND}"},
		{name: "field replacement", first: "object: {a: private-a, b: inherited}", second: "object: {a: private-b}", root: "object: {a: local}", want: "object: {a: local, b: inherited}"},
		{name: "field removal", first: "object: {a: private-a, b: inherited}", second: "object: {a: private-b}", root: "object: {a: {$remove: true}}", overlay: "object: {b: overlay}", want: "object: {b: overlay}"},
		{name: "ancestor removal", first: "object: {a: private-a}", second: "object: {a: private-b}", overlay: "object: {$remove: true}", want: "{}"},
		{name: "object removal", first: "object: {a: private-a}", second: "object: {a: private-b}", root: "{$remove: true}", want: "null"},
		{name: "reintroduced object", first: "object: {a: inherited}", root: "object: {$remove: true}", overlay: "object: {b: overlay}", want: "object: {b: overlay}"},
		{name: "empty struct inherits", first: "object: {a: inherited}", root: "object: {}", want: "object: {a: inherited}"},
		{name: "atomic pointers replace", first: "pointer: {a: private-a}", second: "pointer: {b: private-b}", want: "pointer: {b: private-b}"},
		{name: "atomic pointer override", first: "pointer: {a: private-a}", second: "pointer: {b: private-b}", root: "pointer: {}", want: "pointer: {}"},
		{name: "nil overrides", first: "pointer: {a: private-a}", root: "pointer: null", want: "pointer: null"},
		{name: "empty map replaces", first: "map: {x: private-a}", root: "map: {}", want: "map: {}"},
		{name: "invalid ancestor remains invalid", first: "object: {a: 2}", root: "object: {$remove: true}", rule: "compiled Go type"},
		{name: "ancestor removal reintroduced", first: "object: {a: {$remove: true}}", root: "object: {a: local}", want: "object: {a: local}"},
		{name: "atomic removal forbidden", first: "pointer: {a: inherited}", root: "pointer: {a: {$remove: true}}", rule: "reserved removal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := func(text string) *yaml.Node {
				if text == "" {
					return nil
				}
				return decode(t, text)
			}
			for range 2 {
				got, err := constructorconfig.ComposeLayers(schema, node(tc.first), node(tc.second), node(tc.root), node(tc.overlay))
				if tc.rule != "" {
					if !errors.Is(err, constructorconfig.ErrValue) || !strings.Contains(err.Error(), tc.rule) {
						t.Fatalf("composition = %v", err)
					}
					if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "PRIVATE_") {
						t.Fatal("error exposed private input")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				var want *yaml.Node
				if tc.want != "null" {
					want, err = constructorconfig.Compose(schema, node(tc.want), nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				gotBytes, _ := yaml.Marshal(got)
				wantBytes, _ := yaml.Marshal(want)
				if !bytes.Equal(gotBytes, wantBytes) {
					t.Fatal("composition does not match expected typed value")
				}
			}
		})
	}
}

func TestOrderedLayersDeferDefaultsAndRequiredness(t *testing.T) {
	leaf := constructorconfig.Schema{Kind: "string"}
	schema := object(constructorconfig.Field{Name: "settings", GoName: "Settings", Value: object(
		constructorconfig.Field{Name: "required", GoName: "Required", Required: true, Value: leaf},
		constructorconfig.Field{Name: "defaulted", GoName: "Defaulted", HasDefault: true, Default: []byte(`"PRIVATE_DEFAULT"`), Value: leaf},
	)})
	layers := []*yaml.Node{
		decode(t, "settings: {required: inherited, defaulted: inherited}"),
		decode(t, "settings: {$remove: true}"),
		decode(t, "settings: {}"),
	}
	partial, err := constructorconfig.ComposeLayers(schema, layers...)
	if err != nil {
		t.Fatalf("partial required object rejected: %v", err)
	}
	data, _ := yaml.Marshal(partial)
	if strings.Contains(string(data), "defaulted") || strings.Contains(string(data), "inherited") {
		t.Fatal("composition applied defaults or resurrected excluded values")
	}
	if _, err := constructorconfig.Normalize(schema, partial); !errors.Is(err, constructorconfig.ErrValue) {
		t.Fatalf("missing final required field accepted: %v", err)
	}
	layers = append(layers, decode(t, "settings: {required: selected}"))
	composed, err := constructorconfig.ComposeLayers(schema, layers...)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := constructorconfig.Normalize(schema, composed)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Settings struct{ Required, Defaulted string }
	}
	if err := constructorconfig.Bind(context.Background(), nil, schema, normalized, &result); err != nil {
		t.Fatal(err)
	}
	if result.Settings.Required != "selected" || result.Settings.Defaulted != "PRIVATE_DEFAULT" {
		t.Fatal("final typed delivery lost required field or restored default")
	}
}

func FuzzOrderedConfigurationOrder(f *testing.F) {
	for _, seed := range [][3]string{
		{"object: {a: first}", "object: {b: second}", "{}"},
		{"object: {a: first}", "object: {a: second}", "object: {a: {$remove: true}}"},
		{"pointer: {a: first}", "pointer: {b: second}", "pointer: {}"},
		{"mapping: {private: value}", "mapping: {}", "{}"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	leaf := constructorconfig.Schema{Kind: "string"}
	child := object(constructorconfig.Field{Name: "a", Value: leaf}, constructorconfig.Field{Name: "b", Value: leaf})
	schema := object(constructorconfig.Field{Name: "object", Value: child}, constructorconfig.Field{Name: "pointer", Value: constructorconfig.Schema{Kind: "pointer", Element: &child}}, constructorconfig.Field{Name: "mapping", Value: constructorconfig.Schema{Kind: "map", Element: &leaf}})
	f.Fuzz(func(t *testing.T, first, second, current string) {
		var nodes []*yaml.Node
		for _, input := range []string{first, second, current} {
			if len(input) > 4096 {
				return
			}
			var document yaml.Node
			if yaml.Unmarshal([]byte(input), &document) != nil || len(document.Content) != 1 {
				return
			}
			nodes = append(nodes, document.Content[0])
		}
		a, firstErr := constructorconfig.ComposeLayers(schema, nodes...)
		b, secondErr := constructorconfig.ComposeLayers(schema, nodes[0], nodes[1], nodes[2])
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatal("repeat changed acceptance")
		}
		if firstErr != nil {
			return
		}
		left, _ := yaml.Marshal(a)
		right, _ := yaml.Marshal(b)
		if !bytes.Equal(left, right) {
			t.Fatal("repeat changed composition")
		}
	})
}
