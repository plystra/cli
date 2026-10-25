package moduledependency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesRetainsCompletedAcquisitions(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, changed := range []string{"manifest", "checksum", "source"} {
			t.Run(phase+"/"+changed, func(t *testing.T) {
				t.Parallel()
				const first, missing = "example.com/first", "example.com/missing"
				root := t.TempDir()
				effectiveFirst, effectiveMissing := first, missing
				if phase == "workspace" {
					effectiveFirst, effectiveMissing = "example.com/workfirst", "example.com/workmissing"
				}
				proxy := writeModuleProxy(t, root, effectiveFirst, "v1.0.0")
				writeModuleProxy(t, root, effectiveMissing, "v1.0.0")
				// Workspace selection can read the manifest; only acquisition fails.
				if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(effectiveMissing), "@v", "v1.0.0.zip")); err != nil {
					t.Fatal(err)
				}
				app := filepath.Join(root, "app")
				writeModule(t, app, "example.com/app")
				writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
				env := isolatedGoEnvironment(t, proxy)
				originals := []string{filepath.Join(app, "go.mod"), filepath.Join(app, "go.sum")}
				if phase == "workspace" {
					goMod := string(readFile(t, originals[0]))
					for _, name := range []string{"first", "missing"} {
						writeModule(t, filepath.Join(root, "local-"+name), "example.com/"+name)
						goMod += "replace example.com/" + name + " => ../local-" + name + "\n"
					}
					writeFile(t, originals[0], goMod)
					work := filepath.Join(root, "selected.work")
					writeFile(t, work, "go 1.26\nuse ./app\nreplace "+first+" => "+effectiveFirst+" v1.0.0\nreplace "+missing+" => "+effectiveMissing+" v1.0.0\n")
					env = append(withoutEnvironmentKey(env, "GOWORK"), "GOWORK="+work)
					originals = append(originals, work, work+".sum")
				}
				before := map[string][]byte{}
				for _, path := range originals {
					before[path], _ = os.ReadFile(path)
				}
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
				var retained []moduledependency.Module
				err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), []module.Version{
					{Path: first, Version: "v1.0.0"}, {Path: missing, Version: "v1.0.0"},
				}, moduledependency.Options{Environment: env, GoCommand: command}, func(_ context.Context, sources []moduledependency.Module) error {
					if len(sources) == 0 {
						return stage.Recheck(t.Context())
					}
					if len(sources) != 1 || sources[0].Path() != first {
						t.Fatalf("unexpected retained sources: %v", sources)
					}
					if commands := string(readFile(t, trace)); strings.Contains(commands, "mod download -json "+effectiveMissing+"@") {
						t.Fatalf("later acquisition started before source retention: %s", commands)
					}
					retained = append(retained, sources...)
					dependency := sources[0]
					manifest := dependency.GoModFile()
					if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{
						{Path: manifest, GoModChecksum: dependency.GoModChecksum()},
						{Path: strings.TrimSuffix(manifest, ".mod") + ".ziphash", Expected: []byte(dependency.Checksum())},
					}); err != nil {
						return err
					}
					return stage.RetainSources(t.Context(), []projectstage.Source{{Directory: dependency.SourceDirectory(), Module: module.Version{Path: effectiveFirst, Version: "v1.0.0"}, Checksum: dependency.Checksum()}})
				})
				if !errors.Is(err, moduledependency.ErrModuleUnavailable) || len(retained) != 1 {
					t.Fatalf("completed acquisition or later failure lost: %v, %v", retained, err)
				}
				if commands := string(readFile(t, trace)); !strings.Contains(commands, "mod download -json "+effectiveMissing+"@v1.0.0\n") || strings.Contains(commands, "get ") {
					t.Fatalf("unexpected preparation commands: %s", commands)
				}
				for _, path := range originals {
					after, readErr := os.ReadFile(path)
					if !bytes.Equal(before[path], after) || before[path] == nil && !errors.Is(readErr, os.ErrNotExist) {
						t.Fatalf("preparation changed original metadata: %s", path)
					}
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				dependency := retained[0]
				writeFile(t, filepath.Join(filepath.Dir(dependency.GoModFile()), "v9.0.0.mod"), "unrelated cache metadata\n")
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Fatalf("unchanged inputs rejected after failed preparation and cleanup: %v", err)
				}
				path := dependency.GoModFile()
				switch changed {
				case "checksum":
					path = strings.TrimSuffix(path, ".mod") + ".ziphash"
				case "source":
					path = filepath.Join(dependency.SourceDirectory(), "smtp", "plugin.yaml")
				}
				data := readFile(t, path)
				info, err := os.Stat(path)
				if err != nil || !os.SameFile(info, info) {
					t.Fatal("cannot retain original file identity")
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
					t.Fatal("identity fixture did not replace the original file")
				}
				if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("same-byte %s identity replacement survived cleanup: %v", changed, err)
				}
			})
		}
	}
}

