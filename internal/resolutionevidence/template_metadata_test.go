package resolutionevidence_test

import (
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestTemplateMetadataRemainsRootOwnedWithReplacementSelection(t *testing.T) {
	t.Parallel()
	lookup := configurationSchemaLookup(t)
	root, err := applicationmeta.ParseRootMetadataSource("plystra.yaml", []byte("template: example.com/base\nhttp: excluded-application-value\n"))
	if err != nil {
		t.Fatal(err)
	}
	replacement := configurationManifest(t, "deploy/customer.yaml", "http: {address: ':8080'}")
	selected := applicationmeta.WithRootMetadata(replacement, root)
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath: "example.com/base", ModuleVersion: "v1.0.0", Manifest: configurationManifest(t, "plystra.yaml", "{}"),
	}}, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	input := configurationEvidenceInput(t, generation.ConfigurationModeExplicit, "", "deploy/customer.yaml", composition, []resolutionevidence.ConfigurationLayerInput{
		{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)},
		{Owner: resolutionevidence.ConfigurationOwnerExplicit, Decisions: configurationDecisions(t, replacement, lookup)},
	}, []resolutionevidence.ModuleInput{
		{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
		{Path: "example.com/base", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/base"},
	})
	evidence, err := resolutionevidence.Build(input)
	if err != nil || !evidence.Valid() {
		t.Fatalf("Build: %v", err)
	}
	field := configurationField(t, evidence, "template")
	if !field.Effective() || field.Owner() != resolutionevidence.ConfigurationOwnerRoot || len(field.Contributors()) != 1 || field.Contributors()[0].Sources()[0].Path() != "plystra.yaml" {
		t.Fatal("replacement selector changed root relationship ownership")
	}
	input.Configuration.Layers[0].Decisions = configurationDecisions(t, configurationManifest(t, "plystra.yaml", "http: {address: ':9999'}"), lookup)
	if _, err := resolutionevidence.Build(input); err == nil {
		t.Fatal("replacement evidence accepted excluded root application values")
	}
}

func TestTemplateHistoryRetainsRemovalBarriersAfterObjectRevival(t *testing.T) {
	t.Parallel()
	lookup := configurationSchemaLookup(t)
	for _, wholeConstructor := range []bool{false, true} {
		root := configurationManifest(t, "plystra.yaml", "{}")
		oldest := configurationManifest(t, "plystra.yaml", "config: {example.com/acme/smtp.New: {settings: {nested: {dependency: old}}}}")
		removal := "config: {example.com/acme/smtp.New: {settings: {$remove: true}}}"
		if wholeConstructor {
			removal = "config: {example.com/acme/smtp.New: {$remove: true}}"
		}
		nearest := configurationManifest(t, "plystra.yaml", removal)
		overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("config: {example.com/acme/smtp.New: {settings: {nested: {root: current}}}}"))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose([]applicationmeta.Dependency{
			{ModulePath: "example.com/oldest", ModuleVersion: "v1.0.0", Manifest: oldest},
			{ModulePath: "example.com/nearest", ModuleVersion: "v1.0.0", Manifest: nearest},
		}, selected, lookup)
		if err != nil {
			t.Fatal(err)
		}
		input := configurationEvidenceInput(t, generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml", composition, []resolutionevidence.ConfigurationLayerInput{
			{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)},
			{Owner: resolutionevidence.ConfigurationOwnerEnvironment, Decisions: configurationDecisions(t, overlay, lookup)},
		}, []resolutionevidence.ModuleInput{
			{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
			{Path: "example.com/oldest", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/oldest"},
			{Path: "example.com/nearest", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/nearest"},
		})
		evidence, err := resolutionevidence.Build(input)
		if err != nil || !evidence.Valid() {
			t.Fatalf("revived object history: %v", err)
		}
		removed := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["settings"]["nested"]["dependency"]`)
		if removed.Effective() || removed.Contributors()[0].Effective() || removed.Contributors()[0].TemplateOrder() != 1 {
			t.Fatal("revived object resurrected an excluded old child")
		}
		current := configurationField(t, evidence, `config["example.com/acme/smtp.New"]["settings"]["nested"]["root"]`)
		if !current.Effective() || current.Owner() != resolutionevidence.ConfigurationOwnerEnvironment {
			t.Fatal("revived object lost its selected child")
		}
	}
}
