package applicationmeta_test

import (
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestInterfaceRequirementCompleteSetsReplaceLowerLayers(t *testing.T) {
	t.Parallel()
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ExportName: "defaults", Manifest: composeManifest(t, "interfaces: {require: [audit.write/v1, email.send/v1]}\n")},
		{ModulePath: "example.com/b", ExportName: "defaults", Manifest: composeManifest(t, "interfaces: {require: [email.send/v1]}\n")},
	}
	for _, test := range []struct {
		name, root, overlay string
		want                []string
	}{
		{"omitted", "{}", "", []string{"audit.write/v1", "email.send/v1"}},
		{"sparse empty", "interfaces: {require: {}}", "", []string{"audit.write/v1", "email.send/v1"}},
		{"complete empty", "interfaces: {require: []}", "", []string{}},
		{"complete subset", "interfaces: {require: [email.send/v1]}", "", []string{"email.send/v1"}},
		{"sparse removal", "interfaces: {require: {remove: [email.send/v1]}}", "", []string{"audit.write/v1"}},
		{"overlay complete empty", "interfaces: {require: [cache.read/v1]}", "interfaces: {require: []}", []string{}},
		{"overlay complete replacement", "interfaces: {require: [cache.read/v1]}", "interfaces: {require: [email.send/v1]}", []string{"email.send/v1"}},
		{"overlay sparse over complete", "interfaces: {require: [cache.read/v1]}", "interfaces: {require: {add: [reports.read/v1], remove: [cache.read/v1]}}", []string{"reports.read/v1"}},
		{"overlay sparse over empty complete", "interfaces: {require: []}", "interfaces: {require: {add: [reports.read/v1]}}", []string{"reports.read/v1"}},
		{"overlay omitted", "interfaces: {require: []}", "{}", []string{}},
		{"overlay complete drops lower removals", "interfaces: {require: {remove: [email.send/v1]}}", "interfaces: {require: [email.send/v1]}", []string{"email.send/v1"}},
		{"overlay sparse over sparse", "interfaces: {require: {remove: [email.send/v1]}}", "interfaces: {require: {add: [cache.read/v1]}}", []string{"audit.write/v1", "cache.read/v1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := composeManifest(t, test.root+"\n")
			if test.overlay != "" {
				overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(test.overlay+"\n"))
				if err != nil {
					t.Fatal(err)
				}
				current, err = applicationmeta.ApplyOverlay(current, overlay, composeSchemaLookup(nil))
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, order := range [][]applicationmeta.Dependency{dependencies, {dependencies[1], dependencies[0]}} {
				composed, err := applicationmeta.Compose(order, current, composeSchemaLookup(nil))
				if err != nil {
					t.Fatal(err)
				}
				if got := interfaceRequirementIDs(composed.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, test.want) {
					t.Fatalf("effective requirements = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestInterfaceRequirementSetModeChangesLayerIdentity(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{
		{"{}", "interfaces: {require: []}"},
		{"interfaces: {require: {}}", "interfaces: {require: []}"},
		{"interfaces: {require: {add: [email.send/v1]}}", "interfaces: {require: [email.send/v1]}"},
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
			t.Fatalf("distinct set semantics have equal identity: %s and %s", pair[0], pair[1])
		}
	}
}

func TestAddExposurePreservesRequirementSetMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"[]", "{}"} {
		original := composeManifest(t, "interfaces: {require: "+mode+"}\n")
		updated, changed, err := applicationmeta.AddHTTPExposure([]byte("interfaces: {require: "+mode+"}\n"), mustExposureID(t, "kernel.health/v1"))
		if err != nil || !changed {
			t.Fatalf("AddHTTPExposure: changed %t, %v", changed, err)
		}
		for _, current := range []applicationmeta.Manifest{original, composeManifest(t, string(updated))} {
			composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{
				ModulePath: "example.com/a", ExportName: "defaults", Manifest: composeManifest(t, "interfaces: {require: [email.send/v1]}\n"),
			}}, current, composeSchemaLookup(nil))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(composition.Manifest().InterfaceRequirements()); (got == 0) != (mode == "[]") {
				t.Fatalf("exposure mutation changed requirement mode %s: %d members", mode, got)
			}
		}
	}
}
