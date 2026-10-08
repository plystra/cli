package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/modulepath"
)

// GenerateSchemaV1 identifies the payload returned by plystra generate.
const GenerateSchemaV1 = "plystra.generate/v1"

// ErrGeneratePayload reports an invalid generation payload.
var ErrGeneratePayload = errors.New("build plystra.generate result")

// GeneratePayloadInput is the construction-only input for one generation
// result. The compiler identity is public-safe and contains no executable or
// cache paths.
type GeneratePayloadInput struct {
	ModulePath string
	Mode       string
}

// GeneratePayload is one immutable plystra.generate/v1 payload.
type GeneratePayload struct {
	modulePath    string
	mode          string
	canonicalJSON []byte
	prepared      bool
}

type generatePayloadDocument struct {
	Schema     string `json:"schema"`
	ModulePath string `json:"module_path"`
	Mode       string `json:"mode"`
}

// NewGeneratePayload validates and constructs one generation payload.
func NewGeneratePayload(input GeneratePayloadInput) (GeneratePayload, error) {
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return GeneratePayload{}, fmt.Errorf("%w: module path: %v", ErrGeneratePayload, err)
	}
	if input.Mode != "check" && input.Mode != "install" {
		return GeneratePayload{}, fmt.Errorf("%w: mode %q is not supported", ErrGeneratePayload, input.Mode)
	}
	payload := GeneratePayload{modulePath: input.ModulePath, mode: input.Mode, prepared: true}
	canonical, err := json.Marshal(payload.document())
	if err != nil {
		return GeneratePayload{}, fmt.Errorf("%w: encode: %v", ErrGeneratePayload, err)
	}
	payload.canonicalJSON = canonical
	return payload, nil
}

// Valid reports whether NewGeneratePayload produced this payload.
func (p GeneratePayload) Valid() bool {
	if !p.prepared {
		return false
	}
	input := GeneratePayloadInput{ModulePath: p.modulePath, Mode: p.mode}
	rebuilt, err := NewGeneratePayload(input)
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

// Schema returns the immutable payload schema identity.
func (GeneratePayload) Schema() string { return GenerateSchemaV1 }

// ModulePath returns the generated Project module identity.
func (p GeneratePayload) ModulePath() string { return p.modulePath }

// Mode returns check or install.
func (p GeneratePayload) Mode() string { return p.mode }

// CanonicalJSON returns a defensive copy of the payload document.
func (p GeneratePayload) CanonicalJSON() []byte { return append([]byte(nil), p.canonicalJSON...) }

func (GeneratePayload) commandPayload() {}

func (p GeneratePayload) document() generatePayloadDocument {
	return generatePayloadDocument{Schema: GenerateSchemaV1, ModulePath: p.modulePath, Mode: p.mode}
}
