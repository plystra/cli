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

func TestDiscoverRetainsRawRootManifestFilesBeforeListing(t *testing.T) {
	for _, mode := range []string{"identity", "parent-identity", "unchanged", "missing", "directory", "rejected", "cancelled", "metadata-drift", "combined", "manifest-drift", "selection-failure", "mutated-callback", "no-callback", "unauthorized-graph"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			project := filepath.Join(fixture.app, "go.mod")
			writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
			writeFile(t, fixture.authority, "")
			// A different replacement for another version prevents the existing
			// all-version source proof from establishing a provisional selection.
			writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
			if mode == "missing" || mode == "directory" {
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				if mode == "directory" {
					if err := os.Mkdir(fixture.downloaded.GoMod, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			} else if mode == "unauthorized-graph" {
				writeFile(t, fixture.downloaded.GoMod, "module "+warmSourceModule+"\ngo 1.26\nrequire example.com/unavailable v1.0.0\n")
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			marker := filepath.Join(fixture.root, "raw-root-manifest-replaced")
			if strings.HasSuffix(mode, "identity") {
				environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+fixture.downloaded.GoMod, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
				if mode == "parent-identity" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=parent-identity")
				}
			} else if mode == "selection-failure" {
				environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("raw root manifest rejected")
			calls, early := 0, 0
			options := moduledependency.Options{
				GoCommand: command, Environment: environment,
				RetainRootManifests: func(context.Context, []moduledependency.RootManifest) error {
					t.Fatal("raw root evidence supplied checksum authority")
					return nil
				},
				RetainManifestFiles: func(ctx context.Context, paths []string) error {
					calls++
					if !strings.Contains(string(readFile(t, trace)), "list -m ") {
						early++
					}
					if len(paths) != 1 || paths[0] != fixture.downloaded.GoMod {
						t.Fatal("raw root manifest lost its physical identity", paths)
					}
					if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: paths[0]}}); err != nil {
						return err
					}
					if mode == "metadata-drift" || mode == "combined" {
						writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
					} else if mode == "manifest-drift" {
						writeFile(t, paths[0], string(readFile(t, paths[0]))+"\n")
					}
					if mode == "cancelled" || mode == "combined" {
						cancel()
					}
					if mode == "rejected" || mode == "combined" {
						return rejected
					}
					if mode == "mutated-callback" {
						paths[0] = "unrelated"
					}
					return nil
				},
			}
			if mode == "no-callback" {
				options.RetainManifestFiles = nil
			}
			if mode == "unauthorized-graph" {
				options.RetainModuleInformation = func(context.Context, []string) error {
					t.Fatal("raw root evidence supplied parsed graph authority")
					return nil
				}
			}
			_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
			commands := string(readFile(t, trace))
			concurrent := errors.Is(err, moduledependency.ErrConcurrentChange) || errors.Is(err, projectstage.ErrConcurrentChange)
			switch mode {
			case "identity", "parent-identity":
				if _, markerErr := os.Stat(marker); markerErr != nil {
					t.Fatal("real graph listing did not replace manifest identity", markerErr)
				}
				if !concurrent || strings.Contains(commands, "mod download ") {
					t.Fatalf("raw root identity drift reached acquisition: calls=%d early=%d err=%v", calls, early, err)
				}
			case "directory":
				if err == nil || calls != 0 || strings.Contains(commands, "list -m ") {
					t.Fatal("unsafe raw root manifest reached graph listing", err)
				}
				return
			case "rejected", "cancelled", "metadata-drift", "combined", "manifest-drift":
				if (mode == "rejected" || mode == "combined") && !errors.Is(err, rejected) ||
					(mode == "cancelled" || mode == "combined") && !errors.Is(err, context.Canceled) ||
					(mode == "metadata-drift" || mode == "combined" || mode == "manifest-drift") && !concurrent ||
					strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
					t.Fatal("raw root retention failure reached Go or lost errors", err)
				}
			case "selection-failure":
				if !errors.Is(err, moduledependency.ErrInvalidOutput) {
					t.Fatal("raw retention lost graph listing failure", err)
				}
			case "unauthorized-graph":
				if err == nil || !strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
					t.Fatal("raw root graph bypassed ordinary Go selection", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			wantCalls, wantEarly := 1, 1
			if mode == "missing" {
				wantEarly = 0
			} else if mode == "no-callback" {
				wantCalls, wantEarly = 0, 0
			}
			if calls != wantCalls || early != wantEarly || string(readFile(t, fixture.authority)) != "" {
				t.Fatalf("raw root retention changed authority or its first-read boundary: calls=%d early=%d", calls, early)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(mode, "identity") || mode == "manifest-drift" {
				if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
					t.Fatal("raw root manifest evidence lost after cleanup")
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

func TestDiscoverRawRootManifestFilesRespectReadonlySums(t *testing.T) {
	for _, mode := range []string{"recorded", "missing", "unknown", "sentinel", "invalid-h1", "malformed", "no-callback", "authorized-drift"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, false, false)
			writeFile(t, filepath.Join(fixture.app, "go.mod"), "module example.com/app\ngo 1.26\nrequire "+warmSourceModule+" v1.0.0\n")
			row := warmSourceModule + " v1.0.0/go.mod "
			switch mode {
			case "missing":
				writeFile(t, fixture.authority, "")
			case "unknown":
				writeFile(t, fixture.authority, row+"h2:unknown\n")
			case "sentinel":
				writeFile(t, fixture.authority, row+"h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY=\n")
			case "invalid-h1":
				writeFile(t, fixture.authority, row+"h1:garbage\n")
			case "malformed":
				writeFile(t, fixture.authority, "unrelated malformed\n")
			}
			original := string(readFile(t, fixture.authority))
			command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
			calls := 0
			options := moduledependency.Options{GoCommand: command, Environment: environment,
				RetainManifestFiles: func(_ context.Context, paths []string) error {
					calls++
					if len(paths) != 1 || paths[0] != fixture.downloaded.GoMod || strings.Contains(string(readFile(t, trace)), "list -m ") {
						t.Fatal("readonly raw root manifest first captured after its read", paths)
					}
					return nil
				},
			}
			if mode == "no-callback" {
				options.RetainManifestFiles = nil
			} else if mode == "authorized-drift" {
				options.RetainRootManifests = func(_ context.Context, manifests []moduledependency.RootManifest) error {
					if len(manifests) != 1 || manifests[0].Path != fixture.downloaded.GoMod || manifests[0].Checksum != fixture.downloaded.GoModSum {
						t.Fatal("authorized manifest lost its original checksum")
					}
					path := manifests[0].Path
					data := readFile(t, path)
					before, err := os.Stat(path)
					if err != nil || !os.SameFile(before, before) {
						t.Fatal("cannot initialize authorized manifest identity")
					}
					if err := os.Rename(path, path+".prior"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, before.Mode()); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
					after, err := os.Stat(path)
					if err != nil || os.SameFile(before, after) {
						t.Fatal("authorized callback did not replace the manifest")
					}
					return os.Remove(path + ".prior")
				}
			}
			_, err := moduledependency.Discover(t.Context(), locate(t, fixture.app), options)
			wantCalls := 0
			if mode == "recorded" || mode == "invalid-h1" || mode == "authorized-drift" {
				wantCalls = 1
			}
			wantError := mode != "recorded" && mode != "no-callback"
			if calls != wantCalls || (err != nil) != wantError || string(readFile(t, fixture.authority)) != original {
				t.Fatalf("readonly raw capture bypassed original checksum policy: calls=%d err=%v", calls, err)
			}
			if strings.Contains(string(readFile(t, trace)), "list -m ") == (mode == "malformed" || mode == "authorized-drift") {
				t.Fatal("invalid original inputs reached graph listing")
			}
			if mode == "authorized-drift" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
				t.Fatal("raw manifest baseline was refreshed after the authorized callback", err)
			}
		})
	}
}

func TestDiscoverRetainsLateRawRootManifestFilesBeforeListing(t *testing.T) {
	for _, mode := range []string{"raw-cascade", "authorized-cascade"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			if mode == "raw-cascade" {
				writeFile(t, fixture.authority, "")
			}
			paths := []string{fixture.downloaded.GoMod}
			var contents []string
			project := filepath.Join(fixture.app, "go.mod")
			source := string(readFile(t, project)) + "require " + warmRequestedModule + " v1.0.0\n"
			for _, name := range []string{"example.com/Root/Second", "example.com/Root/Third"} {
				writeModuleProxy(t, fixture.root, name, "v1.0.0")
				escaped, err := module.EscapePath(name)
				if err != nil {
					t.Fatal(err)
				}
				paths = append(paths, filepath.Join(fixture.cache, "cache", "download", filepath.FromSlash(escaped), "@v", "v1.0.0.mod"))
				contents = append(contents, string(readFile(t, filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.mod"))))
				source += "require " + name + " v1.0.0\n"
			}
			writeFile(t, project, source)
			writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" v1.1.0\n")
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			marker := filepath.Join(fixture.root, "late-raw-root-replaced")
			environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+paths[2], "PLYSTRA_MODULE_METADATA_MARKER="+marker)
			calls, early := 0, 0
			_, err = moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{
				GoCommand: command, Environment: environment,
				RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
					if mode != "authorized-cascade" || len(manifests) != 1 || manifests[0].Path != paths[0] {
						t.Fatal("late raw root acquired checksum authority")
					}
					writeFile(t, paths[1], contents[0])
					return stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: paths[0], GoModChecksum: manifests[0].Checksum}})
				},
				RetainManifestFiles: func(ctx context.Context, captured []string) error {
					calls++
					if !strings.Contains(string(readFile(t, trace)), "list -m ") {
						early++
					}
					if len(captured) != 1 || calls > len(paths) || captured[0] != paths[calls-1] {
						t.Fatal("late raw root lost its exact identity", captured)
					}
					if err := stage.RetainMetadata(ctx, []projectstage.MetadataInput{{Path: captured[0]}}); err != nil {
						return err
					}
					if calls == 1 && mode == "raw-cascade" || calls == 2 {
						writeFile(t, paths[calls], contents[calls-1])
					}
					return nil
				},
			})
			if _, markerErr := os.Stat(marker); markerErr != nil {
				t.Fatal("late raw root was not consumed by real Go listing", markerErr)
			}
			if !errors.Is(err, moduledependency.ErrConcurrentChange) && !errors.Is(err, projectstage.ErrConcurrentChange) || calls != 3 || early != 3 || strings.Contains(string(readFile(t, trace)), "mod download ") {
				t.Fatalf("late raw root replacement accepted: calls=%d early=%d err=%v", calls, early, err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
				t.Fatal("late raw root evidence lost after cleanup")
			}
		})
	}
}

