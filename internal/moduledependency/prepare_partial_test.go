package moduledependency_test

import (
	"bytes"
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

func TestPrepareSourcesRetainsMaterializedManifest(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"failure", "success", "failure-rejected", "success-rejected", "failure-cancelled", "success-cancelled", "failure-combined", "success-combined", "failure-unrecorded", "success-unrecorded", "invalid-output", "later-failure", "shared-source", "poisoned"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", false)
				if err := os.RemoveAll(fixture.cache); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(mode, "failure") {
					escaped, _ := module.EscapePath(warmSourceModule)
					if err := os.Remove(filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasSuffix(mode, "unrecorded") {
					writeFile(t, fixture.authority, "")
				}
				requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}
				if mode == "later-failure" || mode == "shared-source" || strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
					const second = "example.com/second"
					requested = append(requested, module.Version{Path: second, Version: "v1.0.0"})
					if phase == "workspace" {
						writeModule(t, filepath.Join(fixture.root, "local-second"), second)
						path := filepath.Join(fixture.app, "go.mod")
						writeFile(t, path, string(readFile(t, path))+"replace "+second+" => ../local-second\n")
					}
					if mode == "shared-source" {
						path := filepath.Join(fixture.app, "go.mod")
						if fixture.work != "" {
							path = fixture.work
						}
						writeFile(t, path, string(readFile(t, path))+"replace "+second+" => "+warmSourceModule+" v1.0.0\n")
					} else {
						if phase == "workspace" {
							writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+second+" => "+second+" v1.0.0\n")
						}
						proxy := writeModuleProxy(t, fixture.root, second, "v1.0.0", "module "+second+"\ngo 1.26\n")
						if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(second), "@v", "v1.0.0.zip")); err != nil {
							t.Fatal(err)
						}
					}
				}
				original := readFile(t, fixture.authority)
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				if mode == "invalid-output" {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=trailing-json")
				}
				if mode == "poisoned" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MODE=poison", "PLYSTRA_MODULE_METADATA_MARKER="+filepath.Join(fixture.root, "poisoned"))
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("materialized manifest rejected")
				captures, completed := 0, 0
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), requested, moduledependency.Options{Environment: environment, GoCommand: command,
					RetainRootManifests: func(_ context.Context, manifests []moduledependency.RootManifest) error {
						captures++
						if len(manifests) != 1 || manifests[0].Path != fixture.downloaded.GoMod || manifests[0].Checksum != fixture.downloaded.GoModSum {
							t.Fatalf("materialized manifest lost its original authority: %v", manifests)
						}
						commands := string(readFile(t, trace))
						post := strings.Contains(commands, "mod download -json "+warmSourceModule+"@v1.0.0\n")
						wantPost := phase == "module" || captures == 2
						if post != wantPost || strings.Contains(commands, "example.com/second@v1.0.0\n") || completed != 0 {
							t.Fatalf("manifest retained outside its acquisition boundary: %s, completed=%d", commands, completed)
						}
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: manifests[0].Path, GoModChecksum: manifests[0].Checksum}}); err != nil {
							return err
						}
						if !post {
							return nil
						}
						if strings.HasSuffix(mode, "combined") {
							path := filepath.Join(stage.Root(), "go.mod")
							writeFile(t, path, string(readFile(t, path))+"// concurrent metadata edit\n")
						}
						if strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
							cancel()
						}
						if strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined") {
							return rejected
						}
						return nil
					},
				}, func(_ context.Context, sources []moduledependency.Module) error {
					if len(sources) != 0 {
						if !strings.HasSuffix(mode, "unrecorded") && captures == 0 {
							t.Fatal("completed source preceded independent manifest capture")
						}
						completed += len(sources)
					}
					return nil
				})
				wantCaptures := 1
				if phase == "workspace" {
					wantCaptures = 2
				}
				if strings.HasSuffix(mode, "unrecorded") {
					wantCaptures = 0
				}
				if captures != wantCaptures {
					t.Fatalf("materialized manifest callbacks=%d, want %d: %v", captures, wantCaptures, err)
				}
				if strings.HasPrefix(mode, "failure") || mode == "later-failure" {
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
						t.Fatalf("acquisition failure lost: %v", err)
					}
					if mode == "later-failure" && (completed != 1 || !strings.Contains(string(readFile(t, trace)), "mod download -json example.com/second@v1.0.0\n")) {
						t.Fatalf("later failure did not follow a completed acquisition: completed=%d, %v", completed, err)
					}
				} else if mode == "invalid-output" {
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("invalid acquisition output accepted: %v", err)
					}
				} else if mode == "poisoned" {
					want := projectstage.ErrStage
					if phase == "workspace" {
						want = projectstage.ErrConcurrentChange
					}
					if !errors.Is(err, want) || completed != 0 || len(readFile(t, filepath.Join(fixture.root, "poisoned"))) == 0 {
						t.Fatalf("poisoned manifest accepted under original checksum: %v", err)
					}
				} else if mode == "success" || mode == "success-unrecorded" || mode == "shared-source" {
					if err != nil || completed != len(requested) {
						t.Fatalf("valid acquisition failed: completed=%d, %v", completed, err)
					}
				}
				if (strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, rejected) {
					t.Fatalf("retention rejection lost: %v", err)
				}
				if (strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined")) && !errors.Is(err, context.Canceled) {
					t.Fatalf("retention cancellation lost: %v", err)
				}
				if strings.HasSuffix(mode, "combined") && !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("original metadata drift lost: %v", err)
				}
				if mode != "later-failure" && strings.Contains(string(readFile(t, trace)), "mod download -json example.com/second@v1.0.0\n") {
					t.Fatal("later acquisition followed failed manifest retention")
				}
				if strings.HasPrefix(mode, "failure") || strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") || mode == "invalid-output" {
					if completed != 0 {
						t.Fatal("incomplete acquisition delivered a source")
					}
				}
				if !bytes.Equal(original, readFile(t, fixture.authority)) {
					t.Fatal("acquisition changed original checksum authority")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if phase == "workspace" && mode == "poisoned" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("poisoned manifest lost its earlier evidence after cleanup")
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("retained evidence failed after cleanup: %v", err)
				}
				if wantCaptures != 0 && mode != "poisoned" {
					path := fixture.downloaded.GoMod
					data := readFile(t, path)
					info, err := os.Stat(path)
					if err != nil || !os.SameFile(info, info) {
						t.Fatal(err)
					}
					if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, string(data))
					if err := os.Chmod(path, info.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
						t.Fatal(err)
					}
					replacement, err := os.Stat(path)
					if err != nil || os.SameFile(info, replacement) {
						t.Fatal("fixture did not replace manifest identity")
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("same-byte manifest replacement survived cleanup: %v", err)
					}
				}
			})
		}
	}
}

