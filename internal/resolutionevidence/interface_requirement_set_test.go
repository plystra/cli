package resolutionevidence_test

import (
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestConfigurationEvidenceRetainsSuppressedRequirementSources(t *testing.T) {
	t.Parallel()
	lookup := configurationSchemaLookup(t)
	dependencies := []applicationmeta.Dependency{{
		ModulePath: "example.com/platform", ModuleVersion: "v1.0.0", ExportName: "defaults",
		Manifest: configurationManifest(t, "plystra.yaml", "interfaces: {require: [audit.write/v1, email.send/v1]}"),
	}}
	for _, overlayData := range []string{"interfaces: {require: []}", "interfaces: {require: [email.send/v1]}"} {
		root := configurationManifest(t, "plystra.yaml", "interfaces: {require: {add: [cache.read/v1]}}")
		overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(overlayData))
		if err != nil {
			t.Fatal(err)
		}
		current, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose(dependencies, current, lookup)
		if err != nil {
			t.Fatal(err)
		}
		input := configurationEvidenceInput(t, generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml", composition,
			[]resolutionevidence.ConfigurationLayerInput{
				{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)},
				{Owner: resolutionevidence.ConfigurationOwnerEnvironment, Decisions: configurationDecisions(t, overlay, lookup)},
			}, []resolutionevidence.ModuleInput{
				{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"},
				{Path: "example.com/platform", Role: resolutionevidence.ModuleRoleDependency, SelectedVersion: "v1.0.0", SourceModulePath: "example.com/platform"},
			})
		evidence, err := resolutionevidence.Build(input)
		if err != nil || !evidence.Valid() {
			t.Fatalf("Build complete-set evidence: %v", err)
		}
		boundary := configurationField(t, evidence, "interfaces.require")
		if !boundary.Effective() || boundary.Summary() != "complete-set" || boundary.Owner() != resolutionevidence.ConfigurationOwnerEnvironment {
			t.Fatalf("replacement boundary = %#v", boundary)
		}
		for _, path := range []string{`interfaces.require["audit.write/v1"]`, `interfaces.require["cache.read/v1"]`} {
			field := configurationField(t, evidence, path)
			if field.Effective() || len(field.Contributors()) != 1 || field.Contributors()[0].Effective() || len(field.Contributors()[0].Sources()) != 1 {
				t.Fatalf("suppressed requirement lost source or remained effective: %#v", field)
			}
		}
		email := configurationField(t, evidence, `interfaces.require["email.send/v1"]`)
		if email.Effective() != (len(current.InterfaceRequirements()) != 0) {
			t.Fatalf("replacement member effectiveness = %#v", email)
		}
	}
}
