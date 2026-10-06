package applicationmeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestDataMembersParseSortedWithExactSources(t *testing.T) {
	t.Parallel()
	manifest, err := applicationmeta.ParseSource("deploy/project.yaml", []byte(`data:
  members:
    authz.persistence/v1: {resource: database.primary}
    authn.persistence/v1: {resource: database.primary, access: authn.persistence}
`))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/project")
	if err != nil {
		t.Fatal(err)
	}
	members := manifest.DataMembers()
	if len(members) != 2 || members[0].ID() != "authn.persistence/v1" || members[0].Resource() != "database.primary" || members[0].Access() != "authn.persistence" || members[1].ID() != "authz.persistence/v1" || members[1].Access() != "" {
		t.Fatalf("members = %#v", members)
	}
	if members[0].Source() != `deploy/project.yaml data.members["authn.persistence/v1"]` || members[0].DeclarationSource().Path() != "deploy/project.yaml" || members[0].DeclarationSource().ModulePath() != "example.com/project" || members[0].DeclarationSource().Line() != 4 || members[0].DeclarationSource().Column() != 5 {
		t.Fatalf("source = %s, %#v", members[0].Source(), members[0].DeclarationSource())
	}
	members[0] = applicationmeta.DataMember{}
	if manifest.DataMembers()[0].ID() != "authn.persistence/v1" {
		t.Fatal("DataMembers returned manifest storage")
	}
}

