package applicationmeta_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
)

func TestAddHTTPExposurePreservesApplicationSemantics(t *testing.T) {
	t.Parallel()

	input := []byte(`# Application settings.
timeouts:
  startup: 45s
capabilities:
  require: [kernel.info/v1]
  use: {}
  aliases:
    health.status/v1:
      target: kernel.health/v1
      expose: {go: true, http: false, javascript: false}
config:
  acme.mail:
    password:
      env: SMTP_PASSWORD
`)
	id := mustExposureID(t, "kernel.health/v1")
	updated, changed, err := applicationmeta.AddHTTPExposure(input, id)
	if err != nil || !changed {
		t.Fatalf("AddHTTPExposure = changed %t, %v", changed, err)
	}
	for _, retained := range [][]byte{
		[]byte("# Application settings."),
		[]byte("startup: 45s"),
		[]byte("health.status/v1:"),
		[]byte("env: SMTP_PASSWORD"),
		[]byte("expose:\n    - kernel.health/v1"),
	} {
		if !bytes.Contains(updated, retained) {
			t.Fatalf("updated manifest omits %q:\n%s", retained, updated)
		}
	}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil || len(manifest.HTTPExposures()) != 1 || manifest.HTTPExposures()[0].ID() != id {
		t.Fatalf("updated manifest exposures = %#v, %v", manifest.HTTPExposures(), err)
	}
	updated[0] = 'x'
	repeated, repeatedChanged, err := applicationmeta.AddHTTPExposure(input, id)
	if err != nil || !repeatedChanged || bytes.Equal(updated, repeated) {
		t.Fatalf("repeated AddHTTPExposure = changed %t, %v", repeatedChanged, err)
	}
}

func TestAddHTTPExposureSortsAndIsByteIdempotent(t *testing.T) {
	t.Parallel()

	input := []byte("http:\r\n  address: \":8080\"\r\n  expose: [records.write/v1, records.read/v1]\r\n")
	updated, changed, err := applicationmeta.AddHTTPExposure(input, mustExposureID(t, "kernel.health/v1"))
	if err != nil || !changed {
		t.Fatalf("AddHTTPExposure = changed %t, %v", changed, err)
	}
	wantOrder := []string{"kernel.health/v1", "records.read/v1", "records.write/v1"}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil || len(manifest.HTTPExposures()) != len(wantOrder) {
		t.Fatalf("Parse(updated) = %#v, %v", manifest.HTTPExposures(), err)
	}
	for index, exposure := range manifest.HTTPExposures() {
		if exposure.ID().String() != wantOrder[index] {
			t.Fatalf("HTTPExposures[%d] = %s, want %s", index, exposure.ID(), wantOrder[index])
		}
	}
	idempotent, idempotentChanged, err := applicationmeta.AddHTTPExposure(updated, mustExposureID(t, "records.read/v1"))
	if err != nil || idempotentChanged || !bytes.Equal(idempotent, updated) {
		t.Fatalf("idempotent AddHTTPExposure = changed %t, data %q, %v", idempotentChanged, idempotent, err)
	}
	idempotent[0] = 'x'
	if updated[0] == 'x' {
		t.Fatal("idempotent result exposed input storage")
	}
}

func TestAddHTTPExposureRejectsInvalidInputsWithoutPartialOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		id   capabilityid.Identifier
	}{
		{name: "empty Capability", data: []byte("{}\n")},
		{name: "invalid manifest", data: []byte("unknown: true\n"), id: mustExposureID(t, "kernel.health/v1")},
		{name: "duplicate exposure", data: []byte("http: {expose: [kernel.health/v1, kernel.health/v1]}\n"), id: mustExposureID(t, "kernel.info/v1")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			updated, changed, err := applicationmeta.AddHTTPExposure(test.data, test.id)
			if !errors.Is(err, applicationmeta.ErrAddHTTPExposure) || changed || updated != nil {
				t.Fatalf("AddHTTPExposure = changed %t, data %q, error %v", changed, updated, err)
			}
		})
	}
}

func mustExposureID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}

func TestAddHTTPExposureDoesNotRenderSecretsInErrors(t *testing.T) {
	t.Parallel()

	secret := "unique-secret-value"
	data := []byte("config:\n  acme.mail:\n    password: " + secret + "\nhttp: {expose: {}}\n")
	_, _, err := applicationmeta.AddHTTPExposure(data, mustExposureID(t, "kernel.health/v1"))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("AddHTTPExposure error = %v", err)
	}
}
