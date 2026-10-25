package moduledependency_test

import (
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

func TestPendingSourcesRetainedBeforeSelection(t *testing.T) {
	for _, phase := range []string{"module", "workspace", "prepare-fixed", "prepare-inferred", "module-fixed", "workspace-fixed"} {
		modes := []string{"source", "source-manifest", "unchanged", "selection-failure", "rejected", "cancelled", "metadata-drift", "rejected-cancelled-drift", "partial", "missing-tree", "legacy"}
		if !strings.HasPrefix(phase, "prepare-") {
			modes = append(modes, "trees-only")
		} else if phase == "prepare-fixed" {
			modes = append(modes, "no-sums")
		} else {
			modes = append(modes, "unrecorded-graph")
		}
		if phase == "workspace-fixed" {
			modes = append(modes, "mvs-upgrade")
		}
		for _, mode := range modes {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				preparing := strings.HasPrefix(phase, "prepare-")
				fixture := warmPreparation(t, phase != "module" && phase != "module-fixed", mode == "legacy")
				if !preparing {
					path := filepath.Join(fixture.app, "go.mod")
					writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.0.0\n")
				} else if phase == "prepare-inferred" {
					work := string(readFile(t, fixture.work))
					writeFile(t, fixture.work, strings.Replace(work, "replace "+warmRequestedModule+" =>", "replace "+warmRequestedModule+" v1.0.0 =>", 1))
				}
				sums := warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
				if mode == "no-sums" || mode == "unrecorded-graph" || phase == "workspace-fixed" {
					sums = ""
				}
				writeFile(t, fixture.authority, sums)
				if mode == "mvs-upgrade" {
					path := filepath.Join(fixture.root, "member", "go.mod")
					writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.1.0\n")
				}
				if phase == "module-fixed" {
					if err := os.Remove(fixture.downloaded.GoMod); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "partial" {
					writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".partial", "partial extraction\n")
				} else if mode == "missing-tree" {
					relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
					if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
						t.Fatal("missing-tree fixture escaped the isolated cache")
					}
					if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
						t.Fatal(err)
					}
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
				if mode == "source-manifest" {
					path = filepath.Join(fixture.downloaded.Dir, "go.mod")
				}
				marker := filepath.Join(fixture.root, "changed-during-selection")
				if mode == "source" || mode == "source-manifest" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				} else if mode == "selection-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("pending selection source rejected")
				early := 0
				authenticated := false
				options := moduledependency.Options{Environment: environment, GoCommand: command, RequireSourceChecksums: true,
					RetainSourceTrees: func(retainCtx context.Context, directories []string) error {
						if len(directories) != 1 || directories[0] != fixture.downloaded.Dir {
							t.Fatal("pending selection captured an unrelated source")
						}
						if !strings.Contains(string(readFile(t, trace)), "list -m ") {
							early++
						}
						if err := stage.RetainSourceTrees(retainCtx, directories); err != nil {
							return err
						}
						if !authenticated && !errors.Is(stage.CheckSources(retainCtx), projectstage.ErrSourceIntegrity) {
							t.Fatal("pending selection evidence authenticated itself")
						}
						switch mode {
						case "rejected":
							return rejected
						case "cancelled":
							cancel()
						case "metadata-drift":
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						case "rejected-cancelled-drift":
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
							cancel()
							return rejected
						}
						return nil
					},
				}
				retain := func(_ context.Context, sources []moduledependency.Module) error {
					err := retainWarmBundle(t, stage, ctx, sources)
					if err == nil && len(sources) != 0 {
						authenticated = true
					}
					return err
				}
				if preparing {
					err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options, retain)
				} else {
					if mode != "trees-only" {
						options.RetainSelectedSources = retain
					}
					var index moduledependency.Index
					index, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
					if err == nil {
						source, exists := index.ByPath(warmRequestedModule)
						if !exists || source.Checksum() != fixture.downloaded.Sum {
							t.Fatal("selected source lost its independently resolved checksum")
						}
						if mode == "mvs-upgrade" && source.SelectedVersion() != "v1.1.0" {
							t.Fatal("fixed-source retention replaced actual Go version selection")
						}
						err = retain(ctx, []moduledependency.Module{source})
					}
				}
				switch mode {
				case "source", "source-manifest":
					if !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("selection accepted an identical-byte source replacement: %v", err)
					}
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("real Go selection did not exercise source replacement")
					}
				case "unchanged", "trees-only", "no-sums", "partial", "missing-tree", "legacy", "unrecorded-graph", "mvs-upgrade":
					if err != nil || stage.CheckSources(t.Context()) != nil {
						t.Fatalf("unchanged selection failed to authenticate the original tree: %v", err)
					}
				case "selection-failure":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) || !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
						t.Fatalf("failed selection lost pending source evidence: %v", err)
					}
				default:
					want := rejected
					if mode == "cancelled" {
						want = context.Canceled
					} else if mode == "metadata-drift" {
						want = moduledependency.ErrConcurrentChange
					}
					if !errors.Is(err, want) || strings.Contains(string(readFile(t, trace)), "list -m ") {
						t.Fatalf("failed pending capture reached selection: %v", err)
					}
					if mode == "rejected-cancelled-drift" && (!errors.Is(err, context.Canceled) || !errors.Is(err, moduledependency.ErrConcurrentChange)) {
						t.Fatalf("pending capture discarded cancellation or metadata drift: %v", err)
					}
				}
				wantEarly := mode != "partial" && mode != "missing-tree" && mode != "unrecorded-graph"
				if (early != 0) != wantEarly {
					t.Fatalf("pending source selection boundary: early=%d want=%t", early, wantEarly)
				}
				if mode == "legacy" {
					if _, err := os.Stat(filepath.Join(fixture.downloaded.Dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("pending capture synthesized a legacy manifest")
					}
				}
				if string(readFile(t, fixture.authority)) != sums {
					t.Fatal("selection changed original checksum authority")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode != "source" && mode != "source-manifest" {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal("pending selection evidence was lost after cleanup")
					}
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed after selection cleanup\n")
				}
				if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
					t.Fatal("selection source was absent from retained evidence")
				}
			})
		}
	}
}

