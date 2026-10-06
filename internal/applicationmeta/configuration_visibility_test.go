package applicationmeta_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestConstructorValuePublicVisibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field string
		private     []string
		public      []string
	}{
		{"string", "Value string", []string{`"PRIVATE_FIRST"`, `"PRIVATE_SECOND"`, `""`}, nil},
		{"boolean", "Value bool", []string{"true", "false"}, nil},
		{"integer", "Value int64", []string{"0", "-9223372036854775808", "9223372036854775807"}, nil},
		{"unsigned", "Value uint64", []string{"0", "18446744073709551615"}, nil},
		{"number", "Value float64", []string{"0.0", "1.5"}, nil},
		{"duration", "Value time.Duration", []string{"1s", "2h"}, nil},
		{"url", "Value url.URL", []string{"https://private-one.test", "https://private-two.test"}, nil},
		{"pointer", "Value *struct { Private string }", []string{"null", "{}", "{private: PRIVATE_FIRST}"}, nil},
		{"list", "Value []string", []string{"null", "[]", "[PRIVATE_FIRST, PRIVATE_SECOND]"}, nil},
		{"array", "Value [2]string", []string{"[PRIVATE_FIRST, PRIVATE_SECOND]", "['', '']"}, nil},
		{"map", "Value map[string][]*string", []string{"null", "{}", "{PRIVATE_KEY: [PRIVATE_FIRST, null]}"}, nil},
		{"fixed", "Value struct { Private string; Public string `plystra:\"build-visible\"` }", []string{"{private: PRIVATE_FIRST, public: stable}", "{private: PRIVATE_SECOND, public: stable}"}, []string{"{private: PRIVATE_FIRST, public: changed}"}},
		{"inherited", "Value struct { Child struct { Private string } } `plystra:\"build-visible\"`", []string{"{child: {private: first}}"}, []string{"{child: {private: second}}"}},
		{"visible-pointer", "Value *string `plystra:\"build-visible\"`", []string{"first"}, []string{"second", "null"}},
		{"visible-list", "Value []string `plystra:\"build-visible\"`", []string{"[first]"}, []string{"[second]", "[]", "null"}},
		{"mixed-pointer", "Value **struct { Private string; Public string `plystra:\"build-visible\"` }", []string{"{private: PRIVATE_FIRST, public: stable}", "{private: PRIVATE_SECOND, public: stable}", "{public: stable}"}, []string{"{public: changed}", "null", "{}"}},
		{"mixed-list", "Value []struct { Private string; Public string `plystra:\"build-visible\"` }", []string{"[{private: PRIVATE_FIRST, public: stable}]", "[{public: stable}]"}, []string{"[{public: changed}]", "[]", "null", "[{public: stable}, {}]"}},
		{"mixed-array", "Value [2]struct { Private string; Public string `plystra:\"build-visible\"` }", []string{"[{private: PRIVATE_FIRST, public: first}, {public: second}]", "[{public: first}, {private: PRIVATE_SECOND, public: second}]"}, []string{"[{public: second}, {public: first}]"}},
		{"mixed-map", "Value map[string]*struct { Private []string; Public string `plystra:\"build-visible\"` }", []string{"{entry: {private: [PRIVATE_FIRST], public: stable}}", "{entry: {private: null, public: stable}}", "{entry: {public: stable}}"}, []string{"{entry: {public: changed}}", "{other: {public: stable}}", "{entry: null}", "{}", "null"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := composeSchema(t, test.field+"\n")
			lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
			manifest := func(value string) applicationmeta.Manifest {
				return composeManifest(t, "config: {example.com/acme/smtp.New: {value: "+value+"}}")
			}
			for _, wrapper := range []string{"%s"} {
				var first string
				for _, value := range append(append([]string{}, test.private...), test.public...) {
					input := manifest(value)
					if _, err := applicationmeta.ConfigurationDecisions(input, lookup); err != nil {
						t.Fatal(err)
					}
					input = composeManifest(t, fmt.Sprintf(wrapper, "config: {example.com/acme/smtp.New: {value: "+value+"}}"))
					got, err := applicationmeta.ConfigurationLayerDigest(input, lookup)
					if err != nil {
						t.Fatal(err)
					}
					if first == "" {
						first = got
					}
					private := false
					for _, candidate := range test.private {
						private = private || value == candidate
					}
					if (got == first) != private {
						t.Fatalf("public identity equality = %t, want %t", got == first, private)
					}
				}
			}
			if len(test.private) < 2 {
				return
			}
		})
	}
}

func FuzzConstructorValuePublicVisibility(f *testing.F) {
	f.Add([]byte("first"), false)
	f.Add([]byte("second"), true)
	f.Add(bytes.Repeat([]byte("0"), 1024), false)
	schema := composeSchema(f, "Private map[string][]*string\nMixed []struct { Private string; Public string `plystra:\"build-visible\"` }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	digest := func(t testing.TB, values string) string {
		t.Helper()
		manifest := composeManifest(t, "config: {example.com/acme/smtp.New: "+values+"}")
		if _, err := applicationmeta.ConfigurationDecisions(manifest, lookup); err != nil {
			t.Fatal(err)
		}
		got, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	want := digest(f, "{private: null, mixed: [{public: stable}]}")
	f.Fuzz(func(t *testing.T, value []byte, nullable bool) {
		if len(value) > 1024 {
			t.Skip()
		}
		// Explicit keys also represent long dynamic keys beyond YAML's simple-key limit.
		private := fmt.Sprintf("{? PRIVATE_%x : [PRIVATE_%x, null]}", value, value)
		if nullable {
			private = "null"
		}
		values := fmt.Sprintf("{private: %s, mixed: [{private: PRIVATE_%x, public: stable}]}", private, value)
		if digest(t, values) != want {
			t.Fatal("runtime-only contents or shape changed public identity")
		}
	})
}
