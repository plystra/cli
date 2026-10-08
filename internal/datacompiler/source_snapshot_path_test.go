package datacompiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalSourceDirectoryNormalizesResolvedPackagePath(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "module")
	packageRoot := filepath.Join(moduleRoot, "resource")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "module-alias")
	if err := os.Symlink(moduleRoot, aliasRoot); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatal(err)
	}

	got, err := canonicalSourceDirectory(moduleRoot, filepath.Join(aliasRoot, "resource"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(packageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonical package directory = %q, want %q", got, want)
	}
}
