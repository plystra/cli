package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestExactAcquisitionRetainsCachedArchives(t *testing.T) {
	for _, phase := range []string{"preparation", "workspace-preparation", "discovery", "workspace-discovery", "checksums", "workspace-checksums"} {
		for _, mode := range []string{"unchanged", "identity", "parent-identity", "late", "unused", "no-callback", "pre-rejected", "pre-cancelled", "pre-drift", "pre-combined", "post-rejected", "post-combined", "failed", "failed-post-combined"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, strings.HasPrefix(phase, "workspace-"), false)
				archive := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".zip"
				archiveData := readFile(t, archive)
				checksumPhase := strings.HasSuffix(phase, "checksums")
				relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
				if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
					t.Fatal("source removal escaped isolated cache")
				}
				if checksumPhase || mode == "unused" {
					if checksumPhase && phase != "checksums" && mode != "unused" {
						writeFile(t, archive+"hash", "truncated")
					}
				} else if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
					t.Fatal(err)
				}
				if checksumPhase && phase != "checksums" {
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				}
				if mode == "unused" {
					writeFile(t, archive, "unused invalid archive")
				} else if mode == "late" {
					if err := os.Remove(archive); err != nil {
						t.Fatal(err)
					}
				}
				discovery := strings.HasSuffix(phase, "discovery") || checksumPhase
				project := filepath.Join(fixture.app, "go.mod")
				if discovery {
					writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				}
				original := readFile(t, fixture.authority)
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				if phase == "checksums" && mode != "unused" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_CHECKSUM=checksum-normalization")
				}
				if strings.HasSuffix(mode, "identity") {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+archive,
						"PLYSTRA_MODULE_METADATA_MARKER="+filepath.Join(fixture.root, "replaced"))
					if mode == "parent-identity" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=parent-identity")
					}
				}
				if strings.HasPrefix(mode, "failed") {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=command-error")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("archive capture rejected")
				before, after := 0, 0
				options := moduledependency.Options{GoCommand: command, Environment: environment, RequireSourceChecksums: true,
					RetainModuleArchives: func(captureCtx context.Context, archives []moduledependency.ModuleArchive) error {
						post := strings.Contains(string(readFile(t, trace)), "mod download ")
						if post {
							after++
							if captureCtx.Err() != nil || captureCtx == ctx {
								t.Fatal("post-command archive retention lacks independent bounded context")
							}
							if _, bounded := captureCtx.Deadline(); !bounded {
								t.Fatal("post-command capture has no deadline")
							}
						} else {
							before++
						}
						if len(archives) != 1 || archives[0].Path != archive || archives[0].Module != (module.Version{Path: warmSourceModule, Version: "v1.0.0"}) {
							t.Fatal("archive capture lost effective replacement identity")
						}
						if err := stage.RetainArchives(captureCtx, []projectstage.ArchiveInput{{Path: archives[0].Path, Module: archives[0].Module}}); err != nil {
							return err
						}
						if strings.HasPrefix(mode, "pre-") && !post || strings.Contains(mode, "post-") && post {
							if strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined") {
								writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
							}
							if strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
								cancel()
							}
							if strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined") {
								return rejected
							}
						}
						archives[0] = moduledependency.ModuleArchive{}
						return nil
					},
				}
				if mode == "no-callback" {
					options.RetainModuleArchives = nil
				}
				if mode == "late" {
					options.RetainModuleInformation = func(context.Context, []string) error {
						if _, err := os.Stat(archive); errors.Is(err, os.ErrNotExist) {
							writeFile(t, archive, string(archiveData))
						}
						return nil
					}
				}
				if discovery {
					_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				} else {
					err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options, func([]moduledependency.Module) error { return nil })
				}
				if strings.HasSuffix(mode, "identity") {
					if _, markerErr := os.Stat(filepath.Join(fixture.root, "replaced")); markerErr != nil || !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("archive identity replacement was not rejected: %v, mutation=%v", err, markerErr)
					}
				} else if strings.HasPrefix(mode, "pre-") || strings.Contains(mode, "post-") {
					if (strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, rejected) ||
						(strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, context.Canceled) ||
						(strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("archive callback lost rejection, cancellation, or drift: %v", err)
					}
				} else if mode == "failed" {
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
						t.Fatal("archive retention lost download failure", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				wantBefore, wantAfter := 1, 1
				if mode == "unused" || mode == "no-callback" {
					wantBefore, wantAfter = 0, 0
				} else if strings.HasPrefix(mode, "pre-") {
					wantAfter = 0
					if strings.Contains(string(readFile(t, trace)), "mod download ") {
						t.Fatal("rejected archive capture reached acquisition")
					}
				}
				if before != wantBefore || after != wantAfter {
					t.Fatalf("archive capture before=%d after=%d want=%d/%d error=%v", before, after, wantBefore, wantAfter, err)
				}
				if string(readFile(t, fixture.authority)) != string(original) {
					t.Fatal("archive retention changed original checksum authority")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(mode, "identity") {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("archive evidence lost after cleanup")
					}
				} else {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal(err)
					}
					if before != 0 {
						rejectAcquisitionManifestReplacement(t, inputs, archive)
					}
				}
			})
		}
	}
}

