package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestDiscoverRetainsMaterializedGraphManifestsAfterListing(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "command-failure", "failed-frontier", "interrupted", "rejected-interrupted", "rejected", "cancelled", "metadata-drift", "prior-drift", "mutated-callback", "unrecorded", "no-callback"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				app := filepath.Join(root, "app")
				writeModule(t, app, "example.com/app")
				writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
				a1 := module.Version{Path: "example.com/listed/a", Version: "v1.0.0"}
				a2 := module.Version{Path: a1.Path, Version: "v1.1.0"}
				a3 := module.Version{Path: a1.Path, Version: "v1.2.0"}
				b := module.Version{Path: "example.com/listed/b", Version: "v1.0.0"}
				c := module.Version{Path: "example.com/listed/c", Version: "v1.0.0"}
				d := module.Version{Path: "example.com/listed/d", Version: "v1.0.0"}
				unused := module.Version{Path: "example.com/listed/unused", Version: "v1.0.0"}
				versions := []module.Version{a1, a2, a3, b, c, d, unused}
				requirements := map[module.Version]module.Version{b: a2, a2: c, c: a3, a3: d}
				var proxy string
				arguments := []string{"mod", "download", "-json"}
				for _, version := range versions {
					source := "module " + version.Path + "\ngo 1.16\n"
					if required, exists := requirements[version]; exists {
						source += "require " + required.Path + " " + required.Version + "\n"
					}
					proxy = writeModuleProxy(t, root, version.Path, version.Version, source)
					arguments = append(arguments, version.String())
				}
				environment := isolatedGoEnvironment(t, proxy)
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: environment}, arguments...)
				if err != nil {
					t.Fatal(err)
				}
				type download struct{ Path, Version, GoMod, GoModSum, Sum string }
				facts := map[module.Version]download{}
				decoder := json.NewDecoder(strings.NewReader(string(output)))
				var sums strings.Builder
				for {
					var downloaded download
					if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
						break
					} else if err != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
						t.Fatal("invalid fixture checksums", err)
					}
					version := module.Version{Path: downloaded.Path, Version: downloaded.Version}
					facts[version] = downloaded
					sums.WriteString(version.Path + " " + version.Version + " " + downloaded.Sum + "\n")
					if mode != "unrecorded" || version != a2 {
						sums.WriteString(version.Path + " " + version.Version + "/go.mod " + downloaded.GoModSum + "\n")
					}
				}
				manifest := "module example.com/app\ngo 1.26\nrequire (\n" + a1.Path + " " + a1.Version + "\n" + b.Path + " " + b.Version + "\n)\n"
				writeFile(t, filepath.Join(app, "go.mod"), manifest)
				writeFile(t, filepath.Join(app, "go.sum"), sums.String())
				if workspace {
					work := filepath.Join(root, "go.work")
					writeFile(t, work, "go 1.26\nuse ./app\n")
					environment = append(environment, "GOWORK="+work)
				}
				for _, version := range []module.Version{a2, a3, c, d} {
					if err := os.Remove(facts[version].GoMod); err != nil {
						t.Fatal(err)
					}
				}
				writeFile(t, facts[unused].GoMod, "invalid unrelated manifest\n")
				interrupted := strings.Contains(mode, "interrupted")
				started := make(chan struct{}, 1)
				var archives atomic.Int32
				if interrupted || mode == "failed-frontier" {
					files := http.FileServer(http.Dir(filepath.Join(root, "proxy")))
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if strings.HasSuffix(r.URL.Path, ".zip") {
							archives.Add(1)
						}
						if strings.HasPrefix(r.URL.Path, "/"+c.Path+"/") {
							if mode == "failed-frontier" {
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
					environment = append(withoutEnvironmentKey(environment, "GOPROXY"), "GOPROXY="+server.URL)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, root, stage.Environment())
				if mode == "command-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_FAIL=1")
				}
				type contextKey struct{}
				ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), contextKey{}, "graph-listing"), 15*time.Second)
				defer cancel()
				if interrupted {
					go func() {
						select {
						case <-started:
							cancel()
						case <-ctx.Done():
						}
					}()
				}
				rejected := errors.New("materialized graph manifest rejected")
				retained := map[string]bool{}
				late := 0
				options := moduledependency.Options{GoCommand: command, Environment: environment,
					RetainRootManifests: func(captureCtx context.Context, manifests []moduledependency.RootManifest) error {
						commands := string(readFile(t, trace))
						post := strings.Contains(commands, "list -m ")
						if captureCtx.Err() != nil {
							t.Fatal("post-list capture inherited cancellation")
						}
						if post {
							deadline, bounded := captureCtx.Deadline()
							if !bounded || time.Until(deadline) > 30*time.Second || captureCtx.Value(contextKey{}) != "graph-listing" || interrupted && !errors.Is(ctx.Err(), context.Canceled) {
								t.Fatal("post-list graph capture lost its independent bounded context")
							}
						}
						var metadata []projectstage.MetadataInput
						for _, input := range manifests {
							if post && !retained[input.Path] {
								if strings.Contains(commands, "mod download ") {
									t.Fatal("new graph manifest first captured after acquisition")
								}
								late++
								if input.Path == facts[a2].GoMod && input.Checksum != facts[a2].GoModSum {
									t.Fatal("post-list capture replaced original checksum authority")
								}
							}
							retained[input.Path] = true
							metadata = append(metadata, projectstage.MetadataInput{Path: input.Path, GoModChecksum: input.Checksum})
						}
						if err := stage.RetainMetadata(captureCtx, metadata); err != nil {
							return err
						}
						if post && retained[facts[a2].GoMod] {
							switch mode {
							case "rejected", "rejected-interrupted":
								return rejected
							case "cancelled":
								cancel()
							case "metadata-drift":
								writeFile(t, filepath.Join(stage.Root(), "go.sum"), sums.String()+"\n")
								return rejected
							case "prior-drift":
								writeFile(t, facts[a1].GoMod, string(readFile(t, facts[a1].GoMod))+"\n")
							}
						}
						if mode == "mutated-callback" {
							manifests[0] = moduledependency.RootManifest{}
						}
						return nil
					},
				}
				if mode == "no-callback" {
					options.RetainRootManifests = nil
				}
				index, err := moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				commands := string(readFile(t, trace))
				switch mode {
				case "rejected", "metadata-drift", "rejected-interrupted":
					if !errors.Is(err, rejected) || mode == "metadata-drift" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatal("post-list capture lost rejection or metadata drift", err)
					}
				case "cancelled", "interrupted":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("post-list capture lost cancellation", err)
					}
				case "prior-drift":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatal("post-list capture refreshed an earlier manifest", err)
					}
				case "command-failure", "failed-frontier":
					if !errors.Is(err, gocommand.ErrRun) {
						t.Fatal("post-list capture lost command failure", err)
					}
				default:
					if workspace && err != nil || !workspace && !errors.Is(err, gocommand.ErrRun) {
						t.Fatal("unexpected real Go graph result", err)
					}
				}
				if !workspace && !interrupted && !errors.Is(err, gocommand.ErrRun) {
					t.Fatal("post-list capture discarded the readonly graph failure", err)
				}
				if interrupted && !errors.Is(err, context.Canceled) || archives.Load() != 0 {
					t.Fatal("interrupted graph capture lost cancellation or acquired archives", err)
				}
				if workspace && (mode == "unchanged" || mode == "mutated-callback") {
					selected, exists := index.ByPath(a1.Path)
					if !exists || selected.SelectedVersion() != a3.Version {
						t.Fatal("fixture did not remove the intermediate version from the final graph")
					}
				}
				if mode != "no-callback" && mode != "unrecorded" && (!retained[facts[a2].GoMod] || late == 0) {
					t.Fatal("materialized intermediate graph manifest was not retained")
				}
				if mode == "unchanged" || mode == "command-failure" || mode == "cancelled" || mode == "mutated-callback" {
					for _, version := range []module.Version{a2, a3, c, d} {
						if !retained[facts[version].GoMod] {
							t.Fatal("post-list capture lost a demanded graph manifest", version)
						}
					}
				}
				if mode == "unrecorded" && retained[facts[a2].GoMod] || retained[facts[unused].GoMod] {
					t.Fatal("post-list graph capture invented authority or read an unrelated manifest")
				}
				if mode != "unchanged" && mode != "mutated-callback" && mode != "no-callback" && mode != "unrecorded" && strings.Contains(commands, "mod download ") {
					t.Fatal("failed post-list capture reached source acquisition")
				}
				if string(readFile(t, filepath.Join(app, "go.mod"))) != manifest || string(readFile(t, filepath.Join(app, "go.sum"))) != sums.String() {
					t.Fatal("post-list capture changed original Project metadata")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "prior-drift" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("cleanup discarded earlier manifest drift")
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
				if retained[facts[a2].GoMod] {
					rejectAcquisitionManifestReplacement(t, inputs, facts[a2].GoMod)
				}
			})
		}
	}
}

