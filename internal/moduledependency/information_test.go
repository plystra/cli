package moduledependency_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesRetainsModuleInformation(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"unchanged", "bytes", "identity", "removed", "mode", "missing", "no-authority", "no-tree", "partial", "no-manifest", "failed-warm", "failed-cold", "failed-callback", "offline", "noncanonical", "directory", "oversized", "callback-error", "callback-cancellation", "callback-changed-sums", "post-error", "post-cancellation"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", false)
				prefix := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")
				path := prefix + ".info"
				original := readFile(t, path)
				if mode == "missing" || mode == "failed-cold" || mode == "directory" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				switch mode {
				case "no-authority":
					writeFile(t, fixture.authority, "")
				case "no-tree":
					if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
						t.Fatal(err)
					}
				case "partial":
					writeFile(t, prefix+".partial", "")
				case "no-manifest":
					if err := os.Remove(fixture.downloaded.GoMod); err != nil {
						t.Fatal(err)
					}
				case "noncanonical":
					writeFile(t, path, string(original)+"\n")
				case "directory":
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "oversized":
					writeFile(t, path, strings.Repeat(" ", (1<<20)+1))
				}
				if strings.HasPrefix(mode, "failed-") {
					escaped, _ := module.EscapePath(warmSourceModule)
					for _, unavailable := range []string{prefix + ".zip", prefix + ".ziphash", filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")} {
						if err := os.Remove(unavailable); err != nil {
							t.Fatal(err)
						}
					}
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment, IncludeProjectReplacements: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				calls, early := 0, false
				rejected := errors.New("information retention rejected")
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{
					Environment: environment, GoCommand: command, Offline: mode == "offline",
					RetainModuleInformation: func(_ context.Context, paths []string) error {
						calls++
						if len(paths) != 1 || paths[0] != path {
							t.Fatalf("wrong effective information file: %v", paths)
						}
						beforeDownload := !strings.Contains(string(readFile(t, trace)), "download ")
						if calls == 1 {
							early = beforeDownload
						}
						if !beforeDownload && (mode == "post-error" || mode == "failed-callback") {
							return rejected
						}
						if mode == "callback-error" {
							return rejected
						}
						if mode == "callback-changed-sums" {
							sum := filepath.Join(stage.Root(), "go.sum")
							before, err := os.ReadFile(sum)
							if err != nil && !errors.Is(err, os.ErrNotExist) {
								t.Fatal(err)
							}
							writeFile(t, sum, string(before)+"\n")
							return rejected
						}
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if mode == "callback-cancellation" || mode == "post-cancellation" && !beforeDownload {
							cancel()
						}
						return nil
					},
				}, func(context.Context, []moduledependency.Module) error { return nil })
				commands := string(readFile(t, trace))
				switch {
				case mode == "post-error":
					if !errors.Is(err, rejected) || !strings.Contains(commands, "download ") {
						t.Fatalf("post-download rejection lost: %v", err)
					}
					return
				case mode == "post-cancellation":
					if !errors.Is(err, context.Canceled) || !strings.Contains(commands, "download ") {
						t.Fatalf("post-download cancellation lost: %v", err)
					}
					return
				case mode == "callback-error" || mode == "callback-changed-sums":
					if !errors.Is(err, rejected) || mode == "callback-changed-sums" && !errors.Is(err, moduledependency.ErrConcurrentChange) || strings.Contains(commands, "download ") {
						t.Fatalf("information callback failure lost or download started: %v, %s", err, commands)
					}
					return
				case mode == "callback-cancellation":
					if !errors.Is(err, context.Canceled) || strings.Contains(commands, "download ") {
						t.Fatalf("information callback cancellation lost: %v, %s", err, commands)
					}
					return
				case mode == "directory" || mode == "oversized":
					if err == nil || strings.Contains(commands, "download ") {
						t.Fatalf("unsafe information reached acquisition: %v, %s", err, commands)
					}
					return
				case mode == "noncanonical":
					if !errors.Is(err, projectstage.ErrConcurrentChange) || !early || calls < 2 {
						t.Fatalf("Go information rewrite refreshed original identity: %v, calls=%d", err, calls)
					}
					return
				case strings.HasPrefix(mode, "failed-"):
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) || mode == "failed-callback" && !errors.Is(err, rejected) {
						t.Fatalf("missing archive did not fail acquisition: %v", err)
					}
				default:
					if err != nil {
						t.Fatal(err)
					}
				}
				wantCalls := 2
				if phase == "workspace" {
					wantCalls++
				}
				if calls == 0 || mode != "missing" && mode != "failed-cold" && (!early || calls != wantCalls) {
					t.Fatalf("information retention boundary missing: calls=%d, early=%v", calls, early)
				}
				if (mode == "missing" || mode == "failed-cold") && phase == "module" && (early || calls != 1) {
					t.Fatalf("cold information captured before materialization: calls=%d, early=%v", calls, early)
				}
				inputs := stage.Inputs()
				writeFile(t, prefix+".unrelated.info", "unrelated cache metadata")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("unchanged information or unrelated sibling rejected: %v", err)
				}
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("cleanup discarded original information evidence: %v", err)
				}
				data := readFile(t, path)
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(info, info) {
					t.Fatal("cannot initialize original information identity")
				}
				switch mode {
				case "bytes":
					writeFile(t, path, strings.Replace(string(data), "2026", "2025", 1))
					if bytes.Equal(data, readFile(t, path)) {
						t.Fatal("information bytes unchanged")
					}
				case "removed":
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
				case "mode":
					if err := os.Chmod(path, 0o444); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				default:
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, string(data))
				}
				if mode != "removed" && mode != "mode" {
					if err := os.Chmod(path, info.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
					if mode != "bytes" {
						replacement, err := os.Stat(path)
						if err != nil || os.SameFile(info, replacement) || !bytes.Equal(data, readFile(t, path)) {
							t.Fatal("same-byte identity fixture failed")
						}
					}
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("retained information drift accepted after cleanup: %v", err)
				}
			})
		}
	}
}