func tracePreparation(t *testing.T, root string, environment []string) (string, []string, string) {
	t.Helper()
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(root, "commands")
	writeFile(t, trace, "")
	environment = append(environment, "PLYSTRA_MODULE_DEPENDENCY_HELPER=root-manifests", "PLYSTRA_MODULE_REAL_GO="+realGo, "PLYSTRA_MODULE_COMMAND_TRACE="+trace)
	return command, environment, trace
}

func TestPrepareSourcesRetainsAcquiredSourcesBeforeValidation(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"success", "invalid-manifest", "rejected", "cancelled", "cancelled-and-metadata", "changed-metadata", "rejected-and-metadata", "mutated-callback", "legacy", "unrecorded", "offline"} {
			phase := "module"
			if workspace {
				phase = "workspace"
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				const later = "example.com/zz-later"
				fixture := warmPreparation(t, workspace, mode == "legacy")
				writeModuleProxy(t, fixture.root, later, "v1.0.0")
				project := filepath.Join(fixture.app, "go.mod")
				if workspace {
					writeModule(t, filepath.Join(fixture.root, "later"), later)
					writeFile(t, project, string(readFile(t, project))+"replace "+later+" => ../later\n")
					writeFile(t, fixture.work, string(readFile(t, fixture.work))+"replace "+later+" => "+later+" v1.0.0\n")
				}
				if mode == "unrecorded" {
					if err := os.Remove(fixture.authority); err != nil {
						t.Fatal(err)
					}
				}
				relative, err := filepath.Rel(fixture.cache, fixture.downloaded.Dir)
				if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
					t.Fatal("source removal escaped isolated cache")
				}
				if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
					t.Fatal(err)
				}
				originals := map[string][]byte{project: readFile(t, project)}
				originals[fixture.authority], _ = os.ReadFile(fixture.authority)
				if workspace {
					originals[fixture.work] = readFile(t, fixture.work)
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("acquired preparation rejected")
				calls, parsedAtRetention := 0, false
				mutated := mode == "invalid-manifest" || mode == "rejected" || mode == "cancelled" || strings.Contains(mode, "metadata")
				requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}, {Path: later, Version: "v1.0.0"}}
				if mode == "offline" {
					requested = requested[:1]
				}
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), requested, moduledependency.Options{Environment: environment, GoCommand: command, Offline: mode == "offline"}, func(_ context.Context, sources []moduledependency.Module) error {
					if len(sources) == 0 {
						return nil
					}
					calls++
					if len(sources) != 1 {
						t.Fatalf("unexpected acquired source group: %v", sources)
					}
					parsedAtRetention = parsedAtRetention || len(sources[0].ProjectGoMod()) != 0
					if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
						return err
					}
					if calls != 1 {
						return nil
					}
					if strings.Contains(string(readFile(t, trace)), "mod download -json "+later+"@") {
						t.Fatal("later acquisition preceded source retention")
					}
					if source := sources[0]; source.Path() != warmRequestedModule || source.SourceDirectory() != fixture.downloaded.Dir || source.GoModFile() != fixture.downloaded.GoMod || source.Checksum() != fixture.downloaded.Sum || source.GoModChecksum() != fixture.downloaded.GoModSum {
						t.Fatal("acquired preparation lost source identity or checksum authority")
					}
					if mutated {
						manifest := filepath.Join(fixture.downloaded.Dir, "go.mod")
						if err := os.Chmod(manifest, 0o644); err != nil {
							t.Fatal(err)
						}
						writeFile(t, manifest, "module (\n")
					}
					if strings.Contains(mode, "metadata") {
						path := filepath.Join(stage.Root(), "go.mod")
						writeFile(t, path, string(readFile(t, path))+"// concurrent preparation edit\n")
					}
					switch mode {
					case "rejected", "rejected-and-metadata":
						return rejected
					case "cancelled", "cancelled-and-metadata":
						cancel()
					case "mutated-callback":
						sources[0] = moduledependency.Module{}
					}
					return nil
				})
				if parsedAtRetention {
					t.Error("retention consumed an extracted manifest before preserving source evidence")
				}
				wantCalls := len(requested)
				if mutated {
					wantCalls = 1
					if calls != wantCalls || strings.Contains(string(readFile(t, trace)), "mod download -json "+later+"@") {
						t.Fatalf("validation or callback failure permitted later acquisition: calls=%d, error=%v", calls, err)
					}
				}
				switch mode {
				case "invalid-manifest":
					if !errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("changed extracted manifest was already validated: %v", err)
					}
				case "rejected", "rejected-and-metadata":
					if !errors.Is(err, rejected) || errors.Is(err, moduledependency.ErrInvalidOutput) || mode == "rejected-and-metadata" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("retention rejection lost or followed by validation: %v", err)
					}
				case "cancelled", "cancelled-and-metadata":
					if !errors.Is(err, context.Canceled) || errors.Is(err, moduledependency.ErrInvalidOutput) || mode == "cancelled-and-metadata" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("retention cancellation lost or followed by validation: %v", err)
					}
				case "changed-metadata":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) || errors.Is(err, moduledependency.ErrInvalidOutput) {
						t.Fatalf("original metadata drift lost or followed by validation: %v", err)
					}
				default:
					if err != nil || calls != wantCalls {
						t.Fatalf("cold preparation rejected: calls=%d, error=%v", calls, err)
					}
				}
				for path, before := range originals {
					after, readErr := os.ReadFile(path)
					if !bytes.Equal(before, after) || before == nil && !errors.Is(readErr, os.ErrNotExist) {
						t.Fatal("preparation changed authored metadata")
					}
				}
				inputs := stage.Inputs()
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
				if err := inputs.Recheck(t.Context()); mutated != errors.Is(err, projectstage.ErrConcurrentChange) || !mutated && err != nil {
					t.Fatalf("retained evidence did not survive cleanup: %v", err)
				}
			})
		}
	}
}

