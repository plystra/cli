package applicationresolve

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecheckDependencyManifestsRejectsConcurrentChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestPath := filepath.Join(root, applicationManifestName)
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snapshot, err := ReadManifestSnapshot(root)
	if err != nil {
		t.Fatalf("ReadManifestSnapshot: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("capabilities: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile changed manifest: %v", err)
	}

	err = recheckDependencyManifests([]dependencyManifestSnapshot{{
		identity: "example.com/dependency@v1.2.3",
		root:     root,
		snapshot: snapshot,
	}})
	if !errors.Is(err, ErrConcurrentChange) || !strings.Contains(err.Error(), "example.com/dependency@v1.2.3") || !strings.Contains(err.Error(), "plystra.yaml changed") {
		t.Fatalf("recheckDependencyManifests error = %v", err)
	}
}
