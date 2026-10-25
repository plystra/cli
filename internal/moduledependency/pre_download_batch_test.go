package moduledependency_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

type preDownloadWalkContext struct {
	context.Context
	polls *atomic.Int64
}

func (c preDownloadWalkContext) Err() error {
	c.polls.Add(1)
	return c.Context.Err()
}

func TestPrepareSourcesBatchesPreDownloadMetadata(t *testing.T) {
	var separate, batched int64
	for _, mode := range []string{"unbatched", "success", "root-only", "rejected", "masked-error", "skipped", "cancelled", "metadata-drift", "source-drift", "panic"} {
		t.Run(mode, func(t *testing.T) {
			fixture := warmPreparation(t, false, false)
			relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				t.Fatal("source removal escaped isolated fixture cache")
			}
			if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
				t.Fatal(err)
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			source := t.TempDir()
			for index := range 64 {
				writeFile(t, filepath.Join(source, fmt.Sprintf("value%d.go", index)), "package retained\n")
			}
			if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
				t.Fatal(err)
			}
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			type batchKey struct{}
			type callerKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), callerKey{}, true))
			defer cancel()
			var polls atomic.Int64
			var events []string
			batches, sources := 0, 0
			recording := false
			rejected := errors.New("pre-download capture rejected")
			information := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".info"
			retainFiles := func(captureCtx context.Context, label string, inputs []projectstage.MetadataInput) error {
				beforeDownload := !strings.Contains(string(readFile(t, trace)), "mod download ")
				if beforeDownload && label == "information" {
					recording = true
				}
				if !beforeDownload || !recording {
					return stage.RetainMetadata(captureCtx, inputs)
				}
				events = append(events, label)
				var captureErr error
				if mode == "unbatched" {
					captureErr = stage.RetainMetadata(preDownloadWalkContext{captureCtx, &polls}, inputs)
				} else {
					retain, inside := captureCtx.Value(batchKey{}).(projectstage.MetadataRetention)
					if !inside || captureCtx.Value(callerKey{}) != true {
						t.Fatal("pre-download metadata escaped its batch context")
					}
					captureErr = retain.Files(inputs)
				}
				if captureErr != nil || label != "files" {
					return captureErr
				}
				switch mode {
				case "rejected", "masked-error":
					return rejected
				case "cancelled":
					cancel()
				case "metadata-drift":
					return os.Rename(information, information+".prior")
				case "source-drift":
					writeFile(t, filepath.Join(source, "value0.go"), "package changed\n")
				case "panic":
					panic(rejected)
				}
				return nil
			}
			options := moduledependency.Options{
				GoCommand: command, Environment: environment,
				RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
					inputs := make([]projectstage.MetadataInput, len(manifests))
					for index, manifest := range manifests {
						inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
					}
					return retainFiles(captureCtx, "manifest", inputs)
				},
			}
			paths := func(label string) func(context.Context, []string) error {
				return func(captureCtx context.Context, paths []string) error {
					inputs := make([]projectstage.MetadataInput, len(paths))
					for index, path := range paths {
						inputs[index].Path = path
					}
					return retainFiles(captureCtx, label, inputs)
				}
			}
			if mode != "root-only" {
				options.RetainModuleInformation, options.RetainManifestFiles = paths("information"), paths("files")
			}
			if mode != "unbatched" {
				options.MetadataBatch = func(captureCtx context.Context, capture func(context.Context) error) error {
					before := string(readFile(t, trace))
					if strings.Contains(before, "mod download ") {
						return capture(captureCtx) // The settled phase is covered separately.
					}
					batches++
					deadline, bounded := captureCtx.Deadline()
					if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatal("pre-download batch lost its fixed capture budget")
					}
					if mode == "skipped" {
						return nil
					}
					recording = true
					measured := preDownloadWalkContext{captureCtx, &polls}
					captureErr := stage.WithMetadataBatch(measured, func(retain projectstage.MetadataRetention) error {
						return capture(context.WithValue(measured, batchKey{}, retain))
					})
					if string(readFile(t, trace)) != before {
						t.Fatal("pre-download batch started Go work")
					}
					if mode == "masked-error" {
						return nil
					}
					return captureErr
				}
			}
			panicked := false
			func() {
				defer func() { panicked = recover() == rejected }()
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options,
					func(context.Context, []moduledependency.Module) error { sources++; return nil })
			}()
			wantEvents := []string{"information", "manifest", "files"}
			if mode == "root-only" {
				wantEvents = []string{"manifest"}
			}
			if mode != "unbatched" && batches != 1 || mode != "skipped" && !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("pre-download capture changed: batches=%d events=%v err=%v", batches, events, err)
			}
			success := mode == "success" || mode == "unbatched" || mode == "root-only"
			if downloaded := strings.Contains(string(readFile(t, trace)), "mod download "); downloaded != success || sources != 0 && !success {
				t.Fatal("pre-download rejection allowed acquisition or source validation", err)
			}
			drift := mode == "metadata-drift" || mode == "source-drift"
			if success && (err != nil || sources != 1) || (mode == "rejected" || mode == "masked-error") && !errors.Is(err, rejected) ||
				mode == "skipped" && !errors.Is(err, moduledependency.ErrSourceIntegrity) || mode == "cancelled" && !errors.Is(err, context.Canceled) ||
				drift && !errors.Is(err, projectstage.ErrConcurrentChange) || panicked != (mode == "panic") {
				t.Fatal("pre-download capture lost its outcome", err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); drift && !errors.Is(err, projectstage.ErrConcurrentChange) || !drift && err != nil {
				t.Fatal("pre-download evidence changed after cleanup", err)
			}
			if !drift {
				if err := os.Rename(fixture.downloaded.GoMod, fixture.downloaded.GoMod+".prior"); err != nil {
					t.Fatal(err)
				}
				if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
					t.Fatal("pre-download failure discarded retained evidence")
				}
			}
			if mode == "unbatched" {
				separate = polls.Load()
			} else if mode == "success" {
				batched = polls.Load()
			}
		})
	}
	t.Logf("pre-download cancellation polls: separate=%d batched=%d", separate, batched)
	if batched == 0 || separate < 2*batched {
		t.Fatal("pre-download callbacks still repeat complete source traversal")
	}
}
