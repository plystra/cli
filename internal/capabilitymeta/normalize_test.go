package capabilitymeta_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
)

const querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}`

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
` + querySemanticsYAML + `
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

func TestNormalizeSchemaUsesKernelTypedSemantics(t *testing.T) {
	t.Parallel()

	canonical, metadata, err := capabilitymeta.NormalizeSchemaAndManifest([]byte(normalizationInput))
	if err != nil {
		t.Fatalf("NormalizeSchemaAndManifest: %v", err)
	}
	semantics := metadata.Semantics()
	if semantics.Kind() != capabilitymeta.CapabilityKindQuery || semantics.Effects() != capabilitymeta.CapabilityEffectsNone || semantics.Idempotency().Mode() != capabilitymeta.IdempotencyModeInherent || semantics.Retry().Safety() != capabilitymeta.RetrySafetySafe || semantics.Data().Response() != capabilitymeta.DataClassificationPublic {
		t.Fatalf("Semantics() = %#v", semantics)
	}

	for name, declaration := range map[string]string{
		"missing":             "id: example.check/v1\n",
		"unknown kind":        "id: example.check/v1\n" + strings.Replace(querySemanticsYAML, "kind: query", "kind: job", 1) + "\n",
		"contradictory query": "id: example.check/v1\n" + strings.Replace(querySemanticsYAML, "effects: none", "effects: local", 1) + "\n",
	} {
		name, declaration := name, declaration
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, manifest, err := capabilitymeta.NormalizeSchemaAndManifest([]byte(declaration))
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) || got != nil || manifest.ID().String() != "" {
				t.Fatalf("NormalizeSchemaAndManifest = %s, %#v, %v", got, manifest, err)
			}
		})
	}

	parsed, err := capabilitymeta.Parse(canonical)
	if err != nil || parsed.ID() != metadata.ID() || parsed.Semantics() != metadata.Semantics() {
		t.Fatalf("combined planning view differs from Parse(canonical): %#v then %#v, %v", metadata, parsed, err)
	}
}

func TestNormalizeSchemaExposesInvocationPolicySemantics(t *testing.T) {
	t.Parallel()

	declaration := `id: email.send/v1
request:
  idempotency_key: {type: string, required: true}
  partition: {type: integer, required: true}
semantics:
  kind: command
  effects: external-write
  idempotency:
    mode: keyed
    request_field: idempotency_key
  retry: {safety: requires-idempotency-key}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering:
    mode: per-key
    request_field: partition
  data: {request: confidential, response: restricted}
`
	_, metadata, err := capabilitymeta.NormalizeSchemaAndManifest([]byte(declaration))
	if err != nil {
		t.Fatalf("NormalizeSchemaAndManifest: %v", err)
	}
	semantics := metadata.Semantics()
	if semantics.Kind() != capabilitymeta.CapabilityKindCommand ||
		semantics.Effects() != capabilitymeta.CapabilityEffectsExternalWrite ||
		semantics.Idempotency().Mode() != capabilitymeta.IdempotencyModeKeyed ||
		semantics.Idempotency().RequestField() != "idempotency_key" ||
		semantics.Retry().Safety() != capabilitymeta.RetrySafetyRequiresIdempotencyKey ||
		semantics.Cancellation().Mode() != capabilitymeta.CancellationModeBestEffort ||
		semantics.Completion().Mode() != capabilitymeta.CompletionModeCompletedBeforeReturn ||
		semantics.Ordering().Mode() != capabilitymeta.OrderingModePerKey ||
		semantics.Ordering().RequestField() != "partition" ||
		semantics.Data().Request() != capabilitymeta.DataClassificationConfidential ||
		semantics.Data().Response() != capabilitymeta.DataClassificationRestricted {
		t.Fatalf("invocation-policy semantics = %#v", semantics)
	}
}

func TestNormalizeSchemaIgnoresNonSemanticDifferences(t *testing.T) {
	t.Parallel()

	first, err := capabilitymeta.NormalizeSchema([]byte(normalizationInput))
	if err != nil {
		t.Fatalf("NormalizeSchema(first): %v", err)
	}
	secondSource := "errors: [already_exists, temporarily_unavailable]\r\nresponse: {account_id: {required: true, type: string}}\r\nrequest:\r\n  email: {type: string, required: true, enum: [personal, work]}\r\n  attempts: {enum: [2, -1, 0], required: false, type: integer}\r\n  roles: {required: true, items: string, type: array}\r\ndescription: Provider-specific wording.\r\nid: account.register/v2\r\n" + strings.ReplaceAll(querySemanticsYAML, "\n", "\r\n") + "\r\n"
	second, err := capabilitymeta.NormalizeSchema([]byte(secondSource))
	if err != nil {
		t.Fatalf("NormalizeSchema(second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("non-semantic differences changed schema:\nfirst:  %s\nsecond: %s", first, second)
	}

	empty, err := capabilitymeta.NormalizeSchema([]byte("id: example.health/v1\n" + querySemanticsYAML + "\n"))
	if err != nil {
		t.Fatalf("NormalizeSchema(empty): %v", err)
	}
	explicitEmpty, err := capabilitymeta.NormalizeSchema([]byte("id: example.health/v1\nrequest: {}\nresponse: {}\nerrors: []\n" + querySemanticsYAML + "\n"))
	if err != nil || !bytes.Equal(empty, explicitEmpty) {
		t.Fatalf("empty defaults = %s and %s, %v", empty, explicitEmpty, err)
	}
}

func TestNormalizeSchemaNormalizesExtensions(t *testing.T) {
	t.Parallel()

	firstSource := `id: order.cancel/v1
