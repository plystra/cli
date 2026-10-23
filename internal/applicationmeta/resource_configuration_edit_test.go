package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestResourceSelectionIdentityMatchesAuthoredEdit(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	for _, overlay := range []bool{false, true} {
		for _, remove := range []bool{false, true} {
			for _, instance := range []string{"", "database.primary: {config: {value: [PRIVATE_INVALID]}},", "database.primary: {use: " + replacementResourceProvider + ", config: {value: PRIVATE_OLD}},", "database.primary: {$remove: true},"} {
				if !overlay && strings.Contains(instance, "$remove") {
					continue
				}
				data := []byte("resources: {instances: {" + instance + "database.retained: {use: " + resourceProvider + ", config: {value: PRIVATE_RETAINED}}}}\n")
				parse, set, del := applicationmeta.ParseCompleteSource, applicationmeta.SetResourceProvider, applicationmeta.RemoveResourceInstance
				if overlay {
					parse, set, del = applicationmeta.ParseOverlaySource, applicationmeta.SetResourceProviderOverlay, applicationmeta.RemoveResourceInstanceOverlay
				}
				before, err := parse("deploy/selected.yaml", data)
				if err != nil {
					t.Fatal(err)
				}
				originalDigest, err := applicationmeta.ConfigurationLayerDigest(before, schemas)
				if err != nil {
					t.Fatal(err)
				}
				choice := provider
				updated, _, err := set(data, "database.primary", provider, true, nil)
				if remove {
					choice = constructorsymbol.Symbol{}
					updated, _, err = del(data, "database.primary")
				}
				if err != nil {
					t.Fatal(err)
				}
				expected, err := parse("deploy/selected.yaml", updated)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := applicationmeta.ResourceSelectionIdentity(before, "database.primary", choice, remove, overlay)
				if err != nil {
					t.Fatal(err)
				}
				want, err := applicationmeta.ConfigurationLayerDigest(applicationmeta.WithoutConstructorConfiguration(expected), schemas)
				if err != nil {
					t.Fatal(err)
				}
				got, err := applicationmeta.ConfigurationLayerDigest(projected, schemas)
				if err != nil || want != got {
					t.Fatalf("Resource projection differs from authored edit: %v", err)
				}
				for _, value := range projected.ResourceInstances() {
					if value.HasConfiguration() {
						t.Fatal("identity projection retained private Config")
					}
				}
				afterDigest, err := applicationmeta.ConfigurationLayerDigest(before, schemas)
				if err != nil || originalDigest != afterDigest || len(before.ResourceInstances()) == 0 {
					t.Fatal("identity projection changed captured manifest")
				}
			}
		}
	}
	manifest, _ := applicationmeta.ParseCompleteSource("plystra.yaml", []byte("{}"))
	for _, invalid := range []struct {
		name     string
		provider constructorsymbol.Symbol
		remove   bool
	}{{}, {name: "INVALID", provider: provider}, {name: "database.primary"}, {name: "database.primary", provider: provider, remove: true}} {
		if _, err := applicationmeta.ResourceSelectionIdentity(manifest, invalid.name, invalid.provider, invalid.remove, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid Resource projection accepted")
		}
	}
	if _, err := applicationmeta.ResourceSelectionIdentity(applicationmeta.Manifest{}, "database.primary", provider, false, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
		t.Fatal("source-less manifest accepted")
	}
}

func TestEditResourceConfigurationUsesOnlyFinalBounds(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	value, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceResource, "database.primary", "value", composeSchema(t, "Value string\n"), []byte(`"PRIVATE_NEW"`))
	if err != nil {
		t.Fatal(err)
	}
	base := "# KEEP\nresources: {instances: {database.primary: {config: {value: PRIVATE_OLD}}}}\n"
	input := []byte(strings.Replace(base, "PRIVATE_OLD", strings.Repeat("x", applicationmeta.MaximumSize-len(base)+len("PRIVATE_OLD")-8), 1))
	if _, _, err := applicationmeta.SetResourceProvider(input, "database.primary", provider, false, nil); err == nil {
		t.Fatal("fixture does not exceed provider-only intermediate bound")
	}
	edit := applicationmeta.SelectionConfigurationEdit{Resources: []applicationmeta.ResourceSelectionEdit{{Instance: "database.primary", Provider: provider}}, Values: []applicationmeta.ConfigurationValueEdit{value}}
	updated, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, false)
	if err != nil || !changed || len(updated) > 1024 || !bytes.Contains(updated, []byte("# KEEP")) {
		t.Fatalf("valid complete edit rejected: %t, %v", changed, err)
	}
	again, changed, err := applicationmeta.EditSelectionConfiguration(updated, edit, false)
	if err != nil || !changed || !bytes.Equal(updated, again) {
		t.Fatal("equal-byte set lost write intent or changed comments")
	}
	edit.Resources[0].Provider, edit.Resources[0].Remove = constructorsymbol.Symbol{}, true
	if updated, changed, err := applicationmeta.EditSelectionConfiguration(input, edit, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || updated != nil {
		t.Fatal("Config edit on removed instance accepted")
	}
}

