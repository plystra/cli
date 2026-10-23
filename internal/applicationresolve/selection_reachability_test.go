package applicationresolve_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
)

func TestSelectionInputsReachabilityUsesCapturedInterfaceClosure(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	writeResourceProvider(t, root)
	document := "interfaces: {require: [app.entry/v1]}\nresources: {instances: {database.primary: {config: {value: PRIVATE_UNBOUND}}}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	before := snapshotTree(t, root)
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	composition, err := inputs.ComposeSelectionCandidate([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com/resource-consumer/consumer.New", "example.com/resource-consumer/entry.New"}
	got, err := inputs.CandidateReachableConstructors(composition)
	if err != nil || !reflect.DeepEqual(selectionReachableNames(got), want) {
		t.Fatalf("unbound Resource blocked complete Interface closure: %v, %v", got, err)
	}
	if _, err := inputs.ComposeCandidate([]byte(document)); err == nil {
		t.Fatal("identity-only reachability relaxed typed Resource composition")
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("reachability modified Project state")
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "[PRIVATE_LATER_EDIT]\n")
	got[0] = constructorsymbol.Symbol{}
	got, err = inputs.CandidateReachableConstructors(composition)
	if err != nil || !reflect.DeepEqual(selectionReachableNames(got), want) {
		t.Fatalf("reachability reread changed files or aliased caller storage: %v, %v", got, err)
	}
	if err := inputs.ValidateSnapshot(t.Context()); err == nil {
		t.Fatal("captured reachability hid later snapshot drift")
	}
	dormant, err := inputs.ComposeSelectionCandidate([]byte("interfaces: {use: {app.entry/v1: example.com/resource-consumer/entry.New}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := inputs.CandidateReachableConstructors(dormant); err != nil || len(got) != 0 {
		t.Fatalf("dormant choice became reachable: %v, %v", got, err)
	}
	if owners, err := inputs.CandidateOwners(dormant); err != nil || len(owners) != 1 {
		t.Fatalf("dormant ownership was lost: %v, %v", owners, err)
	}
	if _, err := inputs.CandidateReachableConstructors(applicationmeta.Composition{}); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("empty composition accepted: %v", err)
	}
	if _, err := (applicationresolve.SelectionInputs{}).CandidateReachableConstructors(composition); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("empty captured inputs accepted: %v", err)
	}
}

func TestSelectionInputsReachabilityDoesNotPublishPartialClosures(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	if err := os.Remove(filepath.Join(root, "consumer/service.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.entry/v1]}\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	composition, err := inputs.ComposeSelectionCandidate(inputs.SelectedSnapshot().Data())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := inputs.CandidateReachableConstructors(composition); !errors.Is(err, constructorgraph.ErrMissingBinding) || got != nil {
		t.Fatalf("partial positive owners became complete reachability: %v, %v", got, err)
	}
	if err := inputs.ValidateCandidate(composition); !errors.Is(err, constructorgraph.ErrMissingBinding) {
		t.Fatalf("final validation lost missing Interface failure: %v", err)
	}
}

func selectionReachableNames(symbols []constructorsymbol.Symbol) []string {
	names := make([]string, len(symbols))
	for index, symbol := range symbols {
		names[index] = symbol.String()
	}
	return names
}