func TestPrepareSourcesStopsAfterEmptyRetention(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, requested := range []string{"empty", "local"} {
			for _, mode := range []string{"cancelled", "cancelled-and-metadata", "rejected-cancelled-and-metadata"} {
				t.Run(phase+"/"+requested+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					app := filepath.Join(root, "app")
					writeModule(t, app, "example.com/app")
					writeFile(t, filepath.Join(app, "plystra.yaml"), "{}\n")
					project := filepath.Join(app, "go.mod")
					var versions []module.Version
					if requested == "local" {
						writeModule(t, filepath.Join(root, "local"), "example.com/local")
						writeFile(t, project, string(readFile(t, project))+"replace example.com/local => ../local\n")
						versions = []module.Version{{Path: "example.com/local", Version: "v1.0.0"}}
					}
					environment := isolatedGoEnvironment(t, root)
					if phase == "workspace" {
						work := filepath.Join(root, "selected.work")
						writeFile(t, work, "go 1.26\nuse ./app\n")
						environment = append(withoutEnvironmentKey(environment, "GOWORK"), "GOWORK="+work)
					}
					command, environment, trace := tracePreparation(t, root, environment)
					before := string(readFile(t, project))
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					rejected := errors.New("empty retention rejected")
					calls := 0
					err := moduledependency.PrepareSources(ctx, locate(t, app), versions, moduledependency.Options{Environment: environment, GoCommand: command}, func(_ context.Context, sources []moduledependency.Module) error {
						calls++
						if len(sources) != 0 {
							t.Fatal("empty retention acquired a source")
						}
						if strings.Contains(mode, "metadata") {
							writeFile(t, project, before+"// concurrent empty preparation edit\n")
						}
						cancel()
						if strings.HasPrefix(mode, "rejected") {
							return rejected
						}
						return nil
					})
					if calls != 1 || !errors.Is(err, context.Canceled) || strings.Contains(mode, "metadata") && !errors.Is(err, moduledependency.ErrConcurrentChange) || strings.HasPrefix(mode, "rejected") && !errors.Is(err, rejected) {
						t.Fatalf("empty retention lost failures or continued: calls=%d, error=%v", calls, err)
					}
					if len(readFile(t, trace)) != 0 {
						t.Fatal("Go ran after empty retention failed")
					}
					want := before
					if strings.Contains(mode, "metadata") {
						want += "// concurrent empty preparation edit\n"
					}
					if string(readFile(t, project)) != want {
						t.Fatal("empty preparation overwrote concurrent metadata")
					}
				})
			}
		}
	}
}

