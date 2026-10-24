package moduledependency_test

import (
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

func TestPrepareSourcesRetainsArchiveAuthorizedWarmTrees(t *testing.T) {
	for _, phase := range []string{"module", "workspace", "listed-module", "listed-workspace"} {
		discover := strings.HasPrefix(phase, "listed-")
		missingKinds := []string{"manifest", "checksum-file", "manifest-authority"}
		outcomes := []string{"source", "source-manifest", "unchanged", "failed-acquisition", "rejected", "cancelled"}
		if discover {
			missingKinds, outcomes = []string{"metadata-files"}, outcomes[:3]
		}
		for _, missing := range missingKinds {
			for _, changed := range outcomes {
				t.Run(phase+"/"+missing+"/"+changed, func(t *testing.T) {
					t.Parallel()
					fixture := warmPreparation(t, strings.Contains(phase, "workspace"), false)
					if discover {
						path := filepath.Join(fixture.app, "go.mod")
						writeFile(t, path, string(readFile(t, path))+"require "+warmRequestedModule+" v1.0.0\n")
					}
					switch missing {
					case "manifest", "checksum-file", "metadata-files":
						suffixes := []string{".mod", ".ziphash"}
						if missing == "checksum-file" {
							suffixes = suffixes[1:]
						} else if missing == "manifest" {
							suffixes = suffixes[:1]
						}
						for _, suffix := range suffixes {
							if err := os.Remove(strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + suffix); err != nil {
								t.Fatal(err)
							}
						}
					case "manifest-authority":
						writeFile(t, fixture.authority, warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n")
					}
					before := readFile(t, fixture.authority)
					stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = stage.Close() })
					command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
					path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
					if changed == "source-manifest" {
						path = filepath.Join(fixture.downloaded.Dir, "go.mod")
					}
					marker := filepath.Join(fixture.root, "changed-after-download")
					if changed == "source" || changed == "source-manifest" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
					} else if changed == "failed-acquisition" {
						environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT=download-error")
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					rejected := errors.New("archive-authorized source rejected")
					calls := 0
					retain := func(sources []moduledependency.Module) error {
						if len(sources) == 0 {
							return stage.Recheck(ctx)
						}
						calls++
						if calls == 1 && strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
							t.Fatal("archive-authorized warm tree first captured after Go acquisition")
						}
						if calls == 1 && (!sources[0].IncompleteSourceMetadata() || discover && !strings.Contains(string(readFile(t, trace)), "list -m ")) {
							t.Fatal("incorrect incomplete-source boundary")
						}
						if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
							return err
						}
						if calls == 1 {
							if changed == "rejected" {
								return rejected
							} else if changed == "cancelled" {
								cancel()
							}
						}
						return nil
					}
					options := moduledependency.Options{Environment: environment, GoCommand: command}
					if discover {
						options.RetainSelectedSources, options.RequireSourceChecksums = retain, true
						_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
					} else {
						err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options, retain)
					}
					switch changed {
					case "unchanged":
						if err != nil || calls < 2 {
							t.Fatalf("unchanged warm tree: calls=%d, error=%v", calls, err)
						}
					case "failed-acquisition":
						if !errors.Is(err, moduledependency.ErrSourceIntegrity) || calls == 0 {
							t.Fatalf("failed acquisition lost prior tree evidence: calls=%d, error=%v", calls, err)
						}
					case "rejected", "cancelled":
						want := rejected
						if changed == "cancelled" {
							want = context.Canceled
						}
						if !errors.Is(err, want) || calls != 1 || strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
							t.Fatalf("rejected warm tree reached acquisition: calls=%d, error=%v", calls, err)
						}
					default:
						if !errors.Is(err, projectstage.ErrConcurrentChange) {
							t.Fatalf("post-acquisition identical-byte replacement accepted: calls=%d, error=%v", calls, err)
						}
						if _, err := os.Stat(marker); err != nil {
							t.Fatal("fixture did not replace source identity after real Go acquisition")
						}
					}
					if string(readFile(t, fixture.authority)) != string(before) {
						t.Fatal("source retention changed original checksum authority")
					}
					inputs := stage.Inputs()
					if err := stage.Close(); err != nil {
						t.Fatal(err)
					}
					if changed != "source" && changed != "source-manifest" {
						if err := inputs.Recheck(t.Context()); err != nil {
							t.Fatalf("unchanged source evidence rejected after cleanup: %v", err)
						}
						info, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						data := readFile(t, path)
						if err := os.Rename(path, filepath.Join(t.TempDir(), "original-source")); err != nil {
							t.Fatal(err)
						}
						writeFile(t, path, string(data))
						if err := errors.Join(os.Chmod(path, info.Mode()), os.Chtimes(path, info.ModTime(), info.ModTime())); err != nil {
							t.Fatal(err)
						}
					}
					if !errors.Is(inputs.Recheck(context.Background()), projectstage.ErrConcurrentChange) {
						t.Fatal("source evidence lost after cleanup")
					}
				})
			}
		}
	}
}
