package capabilitymeta_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
)

func TestParseCapabilityExtensions(t *testing.T) {
	t.Parallel()

	metadata, err := capabilitymeta.Parse([]byte(`id: order.cancel/v1
` + querySemanticsYAML + `
extensions:
  rate-limit: 5
  telemetry: [null, true, 1, 1.5, sample]
  authz:
    resource:
      required: true
      kind: order
    permission: order.cancel
  authn:
    methods: [password, passkey]
    authenticated: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := metadata.ID().String(); got != "order.cancel/v1" {
		t.Fatalf("ID() = %q, want order.cancel/v1", got)
	}

	values := metadata.Extensions().Values()
	if len(values) != 4 {
		t.Fatalf("Extensions().Values() = %#v", values)
	}
	want := []struct {
		namespace string
		value     string
	}{
		{namespace: "authn", value: `{"authenticated":true,"methods":["password","passkey"]}`},
		{namespace: "authz", value: `{"permission":"order.cancel","resource":{"kind":"order","required":true}}`},
		{namespace: "rate-limit", value: `5`},
		{namespace: "telemetry", value: `[null,true,1,1.5,"sample"]`},
	}
	for index, expected := range want {
		if values[index].Namespace() != expected.namespace || string(values[index].ValueJSON()) != expected.value {
			t.Fatalf("extension[%d] = %q %s, want %q %s", index, values[index].Namespace(), values[index].ValueJSON(), expected.namespace, expected.value)
		}
		lookup, ok := metadata.Extensions().Lookup(expected.namespace)
		if !ok || lookup.Namespace() != expected.namespace || string(lookup.ValueJSON()) != expected.value {
			t.Fatalf("Extensions().Lookup(%q) = %#v, %t", expected.namespace, lookup, ok)
		}
	}
	if _, ok := metadata.Extensions().Lookup("missing"); ok {
		t.Fatal("Extensions().Lookup(missing) succeeded")
	}

	values[0] = capabilitymeta.CapabilityExtension{}
	encoded := metadata.Extensions().Values()[0].ValueJSON()
	encoded[0] = 'x'
	if got := metadata.Extensions().Values()[0]; got.Namespace() != "authn" || string(got.ValueJSON()) != want[0].value {
		t.Fatal("Capability extension accessors exposed mutable storage")
	}
}

func TestParseNormalizesEmptyCapabilityExtensions(t *testing.T) {
	t.Parallel()

	omitted, err := capabilitymeta.Parse([]byte("id: example.health/v1\n" + querySemanticsYAML + "\n"))
	if err != nil {
		t.Fatalf("Parse(omitted): %v", err)
	}
	empty, err := capabilitymeta.Parse([]byte("id: example.health/v1\nextensions: {}\n" + querySemanticsYAML + "\n"))
	if err != nil {
		t.Fatalf("Parse(empty): %v", err)
	}
	if len(omitted.Extensions().Values()) != 0 || len(empty.Extensions().Values()) != 0 {
		t.Fatalf("empty extensions = %#v and %#v", omitted.Extensions().Values(), empty.Extensions().Values())
	}
}

func TestParseRejectsInvalidCapabilityExtensions(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not mapping":           "extensions: []\n",
		"non string namespace":  "extensions:\n  1: true\n",
		"empty namespace":       "extensions:\n  '': {}\n",
		"long namespace":        "extensions:\n  " + strings.Repeat("a", 129) + ": {}\n",
		"upper namespace":       "extensions:\n  AuthN: {}\n",
		"dotted namespace":      "extensions:\n  acme.authn: {}\n",
		"leading digit":         "extensions:\n  1authn: {}\n",
		"consecutive hyphen":    "extensions:\n  auth--n: {}\n",
		"trailing hyphen":       "extensions:\n  authn-: {}\n",
		"duplicate namespace":   "extensions:\n  authn: {}\n  authn: {}\n",
		"duplicate object key":  "extensions:\n  authn: {authenticated: true, authenticated: false}\n",
		"non string object key": "extensions:\n  authn: {1: true}\n",
		"noncanonical integer":  "extensions:\n  rate-limit: 01\n",
		"nonfinite number":      "extensions:\n  rate-limit: .nan\n",
		"unsupported scalar":    "extensions:\n  audit: 2026-07-15\n",
		"value too deep":        "extensions:\n  audit: " + strings.Repeat("[", 65) + "true" + strings.Repeat("]", 65) + "\n",
	}
	for name, extension := range tests {
		name, extension := name, extension
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			metadata, err := capabilitymeta.Parse([]byte("id: example.check/v1\n" + querySemanticsYAML + "\n" + extension))
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest", err)
			}
			if metadata.ID().String() != "" || len(metadata.Extensions().Values()) != 0 {
				t.Fatalf("invalid Parse returned %#v", metadata)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"id: example.health/v1\n" + querySemanticsYAML + "\n",
		"id: order.cancel/v1\n" + querySemanticsYAML + "\nextensions:\n  authn: {authenticated: true}\n",
		"id: example.check/v1\n" + querySemanticsYAML + "\nextensions:\n  sample: [null, true, 1, 1.5, value, {nested: false}]\n",
		"id: example.check/v1\nextensions: &metadata {authn: {authenticated: true}}\n",
		"[]\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := capabilitymeta.Parse([]byte(input))
		if err != nil {
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) {
				t.Fatalf("Parse returned unexpected error: %v", err)
			}
			return
		}
		if first.ID().String() == "" {
			t.Fatal("Parse returned metadata without an ID")
		}
		values := first.Extensions().Values()
		for index, extension := range values {
			if !json.Valid(extension.ValueJSON()) {
				t.Fatalf("extension %q is not canonical JSON: %q", extension.Namespace(), extension.ValueJSON())
			}
			if index > 0 && values[index-1].Namespace() >= extension.Namespace() {
				t.Fatalf("extensions are not uniquely sorted: %q then %q", values[index-1].Namespace(), extension.Namespace())
			}
		}

		second, err := capabilitymeta.Parse([]byte(input))
		if err != nil || first.ID() != second.ID() || !reflect.DeepEqual(extensionPairs(first), extensionPairs(second)) {
			t.Fatalf("Parse is not deterministic: %#v then %#v, %v", first, second, err)
		}
	})
}

func extensionPairs(metadata capabilitymeta.Manifest) [][]byte {
	values := metadata.Extensions().Values()
	result := make([][]byte, len(values))
	for index, extension := range values {
		result[index] = bytes.Join([][]byte{[]byte(extension.Namespace()), extension.ValueJSON()}, []byte{0})
	}
	return result
}
