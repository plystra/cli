package moduledependency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/module"
)

const preparedOriginal = "example.com/original"
const preparedReplacement = "example.com/replacement"

func preparationWorkspace(t *testing.T) (string, []string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	proxy := writeModuleProxy(t, root, preparedOriginal, "v1.0.0")
	writeModuleProxy(t, root, preparedReplacement, "v1.1.0")
	app, sibling := filepath.Join(root, "app"), filepath.Join(root, "sibling")
	writeModule(t, app, "example.com/app")
	writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
	writeModule(t, sibling, "example.com/sibling")
	paths := map[string]string{"project": filepath.Join(app, "go.mod"), "project-sum": filepath.Join(app, "go.sum"),
		"member": filepath.Join(sibling, "go.mod"), "member-sum": filepath.Join(sibling, "go.sum"),
		"workspace": filepath.Join(root, "selected.work"), "workspace-sum": filepath.Join(root, "selected.work.sum")}
	writeFile(t, paths["workspace"], "go 1.26\nuse (\n ./app\n ./sibling\n)\nreplace "+preparedOriginal+" => "+preparedReplacement+" v1.1.0\n")
	env := isolatedGoEnvironment(t, proxy)
	for index, entry := range env {
		if strings.HasPrefix(entry, "GOWORK=") {
			env[index] = "GOWORK=" + paths["workspace"]
		}
	}
	return app, env, paths
}

func TestPrepareSourcesPreservesWorkspaceChecksumAuthorities(t *testing.T) {
	for _, mode := range []string{"cold", "member", "member-conflict", "member-conflict-with-match", "primary-match", "primary-conflict", "primary-match-first", "primary-conflict-first", "manifest-conflict", "malformed-member", "initial-conflict", "offline", "offline-missing-workspace-sum"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			app, env, paths := preparationWorkspace(t)
			const bad = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			if mode != "cold" {
				sums := map[string]string{}
				for _, query := range []string{preparedOriginal + "@v1.0.0", preparedReplacement + "@v1.1.0"} {
					warmEnvironment := append(withoutEnvironmentKey(env, "GOWORK"), "GOWORK=off")
					output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: warmEnvironment}, "mod", "download", "-json", query)
					if err != nil {
						t.Fatal(err)
					}
					var downloaded struct{ Path, Version, Sum, GoModSum string }
					if err := json.Unmarshal(output, &downloaded); err != nil {
						t.Fatal(err)
					}
					sums[downloaded.Path] = fmt.Sprintf("%s %s %s\n", downloaded.Path, downloaded.Version, downloaded.Sum)
					sums[downloaded.Path+"/go.mod"] = fmt.Sprintf("%s %s/go.mod %s\n", downloaded.Path, downloaded.Version, downloaded.GoModSum)
				}
				good := sums[preparedReplacement]
				conflict := preparedReplacement + " v1.1.0 " + bad + "\n"
				writeFile(t, paths["project-sum"], sums[preparedOriginal])
				switch mode {
				case "member", "offline":
					writeFile(t, paths["member-sum"], good)
				case "member-conflict", "primary-match", "primary-match-first":
					writeFile(t, paths["member-sum"], conflict)
					if mode == "primary-match" {
						writeFile(t, paths["project-sum"], sums[preparedOriginal]+conflict)
					}
				case "member-conflict-with-match":
					writeFile(t, paths["member-sum"], conflict)
					writeFile(t, paths["project-sum"], sums[preparedOriginal]+good)
				case "primary-conflict", "primary-conflict-first":
					writeFile(t, paths["member-sum"], good)
				case "manifest-conflict":
					writeFile(t, paths["member-sum"], good+preparedReplacement+" v1.1.0/go.mod "+bad+"\n")
				case "malformed-member":
					writeFile(t, paths["member-sum"], "malformed row\n")
				case "initial-conflict":
					writeFile(t, paths["project-sum"], preparedOriginal+" v1.0.0 "+bad+"\n")
					writeFile(t, paths["workspace-sum"], sums[preparedOriginal]+good)
				}
				switch mode {
				case "primary-match", "malformed-member":
					writeFile(t, paths["workspace-sum"], good+sums[preparedReplacement+"/go.mod"])
				case "primary-conflict":
					writeFile(t, paths["workspace-sum"], conflict)
				case "primary-match-first":
					writeFile(t, paths["workspace-sum"], good+conflict)
				case "primary-conflict-first":
					writeFile(t, paths["workspace-sum"], conflict+good)
				}
			}
			before := map[string][]byte{}
			for name, path := range paths {
				before[name], _ = os.ReadFile(path)
			}
			command, env, trace := tracePreparation(t, filepath.Dir(app), env)
			var prepared []moduledependency.Module
			completed := map[module.Version]int{}
			err := moduledependency.PrepareSources(t.Context(), locate(t, app), []module.Version{{Path: preparedOriginal, Version: "v1.0.0"}}, moduledependency.Options{Environment: env, GoCommand: command, Offline: strings.HasPrefix(mode, "offline")}, func(_ context.Context, sources []moduledependency.Module) error {
				for _, source := range sources {
					version := module.Version{Path: source.Path(), Version: source.SelectedVersion()}
					if replacement, ok := source.Replacement(); ok {
						version = module.Version{Path: replacement.Path(), Version: replacement.Version()}
					}
					downloads := strings.Count(string(readFile(t, trace)), "mod download -json "+version.String()+"\n")
					if downloads > completed[version] {
						if downloads != completed[version]+1 || len(source.ProjectGoMod()) != 0 {
							t.Fatal("acquired-source retention crossed its validation boundary")
						}
						completed[version] = downloads
						prepared = append(prepared, source)
					}
				}
				return nil
			})
			wantFailure := strings.Contains(mode, "conflict") || mode == "malformed-member" || mode == "offline-missing-workspace-sum"
			if wantFailure {
				if err == nil {
					t.Fatal("invalid checksum authority accepted")
				}
			} else if err != nil || len(prepared) != 2 {
				t.Fatalf("prepare both source phases: %v, sources=%v", err, prepared)
			} else if replacement, _ := prepared[1].Replacement(); replacement.Path() != preparedReplacement || replacement.Version() != "v1.1.0" {
				t.Fatal("workspace replacement was not captured")
			}
			for name, path := range paths {
				after, readErr := os.ReadFile(path)
				if !bytes.Equal(before[name], after) || before[name] == nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("preparation changed %s", name)
				}
			}
			if mode != "initial-conflict" {
				writeFile(t, paths["project"], string(readFile(t, paths["project"]))+"require "+preparedOriginal+" v1.0.0\n")
				project, err := projectlocate.Find(app)
				if err != nil {
					t.Fatal(err)
				}
				_, err = moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: env, Offline: strings.HasPrefix(mode, "offline"), RequireSourceChecksums: true})
				if wantFailure != (err != nil) {
					t.Fatalf("post-resolution checksum authority changed: %v", err)
				}
				for name, path := range paths {
					if name == "project" {
						continue
					}
					after, readErr := os.ReadFile(path)
					if !bytes.Equal(before[name], after) || before[name] == nil && !errors.Is(readErr, os.ErrNotExist) {
						t.Fatalf("read-only discovery changed %s", name)
					}
				}
			}
		})
	}
}