func TestDiscoverFixedRootTreeProofBoundaries(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		modes := []string{"wildcard", "exact-same", "exact-different", "exact-only", "local-alternative", "local", "excluded-original", "excluded-effective", "unused", "shared", "replacement-once", "proof-budget", "query-spelling", "archive-authority"}
		if workspace {
			modes = append(modes, "excluded-and-required", "main-root", "main-target", "member-wildcard", "work-override")
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				project := filepath.Join(fixture.app, "go.mod")
				projectText := string(readFile(t, project)) + "require " + warmRequestedModule + " v1.0.0\n"
				workText := ""
				text := &projectText
				if workspace {
					workText = string(readFile(t, fixture.work))
					text = &workText
				}
				wildcard := "replace " + warmRequestedModule + " => " + warmSourceModule + " v1.0.0\n"
				wantEarly := true
				switch mode {
				case "exact-same":
					*text += "replace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.0.0\n"
				case "exact-different":
					*text += "replace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.1.0\n"
					wantEarly = false
				case "exact-only":
					*text = strings.ReplaceAll(*text, "replace "+warmRequestedModule+" =>", "replace "+warmRequestedModule+" v1.0.0 =>")
					wantEarly = false
				case "local-alternative":
					writeModule(t, filepath.Join(fixture.root, "alternative"), warmRequestedModule)
					*text += fmt.Sprintf("replace %s v1.1.0 => %q\n", warmRequestedModule, filepath.ToSlash(filepath.Join(fixture.root, "alternative")))
					wantEarly = false
				case "local":
					writeModule(t, filepath.Join(fixture.root, "alternative"), warmRequestedModule)
					*text = strings.ReplaceAll(*text, warmSourceModule+" v1.0.0", fmt.Sprintf("%q", filepath.ToSlash(filepath.Join(fixture.root, "alternative"))))
					wantEarly = false
				case "excluded-original":
					projectText += "exclude " + warmRequestedModule + " v1.0.0\n"
					wantEarly = false
				case "excluded-effective":
					projectText += "exclude " + warmSourceModule + " v1.0.0\n"
				case "unused":
					projectText = strings.ReplaceAll(projectText, "require "+warmRequestedModule+" v1.0.0\n", "")
					wantEarly = false
				case "shared":
					projectText += "require example.com/shared v1.0.0\n"
					*text += "replace example.com/shared => " + warmSourceModule + " v1.0.0\n"
				case "replacement-once":
					*text += "replace " + warmSourceModule + " => example.com/unused v1.0.0\n"
				case "proof-budget":
					for version := range 256 {
						*text += fmt.Sprintf("replace %s v1.1.%d => %s v1.0.0\n", warmRequestedModule, version, warmSourceModule)
					}
					wantEarly = false
				case "query-spelling":
					projectText = strings.ReplaceAll(projectText, "require "+warmRequestedModule+" v1.0.0", "require "+warmRequestedModule+" v1")
					wantEarly = false
				case "excluded-and-required":
					projectText += "exclude " + warmRequestedModule + " v1.0.0\n"
					path := filepath.Join(fixture.root, "member", "go.mod")
					writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.1.0\n")
				case "main-root":
					writeModule(t, filepath.Join(fixture.root, "member"), warmRequestedModule)
					*text = strings.ReplaceAll(*text, wildcard, "")
					projectText = strings.ReplaceAll(projectText, "replace "+warmRequestedModule+" => ../local\n", "")
					wantEarly = false
				case "main-target":
					writeModule(t, filepath.Join(fixture.root, "member"), warmSourceModule)
				case "member-wildcard":
					*text = strings.ReplaceAll(*text, wildcard, "")
					projectText = strings.ReplaceAll(projectText, "replace "+warmRequestedModule+" => ../local\n", wildcard)
				case "work-override":
					path := filepath.Join(fixture.root, "member", "go.mod")
					writeFile(t, path, string(readFile(t, path))+"replace "+warmRequestedModule+" v1.1.0 => example.com/unused v1.0.0\n")
				}
				writeFile(t, project, projectText)
				if workspace {
					writeFile(t, fixture.work, workText)
				}
				sums := ""
				if !workspace {
					sums = warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
					if err := os.Remove(fixture.downloaded.GoMod); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "archive-authority" {
					sums += warmSourceModule + " v1.0.0 " + fixture.downloaded.Sum + "\n"
				}
				writeFile(t, fixture.authority, sums)
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				stopped := errors.New("fixed root retained before listing")
				calls := 0
				_, err = moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{Environment: environment, GoCommand: command,
					RetainSourceTrees: func(ctx context.Context, directories []string) error {
						if strings.Contains(string(readFile(t, trace)), "list -m ") {
							return nil
						}
						calls++
						if len(directories) != 1 || directories[0] != fixture.downloaded.Dir {
							t.Fatal("fixed root proof captured an unrelated source")
						}
						if err := stage.RetainSourceTrees(ctx, directories); err != nil {
							return err
						}
						if mode == "shared" {
							return nil
						}
						return stopped
					},
				})
				if mode == "shared" {
					if calls != 1 || errors.Is(err, stopped) || !strings.Contains(string(readFile(t, trace)), "list -m ") || !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
						t.Fatalf("shared fixed source was not retained exactly once: calls=%d error=%v", calls, err)
					}
				} else if wantEarly {
					if !errors.Is(err, stopped) || calls != 1 || strings.Contains(string(readFile(t, trace)), "list -m ") || !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
						t.Fatalf("fixed source proof did not retain pending evidence: calls=%d error=%v", calls, err)
					}
				} else if calls != 0 || errors.Is(err, stopped) || !strings.Contains(string(readFile(t, trace)), "list -m ") {
					t.Fatalf("uncertain or unrelated source was retained: calls=%d error=%v", calls, err)
				}
			})
		}
	}
}
