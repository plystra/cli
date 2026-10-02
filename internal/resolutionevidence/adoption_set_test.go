package resolutionevidence_test

import (
	"bytes"
	"slices"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestBuildRetainsAdoptionSetBoundaryAndMemberOwnership(t *testing.T) {
	t.Parallel()
	const adoption = "{module: example.com/app, export: defaults}"
	for _, test := range []struct {
		name, root, overlay string
		owner               resolutionevidence.ConfigurationOwner
		effective, removed  bool
	}{
		{"complete empty", "[" + adoption + "]", "[]", resolutionevidence.ConfigurationOwnerEnvironment, false, false},
		{"complete same member", "[" + adoption + "]", "[" + adoption + "]", resolutionevidence.ConfigurationOwnerEnvironment, true, false},
		{"sparse empty", "[" + adoption + "]", "{}", resolutionevidence.ConfigurationOwnerRoot, true, false},
		{"sparse over complete", "[]", "{add: [" + adoption + "]}", resolutionevidence.ConfigurationOwnerRoot, true, false},
		{"sparse removal", "[" + adoption + "]", "{remove: [" + adoption + "]}", resolutionevidence.ConfigurationOwnerRoot, true, true},
		{"complete clears removal", "{remove: [" + adoption + "]}", "[]", resolutionevidence.ConfigurationOwnerEnvironment, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := configurationSchemaLookup(t)
			root := configurationManifest(t, "plystra.yaml", "composition: {adopt: "+test.root+"}")
			overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("composition: {adopt: "+test.overlay+"}"))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
			if err != nil {
				t.Fatal(err)
			}
			composition, err := applicationmeta.Compose(nil, selected, lookup)
			if err != nil {
				t.Fatal(err)
			}
			input := configurationEvidenceInput(t, generation.ConfigurationModeEnvironment, "production", "plystra.production.yaml", composition, []resolutionevidence.ConfigurationLayerInput{
				{Owner: resolutionevidence.ConfigurationOwnerRoot, Decisions: configurationDecisions(t, root, lookup)},
				{Owner: resolutionevidence.ConfigurationOwnerEnvironment, Decisions: configurationDecisions(t, overlay, lookup)},
			}, []resolutionevidence.ModuleInput{{Path: "example.com/app", Role: resolutionevidence.ModuleRoleCurrent, SourceModulePath: "example.com/app"}})
			evidence, err := resolutionevidence.Build(input)
			if err != nil || !evidence.Valid() {
				t.Fatalf("Build = %#v, %v", evidence, err)
			}
			boundary := configurationField(t, evidence, "composition.adopt")
			if !boundary.Effective() || boundary.Owner() != test.owner || boundary.Summary() != "complete-set" {
				t.Fatalf("complete adoption boundary = %#v", boundary)
			}
			member := configurationField(t, evidence, `composition.adopt["example.com/app#defaults"]`)
			if member.Effective() != test.effective || member.Removed() != test.removed {
				t.Fatalf("adoption member = %#v", member)
			}
			if test.effective {
				want := resolutionevidence.ConfigurationOwnerEnvironment
				if test.overlay == "{}" {
					want = resolutionevidence.ConfigurationOwnerRoot
				}
				if member.Owner() != want {
					t.Fatalf("member owner = %s, want %s", member.Owner(), want)
				}
			}
			for _, contribution := range member.Contributors() {
				if !test.effective && contribution.Effective() {
					t.Fatal("suppressed member retains effective contribution")
				}
				for _, source := range contribution.Sources() {
					want := "plystra.yaml"
					if contribution.Owner() == resolutionevidence.ConfigurationOwnerEnvironment {
						want = "plystra.production.yaml"
					}
					if source.Path() != want || source.Module() != "example.com/app" {
						t.Fatalf("member source = %#v", source)
					}
				}
			}
			for _, layer := range input.Configuration.Layers {
				slices.Reverse(layer.Decisions)
			}
			permuted, err := resolutionevidence.Build(input)
			if err != nil || !bytes.Equal(evidence.CanonicalJSON(), permuted.CanonicalJSON()) {
				t.Fatalf("permuted adoption evidence changed: %v", err)
			}
		})
	}
}
