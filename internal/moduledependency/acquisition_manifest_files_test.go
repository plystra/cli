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

func TestAcquisitionRetainsManifestFiles(t *testing.T) {
	for _, phase := range []string{"preparation", "workspace-preparation", "discovery", "workspace-discovery", "workspace-checksums"} {
		for _, mode := range []string{"identity", "parent-identity", "unchanged", "missing", "information", "failed", "pre-rejected", "pre-cancelled", "pre-drift", "pre-combined", "no-callback", "mutated-callback"} {
			if phase == "workspace-checksums" && mode == "failed" {
				continue // A complete warm source requires no archive fetch.
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				workspace := strings.HasPrefix(phase, "workspace-")
				fixture := warmPreparation(t, workspace, false)
				manifest := readFile(t, fixture.downloaded.GoMod)
				information := readFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info")
				if phase != "workspace-checksums" {
					clearAcquisitionSource(t, fixture.cache, fixture.downloaded.Dir, fixture.downloaded.GoMod)
				} else if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				if mode != "missing" && mode != "information" {
					writeFile(t, fixture.downloaded.GoMod, string(manifest))
				}
				if phase != "discovery" {
					writeFile(t, fixture.authority, "")
				}
				original := readFile(t, fixture.authority)
				if mode == "information" {
					writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info", string(information))
				}
				if mode == "failed" {
					escaped, err := module.EscapePath(warmSourceModule)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")); err != nil {
						t.Fatal(err)
					}
				}
				project := filepath.Join(fixture.app, "go.mod")
				discovery := strings.HasSuffix(phase, "discovery") || phase == "workspace-checksums"
				if discovery {
					writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				}
				replacements := project
				if workspace {
					replacements = fixture.work
				}
				// An unused differing alternative prevents a fixed-source proof
				// from retaining this file before ordinary Go selection.
				writeFile(t, replacements, string(readFile(t, replacements))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				marker := filepath.Join(fixture.root, "unrecorded-manifest-replaced")
				if strings.HasSuffix(mode, "identity") {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
					if mode == "parent-identity" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=parent-identity")
					}
				}
				calls := 0
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("raw manifest rejected")
				options := moduledependency.Options{GoCommand: command, Environment: environment,
					RetainRootManifests: func(context.Context, []moduledependency.RootManifest) error {
						t.Fatal("unrecorded manifest supplied checksum authority")
						return nil
					},
					RetainManifestFiles: func(ctx context.Context, paths []string) error {
						calls++
						post := strings.Contains(string(readFile(t, trace)), "mod download ")
						wantPost := mode == "missing" && phase == "preparation"
						if len(paths) != 1 || paths[0] != fixture.downloaded.GoMod || post != wantPost {
							t.Fatal("unrecorded manifest first captured after acquisition", paths)
						}
						if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
							return err
						}
						if mode == "pre-drift" || mode == "pre-combined" {
							writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						}
						if mode == "pre-cancelled" || mode == "pre-combined" {
							cancel()
						}
						if mode == "pre-rejected" || mode == "pre-combined" {
							return rejected
						}
						if mode == "mutated-callback" {
							paths[0] = "unrelated"
						}
						return nil
					},
				}
				if phase == "discovery" {
					// Readonly graph listing needs recorded sums; the raw callback
					// must still work independently of the authorized callback.
					options.RetainRootManifests = nil
				}
				if mode == "no-callback" {
					options.RetainManifestFiles = nil
				}
				if mode == "information" {
					options.RetainModuleInformation = func(_ context.Context, paths []string) error {
						for _, path := range paths {
							if path == strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info" {
								if _, err := os.Stat(fixture.downloaded.GoMod); errors.Is(err, os.ErrNotExist) {
									writeFile(t, fixture.downloaded.GoMod, string(manifest))
								} else if err != nil {
									t.Fatal(err)
								}
							}
						}
						return nil
					}
				}
				if discovery {
					_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				} else {
					err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options, func([]moduledependency.Module) error { return stage.Recheck(ctx) })
				}
				if strings.HasSuffix(mode, "identity") {
					if _, markerErr := os.Stat(marker); markerErr != nil {
						t.Fatal("real acquisition did not replace manifest identity", markerErr)
					}
					if !errors.Is(err, moduledependency.ErrConcurrentChange) && !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatal("unrecorded manifest replacement accepted", err)
					}
				} else if strings.HasPrefix(mode, "pre-") {
					if (mode == "pre-rejected" || mode == "pre-combined") && !errors.Is(err, rejected) ||
						(mode == "pre-cancelled" || mode == "pre-combined") && !errors.Is(err, context.Canceled) ||
						(mode == "pre-drift" || mode == "pre-combined") && !errors.Is(err, moduledependency.ErrConcurrentChange) ||
						strings.Contains(string(readFile(t, trace)), "mod download ") {
						t.Fatal("raw capture failure reached acquisition or lost errors", err)
					}
				} else if mode == "failed" {
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
						t.Fatal("failed acquisition lost its original error", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				wantCalls := 1
				if mode == "no-callback" {
					wantCalls = 0
				}
				if calls != wantCalls || string(readFile(t, fixture.authority)) != string(original) {
					t.Fatalf("raw capture count=%d or original checksum authority changed", calls)
				}
				wantDownloads := 1
				if strings.HasPrefix(mode, "pre-") {
					wantDownloads = 0
				} else if phase == "workspace-discovery" && !strings.HasSuffix(mode, "identity") && mode != "failed" {
					wantDownloads = 2 // Source acquisition precedes checksum normalization.
				}
				if strings.Count(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@v1.0.0\n") != wantDownloads {
					t.Fatal("exact source acquisition was omitted or repeated")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(mode, "identity") {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("manifest evidence lost after cleanup")
					}
				} else {
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal(err)
					}
					if calls != 0 {
						rejectAcquisitionManifestReplacement(t, inputs, fixture.downloaded.GoMod)
					}
				}
			})
		}
	}
}
