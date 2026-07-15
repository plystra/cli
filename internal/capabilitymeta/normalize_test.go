package capabilitymeta_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
)

const normalizationInput = `id: account.register/v2
description: Registers an account.
request:
  roles:
    type: array
    items: string
    required: true
  email:
    enum: [work, personal]
    required: true
    type: string
  attempts:
    type: integer
    enum: [-1, 0, 2]
response:
  account_id: {type: string, required: true}
errors: [temporarily_unavailable, already_exists]
`

func TestNormalizeSchemaGolden(t *testing.T) {
	t.Parallel()

	got, err := capabilitymeta.NormalizeSchema([]byte(normalizationInput))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	want, err := os.ReadFile("testdata/account.register.v2.canonical.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want = bytes.TrimSpace(want)
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical schema:\n got: %s\nwant: %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("NormalizeSchema returned invalid JSON: %s", got)
	}
}

func TestNormalizeSchemaIgnoresNonSemanticDifferences(t *testing.T) {
	t.Parallel()

	first, err := capabilitymeta.NormalizeSchema([]byte(normalizationInput))
	if err != nil {
		t.Fatalf("NormalizeSchema(first): %v", err)
	}
	second, err := capabilitymeta.NormalizeSchema([]byte("errors: [already_exists, temporarily_unavailable]\r\nresponse: {account_id: {required: true, type: string}}\r\nrequest:\r\n  email: {type: string, required: true, enum: [personal, work]}\r\n  attempts: {enum: [2, -1, 0], required: false, type: integer}\r\n  roles: {required: true, items: string, type: array}\r\ndescription: Provider-specific wording.\r\nid: account.register/v2\r\n"))
	if err != nil {
		t.Fatalf("NormalizeSchema(second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("non-semantic differences changed schema:\nfirst:  %s\nsecond: %s", first, second)
	}

	empty, err := capabilitymeta.NormalizeSchema([]byte("id: kernel.health/v1\n"))
	if err != nil {
		t.Fatalf("NormalizeSchema(empty): %v", err)
	}
	explicitEmpty, err := capabilitymeta.NormalizeSchema([]byte("id: kernel.health/v1\nrequest: {}\nresponse: {}\nerrors: []\n"))
	if err != nil || !bytes.Equal(empty, explicitEmpty) {
		t.Fatalf("empty defaults = %s and %s, %v", empty, explicitEmpty, err)
	}
}

func TestNormalizeSchemaNormalizesExtensions(t *testing.T) {
	t.Parallel()

	first, err := capabilitymeta.NormalizeSchema([]byte(`id: order.cancel/v1
extensions:
  authz:
    resource:
      required: true
      kind: order
    permission: order.cancel
  authn:
    authenticated: true
    methods: [password, passkey]
`))
	if err != nil {
		t.Fatalf("NormalizeSchema(first): %v", err)
	}
	second, err := capabilitymeta.NormalizeSchema([]byte(`extensions:
  authn: {methods: [password, passkey], authenticated: true}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
id: order.cancel/v1
`))
	if err != nil {
		t.Fatalf("NormalizeSchema(second): %v", err)
	}
	want := `{"id":"order.cancel/v1","request":{},"response":{},"errors":[],"extensions":{"authn":{"authenticated":true,"methods":["password","passkey"]},"authz":{"permission":"order.cancel","resource":{"kind":"order","required":true}}}}`
	if string(first) != want || !bytes.Equal(first, second) {
		t.Fatalf("canonical extensions:\nfirst:  %s\nsecond: %s\nwant:   %s", first, second, want)
	}

	changed, err := capabilitymeta.NormalizeSchema([]byte(`id: order.cancel/v1
extensions:
  authn: {authenticated: false, methods: [password, passkey]}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
`))
	if err != nil {
		t.Fatalf("NormalizeSchema(changed): %v", err)
	}
	if bytes.Equal(first, changed) {
		t.Fatal("changed extension value did not change exact contract")
	}
	reorderedArray, err := capabilitymeta.NormalizeSchema([]byte(`id: order.cancel/v1
extensions:
  authn: {authenticated: true, methods: [passkey, password]}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
`))
	if err != nil {
		t.Fatalf("NormalizeSchema(reorderedArray): %v", err)
	}
	if bytes.Equal(first, reorderedArray) {
		t.Fatal("changed extension array order did not change exact contract")
	}

	omitted, err := capabilitymeta.NormalizeSchema([]byte("id: kernel.health/v1\n"))
	if err != nil {
		t.Fatalf("NormalizeSchema(omitted): %v", err)
	}
	empty, err := capabilitymeta.NormalizeSchema([]byte("id: kernel.health/v1\nextensions: {}\n"))
	if err != nil || !bytes.Equal(omitted, empty) {
		t.Fatalf("empty extensions = %s and %s, %v", omitted, empty, err)
	}
}

func TestNormalizeSchemaPreservesSemanticDifferences(t *testing.T) {
	t.Parallel()

	baseline, err := capabilitymeta.NormalizeSchema([]byte("id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\n"))
	if err != nil {
		t.Fatalf("NormalizeSchema(baseline): %v", err)
	}
	tests := map[string]string{
		"identity":            "id: example.check/v2\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\n",
		"type":                "id: example.check/v1\nrequest:\n  value: {type: integer}\nerrors: [invalid_value]\n",
		"required":            "id: example.check/v1\nrequest:\n  value: {type: string, required: true}\nerrors: [invalid_value]\n",
		"enum":                "id: example.check/v1\nrequest:\n  value: {type: string, enum: [one]}\nerrors: [invalid_value]\n",
		"error":               "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: []\n",
		"extension namespace": "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\nextensions: {authn: {authenticated: true}}\n",
		"extension value":     "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\nextensions: {authz: {permission: example.read}}\n",
	}
	for name, input := range tests {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed, err := capabilitymeta.NormalizeSchema([]byte(input))
			if err != nil {
				t.Fatalf("NormalizeSchema: %v", err)
			}
			if bytes.Equal(baseline, changed) {
				t.Fatalf("semantic %s change did not change canonical schema", name)
			}
		})
	}
}