func TestPrepareSourcesUsesGoWorkspaceReplacementSelection(t *testing.T) {
	for _, mode := range []string{"member", "exact", "reverse-exact", "workfile-override", "local", "workspace-member"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			app, env, paths := preparationWorkspace(t)
			work := string(readFile(t, paths["workspace"]))
			replacement := "replace " + preparedOriginal + " => " + preparedReplacement + " v1.1.0\n"
			switch mode {
			case "member":
				work = strings.ReplaceAll(work, replacement, "")
				writeFile(t, paths["member"], string(readFile(t, paths["member"]))+replacement)
			case "exact", "reverse-exact":
				exact := strings.Replace(replacement, preparedOriginal+" =>", preparedOriginal+" v1.0.0 =>", 1)
				wildcard := "replace " + preparedOriginal + " => example.com/unavailable v1.0.0\n"
				ordered := exact + wildcard
				if mode == "reverse-exact" {
					ordered = wildcard + exact
				}
				work = strings.ReplaceAll(work, replacement, ordered)
			case "workfile-override":
				writeFile(t, paths["member"], string(readFile(t, paths["member"]))+"replace "+preparedOriginal+" => example.com/unavailable v1.0.0\n")
			case "local":
				writeModule(t, filepath.Join(filepath.Dir(paths["workspace"]), "local"), preparedOriginal)
				work = strings.ReplaceAll(work, replacement, "replace "+preparedOriginal+" => ./local\n")
			case "workspace-member":
				work = strings.ReplaceAll(work, replacement, "")
				writeFile(t, paths["member"], "module "+preparedOriginal+"\n\ngo 1.26\n")
			}
			writeFile(t, paths["workspace"], work)
			var prepared []moduledependency.Module
			err := moduledependency.PrepareSources(t.Context(), locate(t, app), []module.Version{{Path: preparedOriginal, Version: "v1.0.0"}}, moduledependency.Options{Environment: env}, func(_ context.Context, sources []moduledependency.Module) error {
				prepared = append(prepared, sources...)
				return nil
			})
			want := 2
			if mode == "local" || mode == "workspace-member" {
				want = 1
			}
			if err != nil || len(prepared) != want {
				t.Fatalf("Go workspace selection rejected: %v, prepared=%v", err, prepared)
			}
			if replacement, _ := prepared[0].Replacement(); replacement.Path() != "" || prepared[0].SelectedVersion() != "v1.0.0" {
				t.Fatal("workspace selection erased the initial module source")
			}
			if want == 2 {
				if replacement, _ := prepared[1].Replacement(); replacement.Path() != preparedReplacement || replacement.Version() != "v1.1.0" {
					t.Fatal("incorrect workspace replacement")
				}
			}
		})
	}
}

