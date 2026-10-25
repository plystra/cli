package moduledependency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestDiscoverRetainsAcquisitionManifests(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		modes := []string{"success", "download-failure", "manifest-only-callback", "manifest-only-authority", "unrecorded", "replacement-chain", "created-during-download", "failed-created-during-download", "pre-rejected", "pre-cancelled", "pre-drift", "pre-combined", "post-rejected", "post-cancelled", "post-drift", "post-combined", "failed-post-combined", "malformed-output", "mutated-callback"}
		if workspace {
			modes = append(modes, "primary-authority", "member-conflict")
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := coldDiscoveryAcquisition(t, workspace)
				project := filepath.Join(fixture.app, "go.mod")
				if mode == "replacement-chain" {
					writeFile(t, project, string(readFile(t, project))+"replace "+warmSourceModule+" => example.com/not-selected v1.0.0\n")
				}
				if mode == "manifest-only-authority" {
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				} else if mode == "unrecorded" {
					writeFile(t, fixture.authority, "")
				} else if mode == "primary-authority" {
					writeFile(t, fixture.work+".sum", string(readFile(t, fixture.authority)))
					writeFile(t, fixture.authority, strings.ReplaceAll(string(readFile(t, fixture.authority)), fixture.downloaded.GoModSum, "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="))
				} else if mode == "member-conflict" {
					writeFile(t, filepath.Join(fixture.app, "go.sum"), warmSourceModule+" v1.0.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
				}
				failedDownload := mode == "download-failure" || strings.HasPrefix(mode, "failed-")
				if failedDownload {
					escaped, err := module.EscapePath(warmSourceModule)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")); err != nil {
						t.Fatal(err)
					}
				}
				originals := map[string][]byte{project: readFile(t, project), fixture.authority: readFile(t, fixture.authority)}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				createdDuringDownload := strings.Contains(mode, "created-during-download")
				marker := filepath.Join(fixture.root, "manifest-removed-after-listing")
				if createdDuringDownload {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MODE=delete", "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				} else if mode == "malformed-output" {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=trailing-json")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("acquisition manifest rejected")
				calls, before, after, sources := 0, 0, 0, 0
				options := moduledependency.Options{
					Environment: environment, GoCommand: command, RequireSourceChecksums: true,
					RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
						calls++
						if len(manifests) != 1 || manifests[0].Path != fixture.downloaded.GoMod || manifests[0].Checksum != fixture.downloaded.GoModSum {
							t.Fatal("acquisition manifest lost its original physical identity or checksum")
						}
						post := strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@v1.0.0")
						if post {
							after++
						} else {
							before++
						}
						if captureCtx.Err() != nil {
							t.Fatal("manifest retention inherited cancellation")
						}
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: manifests[0].Path, GoModChecksum: manifests[0].Checksum}}); err != nil {
							return err
						}
						selected := !post && strings.HasPrefix(mode, "pre-") || post && (strings.HasPrefix(mode, "post-") || mode == "failed-post-combined")
						if selected {
							if strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
								cancel()
							}
							if strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined") {
								path := filepath.Join(stage.Root(), "go.mod")
								writeFile(t, path, string(readFile(t, path))+"// concurrent edit\n")
							}
							if strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined") {
								return rejected
							}
						}
						if mode == "mutated-callback" {
							manifests[0] = moduledependency.RootManifest{}
						}
						return nil
					},
					RetainSelectedSources: func(context.Context, []moduledependency.Module) error { sources++; return nil },
				}
				if mode == "manifest-only-callback" {
					options.RetainSelectedSources = nil
				}
				index, err := moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				commands := string(readFile(t, trace))
				preFailure := strings.HasPrefix(mode, "pre-")
				postFailure := strings.HasPrefix(mode, "post-") || mode == "failed-post-combined"
				wantBefore, wantAfter := 1, 1
				if createdDuringDownload || mode == "unrecorded" || mode == "member-conflict" {
					wantBefore = 0
				}
				if wantBefore != 0 && !preFailure {
					// Listing retains the newly materialized manifest before the
					// later exact-download precheck rechecks the same original.
					wantBefore++
				}
				if preFailure || mode == "unrecorded" || mode == "member-conflict" {
					wantAfter = 0
				}
				wantSources := 1
				wantError := failedDownload || preFailure || postFailure || mode == "malformed-output" || mode == "member-conflict" || mode == "unrecorded" && !workspace
				if wantError || mode == "manifest-only-callback" {
					wantSources = 0
				}
				if (err != nil) != wantError || before != wantBefore || after != wantAfter || sources != wantSources || err != nil && len(index.Modules()) != 0 {
					t.Fatalf("acquisition retention: before=%d after=%d sources=%d, want %d/%d/%d, error=%v", before, after, sources, wantBefore, wantAfter, wantSources, err)
				}
				if (preFailure || mode == "member-conflict" || mode == "unrecorded" && !workspace) && strings.Contains(commands, "mod download ") {
					t.Fatal("rejected manifest retention reached acquisition")
				}
				if failedDownload && !errors.Is(err, moduledependency.ErrModuleUnavailable) || mode == "malformed-output" && !errors.Is(err, moduledependency.ErrInvalidOutput) {
					t.Fatalf("acquisition failure was lost: %v", err)
				}
				if strings.HasSuffix(mode, "rejected") || strings.HasSuffix(mode, "combined") {
					if !errors.Is(err, rejected) {
						t.Fatalf("retention rejection was lost: %v", err)
					}
				}
				if strings.HasSuffix(mode, "cancelled") || strings.HasSuffix(mode, "combined") {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("retention cancellation was lost: %v", err)
					}
				}
				if strings.HasSuffix(mode, "drift") || strings.HasSuffix(mode, "combined") {
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("original metadata drift was lost: %v", err)
					}
				}
				if createdDuringDownload {
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("manifest was not first materialized during acquisition", err)
					}
				}
				for path, data := range originals {
					if !bytes.Equal(data, readFile(t, path)) {
						t.Fatal("discovery changed original Project inputs")
					}
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unchanged evidence failed after acquisition failure and cleanup", err)
				}
				if calls == 0 {
					return
				}
				rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
			})
		}
	}
}

