package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
)

func TestDiscoverDrainsMaterializedModuleInformationBeforeListing(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "identity", "parent-identity", "offline", "invalid-output", "command-failure", "rejected", "cancelled", "metadata-drift", "prior-drift", "mutated-callback", "no-callback"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				app := filepath.Join(root, "app")
				writeModule(t, app, "example.com/app")
				writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
				var proxy string
				arguments := []string{"mod", "download", "-json"}
				manifest := "module example.com/app\ngo 1.26\n"
				for _, name := range []string{"a", "b", "c"} {
					path := "example.com/information/" + name
					proxy = writeModuleProxy(t, root, path, "v1.0.0")
					arguments = append(arguments, path+"@v1.0.0")
					manifest += "require " + path + " v1.0.0\n"
				}
				environment := isolatedGoEnvironment(t, proxy)
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: environment}, arguments...)
				if err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(strings.NewReader(string(output)))
				var paths []string
				var sums strings.Builder
				for {
					var downloaded struct{ Path, Version, GoMod, GoModSum, Sum string }
					if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
						break
					} else if err != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
						t.Fatal("invalid fixture checksums", err)
					}
					paths = append(paths, strings.TrimSuffix(downloaded.GoMod, ".mod")+".info")
					sums.WriteString(downloaded.Path + " " + downloaded.Version + " " + downloaded.Sum + "\n")
					sums.WriteString(downloaded.Path + " " + downloaded.Version + "/go.mod " + downloaded.GoModSum + "\n")
				}
				if len(paths) != 3 {
					t.Fatal("incomplete fixture")
				}
				writeFile(t, filepath.Join(app, "go.mod"), manifest)
				writeFile(t, filepath.Join(app, "go.sum"), sums.String())
				if workspace {
					work := filepath.Join(root, "go.work")
					writeFile(t, work, "go 1.26\nuse ./app\n")
					environment = append(environment, "GOWORK="+work)
				}
				contents := make([][]byte, len(paths))
				for index, path := range paths {
					contents[index] = readFile(t, path)
					if index != 0 {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, root, stage.Environment())
				marker := filepath.Join(root, "information-replaced")
				if mode == "identity" || mode == "parent-identity" {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+paths[1], "PLYSTRA_MODULE_METADATA_MARKER="+marker)
					if mode == "parent-identity" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=parent-identity")
					}
				} else if mode == "invalid-output" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				} else if mode == "command-failure" {
					environment = append(environment, "PLYSTRA_MODULE_LIST_FAIL=1")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("materialized information rejected")
				var retained []string
				options := moduledependency.Options{GoCommand: command, Environment: environment, Offline: mode == "offline",
					RetainModuleInformation: func(captureCtx context.Context, pending []string) error {
						if strings.Contains(string(readFile(t, trace)), "list ") || len(pending) != 1 || len(retained) >= len(paths) || pending[0] != paths[len(retained)] {
							t.Fatal("materialized information first captured after Go consumption", pending, retained)
						}
						retained = append(retained, pending[0])
						if err := stage.RetainMetadata(captureCtx, []projectstage.MetadataInput{{Path: pending[0]}}); err != nil {
							return err
						}
						if len(retained) == 2 {
							switch mode {
							case "rejected":
								return rejected
							case "cancelled":
								cancel()
							case "metadata-drift":
								writeFile(t, filepath.Join(stage.Root(), "go.sum"), sums.String()+"\n")
								return rejected
							case "prior-drift":
								writeFile(t, paths[0], string(contents[0])+"\n")
							}
						}
						if len(retained) < len(paths) {
							writeFile(t, paths[len(retained)], string(contents[len(retained)]))
						}
						if mode == "mutated-callback" {
							pending[0] = "unrelated"
						}
						return nil
					},
				}
				if mode == "no-callback" {
					options.RetainModuleInformation = nil
				}
				_, err = moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				commands := string(readFile(t, trace))
				concurrent := errors.Is(err, moduledependency.ErrConcurrentChange) || errors.Is(err, projectstage.ErrConcurrentChange)
				switch mode {
				case "identity", "parent-identity":
					if _, markerErr := os.Stat(marker); markerErr != nil {
						t.Fatal("real Go listing did not replace information identity", markerErr)
					}
					if !concurrent {
						t.Fatal("Go consumed information before its original identity was captured", err)
					}
				case "invalid-output":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatal("lost listing decode failure", err)
					}
				case "command-failure":
					if !errors.Is(err, gocommand.ErrRun) {
						t.Fatal("lost listing command failure", err)
					}
				case "rejected", "cancelled", "metadata-drift", "prior-drift":
					if (mode == "rejected" || mode == "metadata-drift") && !errors.Is(err, rejected) || mode == "cancelled" && !errors.Is(err, context.Canceled) || (mode == "metadata-drift" || mode == "prior-drift") && !concurrent || strings.Contains(commands, "list ") {
						t.Fatal("materialized information failure reached Go or lost errors", err)
					}
				default:
					if err != nil {
						t.Fatal(err)
					}
				}
				want := paths
				if mode == "no-callback" {
					want = nil
				} else if mode == "rejected" || mode == "cancelled" || mode == "metadata-drift" || mode == "prior-drift" {
					want = paths[:2]
				}
				failed := mode != "unchanged" && mode != "offline" && mode != "mutated-callback" && mode != "no-callback"
				if !slices.Equal(retained, want) || (!workspace || failed) && strings.Contains(commands, "mod download ") || string(readFile(t, filepath.Join(app, "go.mod"))) != manifest || string(readFile(t, filepath.Join(app, "go.sum"))) != sums.String() {
					t.Fatalf("wrong captured information or modified Project: got=%v want=%v err=%v", retained, want, err)
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "identity" || mode == "parent-identity" || mode == "prior-drift" {
					if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
						t.Fatal("cleanup discarded original information evidence")
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(filepath.Dir(paths[0]), "unrelated.info"), "unrelated metadata")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal("unrelated sibling invalidated capture", err)
				}
				if len(retained) != 0 {
					rejectAcquisitionManifestReplacement(t, inputs, retained[len(retained)-1])
				}
			})
		}
	}
}
