package moduledependency_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestDiscoverRetainsEffectiveRootManifestsBeforeListing(t *testing.T) {
	for _, mode := range []string{"warm", "manifest-only", "missing-manifest", "missing-checksum", "replacement", "exact-replacement", "reverse-exact-replacement", "local", "excluded", "callback-error", "changed-sums", "changed-module", "workspace", "ignored-empty-checksum", "offline"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const path, version = "example.com/Source/Dependency", "v1.2.3"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, path, version)
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\n\ngo 1.26\nrequire " + path + " " + version + "\n"
			if strings.Contains(mode, "replacement") {
				goMod = "module example.com/app\n\ngo 1.26\nrequire example.com/original v1.0.0\nreplace example.com/original => " + path + " " + version + "\n"
				if strings.Contains(mode, "exact-replacement") {
					goMod = strings.ReplaceAll(goMod, "replace example.com/original =>", "replace example.com/original v1.0.0 =>")
					wildcard := "replace example.com/original => example.com/unavailable v1.0.0\n"
					if mode == "reverse-exact-replacement" {
						goMod = wildcard + goMod
					} else {
						goMod += wildcard
					}
				}
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			env := isolatedGoEnvironment(t, proxy)
			runGo(t, app, env, "mod", "download", path+"@"+version)
			var expectedChecksum string
			for _, line := range strings.Split(string(readFile(t, filepath.Join(app, "go.sum"))), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 3 && fields[0] == path && fields[1] == version+"/go.mod" {
					expectedChecksum = fields[2]
				}
			}
			if expectedChecksum == "" {
				t.Fatal("fixture has no independent manifest checksum")
			}
			cache := environmentValue(t, env, "GOMODCACHE")
			escaped, _ := module.EscapePath(path)
			cacheBase := filepath.Join(cache, "cache", "download", filepath.FromSlash(escaped), "@v", version)
			manifestPath := cacheBase + ".mod"
			if mode == "local" {
				writeModule(t, filepath.Join(root, "local"), path)
				goMod += "replace " + path + " => ../local\n"
			} else if mode == "excluded" {
				goMod += "exclude " + path + " " + version + "\n"
			} else if mode == "manifest-only" {
				if err := os.RemoveAll(filepath.Join(cache, filepath.FromSlash(escaped)+"@"+version)); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(cacheBase + ".zip"); err != nil {
					t.Fatal(err)
				}
			} else if mode == "missing-manifest" {
				if err := os.Remove(manifestPath); err != nil {
					t.Fatal(err)
				}
			} else if mode == "missing-checksum" {
				var archiveRows []string
				for _, line := range strings.Split(string(readFile(t, filepath.Join(app, "go.sum"))), "\n") {
					if line != "" && !strings.Contains(line, "/go.mod ") {
						archiveRows = append(archiveRows, line)
					}
				}
				writeFile(t, filepath.Join(app, "go.sum"), strings.Join(archiveRows, "\n")+"\n")
			} else if mode == "workspace" {
				work := filepath.Join(root, "go.work")
				writeFile(t, work, "go 1.26\nuse ./app\n")
				env = append(env, "GOWORK="+work)
			} else if mode == "ignored-empty-checksum" {
				writeFile(t, filepath.Join(app, "go.sum"), path+" "+version+"/go.mod h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY=\n"+string(readFile(t, filepath.Join(app, "go.sum"))))
			} else if mode == "offline" {
				env = append(env, "GOTOOLCHAIN=auto", "PLYSTRA_MODULE_EXPECT_OFFLINE=1")
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			originalSums := readFile(t, filepath.Join(app, "go.sum"))
			realGo, err := exec.LookPath("go")
			if err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(root, "commands")
			env = append(env, "PLYSTRA_MODULE_DEPENDENCY_HELPER=root-manifests", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_COMMAND_TRACE="+trace)
			called := false
			rejected := errors.New("root manifest rejected")
			index, err := moduledependency.Discover(t.Context(), locate(t, app), moduledependency.Options{Environment: env, GoCommand: os.Args[0], Offline: mode == "offline", RetainRootManifests: func(manifests []moduledependency.RootManifest) error {
				called = true
				if len(manifests) != 1 || manifests[0].Path != manifestPath || manifests[0].Checksum != expectedChecksum {
					t.Fatalf("wrong effective root manifest: %v", manifests)
				}
				if commands := string(readFile(t, trace)); strings.Contains(commands, "list ") || strings.Contains(commands, "download ") {
					t.Fatalf("root capture happened after resolution: %s", commands)
				}
				if mode == "manifest-only" {
					if _, err := os.Stat(cacheBase + ".zip"); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("root retention prefetched an archive")
					}
				}
				if mode == "callback-error" {
					return rejected
				}
				if mode == "changed-sums" {
					writeFile(t, filepath.Join(app, "go.sum"), string(originalSums)+"\n")
				} else if mode == "changed-module" {
					writeFile(t, filepath.Join(app, "go.mod"), goMod+"\n")
				}
				return nil
			}})
			if mode == "callback-error" || strings.HasPrefix(mode, "changed-") {
				if mode == "callback-error" && !errors.Is(err, rejected) || strings.HasPrefix(mode, "changed-") && !errors.Is(err, moduledependency.ErrConcurrentChange) || len(index.Modules()) != 0 {
					t.Fatalf("invalid capture accepted: %v", err)
				}
				if commands := string(readFile(t, trace)); strings.Contains(commands, "list ") {
					t.Fatalf("graph listing followed a failed capture: %s", commands)
				}
			} else if mode == "excluded" || mode == "missing-checksum" {
				if err == nil {
					t.Fatal("ordinary Go accepted an excluded requirement or missing checksum")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantCall := mode != "local" && mode != "excluded" && mode != "missing-manifest" && mode != "missing-checksum"
			if called != wantCall {
				t.Fatalf("root capture callback=%v, want %v", called, wantCall)
			}
			if mode != "changed-module" && string(readFile(t, filepath.Join(app, "go.mod"))) != goMod || mode != "changed-sums" && !bytes.Equal(readFile(t, filepath.Join(app, "go.sum")), originalSums) {
				t.Fatal("root retention changed Project metadata")
			}
		})
	}
}

func runRootManifestsHelper() int {
	if os.Getenv("PLYSTRA_MODULE_EXPECT_OFFLINE") != "" && (os.Getenv("GOPROXY") != "off" || os.Getenv("GOSUMDB") != "off" || os.Getenv("GOTOOLCHAIN") != "local") {
		return 12
	}
	file, err := os.OpenFile(os.Getenv("PLYSTRA_MODULE_COMMAND_TRACE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 9
	}
	_, err = file.WriteString(strings.Join(os.Args[1:], " ") + "\n")
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return 10
	}
	command := exec.Command(os.Getenv("PLYSTRA_MODULE_REAL_GO"), os.Args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		if exited, ok := err.(*exec.ExitError); ok {
			return exited.ExitCode()
		}
		return 11
	}
	if path := os.Getenv("PLYSTRA_MODULE_METADATA_PATH"); path != "" && len(os.Args) > 1 && os.Args[1] == "list" {
		if os.Getenv("PLYSTRA_MODULE_METADATA_MODE") == "create" {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return 13
			}
			if err := os.WriteFile(path, []byte{'\n'}, 0o600); err != nil {
				return 14
			}
		} else {
			data, err := os.ReadFile(path)
			info, statErr := os.Stat(path)
			if err != nil || statErr != nil || !os.SameFile(info, info) {
				return 15
			}
			if err := os.Rename(path, path+".prior"); err != nil {
				return 16
			}
			if err := os.WriteFile(path, data, info.Mode()); err != nil {
				return 17
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				return 18
			}
			if err := os.Remove(path + ".prior"); err != nil {
				return 19
			}
		}
		if err := os.WriteFile(os.Getenv("PLYSTRA_MODULE_METADATA_MARKER"), []byte("metadata changed after module list\n"), 0o600); err != nil {
			return 20
		}
	}
	return 0
}