func TestDiscoverRetainsManifestsAcrossSequentialAcquisitions(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"later-failure-first", "later-failure-second", "post-rejected", "shared-source"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := coldDiscoveryAcquisition(t, workspace)
				paths := []string{"example.com/zz-failing", "example.com/zzz-never"}
				seed := t.TempDir()
				writeModule(t, seed, "example.com/seed")
				var downloads [2]struct{ Dir, GoMod, Sum, GoModSum string }
				project := filepath.Join(fixture.app, "go.mod")
				for index, path := range paths {
					writeModuleProxy(t, fixture.root, path, "v1.0.0")
					output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: append(withoutEnvironmentKey(fixture.environment, "GOWORK"), "GOWORK=off")}, "mod", "download", "-json", path+"@v1.0.0")
					if err != nil || json.Unmarshal(output, &downloads[index]) != nil || downloads[index].GoModSum == "" {
						t.Fatalf("seed later source: %v", err)
					}
					downloaded := downloads[index]
					writeFile(t, project, string(readFile(t, project))+"require "+path+" v1.0.0\n")
					writeFile(t, fixture.authority, string(readFile(t, fixture.authority))+path+" v1.0.0 "+downloaded.Sum+"\n"+path+" v1.0.0/go.mod "+downloaded.GoModSum+"\n")
					clearAcquisitionSource(t, fixture.cache, downloaded.Dir, downloaded.GoMod)
				}
				if err := os.Remove(filepath.Join(fixture.proxy, filepath.FromSlash(paths[0]), "@v", "v1.0.0.zip")); err != nil {
					t.Fatal(err)
				}
				if mode == "shared-source" {
					writeFile(t, project, string(readFile(t, project))+"require example.com/shared v1.0.0\nreplace example.com/shared => "+warmSourceModule+" v1.0.0\n")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				rejected := errors.New("first post-download manifest rejected")
				captures := map[string]int{}
				sources := 0
				index, err := moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{
					Environment: environment, GoCommand: command, RequireSourceChecksums: true,
					RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
						if len(manifests) != 1 {
							t.Fatal("distinct effective sources were combined")
						}
						manifest := manifests[0]
						captures[manifest.Path]++
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: manifest.Path, GoModChecksum: manifest.Checksum}}); err != nil {
							return err
						}
						if mode == "post-rejected" && manifest.Path == fixture.downloaded.GoMod && captures[manifest.Path] == 2 {
							return rejected
						}
						return nil
					},
					RetainSelectedSources: func(_ context.Context, selected []moduledependency.Module) error {
						sources++
						want := 1
						if mode == "shared-source" {
							want = 2
						}
						if len(selected) != want || captures[fixture.downloaded.GoMod] != 2 || strings.Contains(string(readFile(t, trace)), "mod download -json "+paths[0]+"@") {
							t.Fatal("source retention did not precede the later acquisition or follow its own manifest capture")
						}
						return retainWarmBundle(t, stage, t.Context(), selected)
					},
				})
				wantSources, wantLater, wantError := 1, 2, moduledependency.ErrModuleUnavailable
				if mode == "post-rejected" {
					wantSources, wantLater, wantError = 0, 0, rejected
				}
				if !errors.Is(err, wantError) || len(index.Modules()) != 0 || sources != wantSources || captures[fixture.downloaded.GoMod] != 2 || captures[downloads[0].GoMod] != wantLater || captures[downloads[1].GoMod] != 0 {
					t.Fatalf("sequential acquisition lost evidence or advanced: captures=%v sources=%d error=%v", captures, sources, err)
				}
				commands := string(readFile(t, trace))
				if strings.Count(commands, "mod download -json "+warmSourceModule+"@v1.0.0\n") != 1 || strings.Contains(commands, "mod download -json "+paths[1]+"@") || mode == "post-rejected" && strings.Contains(commands, "mod download -json "+paths[0]+"@") {
					t.Fatal("effective acquisition was duplicated or continued after failure")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("retained sequential evidence failed after cleanup", err)
				}
				path := fixture.downloaded.GoMod
				if mode == "later-failure-second" {
					path = downloads[0].GoMod
				}
				rejectAcquisitionManifestReplacement(t, inputs, path)
			})
		}
	}
}

