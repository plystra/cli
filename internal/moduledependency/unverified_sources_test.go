package moduledependency_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesRetainsTreesBeforeChecksumAcquisition(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "no-sums", "source", "source-manifest", "poisoned-tree", "legacy", "partial", "failed-acquisition", "rejected", "cancelled"} {
			name := "module/" + mode
			if workspace {
				name = "workspace/" + mode
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, mode == "legacy")
				sums := warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
				if mode == "no-sums" {
					sums = ""
				}
				writeFile(t, fixture.authority, sums)
				path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
				if mode == "source-manifest" {
					path = filepath.Join(fixture.downloaded.Dir, "go.mod")
				} else if mode == "poisoned-tree" {
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed tree bytes\n")
				} else if mode == "partial" {
					writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".partial", "partial extraction\n")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				marker := filepath.Join(fixture.root, "changed-source")
				if mode == "source" || mode == "source-manifest" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				} else if mode == "failed-acquisition" {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=download-error")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("pending source rejected")
				rawCalls, authenticatedCalls := 0, 0
				options := moduledependency.Options{Environment: environment, GoCommand: command,
					RetainSourceTrees: func(retainCtx context.Context, directories []string) error {
						rawCalls++
						if len(directories) != 1 || directories[0] != fixture.downloaded.Dir || strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
							t.Fatal("pending tree was not retained before its exact acquisition")
						}
						if err := stage.RetainSourceTrees(retainCtx, directories); err != nil {
							return err
						}
						if !errors.Is(stage.CheckSources(retainCtx), projectstage.ErrSourceIntegrity) {
							t.Fatal("raw tree evidence authenticated itself")
						}
						if mode == "rejected" {
							return rejected
						} else if mode == "cancelled" {
							cancel()
						}
						return nil
					},
				}
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options,
					func(sources []moduledependency.Module) error {
						if len(sources) != 0 {
							authenticatedCalls++
						}
						return retainWarmBundle(t, stage, ctx, sources)
					})
				switch mode {
				case "source", "source-manifest":
					if !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("identical-byte source replacement accepted: %v", err)
					}
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("real acquisition did not exercise the source race")
					}
				case "poisoned-tree":
					if !errors.Is(err, projectstage.ErrSourceIntegrity) {
						t.Fatalf("original tree was not authenticated against Go's checksum: %v", err)
					}
				case "failed-acquisition", "rejected", "cancelled":
					if err == nil || authenticatedCalls != 0 || !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
						t.Fatalf("failed acquisition accepted pending evidence: %v", err)
					}
					if mode == "rejected" && !errors.Is(err, rejected) || mode == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("retention failure lost its cause: %v", err)
					}
					if mode != "failed-acquisition" && strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
						t.Fatal("rejected pending evidence reached acquisition")
					}
				default:
					if err != nil || stage.CheckSources(t.Context()) != nil || authenticatedCalls == 0 {
						t.Fatalf("unchanged acquisition did not authenticate: %v", err)
					}
				}
				wantRaw := 2 // Warm retention and the immediate acquisition boundary.
				if workspace {
					wantRaw++ // The fixed workspace source is also retained before selection.
				}
				if mode == "rejected" || mode == "cancelled" {
					wantRaw = 1
				} else if mode == "partial" {
					wantRaw = 0
				}
				if rawCalls != wantRaw {
					t.Fatalf("unexpected raw capture count: %d", rawCalls)
				}
				if string(readFile(t, fixture.authority)) != sums {
					t.Fatal("preparation changed original checksum authority")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode != "source" && mode != "source-manifest" {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal("pending or authenticated evidence did not survive cleanup")
					}
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed after cleanup\n")
				}
				if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
					t.Fatal("source evidence was discarded after cleanup")
				}
			})
		}
	}
}