func TestPreparationArchivesSurviveInFlightCancellation(t *testing.T) {
	fixture := warmPreparation(t, false, false)
	archive := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".zip"
	if err := os.Remove(strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".info"); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		t.Fatal("source removal escaped isolated cache")
	}
	if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
		t.Fatal(err)
	}
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "retained-value"))
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".info") {
			cancel()
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	environment := append(withoutEnvironmentKey(fixture.environment, "GOPROXY"), "GOPROXY="+server.URL)
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	calls := 0
	err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{
		Environment: stage.Environment(),
		RetainModuleArchives: func(captureCtx context.Context, archives []moduledependency.ModuleArchive) error {
			calls++
			if captureCtx.Err() != nil || captureCtx.Value(contextKey{}) != "retained-value" {
				t.Fatal("archive capture lost context values or inherited cancellation")
			}
			if calls == 2 {
				if ctx.Err() != context.Canceled {
					t.Fatal("fixture did not cancel acquisition in flight")
				}
				if _, bounded := captureCtx.Deadline(); !bounded {
					t.Fatal("post-cancellation archive capture is unbounded")
				}
			}
			return stage.RetainArchives(captureCtx, []projectstage.ArchiveInput{{Path: archives[0].Path, Module: archives[0].Module}})
		},
	}, func([]moduledependency.Module) error {
		t.Fatal("cancelled acquisition delivered a source")
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("cancelled acquisition lost archive evidence: %v, calls=%d", err, calls)
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	rejectAcquisitionManifestReplacement(t, inputs, archive)
}

func TestChecksumArchivesDrainCallbackCreatedTargets(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "module", true: "workspace"}[workspace], func(t *testing.T) {
			t.Parallel()
			const later = "example.com/zz-later"
			fixture := warmPreparation(t, workspace, false)
			writeModuleProxy(t, fixture.root, later, "v1.0.0")
			seed := t.TempDir()
			writeModule(t, seed, "example.com/seed")
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed,
				Environment: append(withoutEnvironmentKey(fixture.environment, "GOWORK"), "GOWORK=off")}, "mod", "download", "-json", later+"@v1.0.0")
			var downloaded struct{ Zip, GoMod, GoModSum, Sum string }
			if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Zip == "" {
				t.Fatal("seed later cached archive", err)
			}
			data := readFile(t, downloaded.Zip)
			if err := os.Remove(downloaded.Zip); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(fixture.app, "go.mod")
			writeFile(t, project, string(readFile(t, project))+"require (\n"+warmRequestedModule+" v1.0.0\n"+later+" v1.0.0\n)\n")
			writeFile(t, fixture.authority, string(readFile(t, fixture.authority))+later+" v1.0.0/go.mod "+downloaded.GoModSum+"\n"+later+" v1.0.0 "+downloaded.Sum+"\n")
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, _ := tracePreparation(t, fixture.root, stage.Environment())
			environment = append(environment, "PLYSTRA_MODULE_LIST_CHECKSUM=checksum-normalization")
			calls := 0
			_, err = moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{
				GoCommand: command, Environment: environment, RequireSourceChecksums: true,
				RetainModuleArchives: func(ctx context.Context, archives []moduledependency.ModuleArchive) error {
					calls++
					inputs := make([]projectstage.ArchiveInput, len(archives))
					for index, archive := range archives {
						inputs[index] = projectstage.ArchiveInput{Path: archive.Path, Module: archive.Module}
					}
					if err := stage.RetainArchives(ctx, inputs); err != nil {
						return err
					}
					if calls == 1 {
						writeFile(t, downloaded.Zip, string(data))
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			rejectAcquisitionManifestReplacement(t, inputs, downloaded.Zip)
			if calls != 3 {
				t.Fatalf("archive cascade was not drained before acquisition: calls=%d", calls)
			}
		})
	}
}
