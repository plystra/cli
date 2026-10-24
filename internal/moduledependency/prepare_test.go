package moduledependency_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestPrepareSourcesMaterializesExactEffectiveVersions(t *testing.T) {
	for _, mode := range []string{"cold", "warm", "replacement", "exact-replacement", "reverse-exact-replacement", "replacement-checksum-conflict", "local", "shared-source", "offline", "offline-missing-sum", "checksum-conflict"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const path, version = "example.com/Source/Dependency", "v1.2.3"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, version)
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\n\ngo 1.26\n"
			requested := []module.Version{{Path: path, Version: version}}
			if strings.Contains(mode, "replacement") || mode == "shared-source" {
				requested[0] = module.Version{Path: "example.com/original", Version: "v1.0.0"}
				goMod += "replace example.com/original => " + path + " " + version + "\n"
				if strings.Contains(mode, "exact-replacement") {
					goMod = strings.ReplaceAll(goMod, "replace example.com/original =>", "replace example.com/original v1.0.0 =>")
					goMod += "replace example.com/original => example.com/unavailable v1.0.0\n"
					if mode == "reverse-exact-replacement" {
						lines := strings.Split(goMod, "\n")
						lines[len(lines)-3], lines[len(lines)-2] = lines[len(lines)-2], lines[len(lines)-3]
						goMod = strings.Join(lines, "\n")
					}
				} else if mode == "shared-source" {
					requested = append(requested, module.Version{Path: "example.com/second", Version: "v1.0.0"})
					goMod += "replace example.com/second => " + path + " " + version + "\n"
				}
			} else if mode == "local" {
				goMod += "replace " + path + " => ../unavailable-local\n"
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			env := isolatedGoEnvironment(t, proxy)
			if mode == "warm" || mode == "offline" || mode == "offline-missing-sum" || strings.HasSuffix(mode, "checksum-conflict") {
				runGo(t, app, env, "mod", "download", path+"@"+version)
				// Exact downloads of an unrequired module need not persist sums.
				cache := environmentValue(t, env, "GOMODCACHE")
				escaped, _ := module.EscapePath(path)
				sum, err := dirhash.HashDir(filepath.Join(cache, filepath.FromSlash(escaped+"@"+version)), path+"@"+version, dirhash.Hash1)
				if err != nil {
					t.Fatal(err)
				}
				if mode != "offline-missing-sum" {
					if strings.HasSuffix(mode, "checksum-conflict") {
						sum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
					}
					writeFile(t, filepath.Join(app, "go.sum"), path+" "+version+" "+sum+"\n")
				}
			}
			before, beforeErr := os.ReadFile(filepath.Join(app, "go.sum"))
			var prepared []moduledependency.Module
			early := 0
			err := moduledependency.PrepareSources(t.Context(), locate(t, app), requested, moduledependency.Options{Environment: env, Offline: strings.HasPrefix(mode, "offline")}, func(sources []moduledependency.Module) error {
				for _, source := range sources {
					if source.IncompleteSourceMetadata() {
						early++
						expected, err := dirhash.HashDir(source.SourceDirectory(), path+"@"+version, dirhash.Hash1)
						if err != nil || source.Checksum() != expected {
							return moduledependency.ErrSourceIntegrity
						}
					} else {
						prepared = append(prepared, source)
					}
				}
				return nil
			})
			if mode == "offline-missing-sum" || strings.HasSuffix(mode, "checksum-conflict") {
				if err == nil || len(prepared) != 0 {
					t.Fatalf("untrusted checksum evidence accepted: %v", err)
				}
			} else if mode == "local" {
				if err != nil || len(prepared) != 0 {
					t.Fatalf("local replacement triggered an original-module download: %v", err)
				}
			} else {
				if err != nil || len(prepared) != len(requested) {
					t.Fatalf("prepare sources: %v, modules=%v", err, prepared)
				}
				if (mode == "warm" || mode == "offline") && early != 1 {
					t.Fatal("archive-only source was not authenticated before acquisition")
				}
				for index, dependency := range prepared {
					expected, err := dirhash.HashDir(dependency.SourceDirectory(), path+"@"+version, dirhash.Hash1)
					if err != nil || dependency.Checksum() != expected || dependency.Path() != requested[index].Path || dependency.SelectedVersion() != requested[index].Version {
						t.Fatalf("prepared source identity: %v, %v", dependency, err)
					}
				}
			}
			after, afterErr := os.ReadFile(filepath.Join(app, "go.sum"))
			if !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) || string(readFile(t, filepath.Join(app, "go.mod"))) != goMod {
				t.Fatal("source preparation changed Project metadata")
			}
		})
	}
}
