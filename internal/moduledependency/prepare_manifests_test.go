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

func TestPrepareSourcesRetainsWarmRequestedManifestOnFailure(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			const effective, requested = "example.com/Source/Dependency", "example.com/requested"
			root := t.TempDir()
			manifest := "module " + effective + "\ngo 1.26\n"
			proxy := writeModuleProxy(t, root, effective, "v1.0.0", manifest)
			escaped, _ := module.EscapePath(effective)
			if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")); err != nil {
				t.Fatal(err)
			}
			app := filepath.Join(root, "app")
			writeModule(t, app, "example.com/app")
			writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
			goMod := string(readFile(t, filepath.Join(app, "go.mod")))
			goMod += "replace " + requested + " => " + effective + " v1.0.0\n"
			env := isolatedGoEnvironment(t, proxy)
			authority := filepath.Join(app, "go.sum")
			if phase == "workspace" {
				local, member := filepath.Join(root, "local"), filepath.Join(root, "member")
				writeModule(t, local, requested)
				writeModule(t, member, "example.com/member")
				goMod = string(readFile(t, filepath.Join(app, "go.mod"))) + "replace " + requested + " => ../local\n"
				work := filepath.Join(root, "selected.work")
				writeFile(t, work, "go 1.26\nuse (\n ./app\n ./member\n)\nreplace "+requested+" => "+effective+" v1.0.0\n")
				env = append(withoutEnvironmentKey(env, "GOWORK"), "GOWORK="+work)
				authority = filepath.Join(member, "go.sum")
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader(manifest)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, authority, effective+" v1.0.0/go.mod "+checksum+"\n")
			cache := environmentValue(t, env, "GOMODCACHE")
			path := filepath.Join(cache, "cache", "download", filepath.FromSlash(escaped), "@v", "v1.0.0.mod")
			writeFile(t, path, manifest)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: app, Environment: env})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stage.Close(); err != nil {
					t.Error(err)
				}
			})
			command, env, trace := tracePreparation(t, root, stage.Environment())
			calls := 0
			err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), []module.Version{{Path: requested, Version: "v1.0.0"}}, moduledependency.Options{Environment: env, GoCommand: command,
				RetainRootManifests: func(manifests []moduledependency.RootManifest) error {
					calls++
					if len(manifests) != 1 || manifests[0].Path != path || manifests[0].Checksum != checksum {
						t.Fatalf("wrong prepared manifest authority: %v", manifests)
					}
					if commands := string(readFile(t, trace)); strings.Contains(commands, "list ") || strings.Contains(commands, "download ") {
						t.Fatalf("manifest retention followed its first Go use: %s", commands)
					}
					return stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{{Path: path, GoModChecksum: checksum}})
				},
			}, func(sources []moduledependency.Module) error {
				if len(sources) != 0 {
					t.Fatal("unavailable archive accepted as a prepared source")
				}
				return nil
			})
			if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != 1 {
				t.Fatalf("failed acquisition lost its warm manifest: %v, callbacks=%d", err, calls)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || !os.SameFile(info, info) {
				t.Fatal("cannot retain original file identity")
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, path, manifest)
			if err := os.Chmod(path, info.Mode()); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			replacement, err := os.Stat(path)
			if err != nil || os.SameFile(info, replacement) {
				t.Fatal("identity fixture did not replace the original manifest")
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("failed acquisition manifest identity replacement accepted: %v", err)
			}
		})
	}
}

