package moduledependency_test

import (
	"context"
	"errors"
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

func TestDiscoverRetainsMaterializedRootManifestsAfterListing(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		phase := "module"
		if workspace {
			phase = "workspace"
		}
		for _, mode := range []string{"success", "invalid-output", "command-failure", "rejected", "cancelled", "metadata-drift", "combined", "no-callback"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				project := filepath.Join(fixture.app, "go.mod")
				writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				if workspace {
					writeFile(t, fixture.authority, "")
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				}
				original := string(readFile(t, fixture.authority))
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				if mode == "invalid-output" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				} else if mode == "command-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_FAIL=1")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("post-list root retention rejected")
				calls := 0
				options := moduledependency.Options{GoCommand: command, Environment: environment,
					RetainManifestFiles: func(captureCtx context.Context, paths []string) error {
						calls++
						commands := string(readFile(t, trace))
						if captureCtx.Err() != nil || len(paths) != 1 || paths[0] != fixture.downloaded.GoMod || !strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
							t.Fatal("materialized root first retained after acquisition", commands)
						}
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if mode == "metadata-drift" || mode == "combined" {
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						}
						if mode == "cancelled" || mode == "combined" {
							cancel()
						}
						if mode == "rejected" || mode == "combined" {
							return rejected
						}
						return nil
					},
				}
				if mode == "no-callback" {
					options.RetainManifestFiles = nil
				}
				_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				wantCalls := 1
				if mode == "no-callback" {
					wantCalls = 0
				}
				if calls != wantCalls || string(readFile(t, fixture.authority)) != original {
					t.Fatalf("materialized root or original authority lost: calls=%d err=%v", calls, err)
				}
				switch mode {
				case "success", "no-callback":
					if err != nil {
						t.Fatal(err)
					}
				case "invalid-output":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatal("post-list capture lost decoding failure", err)
					}
				case "command-failure":
					if !errors.Is(err, gocommand.ErrRun) {
						t.Fatal("post-list capture lost command failure", err)
					}
				default:
					if (mode == "rejected" || mode == "combined") && !errors.Is(err, rejected) ||
						(mode == "cancelled" || mode == "combined") && !errors.Is(err, context.Canceled) ||
						(mode == "metadata-drift" || mode == "combined") && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatal("post-list capture lost callback failures", err)
					}
				}
				if mode != "success" && mode != "no-callback" && strings.Contains(string(readFile(t, trace)), "mod download ") {
					t.Fatal("failed listing retention reached acquisition")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("materialized manifest evidence lost after cleanup", err)
				}
				if calls != 0 {
					rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
				}
			})
		}
	}
}

func TestDiscoverRetainsRootManifestAfterInterruptedListing(t *testing.T) {
	for _, mode := range []string{"failed", "cancelled", "rejected-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			project := filepath.Join(fixture.app, "go.mod")
			writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
			writeFile(t, fixture.authority, "")
			escaped, err := module.EscapePath(warmSourceModule)
			if err != nil {
				t.Fatal(err)
			}
			const blocked = "example.com/blocked"
			writeFile(t, filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.mod"), "module "+warmSourceModule+"\ngo 1.26\nrequire "+blocked+" v1.0.0\n")
			if err := os.Remove(fixture.downloaded.GoMod); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{}, 1)
			var archives atomic.Int32
			files := http.FileServer(http.Dir(fixture.proxy))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".zip") {
					archives.Add(1)
				}
				if strings.HasPrefix(r.URL.Path, "/"+blocked+"/") {
					if mode == "failed" {
						http.NotFound(w, r)
						return
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
			t.Cleanup(func() { _ = stage.Close() })
			type contextKey struct{}
			ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), contextKey{}, "root-listing"), 15*time.Second)
			defer cancel()
			if mode != "failed" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			rejected := errors.New("interrupted root retention rejected")
			calls := 0
			_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), moduledependency.Options{Environment: stage.Environment(),
				RetainManifestFiles: func(captureCtx context.Context, paths []string) error {
					calls++
					deadline, bounded := captureCtx.Deadline()
					if captureCtx.Err() != nil || captureCtx.Value(contextKey{}) != "root-listing" || !bounded || time.Until(deadline) > 30*time.Second || len(paths) != 1 || paths[0] != fixture.downloaded.GoMod {
						t.Fatal("interrupted listing lost bounded retention context", paths)
					}
					if mode != "failed" && !errors.Is(ctx.Err(), context.Canceled) {
						t.Fatal("listing was not interrupted before retention")
					}
					if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
						return err
					}
					if mode == "rejected-cancelled" {
						return rejected
					}
					return nil
				},
			})
			if calls != 1 || archives.Load() != 0 || mode == "failed" && !errors.Is(err, gocommand.ErrRun) || mode != "failed" && !errors.Is(err, context.Canceled) || mode == "rejected-cancelled" && !errors.Is(err, rejected) {
				t.Fatalf("interrupted listing lost input or errors: calls=%d archives=%d err=%v", calls, archives.Load(), err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("interrupted listing evidence lost after cleanup", err)
			}
			rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
		})
	}
}
