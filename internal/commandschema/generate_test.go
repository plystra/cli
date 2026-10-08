package commandschema

import (
	"encoding/json"
	"testing"
)

func TestGeneratePayloadIsVersionedAndPublicSafe(t *testing.T) {
	payload, err := NewGeneratePayload(GeneratePayloadInput{
		ModulePath: "example.com/acme/app",
		Mode:       "install",
	})
	if err != nil {
		t.Fatalf("NewGeneratePayload = %v", err)
	}
	if !payload.Valid() || payload.Schema() != GenerateSchemaV1 || payload.Mode() != "install" {
		t.Fatalf("payload = %#v", payload)
	}
	var document struct {
		Schema     string `json:"schema"`
		ModulePath string `json:"module_path"`
		Mode       string `json:"mode"`
	}
	if err := json.Unmarshal(payload.CanonicalJSON(), &document); err != nil {
		t.Fatalf("Unmarshal(payload) = %v", err)
	}
	if document.Schema != GenerateSchemaV1 || document.ModulePath != "example.com/acme/app" || document.Mode != "install" {
		t.Fatalf("payload document = %s", payload.CanonicalJSON())
	}
}

func TestGeneratePayloadRejectsInvalidInput(t *testing.T) {
	for name, input := range map[string]GeneratePayloadInput{
		"module": {ModulePath: "C:\\private", Mode: "check"},
		"mode":   {ModulePath: "example.com/acme/app", Mode: "write"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewGeneratePayload(input); err == nil {
				t.Fatal("NewGeneratePayload unexpectedly succeeded")
			}
		})
	}
}
