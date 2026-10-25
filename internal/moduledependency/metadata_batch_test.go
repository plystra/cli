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
	"golang.org/x/mod/module"
)

func TestPrepareSourcesBatchesSettledMetadata(t *testing.T) {
	for _, mode := range []string{"success", "download-failure", "capture-failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, false, false)
			relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
			if err != nil || !filepath.IsLocal(relative) || relative == "." {
				t.Fatal("source removal escaped isolated fixture cache")
			}
			if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{fixture.downloaded.GoMod, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".info"} {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
			if mode == "download-failure" {
				environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=command-error")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type batchKey struct{}
			var events []string
			batches, inside := 0, false
			rejected := errors.New("metadata capture rejected")
			record := func(captureCtx context.Context, label string) error {
				if !inside {
					return nil
				}
				if captureCtx.Value(batchKey{}) != true || captureCtx.Err() != nil {
					t.Fatal("metadata callback lost the batch context")
				}
				events = append(events, label)
				if mode == "capture-failure" && label == "information" {
					return rejected
				}
				return nil
			}
			err = moduledependency.PrepareSources(ctx, locate(t, fixture.app), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{
				GoCommand: command, Environment: environment,
				MetadataBatch: func(captureCtx context.Context, capture func(context.Context) error) error {
					batches++
					before := string(readFile(t, trace))
					deadline, bounded := captureCtx.Deadline()
					if !strings.Contains(before, "mod download ") || !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatal("batch is not after a settled download within the fixed capture deadline")
					}
					if mode == "cancelled" {
						cancel()
					}
					inside = true
					err := capture(context.WithValue(captureCtx, batchKey{}, true))
					inside = false
					if string(readFile(t, trace)) != before {
						t.Fatal("metadata batching started new Go work")
					}
					return err
				},
				RetainRootManifests:     func(ctx context.Context, _ []moduledependency.RootManifest) error { return record(ctx, "manifest") },
				RetainModuleArchives:    func(ctx context.Context, _ []moduledependency.ModuleArchive) error { return record(ctx, "archive") },
				RetainManifestFiles:     func(ctx context.Context, _ []string) error { return record(ctx, "files") },
				RetainModuleInformation: func(ctx context.Context, _ []string) error { return record(ctx, "information") },
			}, func(context.Context, []moduledependency.Module) error {
				if inside {
					t.Fatal("source retention moved inside the metadata group")
				}
				return nil
			})
			if batches != 1 || !reflect.DeepEqual(events, []string{"manifest", "archive", "files", "information"}) {
				t.Fatalf("metadata order changed: batches=%d events=%v err=%v", batches, events, err)
			}
			if mode == "success" && err != nil || mode == "download-failure" && !errors.Is(err, moduledependency.ErrModuleUnavailable) ||
				mode == "capture-failure" && !errors.Is(err, rejected) || mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("batch lost the operation outcome", err)
			}
		})
	}
}
