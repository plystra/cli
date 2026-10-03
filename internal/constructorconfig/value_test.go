package constructorconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestMalformedTaggedNullCannotBypassNullableNormalization(t *testing.T) {
	leaf := constructorconfig.Schema{Kind: "string"}
	for _, kind := range []string{"pointer", "list", "map"} {
		t.Run(kind, func(t *testing.T) {
			schema := object(constructorconfig.Field{Name: "value", Value: constructorconfig.Schema{Kind: kind, Element: &leaf}})
			for _, text := range []string{"value: !!null PRIVATE_VALUE", "value: null"} {
				node := decode(t, text)
				_, normalizeErr := constructorconfig.Normalize(schema, node)
				_, lowerErr := constructorconfig.Compose(schema, node, decode(t, "{$remove: true}"))
				_, upperErr := constructorconfig.Compose(schema, nil, node)
				_, layerErr := constructorconfig.ComposeLayers(schema, node)
				for _, err := range []error{normalizeErr, lowerErr, upperErr, layerErr} {
					if text == "value: null" {
						if err != nil {
							t.Fatalf("valid null: %v", err)
						}
					} else if !errors.Is(err, constructorconfig.ErrValue) || strings.Contains(err.Error(), "PRIVATE_") {
						t.Fatalf("malformed null was not rejected safely: %v", err)
					}
				}
			}
		})
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

func TestBindDefaultsRejectsInvalidCompiledMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tag   string
		typ   reflect.Type
		field constructorconfig.Field
	}{
		{name: "empty policy", tag: `plystra:""`},
		{name: "duplicate YAML tag", tag: `yaml:"value" yaml:"private-other"`},
		{name: "duplicate policy tag", tag: `plystra:"required" plystra:"private-other"`, field: constructorconfig.Field{Required: true}},
		{name: "duplicate option", tag: `plystra:"required,required"`, field: constructorconfig.Field{Required: true}},
		{name: "empty option", tag: `plystra:"required,"`, field: constructorconfig.Field{Required: true}},
		{name: "malformed unrelated tag", tag: `json:"value"private-invalid`},
		{name: "duplicate default", tag: `plystra-default:"private-first" plystra-default:"private-second"`, field: constructorconfig.Field{HasDefault: true}},
		{name: "required default", tag: `plystra:"required" plystra-default:"private-default"`, field: constructorconfig.Field{Required: true, HasDefault: true}},
		{name: "invalid boolean default", tag: `plystra-default:"private-invalid"`, typ: reflect.TypeFor[bool](), field: constructorconfig.Field{HasDefault: true, Value: constructorconfig.Schema{Kind: "boolean"}}},
		{name: "invalid duration default", tag: `plystra-default:"private-invalid"`, typ: reflect.TypeFor[time.Duration](), field: constructorconfig.Field{HasDefault: true, Value: constructorconfig.Schema{Kind: "duration"}}},
		{name: "invalid URL default", tag: `plystra-default:"https://private.example/%zz"`, typ: reflect.TypeFor[url.URL](), field: constructorconfig.Field{HasDefault: true, Value: constructorconfig.Schema{Kind: "url"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.typ == nil {
				tc.typ = reflect.TypeFor[string]()
			}
			field := tc.field
			field.Name, field.GoName = "value", "Value"
			if field.Value.Kind == "" {
				field.Value.Kind = "string"
			}
			s := object(field)
			typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: tc.typ, Tag: reflect.StructTag(tc.tag)}})
			err := constructorconfig.BindDefaults(&s, typ)
			if !errors.Is(err, constructorconfig.ErrValue) {
				t.Fatalf("invalid metadata was accepted: %v", err)
			}
			if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "private.example") {
				t.Fatal("private tag entered diagnostic")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		field reflect.StructField
	}{
		{"ignored policy", reflect.StructField{Name: "Ignored", Type: reflect.TypeFor[string](), Tag: `yaml:"-" plystra:"required"`}},
		{"ignored default", reflect.StructField{Name: "Ignored", Type: reflect.TypeFor[string](), Tag: `yaml:"-" plystra-default:"private-default"`}},
		{"unexported YAML", reflect.StructField{Name: "hidden", PkgPath: "example.com/private", Type: reflect.TypeFor[string](), Tag: `yaml:"hidden"`}},
		{"unexported policy", reflect.StructField{Name: "hidden", PkgPath: "example.com/private", Type: reflect.TypeFor[string](), Tag: `plystra:"required"`}},
		{"unexported default", reflect.StructField{Name: "hidden", PkgPath: "example.com/private", Type: reflect.TypeFor[string](), Tag: `plystra-default:"private-default"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := object()
			if err := constructorconfig.BindDefaults(&s, reflect.StructOf([]reflect.StructField{tc.field})); !errors.Is(err, constructorconfig.ErrValue) {
				t.Fatalf("invalid hidden metadata was accepted: %v", err)
			}
		})
	}
}

func TestBindDefaultsAcceptsUnrelatedTagsAndValidPrivateDefaultEdits(t *testing.T) {
	type Config struct {
		Value   string `yaml:"" plystra-default:"edited-private-default"`
		Ignored string `yaml:"-"`
		hidden  string `yaml:"-"`
	}
	s := object(constructorconfig.Field{Name: "value", GoName: "Value", HasDefault: true, Value: constructorconfig.Schema{Kind: "string"}})
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
	if result.Value != "edited-private-default" || result.Ignored != "" || result.hidden != "" {
		t.Fatal("private default or excluded field changed")
	}
	typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string](), Tag: `yaml:"value" json:"one" json:"two" plystra:"build-visible,required"`}})
	s = object(constructorconfig.Field{Name: "value", GoName: "Value", Required: true, BuildVisible: true, Value: constructorconfig.Schema{Kind: "string"}})
	if err := constructorconfig.BindDefaults(&s, typ); err != nil {
		t.Fatal("unowned duplicate tags or valid reversed policy rejected")
	}
}
