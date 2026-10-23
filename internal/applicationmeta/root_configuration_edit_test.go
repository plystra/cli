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
					projected, err := applicationmeta.InterfaceRootIdentity(before, id, kind, present, overlay)
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
		if _, err := applicationmeta.InterfaceRootIdentity(manifest, id, kind, true, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid root identity accepted")
		}
	}
	if _, err := applicationmeta.InterfaceRootIdentity(applicationmeta.Manifest{}, interfaceid.Identifier{}, "require", true, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
		t.Fatal("empty identity accepted")
	}
}

func TestEditRootConfigurationUsesOnlyFinalBounds(t *testing.T) {
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
	edit := applicationmeta.RootConfigurationEdit{Interface: id, Root: "expose", Present: true, Values: []applicationmeta.ConfigurationValueEdit{value}}
	updated, changed, err := applicationmeta.EditRootConfiguration(input, edit, false)
	if err != nil || !changed || len(updated) > 1024 || !bytes.Contains(updated, []byte("# KEEP")) {
		t.Fatalf("valid complete root edit rejected: %t, %v", changed, err)
	}
	edit.Configurations = []applicationmeta.ConstructorConfigurationRemoval{{Constructor: owner}}
	if data, changed, err := applicationmeta.EditRootConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil {
		t.Fatal("field edit on removed owner accepted")
	}
	edit.Configurations, edit.PreserveRoot, edit.Present = nil, true, false
	if _, _, err := applicationmeta.EditRootConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
		t.Fatal("preserving an absent root accepted")
	}
}
