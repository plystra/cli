package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestCleanupSelectionOwnershipTouchesOnlyProvenPaths(t *testing.T) {
	t.Parallel()
	const orphan = "example.com/orphan.New"
	const inherited = "example.com/inherited.New"
	input := []byte(`# selected layer
interfaces: {use: {app.run/v1: example.com/retained.New}}
config:
  example.com/orphan.New: {value: PRIVATE_ORPHAN}
  example.com/inherited.New: {value: PRIVATE_INHERITED} # inherited owner
  example.com/retained.New: {value: PRIVATE_RETAINED} # keep config
  example.com/excluded.New: {$remove: true}
resources:
  instances:
    primary: {use: example.com/database.New, config: {value: PRIVATE_INSTANCE}}
  bind:
    implementations:
      example.com/orphan.New:
        local: primary
        inherited: primary # removed inherited leaf
        retained: primary # preserve unlisted leaf
        excluded: {$remove: true}
      example.com/retained.New: {local: primary}
    instances:
      primary: {local: primary, inherited: primary, retained: primary}
`)
	configurations := []applicationmeta.ConstructorConfigurationRemoval{
		{Constructor: mustImplementationChoiceConstructor(t, orphan)},
		{Constructor: mustImplementationChoiceConstructor(t, inherited), Tombstone: true},
	}
	bindings := []applicationmeta.ResourceBindingRemoval{
		{Namespace: "implementations", Consumer: orphan, ParameterName: "local"},
		{Namespace: "implementations", Consumer: orphan, ParameterName: "inherited", Tombstone: true},
		{Namespace: "instances", Consumer: "primary", ParameterName: "local"},
		{Namespace: "instances", Consumer: "primary", ParameterName: "inherited", Tombstone: true},
	}
	before := bytes.Clone(input)
	updated, changed, err := applicationmeta.CleanupSelectionOwnershipOverlay(input, configurations, bindings)
	if err != nil || !changed {
		t.Fatalf("cleanup: %t, %v", changed, err)
	}
	want := selectionYAML(t, input)
	config := selectionMap(t, want, "config")
	delete(config, orphan)
	config[inherited] = map[string]any{"$remove": true}
	for _, address := range [][2]string{{"implementations", orphan}, {"instances", "primary"}} {
		parameters := selectionMap(t, want, "resources", "bind", address[0], address[1])
		delete(parameters, "local")
		parameters["inherited"] = map[string]any{"$remove": true}
	}
	if !reflect.DeepEqual(selectionYAML(t, updated), want) || !bytes.Equal(before, input) {
		t.Fatal("cleanup changed input or unrelated semantics")
	}
	for _, comment := range []string{"# selected layer", "# inherited owner", "# keep config", "# removed inherited leaf", "# preserve unlisted leaf"} {
		if !bytes.Contains(updated, []byte(comment)) {
			t.Fatalf("lost comment %q", comment)
		}
	}
	again, changed, err := applicationmeta.CleanupSelectionOwnershipOverlay(updated, configurations, bindings)
	if err != nil || changed || !bytes.Equal(updated, again) {
		t.Fatalf("cleanup is not idempotent: %t, %v", changed, err)
	}
}

