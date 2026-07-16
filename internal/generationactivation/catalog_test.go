package generationactivation_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestCatalogAssociatesOneCapabilityWithSeveralProviderExtensions(t *testing.T) {
	t.Parallel()
	inputs := []generationactivation.Declaration{
		declaration(t, "example.authn-passkey", "authn.session.verify/v1", "authn", "passkey/plugin.yaml"),
		declaration(t, "example.audit", "audit.write/v1", "audit", "audit/plugin.yaml"),
		declaration(t, "example.authn-password", "authn.session.verify/v1", "authn", "password/plugin.yaml"),
	}
	catalog, err := generationactivation.New(inputs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := associationStrings(catalog.Associations()); !slices.Equal(got, []string{
		"audit|audit.write/v1|example.audit",
		"authn|authn.session.verify/v1|example.authn-passkey,example.authn-password",
	}) {
		t.Fatalf("Associations = %v", got)
	}
	authn, exists := catalog.Association("authn")
	if !exists || authn.Capability().String() != "authn.session.verify/v1" {
		t.Fatalf("Association(authn) = %#v, %t", authn, exists)
	}
	extensions := authn.Extensions()
	extensions[0] = generationactivation.Extension{}
	if authn.Extensions()[0].PluginID() != "example.authn-passkey" {
		t.Fatal("Association exposed mutable extension storage")
	}
	associations := catalog.Associations()
	associations[0] = generationactivation.Association{}
	if catalog.Associations()[0].Namespace() != "audit" {
		t.Fatal("Catalog exposed mutable association storage")
	}

	selected, err := catalog.Select("authn", "example.authn-password")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if selected.PluginID() != "example.authn-password" || selected.API() != "v1" || selected.Package() != "./generation" || selected.Source() != "password/plugin.yaml" {
		t.Fatalf("selected Extension = %#v", selected)
	}
	if _, err := catalog.Select("authn", "example.unselected"); !errors.Is(err, generationactivation.ErrSelectedProviderExtension) || !strings.Contains(err.Error(), "example.authn-passkey") || !strings.Contains(err.Error(), "example.authn-password") {
		t.Fatalf("Select(unselected) error = %v", err)
	}
	if _, err := catalog.Select("missing", "example.authn-password"); !errors.Is(err, generationactivation.ErrMissingAssociation) {
		t.Fatalf("Select(missing) error = %v", err)
	}
}

func TestCatalogRejectsConflictingNamespaceAssociationsDeterministically(t *testing.T) {
	t.Parallel()
	password := declaration(t, "example.authn-password", "authn.session.verify/v1", "authn", "password/plugin.yaml")
	legacy := declaration(t, "example.authn-legacy", "authn.token.verify/v1", "authn", "legacy/plugin.yaml")
	for _, inputs := range [][]generationactivation.Declaration{{password, legacy}, {legacy, password}} {
		catalog, err := generationactivation.New(inputs)
		if !errors.Is(err, generationactivation.ErrCatalog) || !errors.Is(err, generationactivation.ErrAssociationConflict) {
			t.Fatalf("New error = %v", err)
		}
		if len(catalog.Associations()) != 0 {
			t.Fatalf("conflicting New returned %#v", catalog.Associations())
		}
		var conflict *generationactivation.AssociationConflictError
		if !errors.As(err, &conflict) || conflict.Namespace() != "authn" {
			t.Fatalf("New conflict = %T %#v", err, conflict)
		}
		candidates := conflict.Candidates()
		if len(candidates) != 2 || candidates[0].PluginID() != "example.authn-legacy" || candidates[0].Capability().String() != "authn.token.verify/v1" || candidates[1].PluginID() != "example.authn-password" || candidates[1].Capability().String() != "authn.session.verify/v1" {
			t.Fatalf("conflict Candidates = %#v", candidates)
		}
		message := err.Error()
		for _, detail := range []string{"authn", "example.authn-legacy", "legacy/plugin.yaml", "authn.token.verify/v1", "example.authn-password", "password/plugin.yaml", "authn.session.verify/v1", "./generation", "correction:"} {
			if !strings.Contains(message, detail) {
				t.Fatalf("conflict error omits %q: %v", detail, err)
			}
		}
	}
}

func TestCatalogIsStableAcrossDeclarationOrder(t *testing.T) {
	t.Parallel()
	inputs := []generationactivation.Declaration{
		declaration(t, "example.authz", "authz.check/v1", "authz", "authz/plugin.yaml"),
		declaration(t, "example.authn-password", "authn.session.verify/v1", "authn", "password/plugin.yaml"),
		declaration(t, "example.authn-passkey", "authn.session.verify/v1", "authn", "passkey/plugin.yaml"),
	}
	first, err := generationactivation.New(inputs)
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	slices.Reverse(inputs)
	second, err := generationactivation.New(inputs)
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	if got, want := associationStrings(first.Associations()), associationStrings(second.Associations()); !slices.Equal(got, want) {
		t.Fatalf("order-dependent catalogs: %v != %v", got, want)
	}
}

func TestCatalogSupportsNoGenerationExtensions(t *testing.T) {
	t.Parallel()
	catalog, err := generationactivation.New(nil)
	if err != nil || len(catalog.Associations()) != 0 {
		t.Fatalf("New(nil) = %#v, %v", catalog.Associations(), err)
	}
	if _, exists := catalog.Association("authn"); exists {
		t.Fatal("empty catalog contains authn")
	}
}

func TestCatalogRejectsInvalidOrDuplicatePluginDeclarations(t *testing.T) {
	t.Parallel()
	valid := declaration(t, "example.authn", "authn.session.verify/v1", "authn", "authn/plugin.yaml")
	tests := map[string][]generationactivation.Declaration{
		"invalid plugin":       {{PluginID: "Example", Source: valid.Source, Generation: valid.Generation}},
		"missing source":       {{PluginID: valid.PluginID, Generation: valid.Generation}},
		"multiline source":     {{PluginID: valid.PluginID, Source: "plugin.yaml\nforged", Generation: valid.Generation}},
		"invalid UTF-8":        {{PluginID: valid.PluginID, Source: string([]byte{0xff}), Generation: valid.Generation}},
		"empty generation":     {{PluginID: valid.PluginID, Source: valid.Source}},
		"intrinsic activation": {declaration(t, "example.kernel", "kernel.health/v1", "health", "kernel/plugin.yaml")},
		"duplicate plugin":     {valid, {PluginID: valid.PluginID, Source: "duplicate/plugin.yaml", Generation: valid.Generation}},
	}
	for name, inputs := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			catalog, err := generationactivation.New(inputs)
			if !errors.Is(err, generationactivation.ErrCatalog) || !errors.Is(err, generationactivation.ErrInvalidDeclaration) || len(catalog.Associations()) != 0 {
				t.Fatalf("New = %#v, %v", catalog.Associations(), err)
			}
		})
	}
}

func declaration(t *testing.T, pluginID, capability, namespace, source string) generationactivation.Declaration {
	t.Helper()
	data := []byte("id: " + pluginID + "\nprovides: [" + capability + "]\ngeneration:\n  api: v1\n  package: ./generation\n  activations:\n    - namespace: " + namespace + "\n      capability: " + capability + "\n")
	manifest, err := pluginmeta.Parse(data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", pluginID, err)
	}
	generation, exists := manifest.Generation()
	if !exists {
		t.Fatalf("Parse(%s) returned no generation declaration", pluginID)
	}
	return generationactivation.Declaration{PluginID: pluginID, Source: source, Generation: generation}
}

func associationStrings(associations []generationactivation.Association) []string {
	result := make([]string, len(associations))
	for index, association := range associations {
		plugins := make([]string, len(association.Extensions()))
		for extensionIndex, extension := range association.Extensions() {
			plugins[extensionIndex] = extension.PluginID()
		}
		result[index] = association.Namespace() + "|" + association.Capability().String() + "|" + strings.Join(plugins, ",")
	}
	return result
}