func TestPrepareSourcesRechecksWorkspaceMetadataAfterRetention(t *testing.T) {
	for _, downloaded := range []bool{false, true} {
		for _, name := range []string{"project", "project-sum", "member", "member-sum", "workspace", "workspace-sum"} {
			for _, mutation := range []string{"bytes", "identity", "remove", "create"} {
				if mutation == "create" && !strings.HasSuffix(name, "sum") {
					continue
				}
				t.Run(fmt.Sprintf("%t/%s/%s", downloaded, name, mutation), func(t *testing.T) {
					t.Parallel()
					app, env, paths := preparationWorkspace(t)
					path := paths[name]
					if strings.HasSuffix(name, "sum") && mutation != "create" {
						writeFile(t, path, "")
					}
					var requested []module.Version
					if downloaded {
						requested = []module.Version{{Path: preparedOriginal, Version: "v1.0.0"}}
					}
					calls := 0
					err := moduledependency.PrepareSources(t.Context(), locate(t, app), requested, moduledependency.Options{Environment: env}, func(context.Context, []moduledependency.Module) error {
						calls++
						if calls > 1 {
							return nil
						}
						if mutation == "create" {
							writeFile(t, path, "")
							return nil
						}
						info, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						data := readFile(t, path)
						switch mutation {
						case "bytes":
							writeFile(t, path, string(data)+"\n")
						case "identity":
							if err := os.Rename(path, path+".saved"); err != nil {
								t.Fatal(err)
							}
							writeFile(t, path, string(data))
						case "remove":
							if err := os.Remove(path); err != nil {
								t.Fatal(err)
							}
							return nil
						}
						if err := os.Chmod(path, info.Mode()); err != nil {
							t.Fatal(err)
						}
						if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
							t.Fatal(err)
						}
						return nil
					})
					if calls == 0 || !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("changed %s metadata accepted: %v, retain calls=%d", name, err, calls)
					}
					if mutation == "remove" {
						if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("preparation restored removed metadata")
						}
					} else if _, err := os.Stat(path); err != nil {
						t.Fatal("preparation removed concurrent metadata")
					}
				})
			}
		}
	}
}

func TestDiscoverWorkspaceChecksumFieldsRemainStable(t *testing.T) {
	app, env, paths := preparationWorkspace(t)
	writeFile(t, paths["project"], string(readFile(t, paths["project"]))+"require "+preparedOriginal+" v1.0.0\n")
	project, err := projectlocate.Find(app)
	if err != nil {
		t.Fatal(err)
	}
	first, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	before, ok := first.ByPath(preparedOriginal)
	if !ok || before.Checksum() == "" || before.GoModChecksum() == "" {
		t.Fatal("cold workspace discovery returned incomplete checksum identity")
	}
	writeFile(t, paths["workspace-sum"], fmt.Sprintf("%s v1.1.0 %s\n%s v1.1.0/go.mod %s\n", preparedReplacement, before.Checksum(), preparedReplacement, before.GoModChecksum()))
	second, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	after, ok := second.ByPath(preparedOriginal)
	if !ok || before.Checksum() != after.Checksum() || before.GoModChecksum() != after.GoModChecksum() {
		t.Fatal("checksum materialization changed an unchanged effective module graph")
	}
}
