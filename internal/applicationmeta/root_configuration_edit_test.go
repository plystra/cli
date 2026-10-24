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

func TestInterfaceRootIdentityMatchesAuthoredEdit(t *testing.T) {
	t.Parallel()
	id := mustInterfaceRootID(t)
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, overlay := range []bool{false, true} {
		for _, kind := range []string{"require", "expose"} {
			for _, present := range []bool{false, true} {
				for _, document := range []string{
					"{}\n", "interfaces: {require: []}\n",
					"interfaces: {require: [app.run/v1, other.run/v1]}\nhttp: {expose: {app.run/v1: {transport: connect}}}\n",
					"interfaces: {require: {add: [other.run/v1], remove: [app.run/v1]}}\nhttp: {expose: {app.run/v1: {$remove: true}}}\n",
				} {
					parse, edit := applicationmeta.ParseCompleteSource, applicationmeta.SetInterfaceRoot
					if overlay {
						parse, edit = applicationmeta.ParseOverlaySource, applicationmeta.SetInterfaceRootOverlay
					} else if strings.Contains(document, "remove:") {
						continue
					}
					before, err := parse("deploy/selected.yaml", []byte(document))
					if err != nil {
						t.Fatal(err)
					}
					capturedRequirements, capturedExposures := before.InterfaceRequirements(), before.HTTPExposures()
					updated, _, err := edit([]byte(document), id, kind, present)
					if err != nil {
						t.Fatal(err)
					}
					expected, err := parse("deploy/selected.yaml", updated)
					if err != nil {
						t.Fatal(err)
					}
					projected, err := applicationmeta.InterfaceRootsIdentity(before, []applicationmeta.InterfaceRootEdit{{Interface: id, Root: kind, Present: present}}, overlay)
					if err != nil {
						t.Fatal(err)
					}
					want, err := applicationmeta.ConfigurationLayerDigest(expected, schemas)
					if err != nil {
						t.Fatal(err)
					}
					got, err := applicationmeta.ConfigurationLayerDigest(projected, schemas)
					if err != nil || got != want || !slices.Equal(projected.InterfaceRequirements(), expected.InterfaceRequirements()) || !slices.Equal(projected.HTTPExposures(), expected.HTTPExposures()) {
						t.Fatalf("root identity differs from authored %s %t (%s): %v", kind, present, document, err)
					}
					if !slices.Equal(capturedRequirements, before.InterfaceRequirements()) || !slices.Equal(capturedExposures, before.HTTPExposures()) {
						t.Fatal("projection changed captured root declarations")
					}
				}
			}
		}
	}
	for _, kind := range []string{"", "invalid"} {
		manifest, _ := applicationmeta.Parse([]byte("{}"))
		if _, err := applicationmeta.InterfaceRootsIdentity(manifest, []applicationmeta.InterfaceRootEdit{{Interface: id, Root: kind, Present: true}}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid root identity accepted")
		}
	}
	if _, err := applicationmeta.InterfaceRootsIdentity(applicationmeta.Manifest{}, []applicationmeta.InterfaceRootEdit{{Interface: interfaceid.Identifier{}, Root: "require", Present: true}}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
		t.Fatal("empty identity accepted")
	}
}

