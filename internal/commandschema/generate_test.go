package commandschema

import (
	"encoding/json"
	"testing"
)

func TestGeneratePayloadIsVersionedAndPublicSafe(t *testing.T) {
	payload, err := NewGeneratePayload(GeneratePayloadInput{
		ModulePath: "example.com/acme/app",
		Mode:       "install",
		DataCompiler: &GenerateDataCompilerInput{
			ModulePath:     "github.com/plystra/data",
			ModuleVersion:  "v0.2.0",
			ModuleChecksum: "h1:compiler",
			ManifestDigest: "sha256:manifest",
			BinaryDigest:   "sha256:binary",
			GoToolchain:    "go1.26.6",
			GOOS:           "windows",
			GOARCH:         "amd64",
			CacheHit:       true,
		},
	})
	if err != nil {
		t.Fatalf("NewGeneratePayload = %v", err)
	}
	if !payload.Valid() || payload.Schema() != GenerateSchemaV1 || payload.Mode() != "install" {
		t.Fatalf("payload = %#v", payload)
	}
	var document struct {
		Schema       string          `json:"schema"`
		ModulePath   string          `json:"module_path"`
		Mode         string          `json:"mode"`
		DataCompiler json.RawMessage `json:"data_compiler"`
	}
	if err := json.Unmarshal(payload.CanonicalJSON(), &document); err != nil {
		t.Fatalf("Unmarshal(payload) = %v", err)
	}
	if document.Schema != GenerateSchemaV1 || document.ModulePath != "example.com/acme/app" || document.Mode != "install" || string(document.DataCompiler) == "null" {
		t.Fatalf("payload document = %s", payload.CanonicalJSON())
	}
}

func TestGeneratePayloadRejectsInvalidInput(t *testing.T) {
	for name, input := range map[string]GeneratePayloadInput{
		"module": {ModulePath: "C:\\private", Mode: "check"},
		"mode":   {ModulePath: "example.com/acme/app", Mode: "write"},
		"compiler": {
			ModulePath: "example.com/acme/app", Mode: "check",
			DataCompiler: &GenerateDataCompilerInput{
				ModulePath: "github.com/plystra/data",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewGeneratePayload(input); err == nil {
				t.Fatal("NewGeneratePayload unexpectedly succeeded")
			}
		})
	}
}
