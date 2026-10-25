package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesRetainsLateManifestBeforeAcquisition(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"identity", "unchanged", "information", "information-identity", "pre-rejected", "pre-cancelled", "pre-drift", "pre-combined", "post-rejected", "post-combined", "download-failure", "unrecorded"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", false)
				const later, effective = "example.com/later", "example.com/Later/Source"
				writeModuleProxy(t, fixture.root, effective, "v1.0.0")
				seed := t.TempDir()
				writeModule(t, seed, "example.com/seed")
				var downloaded struct{ Dir, GoMod, Sum, GoModSum string }
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: append(withoutEnvironmentKey(fixture.environment, "GOWORK"), "GOWORK=off")}, "mod", "download", "-json", effective+"@v1.0.0")
				if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.GoModSum == "" {
					t.Fatal("cannot seed later source", err)
				}
				manifest := readFile(t, downloaded.GoMod)
				informationPath := strings.TrimSuffix(downloaded.GoMod, ".mod") + ".info"
				information := readFile(t, informationPath)
				project := filepath.Join(fixture.app, "go.mod")
				if phase == "workspace" {
					writeModule(t, filepath.Join(fixture.root, "local-later"), later)
					writeFile(t, project, string(readFile(t, project))+"replace "+later+" => ../local-later\n")
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+later+" => "+effective+" v1.0.0\n")
				} else {
					writeFile(t, project, string(readFile(t, project))+"replace "+later+" => "+effective+" v1.0.0\n")
				}
				writeFile(t, fixture.authority, string(readFile(t, fixture.authority))+effective+" v1.0.0 "+downloaded.Sum+"\n"+effective+" v1.0.0/go.mod "+downloaded.GoModSum+"\n")
				if mode == "unrecorded" {
					writeFile(t, fixture.authority, strings.ReplaceAll(string(readFile(t, fixture.authority)), effective+" v1.0.0/go.mod "+downloaded.GoModSum+"\n", ""))
				}
				original := readFile(t, fixture.authority)
				clearAcquisitionSource(t, fixture.cache, fixture.downloaded.Dir, fixture.downloaded.GoMod)
				clearAcquisitionSource(t, fixture.cache, downloaded.Dir, downloaded.GoMod)
				if strings.HasPrefix(mode, "information") {
					writeFile(t, informationPath, string(information))
				}
				if mode == "download-failure" {
					escaped, err := module.EscapePath(effective)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")); err != nil {
						t.Fatal(err)
					}
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				marker := filepath.Join(fixture.root, "later-manifest-replaced")
				if strings.HasSuffix(mode, "identity") {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_QUERY="+effective+"@v1.0.0", "PLYSTRA_MODULE_METADATA_PATH="+downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				}
				before, after, completed := 0, 0, 0
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("late manifest rejected")
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{
					{Path: warmRequestedModule, Version: "v1.0.0"}, {Path: later, Version: "v1.0.0"},
				}, moduledependency.Options{Environment: environment, GoCommand: command,
					RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
						for _, captured := range manifests {
							post := false
							if captured.Path == downloaded.GoMod {
								if captured.Checksum != downloaded.GoModSum {
									t.Fatal("later manifest lost its original authority")
								}
								post = strings.Contains(string(readFile(t, trace)), "mod download -json "+effective+"@v1.0.0")
								if post {
									after++
								} else {
									before++
								}
							}
							if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: captured.Path, GoModChecksum: captured.Checksum}}); err != nil {
								return err
							}
							if captured.Path == downloaded.GoMod && (strings.HasPrefix(mode, "pre-") && !post || strings.HasPrefix(mode, "post-") && post) {
								if strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined") {
									path := filepath.Join(stage.Root(), "go.sum")
									writeFile(t, path, "\n")
								}
								if strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
									cancel()
								}
								if strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined") {
									return rejected
								}
							}
						}
						return nil
					},
					RetainModuleInformation: func(_ context.Context, paths []string) error {
						if !strings.HasPrefix(mode, "information") || phase == "workspace" && !strings.Contains(string(readFile(t, trace)), "list -m ") {
							return nil
						}
						for _, path := range paths {
							if path == informationPath {
								if _, err := os.Stat(downloaded.GoMod); errors.Is(err, os.ErrNotExist) {
									writeFile(t, downloaded.GoMod, string(manifest))
								} else if err != nil {
									t.Fatal(err)
								}
							}
						}
						return nil
					},
				}, func(sources []moduledependency.Module) error {
					if len(sources) == 0 {
						return stage.Recheck(t.Context())
					}
					completed++
					if sources[0].Path() == warmRequestedModule && !strings.HasPrefix(mode, "information") {
						if _, err := os.Stat(downloaded.GoMod); errors.Is(err, os.ErrNotExist) {
							writeFile(t, downloaded.GoMod, string(manifest))
						} else if err != nil {
							t.Fatal(err)
						}
					}
					return retainWarmBundle(t, stage, t.Context(), sources)
				})
				wantBefore, wantAfter, wantCompleted := 1, 1, 2
				if strings.HasSuffix(mode, "identity") {
					if _, markerErr := os.Stat(marker); markerErr != nil {
						t.Fatal("real later acquisition did not replace the manifest", markerErr)
					}
					if !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("late manifest identity replacement accepted: %v", err)
					}
					wantCompleted = 1
				} else if strings.HasPrefix(mode, "pre-") || strings.HasPrefix(mode, "post-") {
					wantCompleted = 1
					if strings.HasPrefix(mode, "pre-") {
						wantAfter = 0
						if strings.Contains(string(readFile(t, trace)), "mod download -json "+effective+"@v1.0.0") {
							t.Fatal("rejected manifest capture reached later acquisition")
						}
					}
					if (strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, rejected) ||
						(strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, context.Canceled) ||
						(strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatal("late capture lost callback failure, cancellation, or drift", err)
					}
				} else if mode == "download-failure" {
					wantCompleted = 1
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
						t.Fatal("later download failure lost", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if mode == "unrecorded" {
					wantBefore, wantAfter = 0, 0
				}
				if before != wantBefore || after != wantAfter || completed != wantCompleted {
					t.Fatalf("later acquisition boundaries: before=%d after=%d completed=%d error=%v", before, after, completed, err)
				}
				if string(readFile(t, fixture.authority)) != string(original) {
					t.Fatal("preparation changed original authority")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(mode, "identity") {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("late manifest evidence lost after cleanup")
					}
				} else {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal(err)
					}
					rejectAcquisitionManifestReplacement(t, inputs, downloaded.GoMod)
				}
			})
		}
	}
}