func TestEditSelectionConfigurationUsesOnlyFinalBounds(t *testing.T) {
	t.Parallel()
	owner := mustImplementationChoiceConstructor(t, "example.com/email/old.New")
	schema := composeSchema(t, "Value string\n")
	value, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceImplementation, owner.String(), "value", schema, []byte(`"PRIVATE_NEW"`))
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("# KEEP\nconfig: {" + owner.String() + ": {value: " + strings.Repeat("x", applicationmeta.MaximumSize-96) + "}}\n")
	id := mustInterfaceRootID(t)
	if _, _, err := applicationmeta.SetInterfaceRoot(input, id, "expose", true); err == nil {
		t.Fatal("fixture does not exceed the root-only intermediate bound")
	}
	edit := applicationmeta.SelectionConfigurationEdit{Roots: []applicationmeta.InterfaceRootEdit{{Interface: id, Root: "expose", Present: true}, {Interface: id, Root: "require", Present: true}}, Values: []applicationmeta.ConfigurationValueEdit{value}}
	updated, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, false)
	if err != nil || !changed || len(updated) > 1024 || !bytes.Contains(updated, []byte("# KEEP")) {
		t.Fatalf("valid complete root edit rejected: %t, %v", changed, err)
	}
	edit.Configurations = []applicationmeta.ConstructorConfigurationRemoval{{Constructor: owner}}
	if data, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil {
		t.Fatal("field edit on removed owner accepted")
	}
	edit.Configurations, edit.Roots[0].PreserveRoot, edit.Roots[0].Present = nil, true, false
	if _, _, err := applicationmeta.EditSelectionConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
		t.Fatal("preserving an absent root accepted")
	}
}

