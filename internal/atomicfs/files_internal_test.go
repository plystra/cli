package atomicfs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCreateParentDirectoriesRejectsParentThatAppearedAfterPlanning(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	missing, err := inspectParents(root, filepath.Join("account", "docs", "README.md"))
	if err != nil {
		t.Fatalf("inspectParents: %v", err)
	}
	wantMissing := []string{"account", filepath.Join("account", "docs")}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Fatalf("missing parents = %v, want %v", missing, wantMissing)
	}
	if err := os.Mkdir(filepath.Join(directory, "account"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	planned := []plannedWrite{{
		osPath:         filepath.Join("account", "docs", "README.md"),
		missingParents: missing,
	}}
	created, err := createParentDirectories(root, planned)
	if !errors.Is(err, ErrConcurrentChange) {
		t.Fatalf("createParentDirectories error = %v, want ErrConcurrentChange", err)
	}
	if len(created) != 0 {
		t.Fatalf("created directories = %v, want none", created)
	}
	if _, err := os.Stat(filepath.Join(directory, "account")); err != nil {
		t.Fatalf("concurrent parent was changed: %v", err)
	}
}
