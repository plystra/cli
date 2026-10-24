package commandschema_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
)

func TestChangePlanBindsDependencyIdentities(t *testing.T) {
	digests := make(map[string]string)
	for _, action := range []string{"add", "remove", "update"} {
		modules := []string{"example.com/acme/orders", "example.com/acme/other"}
		if action != "remove" {
			modules = append(modules,
				"example.com/acme/orders@v1.2.3", "example.com/acme/orders@v1.2.4",
				"example.com/acme/orders@latest", "example.com/acme/orders@Main",
				"example.com/acme/orders@main", "example.com/acme/orders@v2.0.0+incompatible",
			)
		}
		for _, module := range modules {
			t.Run(action+"/"+module, func(t *testing.T) {
				input := changePlanEffectInput(t)
				input.Operations = []commandschema.ChangePlanOperationInput{{Kind: "dependency", Action: action, Module: module}}
				plan, err := commandschema.NewChangePlan(input)
				if err != nil || !plan.Valid() {
					t.Fatalf("dependency plan: %v", err)
				}
				if previous, exists := digests[plan.PlanDigest()]; exists {
					t.Fatalf("dependency identity collides with %s", previous)
				}
				digests[plan.PlanDigest()] = action + " " + module
				var document struct{ Operations []map[string]any }
				if err := json.Unmarshal(plan.CanonicalJSON(), &document); err != nil {
					t.Fatal(err)
				}
				want := []map[string]any{{"kind": "dependency", "action": action, "module": module, "derived": false}}
				if !reflect.DeepEqual(document.Operations, want) {
					t.Fatalf("dependency projection = %#v, want %#v", document.Operations, want)
				}
			})
		}
	}
}