func TestNormalizeSchemaRejectsInvalidWireShapes(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"description":    "id: example.check/v1\ndescription: 1\n",
		"request":        "id: example.check/v1\nrequest: []\n",
		"field name":     "id: example.check/v1\nrequest:\n  BadName: {type: string}\n",
		"field type":     "id: example.check/v1\nrequest:\n  value: {type: bytes}\n",
		"required":       "id: example.check/v1\nrequest:\n  value: {type: string, required: yes}\n",
		"array items":    "id: example.check/v1\nrequest:\n  value: {type: array}\n",
		"enum type":      "id: example.check/v1\nrequest:\n  value: {type: integer, enum: [one]}\n",
		"duplicate enum": "id: example.check/v1\nrequest:\n  value: {type: number, enum: [1, 1.0]}\n",
		"error code":     "id: example.check/v1\nerrors: [InvalidValue]\n",
		"extensions":     "id: example.check/v1\nextensions: []\n",
	}
	for name, input := range tests {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := capabilitymeta.NormalizeSchema([]byte(input))
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) || got != nil {
				t.Fatalf("NormalizeSchema = %s, %v", got, err)
			}
		})
	}
}

func FuzzNormalizeSchema(f *testing.F) {
	for _, seed := range []string{normalizationInput, "id: kernel.health/v1\n", "id: order.cancel/v1\nextensions:\n  authz: {permission: order.cancel}\n", "[]\n", "id: &x example.check/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := capabilitymeta.NormalizeSchema([]byte(input))
		if err != nil {
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) {
				t.Fatalf("NormalizeSchema returned unexpected error: %v", err)
			}
			return
		}
		if !json.Valid(first) {
			t.Fatalf("NormalizeSchema returned invalid JSON: %s", first)
		}
		second, err := capabilitymeta.NormalizeSchema([]byte(input))
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("normalization is not deterministic: %s then %s, %v", first, second, err)
		}
	})
}

func BenchmarkSchemaNormalization(b *testing.B) {
	b.ReportAllocs()
	input := []byte(normalizationInput)
	for b.Loop() {
		if _, err := capabilitymeta.NormalizeSchema(input); err != nil {
			b.Fatal(err)
		}
	}
}
