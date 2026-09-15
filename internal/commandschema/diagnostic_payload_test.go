package commandschema_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestDiagnosticPayloadNestsCommandSchemaWithoutCompetingDiagnostics(t *testing.T) {
	t.Parallel()

	schema, err := diagnosticjson.NewSchema("plystra.explain", 1)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 schema,
		ConfigurationMode:      generation.ConfigurationModeEnvironment,
		ApplicationModelDigest: "sha256:" + strings.Repeat("a", 64),
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA_EXPLAIN_AVAILABLE",
			Severity: diagnosticjson.SeverityInfo,
			Message:  "The decision is available.",
		}},
		Sources: []diagnosticjson.Source{{
			Module: "example.com/app",
			Path:   "plystra.production.yaml",
			Kind:   "configuration-selection",
			Line:   2,
			Column: 3,
		}},
		Result: []byte(`{"decision":{"outcome":"available"}}`),
	})
	if err != nil {
		t.Fatalf("diagnosticjson.New: %v", err)
	}
	payload, err := commandschema.NewDiagnosticPayload(envelope)
	if err != nil || !payload.Valid() {
		t.Fatalf("NewDiagnosticPayload = %#v, %v", payload, err)
	}
	want := `{"schema":"plystra.explain/v1","configuration_mode":"environment","application_model_digest":"sha256:` + strings.Repeat("a", 64) + `","sources":[{"module":"example.com/app","path":"plystra.production.yaml","kind":"configuration-selection","line":2,"column":3}],"result":{"decision":{"outcome":"available"}}}`
	if got := string(payload.CanonicalJSON()); got != want {
		t.Fatalf("CanonicalJSON = %s\nwant = %s", got, want)
	}
	if payload.Schema() != "plystra.explain/v1" || bytes.Contains(payload.CanonicalJSON(), []byte(`"diagnostics"`)) {
		t.Fatalf("payload identity or ownership = schema %q JSON %s", payload.Schema(), payload.CanonicalJSON())
	}
	canonical := payload.CanonicalJSON()
	canonical[0] = '['
	if !payload.Valid() || bytes.HasPrefix(payload.CanonicalJSON(), []byte("[")) {
		t.Fatal("diagnostic payload exposed mutable canonical JSON")
	}

	if invalid, buildErr := commandschema.NewDiagnosticPayload(diagnosticjson.Envelope{}); !errors.Is(buildErr, commandschema.ErrDiagnosticPayload) || invalid.Valid() {
		t.Fatalf("zero envelope payload = %#v, %v; want ErrDiagnosticPayload", invalid, buildErr)
	}
}