func TestDiscoverRetainsInterruptedAcquisitionManifests(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(fmt.Sprintf("workspace=%t", workspace), func(t *testing.T) {
			t.Parallel()
			fixture := coldDiscoveryAcquisition(t, workspace)
			started := make(chan struct{}, 1)
			var reached atomic.Int32
			files := http.FileServer(http.Dir(fixture.proxy))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".zip") {
					reached.Add(1)
					select {
					case started <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					return
				}
				files.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			environment := append(withoutEnvironmentKey(fixture.environment, "GOPROXY"), "GOPROXY="+server.URL)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			type contextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "retained"))
			defer cancel()
			watch, stop := context.WithCancel(t.Context())
			defer stop()
			atInterruption := make(chan string, 1)
			go func() {
				select {
				case <-started:
					data, err := os.ReadFile(trace)
					if err != nil {
						t.Error(err)
					}
					atInterruption <- string(data)
					cancel()
				case <-watch.Done():
				}
			}()
			rejected := errors.New("interrupted acquisition manifest rejected")
			calls, post := 0, 0
			index, err := moduledependency.Discover(ctx, locate(t, stage.Root()), moduledependency.Options{
				Environment: environment, GoCommand: command, RequireSourceChecksums: true,
				RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
					calls++
					if len(manifests) != 1 || manifests[0].Path != fixture.downloaded.GoMod || manifests[0].Checksum != fixture.downloaded.GoModSum || captureCtx.Err() != nil || captureCtx.Value(contextKey{}) != "retained" {
						t.Fatal("interrupted acquisition lost original manifest authority or operation context")
					}
					if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: manifests[0].Path, GoModChecksum: manifests[0].Checksum}}); err != nil {
						return err
					}
					if ctx.Err() != nil {
						post++
						deadline, bounded := captureCtx.Deadline()
						if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
							t.Fatal("interrupted capture lost its independent bounded budget")
						}
						path := filepath.Join(stage.Root(), "go.mod")
						writeFile(t, path, string(readFile(t, path))+"// concurrent edit\n")
						return rejected
					}
					return nil
				},
			})
			if calls != 2 || post != 1 || reached.Load() != 1 || len(index.Modules()) != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, rejected) || !errors.Is(err, moduledependency.ErrConcurrentChange) || !errors.Is(err, moduledependency.ErrModuleUnavailable) {
				t.Fatalf("interrupted acquisition lost joined failure evidence: calls=%d post=%d reached=%d error=%v", calls, post, reached.Load(), err)
			}
			select {
			case commands := <-atInterruption:
				if commands != string(readFile(t, trace)) {
					t.Fatal("manifest cleanup started new Go work after cancellation")
				}
			default:
				t.Fatal("acquisition was not interrupted")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("interrupted evidence failed after cleanup", err)
			}
			rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
		})
	}
}

func coldDiscoveryAcquisition(t *testing.T, workspace bool) warmPreparationFixture {
	t.Helper()
	fixture := warmPreparation(t, workspace, false)
	project := filepath.Join(fixture.app, "go.mod")
	writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
	clearAcquisitionSource(t, fixture.cache, fixture.downloaded.Dir, fixture.downloaded.GoMod)
	return fixture
}

func clearAcquisitionSource(t *testing.T, cache, directory, manifest string) {
	t.Helper()
	relative, err := filepath.Rel(cache, directory)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		t.Fatal("source removal escaped the isolated cache")
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(manifest, ".mod")
	for _, suffix := range []string{".mod", ".info", ".zip", ".ziphash"} {
		if err := os.Remove(prefix + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}

func rejectAcquisitionManifestReplacement(t *testing.T, inputs projectstage.Inputs, path string) {
	t.Helper()
	data := readFile(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(t.TempDir(), "original.mod")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
	if err := errors.Join(os.Chmod(path, info.Mode()), os.Chtimes(path, info.ModTime(), info.ModTime())); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("consumed manifest identity replacement survived cleanup: %v", err)
	}
}
