package bootstrapgen_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/bootstrapgen"
)

func TestApplicationModelCompatibilityIsDeterministicAndSecretFree(t *testing.T) {
	t.Parallel()

	first := compatibilityManifest(t, `
http:
  address: ":8080"
  transports: {connect: true, rest: true}
  cors:
    allowed_origins: [https://b.example, https://a.example, https://a.example]
    allow_credentials: true
  expose: [records.read/v1]
timeouts: {startup: 45s}
interfaces:
  require: [records.read/v1]
  use: {records.read/v1: example.com/acme/records.New}
config:
  acme.records:
    endpoint: runtime-private-one
    token: {env: PRIVATE_TOKEN_ONE}
`)
	second := compatibilityManifest(t, `
config:
  acme.records:
    token: {env: PRIVATE_TOKEN_TWO}
    endpoint: runtime-private-two
interfaces:
  use: {records.read/v1: example.com/acme/records.New}
  require: [records.read/v1]
timeouts: {startup: 2m}
http:
  expose: [records.read/v1]
  cors:
    allow_credentials: true
    allowed_origins: [https://a.example, https://b.example]
  transports: {rest: true, connect: true}
  address: ":9090"
`)
	digest := bootstrapDigest("a")
	left, err := bootstrapgen.NewApplicationModelCompatibility(digest, first)
	if err != nil || !left.Valid() {
		t.Fatalf("NewApplicationModelCompatibility(first) = %#v, %v", left, err)
	}
	right, err := bootstrapgen.NewApplicationModelCompatibility(digest, second)
	if err != nil || !right.Valid() {
		t.Fatalf("NewApplicationModelCompatibility(second) = %#v, %v", right, err)
	}
	if left.Digest() != right.Digest() || !bytes.Equal(left.CanonicalJSON(), right.CanonicalJSON()) {
		t.Fatalf("equivalent manifests produced different compatibility projections:\n%s\n%s", left.CanonicalJSON(), right.CanonicalJSON())
	}
	canonical := string(left.CanonicalJSON())
	for _, required := range []string{
		`"application_model_digest":"` + digest + `"`,
		`"http_transports":{"connect":true,"rest":true}`,
		`"http_exposures":["records.read/v1"]`,
		`"interface_requirements":["records.read/v1"]`,
		`"interface":"records.read/v1"`,
		`"constructor":"example.com/acme/records.New"`,
	} {
		if !strings.Contains(canonical, required) {
			t.Fatalf("canonical projection omits %q: %s", required, canonical)
		}
	}
	for _, forbidden := range []string{
		":8080",
		":9090",
		"45s",
		"2m",
		"runtime-private-one",
		"runtime-private-two",
		"PRIVATE_TOKEN_ONE",
		"PRIVATE_TOKEN_TWO",
		`"config"`,
		`"secret"`,
	} {
		if strings.Contains(canonical, forbidden) {
			t.Fatalf("canonical projection contains runtime-only or secret-bearing input %q: %s", forbidden, canonical)
		}
	}
	copyOfCanonical := left.CanonicalJSON()
	copyOfCanonical[0] = '!'
	if !left.Valid() || bytes.Equal(copyOfCanonical, left.CanonicalJSON()) || left.ApplicationModelDigest() != digest {
		t.Fatal("compatibility accessors did not preserve immutable constructor state")
	}
}

func TestApplicationModelCompatibilityChangesForEveryBuildAffectingDeclaration(t *testing.T) {
	t.Parallel()

	base, err := bootstrapgen.NewApplicationModelCompatibility(bootstrapDigest("b"), compatibilityManifest(t, "{}\n"))
	if err != nil {
		t.Fatalf("NewApplicationModelCompatibility(base): %v", err)
	}
	tests := []struct {
		name string
		yaml string
	}{
		{name: "transport", yaml: "http: {transports: {connect: false}}\n"},
		{name: "CORS", yaml: "http: {cors: {allowed_origins: [https://app.example]}}\n"},
		{name: "exposure", yaml: "http: {expose: [kernel.health/v1]}\n"},
		{name: "Interface requirement", yaml: "interfaces: {require: [records.read/v1]}\n"},
		{name: "Implementation choice", yaml: "interfaces: {use: {records.read/v1: example.com/acme/records.New}}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed, err := bootstrapgen.NewApplicationModelCompatibility(bootstrapDigest("b"), compatibilityManifest(t, test.yaml))
			if err != nil {
				t.Fatalf("NewApplicationModelCompatibility: %v", err)
			}
			if changed.Digest() == base.Digest() || bytes.Equal(changed.CanonicalJSON(), base.CanonicalJSON()) {
				t.Fatalf("%s did not change compatibility projection: %s", test.name, changed.CanonicalJSON())
			}
		})
	}
}

func TestApplicationModelCompatibilityRejectsInvalidCompiledDigest(t *testing.T) {
	t.Parallel()

	for _, digest := range []string{"", "sha256:ABC", "sha256:" + strings.Repeat("g", 64)} {
		compatibility, err := bootstrapgen.NewApplicationModelCompatibility(digest, applicationmeta.Manifest{})
		if compatibility.Valid() || !errors.Is(err, bootstrapgen.ErrInvalidApplicationModelCompatibility) {
			t.Fatalf("NewApplicationModelCompatibility(%q) = %#v, %v", digest, compatibility, err)
		}
	}
}

func compatibilityManifest(t testing.TB, source string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(source))
	if err != nil {
		t.Fatalf("applicationmeta.Parse: %v\n%s", err, source)
	}
	return manifest
}