func TestPostListingGraphCapturePreservesPruning(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(fmt.Sprintf("workspace=%t", workspace), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			app := filepath.Join(root, "app")
			writeModule(t, app, "example.com/app")
			writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
			var proxy string
			arguments := []string{"mod", "download", "-json"}
			for index, name := range []string{"a", "b", "c"} {
				path := "example.com/pruned/" + name
				source := "module " + path + "\ngo 1.17\n"
				if index < 2 {
					source += "require example.com/pruned/" + []string{"b", "c"}[index] + " v1.0.0\n"
				}
				proxy = writeModuleProxy(t, root, path, "v1.0.0", source)
				arguments = append(arguments, path+"@v1.0.0")
			}
			environment := isolatedGoEnvironment(t, proxy)
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: environment}, arguments...)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(strings.NewReader(string(output)))
			var sums strings.Builder
			var paths []string
			for {
				var downloaded struct{ Path, Version, GoMod, GoModSum, Sum string }
				if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
					break
				} else if err != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
					t.Fatal("invalid fixture checksums", err)
				}
				paths = append(paths, downloaded.GoMod)
				sums.WriteString(downloaded.Path + " " + downloaded.Version + " " + downloaded.Sum + "\n")
				sums.WriteString(downloaded.Path + " " + downloaded.Version + "/go.mod " + downloaded.GoModSum + "\n")
			}
			if len(paths) != 3 {
				t.Fatal("incomplete pruned fixture")
			}
			writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire example.com/pruned/a v1.0.0\n")
			writeFile(t, filepath.Join(app, "go.sum"), sums.String())
			if workspace {
				work := filepath.Join(root, "go.work")
				writeFile(t, work, "go 1.26\nuse ./app\n")
				environment = append(environment, "GOWORK="+work)
			}
			for _, path := range paths[:2] {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, paths[2], "invalid pruned manifest\n")
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, root, stage.Environment())
			retained := map[string]bool{}
			index, err := moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{GoCommand: command, Environment: environment,
				RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
					if !strings.Contains(string(readFile(t, trace)), "list -m ") {
						t.Fatal("cold pruned manifest appeared before listing")
					}
					var metadata []projectstage.MetadataInput
					for _, manifest := range manifests {
						retained[manifest.Path] = true
						metadata = append(metadata, projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum})
					}
					return stage.RetainMetadata(ctx, metadata)
				},
			})
			if _, selected := index.ByPath("example.com/pruned/c"); err != nil || selected || len(retained) != 2 || !retained[paths[0]] || !retained[paths[1]] {
				t.Fatal("post-list graph capture expanded information-only requirements", retained, err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			writeFile(t, paths[2], "another unrelated pruned manifest\n")
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("pruned manifest became retained evidence", err)
			}
			rejectAcquisitionManifestReplacement(t, inputs, paths[1])
		})
	}
}
