package commandschema

import (
	"encoding/json"
	"testing"
)

func TestResourceSelectionsPayloadIsBoundedAndConfiguredOnly(t *testing.T) {
	instances := []ResourceSelectionInput{
		{Name: "access.generated", Provider: "example.com/app/access.New", SourcePath: "plystra.yaml", SourceLine: 4, SourceColumn: 7},
		{Name: "database.primary", Provider: "example.com/app/database.New", SourcePath: "plystra.yaml", SourceLine: 6, SourceColumn: 7, ProviderVisible: true, ResourceID: "data.database/v1"},
	}
	payload, err := NewResourceSelections("example.com/app", instances)
	if err != nil || !payload.Valid() || payload.Schema() != ResourceSelectionsSchemaV1 {
		t.Fatalf("NewResourceSelections = %#v, %v", payload, err)
	}
	instances[0].Name = "changed"
	returned := payload.Instances()
	returned[0].Name = "changed"
	if payload.Instances()[0].Name != "access.generated" {
		t.Fatal("payload retained mutable input")
	}
	var document struct {
		Schema    string `json:"schema"`
		Scope     string `json:"scope"`
		Instances []struct {
			ResourceID string `json:"resource_id"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(payload.CanonicalJSON(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != ResourceSelectionsSchemaV1 || document.Scope != "configured_only" || len(document.Instances) != 2 || document.Instances[0].ResourceID != "" || document.Instances[1].ResourceID != "data.database/v1" {
		t.Fatalf("payload document = %s", payload.CanonicalJSON())
	}
}

func TestResourceSelectionsRejectInvalidFacts(t *testing.T) {
	good := ResourceSelectionInput{Name: "database.primary", Provider: "example.com/app/database.New", SourcePath: "plystra.yaml", SourceLine: 4, SourceColumn: 7, ProviderVisible: true, ResourceID: "data.database/v1"}
	for name, change := range map[string]func(*ResourceSelectionInput){
		"instance": func(v *ResourceSelectionInput) { v.Name = "Bad" },
		"provider": func(v *ResourceSelectionInput) { v.Provider = "private" },
		"path":     func(v *ResourceSelectionInput) { v.SourcePath = "../private" },
		"span":     func(v *ResourceSelectionInput) { v.SourceLine = 0 },
		"id":       func(v *ResourceSelectionInput) { v.ResourceID = "invalid" },
		"missing":  func(v *ResourceSelectionInput) { v.ProviderVisible = false },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := good
			change(&invalid)
			if _, err := NewResourceSelections("example.com/app", []ResourceSelectionInput{invalid}); err == nil {
				t.Fatal("invalid selection accepted")
			}
		})
	}
	if _, err := NewResourceSelections("example.com/app", []ResourceSelectionInput{good, good}); err == nil {
		t.Fatal("duplicate instances accepted")
	}
}
