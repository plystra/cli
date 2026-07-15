package capabilitysource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathStateComparisonDetectsSourceReplacement(t *testing.T) {
	t.Parallel()

	pluginRoot := t.TempDir()
	relative := "capabilities/account.register/v1/capability.yaml"
	absolute := filepath.Join(pluginRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(absolute, []byte("id: account.register/v1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(before): %v", err)
	}
	root, err := os.OpenRoot(pluginRoot)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()
	before, err := inspectPath(root, relative)
	if err != nil {
		t.Fatalf("inspectPath(before): %v", err)
	}
	if !samePathStates(before, before) {
		t.Fatal("samePathStates rejected identical states")
	}
	replacement := filepath.Join(filepath.Dir(absolute), "replacement.yaml")
	if err := os.WriteFile(replacement, []byte("id: account.register/v1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(replacement): %v", err)
	}
	if err := os.Remove(absolute); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Rename(replacement, absolute); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	after, err := inspectPath(root, relative)
	if err != nil {
		t.Fatalf("inspectPath(after): %v", err)
	}
	if samePathStates(before, after) {
		t.Fatal("samePathStates accepted a replaced source")
	}
}
