package interfacecreate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceinventory"
)

func TestCreateRollsBackScaffoldAfterValidationFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeRollbackFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/rollback\n\ngo 1.26\n")
	writeRollbackFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	want := snapshotTree(t, root)
	sentinel := errors.New("forced post-validation failure")
	_, err := create(context.Background(), Options{
		Start:       root,
		Name:        "order.create",
		Environment: append(os.Environ(), "GOWORK=off"),
	}, func(string, interfaceinventory.Index) error {
		return sentinel
	})
	if !errors.Is(err, ErrCreate) || !errors.Is(err, sentinel) {
		t.Fatalf("create error = %v", err)
	}
	if got := snapshotTree(t, root); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tree changed after rollback:\nbefore: %v\nafter:  %v", want, got)
	}
}

func snapshotTree(t testing.TB, root string) []string {
	t.Helper()
	var result []string
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			result = append(result, filepath.ToSlash(relative)+"/")
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(relative)+"="+string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func writeRollbackFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