func TestDiscoverRetainsFixedRootInformationBeforeListing(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"identity", "unchanged", "missing", "no-tree", "partial", "noncanonical", "rejected", "cancelled", "metadata-drift", "rejected-cancelled-drift", "selection-failure", "shared", "replacement-once", "exact-different", "unused", "directory", "no-authority", "known-go-version"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				project := filepath.Join(fixture.app, "go.mod")
				writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				replacements := project
				if workspace {
					replacements = fixture.work
				}
				switch mode {
				case "shared":
					writeFile(t, project, string(readFile(t, project))+"require example.com/shared v1.0.0\n")
					writeFile(t, replacements, string(readFile(t, replacements))+"replace example.com/shared => "+warmSourceModule+" v1.0.0\n")
				case "replacement-once":
					writeFile(t, replacements, string(readFile(t, replacements))+"replace "+warmSourceModule+" => example.com/unused v1.0.0\n")
				case "exact-different":
					writeFile(t, replacements, string(readFile(t, replacements))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				case "unused":
					writeFile(t, project, strings.ReplaceAll(string(readFile(t, project)), "require "+warmRequestedModule+" v1.0.0\n", ""))
				case "known-go-version":
					writeFile(t, project, string(readFile(t, project))+"require example.com/unavailable v1.0.0\n")
				}
				sums := ""
				if !workspace || mode == "known-go-version" {
					sums = warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
					if mode != "known-go-version" {
						if err := os.Remove(fixture.downloaded.GoMod); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "no-authority" {
					sums = ""
				}
				writeFile(t, fixture.authority, sums)
				path := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".info"
				switch mode {
				case "missing", "directory":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if mode == "directory" {
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				case "noncanonical":
					writeFile(t, path, string(readFile(t, path))+"\n")
				case "no-tree":
					relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
					if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
						t.Fatal("source removal escaped isolated cache")
					}
					if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
						t.Fatal(err)
					}
				case "partial":
					writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".partial", "")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				marker := filepath.Join(fixture.root, "information-replaced")
				if mode == "identity" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				} else if mode == "selection-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("fixed information rejected")
				calls, early := 0, false
				_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), moduledependency.Options{Environment: environment, GoCommand: command,
					RetainModuleInformation: func(ctx context.Context, paths []string) error {
						calls++
						if len(paths) != 1 || paths[0] != path {
							t.Fatal("fixed information lost its effective identity")
						}
						early = early || !strings.Contains(string(readFile(t, trace)), "list -m ")
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: path}}); err != nil {
							return err
						}
						if mode == "metadata-drift" || mode == "rejected-cancelled-drift" {
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						}
						if mode == "cancelled" || mode == "rejected-cancelled-drift" {
							cancel()
						}
						if mode == "rejected" || mode == "rejected-cancelled-drift" || mode == "known-go-version" || mode == "no-authority" {
							return rejected
						}
						return nil
					},
				})
				commands := string(readFile(t, trace))
				switch mode {
				case "identity":
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("real Go listing did not exercise information replacement")
					}
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("fixed information replacement was accepted: %v", err)
					}
				case "directory":
					if err == nil || calls != 0 || strings.Contains(commands, "list -m ") {
						t.Fatalf("unsafe fixed information reached listing: %v", err)
					}
					return
				case "selection-failure":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("failed listing lost its original error: %v", err)
					}
				case "noncanonical":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("Go information rewrite refreshed the original snapshot: %v", err)
					}
				case "rejected", "known-go-version", "no-authority", "cancelled", "metadata-drift", "rejected-cancelled-drift":
					if (mode == "rejected" || mode == "known-go-version" || mode == "no-authority" || mode == "rejected-cancelled-drift") && !errors.Is(err, rejected) ||
						(mode == "cancelled" || mode == "rejected-cancelled-drift") && !errors.Is(err, context.Canceled) ||
						(mode == "metadata-drift" || mode == "rejected-cancelled-drift") && !errors.Is(err, moduledependency.ErrConcurrentChange) || strings.Contains(commands, "list -m ") {
						t.Fatalf("failed fixed information retention reached listing or lost errors: %v", err)
					}
				case "shared":
					// Go may reject two logical paths using the same physical module.
				default:
					if err != nil {
						t.Fatalf("unchanged fixed information failed: calls=%d error=%v", calls, err)
					}
				}
				wantEarly := mode != "missing" && mode != "unused" && mode != "exact-different"
				if early != wantEarly || calls > 1 || wantEarly && calls != 1 {
					t.Fatalf("fixed information retention boundary: calls=%d early=%t want=%t", calls, early, wantEarly)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "identity" || mode == "noncanonical" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("information drift was discarded after cleanup")
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("fixed information evidence was lost after cleanup", err)
				}
				writeFile(t, path, "changed after fixed information cleanup\n")
				if calls != 0 && !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) || calls == 0 && inputs.Recheck(t.Context()) != nil {
					t.Fatal("fixed information capture disagreed with consumed paths")
				}
			})
		}
	}
}

