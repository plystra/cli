package moduledependency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
)

func TestDiscoverRetainsAcquiredSourcesBeforeValidation(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		modes := []string{"success", "shared-source", "replacement-chain", "mutated-callback-group", "legacy", "missing-archive", "manifest-only", "offline", "rejected", "cancelled", "changed-sums", "changed-module", "rejected-and-changed-sums", "invalid-manifest", "checksum-failure", "later-failure-source", "later-failure-manifest", "later-failure-checksum", "trailing-json", "duplicate", "omitted", "unexpected", "archive-mismatch", "manifest-mismatch", "download-error", "directory-removed", "information-only", "information-only-drift", "information-only-removed", "information-only-parent-identity"}
		if workspace {
			modes = append(modes, "primary-archive-authority", "primary-manifest-authority", "member-archive-conflict", "member-manifest-conflict")
		}
		for _, mode := range modes {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				const later = "example.com/zz-later"
				fixture := warmPreparation(t, workspace, mode == "legacy")
				writeModuleProxy(t, fixture.root, later, "v1.0.0")
				seed := t.TempDir()
				writeModule(t, seed, "example.com/seed")
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: append(withoutEnvironmentKey(fixture.environment, "GOWORK"), "GOWORK=off")}, "mod", "download", "-json", later+"@v1.0.0")
				var second struct{ Dir, GoMod, Sum, GoModSum string }
				if err != nil || json.Unmarshal(output, &second) != nil || second.Dir == "" || second.Sum == "" || second.GoModSum == "" {
					t.Fatalf("seed later source: %v", err)
				}
				project := filepath.Join(fixture.app, "go.mod")
				manifest := string(readFile(t, project)) + "require " + warmRequestedModule + " v1.0.0\nrequire " + later + " v1.0.0\n"
				if mode == "shared-source" {
					manifest += "require example.com/shared v1.0.0\nreplace example.com/shared => " + warmSourceModule + " v1.0.0\n"
				}
				if mode == "replacement-chain" {
					manifest += "replace " + warmSourceModule + " => example.com/not-selected v1.0.0\n"
				}
				writeFile(t, project, manifest)
				sums := string(readFile(t, fixture.authority)) + later + " v1.0.0 " + second.Sum + "\n" + later + " v1.0.0/go.mod " + second.GoModSum + "\n"
				if mode == "manifest-only" {
					sums = strings.ReplaceAll(sums, warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n", "")
				}
				if strings.HasPrefix(mode, "primary-") {
					writeFile(t, fixture.work+".sum", sums)
					checksum := fixture.downloaded.Sum
					if mode == "primary-manifest-authority" {
						checksum = fixture.downloaded.GoModSum
					}
					sums = strings.ReplaceAll(sums, checksum, "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
				} else if strings.HasPrefix(mode, "member-") {
					version := "v1.0.0"
					if mode == "member-manifest-conflict" {
						version += "/go.mod"
					}
					writeFile(t, filepath.Join(fixture.app, "go.sum"), warmSourceModule+" "+version+" h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
				}
				writeFile(t, fixture.authority, sums)
				for _, directory := range []string{fixture.downloaded.Dir, second.Dir} {
					relative, err := filepath.Rel(fixture.cache, directory)
					if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
						t.Fatal("source removal escaped isolated cache")
					}
					if err := os.RemoveAll(directory); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "missing-archive" {
					if err := os.Remove(strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".zip"); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(mode, "later-failure-") {
					for _, path := range []string{strings.TrimSuffix(second.GoMod, ".mod") + ".zip", filepath.Join(fixture.proxy, filepath.FromSlash(later), "@v", "v1.0.0.zip")} {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				marker := filepath.Join(fixture.root, "information-replaced")
				informationDrift := strings.HasPrefix(mode, "information-only-")
				if informationDrift {
					environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")+".info", "PLYSTRA_MODULE_METADATA_PHASE=download", "PLYSTRA_MODULE_METADATA_MARKER="+marker)
					if mode == "information-only-removed" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=delete")
					} else if mode == "information-only-parent-identity" {
						environment = append(environment, "PLYSTRA_MODULE_METADATA_MODE=parent-identity")
					}
				}
				malformed := mode == "trailing-json" || mode == "duplicate" || mode == "omitted" || mode == "unexpected"
				if malformed {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT="+mode)
				}
				mismatch := mode == "archive-mismatch" || mode == "manifest-mismatch"
				if mismatch || mode == "download-error" || mode == "directory-removed" {
					environment = append(environment, "PLYSTRA_MODULE_DOWNLOAD_OUTPUT="+mode)
					if mismatch {
						checksum := fixture.downloaded.Sum
						if mode == "manifest-mismatch" {
							checksum = fixture.downloaded.GoModSum
						}
						environment = append(environment, "PLYSTRA_MODULE_LIST_CHECKSUM="+checksum)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("acquired source rejected")
				calls := 0
				information := map[string]bool{}
				options := moduledependency.Options{
					Environment: environment, GoCommand: command, RequireSourceChecksums: true, Offline: mode == "offline",
					RetainModuleInformation: func(paths []string) error {
						var files []projectstage.MetadataInput
						for _, path := range paths {
							if information[path] {
								t.Fatal("discovery recaptured prior information evidence")
							}
							information[path] = true
							files = append(files, projectstage.MetadataInput{Path: path})
						}
						err := stage.RetainMetadata(ctx, files)
						if mode == "mutated-callback-group" && len(paths) != 0 {
							paths[0] = ""
						}
						return err
					},
					RetainSelectedSources: func(sources []moduledependency.Module) error {
						calls++
						commands := string(readFile(t, trace))
						if !strings.Contains(commands, "mod download -json "+warmSourceModule+"@v1.0.0") || calls == 1 && strings.Contains(commands, "mod download -json "+later+"@v1.0.0") {
							t.Fatal("acquired source was retained after a later acquisition")
						}
						want := 1
						if mode == "shared-source" && calls == 1 {
							want = 2
						}
						if len(sources) != want {
							t.Fatalf("logical source group: %d, want %d", len(sources), want)
						}
						for _, source := range sources {
							if len(source.ProjectGoMod()) != 0 || source.Project() || source.Workspace() {
								t.Fatal("retention consumed extracted declarations")
							}
							if calls == 1 {
								replacement, ok := source.Replacement()
								if !ok || replacement.Path() != warmSourceModule || replacement.Version() != "v1.0.0" || source.SourceDirectory() != fixture.downloaded.Dir || source.Root() != canonicalPath(t, fixture.downloaded.Dir) || source.GoModFile() != fixture.downloaded.GoMod || source.Checksum() != fixture.downloaded.Sum || source.GoModChecksum() != fixture.downloaded.GoModSum {
									t.Fatal("acquisition lost source identity, checksum, or replacement provenance")
								}
							}
						}
						if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
							return err
						}
						if calls != 1 {
							return nil
						}
						switch mode {
						case "mutated-callback-group":
							sources[0] = moduledependency.Module{}
						case "rejected":
							return rejected
						case "cancelled":
							cancel()
						case "changed-sums", "rejected-and-changed-sums", "changed-module":
							path := filepath.Join(stage.Root(), "go.sum")
							if mode == "changed-module" {
								path = filepath.Join(stage.Root(), "go.mod")
							}
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
				}
				if strings.HasPrefix(mode, "information-only") {
					options.RetainSelectedSources = nil
				}
				index, err := moduledependency.Discover(ctx, locate(t, stage.Root()), options)
				commands := string(readFile(t, trace))
				if informationDrift {
					if !errors.Is(err, moduledependency.ErrConcurrentChange) || calls != 0 || strings.Contains(commands, later+"@v1.0.0") {
						t.Fatalf("information-only drift reached later acquisition: calls=%d, error=%v", calls, err)
					}
					if _, err := os.Stat(marker); err != nil {
						t.Fatal("information-only race was not exercised", err)
					}
					inputs := stage.Inputs()
					if err := stage.Close(); err != nil {
						t.Fatal(err)
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("information-only drift evidence was lost: %v", err)
					}
					return
				}
				if malformed || mode == "offline" || strings.HasPrefix(mode, "member-") || mismatch || mode == "download-error" || mode == "directory-removed" {
					if err == nil || calls != 0 || strings.Contains(commands, later+"@v1.0.0") || mode == "offline" && strings.Contains(commands, "mod download ") {
						t.Fatalf("invalid or unavailable acquisition reached retention or next source: calls=%d, error=%v, commands=%s", calls, err, commands)
					}
					if mismatch && (!errors.Is(err, moduledependency.ErrSourceIntegrity) || !errors.Is(err, moduledependency.ErrConcurrentChange)) {
						t.Fatalf("earlier checksum mismatch lost its classification: %v", err)
					}
					if mode == "directory-removed" && (!errors.Is(err, moduledependency.ErrInvalidOutput) || !errors.Is(err, moduledependency.ErrModuleUnavailable)) {
						t.Fatalf("unavailable downloaded directory lost its classification: %v", err)
					}
					return
				}
				stopped := mode == "rejected" || mode == "cancelled" || mode == "changed-sums" || mode == "changed-module" || mode == "rejected-and-changed-sums" || mode == "invalid-manifest"
				if stopped && (calls != 1 || strings.Contains(commands, later+"@v1.0.0")) {
					t.Fatalf("failed callback or validation reached next source: calls=%d, error=%v, commands=%s", calls, err, commands)
				}
				switch {
				case mode == "rejected" || mode == "rejected-and-changed-sums":
					if !errors.Is(err, rejected) || mode == "rejected-and-changed-sums" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("callback failure was lost: %v", err)
					}
				case mode == "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("callback cancellation was lost: %v", err)
					}
				case mode == "changed-sums" || mode == "changed-module":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("changed original metadata was accepted: %v", err)
					}
				case mode == "invalid-manifest":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("invalid extracted manifest was accepted: %v", err)
					}
				case strings.HasPrefix(mode, "later-failure-"):
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != 1 || !strings.Contains(commands, later+"@v1.0.0") {
						t.Fatalf("later acquisition failure was lost: calls=%d, error=%v", calls, err)
					}
				case mode == "checksum-failure":
					if err == nil {
						t.Fatal("changed retained checksum was accepted")
					}
				default:
					wantCalls := 2
					if mode == "information-only" {
						wantCalls = 0
					}
					if err != nil || calls != wantCalls {
						t.Fatalf("acquisition failed: calls=%d, error=%v", calls, err)
					}
					selected, ok := index.ByPath(warmRequestedModule)
					if !ok || selected.Root() != canonicalPath(t, fixture.downloaded.Dir) || selected.Checksum() != fixture.downloaded.Sum || selected.GoModChecksum() != fixture.downloaded.GoModSum {
						t.Fatal("validated source lost verified acquisition identity")
					}
				}
				if !bytes.Equal([]byte(manifest), readFile(t, project)) || !bytes.Equal([]byte(sums), readFile(t, fixture.authority)) {
					t.Fatal("discovery changed authored metadata")
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if mode == "invalid-manifest" || mode == "checksum-failure" {
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("later failure discarded original source evidence: %v", err)
					}
					return
				}
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
				if mode == "legacy" {
					if _, err := os.Stat(filepath.Join(fixture.downloaded.Dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("acquisition synthesized an extracted legacy manifest")
					}
				}
				path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
				if mode == "later-failure-manifest" {
					path = fixture.downloaded.GoMod
				} else if mode == "information-only" {
					path = strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".info"
				} else if mode == "later-failure-checksum" {
					path = strings.TrimSuffix(fixture.downloaded.GoMod, ".mod") + ".ziphash"
				}
				data := readFile(t, path)
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(info, info) {
					t.Fatal("missing source identity", err)
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
					t.Fatal("fixture did not replace source identity")
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("acquired input identity drift survived cleanup: %v", err)
				}
			})
		}
	}
}
