package commandschema_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/configurationimpact"
)

const changePlanDigest = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"

func TestChangePlanPreservesVerificationArgumentOrder(t *testing.T) {
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
		ReadSet: []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest}},
		Operations: []commandschema.ChangePlanOperationInput{
			{Kind: "interface_root", Action: "present", Interface: "email.send/v1", Root: "require"},
		},
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: "default", Path: "plystra.yaml"},
		ConfigurationImpact: impact, Effects: effects, FinalModelDigest: changePlanDigest, Completeness: "non_exact",
	}
	for _, arguments := range [][]string{
		{"plystra", "check"},
		{"plystra", "check", "--env", "production"},
		{"go", "build", "-mod=readonly", "./..."},
		{"tool", "first", "second", "first"},
		{"plystra", "check", "--config", strings.Repeat("nested/", 40) + "configuration.yaml"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			input.Verification = append([]string(nil), arguments...)
			plan, err := commandschema.NewChangePlan(input)
			if err != nil {
				t.Fatal(err)
			}
			var document struct{ Verification []string }
			if err := json.Unmarshal(plan.CanonicalJSON(), &document); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(document.Verification, arguments) || !reflect.DeepEqual(input.Verification, arguments) {
				t.Fatalf("verification changed order: got %q, want %q", document.Verification, arguments)
			}
			input.Verification[0], input.Verification[1] = input.Verification[1], input.Verification[0]
			changed, err := commandschema.NewChangePlan(input)
			if err != nil || changed.PlanDigest() == plan.PlanDigest() {
				t.Fatalf("argument order did not change plan identity: %v", err)
			}
			if !plan.Valid() || !changed.Valid() {
				t.Fatal("caller-owned argument edit changed a prepared plan")
			}
		})
	}
	input.Verification = []string{"tool", strings.Repeat("x", 4096)}
	if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
		t.Fatalf("exact argument-length bound rejected: %v", err)
	}
	input.Verification = make([]string, 256)
	for index := range input.Verification {
		input.Verification[index] = "argument"
	}
	if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
		t.Fatalf("exact argument-count bound rejected: %v", err)
	}
	for _, arguments := range [][]string{
		nil,
		{""},
		{"tool", "line\nbreak"},
		{"tool", "zero\x00byte"},
		{"tool", "invalid\xffutf8"},
		{"tool", "${unbound}"},
		{"tool", strings.Repeat("x", 4097)},
		append(input.Verification, "overflow"),
	} {
		input.Verification = arguments
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatal("invalid verification argument vector accepted")
		}
	}
}

func TestChangePlanAndEffectBindLiteralVerificationSelectors(t *testing.T) {
	impact, err := configurationimpact.New(configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, target, environment string
		arguments                 []string
		valid                     bool
	}{
		{name: "literal braces", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "check", "--config", "deploy/{{blue}}.yaml"}, valid: true},
		{name: "literal dollar", target: "deploy/${blue}.yaml", arguments: []string{"plystra", "check", "--config", "deploy/${blue}.yaml"}, valid: true},
		{name: "prefixed literal", target: "--${blue}.yaml", arguments: []string{"plystra", "check", "--config", "./--${blue}.yaml"}, valid: true},
		{name: "literal environment", target: "plystra.{{blue}}.yaml", environment: "{{blue}}", arguments: []string{"plystra", "check", "--env", "{{blue}}"}, valid: true},
		{name: "unbound target", target: "deploy/production.yaml", arguments: []string{"plystra", "check", "--config", "deploy/{{blue}}.yaml"}},
		{name: "unbound environment", target: "plystra.production.yaml", environment: "production", arguments: []string{"plystra", "check", "--env", "{{blue}}"}},
		{name: "unbound executable", target: "deploy/{{blue}}.yaml", arguments: []string{"${tool}", "check", "--config", "deploy/{{blue}}.yaml"}},
		{name: "unbound command", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "${command}", "--config", "deploy/{{blue}}.yaml"}},
		{name: "other command", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "generate", "--config", "deploy/{{blue}}.yaml"}},
		{name: "extra unbound argument", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "check", "--config", "deploy/{{blue}}.yaml", "${extra}"}},
		{name: "traversal", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "check", "--config", "../deploy/{{blue}}.yaml"}},
		{name: "control", target: "deploy/{{blue}}.yaml", arguments: []string{"plystra", "check", "--config", "deploy/{{blue}}.yaml\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode := "explicit-config"
			if test.environment != "" {
				mode = "environment"
			}
			plan, err := commandschema.NewChangePlan(commandschema.ChangePlanInput{
				RequestDigest: changePlanDigest,
				InputSnapshot: commandschema.ChangePlanSnapshotInput{
					ModulePath: "example.com/acme/app", ConfigurationMode: mode,
					ConfigurationPath: test.target, Environment: test.environment,
					RootDigest: changePlanDigest, SelectedDigest: changePlanDigest,
				},
				ReadSet:             []commandschema.ChangePlanPathInput{{Path: test.target, Kind: "configuration", BeforeDigest: changePlanDigest}},
				Operations:          []commandschema.ChangePlanOperationInput{{Kind: "interface_root", Action: "present", Interface: "email.send/v1", Root: "require"}},
				ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: mode, Path: test.target, Environment: test.environment},
				ConfigurationImpact: impact, Effects: effects, FinalModelDigest: changePlanDigest, Completeness: "non_exact",
				Verification: test.arguments,
			})
			if (err == nil) != test.valid || plan.Valid() != test.valid {
				t.Fatalf("literal plan verification = %v, want valid %t", err, test.valid)
			}
			effect, err := commandschema.NewEffect(commandschema.EffectInput{
				ID: "write-configuration", Class: commandschema.EffectProjectWrite, Phase: "apply", Target: test.target,
				Owner: commandschema.Owner{Module: "example.com/acme/app", Path: "."}, Reason: "configuration", Reversibility: "rollback", Verification: test.arguments,
			})
			if (err == nil) != test.valid || effect.Valid() != test.valid {
				t.Fatalf("literal effect verification = %v, want valid %t", err, test.valid)
			}
		})
	}
}