func TestPrepareSourcesInformationExcludesWorkspaceLocalSources(t *testing.T) {
	for _, mode := range []string{"workfile-local", "member-local", "workfile-exact-local", "member-exact-local", "workfile-different-local", "member-different-local", "local-proof-budget", "mixed", "workspace-main"} {
		for _, manifests := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/manifests=%t", mode, manifests), func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, true, false)
				work := "go 1.26\nuse (\n ./app\n ./member\n)\n"
				requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}
				switch mode {
				case "workfile-local", "workfile-exact-local", "workfile-different-local", "local-proof-budget", "mixed":
					old := warmRequestedModule
					if mode == "workfile-exact-local" {
						old += " v1.0.0"
						manifest := filepath.Join(fixture.app, "go.mod")
						writeFile(t, manifest, strings.Replace(string(readFile(t, manifest)), "replace "+warmRequestedModule+" =>", "replace "+old+" =>", 1))
					}
					work += "replace " + old + " => ./local\n"
					if mode == "workfile-different-local" {
						work += "replace " + warmRequestedModule + " v1.1.0 => ./other-local\n"
					}
					if mode == "local-proof-budget" {
						for index := range 256 {
							work += fmt.Sprintf("replace %s v1.1.%d => ./other-local\n", warmRequestedModule, index)
						}
					}
					if mode == "mixed" {
						const other = "example.com/other"
						writeModule(t, filepath.Join(fixture.root, "other"), other)
						manifest := filepath.Join(fixture.app, "go.mod")
						writeFile(t, manifest, string(readFile(t, manifest))+"replace "+other+" => ../other\n")
						work += "replace " + other + " => " + warmSourceModule + " v1.0.0\n"
						requested = append(requested, module.Version{Path: other, Version: "v1.0.0"})
					}
				case "member-different-local":
					manifest := filepath.Join(fixture.app, "go.mod")
					writeFile(t, manifest, string(readFile(t, manifest))+"replace "+warmRequestedModule+" v1.1.0 => ../other-local\n")
				case "member-exact-local":
					manifest := filepath.Join(fixture.app, "go.mod")
					writeFile(t, manifest, strings.Replace(string(readFile(t, manifest)), "replace "+warmRequestedModule+" =>", "replace "+warmRequestedModule+" v1.0.0 =>", 1))
				case "workspace-main":
					manifest := filepath.Join(fixture.app, "go.mod")
					writeFile(t, manifest, string(readFile(t, manifest))+"replace example.com/member => ../member\n")
					requested = []module.Version{{Path: "example.com/member", Version: "v1.0.0"}}
				}
				writeFile(t, fixture.work, work)
				command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
				calls := 0
				options := moduledependency.Options{
					Environment: environment, GoCommand: command,
					RetainModuleInformation: func(_ context.Context, paths []string) error {
						calls++
						if mode != "mixed" || len(paths) != 1 || paths[0] != strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info" {
							t.Fatalf("local source entered information inventory: %v", paths)
						}
						return nil
					},
				}
				if manifests {
					options.RetainRootManifests = func(_ context.Context, roots []moduledependency.RootManifest) error {
						if mode != "mixed" && len(roots) != 0 {
							t.Fatal("local-only request captured cache manifests")
						}
						return nil
					}
				}
				err := moduledependency.PrepareSources(t.Context(), locate(t, fixture.app), requested, options, func(context.Context, []moduledependency.Module) error { return nil })
				if err != nil || mode == "mixed" && calls != 3 || mode != "mixed" && calls != 0 {
					t.Fatalf("workspace source kind changed preparation: %v, callbacks=%d", err, calls)
				}
				if mode != "mixed" && strings.Contains(string(readFile(t, trace)), "GOMODCACHE") {
					t.Fatal("local-only preparation probed the module cache")
				}
			})
		}
	}
}