func TestPrepareSourcesManifestCaptureBoundaries(t *testing.T) {
	for _, mode := range []string{"warm", "callback-error", "callback-changed-sums", "callback-cancellation", "changed-sums", "changed-module", "missing-manifest", "missing-checksum", "archive-only", "invalid-request", "shared-source", "local", "exact-replacement", "reverse-exact-replacement", "unrelated-query", "offline", "toolchain"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const effective, target = "example.com/source", "example.com/target"
			root := t.TempDir()
			manifest := "module " + effective + "\ngo 1.26\n"
			proxy := writeModuleProxy(t, root, effective, "v1.0.0", manifest)
			if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(effective), "@v", "v1.0.0.zip")); err != nil {
				t.Fatal(err)
			}
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\ngo 1.26\nreplace " + target + " => " + effective + " v1.0.0\n"
			requested := []module.Version{{Path: target, Version: "v1.0.0"}}
			if strings.Contains(mode, "exact-replacement") {
				goMod = strings.ReplaceAll(goMod, "replace "+target+" =>", "replace "+target+" v1.0.0 =>")
				wildcard := "replace " + target + " => example.com/unavailable v1.0.0\n"
				if mode == "reverse-exact-replacement" {
					goMod = wildcard + goMod
				} else {
					goMod += wildcard
				}
			}
			if mode == "shared-source" {
				goMod += "replace example.com/second => " + effective + " v1.0.0\n"
				requested = append(requested, module.Version{Path: "example.com/second", Version: "v1.0.0"})
			}
			if mode == "local" {
				writeModule(t, filepath.Join(root, "local"), effective)
				goMod = "module example.com/app\ngo 1.26\nreplace " + target + " => ../local\n"
			}
			if mode == "unrelated-query" {
				goMod += "require example.com/unrelated v1\n"
			}
			if mode == "toolchain" {
				goMod += "toolchain go1.999.0\n"
			}
			if mode == "invalid-request" {
				requested[0].Version = "v1"
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(manifest)), nil })
			if err != nil {
				t.Fatal(err)
			}
			var sums []byte
			if mode != "missing-checksum" && mode != "archive-only" {
				sums = []byte(effective + " v1.0.0/go.mod " + checksum + "\n")
			}
			if mode == "archive-only" || mode == "offline" {
				sums = append(sums, []byte(effective+" v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")...)
			}
			if sums != nil {
				writeFile(t, filepath.Join(app, "go.sum"), string(sums))
			}
			env := isolatedGoEnvironment(t, proxy)
			if mode == "toolchain" {
				env = append(withoutEnvironmentKey(env, "GOTOOLCHAIN"), "GOTOOLCHAIN=path")
			}
			cache := environmentValue(t, env, "GOMODCACHE")
			path := filepath.Join(cache, "cache", "download", filepath.FromSlash(effective), "@v", "v1.0.0.mod")
			if mode != "missing-manifest" {
				writeFile(t, path, manifest)
			}
			command, env, trace := tracePreparation(t, root, env)
			if mode == "offline" {
				env = append(env, "PLYSTRA_MODULE_EXPECT_OFFLINE=1")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("manifest retention rejected")
			calls := 0
			err = moduledependency.PrepareSources(ctx, locate(t, app), requested, moduledependency.Options{Environment: env, GoCommand: command, Offline: mode == "offline",
				RetainRootManifests: func(manifests []moduledependency.RootManifest) error {
					calls++
					if len(manifests) != 1 || manifests[0].Path != path || manifests[0].Checksum != checksum || strings.Contains(string(readFile(t, trace)), "download ") {
						t.Fatalf("invalid early manifest capture: %v", manifests)
					}
					switch mode {
					case "callback-error":
						return rejected
					case "callback-changed-sums":
						writeFile(t, filepath.Join(app, "go.sum"), string(sums)+"\n")
						return rejected
					case "callback-cancellation":
						cancel()
					case "changed-sums":
						writeFile(t, filepath.Join(app, "go.sum"), string(sums)+"\n")
					case "changed-module":
						writeFile(t, filepath.Join(app, "go.mod"), goMod+"// concurrent edit\n")
					}
					return nil
				},
			}, func(sources []moduledependency.Module) error {
				if len(sources) != 0 {
					t.Fatal("failed acquisition reported completed source")
				}
				return nil
			})
			commands := string(readFile(t, trace))
			wantCapture := mode != "missing-manifest" && mode != "missing-checksum" && mode != "archive-only" && mode != "invalid-request" && mode != "local"
			if (calls == 1) != wantCapture || calls > 1 {
				t.Fatalf("manifest callbacks=%d, want capture=%v", calls, wantCapture)
			}
			blocked := strings.HasPrefix(mode, "callback-") || strings.HasPrefix(mode, "changed-") || mode == "invalid-request"
			if blocked && strings.Contains(commands, "download ") {
				t.Fatalf("download followed failed capture: %s", commands)
			}
			switch mode {
			case "callback-error", "callback-changed-sums":
				if !errors.Is(err, rejected) || mode == "callback-changed-sums" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("callback failure replaced: %v", err)
				}
			case "callback-cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("callback cancellation lost: %v", err)
				}
			case "changed-sums", "changed-module":
				if !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("concurrent edit accepted: %v", err)
				}
			case "invalid-request":
				if !errors.Is(err, moduledependency.ErrInvalidOutput) {
					t.Fatalf("invalid request accepted: %v", err)
				}
			case "local":
				if err != nil || strings.Contains(commands, "download ") {
					t.Fatalf("local replacement acquired a cache source: %v, %s", err, commands)
				}
			default:
				if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
					t.Fatalf("archive failure was lost: %v", err)
				}
			}
			if mode != "changed-module" && string(readFile(t, filepath.Join(app, "go.mod"))) != goMod {
				t.Fatal("preparation changed original manifest")
			}
			after, sumErr := os.ReadFile(filepath.Join(app, "go.sum"))
			if mode != "changed-sums" && mode != "callback-changed-sums" && (!bytes.Equal(sums, after) || sums == nil && !errors.Is(sumErr, os.ErrNotExist)) {
				t.Fatal("preparation changed original checksum authority")
			}
		})
	}
}

