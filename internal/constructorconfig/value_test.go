package constructorconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorconfig"
	"go.yaml.in/yaml/v3"
)

func object(fields ...constructorconfig.Field) constructorconfig.Schema {
	return constructorconfig.Schema{Kind: "object", Fields: fields}
}

func decode(t testing.TB, text string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}

func TestConfigurationBoundsAndFinalRequiredness(t *testing.T) {
	leaf := constructorconfig.Schema{Kind: "string"}
	required := object(constructorconfig.Field{Name: "required", GoName: "Required", Required: true, Value: leaf})
	arrayLength := int64(2)
	for _, tc := range []struct {
		name   string
		schema constructorconfig.Schema
		yaml   string
		fail   bool
	}{
		{"absent required object", object(constructorconfig.Field{Name: "object", Value: required}), "{}", true},
		{"absent required array", object(constructorconfig.Field{Name: "array", Value: constructorconfig.Schema{Kind: "list", Length: &arrayLength, Element: &required}}), "{}", true},
		{"absent pointer", object(constructorconfig.Field{Name: "pointer", Value: constructorconfig.Schema{Kind: "pointer", Element: &required}}), "{}", false},
		{"present pointer", object(constructorconfig.Field{Name: "pointer", Value: constructorconfig.Schema{Kind: "pointer", Element: &required}}), "pointer: {}", true},
		{"nil required pointer", object(constructorconfig.Field{Name: "pointer", Required: true, Value: constructorconfig.Schema{Kind: "pointer", Element: &required}}), "pointer: null", false},
		{"zero required string", required, "required: ''", false},
		{"deep values", object(constructorconfig.Field{Name: "required", Required: true, Value: leaf}), "required: [unexpected]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := constructorconfig.Normalize(tc.schema, decode(t, tc.yaml))
			if (err != nil) != tc.fail {
				t.Fatalf("normalization = %v", err)
			}
		})
	}
	deep := leaf
	for i := 0; i < 66; i++ {
		element := deep
		deep = constructorconfig.Schema{Kind: "pointer", Element: &element}
	}
	if _, err := constructorconfig.Normalize(deep, decode(t, "value")); !errors.Is(err, constructorconfig.ErrValue) {
		t.Fatal("unbounded traversal accepted")
	}
	large := int64(65537)
	if _, err := constructorconfig.Normalize(constructorconfig.Schema{Kind: "list", Length: &large, Element: &leaf}, nil); !errors.Is(err, constructorconfig.ErrValue) {
		t.Fatal("unbounded implicit array accepted")
	}
}

func TestLayerValidationCannotBeHiddenByRemoval(t *testing.T) {
	s := object(constructorconfig.Field{Name: "count", Value: constructorconfig.Schema{Kind: "signed-integer", Bits: 8}})
	for _, lower := range []string{"count: 128", "private-key: private-value", "count: !!str 1", "count: {private-key: private-value}"} {
		_, err := constructorconfig.Compose(s, decode(t, lower), decode(t, "{$remove: true}"))
		if !errors.Is(err, constructorconfig.ErrValue) {
			t.Fatal("invalid lower layer was suppressed")
		}
		if strings.Contains(err.Error(), "private-") {
			t.Fatal("private contents entered diagnostic")
		}
	}
}

func TestDefaultsArePrivateAndDoNotAllocateAbsentPointers(t *testing.T) {
	type Nested struct {
		Value string `plystra-default:"private-default"`
	}
	type Config struct {
		Object  Nested
		Pointer *Nested
		Count   uint16 `plystra-default:"0003"`
	}
	child := object(constructorconfig.Field{Name: "value", GoName: "Value", HasDefault: true, Value: constructorconfig.Schema{Kind: "string"}})
	s := object(constructorconfig.Field{Name: "object", GoName: "Object", Value: child}, constructorconfig.Field{Name: "pointer", GoName: "Pointer", Value: constructorconfig.Schema{Kind: "pointer", Element: &child}}, constructorconfig.Field{Name: "count", GoName: "Count", HasDefault: true, Value: constructorconfig.Schema{Kind: "unsigned-integer", Bits: 16}})
	if err := constructorconfig.BindDefaults(&s, reflect.TypeFor[Config]()); err != nil {
		t.Fatal(err)
	}
	n, err := constructorconfig.Normalize(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result Config
	if err := constructorconfig.Bind(context.Background(), nil, s, n, &result); err != nil {
		t.Fatal(err)
	}
	if result.Object.Value != "private-default" || result.Pointer != nil || result.Count != 3 {
		t.Fatal("wrong defaults")
	}
	encoded, err := json.Marshal(s)
	if err != nil || strings.Contains(string(encoded), "private-default") {
		t.Fatal("schema serialized private defaults")
	}
	public, err := constructorconfig.PublicJSON(s, n)
	if err != nil || string(public) != "null" {
		t.Fatal("private object entered public projection")
	}
}

func FuzzTypedConfiguration(f *testing.F) {
	for _, seed := range []string{"{}", "{count: 12}", "{count: 128}", "{mapping: {private: [null]}}", "{mapping: {$remove: true}}", "{count: &x 2, mapping: *x}"} {
		f.Add(seed)
	}
	element := constructorconfig.Schema{Kind: "string"}
	s := object(constructorconfig.Field{Name: "count", GoName: "Count", Value: constructorconfig.Schema{Kind: "signed-integer", Bits: 8}}, constructorconfig.Field{Name: "mapping", GoName: "Mapping", Value: constructorconfig.Schema{Kind: "map", Element: &element}})
	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 65536 {
			return
		}
		var doc yaml.Node
		if yaml.Unmarshal([]byte(data), &doc) != nil || len(doc.Content) != 1 {
			return
		}
		n, err := constructorconfig.Compose(s, doc.Content[0], nil)
		if err != nil {
			return
		}
		n, err = constructorconfig.Normalize(s, n)
		if err != nil {
			return
		}
		var result struct {
			Count   int8
			Mapping map[string]string
		}
		if err := constructorconfig.Bind(context.Background(), nil, s, n, &result); err != nil {
			t.Fatal(err)
		}
		if _, err := constructorconfig.PublicJSON(s, n); err != nil {
			t.Fatal(err)
		}
	})
}

func TestBindDefaultsRejectsStaleCompiledShape(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct{ Value int }](),
		reflect.TypeFor[struct {
			Value string
			Added string
		}](),
		reflect.TypeFor[struct {
			Value string `yaml:"renamed"`
		}](),
		reflect.TypeFor[struct {
			Value string `plystra:"required"`
		}](),
		reflect.TypeFor[struct {
			Value string `plystra:"build-visible"`
		}](),
		reflect.TypeFor[struct {
			Value string `plystra-default:"private"`
		}](),
	} {
		s := object(constructorconfig.Field{Name: "value", GoName: "Value", Value: constructorconfig.Schema{Kind: "string"}})
		if err := constructorconfig.BindDefaults(&s, typ); !errors.Is(err, constructorconfig.ErrValue) {
			t.Fatal("stale compiled schema was accepted")
		}
	}
}
