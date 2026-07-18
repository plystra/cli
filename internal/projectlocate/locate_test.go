package projectlocate_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestFindRequiresNearestModuleProjectMarker(t *testing.T) {
	t.Parallel()

	outer := t.TempDir()
	writeFile(t, filepath.Join(outer, "go.mod"), "module example.com/acme/outer\n")
	writeFile(t, filepath.Join(outer, "plystra.yaml"), "{}\n")
	inner := filepath.Join(outer, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, filepath.Join(inner, "go.mod"), "module example.com/acme/inner\n")
	nested := filepath.Join(inner, "plugin", "capabilities")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if _, err := projectlocate.Find(nested); !errors.Is(err, projectlocate.ErrNotFound) {
		t.Fatalf("Find without nearest marker error = %v", err)
	}
	writeFile(t, filepath.Join(inner, "plystra.yaml"), "{}\n")
	project, err := projectlocate.Find(nested)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if project.ModulePath() != "example.com/acme/inner" {
		t.Fatalf("Find module = %q", project.ModulePath())
	}
}

func TestFindRejectsUnsafeProjectMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "directory",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "plystra.yaml"), 0o755); err != nil {
					t.Fatalf("Mkdir: %v", err)
				}
			},
		},
		{
			name: "symlink",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeFile(t, filepath.Join(root, "configuration.yaml"), "{}\n")
				if err := os.Symlink("configuration.yaml", filepath.Join(root, "plystra.yaml")); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("Symlink unavailable: %v", err)
					}
					t.Fatalf("Symlink: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/app\n")
			test.setup(t, root)
			if _, err := projectlocate.Find(root); !errors.Is(err, projectlocate.ErrInvalidManifest) {
				t.Fatalf("Find error = %v", err)
			}
		})
	}
}

func TestFindPreservesModuleLocationErrors(t *testing.T) {
	t.Parallel()

	if _, err := projectlocate.Find(t.TempDir()); !errors.Is(err, projectlocate.ErrLocate) || !errors.Is(err, modulelocate.ErrNotFound) {
		t.Fatalf("Find error = %v", err)
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