func TestDiscoverPendingTreeRejectsMetadataDriftBeforeAcquisition(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		name := "module"
		if workspace {
			name = "workspace"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, workspace, false)
			path := filepath.Join(fixture.app, "go.mod")
			writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.0.0\n")
			replacements := path
			if workspace {
				replacements = fixture.work
			}
			// An unused differing exact rule prevents a fixed-source proof.
			writeFile(t, replacements, string(readFile(t, replacements))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
			// Missing graph metadata leaves this regression on the post-list boundary.
			if err := os.Remove(fixture.downloaded.GoMod); err != nil {
				t.Fatal(err)
			}
			sums := warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
			writeFile(t, fixture.authority, sums)
			command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
			environment = append(environment, "PLYSTRA_MODULE_LIST_CHECKSUM=missing-source")
			calls := 0
			_, err := moduledependency.Discover(t.Context(), locate(t, fixture.app), moduledependency.Options{
				Environment: environment, GoCommand: command, RequireSourceChecksums: true,
				RetainSourceTrees: func(_ context.Context, directories []string) error {
					calls++
					if len(directories) != 1 || directories[0] != fixture.downloaded.Dir {
						t.Fatal("missing listed paths did not resolve to the effective source")
					}
					if calls == 2 {
						writeFile(t, fixture.authority, sums+"\n")
					}
					return nil
				},
			})
			if calls != 2 || !errors.Is(err, moduledependency.ErrConcurrentChange) {
				t.Fatalf("pending callback metadata drift was not rejected: calls=%d error=%v", calls, err)
			}
			if strings.Contains(string(readFile(t, trace)), "mod download -json ") {
				t.Fatal("changed original checksum authority reached acquisition")
			}
		})
	}
}

func TestDiscoverRetainsListedTreesBeforeChecksumResolution(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "identity", "rejected", "poisoned-tree"} {
			name := "module/" + mode
			if workspace {
				name = "workspace/" + mode
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				project := filepath.Join(fixture.app, "go.mod")
				writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				replacements := project
				if workspace {
					replacements = fixture.work
				}
				// An unused differing exact rule prevents a fixed-source proof.
				writeFile(t, replacements, string(readFile(t, replacements))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				// Go restores this missing manifest after early inference declines.
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
				if mode == "poisoned-tree" {
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed tree bytes\n")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				if mode == "identity" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+filepath.Join(fixture.root, "changed"))
				}
				calls := 0
				rejected := errors.New("listed pending source rejected")
				index, err := moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{
					Environment: environment, GoCommand: command, RequireSourceChecksums: true,
					RetainSourceTrees: func(ctx context.Context, directories []string) error {
						calls++
						commands := string(readFile(t, trace))
						if !strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
							t.Fatal("listed tree was not retained before checksum resolution")
						}
						if err := stage.RetainSourceTrees(ctx, directories); err != nil {
							return err
						}
						if mode == "rejected" {
							return rejected
						}
						return nil
					},
				})
				if mode == "rejected" {
					if !errors.Is(err, rejected) || strings.Contains(string(readFile(t, trace)), "mod download ") {
						t.Fatalf("rejected listed tree reached checksum resolution: %v", err)
					}
					return
				}
				// Without archive authority, Go omits the extracted path from its
				// list result. Both post-list retention and exact acquisition must
				// therefore recheck the same pending tree before download starts.
				if err != nil || calls != 2 {
					t.Fatalf("listed tree discovery failed: calls=%d error=%v", calls, err)
				}
				source, exists := index.ByPath(warmRequestedModule)
				if !exists || source.Workspace() || source.Checksum() != fixture.downloaded.Sum {
					t.Fatal("listed effective source lost its independent checksum")
				}
				err = retainWarmBundle(t, stage, t.Context(), []moduledependency.Module{source})
				if mode == "identity" && !errors.Is(err, projectstage.ErrConcurrentChange) ||
					mode == "poisoned-tree" && !errors.Is(err, projectstage.ErrSourceIntegrity) ||
					mode == "unchanged" && (err != nil || stage.CheckSources(t.Context()) != nil) {
					t.Fatalf("listed original-tree authentication disagreed with %s: %v", mode, err)
				}
			})
		}
	}
}
