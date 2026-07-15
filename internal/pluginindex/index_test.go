package pluginindex_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/pluginindex"
)

func TestScanBuildsDeterministicImmutableIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePlugin(t, root, "profile", "acme.app.profile")
	writePlugin(t, root, "account", "acme.app.account")
	index, err := pluginindex.Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	plugins := index.Plugins()
	if len(plugins) != 2 || plugins[0].Name() != "account" || plugins[0].Path() != "account" || plugins[0].ID() != "acme.app.account" || plugins[1].Name() != "profile" {
		t.Fatalf("Plugins() = %#v", plugins)
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

func TestScanRejectsInvalidManifestIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "account")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte("id: Acme.Bad\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := pluginindex.Scan(root); !errors.Is(err, pluginindex.ErrIndex) {
		t.Fatalf("Scan error = %v, want ErrIndex", err)
	}
}

func writePlugin(t *testing.T, root, name, id string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir(%s): %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte("id: "+id+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
