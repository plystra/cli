package commandschema_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/configurationimpact"
)

const changePlanDigest = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"

func TestChangePlanCanonicalizesIdentityAndKeepsAllImpactArrays(t *testing.T) {
	impact, err := configurationimpact.New(configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatal(err)
	}
	input := commandschema.ChangePlanInput{
		RequestDigest: changePlanDigest,
		InputSnapshot: commandschema.ChangePlanSnapshotInput{
			ModulePath: "example.com/acme/app", ConfigurationMode: "default", ConfigurationPath: "plystra.yaml",
			RootDigest: changePlanDigest, SelectedDigest: changePlanDigest,
		},
		ReadSet: []commandschema.ChangePlanPathInput{
			{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest},
		},
		WriteSet: []commandschema.ChangePlanPathInput{
			{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest, AfterDigest: changePlanDigest},
		},
		Operations: []commandschema.ChangePlanOperationInput{
			{Kind: "implementation_selection", Action: "set", Interface: "email.send/v1", Constructor: "example.com/email/smtp.New"},
			{Kind: "configuration", Action: "remove", Owner: "example.com/email/smtp.New", Path: "config.example.com/email/smtp.New", Derived: true},
		},
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: "default", Path: "plystra.yaml"},
		ConfigurationImpact: impact,
		Effects:             effects,
		FinalModelDigest:    changePlanDigest,
		Verification:        []string{"plystra", "check"},
		Completeness:        "non_exact",
	}
	first, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Operations[0], input.Operations[1] = input.Operations[1], input.Operations[0]
	second, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Valid() || !second.Valid() || first.PlanDigest() != second.PlanDigest() {
		t.Fatalf("plans = valid %t/%t digest %q/%q", first.Valid(), second.Valid(), first.PlanDigest(), second.PlanDigest())
	}
	if first.Schema() != commandschema.ChangePlanSchemaV1 || first.Completeness() != "non_exact" {
		t.Fatalf("plan identity = %q/%q", first.Schema(), first.Completeness())
	}

	var document struct {
		Impact map[string]json.RawMessage `json:"configuration_impact"`
	}
	if err := json.Unmarshal(first.CanonicalJSON(), &document); err != nil {
		t.Fatal(err)
	}
	wantArrays := []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"}
	for _, name := range wantArrays {
		value, exists := document.Impact[name]
		if !exists || string(value) == "null" || string(value) != "[]" {
			t.Fatalf("configuration_impact.%s = %s", name, value)
		}
	}

	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatalf("permuted plan changed canonical bytes: %s != %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	if !strings.HasPrefix(first.PlanDigest(), "sha256:") {
		t.Fatalf("plan digest = %q", first.PlanDigest())
	}
}

func TestChangePlanRejectsInvalidNewWritePaths(t *testing.T) {
	impact, err := configurationimpact.New(configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatal(err)
	}
	base := commandschema.ChangePlanInput{
		RequestDigest: changePlanDigest,
		InputSnapshot: commandschema.ChangePlanSnapshotInput{
			ModulePath: "example.com/acme/app", ConfigurationMode: "default", ConfigurationPath: "plystra.yaml",
			RootDigest: changePlanDigest, SelectedDigest: changePlanDigest,
		},
		ReadSet:             []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest}},
		WriteSet:            []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", AfterDigest: changePlanDigest}},
		Operations:          []commandschema.ChangePlanOperationInput{{Kind: "implementation_selection", Action: "set", Interface: "email.send/v1", Constructor: "example.com/email/smtp.New"}},
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: "default", Path: "plystra.yaml"},
		ConfigurationImpact: impact, Effects: effects, FinalModelDigest: changePlanDigest,
		Verification: []string{"plystra", "check"}, Completeness: "non_exact",
	}
	for _, path := range []string{"../escape", "C:/absolute", ""} {
		input := base
		input.WriteSet = []commandschema.ChangePlanPathInput{{Path: path, Kind: "configuration", AfterDigest: changePlanDigest}}
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatalf("path %q was accepted for a new write", path)
		}
	}
	input := base
	input.WriteSet = []commandschema.ChangePlanPathInput{{Path: "new.yaml", AfterDigest: changePlanDigest}}
	if _, err := commandschema.NewChangePlan(input); err == nil {
		t.Fatal("empty write kind was accepted for a new write")
	}
}