func TestRootManifestRetentionSurvivesRejectedMVSUpgradeAndStageCleanup(t *testing.T) {
	testRootManifestRetentionAcrossMVSAndCleanup(t, false)
}

func TestWorkspaceRootManifestRetentionSurvivesMVSUpgradeAndStageCleanup(t *testing.T) {
	testRootManifestRetentionAcrossMVSAndCleanup(t, true)
}

func testRootManifestRetentionAcrossMVSAndCleanup(t *testing.T, workspace bool) {
	t.Helper()
	const first, second = "example.com/Source/Dependency", "example.com/root"
	root := t.TempDir()
	proxy := writeModuleProxy(t, root, first, "v1.0.0", "module "+first+"\ngo 1.16\n")
	writeModuleProxy(t, root, first, "v1.1.0", "module "+first+"\ngo 1.16\n")
	writeModuleProxy(t, root, second, "v1.0.0", "module "+second+"\ngo 1.16\nrequire "+first+" v1.1.0\n")
	app := filepath.Join(root, "app")
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.16\nrequire (\n"+first+" v1.0.0\n"+second+" v1.0.0\n)\n")
	writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
	env := isolatedGoEnvironment(t, proxy)
	runGo(t, app, env, "mod", "download", first+"@v1.0.0", first+"@v1.1.0", second+"@v1.0.0")
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: env}, "mod", "download", "-json", first+"@v1.1.0")
	var downloaded struct{ Sum, GoModSum string }
	if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
		t.Fatalf("prepare selected-version checksum authority: %v", err)
	}
	sums := readFile(t, filepath.Join(app, "go.sum"))
	writeFile(t, filepath.Join(app, "go.sum"), string(sums)+first+" v1.1.0 "+downloaded.Sum+"\n"+first+" v1.1.0/go.mod "+downloaded.GoModSum+"\n")
	if workspace {
		work := filepath.Join(root, "go.work")
		writeFile(t, work, "go 1.26\nuse ./app\n")
		env = append(env, "GOWORK="+work)
	}
	cache := environmentValue(t, env, "GOMODCACHE")
	escaped, _ := module.EscapePath(first)
	oldBase := filepath.Join(cache, "cache", "download", filepath.FromSlash(escaped), "@v", "v1.0.0")
	if err := os.RemoveAll(filepath.Join(cache, filepath.FromSlash(escaped)+"@v1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldBase + ".zip"); err != nil {
		t.Fatal(err)
	}
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	})
	var retained []moduledependency.RootManifest
	index, err := moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{Environment: stage.Environment(), RetainRootManifests: func(manifests []moduledependency.RootManifest) error {
		retained = append(retained, manifests...)
		var inputs []projectstage.MetadataInput
		for _, manifest := range manifests {
			inputs = append(inputs, projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum})
		}
		return stage.RetainMetadata(t.Context(), inputs)
	}})
	if workspace {
		selected, ok := index.ByPath(first)
		if err != nil || !ok || selected.SelectedVersion() != "v1.1.0" || len(retained) != 2 {
			t.Fatalf("workspace MVS result or retained roots: %v, %v, %v", err, index.Modules(), retained)
		}
	} else if !errors.Is(err, moduledependency.ErrDiscover) || !strings.Contains(err.Error(), "updates to go.mod needed") || len(index.Modules()) != 0 || len(retained) != 2 {
		t.Fatalf("unexpected readonly MVS result or retained roots: %v, %v, %v", err, index.Modules(), retained)
	}
	found := false
	for _, manifest := range retained {
		found = found || manifest.Path == oldBase+".mod"
	}
	if !found {
		t.Fatal("MVS discarded the original root manifest evidence")
	}
	if _, err := os.Stat(oldBase + ".zip"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("root capture downloaded the unselected archive")
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(oldBase), "v9.0.0.mod")
	writeFile(t, unrelated, "unrelated cache file")
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal("unrelated metadata invalidated root evidence")
	}
	path := oldBase + ".mod"
	data := readFile(t, path)
	info, err := os.Stat(path)
	if err != nil || !os.SameFile(info, info) {
		t.Fatal("cannot retain the original manifest identity")
	}
	if err := os.Rename(path, path+".prior"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
	if err := os.Chmod(path, info.Mode()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("unselected root identity replacement survived cleanup: %v", err)
	}
}
