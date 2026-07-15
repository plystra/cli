package modulelocate_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/modulelocate"
)

func TestFindNearestEnclosingModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/outer\n\ngo 1.26\n")
	nestedModule := filepath.Join(root, "nested")
	if err := os.Mkdir(nestedModule, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, filepath.Join(nestedModule, "go.mod"), "module example.com/acme/inner\n\ngo 1.26\n")
	workingDirectory := filepath.Join(nestedModule, "plugin", "capabilities")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	located, err := modulelocate.Find(workingDirectory)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if located.Path() != nestedModule || located.ModulePath() != "example.com/acme/inner" {
		t.Fatalf("Find = path %q, module %q", located.Path(), located.ModulePath())
	}
}

func TestFindResolvesStartSymlink(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "module")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/module\n")
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(root, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("Symlink unavailable: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}
	located, err := modulelocate.Find(link)
	if err != nil || located.Path() != root {
		t.Fatalf("Find(link) = %#v, %v", located, err)
	}
}

func TestFindRejectsMissingAndInvalidModules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    error
	}{
		{name: "missing", want: modulelocate.ErrNotFound},
		{name: "missing directive", content: "go 1.26\n", want: modulelocate.ErrInvalidGoMod},
		{name: "malformed", content: "module\n", want: modulelocate.ErrInvalidGoMod},
		{name: "invalid module path", content: "module example.com/acme/../bad\n", want: modulelocate.ErrInvalidGoMod},
		{name: "oversized", content: "module example.com/acme/large\n" + strings.Repeat(" ", (1<<20)+1), want: modulelocate.ErrInvalidGoMod},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if test.content != "" {
				writeFile(t, filepath.Join(root, "go.mod"), test.content)
			}
			located, err := modulelocate.Find(root)
			if !errors.Is(err, test.want) {
				t.Fatalf("Find error = %v, want %v", err, test.want)
			}
			if located.Path() != "" || located.ModulePath() != "" {
				t.Fatalf("invalid Find returned %#v", located)
			}
		})
	}
}

func TestFindRejectsInvalidStarts(t *testing.T) {
	t.Parallel()

	if _, err := modulelocate.Find(""); !errors.Is(err, modulelocate.ErrLocate) {
		t.Fatalf("Find(empty) error = %v", err)
	}
	root := t.TempDir()
	file := filepath.Join(root, "file")
	writeFile(t, file, "not a directory")
	if _, err := modulelocate.Find(file); !errors.Is(err, modulelocate.ErrLocate) {
		t.Fatalf("Find(file) error = %v", err)
	}
	if _, err := modulelocate.Find(filepath.Join(root, "missing")); !errors.Is(err, modulelocate.ErrLocate) {
		t.Fatalf("Find(missing) error = %v", err)
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
