package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestSelectionInputsProjectResourceSelectionUsesCapturedIdentities(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "direct")
			writeResourceProvider(t, root)
			options, path := selectionOptions(root), "plystra.yaml"
			switch mode {
			case "environment":
				options.EnvironmentName, path = "test", "plystra.test.yaml"
			case "replacement":
				options.ConfigurationPath, path = "deploy/app.yaml", "deploy/app.yaml"
			}
			document := "resources: {instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}, database.retained: {use: example.com/resource-consumer/provider.New, config: {value: [PRIVATE_INVALID]}}}}\n"
			writeFile(t, filepath.Join(root, path), document)
			before := snapshotTree(t, root)
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			captured := inputs.CurrentLayers()
			provider, err := constructorsymbol.Parse("example.com/resource-consumer/provider.New")
			if err != nil {
				t.Fatal(err)
			}
			for _, remove := range []bool{false, true} {
				set, del := applicationmeta.SetResourceProvider, applicationmeta.RemoveResourceInstance
				if mode == "environment" {
					set, del = applicationmeta.SetResourceProviderOverlay, applicationmeta.RemoveResourceInstanceOverlay
				}
				choice := provider
				updated, _, err := set([]byte(document), "database.primary", provider, false, nil)
				if remove {
					choice = constructorsymbol.Symbol{}
					updated, _, err = del([]byte(document), "database.primary")
				}
				if err != nil {
					t.Fatal(err)
				}
				want, err := inputs.ComposeSelectionCandidate(updated)
				if err != nil {
					t.Fatal(err)
				}
				got, err := inputs.ProjectResourceSelection("database.primary", choice, remove)
				if err != nil {
					t.Fatal(err)
				}
				schemas := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
					return implementationinventory.Configuration{}, false
				}
				wantDigest, err := applicationmeta.ConfigurationLayerDigest(want.Manifest(), schemas)
				if err != nil {
					t.Fatal(err)
				}
				gotDigest, err := applicationmeta.ConfigurationLayerDigest(got.Manifest(), schemas)
				if err != nil || wantDigest != gotDigest {
					t.Fatalf("projection differs from authored Resource change: %v", err)
				}
				if _, err := inputs.ComposeCandidate(updated); !errors.Is(err, applicationmeta.ErrConfigurationValues) || strings.Contains(err.Error(), "PRIVATE_INVALID") {
					t.Fatalf("projection relaxed final typed composition: %v", err)
				}
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("Resource projection changed Project files")
			}
			writeFile(t, filepath.Join(root, path), "[PRIVATE_LATER_EDIT]\n")
			drifted := snapshotTree(t, root)
			if _, err := inputs.ProjectResourceSelection("database.primary", provider, false); err != nil {
				t.Fatalf("projection reread changed file: %v", err)
			}
			if !reflect.DeepEqual(captured, inputs.CurrentLayers()) || string(inputs.SelectedSnapshot().Data()) != document {
				t.Fatal("projection mutated captured input")
			}
			if err := inputs.ValidateSnapshot(t.Context()); err == nil {
				t.Fatal("snapshot missed later document drift")
			}
			if !reflect.DeepEqual(drifted, snapshotTree(t, root)) {
				t.Fatal("projection overwrote later edits")
			}
		})
	}
	if _, err := (applicationresolve.SelectionInputs{}).ProjectResourceSelection("database.primary", constructorsymbol.Symbol{}, true); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty input projected Resource selection")
	}
}