func TestPrepareSourcesRechecksWarmManifestAfterAcquisition(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"file-identity", "parent-identity", "poison"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", false)
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				marker := filepath.Join(fixture.root, "changed")
				command, environment, _ := tracePreparation(t, fixture.root, stage.Environment())
				environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MODE="+mode, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				captures := 0
				err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{Environment: environment, GoCommand: command,
					RetainRootManifests: func(_ context.Context, manifests []moduledependency.RootManifest) error {
						captures++
						if len(manifests) != 1 || manifests[0].Path != fixture.downloaded.GoMod || manifests[0].Checksum != fixture.downloaded.GoModSum {
							t.Fatalf("warm manifest authority changed: %v", manifests)
						}
						return stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{{Path: manifests[0].Path, GoModChecksum: manifests[0].Checksum}})
					},
				}, func(_ context.Context, sources []moduledependency.Module) error {
					if _, err := os.Stat(marker); len(sources) != 0 && err == nil {
						t.Fatal("changed manifest delivered a completed source")
					}
					return nil
				})
				if captures != 3 || !errors.Is(err, projectstage.ErrConcurrentChange) || len(readFile(t, marker)) == 0 {
					t.Fatalf("warm evidence was refreshed after acquisition: captures=%d, %v", captures, err)
				}
			})
		}
	}
}
