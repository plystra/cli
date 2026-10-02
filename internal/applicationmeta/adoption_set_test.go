package applicationmeta_test

import (
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestAdoptionSetModeChangesLayerIdentity(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{
		{"{}", "composition: {adopt: []}"},
		{"composition: {adopt: {}}", "composition: {adopt: []}"},
		{"composition: {adopt: {add: [{module: example.com/a, export: defaults}]}}", "composition: {adopt: [{module: example.com/a, export: defaults}]}"},
	} {
		left, err := applicationmeta.ConfigurationLayerDigest(composeManifest(t, pair[0]), composeSchemaLookup(nil))
		if err != nil {
			t.Fatal(err)
		}
		right, err := applicationmeta.ConfigurationLayerDigest(composeManifest(t, pair[1]), composeSchemaLookup(nil))
		if err != nil {
			t.Fatal(err)
		}
		if left == right {
			t.Fatalf("distinct adoption semantics have equal identity: %s and %s", pair[0], pair[1])
		}
	}
}

func TestOverlayPreservesAuthoredAdoptionBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		root, overlay string
		complete      bool
	}{
		{"{}", "{}", false},
		{"composition: {adopt: {}}", "{}", false},
		{"{}", "composition: {adopt: {}}", false},
		{"composition: {adopt: {add: [{module: example.com/a, export: defaults}]}}", "{}", false},
		{"composition: {adopt: {}}", "composition: {adopt: {remove: [{module: example.com/a, export: defaults}]}}", false},
		{"composition: {adopt: []}", "{}", true},
		{"{}", "composition: {adopt: []}", true},
		{"composition: {adopt: []}", "composition: {adopt: {add: [{module: example.com/a, export: defaults}]}}", true},
	} {
		base := composeManifest(t, test.root)
		overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(test.overlay))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := applicationmeta.ApplyOverlay(base, overlay, composeSchemaLookup(nil))
		if err != nil {
			t.Fatal(err)
		}
		decisions, err := applicationmeta.ConfigurationDecisions(selected, composeSchemaLookup(nil))
		if err != nil {
			t.Fatal(err)
		}
		complete := false
		for _, decision := range decisions {
			if decision.Path() == "composition.adopt" && decision.Summary() == applicationmeta.ConfigurationSummaryCompleteSet {
				complete = true
			}
		}
		if complete != test.complete {
			t.Fatalf("complete boundary for %s plus %s = %t, want %t", test.root, test.overlay, complete, test.complete)
		}
	}
}

func TestAdoptionSetIdentityIsOrderIndependent(t *testing.T) {
	t.Parallel()
	left := composeManifest(t, "composition: {adopt: [{module: example.com/a, export: defaults}, {module: example.com/b, export: defaults}]}")
	right := composeManifest(t, "composition: {adopt: [{module: example.com/b, export: defaults}, {module: example.com/a, export: defaults}]}")
	first, err := applicationmeta.ConfigurationLayerDigest(left, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := applicationmeta.ConfigurationLayerDigest(right, composeSchemaLookup(nil))
	if err != nil || first != second {
		t.Fatalf("adoption permutation changed identity: %s, %s, %v", first, second, err)
	}
}