func TestDiscoverRawRootManifestFilesExcludeSyntheticModules(t *testing.T) {
	for _, name := range []string{"go", "toolchain"} {
		for _, owner := range []string{"application", "member"} {
			t.Run(name+"/"+owner, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, true, false)
				writeFile(t, fixture.authority, "")
				ownerRoot, ownerPath := fixture.app, "example.com/app"
				if owner == "member" {
					ownerRoot, ownerPath = filepath.Join(fixture.root, "member"), "example.com/member"
				}
				writeFile(t, filepath.Join(ownerRoot, "go.mod"), "module "+ownerPath+"\ngo 1.26\nrequire "+name+" v1.0.0\n")
				writeFile(t, fixture.work, "go 1.26\nuse (\n./app\n./member\n)\nreplace "+name+" => "+warmSourceModule+" v1.0.0\n")
				command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
				_, err := moduledependency.Discover(t.Context(), locate(t, fixture.app), moduledependency.Options{
					GoCommand: command, Environment: environment,
					RetainManifestFiles: func(context.Context, []string) error {
						t.Fatal("synthetic root introduced a cache manifest")
						return nil
					},
				})
				listed := strings.Contains(string(readFile(t, trace)), "list -m ")
				if owner == "application" && (!errors.Is(err, moduledependency.ErrInvalidGoMod) || listed) || owner == "member" && !listed {
					t.Fatal("synthetic requirement bypassed its owning validation", err)
				}
			})
		}
	}
}
