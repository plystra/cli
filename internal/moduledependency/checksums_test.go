package moduledependency_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestDiscoverRequiresEffectiveSourceChecksums(t *testing.T) {
	for _, mode := range []string{"warm", "cold", "missing-full-sum", "missing-sum-file", "replacement-warm", "replacement-cold", "offline", "offline-missing-sum", "cache-checksum-drift"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const path, version = "example.com/Source/Dependency", "v1.2.3"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, version)
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\n\ngo 1.26\nrequire " + path + " " + version + "\n"
			required := path
			if strings.HasPrefix(mode, "replacement-") {
				required = "example.com/original"
				goMod = "module example.com/app\n\ngo 1.26\nrequire " + required + " v1.0.0\nreplace " + required + " => " + path + " " + version + "\n"
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			env := isolatedGoEnvironment(t, proxy)
			runGo(t, app, env, "mod", "download", path+"@"+version)
			index, err := moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			source, exists := index.ByPath(required)
			if !exists {
				t.Fatal("source missing")
			}
			expected, err := dirhash.HashDir(source.Root(), path+"@"+version, dirhash.Hash1)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing-full-sum" || mode == "offline-missing-sum" {
				var lines []string
				for _, line := range strings.Split(string(readFile(t, filepath.Join(app, "go.sum"))), "\n") {
					if strings.Contains(line, "/go.mod ") {
						lines = append(lines, line)
					}
				}
				writeFile(t, filepath.Join(app, "go.sum"), strings.Join(lines, "\n")+"\n")
			} else if mode == "missing-sum-file" {
				if err := os.Remove(filepath.Join(app, "go.sum")); err != nil {
					t.Fatal(err)
				}
			} else if mode == "cold" || mode == "replacement-cold" {
				if err := os.RemoveAll(source.Root()); err != nil {
					t.Fatal(err)
				}
			} else if mode == "cache-checksum-drift" {
				cache := environmentValue(t, env, "GOMODCACHE")
				writeFile(t, filepath.Join(cache, "cache", "download", "example.com", "!source", "!dependency", "@v", version+".ziphash"), "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
			}
			before, readErr := os.ReadFile(filepath.Join(app, "go.sum"))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal(readErr)
			}
			index, err = moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{
				Environment: env, RequireSourceChecksums: true, Offline: strings.HasPrefix(mode, "offline"),
			})
			if mode == "offline-missing-sum" {
				if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
					t.Fatalf("offline discovery accepted missing local evidence: %v", err)
				}
			} else if mode == "missing-sum-file" {
				if !errors.Is(err, moduledependency.ErrDiscover) {
					t.Fatalf("readonly graph discovery accepted missing manifest checksum: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				selected, exists := index.ByPath(required)
				if !exists || selected.Checksum() != expected {
					t.Fatalf("effective archive checksum = %q, want %q", selected.Checksum(), expected)
				}
				if strings.HasPrefix(mode, "replacement-") && selected.GoModChecksum() == "" {
					t.Fatal("replacement lost nested manifest checksum")
				}
			}
			after, afterErr := os.ReadFile(filepath.Join(app, "go.sum"))
			if !bytes.Equal(before, after) || errors.Is(readErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) ||
				string(readFile(t, filepath.Join(app, "go.mod"))) != goMod {
				t.Fatal("checksum resolution changed Project module files")
			}
		})
	}
}

func TestDiscoverSourceChecksumsAcceptUnchangedAbsentGoSum(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "dependency-free", true: "local-only"}[local], func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\n\ngo 1.26\n"
			if local {
				writeModule(t, filepath.Join(root, "dependency"), "example.com/local")
				goMod += "require example.com/local v1.0.0\nreplace example.com/local => ../dependency\n"
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			_, err := moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{
				Environment: isolatedGoEnvironment(t, t.TempDir()), RequireSourceChecksums: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(app, "go.sum")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("checksum verification created go.sum")
			}
		})
	}
}