func TestPrepareSourcesWorkspaceManifestAuthority(t *testing.T) {
	for _, mode := range []string{"member", "primary-match", "primary-conflict", "member-conflict", "primary-match-first", "primary-conflict-first", "missing-authority", "malformed-member", "workspace-main", "replacement-target-main", "query-skip", "toolchain"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			app, env, paths := preparationWorkspace(t)
			root := filepath.Dir(app)
			local := filepath.Join(root, "local")
			writeModule(t, local, preparedOriginal)
			writeFile(t, paths["project"], string(readFile(t, paths["project"]))+"replace "+preparedOriginal+" => ../local\n")
			manifest := readFile(t, filepath.Join(root, "proxy", filepath.FromSlash(preparedReplacement), "@v", "v1.1.0.mod"))
			if err := os.Remove(filepath.Join(root, "proxy", filepath.FromSlash(preparedReplacement), "@v", "v1.1.0.zip")); err != nil {
				t.Fatal(err)
			}
			checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(manifest)), nil })
			if err != nil {
				t.Fatal(err)
			}
			good := preparedReplacement + " v1.1.0/go.mod " + checksum + "\n"
			bad := preparedReplacement + " v1.1.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n"
			writeFile(t, paths["member-sum"], good)
			switch mode {
			case "primary-match":
				writeFile(t, paths["member-sum"], bad)
				writeFile(t, paths["workspace-sum"], good)
			case "primary-conflict":
				writeFile(t, paths["workspace-sum"], bad)
			case "member-conflict":
				writeFile(t, paths["project-sum"], bad)
			case "primary-match-first":
				writeFile(t, paths["workspace-sum"], good+bad)
			case "primary-conflict-first":
				writeFile(t, paths["workspace-sum"], bad+good)
			case "missing-authority":
				if err := os.Remove(paths["member-sum"]); err != nil {
					t.Fatal(err)
				}
			case "malformed-member":
				writeFile(t, paths["member-sum"], "invalid checksum row with extra fields\n")
			case "workspace-main":
				writeModule(t, filepath.Dir(paths["member"]), preparedOriginal)
				writeFile(t, paths["workspace"], "go 1.26\nuse (\n ./app\n ./sibling\n)\n")
			case "replacement-target-main":
				writeModule(t, filepath.Dir(paths["member"]), preparedReplacement)
			case "query-skip":
				writeFile(t, paths["member"], string(readFile(t, paths["member"]))+"require example.com/unrelated v1\n")
			case "toolchain":
				writeFile(t, paths["project"], string(readFile(t, paths["project"]))+"toolchain go1.999.0\n")
				env = append(withoutEnvironmentKey(env, "GOTOOLCHAIN"), "GOTOOLCHAIN=path")
			}
			cache := environmentValue(t, env, "GOMODCACHE")
			path := filepath.Join(cache, "cache", "download", filepath.FromSlash(preparedReplacement), "@v", "v1.1.0.mod")
			writeFile(t, path, string(manifest))
			before := map[string][]byte{}
			for _, path := range paths {
				before[path], _ = os.ReadFile(path)
			}
			command, env, trace := tracePreparation(t, root, env)
			calls := 0
			rejected := errors.New("manifest checksum rejected")
			err = moduledependency.PrepareSources(t.Context(), locate(t, app), []module.Version{{Path: preparedOriginal, Version: "v1.0.0"}}, moduledependency.Options{Environment: env, GoCommand: command,
				RetainRootManifests: func(manifests []moduledependency.RootManifest) error {
					calls++
					if len(manifests) != 1 || manifests[0].Path != path || strings.Contains(string(readFile(t, trace)), "list ") || strings.Contains(string(readFile(t, trace)), "download ") {
						t.Fatalf("workspace manifest captured after selection or under wrong identity: %v", manifests)
					}
					if manifests[0].Checksum != checksum {
						return rejected
					}
					return nil
				},
			}, func(sources []moduledependency.Module) error {
				if len(sources) != 0 {
					t.Fatal("unavailable versioned source reported as complete")
				}
				return nil
			})
			wantCapture := mode != "member-conflict" && mode != "missing-authority" && mode != "malformed-member" && mode != "workspace-main" && mode != "query-skip"
			if (calls == 1) != wantCapture || calls > 1 {
				t.Fatalf("workspace capture count=%d, want capture=%v", calls, wantCapture)
			}
			switch mode {
			case "primary-conflict", "primary-conflict-first":
				if !errors.Is(err, rejected) || strings.Contains(string(readFile(t, trace)), "list ") {
					t.Fatalf("primary authority ignored before selection: %v", err)
				}
			case "malformed-member":
				if !errors.Is(err, moduledependency.ErrInvalidOutput) || strings.Contains(string(readFile(t, trace)), "list ") {
					t.Fatalf("malformed original checksum input accepted: %v", err)
				}
			case "workspace-main":
				if err != nil || strings.Contains(string(readFile(t, trace)), "download ") {
					t.Fatalf("workspace main acquired as versioned input: %v", err)
				}
			default:
				if !errors.Is(err, moduledependency.ErrModuleUnavailable) {
					t.Fatalf("workspace acquisition failure lost: %v", err)
				}
			}
			for _, path := range paths {
				after, readErr := os.ReadFile(path)
				if !bytes.Equal(before[path], after) || before[path] == nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("early retention changed original input: %s", path)
				}
			}
		})
	}
}