func TestSelectionChangesIdentityMatchesFinalAuthoredEdit(t *testing.T) {
	t.Parallel()
	id := mustInterfaceRootID(t)
	other, _ := interfaceid.Parse("other.run/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/email/new.New")
	roots := []applicationmeta.InterfaceRootEdit{{Interface: id, Root: "require"}, {Interface: id, Root: "expose", Present: true}}
	selections := []applicationmeta.ImplementationSelectionEdit{{Interface: id, Constructor: constructor}, {Interface: other, Remove: true}}
	resources := []applicationmeta.ResourceSelectionEdit{
		{Instance: "database.primary", Provider: mustImplementationChoiceConstructor(t, resourceProvider)},
		{Instance: "database.retained", Remove: true},
	}
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, overlay := range []bool{false, true} {
		input := []byte("# KEEP\ninterfaces: {require: [app.run/v1], use: {other.run/v1: example.com/email/old.New}}\nconfig: {example.com/email/old.New: {value: PRIVATE_VALUE}}\nresources: {instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}, database.retained: {use: " + resourceProvider + ", config: {value: PRIVATE_RETAINED}}}}\n")
		parse := applicationmeta.ParseCompleteSource
		if overlay {
			parse = applicationmeta.ParseOverlaySource
		}
		before, err := parse("selected.yaml", input)
		if err != nil {
			t.Fatal(err)
		}
		for _, reverse := range []bool{false, true} {
			rootEdits, choices, instances := slices.Clone(roots), slices.Clone(selections), slices.Clone(resources)
			if reverse {
				slices.Reverse(rootEdits)
				slices.Reverse(choices)
				slices.Reverse(instances)
			}
			updated, _, err := applicationmeta.EditSelectionConfiguration(input, applicationmeta.SelectionConfigurationEdit{Roots: rootEdits, Selections: choices, Resources: instances}, overlay)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := parse("selected.yaml", updated)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := applicationmeta.SelectionChangesIdentity(before, rootEdits, choices, instances, overlay)
			if err != nil || len(projected.Configurations()) != 0 || len(before.Configurations()) != 1 {
				t.Fatalf("joint projection mutated Config: %v", err)
			}
			want, err := applicationmeta.ConfigurationLayerDigest(applicationmeta.WithoutConstructorConfiguration(expected), schemas)
			if err != nil {
				t.Fatal(err)
			}
			got, err := applicationmeta.ConfigurationLayerDigest(projected, schemas)
			if err != nil || got != want {
				t.Fatalf("joint identity differs from authored final document: %v", err)
			}
		}
	}
	for _, choices := range [][]applicationmeta.ImplementationSelectionEdit{{}, {selections[0], selections[0]}, {{Interface: id}}, {{Interface: id, Constructor: constructor, Remove: true}}} {
		manifest, _ := applicationmeta.Parse([]byte("{}"))
		if _, err := applicationmeta.SelectionChangesIdentity(manifest, nil, choices, nil, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid joint identity accepted")
		}
		if len(choices) == 0 {
			continue // The document editor also supports edits without choices.
		}
		if data, changed, err := applicationmeta.EditSelectionConfiguration([]byte("{}"), applicationmeta.SelectionConfigurationEdit{Selections: choices}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil {
			t.Fatal("invalid joint edit accepted")
		}
	}
}

func TestInterfaceRootBatchIdentityMatchesFinalAuthoredEdit(t *testing.T) {
	t.Parallel()
	id := mustInterfaceRootID(t)
	other, _ := interfaceid.Parse("other.run/v1")
	roots := []applicationmeta.InterfaceRootEdit{{Interface: id, Root: "require"}, {Interface: id, Root: "expose", Present: true}, {Interface: other, Root: "require", Present: true}}
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, overlay := range []bool{false, true} {
		for _, document := range []string{"{}\n", "interfaces: {require: [app.run/v1]}\nhttp: {expose: {}}\n", "interfaces: {require: {add: [app.run/v1], remove: [other.run/v1]}}\nhttp: {expose: {app.run/v1: {$remove: true}}}\n"} {
			if !overlay && strings.Contains(document, "remove:") {
				continue
			}
			parse := applicationmeta.ParseCompleteSource
			if overlay {
				parse = applicationmeta.ParseOverlaySource
			}
			before, err := parse("selected.yaml", []byte(document))
			if err != nil {
				t.Fatal(err)
			}
			captured, err := applicationmeta.ConfigurationLayerDigest(before, schemas)
			if err != nil {
				t.Fatal(err)
			}
			for _, reversed := range []bool{false, true} {
				edits := slices.Clone(roots)
				if reversed {
					slices.Reverse(edits)
				}
				updated, _, err := applicationmeta.EditSelectionConfiguration([]byte(document), applicationmeta.SelectionConfigurationEdit{Roots: edits}, overlay)
				if err != nil {
					t.Fatal(err)
				}
				expected, err := parse("selected.yaml", updated)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := applicationmeta.InterfaceRootsIdentity(before, edits, overlay)
				if err != nil {
					t.Fatal(err)
				}
				want, _ := applicationmeta.ConfigurationLayerDigest(expected, schemas)
				got, err := applicationmeta.ConfigurationLayerDigest(projected, schemas)
				if err != nil || want != got {
					t.Fatalf("batch projection differs from complete authored edit: %v", err)
				}
			}
			if after, _ := applicationmeta.ConfigurationLayerDigest(before, schemas); after != captured {
				t.Fatal("batch projection mutated captured root declarations")
			}
		}
	}
	manifest, _ := applicationmeta.Parse([]byte("{}"))
	for _, invalid := range [][]applicationmeta.InterfaceRootEdit{nil, {roots[0], roots[0]}, {{Interface: id, Root: "invalid"}}, {{Interface: id, Root: "require", PreserveRoot: true}}} {
		if _, err := applicationmeta.InterfaceRootsIdentity(manifest, invalid, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid root batch identity accepted")
		}
		if len(invalid) == 0 {
			continue
		}
		if data, changed, err := applicationmeta.EditSelectionConfiguration([]byte("{}"), applicationmeta.SelectionConfigurationEdit{Roots: invalid}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || data != nil || changed {
			t.Fatal("invalid root batch edit accepted")
		}
	}
}

func TestEditSelectionConfigurationWithoutSelections(t *testing.T) {
	t.Parallel()
	owner := mustImplementationChoiceConstructor(t, "example.com/email/current.New")
	old := mustImplementationChoiceConstructor(t, "example.com/email/old.New")
	schema := composeSchema(t, "Value string\n")
	value, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceImplementation, owner.String(), "value", schema, []byte(`"PRIVATE_NEW"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, overlay := range []bool{false, true} {
		input := []byte("# KEEP\ninterfaces: {require: [app.run/v1]}\nconfig: {" + owner.String() + ": {value: PRIVATE_KEEP}, " + old.String() + ": {value: PRIVATE_OLD}}\nresources: {bind: {implementations: {" + old.String() + ": {primary: database}}}}\n")
		before := bytes.Clone(input)
		for _, test := range []struct {
			name            string
			fields, cleanup bool
		}{
			{"noop", false, false}, {"fields-only", true, false}, {"cleanup-only", false, true}, {"cleanup-and-fields", true, true},
		} {
			t.Run(test.name+map[bool]string{false: "-complete", true: "-overlay"}[overlay], func(t *testing.T) {
				edit := applicationmeta.SelectionConfigurationEdit{}
				if test.fields {
					edit.Values = []applicationmeta.ConfigurationValueEdit{value}
				}
				if test.cleanup {
					edit.Configurations = []applicationmeta.ConstructorConfigurationRemoval{{Constructor: old, Tombstone: overlay}}
					edit.Bindings = []applicationmeta.ResourceBindingRemoval{{Namespace: "implementations", Consumer: old.String(), ParameterName: "primary", Tombstone: overlay}}
				}
				updated, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, overlay)
				if err != nil || changed != (test.fields || test.cleanup) || !bytes.Equal(input, before) || !bytes.Contains(updated, []byte("# KEEP")) {
					t.Fatalf("document-only edit = %t, %v", changed, err)
				}
				if !test.fields && !test.cleanup && !bytes.Equal(updated, before) {
					t.Fatal("no-op changed captured document bytes")
				}
				if test.fields != bytes.Contains(updated, []byte("PRIVATE_NEW")) || test.cleanup == bytes.Contains(updated, []byte("PRIVATE_OLD")) {
					t.Fatal("field or cleanup edit changed the wrong owned paths")
				}
				if overlay && test.cleanup && bytes.Count(updated, []byte("$remove: true")) != 2 {
					t.Fatal("cleanup did not preserve the exact overlay exclusions")
				}
				updated[0] = '!'
				if !bytes.Equal(input, before) {
					t.Fatal("editor returned aliased bytes")
				}
			})
		}
		for _, edit := range []applicationmeta.SelectionConfigurationEdit{
			{Values: []applicationmeta.ConfigurationValueEdit{{}}},
			{Configurations: []applicationmeta.ConstructorConfigurationRemoval{{Constructor: owner}}, Values: []applicationmeta.ConfigurationValueEdit{value}},
		} {
			if data, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, overlay); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil {
				t.Fatal("invalid field or overlapping cleanup accepted without selections")
			}
		}
	}
}

func TestEditSelectionConfigurationWithoutSelectionsUsesFinalBounds(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Before string\nZAfter string\n")
	input := []byte("config:\n  " + constructorConfigurationSymbol + ":\n    zafter: " + strings.Repeat("x", applicationmeta.MaximumSize-150) + "\n")
	value := []byte(`"` + strings.Repeat("a", 256) + `"`)
	set, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceImplementation, constructorConfigurationSymbol, "before", schema, value)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceImplementation, constructorConfigurationSymbol, "zafter", schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := applicationmeta.EditSelectionConfiguration(input, applicationmeta.SelectionConfigurationEdit{Values: []applicationmeta.ConfigurationValueEdit{set}}, false); err == nil {
		t.Fatal("fixture does not exceed the intermediate document bound")
	}
	for _, values := range [][]applicationmeta.ConfigurationValueEdit{{set, remove}, {remove, set}} {
		updated, changed, err := applicationmeta.EditSelectionConfiguration(input, applicationmeta.SelectionConfigurationEdit{Values: values}, false)
		if err != nil || !changed || len(updated) >= 1024 {
			t.Fatalf("final-sized edit rejected without selections: %t, %v", changed, err)
		}
	}
	for _, invalid := range [][]byte{[]byte("[PRIVATE_INVALID"), []byte("#" + strings.Repeat("x", applicationmeta.MaximumSize))} {
		if data, changed, err := applicationmeta.EditSelectionConfiguration(invalid, applicationmeta.SelectionConfigurationEdit{}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatal("a no-op bypassed document validation or disclosed private input")
		}
	}
}
