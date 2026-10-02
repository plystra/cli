package privatefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/privatefile"
)

func TestCreatePrivateBeforeWriteAndAfterRename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	file, err := privatefile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Check(file); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("private-data"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := privatefile.Create(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite: %v", err)
	}
	installed := filepath.Join(root, "installed")
	if err := os.Rename(path, installed); err != nil {
		t.Fatal(err)
	}
	file, err = os.Open(installed)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := privatefile.Check(file); err != nil {
		t.Fatal(err)
	}
	makePublic(t, installed)
	if !errors.Is(privatefile.Check(file), privatefile.ErrPrivate) {
		t.Fatal("accepted public file")
	}
}

func TestTransactionPreservesConcurrentPermissionChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	if err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "private", Data: []byte("original"), OwnerPrivate: true}}, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "private", Data: []byte("replacement"), OwnerPrivate: true}}, func(string) error {
		makePublic(t, path)
		return nil
	})
	if !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("permission edit accepted: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatal("rollback overwrote concurrently edited file")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if privatefile.Check(file) == nil {
		t.Fatal("rollback changed user permissions")
	}
	backups, err := filepath.Glob(filepath.Join(root, ".plystra-files-*", "backup", "*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing recovery file: %v", err)
	}
	backup, err := os.Open(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := privatefile.Check(backup); err != nil {
		t.Fatalf("recovery file is public: %v", err)
	}
}
