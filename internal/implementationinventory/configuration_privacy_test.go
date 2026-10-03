package implementationinventory_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"go/types"
	"log/slog"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationinventory"
)

func TestAnonymousConfigurationTypeDoesNotDiscloseDefaults(t *testing.T) {
	t.Parallel()
	for _, wrap := range []struct {
		name   string
		typeOf func(types.Type) types.Type
	}{
		{"object", func(v types.Type) types.Type { return v }},
		{"pointer", func(v types.Type) types.Type { return types.NewPointer(v) }},
		{"slice", func(v types.Type) types.Type { return types.NewSlice(v) }},
		{"array", func(v types.Type) types.Type { return types.NewArray(v, 2) }},
		{"map", func(v types.Type) types.Type { return types.NewMap(types.Typ[types.String], v) }},
		{"nested", func(v types.Type) types.Type {
			return types.NewSlice(types.NewMap(types.Typ[types.String], types.NewPointer(v)))
		}},
		{"alias", func(v types.Type) types.Type {
			return types.NewAlias(types.NewTypeName(token.NoPos, nil, "Alias", nil), v)
		}},
		{"generic", func(v types.Type) types.Type {
			pkg := types.NewPackage("example.com/shapes", "shapes")
			parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "T", nil), types.NewInterfaceType(nil, nil).Complete())
			list := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Entries", nil), types.NewSlice(parameter), nil)
			list.SetTypeParams([]*types.TypeParam{parameter})
			instance, err := types.Instantiate(nil, list, []types.Type{v}, true)
			if err != nil {
				t.Fatal(err)
			}
			return instance
		}},
	} {
		t.Run(wrap.name, func(t *testing.T) {
			var first string
			for _, literal := range []string{"PRIVATE_FIRST", "PRIVATE_SECOND"} {
				object := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Value", types.Typ[types.String], false)}, []string{fmt.Sprintf(`yaml:"value" plystra-default:%q`, literal)})
				authored := wrap.typeOf(object)
				before := types.TypeString(authored, nil)
				configuration, err := buildPrivacyConfiguration(t, authored)
				if err != nil {
					t.Fatal(err)
				}
				if types.TypeString(authored, nil) != before {
					t.Fatal("compilation mutated the authored Go type")
				}
				field, exists := configuration.Lookup("value")
				if !exists {
					t.Fatal("missing compiled field")
				}
				identity := field.TypeIdentity()
				if first == "" {
					first = identity
				}
				if strings.Contains(identity, "PRIVATE_") || identity != first {
					t.Error("private defaults entered public type identity")
				}
				value := field.Value()
				for {
					element, ok := value.Element()
					if !ok {
						break
					}
					value = element
				}
				child := value.Fields()[0]
				if !child.HasDefault() || string(child.DefaultJSON()) != fmt.Sprintf("%q", literal) {
					t.Fatal("private default accessor lost its value")
				}
				for _, formatted := range []string{fmt.Sprintf("%v", field), fmt.Sprintf("%+v", field), fmt.Sprintf("%#v", field), fmt.Sprintf("%v", field.Value()), fmt.Sprintf("%+v", field.Value()), fmt.Sprintf("%#v", field.Value()), fmt.Sprintf("%#v", configuration.Fields())} {
					if strings.Contains(formatted, "PRIVATE_") {
						t.Error("schema formatting disclosed a private default")
					}
				}
				var logged bytes.Buffer
				slog.New(slog.NewJSONHandler(&logged, nil)).Info("schema", "field", field, "value", field.Value(), "child", child)
				if strings.Contains(logged.String(), "PRIVATE_") || !strings.Contains(logged.String(), `"has_default":true`) {
					t.Error("structured schema log disclosed a default or lost its presence")
				}
			}
		})
	}
}

func TestInvalidConfigurationTypeDoesNotDiscloseNestedDefaults(t *testing.T) {
	t.Parallel()
	object := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Value", types.Typ[types.String], false)}, []string{`plystra-default:"PRIVATE_INVALID"`})
	for _, value := range []types.Type{types.NewChan(types.SendRecv, object), types.NewMap(object, types.Typ[types.String])} {
		_, err := buildPrivacyConfiguration(t, value)
		if err == nil || strings.Contains(err.Error(), "PRIVATE_") {
			t.Error("invalid type accepted or nested default disclosed")
		}
	}
	pkg := types.NewPackage("example.com/generic", "generic")
	parameter := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "T", nil), types.NewInterfaceType(nil, nil).Complete())
	config := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Config", nil), types.NewStruct(nil, nil), nil)
	config.SetTypeParams([]*types.TypeParam{parameter})
	instance, err := types.Instantiate(nil, config, []types.Type{object}, true)
	if err != nil {
		t.Fatal(err)
	}
	parameters := types.NewTuple(types.NewVar(token.NoPos, pkg, "config", types.NewPointer(instance)))
	constructor := types.NewFunc(token.NoPos, pkg, "New", types.NewSignatureType(nil, nil, nil, parameters, nil, false))
	if _, _, err := implementationinventory.CompileConfiguration(pkg, constructor); err == nil || !strings.Contains(err.Error(), "struct value") || strings.Contains(err.Error(), "PRIVATE_") {
		t.Fatal("invalid generic Config parameter accepted or disclosed its default")
	}
}