func TestCleanupSelectionOwnershipIsSparseDeterministicAndNoncascading(t *testing.T) {
	t.Parallel()
	configurations := []applicationmeta.ConstructorConfigurationRemoval{
		{Constructor: mustImplementationChoiceConstructor(t, "example.com/z.New"), Tombstone: true},
		{Constructor: mustImplementationChoiceConstructor(t, "example.com/a.New"), Tombstone: true},
	}
	bindings := []applicationmeta.ResourceBindingRemoval{
		{Namespace: "instances", Consumer: "primary", ParameterName: "Database", Tombstone: true},
		{Namespace: "implementations", Consumer: "example.com/orphan.New", ParameterName: "database", Tombstone: true},
		{Namespace: "instances", Consumer: "primary", ParameterName: "database", Tombstone: true},
		{Namespace: "instances", Consumer: "primary", ParameterName: "_cache", Tombstone: true},
		{Namespace: "instances", Consumer: "primary", ParameterName: "\u6570\u636e\u5e93", Tombstone: true},
	}
	originalConfigs := append([]applicationmeta.ConstructorConfigurationRemoval(nil), configurations...)
	originalBindings := append([]applicationmeta.ResourceBindingRemoval(nil), bindings...)
	first, changed, err := applicationmeta.CleanupSelectionOwnershipOverlay([]byte("{}\n"), configurations, bindings)
	if err != nil || !changed {
		t.Fatalf("sparse cleanup: %t, %v", changed, err)
	}
	if !reflect.DeepEqual(configurations, originalConfigs) || !reflect.DeepEqual(bindings, originalBindings) {
		t.Fatal("cleanup sorted caller-owned slices in place")
	}
	for left, right := 0, len(bindings)-1; left < right; left, right = left+1, right-1 {
		bindings[left], bindings[right] = bindings[right], bindings[left]
	}
	configurations[0], configurations[1] = configurations[1], configurations[0]
	second, _, err := applicationmeta.CleanupSelectionOwnershipOverlay([]byte("{}\n"), configurations, bindings)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("cleanup depends on decision-list ordering", err)
	}
	value := selectionYAML(t, first)
	resources := selectionMap(t, value, "resources")
	if len(resources) != 1 || resources["instances"] != nil {
		t.Fatal("binding cleanup materialized a Resource instance")
	}
	unchanged, changed, err := applicationmeta.CleanupSelectionOwnership([]byte("# unchanged\n{}\n"), nil, nil)
	if err != nil || changed || !bytes.Equal(unchanged, []byte("# unchanged\n{}\n")) {
		t.Fatalf("local missing-leaf cleanup is not a byte-preserving no-op: %t, %v", changed, err)
	}
}

func TestCleanupSelectionOwnershipRejectsInvalidPlansAndDocumentsPrivately(t *testing.T) {
	t.Parallel()
	valid := applicationmeta.ResourceBindingRemoval{Namespace: "instances", Consumer: "primary", ParameterName: "database"}
	constructor := mustImplementationChoiceConstructor(t, "example.com/orphan.New")
	for _, test := range []struct {
		name           string
		data           string
		configurations []applicationmeta.ConstructorConfigurationRemoval
		bindings       []applicationmeta.ResourceBindingRemoval
	}{
		{"empty constructor", "{}", []applicationmeta.ConstructorConfigurationRemoval{{}}, nil},
		{"duplicate constructor", "{}", []applicationmeta.ConstructorConfigurationRemoval{{Constructor: constructor}, {Constructor: constructor, Tombstone: true}}, nil},
		{"duplicate binding", "{}", nil, []applicationmeta.ResourceBindingRemoval{valid, valid}},
		{"namespace", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "PRIVATE_INPUT", Consumer: "primary", ParameterName: "database"}}},
		{"instance", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "instances", Consumer: "PRIVATE_INPUT", ParameterName: "database"}}},
		{"constructor", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "implementations", Consumer: "PRIVATE_INPUT", ParameterName: "database"}}},
		{"parameter", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "instances", Consumer: "primary", ParameterName: "PRIVATE-INPUT"}}},
		{"blank parameter", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "instances", Consumer: "primary", ParameterName: "_"}}},
		{"keyword parameter", "{}", nil, []applicationmeta.ResourceBindingRemoval{{Namespace: "instances", Consumer: "primary", ParameterName: "for"}}},
		{"invalid document", "PRIVATE_INPUT: true", nil, nil},
		{"invalid target", "resources: {bind: {instances: {primary: {database: PRIVATE_INPUT}}}}", nil, []applicationmeta.ResourceBindingRemoval{valid}},
		{"duplicate YAML key", "config: {example.com/orphan.New: {}, example.com/orphan.New: {value: PRIVATE_INPUT}}", []applicationmeta.ConstructorConfigurationRemoval{{Constructor: constructor}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(test.data)
			before := bytes.Clone(input)
			updated, changed, err := applicationmeta.CleanupSelectionOwnership(input, test.configurations, test.bindings)
			if !errors.Is(err, applicationmeta.ErrCleanupSelectionOwnership) || updated != nil || changed || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "PRIVATE_INPUT") {
				t.Fatalf("invalid cleanup accepted or leaked: %t, %v", changed, err)
			}
			if !bytes.Equal(before, input) {
				t.Fatal("rejection changed input bytes")
			}
		})
	}
	_, _, err := applicationmeta.CleanupSelectionOwnershipOverlay([]byte("template: example.com/base\n"), nil, nil)
	if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !errors.Is(err, applicationmeta.ErrCleanupSelectionOwnership) {
		t.Fatalf("overlay accepted root metadata: %v", err)
	}
}
