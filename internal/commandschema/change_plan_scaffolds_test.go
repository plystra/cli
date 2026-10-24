package commandschema_test

import (
	"bytes"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
)

func TestChangePlanBindsScaffoldIdentities(t *testing.T) {
	input := changePlanEffectInput(t)
	input.Operations = []commandschema.ChangePlanOperationInput{
		{Kind: "interface_create", Action: "create", Name: "todo.add/v1", Semantics: "command"},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "./todo/add"},
	}
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(){
		func() { input.Operations[0].Name = "todo.other/v1" },
		func() { input.Operations[0].Semantics = "query" },
		func() { input.Operations[1].Contract = "todo.other/v1" },
		func() { input.Operations[1].Package = "./todo/other" },
	} {
		original := append([]commandschema.ChangePlanOperationInput(nil), input.Operations...)
		edit()
		changed, err := commandschema.NewChangePlan(input)
		if err != nil || !changed.Valid() || changed.PlanDigest() == baseline.PlanDigest() || !baseline.Valid() {
			t.Fatalf("scaffold identity not bound or owned: %v", err)
		}
		input.Operations = original
	}
	input.Operations[0], input.Operations[1] = input.Operations[1], input.Operations[0]
	ordered, err := commandschema.NewChangePlan(input)
	if err != nil || !bytes.Equal(ordered.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("scaffold order changed plan: %v", err)
	}
	for _, fragment := range []string{`"name":"todo.add/v1"`, `"semantics":"command"`, `"contract":"todo.add/v1"`, `"package":"./todo/add"`} {
		if !bytes.Contains(baseline.CanonicalJSON(), []byte(fragment)) {
			t.Fatalf("scaffold identity missing: %s", baseline.CanonicalJSON())
		}
	}
}

func TestChangePlanRejectsInvalidScaffoldProjection(t *testing.T) {
	for _, operation := range []commandschema.ChangePlanOperationInput{
		{Kind: "interface_create", Action: "create", Name: "todo.add", Semantics: "query"},
		{Kind: "interface_create", Action: "create", Name: "todo.add/v2", Semantics: "query"},
		{Kind: "interface_create", Action: "create", Name: "todo.add/v1"},
		{Kind: "interface_create", Action: "create", Name: "todo.add/v1", Semantics: "query", Package: "./x"},
		{Kind: "interface_create", Action: "create", Name: "todo.add/v1", Semantics: "query", Interface: "other.id/v1"},
		{Kind: "interface_create", Action: "create", Name: "todo.add/v1", Semantics: "query", Derived: true},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "./"},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "./a/./b"},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "./a/../b"},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "/absolute"},
		{Kind: "implementation_create", Action: "create", Contract: "todo.add/v1", Package: "./x", Semantics: "query"},
		{Kind: "implementation_selection", Action: "set", Contract: "todo.add/v1", Package: "./x"},
	} {
		input := changePlanEffectInput(t)
		input.Operations = []commandschema.ChangePlanOperationInput{operation}
		if _, err := commandschema.NewChangePlan(input); err == nil {
			t.Fatalf("invalid scaffold projection accepted: %+v", operation)
		}
	}
}
