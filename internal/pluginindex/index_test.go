package pluginindex_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestScanBuildsDeterministicImmutableIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "profile", "acme.app.profile")
	accountManifest := []byte("id: acme.app.account\nprovides:\n  - profile.get/v2\n  - account.register/v1\ngeneration: {api: v1, package: ./generation, activations: [{namespace: profile, capability: profile.get/v2}]}\n")
	writeManifest(t, root, "account", string(accountManifest))
	index, err := pluginindex.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	plugins := index.Plugins()
	if len(plugins) != 2 || plugins[0].Name() != "account" || plugins[0].Path() != "account" || plugins[0].ID() != "acme.app.account" || plugins[1].Name() != "profile" {
		t.Fatalf("Plugins() = %#v", plugins)
	}
	if got := identifierStrings(plugins[0].Provides()); !reflect.DeepEqual(got, []string{"account.register/v1", "profile.get/v2"}) {
		t.Fatalf("account Provides() = %v", got)
	}
	if got := plugins[0].ManifestData(); !bytes.Equal(got, accountManifest) {
		t.Fatalf("account ManifestData() = %q", got)
	}
	generation, ok := plugins[0].Generation()
	if !ok || generation.API() != "v1" || generation.Package() != "./generation" || len(generation.Activations()) != 1 || generation.Activations()[0].Namespace() != "profile" {
		t.Fatalf("account Generation() = %#v, %t", generation, ok)
	}
	activations := generation.Activations()
	activations[0] = pluginmeta.GenerationActivation{}
	indexedGeneration, _ := index.Plugins()[0].Generation()
	if indexedGeneration.Activations()[0].Namespace() != "profile" {
		t.Fatal("Generation exposed mutable index storage")
	}
	if _, ok := plugins[1].Generation(); ok {
		t.Fatal("profile unexpectedly has generation metadata")
	}
	if byName, ok := index.ByReference("account"); !ok || byName.ID() != "acme.app.account" {
		t.Fatalf("ByReference(directory) = %#v, %t", byName, ok)
	}
	if byID, ok := index.ByReference("acme.app.profile"); !ok || byID.Name() != "profile" {
		t.Fatalf("ByReference(ID) = %#v, %t", byID, ok)
	}
	if _, ok := index.ByReference("missing"); ok {
		t.Fatal("ByReference(missing) succeeded")
	}
	plugins[0] = pluginindex.Plugin{}
	if index.Plugins()[0].Name() != "account" {
		t.Fatal("Plugins exposed mutable index storage")
	}
	provided := index.Plugins()[0].Provides()
	provided[0] = capabilityid.Identifier{}
	if index.Plugins()[0].Provides()[0].String() != "account.register/v1" {
		t.Fatal("Provides exposed mutable index storage")
	}
	manifest := index.Plugins()[0].ManifestData()
	manifest[0] = 'x'
	if index.Plugins()[0].ManifestData()[0] != 'i' {
		t.Fatal("ManifestData exposed mutable index storage")
	}
}

func TestScanRejectsDuplicateIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "account", "acme.app.shared")
	writePlugin(t, root, "profile", "acme.app.shared")
	index, err := pluginindex.Scan(root)
	if !errors.Is(err, pluginindex.ErrIndex) || !errors.Is(err, pluginindex.ErrDuplicateID) {
		t.Fatalf("Scan error = %v, want ErrIndex and ErrDuplicateID", err)
	}
	if len(index.Plugins()) != 0 {
		t.Fatalf("invalid Scan returned %#v", index.Plugins())
	}
}

func TestScanRejectsInvalidIndexedMetadata(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"identity":   "id: Acme.Bad\n",
		"capability": "id: acme.app.account\nprovides: [account.register]\n",
	}
	for name, declaration := range tests {
		name, declaration := name, declaration
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, "account", declaration)
			if _, err := pluginindex.Scan(root); !errors.Is(err, pluginindex.ErrIndex) {
				t.Fatalf("Scan error = %v, want ErrIndex", err)
			}
		})
	}
}

func writePlugin(t *testing.T, root, name, id string) {
	t.Helper()
	writeManifest(t, root, name, "id: "+id+"\n")
}

func writeManifest(t *testing.T, root, name, declaration string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir(%s): %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(declaration), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}
