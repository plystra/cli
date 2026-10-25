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

func TestFixedRootManifestsRetainedBeforeSelection(t *testing.T) {
	for _, phase := range []string{"discovery", "preparation"} {
		for _, mode := range []string{"identity", "unchanged", "missing-tree", "missing", "directory", "partial", "legacy", "rejected", "cancelled", "metadata-drift", "combined", "manifest-drift", "selection-failure", "shared", "replacement-once", "exact-different", "unused", "mutated-callback", "unauthorized-graph", "no-callback", "manifest-only-callback", "source-authentication"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, true, mode == "legacy")
				if phase == "discovery" {
					path := filepath.Join(fixture.app, "go.mod")
					writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.0.0\n")
				}
				writeFile(t, fixture.authority, "")
				requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}
				switch mode {
				case "shared":
					path := filepath.Join(fixture.app, "go.mod")
					if phase == "discovery" {
						writeFile(t, path, string(readFile(t, path))+"require example.com/shared v1.0.0\n")
					}
					requested = append(requested, module.Version{Path: "example.com/shared", Version: "v1.0.0"})
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace example.com/shared => "+warmSourceModule+" v1.0.0\n")
					writeModule(t, filepath.Join(fixture.root, "shared"), "example.com/shared")
					writeFile(t, path, string(readFile(t, path))+"replace example.com/shared => ../shared\n")
				case "replacement-once":
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmSourceModule+" => example.com/unused v1.0.0\n")
				case "exact-different":
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				case "unused":
					path := filepath.Join(fixture.app, "go.mod")
					writeFile(t, path, strings.ReplaceAll(string(readFile(t, path)), "require "+warmRequestedModule+" v1.0.0\n", ""))
					requested = nil
				case "missing", "directory":
					if err := os.Remove(fixture.downloaded.GoMod); err != nil {
						t.Fatal(err)
					}
					if mode == "directory" {
						if err := os.Mkdir(fixture.downloaded.GoMod, 0o700); err != nil {
							t.Fatal(err)
						}
					}
				case "partial":
					writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".partial", "")
				case "unauthorized-graph":
					writeFile(t, fixture.downloaded.GoMod, "module "+warmSourceModule+"\nrequire example.com/unavailable v1.0.0\n")
				}
				if mode == "missing-tree" {
					relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
					if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
						t.Fatal("source removal escaped isolated cache")
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
				marker := filepath.Join(fixture.root, "manifest-replaced")
				if mode == "identity" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				} else if mode == "selection-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("fixed manifest rejected")
				calls, early := 0, 0
				options := moduledependency.Options{Environment: environment, GoCommand: command,
					RetainRootManifests: func(context.Context, []moduledependency.RootManifest) error {
						t.Fatal("fixed evidence supplied unsupported graph authority")
						return nil
					},
					RetainManifestFiles: func(ctx context.Context, paths []string) error {
						calls++
						if len(paths) != 1 || paths[0] != fixture.downloaded.GoMod {
							t.Fatal("fixed manifest lost its physical source identity")
						}
						commands := string(readFile(t, trace))
						if strings.Contains(commands, "mod download ") {
							t.Fatal("first raw manifest capture followed acquisition")
						}
						if !strings.Contains(commands, "list -m ") {
							early++
						}
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if mode == "metadata-drift" || mode == "combined" {
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						}
						if mode == "cancelled" || mode == "combined" {
							cancel()
						}
						if mode == "manifest-drift" {
							writeFile(t, paths[0], string(readFile(t, paths[0]))+"\n")
						}
						if mode == "rejected" || mode == "combined" || mode == "unauthorized-graph" {
							return rejected
						}
						if mode == "mutated-callback" {
							paths[0] = "unrelated"
						}
						return nil
					},
				}
				if mode == "no-callback" {
					options.RetainManifestFiles = nil
				} else if mode == "manifest-only-callback" {
					options.RetainRootManifests = nil
				}
				retain := func(context.Context, []moduledependency.Module) error { return stage.Recheck(ctx) }
				if mode == "source-authentication" {
					options.RetainSourceTrees = func(ctx context.Context, directories []string) error {
						if err := stage.RetainSourceTrees(ctx, directories); err != nil {
							return err
						}
						if !errors.Is(stage.CheckSources(ctx), projectstage.ErrSourceIntegrity) {
							t.Fatal("manifest identity evidence authenticated a pending source")
						}
						return nil
					}
					retain = func(_ context.Context, sources []moduledependency.Module) error {
						return retainWarmBundle(t, stage, ctx, sources)
					}
					options.RetainSelectedSources = retain
				}
				if phase == "preparation" {
					err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), requested, options, retain)
				} else {
					var index moduledependency.Index
					index, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
					if err == nil && mode == "source-authentication" {
						source, exists := index.ByPath(warmRequestedModule)
						if !exists {
							t.Fatal("missing independently resolved source")
						}
						err = retain(ctx, []moduledependency.Module{source})
					}
				}
				commands := string(readFile(t, trace))
				switch mode {
				case "identity":
					if _, markerErr := os.Stat(marker); markerErr != nil {
						t.Fatal("real Go selection did not replace the cache manifest", markerErr)
					}
					if !errors.Is(err, projectstage.ErrConcurrentChange) && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("selection accepted an identical-byte cache manifest replacement: %v", err)
					}
				case "directory":
					if err == nil || calls != 0 || strings.Contains(commands, "list -m ") {
						t.Fatal("unsafe manifest reached selection", err)
					}
					return
				case "rejected", "cancelled", "metadata-drift", "combined", "manifest-drift", "unauthorized-graph":
					if (mode == "rejected" || mode == "combined" || mode == "unauthorized-graph") && !errors.Is(err, rejected) ||
						(mode == "cancelled" || mode == "combined") && !errors.Is(err, context.Canceled) ||
						(mode == "metadata-drift" || mode == "combined" || mode == "manifest-drift") && !errors.Is(err, moduledependency.ErrConcurrentChange) ||
						strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
						t.Fatal("failed manifest retention reached Go or lost errors", err)
					}
				case "selection-failure":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatal("failed selection lost its original failure", err)
					}
				case "shared":
					// Go may reject two logical identities sharing a physical module.
				default:
					if err != nil {
						t.Fatal("unchanged fixed manifest failed", err)
					}
				}
				wantEarly, wantCalls := 1, 1
				if mode == "missing" || mode == "exact-different" || mode == "unused" || mode == "no-callback" {
					wantEarly = 0
				}
				if phase == "discovery" && mode == "exact-different" {
					wantEarly = 1 // The exact raw requirement is consumed before selection.
				}
				if mode == "unused" || mode == "no-callback" {
					wantCalls = 0
				}
				if early != wantEarly || calls != wantCalls {
					t.Fatalf("fixed manifest first-read boundary: early=%d calls=%d", early, calls)
				}
				if strings.Contains(string(readFile(t, trace)), "mod download ") && mode == "identity" {
					t.Fatal("manifest identity change reached source acquisition")
				}
				if string(readFile(t, fixture.authority)) != "" {
					t.Fatal("retention changed original checksum authority")
				}
				if mode == "source-authentication" && stage.CheckSources(t.Context()) != nil {
					t.Fatal("source could not authenticate its original pending evidence")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "identity" || mode == "manifest-drift" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("manifest evidence was discarded after cleanup")
					}
				} else {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal(err)
					}
					if calls != 0 {
						rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
					}
				}
			})
		}
	}
}
