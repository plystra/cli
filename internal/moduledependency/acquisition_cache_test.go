package moduledependency_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestPrepareSourcesAcquisitionCache(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"success", "offline", "rejected", "cancelled", "relative-cache", "callback-copy", "callback-drift", "checksum-service", "toolchain-auto"} {
			phase := "module/"
			if workspace {
				phase = "workspace/"
			}
			t.Run(phase+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
				if err != nil || !filepath.IsLocal(relative) || relative == "." {
					t.Fatal("source removal escaped cache")
				}
				if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
					t.Fatal(err)
				}
				if mode == "checksum-service" {
					fixture.environment = append(fixture.environment, "GOSUMDB=sum.golang.org")
				}
				if mode == "toolchain-auto" {
					fixture.environment = append(fixture.environment, "GOTOOLCHAIN=auto")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("reject acquisition cache")
				calls := 0
				options := moduledependency.Options{GoCommand: command, Environment: environment, Offline: mode == "offline",
					AcquisitionCache: func(captureCtx context.Context, cache string, versions []module.Version) (string, error) {
						calls++
						if cache != fixture.cache || len(versions) != 1 || versions[0] != (module.Version{Path: warmSourceModule, Version: "v1.0.0"}) {
							t.Fatal("cache callback lost effective escaped replacement identity")
						}
						switch mode {
						case "rejected":
							return "", rejected
						case "cancelled":
							cancel()
							return cache, nil
						case "relative-cache":
							return "relative", nil
						case "callback-drift":
							writeFile(t, filepath.Join(stage.Root(), "go.mod"), "module example.com/changed\ngo 1.26\n")
							return cache, nil
						}
						isolated, err := stage.AcquisitionCache(captureCtx, cache, versions)
						if mode == "callback-copy" {
							versions[0] = module.Version{}
						}
						return isolated, err
					},
				}
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options,
					func(sources []moduledependency.Module) error { return retainWarmBundle(t, stage, ctx, sources) })
				failed := mode == "rejected" || mode == "cancelled" || mode == "relative-cache" || mode == "callback-drift"
				if failed {
					if err == nil || mode == "rejected" && !errors.Is(err, rejected) || mode == "cancelled" && !errors.Is(err, context.Canceled) || mode == "relative-cache" && !errors.Is(err, moduledependency.ErrInvalidOutput) || mode == "callback-drift" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatal("cache callback failure was not preserved", err)
					}
					if strings.Contains(string(readFile(t, trace)), "mod download ") {
						t.Fatal("Go started after cache callback rejection")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				skipped := mode == "checksum-service" || mode == "toolchain-auto"
				if (calls == 0) != skipped {
					t.Fatal("unsupported acquisition environment did not preserve the original cache")
				}
				_, statErr := os.Stat(fixture.downloaded.Dir)
				if skipped && statErr != nil || !skipped && !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("exact acquisition selected the wrong consuming cache", statErr)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("acquired source evidence failed after cleanup", err)
				}
			})
		}
	}
}

func TestDiscoverKeepsAcquisitionCacheDuringChecksumResolution(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, multiple := range []bool{false, true} {
			name := "one-source"
			if multiple {
				name = "two-sources"
			}
			if workspace {
				name = "workspace/" + name
			} else {
				name = "module/" + name
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, false)
				writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				project := filepath.Join(fixture.app, "go.mod")
				writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				if multiple {
					const later = "example.com/zz-later"
					writeModuleProxy(t, fixture.root, later, "v1.0.0")
					writeFile(t, project, string(readFile(t, project))+"require "+later+" v1.0.0\n")
					manifest := readFile(t, filepath.Join(fixture.proxy, filepath.FromSlash(later), "@v", "v1.0.0.mod"))
					checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(manifest)), nil })
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, fixture.authority, string(readFile(t, fixture.authority))+later+" v1.0.0/go.mod "+checksum+"\n")
				}
				relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
				if err != nil || !filepath.IsLocal(relative) || relative == "." {
					t.Fatal("source removal escaped cache")
				}
				if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
					t.Fatal(err)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				options := moduledependency.Options{Environment: stage.Environment(), RequireSourceChecksums: true,
					AcquisitionCache: stage.AcquisitionCache,
					RetainSelectedSources: func(sources []moduledependency.Module) error {
						return retainWarmBundle(t, stage, t.Context(), sources)
					},
				}
				index, err := moduledependency.Discover(t.Context(), locate(t, stage.Root()), options)
				if err != nil {
					t.Fatal("private sources lost identity during workspace checksum normalization", err)
				}
				private := 0
				for _, source := range index.Modules() {
					if source.Workspace() {
						continue
					}
					if replacement, ok := source.Replacement(); ok && replacement.Local() {
						continue
					}
					if strings.HasPrefix(source.SourceDirectory(), fixture.cache+string(filepath.Separator)) {
						t.Fatal("discovery discarded its private source")
					}
					private++
				}
				expected := 1
				if multiple {
					expected = 2
				}
				if private != expected || stage.CheckSources(t.Context()) != nil {
					t.Fatal("private discovered sources were not authenticated")
				}
				if _, err := os.Stat(fixture.downloaded.Dir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("workspace checksum normalization populated original source", err)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
