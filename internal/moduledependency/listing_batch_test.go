package moduledependency_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
)

func TestDiscoverBatchesSettledListingManifests(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		phase := "module"
		if workspace {
			phase = "workspace"
		}
		for _, mode := range []string{"success", "command-failure", "cancelled", "masked-error", "skipped", "drift"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				manifest := filepath.Join(fixture.app, "go.mod")
				writeFile(t, manifest, string(readFile(t, manifest))+"require "+warmRequestedModule+" v1.0.0\n")
				if workspace {
					writeFile(t, fixture.authority, "")
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				}
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				if mode == "command-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_FAIL=1")
				}
				type batchKey struct{}
				type callerKey struct{}
				ctx, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, true))
				defer cancel()
				rejected := errors.New("listing capture rejected")
				batches, callbacks := 0, 0
				retainFiles := func(captureCtx context.Context, files []projectstage.MetadataInput) error {
					retain, inside := captureCtx.Value(batchKey{}).(projectstage.MetadataRetention)
					if !inside || captureCtx.Err() != nil || captureCtx.Value(callerKey{}) != true {
						t.Fatal("settled listing capture escaped its live metadata batch")
					}
					callbacks++
					if err := retain.Files(files); err != nil {
						return err
					}
					if mode == "masked-error" {
						return rejected
					}
					if mode == "drift" {
						writeFile(t, fixture.downloaded.GoMod, string(readFile(t, fixture.downloaded.GoMod))+"\n")
					}
					return nil
				}
				_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), moduledependency.Options{
					GoCommand: command, Environment: environment,
					MetadataBatch: func(captureCtx context.Context, capture func(context.Context) error) error {
						before := string(readFile(t, trace))
						if !strings.Contains(before, "mod download ") {
							batches++ // Later exact acquisitions use separate metadata batches.
						}
						deadline, bounded := captureCtx.Deadline()
						if !strings.Contains(before, "list -m ") || !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
							t.Fatal("listing batch lost its settled-command boundary or fixed deadline")
						}
						if mode == "cancelled" {
							cancel()
						}
						if mode == "skipped" {
							return nil
						}
						captureErr := stage.WithMetadataBatch(captureCtx, func(retain projectstage.MetadataRetention) error {
							return capture(context.WithValue(captureCtx, batchKey{}, retain))
						})
						if string(readFile(t, trace)) != before {
							t.Fatal("listing metadata batch started Go work")
						}
						if mode == "masked-error" {
							return nil
						}
						return captureErr
					},
					RetainManifestFiles: func(captureCtx context.Context, paths []string) error {
						files := make([]projectstage.MetadataInput, len(paths))
						for index, path := range paths {
							files[index].Path = path
						}
						return retainFiles(captureCtx, files)
					},
					RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
						files := make([]projectstage.MetadataInput, len(manifests))
						for index, manifest := range manifests {
							files[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
						}
						return retainFiles(captureCtx, files)
					},
				})
				if batches != 1 || mode == "skipped" && callbacks != 0 || mode != "skipped" && callbacks == 0 {
					t.Fatalf("listing batch lost capture: batches=%d callbacks=%d err=%v", batches, callbacks, err)
				}
				switch mode {
				case "success":
					if err != nil || !workspace && callbacks < 2 {
						t.Fatal("listing did not batch raw and authenticated manifests", callbacks, err)
					}
				case "command-failure":
					if !errors.Is(err, gocommand.ErrRun) {
						t.Fatal("listing batch lost command failure", err)
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("listing batch lost caller cancellation", err)
					}
				case "masked-error":
					if !errors.Is(err, rejected) {
						t.Fatal("listing batch discarded capture rejection", err)
					}
				case "skipped":
					if !errors.Is(err, moduledependency.ErrSourceIntegrity) {
						t.Fatal("listing batch omitted capture", err)
					}
				case "drift":
					if !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatal("listing batch accepted changed captured metadata", err)
					}
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "drift" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("listing drift evidence was lost after cleanup")
					}
				} else if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unchanged listing evidence was lost after cleanup", err)
				}
			})
		}
	}
}
