package moduledependency_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"golang.org/x/mod/module"
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
				manifest, err := os.ReadFile(selected.GoModFile())
				if err != nil {
					t.Fatal(err)
				}
				manifestSum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(manifest)), nil
				})
				if err != nil || selected.GoModChecksum() != manifestSum {
					t.Fatalf("effective manifest checksum = %q, want %q: %v", selected.GoModChecksum(), manifestSum, err)
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

func TestDiscoverRequiresCompleteManifestChecksumEvidence(t *testing.T) {
	for _, fact := range []string{"missing-list", "offline-missing-list", "malformed-list", "missing-download", "changed-download", "preparation-missing-download", "preparation-missing-download-cold"} {
		t.Run(fact, func(t *testing.T) {
			t.Parallel()
			const path, version = "example.com/Source/Dependency", "v1.2.3"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, version)
			app := filepath.Join(root, "app")
			writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire "+path+" "+version+"\n")
			env := isolatedGoEnvironment(t, proxy)
			runGo(t, app, env, "mod", "download", path+"@"+version)
			index, err := moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			original, exists := index.ByPath(path)
			if !exists || original.GoModChecksum() == "" {
				t.Fatal("missing original manifest checksum")
			}
			if fact == "changed-download" {
				var lines []string
				for _, line := range strings.Split(string(readFile(t, filepath.Join(app, "go.sum"))), "\n") {
					if strings.Contains(line, "/go.mod ") {
						lines = append(lines, line)
					}
				}
				writeFile(t, filepath.Join(app, "go.sum"), strings.Join(lines, "\n")+"\n")
			}
			before := readFile(t, filepath.Join(app, "go.sum"))
			if fact == "preparation-missing-download-cold" {
				relative, err := filepath.Rel(environmentValue(t, env, "GOMODCACHE"), original.SourceDirectory())
				if err != nil || relative == "." || !filepath.IsLocal(relative) {
					t.Fatal("source is outside the fixture module cache")
				}
				if err := os.RemoveAll(original.SourceDirectory()); err != nil {
					t.Fatal(err)
				}
			}
			realGo, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			env = append(env, "PLYSTRA_MODULE_DEPENDENCY_HELPER=checksum-facts", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_CHECKSUM_FACT="+fact)
			options := moduledependency.Options{Environment: env, GoCommand: os.Args[0], RequireSourceChecksums: true, Offline: fact == "offline-missing-list"}
			if strings.HasPrefix(fact, "preparation-missing-download") {
				warmCalls := 0
				err = moduledependency.PrepareSources(t.Context(), locate(t, app), []module.Version{{Path: path, Version: version}}, options, func(sources []moduledependency.Module) error {
					warmCalls++
					if len(sources) != 1 || len(sources[0].ProjectGoMod()) != 0 || sources[0].GoModChecksum() != original.GoModChecksum() || sources[0].Checksum() != original.Checksum() {
						t.Fatal("incomplete acquisition produced a completed or unverified source")
					}
					return nil
				})
				wantWarm := 1
				if strings.HasSuffix(fact, "-cold") {
					wantWarm = 0
				}
				if !errors.Is(err, moduledependency.ErrSourceIntegrity) || warmCalls != wantWarm {
					t.Fatalf("incomplete preparation checksum accepted: %v, warm callbacks=%d", err, warmCalls)
				}
			} else {
				index, err = moduledependency.Discover(t.Context(), locate(t, app), options)
				if fact == "missing-list" {
					selected, exists := index.ByPath(path)
					if err != nil || !exists || selected.GoModChecksum() != original.GoModChecksum() || selected.Checksum() != original.Checksum() {
						t.Fatalf("isolated manifest checksum fallback: %v, selected=%v", err, selected)
					}
				} else {
					cause := moduledependency.ErrInvalidOutput
					if fact == "offline-missing-list" {
						cause = moduledependency.ErrModuleUnavailable
					} else if fact == "changed-download" {
						cause = moduledependency.ErrConcurrentChange
					}
					if !errors.Is(err, moduledependency.ErrSourceIntegrity) || !errors.Is(err, cause) {
						t.Fatalf("invalid %s checksum evidence accepted: %v", fact, err)
					}
				}
			}
			if !bytes.Equal(before, readFile(t, filepath.Join(app, "go.sum"))) {
				t.Fatal("manifest checksum resolution changed Project sums")
			}
		})
	}
}

func runChecksumFactsHelper() int {
	command := exec.Command(os.Getenv("PLYSTRA_MODULE_REAL_GO"), os.Args[1:]...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return 9
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	encoder := json.NewEncoder(os.Stdout)
	for {
		var result map[string]any
		if err := decoder.Decode(&result); errors.Is(err, io.EOF) {
			return 0
		} else if err != nil {
			return 10
		}
		if result["Path"] == "example.com/Source/Dependency" {
			fact := os.Getenv("PLYSTRA_MODULE_CHECKSUM_FACT")
			if len(os.Args) > 1 && os.Args[1] == "list" {
				if fact == "malformed-list" {
					result["GoModSum"] = "h1:invalid"
				} else if fact != "changed-download" {
					delete(result, "GoModSum")
				}
			} else if fact == "missing-download" || strings.HasPrefix(fact, "preparation-missing-download") {
				delete(result, "GoModSum")
			} else if fact == "changed-download" {
				result["GoModSum"] = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			}
		}
		if err := encoder.Encode(result); err != nil {
			return 11
		}
	}
}