func TestPrepareSourcesStopsAtAcquisitionBoundary(t *testing.T) {
	for _, mode := range []string{"callback-error", "callback-cancellation", "shared-source", "invalid-request", "checksum-conflict"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const first, second, alias = "example.com/first", "example.com/second", "example.com/alias"
			root := t.TempDir()
			proxy := writeModuleProxy(t, root, first, "v1.0.0")
			writeModuleProxy(t, root, second, "v1.0.0")
			app := filepath.Join(root, "app")
			goMod := "module example.com/app\ngo 1.26\n"
			requested := []module.Version{{Path: first, Version: "v1.0.0"}, {Path: second, Version: "v1.0.0"}}
			if mode == "shared-source" {
				goMod += "replace " + alias + " => " + first + " v1.0.0\n"
				requested = append(requested, module.Version{Path: alias, Version: "v1.0.0"})
				if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(second), "@v", "v1.0.0.zip")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "invalid-request" {
				requested[1].Version = "v1"
			}
			writeFile(t, filepath.Join(app, "go.mod"), goMod)
			var originalSums []byte
			if mode == "checksum-conflict" {
				originalSums = []byte(second + " v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
				writeFile(t, filepath.Join(app, "go.sum"), string(originalSums))
			}
			command, env, trace := tracePreparation(t, root, isolatedGoEnvironment(t, proxy))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("retention rejected")
			var retained []moduledependency.Module
			calls := 0
			err := moduledependency.PrepareSources(ctx, locate(t, app), requested, moduledependency.Options{Environment: env, GoCommand: command}, func(_ context.Context, sources []moduledependency.Module) error {
				calls++
				want := 1
				if mode == "shared-source" {
					want = 2
				}
				if len(sources) != want || sources[0].Path() != first || want == 2 && sources[1].Path() != alias {
					t.Fatalf("effective-source group lost logical identities: %v", sources)
				}
				if sources[0].Checksum() == "" || sources[0].GoModChecksum() == "" || sources[0].SourceDirectory() == "" {
					t.Fatal("source retained before verification")
				}
				if commands := string(readFile(t, trace)); strings.Contains(commands, second+"@") {
					t.Fatalf("second acquisition preceded retention: %s", commands)
				}
				retained = append(retained, sources...)
				if mode == "callback-error" {
					return rejected
				}
				if mode == "callback-cancellation" {
					cancel()
				}
				return nil
			})
			commands := string(readFile(t, trace))
			switch mode {
			case "callback-error":
				if !errors.Is(err, rejected) || errors.Is(err, moduledependency.ErrModuleUnavailable) {
					t.Fatalf("retention failure replaced: %v", err)
				}
			case "callback-cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("retention cancellation lost: %v", err)
				}
			case "invalid-request":
				if !errors.Is(err, moduledependency.ErrInvalidOutput) || calls != 0 || strings.Contains(commands, "download") {
					t.Fatalf("invalid request acquired a prefix: %v, %s", err, commands)
				}
			default:
				if !errors.Is(err, moduledependency.ErrModuleUnavailable) || !strings.Contains(commands, second+"@v1.0.0") {
					t.Fatalf("later download failure replaced: %v, %s", err, commands)
				}
			}
			if mode != "invalid-request" && (calls != 1 || strings.Count(commands, first+"@v1.0.0") != 1 || len(retained) == 0) {
				t.Fatalf("completed source downloaded or retained repeatedly: %d, %s", calls, commands)
			}
			if strings.HasPrefix(mode, "callback-") && strings.Contains(commands, second+"@") {
				t.Fatalf("download continued after callback failure: %s", commands)
			}
			afterSums, sumErr := os.ReadFile(filepath.Join(app, "go.sum"))
			if string(readFile(t, filepath.Join(app, "go.mod"))) != goMod || !bytes.Equal(originalSums, afterSums) || originalSums == nil && !errors.Is(sumErr, os.ErrNotExist) {
				t.Fatal("failed preparation changed Project metadata")
			}
		})
	}
}

