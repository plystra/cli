package applicationmeta_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestImplementationSelectionIdentityMatchesAuthoredEdit(t *testing.T) {
	t.Parallel()
	id := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/email/new.New")
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, overlay := range []bool{false, true} {
		for _, remove := range []bool{false, true} {
			for _, choice := range []string{"", "interfaces: {use: {email.send/v1: example.com/email/old.New}}\n"} {
				input := []byte(choice + "config: {example.com/email/old.New: {private: PRIVATE_VALUE}}\n")
				parse, set, del := applicationmeta.ParseCompleteSource, applicationmeta.SetImplementationChoice, applicationmeta.RemoveImplementationChoice
				if overlay {
					parse, set, del = applicationmeta.ParseOverlaySource, applicationmeta.SetImplementationChoiceOverlay, applicationmeta.RemoveImplementationChoiceOverlay
				}
				before, err := parse("deploy/selected.yaml", input)
				if err != nil {
					t.Fatal(err)
				}
				selected := constructor
				updated, _, err := set(input, id, constructor)
				if remove {
					selected = constructorsymbol.Symbol{}
					updated, _, err = del(input, id)
				}
				if err != nil {
					t.Fatal(err)
				}
				expected, err := parse("deploy/selected.yaml", updated)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := applicationmeta.ImplementationSelectionIdentity(before, id, selected, remove, overlay)
				if err != nil || len(projected.Configurations()) != 0 || len(before.Configurations()) != 1 {
					t.Fatalf("projection changed captured Config or retained values: %v", err)
				}
				want, err := applicationmeta.ConfigurationLayerDigest(applicationmeta.WithoutConstructorConfiguration(expected), schemas)
				if err != nil {
					t.Fatal(err)
				}
				got, err := applicationmeta.ConfigurationLayerDigest(projected, schemas)
				if err != nil || got != want || !slices.Equal(projected.ImplementationChoices(), expected.ImplementationChoices()) {
					t.Fatalf("projection differs from authored choice semantics: %v", err)
				}
			}
		}
	}
	for _, invalid := range []struct {
		id          interfaceid.Identifier
		constructor constructorsymbol.Symbol
		remove      bool
	}{{}, {id: id}, {id: id, constructor: constructor, remove: true}} {
		manifest, _ := applicationmeta.Parse([]byte("{}"))
		if _, err := applicationmeta.ImplementationSelectionIdentity(manifest, invalid.id, invalid.constructor, invalid.remove, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid identity projection accepted")
		}
	}
}

func TestEditInterfaceConfigurationSelectionUsesOnlyFinalBounds(t *testing.T) {
	t.Parallel()
	id := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/email/new.New")
	schema := composeSchema(t, "Value string\n")
	value, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceImplementation, constructor.String(), "value", schema, []byte(`"PRIVATE_NEW"`))
	if err != nil {
		t.Fatal(err)
	}
	old := mustImplementationChoiceConstructor(t, "example.com/email/old.New")
	input := []byte("# KEEP\nconfig: {" + old.String() + ": {value: " + strings.Repeat("x", applicationmeta.MaximumSize-96) + "}}\n")
	if _, _, err := applicationmeta.SetImplementationChoice(input, id, constructor); err == nil {
		t.Fatal("fixture does not exceed the selection-only intermediate bound")
	}
	edit := applicationmeta.InterfaceConfigurationEdit{Roots: []applicationmeta.InterfaceRootEdit{{Interface: id, Root: "expose", Present: true}}, Selections: []applicationmeta.ImplementationSelectionEdit{{Interface: id, Constructor: constructor}}, Values: []applicationmeta.ConfigurationValueEdit{value}, Configurations: []applicationmeta.ConstructorConfigurationRemoval{{Constructor: old}}}
	updated, changed, err := applicationmeta.EditInterfaceConfiguration(input, edit, false)
	if err != nil || !changed || len(updated) > 1024 || !bytes.Contains(updated, []byte("# KEEP")) {
		t.Fatalf("valid complete edit rejected: %t, %v", changed, err)
	}
	edit.Configurations = []applicationmeta.ConstructorConfigurationRemoval{{Constructor: constructor}}
	if updated, changed, err := applicationmeta.EditInterfaceConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || updated != nil {
		t.Fatal("overlapping owner cleanup accepted")
	}
}
