package moduledependency_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesRetainsInterruptedMetadata(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"cancel", "deadline", "reject-manifest", "reject-information", "combined", "unrecorded", "information-only", "manifest-only", "before-start", "wrapped-cancel", "wrapped-deadline", "unsettled", "exhausted-budget", "raw-manifest", "reject-raw-manifest", "raw-combined"} {
			if phase == "workspace" && (mode == "reject-raw-manifest" || mode == "raw-combined") {
				continue // Workspace selection already retains this warm manifest.
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				raw := strings.Contains(mode, "raw-")
				combined := mode == "combined" || mode == "raw-combined"
				wrapped := strings.HasPrefix(mode, "wrapped-") || mode == "unsettled"
				mode = strings.TrimPrefix(mode, "wrapped-")
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", false)
				if err := os.RemoveAll(fixture.cache); err != nil {
					t.Fatal(err)
				}
				if mode == "unrecorded" || raw {
					writeFile(t, fixture.authority, "")
				}
				const second = "example.com/second"
				writeModuleProxy(t, fixture.root, second, "v1.0.0", "module "+second+"\ngo 1.26\n")
				if phase == "workspace" {
					writeModule(t, filepath.Join(fixture.root, "local-second"), second)
					path := filepath.Join(fixture.app, "go.mod")
					writeFile(t, path, string(readFile(t, path))+"replace "+second+" => ../local-second\n")
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+second+" => "+second+" v1.0.0\n")
				}
				started := make(chan struct{}, 1)
				abandoned := filepath.Join(fixture.root, "abandoned-download")
				var reached, later atomic.Int32
				files := http.FileServer(http.Dir(fixture.proxy))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, ".zip") {
						if strings.Contains(r.URL.Path, second+"/") {
							later.Add(1)
							files.ServeHTTP(w, r)
							return
						}
						reached.Add(1)
						if mode == "unsettled" {
							if err := os.WriteFile(abandoned+".exit", nil, 0o600); err != nil {
								t.Error(err)
							}
						}
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
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				command, trace := "", ""
				environment = stage.Environment()
				if wrapped {
					command, environment, trace = tracePreparation(t, fixture.root, environment)
				}
				if mode == "unsettled" {
					environment = append(environment, "PLYSTRA_MODULE_ABANDON_DOWNLOAD="+abandoned)
					t.Cleanup(func() {
						pid, err := strconv.Atoi(string(readFile(t, abandoned)))
						if err != nil || pid <= 0 {
							t.Error("invalid owned Go process identity")
							return
						}
						if process, err := os.FindProcess(pid); err == nil {
							_ = process.Kill()
							_ = process.Release()
						}
					})
				}
				type contextKey struct{}
				parent := context.WithValue(t.Context(), contextKey{}, "retained")
				ctx, cancel := context.WithCancel(parent)
				if mode == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(parent, 10*time.Second)
				}
				if mode == "before-start" {
					cancel()
				}
				defer cancel()
				watch, stop := context.WithCancel(t.Context())
				defer stop()
				commandsAtInterruption := make(chan []byte, 1)
				go func() {
					select {
					case <-started:
						if wrapped {
							data, err := os.ReadFile(trace)
							if err != nil {
								t.Error(err)
							}
							commandsAtInterruption <- data
						}
						if mode != "deadline" && mode != "unsettled" {
							cancel()
						}
					case <-watch.Done():
					}
				}()
				manifests, information, rawManifests := 0, 0, 0
				var retentionContext context.Context
				manifestRejected, informationRejected := errors.New("manifest retention rejected"), errors.New("information retention rejected")
				checkContext := func(captureCtx context.Context) {
					t.Helper()
					if captureCtx.Value(contextKey{}) != "retained" || captureCtx.Err() != nil {
						t.Fatal("retention lost context values or inherited cancellation")
					}
					if ctx.Err() != nil {
						if retentionContext != nil && retentionContext != captureCtx {
							t.Fatal("metadata captures did not share one cleanup context")
						}
						retentionContext = captureCtx
						deadline, bounded := captureCtx.Deadline()
						if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
							t.Fatal("post-download retention has no bounded independent budget")
						}
					}
				}
				options := moduledependency.Options{Environment: environment, GoCommand: command,
					RetainRootManifests: func(captureCtx context.Context, inputs []moduledependency.RootManifest) error {
						checkContext(captureCtx)
						manifests += len(inputs)
						if len(inputs) != 1 || inputs[0].Path != fixture.downloaded.GoMod || inputs[0].Checksum != fixture.downloaded.GoModSum {
							t.Fatalf("interrupted manifest authority changed: %v", inputs)
						}
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: inputs[0].Path, GoModChecksum: inputs[0].Checksum}}); err != nil {
							return err
						}
						if ctx.Err() != nil && mode == "exhausted-budget" {
							<-captureCtx.Done()
							return captureCtx.Err()
						}
						if ctx.Err() != nil && combined {
							path := filepath.Join(stage.Root(), "go.mod")
							writeFile(t, path, string(readFile(t, path))+"// concurrent metadata edit\n")
						}
						if ctx.Err() != nil && (mode == "reject-manifest" || combined) {
							return manifestRejected
						}
						return nil
					},
					RetainModuleInformation: func(captureCtx context.Context, paths []string) error {
						checkContext(captureCtx)
						information += len(paths)
						if len(paths) != 1 || paths[0] != strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info" {
							t.Fatalf("unexpected interrupted information path: %v", paths)
						}
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if ctx.Err() != nil && (mode == "reject-information" || combined) {
							return informationRejected
						}
						return nil
					},
				}
				if raw {
					options.RetainRootManifests = nil
					options.RetainManifestFiles = func(captureCtx context.Context, paths []string) error {
						checkContext(captureCtx)
						rawManifests++
						if len(paths) != 1 || paths[0] != fixture.downloaded.GoMod {
							t.Fatal("interrupted raw manifest lost its physical identity")
						}
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if ctx.Err() != nil && combined {
							path := filepath.Join(stage.Root(), "go.mod")
							writeFile(t, path, string(readFile(t, path))+"// concurrent metadata edit\n")
						}
						if ctx.Err() != nil && (mode == "reject-raw-manifest" || combined) {
							return manifestRejected
						}
						return nil
					}
				}
				if mode == "manifest-only" {
					options.RetainModuleInformation = nil
				} else if mode == "information-only" {
					options.RetainRootManifests = nil
				}
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}, {Path: second, Version: "v1.0.0"}}, options, func(sources []moduledependency.Module) error {
					if len(sources) != 0 {
						t.Fatal("interrupted acquisition delivered a source")
					}
					return nil
				})
				wantManifest, wantInformation := 1, 1
				if phase == "workspace" {
					wantInformation++
					wantManifest++
				}
				if mode == "manifest-only" {
					wantInformation = 0
				} else if mode == "information-only" || mode == "unrecorded" || raw {
					wantManifest = 0
				}
				if mode == "before-start" {
					wantManifest, wantInformation = 0, 0
				}
				if mode == "unsettled" {
					wantManifest, wantInformation = 0, 0
					if phase == "workspace" {
						wantInformation = 1
						wantManifest = 1
					}
				}
				if mode == "exhausted-budget" {
					wantInformation--
				}
				wantError := context.Canceled
				if mode == "deadline" {
					wantError = context.DeadlineExceeded
				}
				if mode == "unsettled" {
					wantError = gocommand.ErrProcessUnsettled
				}
				if !errors.Is(err, wantError) || manifests != wantManifest || information != wantInformation || later.Load() != 0 || mode != "before-start" && reached.Load() == 0 || mode == "before-start" && reached.Load() != 0 {
					t.Fatalf("interrupted metadata lost: manifests=%d information=%d, %v", manifests, information, err)
				}
				if raw && rawManifests != 1 {
					t.Fatal("interrupted download lost its raw manifest", rawManifests)
				}
				if mode == "exhausted-budget" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("cleanup budget exhaustion lost: %v", err)
				}
				if (mode == "reject-manifest" || mode == "reject-raw-manifest" || combined) && !errors.Is(err, manifestRejected) || (mode == "reject-information" || combined) && !errors.Is(err, informationRejected) || combined && !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("interrupted retention failure or metadata drift lost: %v", err)
				}
				if wrapped && !bytes.Equal(<-commandsAtInterruption, readFile(t, trace)) {
					t.Fatal("interrupted acquisition started another Go command")
				}
				if mode == "unsettled" {
					return
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("interrupted metadata did not survive cleanup: %v", err)
				}
				path := fixture.downloaded.GoMod
				if mode == "information-only" || mode == "unrecorded" {
					path = strings.TrimSuffix(path, ".mod") + ".info"
				}
				if mode == "before-start" {
					return
				}
				if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("interrupted manifest identity was not retained: %v", err)
				}
			})
		}
	}
}
