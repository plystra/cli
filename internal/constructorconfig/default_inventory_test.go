package constructorconfig_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorconfig"
)

func TestRestorePrivateDefaults(t *testing.T) {
	schema := object(constructorconfig.Field{Name: "nested", Value: constructorconfig.Schema{Kind: "pointer", Element: &constructorconfig.Schema{Kind: "object", Fields: []constructorconfig.Field{
		{Name: "value", HasDefault: true, Default: json.RawMessage(`"private-default"`), Value: constructorconfig.Schema{Kind: "string"}},
	}}}}, constructorconfig.Field{Name: "count", HasDefault: true, Default: json.RawMessage(`7`), Value: constructorconfig.Schema{Kind: "signed-integer", Bits: 8}})
	data, err := constructorconfig.DefaultsJSON(schema)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(schema)
	if err != nil || bytes.Contains(encoded, []byte("private-default")) {
		t.Fatal("public schema leaked private default")
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		invalid bool
	}{
		{"canonical", data, false},
		{"missing", []byte(`{}`), true},
		{"unknown", []byte(`{"/count":7,"/nested/*/value":"private-default","private-extra":1}`), true},
		{"overflow", []byte(`{"/count":128,"/nested/*/value":"private-default"}`), true},
		{"wrong type", []byte(`{"/count":"private-invalid","/nested/*/value":"private-default"}`), true},
		{"null", []byte(`{"/count":7,"/nested/*/value":null}`), true},
		{"duplicate", []byte(`{"/count":7,"/count":7,"/nested/*/value":"private-default"}`), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var restored constructorconfig.Schema
			if err := json.Unmarshal(encoded, &restored); err != nil {
				t.Fatal(err)
			}
			err := constructorconfig.RestoreDefaults(&restored, tc.data)
			if (err != nil) != tc.invalid {
				t.Fatalf("RestoreDefaults: %v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "private-") {
					t.Fatal("default leaked")
				}
				return
			}
			roundtrip, err := constructorconfig.DefaultsJSON(restored)
			if err != nil || !bytes.Equal(roundtrip, data) {
				t.Fatal("private defaults did not round trip")
			}
		})
	}
}

func FuzzRestorePrivateDefaults(f *testing.F) {
	f.Add([]byte(`{"/value":"private-default"}`))
	f.Add([]byte(`{"/value":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		schema := object(constructorconfig.Field{Name: "value", HasDefault: true, Value: constructorconfig.Schema{Kind: "string"}})
		if err := constructorconfig.RestoreDefaults(&schema, data); err != nil {
			return
		}
		roundtrip, err := constructorconfig.DefaultsJSON(schema)
		if err != nil || !bytes.Equal(roundtrip, data) {
			t.Fatal("accepted noncanonical private default inventory")
		}
		if _, err := constructorconfig.Normalize(schema, nil); err != nil {
			t.Fatal("accepted unusable private defaults")
		}
	})
}

func TestRestorePrivateScalarDefaults(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		bits    int
		valid   string
		invalid []string
	}{
		{"boolean", 0, "true", []string{`"true"`, "1"}},
		{"unsigned-integer", 8, "255", []string{"256", "-1", `"7"`}},
		{"number", 32, "1.5", []string{"1e100", `"NaN"`, "true"}},
		{"duration", 0, `"1m0s"`, []string{`"private-invalid"`, "60"}},
		{"url", 0, `"https://private.example/path"`, []string{`"https://private.example/%zz"`, "7"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			for i, value := range append([]string{tc.valid}, tc.invalid...) {
				schema := object(constructorconfig.Field{Name: "value", HasDefault: true, Value: constructorconfig.Schema{Kind: tc.kind, Bits: tc.bits}})
				data := []byte(`{"/value":` + value + `}`)
				err := constructorconfig.RestoreDefaults(&schema, data)
				if (err == nil) != (i == 0) {
					t.Fatalf("case %d: RestoreDefaults = %v", i, err)
				}
				if err != nil {
					if strings.Contains(err.Error(), "private-invalid") || strings.Contains(err.Error(), "private.example") {
						t.Fatal("default leaked")
					}
					continue
				}
				if _, err := constructorconfig.Normalize(schema, nil); err != nil {
					t.Fatal("restored default is unusable")
				}
				roundtrip, err := constructorconfig.DefaultsJSON(schema)
				if err != nil || !bytes.Equal(roundtrip, data) {
					t.Fatal("scalar default did not round trip")
				}
			}
		})
	}
}
