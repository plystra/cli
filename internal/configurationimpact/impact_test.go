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

func TestCompareBindingsUsesExactAddressesWithoutConfigValidity(t *testing.T) {
	before, err := applicationmeta.ParseCompleteSource("deploy/before.yaml", []byte(`resources:
  instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}}
  bind:
    instances:
      database.primary: {Old: database.retained, old: database.retained, Keep: database.retained}
    implementations:
      example.com/app.New: {Old: database.retained}
`))
	if err != nil {
		t.Fatal(err)
	}
	after, err := applicationmeta.ParseCompleteSource("deploy/after.yaml", []byte(`resources:
  bind:
    instances:
      database.primary: {old: database.retained, Keep: database.other}
    implementations:
      example.com/app.New: {Old: database.retained}
`))
	if err != nil {
		t.Fatal(err)
	}
	input := configurationimpact.Input{RetainedOwners: []configurationimpact.RetainedOwner{{Owner: "database.retained", Path: "resources.instances[\"database.retained\"].config", Reason: "resource-instance-remains-effective"}}}
	impact, err := configurationimpact.CompareBindings(before.ResourceBindings(), after.ResourceBindings(), input)
	if err != nil || len(impact.DerivedOperations()) != 1 || len(impact.NewlyUnownedPaths()) != 1 || len(impact.RetainedOwners()) != 1 {
		t.Fatalf("exact binding comparison failed: %v", err)
	}
	operation := impact.DerivedOperations()[0]
	if operation.Path != "resources.bind.instances[\"database.primary\"][\"Old\"]" || !strings.HasPrefix(operation.Source, "deploy/before.yaml") || strings.Contains(string(impact.CanonicalJSON()), "PRIVATE_") {
		t.Fatal("binding comparison widened address, lost source, or exposed private Config")
	}
}
