package pluginscan_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/plystra/cli/internal/pluginscan"
)

func TestScanDiscoversOnlyDirectChildPlugins(t *testing.T) {
	t.Parallel()

	source := fstest.MapFS{
		"account/plugin.yaml":               {Data: []byte("id: acme.account")},
		"profile/plugin.yaml":               {Data: []byte("id: acme.profile")},
		"ordinary/readme.txt":               {Data: []byte("not a plugin")},
		"parent/nested/plugin.yaml":         {Data: []byte("id: acme.nested")},
		"plugin.yaml":                       {Data: []byte("id: acme.root")},
		"plugins/legacy/plugin.yaml":        {Data: []byte("id: acme.legacy")},
		"generated/tooling/plugin.yaml":     {Data: []byte("id: acme.generated")},
		".private/hidden/plugin.yaml":       {Data: []byte("id: acme.hidden")},
		"node_modules/dependency/plugin.go": {Data: []byte("package dependency")},
	}

	result, err := pluginscan.Scan(source)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := result.Directories()
	if len(got) != 2 || got[0].Name() != "account" || got[0].Path() != "account" || got[1].Name() != "profile" || got[1].Path() != "profile" {
		t.Fatalf("Directories() = %#v, want account then profile", got)
	}

	got[0] = pluginscan.Directory{}
	if result.Directories()[0].Name() != "account" {
		t.Fatal("Directories exposed mutable result storage")
	}
}

func TestReservedDirectorySet(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".git", ".github", ".hidden", "docs", "generated", "dist", "examples", "testdata", "vendor", "node_modules"} {
		if !pluginscan.IsReserved(name) {
			t.Fatalf("IsReserved(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"account", "profile", "documentation", "generator", "vendor-plugin"} {
		if pluginscan.IsReserved(name) {
			t.Fatalf("IsReserved(%q) = true, want false", name)
		}
	}
}

func TestScanSkipsDirectorySymlinks(t *testing.T) {
	t.Parallel()

	source := fstest.MapFS{
		"linked":             {Mode: fs.ModeSymlink, Data: []byte("outside")},
		"linked/plugin.yaml": {Data: []byte("id: acme.linked")},
	}
	result, err := pluginscan.Scan(source)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Directories()) != 0 {
		t.Fatalf("Directories() = %#v, want empty", result.Directories())
	}
}

func TestScanRejectsUnsafePluginMarker(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		marker *fstest.MapFile
	}{
		{name: "directory", marker: &fstest.MapFile{Mode: fs.ModeDir}},
		{name: "symlink", marker: &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("outside.yaml")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := pluginscan.Scan(fstest.MapFS{"account/plugin.yaml": test.marker})
			if !errors.Is(err, pluginscan.ErrInvalidMarker) {
				t.Fatalf("Scan error = %v, want ErrInvalidMarker", err)
			}
		})
	}
}

func TestScanRootUsesRealFilesystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pluginDirectory := filepath.Join(root, "account")
	if err := os.Mkdir(pluginDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDirectory, "plugin.yaml"), []byte("id: acme.account\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := pluginscan.ScanRoot(root)
	if err != nil {
		t.Fatalf("ScanRoot: %v", err)
	}
	if got := result.Directories(); len(got) != 1 || got[0].Name() != "account" {
		t.Fatalf("Directories() = %#v, want account", got)
	}
}

func TestScanRejectsNilAndMissingRoots(t *testing.T) {
	t.Parallel()

	if _, err := pluginscan.Scan(nil); !errors.Is(err, pluginscan.ErrScan) {
		t.Fatalf("Scan(nil) error = %v, want ErrScan", err)
	}
	if _, err := pluginscan.ScanRoot(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, pluginscan.ErrScan) {
		t.Fatalf("ScanRoot(missing) error = %v, want ErrScan", err)
	}
}
