package interfaceprovenance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceprovenance"
)

func resourceProvenanceInput() interfaceprovenance.Input {
	input := completeInput()
	selection := []interfaceprovenance.ResourceSource{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-selection", Line: 1, Column: 1}, {Module: "example.com/template", Path: "plystra.yaml", Kind: "resource-selection", Line: 1, Column: 1}}
	provider := interfaceprovenance.ResourceSource{Module: "example.com/provider", Path: "database/new.go", Kind: "resource-provider-constructor", Line: 9, Column: 6}
	for i, name := range []string{"database.primary", "database.secondary", "database.unconsumed"} {
		input.Resources = append(input.Resources, interfaceprovenance.ResourceInput{Name: name, ResourceID: "data.database/v1", PackagePath: "example.com/contracts/database", ContractDigest: digest("d"), Provider: "example.com/provider/database.New", ModulePath: "example.com/provider", ModuleVersion: "v1.2.3", DeclarationSource: provider, SelectionSources: selection, ConstructionOrder: i + 1, ConfigurationOwner: `resources.instances["` + name + `"].config`})
	}
	for i, name := range []string{"database", "Database", "\u03b4"} {
		input.ResourceBindings = append(input.ResourceBindings, interfaceprovenance.ResourceBindingInput{ConsumerKind: "implementations", Consumer: "example.com/app/order.New", Constructor: "example.com/app/order.New", ResourceID: input.Resources[0].ResourceID, PackagePath: input.Resources[0].PackagePath, ParameterName: name, ParameterPosition: i + 4, InstanceName: input.Resources[i%2].Name, Provider: input.Resources[0].Provider, Reason: interfaceprovenance.SelectionExplicit, DeclarationSource: interfaceprovenance.ResourceSource{Module: "example.com/app", Path: "order/order.go", Kind: "implementation-constructor", Line: 22, Column: 1}, BindingSources: []interfaceprovenance.ResourceSource{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-binding", Line: 1, Column: 1}}, SelectionSources: selection})
	}
	input.Resources = append(input.Resources, interfaceprovenance.ResourceInput{Name: "view", ResourceID: "data.view/v1", PackagePath: "example.com/contracts/view", ContractDigest: digest("e"), Provider: "example.com/provider/view.New", ModulePath: "example.com/provider", ModuleVersion: "v1.2.3", DeclarationSource: interfaceprovenance.ResourceSource{Module: "example.com/provider", Path: "view/new.go", Kind: "resource-provider-constructor", Line: 3, Column: 6}, SelectionSources: selection, ConstructionOrder: 4})
	binding := input.ResourceBindings[0]
	binding.ConsumerKind = "instances"
	binding.Consumer = "view"
	binding.Constructor = input.Resources[3].Provider
	binding.DeclarationSource = input.Resources[3].DeclarationSource
	binding.ParameterName = "upstream"
	binding.ParameterPosition = 1
	binding.ConsumerSelectionSources = selection
	input.ResourceBindings = append(input.ResourceBindings, binding)
	for i := range input.Resources {
		resource := &input.Resources[i]
		resource.ContractSource = interfaceprovenance.ResourceSource{Module: "example.com/contracts", Path: strings.TrimPrefix(resource.PackagePath, "example.com/contracts/") + "/resource.go", Kind: "resource-declaration", Line: 2, Column: 1}
		if resource.ConfigurationOwner != "" {
			resource.ConfigurationSources = []interfaceprovenance.ResourceSource{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "configuration-declaration", Line: 5, Column: 7}, {Module: "example.com/template", Path: "plystra.yaml", Kind: "configuration-declaration", Line: 8, Column: 7}}
		}
	}
	return input
}

func TestResourceProvenanceRetainsDistinctInstancesAndExactEdges(t *testing.T) {
	t.Parallel()
	input := resourceProvenanceInput()
	provenance, err := interfaceprovenance.New(input)
	if err != nil || !provenance.Valid() {
		t.Fatalf("New: %v", err)
	}
	if len(provenance.Resources()) != 4 || len(provenance.ResourceBindings()) != 4 || len(provenance.Bindings()) != len(input.Bindings) || len(provenance.Constructors()) != len(input.Constructors) {
		t.Fatal("Resource records changed Interface membership or collapsed instances")
	}
	before := provenance.RecordJSON()
	for _, fragment := range []string{`"database.unconsumed"`, `"consumer_kind":"instances"`, `"consumer_kind":"implementations"`, `"parameter_name":"Database"`, `"parameter_position":6`, `"resource_bindings"`, `"resource-selection"`, `"resource-binding"`} {
		if !bytes.Contains(before, []byte(fragment)) {
			t.Fatalf("missing %s", fragment)
		}
	}
	decoded, err := interfaceprovenance.Decode(before)
	if err != nil || !decoded.Valid() || !bytes.Equal(decoded.RecordJSON(), before) {
		t.Fatalf("Decode: %v", err)
	}
	slices.Reverse(input.Resources)
	slices.Reverse(input.ResourceBindings)
	for i := range input.Resources {
		slices.Reverse(input.Resources[i].SelectionSources)
	}
	reordered, err := interfaceprovenance.New(input)
	if err != nil || !bytes.Equal(before, reordered.RecordJSON()) {
		t.Fatalf("nondeterministic Resource input: %v", err)
	}
	provenance.Resources()[0].SelectionSources[0].Path = "changed.yaml"
	provenance.Resources()[0].ConfigurationSources[0].Path = "changed.yaml"
	provenance.ResourceBindings()[0].BindingSources[0].Path = "changed.yaml"
	provenance.ResourceBindings()[0].SelectionSources[0].Path = "changed.yaml"
	provenance.ResourceBindings()[3].ConsumerSelectionSources[0].Path = "changed.yaml"
	input.Resources[0].Name = "changed"
	if !provenance.Valid() || !bytes.Equal(before, provenance.RecordJSON()) {
		t.Fatal("mutable Resource record escaped")
	}
	changed := resourceProvenanceInput()
	changed.ResourceBindings[0].ParameterName = "newName"
	newProvenance, err := interfaceprovenance.New(changed)
	if err != nil || newProvenance.Digest() == provenance.Digest() {
		t.Fatalf("exact Resource parameter did not affect evidence identity: %v", err)
	}
}

func TestResourceProvenanceRejectsInconsistentUnsafeAndCyclicRecords(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*interfaceprovenance.Input){
		"name":            func(i *interfaceprovenance.Input) { i.Resources[0].Name = "Database" },
		"duplicate":       func(i *interfaceprovenance.Input) { i.Resources[1].Name = i.Resources[0].Name },
		"order":           func(i *interfaceprovenance.Input) { i.Resources[0].ConstructionOrder = 0 },
		"digest":          func(i *interfaceprovenance.Input) { i.Resources[0].ContractDigest = "private-value" },
		"provider":        func(i *interfaceprovenance.Input) { i.Resources[0].Provider = "bad" },
		"provider-module": func(i *interfaceprovenance.Input) { i.Resources[0].ModulePath = "example.com/other" },
		"absolute-source": func(i *interfaceprovenance.Input) { i.Resources[0].DeclarationSource.Path = "C:/private/secret.yaml" },
		"contract-source": func(i *interfaceprovenance.Input) { i.Resources[0].ContractSource.Path = "C:/private/secret.go" },
		"configuration-source": func(i *interfaceprovenance.Input) {
			i.Resources[0].ConfigurationSources[0].Path = "C:/private/secret.yaml"
		},
		"missing-source": func(i *interfaceprovenance.Input) { i.Resources[0].SelectionSources = nil },
		"configuration-owner": func(i *interfaceprovenance.Input) {
			i.Resources[0].ConfigurationOwner = `config["example.com/provider/database.New"]`
		},
		"absent-target": func(i *interfaceprovenance.Input) { i.ResourceBindings[0].InstanceName = "missing" },
		"contract":      func(i *interfaceprovenance.Input) { i.ResourceBindings[0].ResourceID = "data.other/v1" },
		"reason":        func(i *interfaceprovenance.Input) { i.ResourceBindings[0].Reason = "first" },
		"ambiguous-implicit": func(i *interfaceprovenance.Input) {
			i.ResourceBindings[0].Reason = interfaceprovenance.SelectionUniqueCompatible
			i.ResourceBindings[0].BindingSources = nil
		},
		"explicit-without-source": func(i *interfaceprovenance.Input) { i.ResourceBindings[0].BindingSources = nil },
		"consumer":                func(i *interfaceprovenance.Input) { i.ResourceBindings[0].ConsumerKind = "interface" },
		"dormant": func(i *interfaceprovenance.Input) {
			i.ResourceBindings[0].Consumer = "example.com/app/dormant.New"
			i.ResourceBindings[0].Constructor = i.ResourceBindings[0].Consumer
		},
		"name-collision": func(i *interfaceprovenance.Input) {
			i.ResourceBindings[1].ParameterName = i.ResourceBindings[0].ParameterName
		},
		"position-collision": func(i *interfaceprovenance.Input) {
			i.ResourceBindings[1].ParameterPosition = i.ResourceBindings[0].ParameterPosition
		},
		"interface-parameter-collision": func(i *interfaceprovenance.Input) { i.ResourceBindings[0].ParameterPosition = 2 },
		"unnamed":                       func(i *interfaceprovenance.Input) { i.ResourceBindings[0].ParameterName = "_" },
		"consumer-source":               func(i *interfaceprovenance.Input) { i.ResourceBindings[3].ConsumerSelectionSources = nil },
		"cycle": func(i *interfaceprovenance.Input) {
			i.Resources[0].ConstructionOrder, i.Resources[3].ConstructionOrder = 4, 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := resourceProvenanceInput()
			change(&input)
			if _, err := interfaceprovenance.New(input); !errors.Is(err, interfaceprovenance.ErrInvalid) {
				t.Fatalf("accepted invalid evidence: %v", err)
			}
		})
	}
}

func TestResourceProvenanceRejectsUnknownPrivateFields(t *testing.T) {
	t.Parallel()
	provenance, err := interfaceprovenance.New(resourceProvenanceInput())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(provenance.RecordJSON(), &document); err != nil {
		t.Fatal(err)
	}
	document["resources"].([]any)[0].(map[string]any)["config"] = map[string]any{"secret": "PRIVATE_REFERENCE"}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := interfaceprovenance.Decode(encoded); !errors.Is(err, interfaceprovenance.ErrRecord) || strings.Contains(err.Error(), "PRIVATE_REFERENCE") {
		t.Fatalf("unsafe private payload: %v", err)
	}
}

func TestResourceNormalizationAllowsUniqueImplicitBinding(t *testing.T) {
	input := resourceProvenanceInput()
	input.Resources = input.Resources[:1]
	input.ResourceBindings = input.ResourceBindings[:1]
	input.ResourceBindings[0].Reason = interfaceprovenance.SelectionUniqueCompatible
	input.ResourceBindings[0].BindingSources = nil
	resources, bindings, err := interfaceprovenance.NormalizeResources(input.Resources, input.ResourceBindings)
	if err != nil || len(resources) != 1 || len(bindings) != 1 || len(bindings[0].BindingSources) != 0 {
		t.Fatalf("unique implicit: %v", err)
	}
	if !reflect.DeepEqual(resources[0].SelectionSources, bindings[0].SelectionSources) {
		t.Fatal("selection sources differ")
	}
}
