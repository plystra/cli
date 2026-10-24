package moduledependency_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
)

func TestDiscoverRetainsListedSourcesBeforeValidation(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		modes := []string{"success", "legacy", "shared-source", "local-source", "mutated-callback-group", "reported-source-path", "rejected", "cancelled", "changed-sums", "rejected-and-changed-sums", "unparsed-manifest", "invalid-manifest", "checksum-failure", "archive-only", "manifest-only", "trailing-json", "duplicate-main", "listed-error"}
		if workspace {
			modes = append(modes, "primary-authority", "split-authority")
		} else {
			modes = append(modes, "listed-sum-identity")
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, mode == "legacy")
				project := filepath.Join(fixture.app, "go.mod")
				source := string(readFile(t, project)) + "require " + warmRequestedModule + " v1.0.0\n"
				if mode == "shared-source" {
					source += "require example.com/shared v1.0.0\nreplace example.com/shared => " + warmSourceModule + " v1.0.0\n"
				}
				if mode == "local-source" {
					local := filepath.Join(fixture.root, "ordinary")
					writeModule(t, local, "example.com/ordinary")
					source += "require example.com/ordinary v1.0.0\nreplace example.com/ordinary => ../ordinary\n"
				}
				writeFile(t, project, source)
				if mode == "unparsed-manifest" {
					path := filepath.Join(fixture.downloaded.Dir, "go.mod")
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "invalid extracted manifest\n")
				}
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				sums := string(readFile(t, fixture.authority))
				// Go's first matching row verifies the manifest, while conflicting
				// recorded rows omit GoModSum from list output and require download.
				if !workspace {
					sums += warmSourceModule + " v1.0.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n"
				}
				if mode == "archive-only" {
					sums = warmSourceModule + " v1.0.0 " + fixture.downloaded.Sum + "\n"
				} else if mode == "manifest-only" {
					sums = warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
				}
				if mode == "primary-authority" {
					writeFile(t, fixture.work+".sum", sums)
					sums = strings.ReplaceAll(sums, fixture.downloaded.GoModSum, "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
				} else if mode == "split-authority" {
					writeFile(t, fixture.work+".sum", warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n")
					sums = warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
				}
				writeFile(t, fixture.authority, sums)
				if mode == "reported-source-path" {
					alias := filepath.Join(fixture.root, "cache-alias")
					if err := os.Symlink(fixture.cache, alias); err != nil {
						t.Fatal(err)
					}
					fixture.environment = append(withoutEnvironmentKey(fixture.environment, "GOMODCACHE"), "GOMODCACHE="+alias)
					fixture.downloaded.Dir = strings.Replace(fixture.downloaded.Dir, fixture.cache, alias, 1)
					fixture.downloaded.GoMod = strings.Replace(fixture.downloaded.GoMod, fixture.cache, alias, 1)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				switch mode {
				case "trailing-json":
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
				case "duplicate-main":
					environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX="+fmt.Sprintf("{\"Path\":\"example.com/app\",\"Main\":true,\"Dir\":%q,\"GoMod\":%q}\n", stage.Root(), filepath.Join(stage.Root(), "go.mod")))
				case "listed-error":
					environment = append(environment, `PLYSTRA_MODULE_LIST_SUFFIX={"Path":"example.com/failure","Version":"v1.0.0","Error":{"Err":"failed"}}`)
				case "listed-sum-identity":
					path := filepath.Join(stage.Root(), "go.sum")
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+filepath.Join(fixture.root, "replaced"))
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("listed source rejected")
				calls := 0
				index, err := moduledependency.Discover(ctx, locate(t, stage.Root()), moduledependency.Options{
					Environment: environment, GoCommand: command, RequireSourceChecksums: true,
					RetainSelectedSources: func(sources []moduledependency.Module) error {
						calls++
						commands := string(readFile(t, trace))
						if !strings.Contains(commands, "list -m ") || strings.Contains(commands, "mod download ") {
							t.Fatal("source retention did not precede post-list acquisition")
						}
						want := 1
						if mode == "shared-source" {
							want = 2
						}
						if len(sources) != want {
							t.Fatalf("logical source group: %d, want %d", len(sources), want)
						}
						for _, source := range sources {
							replacement, ok := source.Replacement()
							if !ok || replacement.Path() != warmSourceModule || replacement.Version() != "v1.0.0" || source.SourceDirectory() != fixture.downloaded.Dir || source.Root() != canonicalPath(t, fixture.downloaded.Dir) || source.GoModFile() != fixture.downloaded.GoMod || source.Checksum() != fixture.downloaded.Sum || source.GoModChecksum() != fixture.downloaded.GoModSum || source.Workspace() || len(source.ProjectGoMod()) != 0 {
								t.Fatal("listed source lost original authority or unparsed selection provenance")
							}
						}
						if mode == "unparsed-manifest" {
							return rejected
						}
						if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
							return err
						}
						switch mode {
						case "mutated-callback-group":
							sources[0] = moduledependency.Module{}
						case "rejected":
							return rejected
						case "cancelled":
							cancel()
						case "changed-sums", "rejected-and-changed-sums":
							path := filepath.Join(stage.Root(), "go.sum")
							data, err := os.ReadFile(path)
							if err != nil && !errors.Is(err, os.ErrNotExist) {
								t.Fatal(err)
							}
							writeFile(t, path, string(data)+"\n")
							if mode == "rejected-and-changed-sums" {
								return rejected
							}
						case "invalid-manifest":
							path := filepath.Join(fixture.downloaded.Dir, "go.mod")
							if err := os.Chmod(path, 0o600); err != nil {
								t.Fatal(err)
							}
							writeFile(t, path, "invalid extracted manifest\n")
						case "checksum-failure":
							writeFile(t, strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".ziphash", "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
						}
						return nil
					},
				})
				wantCalls := 1
				if mode == "archive-only" || mode == "manifest-only" {
					wantCalls = 0
				}
				if mode == "trailing-json" || mode == "duplicate-main" || mode == "listed-error" {
					if !errors.Is(err, moduledependency.ErrInvalidOutput) || calls != 0 {
						t.Fatalf("incomplete decoded selection reached source capture: calls=%d, error=%v", calls, err)
					}
					return
				}
				if calls != wantCalls {
					t.Fatalf("listed source calls=%d, want=%d; error=%v", calls, wantCalls, err)
				}
				switch mode {
				case "rejected", "rejected-and-changed-sums", "unparsed-manifest":
					if !errors.Is(err, rejected) || mode == "rejected-and-changed-sums" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("retention rejection lost: %v", err)
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("retention cancellation lost: %v", err)
					}
				case "changed-sums", "listed-sum-identity":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("changed original sums accepted: %v", err)
					}
				case "invalid-manifest":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("extracted-manifest validation failure lost: %v", err)
					}
				case "checksum-failure":
					if !errors.Is(err, moduledependency.ErrSourceIntegrity) {
						t.Fatalf("post-list checksum failure lost: %v", err)
					}
				case "archive-only", "manifest-only":
					// Ordinary discovery may still obtain missing authority later;
					// it cannot retroactively authorize this early capture.
				default:
					if err != nil {
						t.Fatal(err)
					}
					selected, ok := index.ByPath(warmRequestedModule)
					if !ok || selected.SourceDirectory() != fixture.downloaded.Dir || selected.Checksum() != fixture.downloaded.Sum || selected.GoModChecksum() != fixture.downloaded.GoModSum {
						t.Fatal("discovery result lost Go-selected source provenance")
					}
				}
				if strings.Contains(mode, "rejected") || mode == "cancelled" || mode == "changed-sums" || mode == "invalid-manifest" || mode == "unparsed-manifest" {
					if strings.Contains(string(readFile(t, trace)), "mod download ") {
						t.Fatal("failed retention or manifest validation continued to checksum acquisition")
					}
				}
				if !bytes.Equal([]byte(sums), readFile(t, fixture.authority)) {
					t.Fatal("discovery modified authored checksum authority")
				}
				if wantCalls == 0 || mode == "unparsed-manifest" {
					return
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "invalid-manifest" || mode == "checksum-failure" {
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("failure discarded retained inputs after cleanup: %v", err)
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
				data := readFile(t, path)
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(info, info) {
					t.Fatal("missing file identity", err)
				}
				if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(data))
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				replacement, err := os.Stat(path)
				if err != nil || os.SameFile(info, replacement) {
					t.Fatal("fixture did not replace the source identity")
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("listed source identity drift survived cleanup: %v", err)
				}
			})
		}
	}
}
