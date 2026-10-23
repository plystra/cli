package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestSelectionInputsProjectInterfaceRootUsesCapturedIdentities(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "direct")
			writeResourceProvider(t, root)
			options, path := selectionOptions(root), "plystra.yaml"
			if mode == "environment" {
				options.EnvironmentName, path = "test", "plystra.test.yaml"
			} else if mode == "replacement" {
				options.ConfigurationPath, path = "deploy/app.yaml", "deploy/app.yaml"
			}
			document := "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New, config: {value: [PRIVATE_INVALID]}}}}\n"
			writeFile(t, filepath.Join(root, path), document)
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			captured := inputs.CurrentLayers()
			id, _ := interfaceid.Parse("app.resource/v1")
			for _, kind := range []string{"require", "expose"} {
				for _, present := range []bool{false, true} {
					edit := applicationmeta.SetInterfaceRoot
					if mode == "environment" {
						edit = applicationmeta.SetInterfaceRootOverlay
					}
					updated, _, err := edit([]byte(document), id, kind, present)
					if err != nil {
						t.Fatal(err)
					}
					want, err := inputs.ComposeSelectionCandidate(updated)
					if err != nil {
						t.Fatal(err)
					}
					got, err := inputs.ProjectInterfaceRoots([]applicationmeta.InterfaceRootEdit{{Interface: id, Root: kind, Present: present}})
					if err != nil {
						t.Fatal(err)
					}
					schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
						return implementationinventory.Configuration{}, false
					}
					wantDigest, _ := applicationmeta.ConfigurationLayerDigest(want.Manifest(), schemas)
					gotDigest, err := applicationmeta.ConfigurationLayerDigest(got.Manifest(), schemas)
					if err != nil || gotDigest != wantDigest {
						t.Fatalf("root projection differs from authored candidate: %v", err)
					}
					if _, err := inputs.ComposeCandidate(updated); !errors.Is(err, applicationmeta.ErrConfigurationValues) {
						t.Fatal("identity projection relaxed final typed validation")
					}
				}
			}
			roots := []applicationmeta.InterfaceRootEdit{{Interface: id, Root: "require"}, {Interface: id, Root: "expose", Present: true}}
			updated, _, err := applicationmeta.EditRootConfiguration([]byte(document), applicationmeta.RootConfigurationEdit{Roots: roots}, mode == "environment")
			if err != nil {
				t.Fatal(err)
			}
			want, err := inputs.ComposeSelectionCandidate(updated)
			if err != nil {
				t.Fatal(err)
			}
			got, err := inputs.ProjectInterfaceRoots(roots)
			if err != nil || !reflect.DeepEqual(got.Manifest().InterfaceRequirements(), want.Manifest().InterfaceRequirements()) || !reflect.DeepEqual(got.Manifest().HTTPExposures(), want.Manifest().HTTPExposures()) || roots[0].PreserveRoot || roots[1].PreserveRoot {
				t.Fatalf("captured root batch differs from authored candidate or mutated request: %v", err)
			}
			writeFile(t, filepath.Join(root, path), "[PRIVATE_LATER_EDIT]\n")
			before := snapshotTree(t, root)
			if _, err := inputs.ProjectInterfaceRoots([]applicationmeta.InterfaceRootEdit{{Interface: id, Root: "require", Present: true}}); err != nil {
				t.Fatalf("projection reread changed input: %v", err)
			}
			if !reflect.DeepEqual(captured, inputs.CurrentLayers()) || string(inputs.SelectedSnapshot().Data()) != document {
				t.Fatal("projection changed captured layers")
			}
			if inputs.ValidateSnapshot(t.Context()) == nil || !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("snapshot missed or overwrote later document drift")
			}
		})
	}
	if _, err := (applicationresolve.SelectionInputs{}).ProjectInterfaceRoots(nil); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty root projection accepted")
	}
}