func TestDataMembersRejectMalformedClosedConfiguration(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, document, field string
	}{
		{"invalid ID", `data: {members: {authn: {resource: database.primary}}}`, "data.members"},
		{"duplicate member", `data: {members: {authn.persistence/v1: {resource: database.primary}, authn.persistence/v1: {resource: database.other}}}`, "data.members"},
		{"duplicate field", `data: {members: {authn.persistence/v1: {resource: database.primary, resource: database.other}}}`, `data.members["authn.persistence/v1"]`},
		{"unknown data field", `data: {other: true}`, "data"},
		{"unknown entry field", `data: {members: {authn.persistence/v1: {resource: database.primary, other: true}}}`, `data.members["authn.persistence/v1"]`},
		{"missing resource", `data: {members: {authn.persistence/v1: {access: authn.persistence}}}`, `data.members["authn.persistence/v1"].resource`},
		{"null resource", `data: {members: {authn.persistence/v1: {resource: null}}}`, `data.members["authn.persistence/v1"].resource`},
		{"invalid resource", `data: {members: {authn.persistence/v1: {resource: Database.Primary}}}`, `data.members["authn.persistence/v1"].resource`},
		{"invalid access", `data: {members: {authn.persistence/v1: {resource: database.primary, access: 12}}}`, `data.members["authn.persistence/v1"].access`},
		{"null members", `data: {members: null}`, "data.members"},
		{"false removal", `data: {members: {authn.persistence/v1: {$remove: false}}}`, `data.members["authn.persistence/v1"]`},
		{"removal siblings", `data: {members: {authn.persistence/v1: {$remove: true, resource: database.primary}}}`, `data.members["authn.persistence/v1"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := applicationmeta.ParseSource("deploy/project.yaml", []byte(test.document))
			var detail *applicationmeta.DataMemberMetadataError
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !errors.Is(err, applicationmeta.ErrInvalidDataMember) || !errors.As(err, &detail) || detail.Field() != test.field || detail.Source().Path() != "deploy/project.yaml" {
				t.Fatalf("ParseSource = %v, want Data error at %s", err, test.field)
			}
		})
	}
	for _, source := range []string{"plystra.yaml", "deploy/replacement.yaml"} {
		_, err := applicationmeta.ParseCompleteSource(source, []byte(`data: {members: {authn.persistence/v1: {$remove: true}}}`))
		var detail *applicationmeta.DataMemberMetadataError
		if !errors.Is(err, applicationmeta.ErrInvalidDataMember) || !errors.As(err, &detail) || detail.Source().Path() != source || detail.Field() != `data.members["authn.persistence/v1"]` || !strings.Contains(err.Error(), "removal markers") {
			t.Fatalf("complete document %s accepted removal: %v", source, err)
		}
	}
}

func TestDataMemberIDUsesUnboundedInterfaceGrammar(t *testing.T) {
	t.Parallel()
	id := strings.Repeat("a", 124) + ".b/v1"
	manifest, err := applicationmeta.ParseSource("plystra.yaml", []byte("data: {members: {"+id+": {resource: database.primary}}}"))
	if err != nil || len(manifest.DataMembers()) != 1 || manifest.DataMembers()[0].ID() != id {
		t.Fatalf("member ID = %#v, %v", manifest.DataMembers(), err)
	}
}

func TestDataMembersComposeWholeEntriesAndRetainRemovalEvidence(t *testing.T) {
	t.Parallel()
	root := resourceManifest(t, "plystra.yaml", `data:
  members:
    authn.persistence/v1: {resource: database.primary, access: authn.persistence}
    authz.persistence/v1: {resource: database.primary}
`)
	lookup := composeSchemaLookup(nil)
	replacement := resourceOverlayManifest(t, "plystra.production.yaml", `data:
  members:
    authn.persistence/v1: {resource: database.replica}
    authz.persistence/v1: {$remove: true}
`)
	selected, err := applicationmeta.ApplyOverlay(root, replacement, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose(nil, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	members := composition.Manifest().DataMembers()
	if len(members) != 1 || members[0].ID() != "authn.persistence/v1" || members[0].Resource() != "database.replica" || members[0].Access() != "" || members[0].Source() != `plystra.production.yaml data.members["authn.persistence/v1"]` {
		t.Fatalf("effective members = %#v", members)
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composition.Manifest(), lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]applicationmeta.ConfigurationDecisionSummary{
		`data.members["authn.persistence/v1"]`: applicationmeta.ConfigurationSummaryObject,
		`data.members["authz.persistence/v1"]`: applicationmeta.ConfigurationSummaryRemoval,
	}
	for _, decision := range decisions {
		if summary, ok := want[decision.Path()]; ok {
			if decision.Summary() != summary || decision.Removed() != (summary == applicationmeta.ConfigurationSummaryRemoval) || decision.Source() != `plystra.production.yaml `+decision.Path() {
				t.Fatalf("decision = %#v", decision)
			}
			delete(want, decision.Path())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing decisions: %v", want)
	}
	for _, source := range composition.ResolutionSources() {
		if strings.HasPrefix(source.Path(), "data.members") && !reflect.DeepEqual(source.Sources(), []string{`plystra.production.yaml ` + source.Path()}) {
			t.Fatalf("resolution source = %#v", source)
		}
	}
	if composition.CompositionDigest() == "" {
		t.Fatal("missing composition digest")
	}
	unmodified, err := applicationmeta.Compose(nil, root, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if unmodified.CompositionDigest() == composition.CompositionDigest() {
		t.Fatal("member replacement/removal did not change composition digest")
	}
	layerDigest, err := applicationmeta.ConfigurationLayerDigest(replacement, lookup)
	if err != nil || layerDigest == "" {
		t.Fatalf("ConfigurationLayerDigest = %q, %v", layerDigest, err)
	}
}

func TestDataMemberLayerDigestTracksAssignmentNotYAMLLayout(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(nil)
	var digests []string
	for _, source := range []string{
		`data: {members: {authn.persistence/v1: {resource: database.primary, access: authn.persistence}}}`,
		"data:\n  members:\n    authn.persistence/v1:\n      access: authn.persistence\n      resource: database.primary\n",
		`data: {members: {authn.persistence/v1: {resource: database.replica, access: authn.persistence}}}`,
	} {
		manifest, err := applicationmeta.ParseSource("plystra.yaml", []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if digests[0] != digests[1] || digests[0] == digests[2] {
		t.Fatalf("layer digests = %v", digests)
	}
}
