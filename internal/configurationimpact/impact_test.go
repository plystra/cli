package configurationimpact_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/configurationimpact"
)

func TestNewAlwaysEmitsAllFiveArraysAndSorts(t *testing.T) {
	impact, err := configurationimpact.New(configurationimpact.Input{
		RetainedOwners:    []configurationimpact.RetainedOwner{{Owner: "z.New", Path: "config.z.New", Reason: "owner-remains-effective"}, {Owner: "a.New", Path: "config.a.New", Reason: "owner-remains-effective"}},
		DerivedOperations: []configurationimpact.DerivedOperation{{Kind: "resource_binding", Action: "remove", Owner: "z.New", Path: "resources.bind.implementations.z.New.DB", Source: "plystra.yaml"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := string(impact.CanonicalJSON())
	for _, field := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
		if !strings.Contains(document, `"`+field+`"`) {
			t.Fatalf("missing %s in %s", field, document)
		}
	}
	if strings.Contains(document, "null") {
		t.Fatalf("impact used null arrays: %s", document)
	}
	if !bytes.Contains(impact.CanonicalJSON(), []byte(`"owner":"a.New"`)) {
		t.Fatalf("retained owners were not encoded: %s", document)
	}
	if !impact.Valid() {
		t.Fatal("impact is not valid")
	}
}

func TestCompareReportsOnlyOwnershipIdentityAndDerivedCleanup(t *testing.T) {
	before, err := applicationmeta.Parse([]byte(`config:
  example.com/app.New: {password: PRIVATE_SECRET}
resources:
  instances:
    database.primary:
      use: example.com/app.Database
      config: {password: PRIVATE_DATABASE_SECRET}
  bind:
    implementations:
      example.com/app.New: {Database: database.primary}
`))
	if err != nil {
		t.Fatal(err)
	}
	after, err := applicationmeta.Parse([]byte(`resources:
  instances:
    database.primary:
      use: example.com/app.Database
`))
	if err != nil {
		t.Fatal(err)
	}
	impact, err := configurationimpact.Compare(before, after, configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if len(impact.NewlyUnownedPaths()) != 3 || len(impact.DerivedOperations()) != 3 {
		t.Fatalf("unowned=%d derived=%d", len(impact.NewlyUnownedPaths()), len(impact.DerivedOperations()))
	}
	encoded := string(impact.CanonicalJSON())
	for _, private := range []string{"PRIVATE_SECRET", "PRIVATE_DATABASE_SECRET"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("private value leaked in impact: %s", encoded)
		}
	}
	if !strings.Contains(encoded, `resources.instances[\"database.primary\"].config`) || !strings.Contains(encoded, `resources.bind.implementations[\"example.com/app.New\"][\"Database\"]`) {
		t.Fatalf("exact ownership paths missing: %s", encoded)
	}
}