func TestRejectedConfigurationDefaultsDoNotDiscloseLiterals(t *testing.T) {
	t.Parallel()
	durationPackage := types.NewPackage("time", "time")
	duration := types.NewNamed(types.NewTypeName(token.NoPos, durationPackage, "Duration", nil), types.Typ[types.Int64], nil)
	urlPackage := types.NewPackage("net/url", "url")
	url := types.NewNamed(types.NewTypeName(token.NoPos, urlPackage, "URL", nil), types.NewStruct(nil, nil), nil)
	for _, test := range []struct {
		name    string
		value   types.Type
		literal string
	}{
		{"boolean", types.Typ[types.Bool], "PRIVATE_DEFAULT"},
		{"signed-syntax", types.Typ[types.Int64], "PRIVATE_DEFAULT"},
		{"signed-width", types.Typ[types.Int8], "123456789"},
		{"signed-platform", types.Typ[types.Int], "123456789012345"},
		{"unsigned-syntax", types.Typ[types.Uint64], "PRIVATE_DEFAULT"},
		{"unsigned-width", types.Typ[types.Uint8], "123456789"},
		{"unsigned-platform", types.Typ[types.Uint], "123456789012345"},
		{"float", types.Typ[types.Float64], "PRIVATE_DEFAULT"},
		{"duration", duration, "PRIVATE_DEFAULT"},
		{"url", url, "https://PRIVATE_DEFAULT/%zz"},
	} {
		t.Run(test.name, func(t *testing.T) {
			object := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Literal", test.value, false)}, []string{fmt.Sprintf("plystra-default:%q", test.literal)})
			_, err := buildPrivacyConfiguration(t, object)
			if err == nil || strings.Contains(err.Error(), test.literal) || !strings.Contains(err.Error(), "Literal") || !strings.Contains(err.Error(), "default") {
				t.Fatalf("unsafe or missing default diagnostic: %v", err)
			}
		})
	}
}

func FuzzConfigurationTypeIdentityExcludesTagLiterals(f *testing.F) {
	f.Add([]byte("plain"))
	f.Add([]byte("\"quoted\"\\path\nnext `tag`"))
	f.Add([]byte{0, 255, 128})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1024 {
			t.Skip()
		}
		literal := "PRIVATE_" + string(input)
		object := types.NewStruct([]*types.Var{types.NewField(token.NoPos, nil, "Value", types.Typ[types.String], false)}, []string{fmt.Sprintf(`yaml:"renamed" plystra:"build-visible" plystra-default:%q json:"PRIVATE_OTHER_TAG"`, literal)})
		configuration, err := buildPrivacyConfiguration(t, types.NewSlice(object))
		if err != nil {
			t.Fatal(err)
		}
		field, _ := configuration.Lookup("value")
		if field.TypeIdentity() != "[]struct{Value string}" {
			t.Fatal("type identity retained tag literals")
		}
		element, _ := field.Value().Element()
		child := element.Fields()[0]
		want, err := json.Marshal(literal)
		if err != nil {
			t.Fatal(err)
		}
		if child.Name() != "renamed" || !child.BuildVisible() || child.Required() || !bytes.Equal(child.DefaultJSON(), want) {
			t.Fatal("type redaction changed compiled field policy or private default")
		}
	})
}

func buildPrivacyConfiguration(t testing.TB, value types.Type) (implementationinventory.Configuration, error) {
	t.Helper()
	compiled := compiledConfigurationPackage("example.com/app/service", "service", []configurationTestField{{name: "Value", fieldType: value}})
	index, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath: "example.com/app", PackagePath: "example.com/app/service", Local: true,
		Declaration: declaration(t, "service/implementation.go", "service", "New", "service.operation.run/v1"), Types: compiled,
	}}, []implementationinventory.InterfaceInput{canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")})
	if err != nil {
		return implementationinventory.Configuration{}, err
	}
	configuration, _ := index.Implementations()[0].Configuration()
	return configuration, nil
}
