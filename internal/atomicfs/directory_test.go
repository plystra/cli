package atomicfs_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
)

func TestCreateDirectoryCommitsPopulatedTree(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	err := atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		if filepath.Dir(stagingRoot) != parent {
			t.Fatalf("staging root %q is not under %q", stagingRoot, parent)
		}
		if err := os.Mkdir(filepath.Join(stagingRoot, "generated"), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(stagingRoot, "go.mod"), []byte("module example.test/project\n"), 0o644)
	})
	if err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(content) != "module example.test/project\n" {
		t.Fatalf("go.mod = %q", content)
	}
	if info, err := os.Stat(filepath.Join(target, "generated")); err != nil || !info.IsDir() {
		t.Fatalf("generated directory = %#v, %v", info, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Stat(target): %v", err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("target mode = %o, want 755", got)
		}
	}
	assertNoStagingDirectories(t, parent)
}

func TestCreateDirectoryRollsBackPopulateFailure(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	wantErr := errors.New("validation failed")
	err := atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		if err := os.WriteFile(filepath.Join(stagingRoot, "partial.txt"), []byte("partial"), 0o644); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) || !errors.Is(err, atomicfs.ErrCreateDirectory) {
		t.Fatalf("CreateDirectory error = %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after rollback: %v", err)
	}
	assertNoStagingDirectories(t, parent)
}

func TestCreateDirectoryCleansStagingAfterPopulatePanic(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = atomicfs.CreateDirectory(target, func(stagingRoot string) error {
			if err := os.WriteFile(filepath.Join(stagingRoot, "partial.txt"), []byte("partial"), 0o644); err != nil {
				return err
			}
			panic("populate panic")
		})
	}()
	if recovered != "populate panic" {
		t.Fatalf("recovered value = %#v", recovered)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after panic: %v", err)
	}
	assertNoStagingDirectories(t, parent)
}

func TestCreateDirectoryPreservesExistingTargets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		create func(t *testing.T, target string)
	}{
		{
			name: "directory",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatalf("Mkdir: %v", err)
				}
			},
		},
		{
			name: "file",
			create: func(t *testing.T, target string) {
				t.Helper()
				if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			target := filepath.Join(parent, "project")
			test.create(t, target)
			called := false
			err := atomicfs.CreateDirectory(target, func(string) error {
				called = true
				return nil
			})
			if !errors.Is(err, atomicfs.ErrTargetExists) {
				t.Fatalf("CreateDirectory error = %v, want ErrTargetExists", err)
			}
			if called {
				t.Fatal("populate callback ran for an existing target")
			}
			assertNoStagingDirectories(t, parent)
		})
	}
}

func TestCreateDirectoryPreservesTargetCreatedDuringPopulate(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	err := atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		if err := os.WriteFile(filepath.Join(stagingRoot, "new.txt"), []byte("new"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(target, []byte("concurrent"), 0o644)
	})
	if !errors.Is(err, atomicfs.ErrTargetExists) {
		t.Fatalf("CreateDirectory error = %v, want ErrTargetExists", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "concurrent" {
		t.Fatalf("concurrent target = %q, %v", content, err)
	}
	assertNoStagingDirectories(t, parent)
}

func TestCreateDirectoryRejectsInvalidInputsAndReplacedStaging(t *testing.T) {
	t.Parallel()

	if err := atomicfs.CreateDirectory("", func(string) error { return nil }); !errors.Is(err, atomicfs.ErrCreateDirectory) {
		t.Fatalf("empty target error = %v", err)
	}
	if err := atomicfs.CreateDirectory(filepath.Join(t.TempDir(), "project"), nil); !errors.Is(err, atomicfs.ErrCreateDirectory) {
		t.Fatalf("nil callback error = %v", err)
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "project")
	err := atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		return os.Remove(stagingRoot)
	})
	if !errors.Is(err, atomicfs.ErrCreateDirectory) {
		t.Fatalf("removed staging error = %v", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after removed staging: %v", err)
	}
	assertNoStagingDirectories(t, parent)
}

func assertNoStagingDirectories(t *testing.T, parent string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(parent, ".*.plystra-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging directories remain: %v", matches)
	}
}
