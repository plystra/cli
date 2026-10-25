package moduledependency_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesBatchesSettledSources(t *testing.T) {
	for _, mode := range []string{"success", "download-failure", "metadata-failure", "source-failure", "cancel-before-source", "cancel-during-source", "trailing-json"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, false, false)
			relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				t.Fatal("source removal escaped the isolated fixture")
			}
			if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{".mod", ".info"} {
				if err := os.Remove(strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + suffix); err != nil {
					t.Fatal(err)
				}
			}
			const later = "example.com/zz-later"
			writeModuleProxy(t, fixture.root, later, "v1.0.0")
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			if mode == "download-failure" {
				environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=command-error")
			} else if mode == "trailing-json" {
				environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=trailing-json")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type batchKey struct{}
			var events []string
			batches, sources := 0, 0
			rejected := errors.New("capture rejected")
			files := func(captureCtx context.Context, label string, inputs []projectstage.MetadataInput) error {
				if retain, ok := captureCtx.Value(batchKey{}).(projectstage.SourceRetention); ok {
					if batches == 1 {
						events = append(events, label)
					}
					err := retain.Files(inputs)
					if mode == "metadata-failure" && label == "information" {
						err = errors.Join(err, rejected)
					}
					return err
				}
				return stage.RetainMetadata(captureCtx, inputs)
			}
			paths := func(captureCtx context.Context, label string, paths []string) error {
				inputs := make([]projectstage.MetadataInput, len(paths))
				for index, path := range paths {
					inputs[index].Path = path
				}
				return files(captureCtx, label, inputs)
			}
			err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}, {Path: later, Version: "v1.0.0"}}, moduledependency.Options{
				GoCommand: command, Environment: environment,
				MetadataBatch: func(context.Context, func(context.Context) error) error {
					t.Fatal("source batching fell back to metadata-only batching")
					return nil
				},
				SourceBatch: func(captureCtx context.Context, capture func(context.Context) error) error {
					batches++
					before := string(readFile(t, trace))
					deadline, bounded := captureCtx.Deadline()
					if !strings.Contains(before, "mod download ") || !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatal("source batch crossed the settled-download boundary or capture budget")
					}
					if mode == "cancel-before-source" {
						cancel()
					}
					err := stage.WithSourceBatch(captureCtx, func(retain projectstage.SourceRetention) error {
						return capture(context.WithValue(captureCtx, batchKey{}, retain))
					})
					if string(readFile(t, trace)) != before {
						t.Fatal("source batch started Go work")
					}
					return err
				},
				RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
					inputs := make([]projectstage.MetadataInput, len(manifests))
					for index, manifest := range manifests {
						inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
					}
					return files(captureCtx, "manifest", inputs)
				},
				RetainManifestFiles: func(captureCtx context.Context, inputs []string) error { return paths(captureCtx, "files", inputs) },
				RetainModuleInformation: func(captureCtx context.Context, inputs []string) error {
					return paths(captureCtx, "information", inputs)
				},
			}, func(captureCtx context.Context, selected []moduledependency.Module) error {
				retain, inside := captureCtx.Value(batchKey{}).(projectstage.SourceRetention)
				if !inside || captureCtx.Err() != nil {
					t.Fatal("acquired source callback lost its bounded batch context")
				}
				sources++
				if sources == 1 {
					events = append(events, "sources")
					if strings.Contains(string(readFile(t, trace)), "mod download -json "+later+"@v1.0.0") {
						t.Fatal("source evidence was delayed past the next acquisition")
					}
				}
				for _, source := range selected {
					if len(source.ProjectGoMod()) != 0 {
						t.Fatal("batch consumed an extracted manifest before retention")
					}
					version := module.Version{Path: source.Path(), Version: source.SelectedVersion()}
					if replacement, ok := source.Replacement(); ok {
						version = module.Version{Path: replacement.Path(), Version: replacement.Version()}
					}
					if err := retain.Sources([]projectstage.Source{{Directory: source.SourceDirectory(), Module: version, Checksum: source.Checksum()}}); err != nil {
						return err
					}
				}
				if mode == "source-failure" {
					return rejected
				}
				if mode == "cancel-during-source" {
					cancel()
				}
				return nil
			})
			wantEvents := []string{"manifest", "files", "information"}
			wantSources := 0
			if mode == "success" || mode == "source-failure" || mode == "cancel-during-source" {
				wantEvents = append(wantEvents, "sources")
				wantSources = 1
			}
			if mode == "success" {
				wantSources = 2
			}
			if !reflect.DeepEqual(events, wantEvents) || sources != wantSources || batches != 1 && !(mode == "success" && batches == 2) {
				t.Fatalf("capture boundary changed: batches=%d sources=%d events=%v error=%v", batches, sources, events, err)
			}
			if mode == "success" && err != nil || mode == "download-failure" && !errors.Is(err, moduledependency.ErrModuleUnavailable) ||
				(mode == "metadata-failure" || mode == "source-failure") && !errors.Is(err, rejected) ||
				strings.HasPrefix(mode, "cancel-") && !errors.Is(err, context.Canceled) || mode == "trailing-json" && !errors.Is(err, moduledependency.ErrInvalidOutput) {
				t.Fatalf("capture lost failure classification: %v", err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("unchanged evidence was lost after cleanup", err)
			}
			path := fixture.downloaded.GoMod
			if sources != 0 {
				path = filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
				t.Fatal("completed capture evidence did not survive failure and cleanup")
			}
		})
	}
}