func TestChangePlanCanonicalizesDependencyOrder(t *testing.T) {
	operations := []commandschema.ChangePlanOperationInput{
		{Kind: "dependency", Action: "add", Module: "example.com/a-add@latest"},
		{Kind: "dependency", Action: "add", Module: "example.com/z-add@v1.2.3"},
		{Kind: "dependency", Action: "remove", Module: "example.com/a-remove"},
		{Kind: "dependency", Action: "remove", Module: "example.com/z-remove"},
		{Kind: "dependency", Action: "update", Module: "example.com/a-update@main"},
		{Kind: "dependency", Action: "update", Module: "example.com/z-update@v1.2.3"},
		{Kind: "interface_root", Action: "present", Interface: "order.create/v1", Root: "require"},
	}
	input := changePlanEffectInput(t)
	input.Operations = append([]commandschema.ChangePlanOperationInput(nil), operations...)
	baseline, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	var document struct{ Operations []map[string]any }
	if err := json.Unmarshal(baseline.CanonicalJSON(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Operations) != len(operations) {
		t.Fatalf("operation count = %d, want %d", len(document.Operations), len(operations))
	}
	for index, operation := range operations[:len(operations)-1] {
		if document.Operations[index]["action"] != operation.Action || document.Operations[index]["module"] != operation.Module {
			t.Fatalf("dependency order = %#v", document.Operations)
		}
	}
	if _, present := document.Operations[len(operations)-1]["module"]; present {
		t.Fatal("non-dependency operation serialized a module")
	}
	for offset := range operations {
		for _, reversed := range []bool{false, true} {
			input.Operations = append(append([]commandschema.ChangePlanOperationInput(nil), operations[offset:]...), operations[:offset]...)
			if reversed {
				for left, right := 0, len(input.Operations)-1; left < right; left, right = left+1, right-1 {
					input.Operations[left], input.Operations[right] = input.Operations[right], input.Operations[left]
				}
			}
			original := append([]commandschema.ChangePlanOperationInput(nil), input.Operations...)
			plan, err := commandschema.NewChangePlan(input)
			if err != nil || !plan.Valid() || plan.PlanDigest() != baseline.PlanDigest() || !bytes.Equal(plan.CanonicalJSON(), baseline.CanonicalJSON()) {
				t.Fatalf("dependency permutation changed plan (offset %d, reversed %t): %v", offset, reversed, err)
			}
			if !reflect.DeepEqual(input.Operations, original) {
				t.Fatal("canonicalization reordered caller-owned operations")
			}
		}
	}
}

func TestChangePlanOwnsDependencyInputsAndJSON(t *testing.T) {
	input := changePlanEffectInput(t)
	input.Operations = []commandschema.ChangePlanOperationInput{
		{Kind: "dependency", Action: "add", Module: "example.com/orders@v1.2.3"},
		{Kind: "dependency", Action: "remove", Module: "example.com/old"},
		{Kind: "dependency", Action: "update", Module: "example.com/new@latest"},
	}
	plan, err := commandschema.NewChangePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	canonical, digest := plan.CanonicalJSON(), plan.PlanDigest()
	for index := range input.Operations {
		input.Operations[index].Module = "example.com/replaced"
		input.Operations[index].Action = "remove"
	}
	exposed := plan.CanonicalJSON()
	exposed[0] = '!'
	if !plan.Valid() || plan.PlanDigest() != digest || !bytes.Equal(plan.CanonicalJSON(), canonical) {
		t.Fatal("caller mutation changed the prepared dependency plan")
	}
}

func TestChangePlanRejectsInvalidDependencyProjection(t *testing.T) {
	for _, action := range []string{"add", "remove", "update"} {
		invalidModules := []string{
			"", "local", "-u", "../orders", "./orders", "/orders", "https://example.com/orders",
			"Example.com/orders", "example.com/a/../orders", "example.com/orders@none", "example.com/orders@",
			"example.com/orders@other@main", " example.com/orders", "example.com/orders ",
			"example.com/orders@bad version", "example.com/orders@bad\nversion", "example.com/orders@bad\x00version",
			"example.com/orders@bad\u00a0version", "example.com/orders@bad\xffversion", "example.com/" + strings.Repeat("x", 4097),
		}
		if action == "remove" {
			invalidModules = append(invalidModules, "example.com/orders@v1.2.3", "example.com/orders@latest", "example.com/orders@main")
		}
		for _, module := range invalidModules {
			t.Run(action+"/"+module, func(t *testing.T) {
				input := changePlanEffectInput(t)
				input.Operations = []commandschema.ChangePlanOperationInput{{Kind: "dependency", Action: action, Module: module}}
				if _, err := commandschema.NewChangePlan(input); !errors.Is(err, commandschema.ErrChangePlanPayload) {
					t.Fatalf("invalid dependency module accepted: %v", err)
				}
			})
		}
	}
	for _, action := range []string{"", "set", "create", "upgrade", "Add"} {
		t.Run("action/"+action, func(t *testing.T) {
			input := changePlanEffectInput(t)
			input.Operations = []commandschema.ChangePlanOperationInput{{Kind: "dependency", Action: action, Module: "example.com/orders"}}
			if _, err := commandschema.NewChangePlan(input); !errors.Is(err, commandschema.ErrChangePlanPayload) {
				t.Fatalf("invalid dependency action accepted: %v", err)
			}
		})
	}
}

func TestChangePlanRejectsDependencyForeignFields(t *testing.T) {
	for _, action := range []string{"add", "remove", "update"} {
		for name, mutate := range map[string]func(*commandschema.ChangePlanOperationInput){
			"name":        func(v *commandschema.ChangePlanOperationInput) { v.Name = "order.create/v1" },
			"semantics":   func(v *commandschema.ChangePlanOperationInput) { v.Semantics = "command" },
			"contract":    func(v *commandschema.ChangePlanOperationInput) { v.Contract = "order.create/v1" },
			"package":     func(v *commandschema.ChangePlanOperationInput) { v.Package = "./orders" },
			"interface":   func(v *commandschema.ChangePlanOperationInput) { v.Interface = "order.create/v1" },
			"root":        func(v *commandschema.ChangePlanOperationInput) { v.Root = "require" },
			"constructor": func(v *commandschema.ChangePlanOperationInput) { v.Constructor = "example.com/orders.New" },
			"instance":    func(v *commandschema.ChangePlanOperationInput) { v.Instance = "database.primary" },
			"provider":    func(v *commandschema.ChangePlanOperationInput) { v.Provider = "example.com/database.New" },
			"owner":       func(v *commandschema.ChangePlanOperationInput) { v.Owner = "example.com/orders.New" },
			"path":        func(v *commandschema.ChangePlanOperationInput) { v.Path = "timeout" },
			"derived":     func(v *commandschema.ChangePlanOperationInput) { v.Derived = true },
		} {
			t.Run(action+"/"+name, func(t *testing.T) {
				input := changePlanEffectInput(t)
				operation := commandschema.ChangePlanOperationInput{Kind: "dependency", Action: action, Module: "example.com/orders"}
				mutate(&operation)
				input.Operations = append(input.Operations, operation)
				if _, err := commandschema.NewChangePlan(input); !errors.Is(err, commandschema.ErrChangePlanPayload) {
					t.Fatalf("dependency accepted foreign field: %v", err)
				}
			})
		}
	}
}

func TestChangePlanRejectsModuleOutsideDependencies(t *testing.T) {
	for _, operation := range []commandschema.ChangePlanOperationInput{
		{Kind: "interface_create", Action: "create", Name: "order.create/v1", Semantics: "command"},
		{Kind: "implementation_create", Action: "create", Contract: "order.create/v1", Package: "./orders"},
		{Kind: "interface_root", Action: "present", Interface: "order.create/v1", Root: "require"},
		{Kind: "implementation_selection", Action: "set", Interface: "order.create/v1", Constructor: "example.com/orders.New"},
		{Kind: "implementation_selection", Action: "remove", Interface: "order.create/v1"},
		{Kind: "resource_selection", Action: "set", Instance: "database.primary", Provider: "example.com/database.New"},
		{Kind: "resource_selection", Action: "remove", Instance: "database.primary"},
		{Kind: "configuration", Action: "set", Owner: "example.com/orders.New", Path: "timeout"},
		{Kind: "configuration", Action: "remove", Owner: "example.com/orders.New", Path: "timeout", Derived: true},
	} {
		t.Run(operation.Kind+"/"+operation.Action, func(t *testing.T) {
			input := changePlanEffectInput(t)
			input.Operations = append(input.Operations, operation)
			if plan, err := commandschema.NewChangePlan(input); err != nil || !plan.Valid() {
				t.Fatalf("non-dependency baseline: %v", err)
			}
			input.Operations[len(input.Operations)-1].Module = "example.com/orders"
			if _, err := commandschema.NewChangePlan(input); !errors.Is(err, commandschema.ErrChangePlanPayload) {
				t.Fatalf("non-dependency accepted module: %v", err)
			}
		})
	}
}