func TestEditResourceConfigurationCombinesReplacementAndBindingCleanup(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, replacementResourceProvider)
	value, err := applicationmeta.PrepareConfigurationValueEdit(applicationmeta.ConfigurationNamespaceResource, "database.primary", "value", composeSchema(t, "Value string\n"), []byte(`"PRIVATE_NEW"`))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("# KEEP\nresources: {instances: {database.primary: {use: " + resourceProvider + ", config: {$remove: true}}}, bind: {instances: {database.primary: {Old: database.retained, Keep: database.retained}}}}\n")
	before := bytes.Clone(data)
	edit := applicationmeta.SelectionConfigurationEdit{Resources: []applicationmeta.ResourceSelectionEdit{{Instance: "database.primary", Provider: provider, DiscardConfiguration: true}}, Values: []applicationmeta.ConfigurationValueEdit{value}, Bindings: []applicationmeta.ResourceBindingRemoval{{Namespace: "instances", Consumer: "database.primary", ParameterName: "Old", Tombstone: true}}}
	updated, changed, err := applicationmeta.EditSelectionConfiguration(data, edit, true)
	if err != nil || !changed || !bytes.Equal(data, before) || !bytes.Contains(updated, []byte("# KEEP")) {
		t.Fatalf("compound edit failed or changed original: %t, %v", changed, err)
	}
	want := selectionYAML(t, []byte("resources: {instances: {database.primary: {use: "+replacementResourceProvider+", config: {value: PRIVATE_NEW}}}, bind: {instances: {database.primary: {Old: {$remove: true}, Keep: database.retained}}}}\n"))
	if !reflect.DeepEqual(selectionYAML(t, updated), want) {
		t.Fatal("compound edit widened exact Config or binding cleanup")
	}
}

func TestSelectionConfigurationRejectsInvalidResourceChanges(t *testing.T) {
	t.Parallel()
	provider := mustImplementationChoiceConstructor(t, resourceProvider)
	valid := applicationmeta.ResourceSelectionEdit{Instance: "database.primary", Provider: provider}
	for _, resources := range [][]applicationmeta.ResourceSelectionEdit{
		{valid, valid},
		{{Instance: "INVALID", Provider: provider}},
		{{Instance: "database.primary"}},
		{{Instance: "database.primary", Provider: provider, Remove: true}},
		{{Instance: "database.primary", Remove: true, DiscardConfiguration: true}},
	} {
		manifest, _ := applicationmeta.ParseCompleteSource("plystra.yaml", []byte("{}"))
		if _, err := applicationmeta.SelectionChangesIdentity(manifest, nil, nil, resources, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) {
			t.Fatal("invalid resource batch identity accepted")
		}
		if data, changed, err := applicationmeta.EditSelectionConfiguration([]byte("{}"), applicationmeta.SelectionConfigurationEdit{Resources: resources}, false); !errors.Is(err, applicationmeta.ErrEditConfiguration) || changed || data != nil {
			t.Fatal("invalid resource batch edit accepted")
		}
	}
}

func TestComposeSelectionIdentitiesPreservesUnboundRepairInput(t *testing.T) {
	t.Parallel()
	manifest, err := applicationmeta.ParseCompleteSource("plystra.yaml", []byte("resources: {instances: {database.primary: {config: {unknown: PRIVATE_VALUE}}}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	if _, err := applicationmeta.Compose(nil, manifest, schemas); err == nil {
		t.Fatal("ordinary composition accepted an unbound instance")
	}
	identities, err := applicationmeta.ComposeSelectionIdentities(manifest, schemas)
	if err != nil || !identities.Valid() || len(identities.Manifest().ResourceInstances()) != 1 || identities.Manifest().ResourceInstances()[0].Provider().String() != "" || identities.Manifest().ResourceInstances()[0].HasConfiguration() {
		t.Fatalf("identity projection lost the repairable unbound instance: %v", err)
	}
	if !manifest.ResourceInstances()[0].HasConfiguration() {
		t.Fatal("identity composition mutated captured Config")
	}
	if _, err := applicationmeta.Compose(nil, manifest, schemas); err == nil {
		t.Fatal("identity composition relaxed final typed composition")
	}
}