func TestPrepareSourcesPreservesWorkspaceAcquisitionFailures(t *testing.T) {
	for _, mode := range []string{"callback-error", "callback-cancellation", "metadata-change"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			app, env, paths := preparationWorkspace(t)
			root := filepath.Dir(app)
			const second, replacement = "example.com/second", "example.com/secondreplacement"
			proxy := writeModuleProxy(t, root, second, "v1.0.0")
			writeModuleProxy(t, root, replacement, "v1.0.0")
			if err := os.Remove(filepath.Join(proxy, filepath.FromSlash(replacement), "@v", "v1.0.0.zip")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, paths["workspace"], string(readFile(t, paths["workspace"]))+"replace "+second+" => "+replacement+" v1.0.0\n")
			originalMember := readFile(t, paths["member"])
			command, env, trace := tracePreparation(t, root, env)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("workspace retention rejected")
			var retained []string
			err := moduledependency.PrepareSources(ctx, locate(t, app), []module.Version{
				{Path: preparedOriginal, Version: "v1.0.0"}, {Path: second, Version: "v1.0.0"},
			}, moduledependency.Options{Environment: env, GoCommand: command}, func(_ context.Context, sources []moduledependency.Module) error {
				if len(sources) != 1 {
					t.Fatalf("unexpected source group: %v", sources)
				}
				source := sources[0].Path()
				if replaced, ok := sources[0].Replacement(); ok {
					source = replaced.Path()
				}
				retained = append(retained, source)
				if source != preparedReplacement {
					return nil
				}
				if commands := string(readFile(t, trace)); strings.Contains(commands, "mod download -json "+replacement+"@") {
					t.Fatalf("later workspace acquisition preceded retention: %s", commands)
				}
				switch mode {
				case "callback-error":
					return rejected
				case "callback-cancellation":
					cancel()
				case "metadata-change":
					writeFile(t, paths["member"], string(originalMember)+"// concurrent edit\n")
				}
				return nil
			})
			if strings.Join(retained, ",") != preparedOriginal+","+second+","+preparedReplacement {
				t.Fatalf("completed phase evidence lost: %v", retained)
			}
			commands := string(readFile(t, trace))
			startedLater := strings.Contains(commands, "mod download -json "+replacement+"@")
			switch mode {
			case "callback-error":
				if !errors.Is(err, rejected) || errors.Is(err, moduledependency.ErrModuleUnavailable) || startedLater {
					t.Fatalf("workspace retention failure ignored: %v, %s", err, commands)
				}
			case "callback-cancellation":
				if !errors.Is(err, context.Canceled) || startedLater {
					t.Fatalf("workspace retention cancellation ignored: %v, %s", err, commands)
				}
			case "metadata-change":
				if !errors.Is(err, moduledependency.ErrConcurrentChange) || errors.Is(err, moduledependency.ErrModuleUnavailable) || startedLater || string(readFile(t, paths["member"])) != string(originalMember)+"// concurrent edit\n" {
					t.Fatalf("metadata drift did not stop later acquisition: %v, %s", err, commands)
				}
			}
		})
	}
}

func TestPrepareSourcesReverifiesSharedSourceUnderWorkspaceAuthority(t *testing.T) {
	app, env, paths := preparationWorkspace(t)
	writeFile(t, paths["project"], string(readFile(t, paths["project"]))+"replace "+preparedOriginal+" => "+preparedReplacement+" v1.1.0\n")
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: app, Environment: append(withoutEnvironmentKey(env, "GOWORK"), "GOWORK=off")}, "mod", "download", "-json", preparedReplacement+"@v1.1.0")
	var downloaded struct{ Sum string }
	if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" {
		t.Fatalf("prepare original checksum authority: %v", err)
	}
	writeFile(t, paths["project-sum"], preparedReplacement+" v1.1.0 "+downloaded.Sum+"\n")
	writeFile(t, paths["member-sum"], preparedReplacement+" v1.1.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
	command, env, trace := tracePreparation(t, filepath.Dir(app), env)
	calls := 0
	err = moduledependency.PrepareSources(t.Context(), locate(t, app), []module.Version{{Path: preparedOriginal, Version: "v1.0.0"}}, moduledependency.Options{Environment: env, GoCommand: command}, func(_ context.Context, sources []moduledependency.Module) error {
		calls++
		if len(sources) != 1 || sources[0].Checksum() != downloaded.Sum {
			t.Fatalf("original authority source not retained: %v", sources)
		}
		if sources[0].IncompleteSourceMetadata() != (calls == 1) {
			t.Fatal("original phase did not separate early tree and complete acquisition evidence")
		}
		return nil
	})
	commands := string(readFile(t, trace))
	if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != 2 || strings.Count(commands, "mod download -json "+preparedReplacement+"@v1.1.0") != 2 {
		t.Fatalf("workspace authority skipped after original acquisition: %v, callbacks=%d, %s", err, calls, commands)
	}
}
