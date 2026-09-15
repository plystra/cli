package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
)

// ErrDiagnosticPayload reports an invalid command-specific diagnostic payload.
var ErrDiagnosticPayload = errors.New("build Plystra command diagnostic payload")

// DiagnosticPayload adapts one existing command-owned diagnostic envelope into
// a nested result payload. Diagnostics remain owned by the top-level result.
type DiagnosticPayload struct {
	envelope      diagnosticjson.Envelope
	schema        string
	canonicalJSON []byte
	prepared      bool
}

type diagnosticPayloadDocument struct {
	Schema                 string                       `json:"schema"`
	ConfigurationMode      generation.ConfigurationMode `json:"configuration_mode"`
	ApplicationModelDigest string                       `json:"application_model_digest"`
	Sources                []diagnosticLocationDocument `json:"sources"`
	Result                 json.RawMessage              `json:"result"`
}

// NewDiagnosticPayload validates and adapts one command-owned envelope.
func NewDiagnosticPayload(envelope diagnosticjson.Envelope) (DiagnosticPayload, error) {
	if !envelope.Valid() {
		return DiagnosticPayload{}, fmt.Errorf("%w: envelope is invalid", ErrDiagnosticPayload)
	}
	schema := fmt.Sprintf("%s/v%d", envelope.Schema().Name(), envelope.SchemaVersion())
	document := diagnosticPayloadDocument{
		Schema:                 schema,
		ConfigurationMode:      envelope.ConfigurationMode(),
		ApplicationModelDigest: envelope.ApplicationModelDigest(),
		Sources:                diagnosticLocationDocuments(envelope.Sources()),
		Result:                 envelope.ResultJSON(),
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return DiagnosticPayload{}, fmt.Errorf("%w: encode: %v", ErrDiagnosticPayload, err)
	}
	return DiagnosticPayload{
		envelope:      envelope,
		schema:        schema,
		canonicalJSON: canonical,
		prepared:      true,
	}, nil
}

// Valid reports whether NewDiagnosticPayload produced this payload.
func (p DiagnosticPayload) Valid() bool {
	if !p.prepared || !p.envelope.Valid() {
		return false
	}
	rebuilt, err := NewDiagnosticPayload(p.envelope)
	return err == nil && rebuilt.schema == p.schema && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

// Schema returns the versioned command-owned payload identity.
func (p DiagnosticPayload) Schema() string { return p.schema }

// CanonicalJSON returns a defensive copy of the complete nested payload.
func (p DiagnosticPayload) CanonicalJSON() []byte {
	return append([]byte(nil), p.canonicalJSON...)
}

func (DiagnosticPayload) commandPayload() {}
