package atomicfs_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
)

func TestWriteFilesCommitsAfterValidation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("before"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	validated := false
	err := atomicfs.WriteFiles(root, []atomicfs.Write{
		{Path: "nested/new.txt", Data: []byte("new"), Mode: 0o640, MustNotExist: true},
		{Path: "existing.txt", Data: []byte("after")},
	}, func(updatedRoot string) error {
		validated = true
		assertFile(t, filepath.Join(updatedRoot, "existing.txt"), "after")
		assertFile(t, filepath.Join(updatedRoot, "nested", "new.txt"), "new")
		return nil
	})
	if err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if !validated {
		t.Fatal("validation callback did not run")
	}
	assertFile(t, existing, "after")
	assertFile(t, filepath.Join(root, "nested", "new.txt"), "new")
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(existing); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("existing mode = %#v, %v", info, err)
		}
		if info, err := os.Stat(filepath.Join(root, "nested", "new.txt")); err != nil || info.Mode().Perm() != 0o640 {
			t.Fatalf("new mode = %#v, %v", info, err)
		}
	}
	assertNoFileTransaction(t, root)
}

func TestWriteFilesRequiresMissingParentWhenRequested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := filepath.Join(root, "account")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	keep := filepath.Join(parent, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	validated := false
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{
		Path:               "account/plugin.yaml",
		Data:               []byte("id: acme.account\n"),
		MustNotExist:       true,
		ParentMustNotExist: true,
	}}, func(string) error {
		validated = true
		return nil
	})
	if !errors.Is(err, atomicfs.ErrTargetExists) {
		t.Fatalf("WriteFiles error = %v, want ErrTargetExists", err)
	}
	if validated {
		t.Fatal("validation ran after the parent precondition failed")
	}
	assertFile(t, keep, "keep")
	if _, err := os.Lstat(filepath.Join(parent, "plugin.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plugin.yaml exists after rejected transaction: %v", err)
	}
}

func TestWriteFilesRollsBackValidationFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("before"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	wantErr := errors.New("tests failed")
	err := atomicfs.WriteFiles(root, []atomicfs.Write{
		{Path: "a/b/new.txt", Data: []byte("new"), MustNotExist: true},
		{Path: "existing.txt", Data: []byte("after")},
	}, func(string) error { return wantErr })
	if !errors.Is(err, atomicfs.ErrWriteFiles) || !errors.Is(err, wantErr) {
		t.Fatalf("WriteFiles error = %v", err)
	}
	assertFile(t, filepath.Join(root, "existing.txt"), "before")
	if _, err := os.Lstat(filepath.Join(root, "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created directory remains after rollback: %v", err)
	}
	assertNoFileTransaction(t, root)
}

func TestWriteFilesRollsBackValidationPanic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "new.txt", Data: []byte("new")}}, func(string) error {
			panic("validation panic")
		})
	}()
	if recovered != "validation panic" {
		t.Fatalf("recovered value = %#v", recovered)
	}
	if _, err := os.Lstat(filepath.Join(root, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new file remains after panic: %v", err)
	}
	assertNoFileTransaction(t, root)
}

func TestWriteFilesPreservesConcurrentValidationEditAndRecoveryBackup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("before"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	validationErr := errors.New("validation failed")
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "existing.txt", Data: []byte("generated")}}, func(string) error {
		if err := os.WriteFile(target, []byte("concurrent user edit"), 0o644); err != nil {
			return err
		}
		return validationErr
	})
	if !errors.Is(err, validationErr) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("WriteFiles error = %v", err)
	}
	if !strings.Contains(err.Error(), "recovery data retained in .plystra-files-") {
		t.Fatalf("WriteFiles error does not identify recovery data: %v", err)
	}
	assertFile(t, target, "concurrent user edit")
	backups, globErr := filepath.Glob(filepath.Join(root, ".plystra-files-*", "backup", "*"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("recovery backups = %v, %v", backups, globErr)
	}
	assertFile(t, backups[0], "before")
	transactionRoot := filepath.Dir(filepath.Dir(backups[0]))
	t.Cleanup(func() {
		if err := os.RemoveAll(transactionRoot); err != nil {
			t.Errorf("remove recovery transaction: %v", err)
		}
	})
}

func TestWriteFilesRejectsUnsafeAndDuplicatePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		writes []atomicfs.Write
		want   error
	}{
		{name: "absolute", writes: []atomicfs.Write{{Path: "/outside", Data: []byte("x")}}, want: atomicfs.ErrUnsafePath},
		{name: "traversal", writes: []atomicfs.Write{{Path: "../outside", Data: []byte("x")}}, want: atomicfs.ErrUnsafePath},
		{name: "backslash", writes: []atomicfs.Write{{Path: `nested\file`, Data: []byte("x")}}, want: atomicfs.ErrUnsafePath},
		{name: "root", writes: []atomicfs.Write{{Path: ".", Data: []byte("x")}}, want: atomicfs.ErrUnsafePath},
		{name: "duplicate", writes: []atomicfs.Write{{Path: "same", Data: []byte("a")}, {Path: "same", Data: []byte("b")}}, want: atomicfs.ErrWriteFiles},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			err := atomicfs.WriteFiles(root, test.writes, func(string) error { return nil })
			if !errors.Is(err, test.want) {
				t.Fatalf("WriteFiles error = %v, want %v", err, test.want)
			}
			assertNoFileTransaction(t, root)
		})
	}
}

func TestWriteFilesRejectsExistingMustNotExistTarget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	called := false
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "existing.txt", Data: []byte("replace"), MustNotExist: true}}, func(string) error {
		called = true
		return nil
	})
	if !errors.Is(err, atomicfs.ErrTargetExists) {
		t.Fatalf("WriteFiles error = %v, want ErrTargetExists", err)
	}
	if called {
		t.Fatal("validation ran for rejected transaction")
	}
	assertFile(t, file, "keep")
}

func TestWriteFilesRejectsSymlinkParent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "linked/file.txt", Data: []byte("unsafe")}}, func(string) error { return nil })
	if !errors.Is(err, atomicfs.ErrUnsafePath) {
		t.Fatalf("WriteFiles error = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file escaped through symlink: %v", err)
	}
}

func TestWriteFilesRequiresValidation(t *testing.T) {
	t.Parallel()

	if err := atomicfs.WriteFiles(t.TempDir(), nil, nil); !errors.Is(err, atomicfs.ErrWriteFiles) {
		t.Fatalf("WriteFiles error = %v, want ErrWriteFiles", err)
	}
}

func assertFile(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", name, data, want)
	}
}

func assertNoFileTransaction(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction directories remain: %v", matches)
	}
}
