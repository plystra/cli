package commandschema_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
)

func TestChangePlanRepresentsPathExistenceIndependentlyOfDigests(t *testing.T) {
	for _, test := range []struct {
		name                      string
		writes, before, after     bool
		beforeDigest, afterDigest string
		valid                     bool
	}{
		{name: "absent input", valid: true},
		{name: "present input", before: true, beforeDigest: changePlanDigest, valid: true},
		{name: "private present input", before: true, valid: true},
		{name: "create", writes: true, after: true, afterDigest: changePlanDigest, valid: true},
		{name: "replace", writes: true, before: true, after: true, beforeDigest: changePlanDigest, afterDigest: changePlanDigest, valid: true},
		{name: "delete", writes: true, before: true, beforeDigest: changePlanDigest, valid: true},
		{name: "private create", writes: true, after: true, valid: true},
		{name: "private replace", writes: true, before: true, after: true, valid: true},
		{name: "private delete", writes: true, before: true, valid: true},
		{name: "absent to absent", writes: true},
		{name: "digest for absent input", beforeDigest: changePlanDigest},
		{name: "digest for absent preimage", writes: true, after: true, beforeDigest: changePlanDigest},
		{name: "digest for absent result", writes: true, before: true, afterDigest: changePlanDigest},
		{name: "invalid input digest", before: true, beforeDigest: "invalid"},
		{name: "invalid preimage digest", writes: true, before: true, after: true, beforeDigest: "invalid"},
		{name: "invalid result digest", writes: true, after: true, afterDigest: "invalid"},
		{name: "read with result existence", after: true},
		{name: "read with result digest", before: true, after: true, afterDigest: changePlanDigest},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := changePlanEffectInput(t)
			entry := commandschema.ChangePlanPathInput{
				Path: "generated/source.go", Kind: "generated", BeforeExists: test.before, AfterExists: test.after,
				BeforeDigest: test.beforeDigest, AfterDigest: test.afterDigest,
			}
			if test.writes {
				input.WriteSet = []commandschema.ChangePlanPathInput{entry}
			} else {
				input.ReadSet = []commandschema.ChangePlanPathInput{entry}
			}
			plan, err := commandschema.NewChangePlan(input)
			if (err == nil) != test.valid || plan.Valid() != test.valid {
				t.Fatalf("path state valid=%t: %v", test.valid, err)
			}
			if !test.valid {
				return
			}
			var document struct {
				ReadSet  []map[string]json.RawMessage `json:"read_set"`
				WriteSet []map[string]json.RawMessage `json:"write_set"`
			}
			if err := json.Unmarshal(plan.CanonicalJSON(), &document); err != nil {
				t.Fatal(err)
			}
			entryDocument := document.ReadSet[0]
			if test.writes {
				entryDocument = document.WriteSet[0]
			}
			wantBefore, _ := json.Marshal(test.before)
			if !bytes.Equal(entryDocument["before_exists"], wantBefore) {
				t.Fatalf("before existence lost: %s", plan.CanonicalJSON())
			}
			after, exists := entryDocument["after_exists"]
			wantAfter, _ := json.Marshal(test.after)
			if exists != test.writes || test.writes && !bytes.Equal(after, wantAfter) {
				t.Fatalf("after existence lost or invented: %s", plan.CanonicalJSON())
			}
			for name, digest := range map[string]string{"before_digest": test.beforeDigest, "after_digest": test.afterDigest} {
				if _, exists := entryDocument[name]; exists != (digest != "") {
					t.Fatalf("digest disclosure does not match supplied public identity: %s", plan.CanonicalJSON())
				}
			}
		})
	}
}

func TestChangePlanBindsAndOwnsPathExistence(t *testing.T) {
	input := changePlanEffectInput(t)
	input.ReadSet = []commandschema.ChangePlanPathInput{
		{Path: "private-baseline.json", Kind: "private", BeforeExists: true},
		{Path: "new.go", Kind: "authored"},
	}
	input.WriteSet = []commandschema.ChangePlanPathInput{
		{Path: "private-baseline.json", Kind: "private", BeforeExists: true, AfterExists: true},
		{Path: "new.go", Kind: "authored", AfterExists: true, AfterDigest: changePlanDigest},
	}
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ReadSet[0], input.ReadSet[1] = input.ReadSet[1], input.ReadSet[0]
	input.WriteSet[0], input.WriteSet[1] = input.WriteSet[1], input.WriteSet[0]
	ordered, err := commandschema.NewChangePlan(input)
	if err != nil || !bytes.Equal(baseline.CanonicalJSON(), ordered.CanonicalJSON()) {
		t.Fatalf("path order changed plan identity: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func()
	}{
		{"input existence", func() { input.ReadSet[1].BeforeExists = false }},
		{"output preimage existence", func() { input.WriteSet[1].BeforeExists = false }},
		{"output result existence", func() { input.WriteSet[1].AfterExists = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.edit()
			changed, err := commandschema.NewChangePlan(input)
			if err != nil || !changed.Valid() || changed.PlanDigest() == baseline.PlanDigest() || !baseline.Valid() || !ordered.Valid() {
				t.Fatalf("path existence was not bound or owned: %v", err)
			}
			input.ReadSet[1].BeforeExists = true
			input.WriteSet[1].BeforeExists, input.WriteSet[1].AfterExists = true, true
		})
	}
}

func TestChangePlanDoesNotClaimExactPathPreimagesWithoutDigests(t *testing.T) {
	input := changePlanEffectInput(t)
	input.Completeness = "exact"
	input.WriteSet = []commandschema.ChangePlanPathInput{{Path: "generated/source.go", Kind: "generated", BeforeExists: true, AfterExists: true, BeforeDigest: changePlanDigest, AfterDigest: changePlanDigest}}
	if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
		t.Fatalf("complete path identities rejected: %v", err)
	}
	for _, clear := range []func(){
		func() { input.ReadSet[0].BeforeDigest = "" },
		func() { input.WriteSet[0].BeforeDigest = "" },
		func() { input.WriteSet[0].AfterDigest = "" },
	} {
		clear()
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatal("undisclosed present path identity accepted for exact plan")
		}
		input.Completeness = "non_exact"
		if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
			t.Fatalf("non-exact path identity rejected: %v", err)
		}
		input.Completeness = "exact"
		input.ReadSet[0].BeforeDigest = changePlanDigest
		input.WriteSet[0].BeforeDigest, input.WriteSet[0].AfterDigest = changePlanDigest, changePlanDigest
	}
	input.ReadSet = []commandschema.ChangePlanPathInput{{Path: "missing.go", Kind: "authored"}}
	input.WriteSet[0].AfterExists, input.WriteSet[0].AfterDigest = false, ""
	if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
		t.Fatalf("exact absence observation or deletion rejected: %v", err)
	}
}
