package interfaceinventory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestOwnershipSnapshotPathAliases(t *testing.T) {
	for _, mode := range []string{"module-root", "package-directory", "both"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			physical := filepath.Join(base, "physical")
			root := filepath.Join(physical, "project")
			alias := filepath.Join(base, "alias")
			if err := os.MkdirAll(filepath.Join(root, "api"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(physical, alias); err != nil {
				t.Skipf("directory symlinks unavailable: %v", err)
			}
			files := map[string]string{
				"go.mod":       "module example.com/alias\n\ngo 1.26\n",
				"plystra.yaml": "{}\n",
				"api/interface.go": `package api
import "context"
//plystra:interface alias.read/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct {}
type Response struct {}
`,
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			project, err := projectlocate.Find(root)
			if err != nil {
				t.Fatal(err)
			}
			options := Options{Environment: moduledependencyEnvironment(os.Environ(), "GOWORK", "off"), Offline: true}
			discovery, err := DiscoverApplication(t.Context(), project, moduledependency.Index{}, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(discovery.Interfaces().Interfaces()) != 1 || discovery.sourcePackages["example.com/alias/api"].Module == nil {
				t.Fatal("fixture did not select its real authored package")
			}
			aliasRoot := filepath.Join(alias, "project")
			// Go may report a physical package directory and an aliased module
			// root (or vice versa). Change only spellings of real selected inputs;
			// capture and re-selection still use the actual filesystem/toolchain.
			for name, loaded := range discovery.sourcePackages {
				if !strings.HasPrefix(name, "example.com/alias/") {
					continue
				}
				module := *loaded.Module
				loaded.Module = &module
				if mode != "package-directory" {
					loaded.Module.Dir = aliasRoot
				}
				if mode != "module-root" {
					loaded.Dir = filepath.Join(aliasRoot, "api")
				}
				discovery.sourcePackages[name] = loaded
			}
			snapshot, err := discovery.CaptureOwnershipSources(t.Context(), project, moduledependency.Index{}, options)
			if err != nil {
				t.Fatalf("equivalent directory aliases rejected: %v", err)
			}
			if err := snapshot.ValidateSnapshot(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Retargeting an alias must not reinterpret the retained directory
			// identities, even when its replacement contains identical source.
			other := filepath.Join(base, "other")
			if err := os.MkdirAll(filepath.Join(other, "project", "api"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(other, "project", filepath.FromSlash(name)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			outside := discovery
			outside.sourcePackages = make(map[string]listedPackage, len(discovery.sourcePackages))
			for name, loaded := range discovery.sourcePackages {
				if name == "example.com/alias/api" {
					loaded.Dir = filepath.Join(other, "project", "api")
				}
				outside.sourcePackages[name] = loaded
			}
			if _, err := outside.CaptureOwnershipSources(t.Context(), project, moduledependency.Index{}, options); !errors.Is(err, ErrOwnershipSnapshot) {
				t.Fatalf("physical package outside its module accepted: %v", err)
			}
			file := filepath.Join(root, "api", "interface.go")
			if err := os.Rename(file, file+".retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(files["api/interface.go"]), 0600); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.ValidateSnapshot(t.Context()); !errors.Is(err, ErrOwnershipSnapshot) {
				t.Fatalf("same-byte file replacement accepted: %v", err)
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(file+".retained", file); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(root, root+".retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "api"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := snapshot.ValidateSnapshot(t.Context()); !errors.Is(err, ErrOwnershipSnapshot) {
				t.Fatalf("same-byte directory replacement accepted: %v", err)
			}
			if err := os.Rename(root, root+".replacement"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(root+".retained", root); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.ValidateSnapshot(t.Context()); err != nil {
				t.Fatalf("restored retained identities rejected: %v", err)
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, alias); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.ValidateSnapshot(t.Context()); !errors.Is(err, ErrOwnershipSnapshot) {
				t.Fatalf("retargeted alias accepted: %v", err)
			}
		})
	}
}