` + querySemanticsYAML + `
extensions:
  authz:
    resource: {required: true, kind: order}
    permission: order.cancel
  authn: {authenticated: true, methods: [password, passkey]}
`
	secondSource := `extensions:
  authn: {methods: [password, passkey], authenticated: true}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
id: order.cancel/v1
` + querySemanticsYAML + `
`
	first, err := capabilitymeta.NormalizeSchema([]byte(firstSource))
	if err != nil {
		t.Fatalf("NormalizeSchema(first): %v", err)
	}
	second, err := capabilitymeta.NormalizeSchema([]byte(secondSource))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("canonical extensions:\nfirst:  %s\nsecond: %s, %v", first, second, err)
	}
	if !bytes.Contains(first, []byte(`"extensions":{"authn":{"authenticated":true,"methods":["password","passkey"]},"authz":{"permission":"order.cancel","resource":{"kind":"order","required":true}}}`)) {
		t.Fatalf("canonical extensions missing: %s", first)
	}

	changed, err := capabilitymeta.NormalizeSchema([]byte(strings.Replace(firstSource, "authenticated: true", "authenticated: false", 1)))
	if err != nil || bytes.Equal(first, changed) {
		t.Fatalf("changed extension = %s, %v", changed, err)
	}
	reorderedArray, err := capabilitymeta.NormalizeSchema([]byte(strings.Replace(firstSource, "password, passkey", "passkey, password", 1)))
	if err != nil || bytes.Equal(first, reorderedArray) {
		t.Fatalf("reordered extension array = %s, %v", reorderedArray, err)
	}
}

func TestNormalizeSchemaPreservesEveryExactContractDifference(t *testing.T) {
	t.Parallel()

	base := "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\n" + querySemanticsYAML + "\n"
	baseline, err := capabilitymeta.NormalizeSchema([]byte(base))
	if err != nil {
		t.Fatalf("NormalizeSchema(baseline): %v", err)
	}
	tests := map[string]string{
		"identity":            strings.Replace(base, "example.check/v1", "example.check/v2", 1),
		"type":                strings.Replace(base, "type: string", "type: integer", 1),
		"required":            strings.Replace(base, "type: string", "type: string, required: true", 1),
		"enum":                strings.Replace(base, "type: string", "type: string, enum: [one]", 1),
		"error":               strings.Replace(base, "errors: [invalid_value]", "errors: []", 1),
		"typed semantics":     strings.Replace(base, "response: public", "response: internal", 1),
		"extension namespace": base + "extensions: {authn: {authenticated: true}}\n",
		"extension value":     base + "extensions: {authz: {permission: example.read}}\n",
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
				t.Fatalf("%s change did not change canonical schema", name)
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
			got, err := capabilitymeta.NormalizeSchema([]byte(input + querySemanticsYAML + "\n"))
			if !errors.Is(err, capabilitymeta.ErrInvalidManifest) || got != nil {
				t.Fatalf("NormalizeSchema = %s, %v", got, err)
			}
		})
	}
}

func FuzzNormalizeSchema(f *testing.F) {
	for _, seed := range []string{normalizationInput, "id: example.health/v1\n" + querySemanticsYAML + "\n", "id: order.cancel/v1\nextensions:\n  authz: {permission: order.cancel}\n" + querySemanticsYAML + "\n", "[]\n", "id: &x example.check/v1\ndescription: *x\n"} {
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
