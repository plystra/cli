package commandschema_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/configurationimpact"
)

func TestChangePlanIdentityExcludesInvocationEffectEvidence(t *testing.T) {
	input := changePlanEffectInput(t)
	input.EffectRequirements = []commandschema.Effect{changePlanTestEffect(t, changePlanRequiredEffectInput())}
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, disposition := range []string{"observed", "planned", "skipped", "unverified"} {
		t.Run(disposition, func(t *testing.T) {
			observation := changePlanTestEffect(t, commandschema.EffectInput{
				ID: "candidate-cache", Class: commandschema.EffectCacheMaterialization, Phase: "plan", Target: "compiler-cache",
				Owner: commandschema.Owner{Module: "example.com/acme/app", Path: "."}, Reason: "candidate_validation", Reversibility: "none", Verification: []string{"go", "version"},
			})
			accounting := commandschema.EffectsInput{}
			switch disposition {
			case "observed":
				accounting.Observed = []commandschema.Effect{observation}
			case "planned":
				accounting.Planned = []commandschema.Effect{observation}
			case "skipped":
				accounting.Skipped = []commandschema.Effect{observation}
			case "unverified":
				accounting.Unverified = []commandschema.Effect{observation}
			}
			input.Effects, err = commandschema.NewEffects(accounting)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := commandschema.NewChangePlan(input)
			if err != nil || !plan.Valid() {
				t.Fatalf("observation plan: %v", err)
			}
			if plan.PlanDigest() != baseline.PlanDigest() {
				t.Fatal("invocation effect evidence changed stable plan identity")
			}
			if bytes.Equal(plan.CanonicalJSON(), baseline.CanonicalJSON()) {
				t.Fatal("invocation evidence was lost from the public payload")
			}
			var document struct{ Effects map[string][]json.RawMessage }
			if err := json.Unmarshal(plan.CanonicalJSON(), &document); err != nil || len(document.Effects[disposition]) != 1 {
				t.Fatalf("effect disposition not retained: %s, %v", plan.CanonicalJSON(), err)
			}
		})
	}
}

func TestChangePlanIdentityBindsStableEffectRequirements(t *testing.T) {
	input := changePlanEffectInput(t)
	required := changePlanRequiredEffectInput()
	input.EffectRequirements = []commandschema.Effect{changePlanTestEffect(t, required)}
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*commandschema.EffectInput){
		"id":           func(effect *commandschema.EffectInput) { effect.ID = "other-write" },
		"class":        func(effect *commandschema.EffectInput) { effect.Class = commandschema.EffectBackendWrite },
		"phase":        func(effect *commandschema.EffectInput) { effect.Phase = "verify" },
		"target":       func(effect *commandschema.EffectInput) { effect.Target = "generated/source.go" },
		"owner module": func(effect *commandschema.EffectInput) { effect.Owner.Module = "example.com/other/app" },
		"owner path":   func(effect *commandschema.EffectInput) { effect.Owner.Path = "generated" },
		"reason":       func(effect *commandschema.EffectInput) { effect.Reason = "other_reason" },
		"reversibility": func(effect *commandschema.EffectInput) {
			effect.Reversibility = "none"
		},
		"verification": func(effect *commandschema.EffectInput) { effect.Verification = []string{"go", "build", "./..."} },
		"verification order": func(effect *commandschema.EffectInput) {
			effect.Verification = []string{"check", "plystra"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := required
			mutate(&changed)
			input.EffectRequirements = []commandschema.Effect{changePlanTestEffect(t, changed)}
			plan, err := commandschema.NewChangePlan(input)
			if err != nil || !plan.Valid() || plan.PlanDigest() == baseline.PlanDigest() {
				t.Fatalf("stable effect requirement did not participate in identity: %v", err)
			}
		})
	}
	input.EffectRequirements = nil
	without, err := commandschema.NewChangePlan(input)
	if err != nil || without.PlanDigest() == baseline.PlanDigest() || !bytes.Contains(without.CanonicalJSON(), []byte(`"effect_requirements":[]`)) {
		t.Fatalf("empty stable requirements not represented or bound: %v", err)
	}
}

func TestChangePlanNormalizesAndOwnsEffectRequirements(t *testing.T) {
	input := changePlanEffectInput(t)
	firstInput := changePlanRequiredEffectInput()
	secondInput := firstInput
	secondInput.ID, secondInput.Target = "second-write", "generated/source.go"
	first, second := changePlanTestEffect(t, firstInput), changePlanTestEffect(t, secondInput)
	input.EffectRequirements = []commandschema.Effect{first, second}
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	input.EffectRequirements[0], input.EffectRequirements[1] = second, first
	reordered, err := commandschema.NewChangePlan(input)
	if err != nil || reordered.PlanDigest() != baseline.PlanDigest() || !bytes.Equal(reordered.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("stable requirement order changed canonical identity: %v", err)
	}
	input.EffectRequirements[0] = commandschema.Effect{}
	returned := baseline.EffectRequirements()
	returned[0] = commandschema.Effect{}
	if !baseline.Valid() || !reordered.Valid() || !baseline.EffectRequirements()[0].Valid() {
		t.Fatal("caller-owned or returned requirement slice changed immutable plan")
	}
	for _, requirements := range [][]commandschema.Effect{
		{{}},
		{first, first},
		make([]commandschema.Effect, 4097),
	} {
		input.EffectRequirements = requirements
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatal("invalid, duplicate, or excessive stable effect requirements accepted")
		}
	}
}

func changePlanRequiredEffectInput() commandschema.EffectInput {
	return commandschema.EffectInput{
		ID: "configuration-write", Class: commandschema.EffectProjectWrite, Phase: "apply", Target: "plystra.yaml",
		Owner: commandschema.Owner{Module: "example.com/acme/app", Path: "."}, Reason: "configuration_change", Reversibility: "rollback", Verification: []string{"plystra", "check"},
	}
}

func changePlanEffectInput(t *testing.T) commandschema.ChangePlanInput {
	t.Helper()
	impact, err := configurationimpact.New(configurationimpact.Input{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatal(err)
	}
	return commandschema.ChangePlanInput{
		RequestDigest: changePlanDigest,
		InputSnapshot: commandschema.ChangePlanSnapshotInput{
			ModulePath: "example.com/acme/app", ConfigurationMode: "default", ConfigurationPath: "plystra.yaml",
			RootDigest: changePlanDigest, SelectedDigest: changePlanDigest,
		},
		ReadSet:             []commandschema.ChangePlanPathInput{{Path: "plystra.yaml", Kind: "configuration", BeforeDigest: changePlanDigest}},
		Operations:          []commandschema.ChangePlanOperationInput{{Kind: "interface_root", Action: "present", Interface: "email.send/v1", Root: "require"}},
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: "default", Path: "plystra.yaml"},
		ConfigurationImpact: impact, Effects: effects, FinalModelDigest: changePlanDigest,
		Verification: []string{"plystra", "check"}, Completeness: "non_exact",
	}
}

func changePlanTestEffect(t *testing.T, input commandschema.EffectInput) commandschema.Effect {
	t.Helper()
	effect, err := commandschema.NewEffect(input)
	if err != nil {
		t.Fatal(err)
	}
	return effect
}
