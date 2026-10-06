package applicationmeta_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestInterfaceRequirementCompleteSetsReplaceLowerLayers(t *testing.T) {
	t.Parallel()
	root := composeManifest(t, "interfaces: {require: [audit.write/v1, email.send/v1]}\n")
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("interfaces: {require: {add: [cache.read/v1], remove: [audit.write/v1]}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ApplyOverlay(root, overlay, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	composed, err := applicationmeta.Compose(nil, selected, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := interfaceRequirementIDs(composed.Manifest().InterfaceRequirements()), []string{"cache.read/v1", "email.send/v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("effective requirements = %v, want %v", got, want)
	}

	replacement, err := applicationmeta.ParseCompleteSource("deploy/customer.yaml", []byte("interfaces: {require: [reports.read/v1]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := applicationmeta.Compose(nil, replacement, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := interfaceRequirementIDs(replaced.Manifest().InterfaceRequirements()), []string{"reports.read/v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replacement requirements = %v, want %v", got, want)
	}
}

func TestCompleteRequirementDocumentsRejectSparseForms(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"interfaces: {require: {}}\n",
		"interfaces: {require: {add: [email.send/v1]}}\n",
		"interfaces: {require: {remove: [email.send/v1]}}\n",
	} {
		if _, err := applicationmeta.ParseCompleteSource("plystra.yaml", []byte(data)); err == nil || !strings.Contains(err.Error(), "complete sequence") {
			t.Fatalf("ParseCompleteSource(%q) = %v", data, err)
		}
	}
}

func TestInterfaceRequirementSetModeChangesLayerIdentity(t *testing.T) {
	t.Parallel()
	root, err := applicationmeta.ParseCompleteSource("plystra.yaml", []byte("interfaces: {require: []}\n"))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("interfaces: {require: {add: [email.send/v1]}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ApplyOverlay(root, overlay, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	rootDigest, err := applicationmeta.ConfigurationLayerDigest(root, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	overlayDigest, err := applicationmeta.ConfigurationLayerDigest(overlay, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rootDigest == overlayDigest || len(selected.InterfaceRequirements()) != 1 {
		t.Fatalf("root and overlay set identities or selected requirements are incorrect: %q, %q, %#v", rootDigest, overlayDigest, selected.InterfaceRequirements())
	}
}

func TestAddExposurePreservesCompleteRequirementSet(t *testing.T) {
	t.Parallel()
	original := "interfaces: {require: [email.send/v1]}\n"
	updated, changed, err := applicationmeta.AddHTTPExposure([]byte(original), mustExposureID(t, "kernel.health/v1"))
	if err != nil || !changed {
		t.Fatalf("AddHTTPExposure: changed %t, %v", changed, err)
	}
	manifest, err := applicationmeta.ParseCompleteSource("plystra.yaml", updated)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose(nil, manifest, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := interfaceRequirementIDs(composition.Manifest().InterfaceRequirements()), []string{"email.send/v1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exposure mutation changed requirement set: %v, want %v", got, want)
	}
}
