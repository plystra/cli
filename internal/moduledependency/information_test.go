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
				}, func([]moduledependency.Module) error { return nil })
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
				err := moduledependency.PrepareSources(t.Context(), locate(t, fixture.app), requested, options, func([]moduledependency.Module) error { return nil })
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