func TestChangePlanBoundsRequestedAndDerivedOperationsSeparately(t *testing.T) {
	impact, err := configurationimpact.New(configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name               string
		requested, derived int
		valid              bool
	}{
		{"maximum request with cleanup", 1024, 1, true},
		{"maximum combined", 1024, 1024, true},
		{"too many requested", 1025, 0, false},
		{"too many derived", 1, 1025, false},
		{"too many combined", 1024, 1025, false},
		{"only derived", 0, 1, false},
		{"empty", 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			operations := make([]commandschema.ChangePlanOperationInput, 0, test.requested+test.derived)
			for index := 0; index < test.requested+test.derived; index++ {
				operations = append(operations, commandschema.ChangePlanOperationInput{
					Kind: "configuration", Action: "remove", Owner: "example.com/email/smtp.New",
					Path: fmt.Sprintf("field_%04d", index), Derived: index >= test.requested,
				})
			}
			plan, err := commandschema.NewChangePlan(commandschema.ChangePlanInput{
				RequestDigest: changePlanDigest,
				InputSnapshot: commandschema.ChangePlanSnapshotInput{
					ModulePath: "example.com/acme/app", ConfigurationMode: "default", ConfigurationPath: "plystra.yaml",
					RootDigest: changePlanDigest, SelectedDigest: changePlanDigest,
				},
				ReadSet:    []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest}},
				Operations: operations, ConfigurationScope: commandschema.ChangePlanConfigurationScopeInput{Mode: "default", Path: "plystra.yaml"},
				ConfigurationImpact: impact, Effects: effects, FinalModelDigest: changePlanDigest,
				Verification: []string{"plystra", "check"}, Completeness: "non_exact",
			})
			if test.valid {
				if err != nil || !plan.Valid() {
					t.Fatalf("bounded operations rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("out-of-bounds operations accepted")
			}
		})
	}
}

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

	input.WriteSet = nil
	noWrite, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatalf("empty write set: %v", err)
	}
	var noWriteDocument struct {
		WriteSet json.RawMessage `json:"write_set"`
	}
	if err := json.Unmarshal(noWrite.CanonicalJSON(), &noWriteDocument); err != nil {
		t.Fatal(err)
	}
	if !noWrite.Valid() || string(noWriteDocument.WriteSet) != "[]" || noWrite.PlanDigest() == first.PlanDigest() {
		t.Fatalf("no-write plan = %s", noWrite.CanonicalJSON())
	}
	input.ReadSet = nil
	if _, err := commandschema.NewChangePlan(input); err == nil {
		t.Fatal("empty read set was accepted")
	}
	input.ReadSet = []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest}}
	input.Operations = []commandschema.ChangePlanOperationInput{{Kind: "interface_root", Action: "present", Interface: "email.send/v1", Root: "require"}}
	require, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Operations[0].Root = "expose"
	expose, err := commandschema.NewChangePlan(input)
	if err != nil || require.PlanDigest() == expose.PlanDigest() || !bytes.Contains(expose.CanonicalJSON(), []byte(`"root":"expose"`)) {
		t.Fatalf("root identity not encoded or bound: %v", err)
	}
	for _, root := range []string{"", "unknown"} {
		input.Operations[0].Root = root
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatalf("invalid root %q accepted", root)
		}
	}
	input.Operations[0] = commandschema.ChangePlanOperationInput{Kind: "implementation_selection", Action: "remove", Interface: "email.send/v1", Root: "require"}
	if _, err := commandschema.NewChangePlan(input); err == nil {
		t.Fatal("selection accepted root field")
	}
	input.Operations[0] = commandschema.ChangePlanOperationInput{Kind: "configuration", Action: "set", Owner: "database.primary", Path: "nested.value"}
	configuration, err := commandschema.NewChangePlan(input)
	if err != nil || !bytes.Contains(configuration.CanonicalJSON(), []byte(`"path":"nested.value"`)) {
		t.Fatalf("configuration projection: %v", err)
	}
	for _, operation := range []commandschema.ChangePlanOperationInput{
		{Kind: "configuration", Action: "set", Path: "value"},
		{Kind: "configuration", Action: "remove", Owner: "database.primary"},
		{Kind: "configuration", Action: "other", Owner: "database.primary", Path: "value"},
		{Kind: "configuration", Action: "set", Owner: "database.primary", Path: "value", Provider: "example.com/db.New"},
	} {
		input.Operations[0] = operation
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatal("incomplete or unrelated configuration operation projection accepted")
		}
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
