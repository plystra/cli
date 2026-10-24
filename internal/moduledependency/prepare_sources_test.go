package moduledependency_test

import (
	"archive/zip"
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
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

const warmSourceModule = "example.com/Warm/Source"
const warmRequestedModule = "example.com/requested"

type warmPreparationFixture struct {
	app, root, authority, cache, proxy, work string
	environment                              []string
	downloaded                               struct{ Dir, GoMod, Sum, GoModSum string }
}

func warmPreparation(t *testing.T, workspace, legacy bool) warmPreparationFixture {
	t.Helper()
	fixture := warmPreparationFixture{root: t.TempDir()}
	fixture.proxy = writeModuleProxy(t, fixture.root, warmSourceModule, "v1.0.0")
	if legacy {
		escaped, _ := module.EscapePath(warmSourceModule)
		prefix := filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0")
		var content bytes.Buffer
		archive := zip.NewWriter(&content)
		file, err := archive.Create(warmSourceModule + "@v1.0.0/smtp/plugin.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("id: example.smtp\n")); err != nil {
			t.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		writeFile(t, prefix+".zip", content.String())
		writeFile(t, prefix+".mod", "module "+warmSourceModule+"\n")
	}
	fixture.app = filepath.Join(fixture.root, "app")
	writeModule(t, fixture.app, "example.com/app")
	writeFile(t, filepath.Join(fixture.app, "plystra.yaml"), "{}\n")
	fixture.environment = isolatedGoEnvironment(t, fixture.proxy)
	fixture.cache = environmentValue(t, fixture.environment, "GOMODCACHE")
	seed := t.TempDir()
	writeModule(t, seed, "example.com/seed")
	output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: fixture.environment}, "mod", "download", "-json", warmSourceModule+"@v1.0.0")
	if err != nil || json.Unmarshal(output, &fixture.downloaded) != nil || fixture.downloaded.Sum == "" || fixture.downloaded.GoModSum == "" {
		t.Fatalf("seed warm source: %v", err)
	}
	fixture.authority = filepath.Join(fixture.app, "go.sum")
	project := string(readFile(t, filepath.Join(fixture.app, "go.mod")))
	if workspace {
		writeModule(t, filepath.Join(fixture.root, "local"), warmRequestedModule)
		writeModule(t, filepath.Join(fixture.root, "member"), "example.com/member")
		project += "replace " + warmRequestedModule + " => ../local\n"
		fixture.work = filepath.Join(fixture.root, "selected.work")
		writeFile(t, fixture.work, "go 1.26\nuse (\n ./app\n ./member\n)\nreplace "+warmRequestedModule+" => "+warmSourceModule+" v1.0.0\n")
		fixture.environment = append(withoutEnvironmentKey(fixture.environment, "GOWORK"), "GOWORK="+fixture.work)
		fixture.authority = filepath.Join(fixture.root, "member", "go.sum")
	} else {
		project += "replace " + warmRequestedModule + " => " + warmSourceModule + " v1.0.0\n"
	}
	writeFile(t, filepath.Join(fixture.app, "go.mod"), project)
	writeFile(t, fixture.authority, warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n"+warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
	return fixture
}

func retainWarmBundle(t *testing.T, stage *projectstage.Stage, ctx context.Context, sources []moduledependency.Module) error {
	t.Helper()
	var metadata []projectstage.MetadataInput
	var trees []projectstage.Source
	for _, source := range sources {
		metadata = append(metadata, projectstage.MetadataInput{Path: source.GoModFile(), GoModChecksum: source.GoModChecksum()}, projectstage.MetadataInput{
			Path: strings.TrimSuffix(source.GoModFile(), ".mod") + ".ziphash", Expected: []byte(source.Checksum()),
		})
		version := module.Version{Path: source.Path(), Version: source.SelectedVersion()}
		if replacement, ok := source.Replacement(); ok {
			version = module.Version{Path: replacement.Path(), Version: replacement.Version()}
		}
		trees = append(trees, projectstage.Source{Directory: source.SourceDirectory(), Module: version, Checksum: source.Checksum()})
	}
	return stage.RetainSources(ctx, trees, metadata...)
}

func TestDiscoverRetainsEligibleWarmSelectedSources(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		for _, mode := range []string{"success", "mutated-callback-group", "offline", "legacy", "missing-archive", "partial", "missing-tree", "missing-manifest", "missing-checksum-file", "archive-only", "manifest-only", "poisoned-source", "poisoned-manifest", "poisoned-checksum", "unsafe-checksum"} {
			t.Run(fmt.Sprintf("workspace=%t/%s", workspace, mode), func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, workspace, mode == "legacy")
				project := filepath.Join(fixture.app, "go.mod")
				writeFile(t, project, string(readFile(t, project))+"require "+warmRequestedModule+" v1.0.0\n")
				prefix := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")
				switch mode {
				case "missing-archive", "missing-manifest", "missing-checksum-file":
					suffix := map[string]string{"missing-archive": ".zip", "missing-manifest": ".mod", "missing-checksum-file": ".ziphash"}[mode]
					if err := os.Remove(prefix + suffix); err != nil {
						t.Fatal(err)
					}
				case "partial":
					writeFile(t, prefix+".partial", "incomplete\n")
				case "missing-tree":
					if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
						t.Fatal(err)
					}
				case "archive-only":
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n")
				case "manifest-only":
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				case "poisoned-source", "poisoned-manifest":
					path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
					if mode == "poisoned-manifest" {
						path = filepath.Join(fixture.downloaded.Dir, "go.mod")
					}
					if err := os.Chmod(path, 0o600); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed source\n")
				case "poisoned-checksum":
					writeFile(t, prefix+".ziphash", "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
				case "unsafe-checksum":
					if err := os.Remove(prefix + ".ziphash"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(prefix+".ziphash", 0o700); err != nil {
						t.Fatal(err)
					}
				}
				original := readFile(t, fixture.authority)
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = stage.Close() })
				command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
				calls, listedCalls, acquiredCalls := 0, 0, 0
				_, err = moduledependency.Discover(t.Context(), locate(t, stage.Root()), moduledependency.Options{
					Environment: environment, GoCommand: command, Offline: mode == "offline",
					RetainSelectedSources: func(sources []moduledependency.Module) error {
						if commands := string(readFile(t, trace)); strings.Contains(commands, "download ") {
							acquiredCalls++
						} else if strings.Contains(commands, "list ") {
							listedCalls++
						} else {
							calls++
						}
						if len(sources) != 1 || len(sources[0].ProjectGoMod()) != 0 {
							t.Fatal("early source callback parsed extracted declarations")
						}
						err := retainWarmBundle(t, stage, t.Context(), sources)
						if mode == "mutated-callback-group" {
							sources[0] = moduledependency.Module{}
						}
						return err
					},
				})
				wantCalls := 1
				if mode == "partial" || mode == "missing-tree" || mode == "missing-manifest" || mode == "missing-checksum-file" || mode == "archive-only" || mode == "manifest-only" || mode == "unsafe-checksum" {
					wantCalls = 0
				}
				wantError := strings.HasPrefix(mode, "poisoned-") || mode == "unsafe-checksum" || mode == "archive-only" && !workspace
				if calls != wantCalls || (err != nil) != wantError {
					t.Fatalf("selected warm capture: calls=%d, want=%d, error=%v", calls, wantCalls, err)
				}
				wantListed := 0
				if mode == "missing-manifest" {
					wantListed = 1
				}
				if listedCalls != wantListed {
					t.Fatalf("listed source capture: calls=%d, want=%d", listedCalls, wantListed)
				}
				wantAcquired := 0
				if mode == "missing-tree" || mode == "partial" || mode == "missing-checksum-file" || mode == "manifest-only" && !workspace {
					wantAcquired = 1
				}
				if acquiredCalls != wantAcquired {
					t.Fatalf("acquired source capture: calls=%d, want=%d", acquiredCalls, wantAcquired)
				}
				if strings.HasPrefix(mode, "poisoned-") || mode == "unsafe-checksum" {
					if strings.Contains(string(readFile(t, trace)), "list ") {
						t.Fatal("unsafe warm source reached Go listing")
					}
				}
				if !bytes.Equal(original, readFile(t, fixture.authority)) {
					t.Fatal("discovery changed original checksum authority")
				}
				if mode == "legacy" {
					if _, err := os.Stat(filepath.Join(fixture.downloaded.Dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("capture synthesized a legacy source manifest")
					}
				}
			})
		}
	}
}

func TestPrepareSourcesWarmWorkspaceChecksumAuthority(t *testing.T) {
	for _, kind := range []string{"archive", "manifest"} {
		for _, mode := range []string{"primary-match", "primary-conflict", "primary-match-first", "primary-conflict-first", "member-conflict", "split", "historical-empty", "unknown-algorithm", "malformed-member"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, true, false)
				version, checksum := "v1.0.0", fixture.downloaded.Sum
				other := warmSourceModule + " v1.0.0/go.mod " + fixture.downloaded.GoModSum + "\n"
				if kind == "manifest" {
					version, checksum = version+"/go.mod", fixture.downloaded.GoModSum
					other = warmSourceModule + " v1.0.0 " + fixture.downloaded.Sum + "\n"
				}
				prefix := warmSourceModule + " " + version + " "
				good := prefix + checksum + "\n"
				bad := prefix + "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n"
				projectSum := filepath.Join(fixture.app, "go.sum")
				primary := good
				switch mode {
				case "primary-match", "primary-match-first", "historical-empty", "unknown-algorithm":
					writeFile(t, fixture.authority, other+bad)
				case "primary-conflict", "primary-conflict-first":
					primary = bad
				case "member-conflict":
					primary = ""
					writeFile(t, projectSum, bad)
				case "split":
					writeFile(t, fixture.authority, other)
				case "malformed-member":
					writeFile(t, fixture.authority, "invalid checksum row with extra fields\n")
				}
				switch mode {
				case "primary-match-first":
					primary += bad
				case "primary-conflict-first":
					primary += good
				case "historical-empty":
					primary = prefix + "h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY=\n" + primary
				case "unknown-algorithm":
					primary = prefix + "h2:unknown\n" + primary
				}
				writeFile(t, fixture.work+".sum", primary)
				before := map[string][]byte{}
				for _, path := range []string{projectSum, fixture.authority, fixture.work + ".sum"} {
					before[path], _ = os.ReadFile(path)
				}
				command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
				calls := 0
				rejected := errors.New("independent warm checksum rejected")
				err := moduledependency.PrepareSources(t.Context(), locate(t, fixture.app), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{Environment: environment, GoCommand: command}, func(sources []moduledependency.Module) error {
					if len(sources) == 0 {
						return nil
					}
					calls++
					if len(sources) != 1 || calls == 1 && len(sources[0].ProjectGoMod()) != 0 || calls == 1 && strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
						t.Fatal("workspace source was not retained before acquisition")
					}
					if sources[0].Checksum() != fixture.downloaded.Sum || sources[0].GoModChecksum() != fixture.downloaded.GoModSum {
						return rejected
					}
					return nil
				})
				switch mode {
				case "primary-conflict", "primary-conflict-first":
					if kind == "manifest" {
						if !errors.Is(err, rejected) || calls != 1 || strings.Contains(string(readFile(t, trace)), "list -m ") {
							t.Fatalf("workspace manifest authority was not retained before selection: %v, calls=%d", err, calls)
						}
					} else if !errors.Is(err, rejected) || calls != 1 {
						t.Fatalf("primary checksum authority lost: %v, calls=%d", err, calls)
					}
				case "member-conflict":
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != 0 {
						t.Fatalf("conflicting members supplied warm authority: %v, calls=%d", err, calls)
					}
				case "malformed-member":
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != 0 || strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
						t.Fatalf("primary match hid malformed member: %v, calls=%d", err, calls)
					}
				default:
					if err != nil || calls != 3 {
						t.Fatalf("warm workspace checksum selection: %v, calls=%d", err, calls)
					}
				}
				for path, original := range before {
					after, readErr := os.ReadFile(path)
					if !bytes.Equal(original, after) || original == nil && !errors.Is(readErr, os.ErrNotExist) {
						t.Fatalf("warm preparation changed original authority: %s", path)
					}
				}
			})
		}
	}
}

func TestPrepareSourcesRetainsWorkspaceSourcesBeforeSelection(t *testing.T) {
	for _, mode := range []string{"source", "extracted-manifest"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
			if mode == "extracted-manifest" {
				path = filepath.Join(fixture.downloaded.Dir, "go.mod")
			}
			if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil || !os.SameFile(before, before) {
				t.Fatal(err)
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			marker := filepath.Join(fixture.root, "replaced")
			environment = append(environment, "PLYSTRA_MODULE_METADATA_PATH="+path, "PLYSTRA_MODULE_METADATA_MARKER="+marker)
			err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{
				Environment: environment, GoCommand: command,
			}, func(sources []moduledependency.Module) error {
				return retainWarmBundle(t, stage, t.Context(), sources)
			})
			if _, markerErr := os.Stat(marker); markerErr != nil {
				t.Fatalf("selection did not replace the source identity: %v, preparation=%v", markerErr, err)
			}
			after, statErr := os.Stat(path)
			if statErr != nil || os.SameFile(before, after) || before.Mode() != after.Mode() || before.ModTime() != after.ModTime() {
				t.Fatal("fixture did not replace only the source identity")
			}
			if !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("source replacement during workspace selection was accepted: %v", err)
			}
			if strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@v1.0.0") {
				t.Fatal("download followed concurrent workspace source replacement")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("source identity evidence was lost after cleanup: %v", err)
			}
		})
	}
}

func TestPrepareSourcesRetainsWarmBundlesBeforeDownloads(t *testing.T) {
	for _, phase := range []string{"module", "workspace"} {
		for _, mode := range []string{"success", "failed-source", "failed-manifest", "failed-checksum", "callback-error", "callback-cancel", "callback-error-and-drift", "changed-sums", "poisoned-source", "poisoned-source-and-hash", "partial", "missing-tree", "missing-manifest", "missing-checksum-file", "unsafe-manifest", "unsafe-checksum-file", "archive-only", "manifest-only", "shared", "toolchain", "offline", "legacy"} {
			if phase == "workspace" && mode == "shared" {
				continue // Go workspace selection rejects two paths for one module version.
			}
			t.Run(phase+"/"+mode, func(t *testing.T) {
				t.Parallel()
				fixture := warmPreparation(t, phase == "workspace", mode == "legacy")
				prefix := strings.TrimSuffix(fixture.downloaded.GoMod, ".mod")
				requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}
				if mode == "shared" {
					project := filepath.Join(fixture.app, "go.mod")
					writeFile(t, project, string(readFile(t, project))+"replace example.com/second => "+warmSourceModule+" v1.0.0\n")
					requested = append(requested, module.Version{Path: "example.com/second", Version: "v1.0.0"})
				}
				if mode == "toolchain" {
					project := filepath.Join(fixture.app, "go.mod")
					writeFile(t, project, string(readFile(t, project))+"toolchain go1.999.0\n")
					fixture.environment = append(withoutEnvironmentKey(fixture.environment, "GOTOOLCHAIN"), "GOTOOLCHAIN=path")
				}
				if strings.HasPrefix(mode, "failed-") {
					escaped, _ := module.EscapePath(warmSourceModule)
					for _, path := range []string{prefix + ".zip", filepath.Join(fixture.proxy, filepath.FromSlash(escaped), "@v", "v1.0.0.zip")} {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
				switch mode {
				case "partial":
					writeFile(t, prefix+".partial", "incomplete\n")
					path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
					if err := os.Chmod(path, 0o644); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "incomplete extraction\n")
				case "missing-tree":
					if err := os.RemoveAll(fixture.downloaded.Dir); err != nil {
						t.Fatal(err)
					}
				case "missing-manifest":
					if err := os.Remove(fixture.downloaded.GoMod); err != nil {
						t.Fatal(err)
					}
				case "missing-checksum-file":
					if err := os.Remove(prefix + ".ziphash"); err != nil {
						t.Fatal(err)
					}
				case "unsafe-manifest", "unsafe-checksum-file":
					path := fixture.downloaded.GoMod
					if mode == "unsafe-checksum-file" {
						path = prefix + ".ziphash"
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "archive-only":
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n")
				case "manifest-only":
					writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
				case "poisoned-source", "poisoned-source-and-hash":
					path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
					if err := os.Chmod(path, 0o644); err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, "changed source\n")
					if mode == "poisoned-source-and-hash" {
						poisoned, err := dirhash.HashDir(fixture.downloaded.Dir, warmSourceModule+"@v1.0.0", dirhash.Hash1)
						if err != nil {
							t.Fatal(err)
						}
						writeFile(t, prefix+".ziphash", poisoned)
					}
				}
				originalSum := readFile(t, fixture.authority)
				stageEnvironment := fixture.environment
				if mode == "toolchain" {
					stageEnvironment = append(withoutEnvironmentKey(stageEnvironment, "GOTOOLCHAIN"), "GOTOOLCHAIN=local")
				}
				stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: stageEnvironment})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stage.Close(); err != nil {
						t.Error(err)
					}
				})
				authority := filepath.Join(stage.Root(), "go.sum")
				if phase == "workspace" {
					work := environmentValue(t, stage.Environment(), "GOWORK")
					parsed, err := modfile.ParseWork(work, readFile(t, work), nil)
					if err != nil || len(parsed.Use) != 2 {
						t.Fatalf("staged workspace members: %v", err)
					}
					member := parsed.Use[1].Path
					if !filepath.IsAbs(member) {
						member = filepath.Join(filepath.Dir(work), member)
					}
					authority = filepath.Join(member, "go.sum")
				}
				environment := stage.Environment()
				if mode == "toolchain" {
					environment = append(withoutEnvironmentKey(environment, "GOTOOLCHAIN"), "GOTOOLCHAIN=path")
				}
				command, environment, trace := tracePreparation(t, fixture.root, environment)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rejected := errors.New("warm bundle rejected")
				calls := 0
				wantEarly := mode != "partial" && !strings.HasPrefix(mode, "missing-") && mode != "archive-only" && mode != "manifest-only"
				if phase == "workspace" && mode == "missing-manifest" {
					wantEarly = true // Metadata-only selection materializes the manifest before acquisition.
				}
				wantSelection := phase == "workspace" && wantEarly && mode != "missing-manifest"
				err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), requested, moduledependency.Options{Environment: environment, GoCommand: command, Offline: mode == "offline"}, func(sources []moduledependency.Module) error {
					if len(sources) == 0 {
						return nil
					}
					calls++
					if len(sources) != len(requested) || sources[0].SourceDirectory() != fixture.downloaded.Dir || sources[0].GoModFile() != fixture.downloaded.GoMod || sources[0].Checksum() != fixture.downloaded.Sum || sources[0].GoModChecksum() != fixture.downloaded.GoModSum {
						t.Fatalf("warm source authority or identity changed: %v", sources)
					}
					if calls == 1 && wantEarly && strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@v1.0.0") {
						t.Fatal("warm source retention followed its first exact download")
					}
					if calls == 1 && wantSelection && strings.Contains(string(readFile(t, trace)), "list -m ") {
						t.Fatal("fixed workspace source retention followed selection")
					}
					if calls == 1 && wantEarly && len(sources[0].ProjectGoMod()) != 0 {
						t.Fatal("early source retention parsed the extracted manifest")
					}
					if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
						return err
					}
					if calls == 1 {
						switch mode {
						case "callback-error":
							return rejected
						case "callback-error-and-drift":
							writeFile(t, authority, string(originalSum)+"\n")
							return rejected
						case "callback-cancel":
							cancel()
						case "changed-sums":
							writeFile(t, authority, string(originalSum)+"\n")
						}
					}
					return nil
				})
				blocked := strings.HasPrefix(mode, "callback-") || mode == "changed-sums" || strings.HasPrefix(mode, "poisoned-") || strings.HasPrefix(mode, "unsafe-")
				if blocked && strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@v1.0.0") {
					t.Fatal("acquisition followed a rejected warm source")
				}
				switch {
				case strings.HasPrefix(mode, "unsafe-"):
					if err == nil || calls != 0 {
						t.Fatalf("unsafe warm metadata accepted: %v, calls=%d", err, calls)
					}
				case strings.HasPrefix(mode, "failed-"):
					wantCalls := 1
					if wantSelection {
						wantCalls++
					}
					if !errors.Is(err, moduledependency.ErrModuleUnavailable) || calls != wantCalls {
						t.Fatalf("failed acquisition lost warm source evidence: %v, calls=%d", err, calls)
					}
				case mode == "callback-error" || mode == "callback-error-and-drift":
					if !errors.Is(err, rejected) || calls != 1 || mode == "callback-error-and-drift" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
						t.Fatalf("warm callback failure lost: %v, calls=%d", err, calls)
					}
				case mode == "callback-cancel":
					if !errors.Is(err, context.Canceled) || calls != 1 {
						t.Fatalf("warm callback cancellation lost: %v", err)
					}
				case mode == "changed-sums":
					if !errors.Is(err, moduledependency.ErrConcurrentChange) || calls != 1 {
						t.Fatalf("changed original sums accepted: %v", err)
					}
				case strings.HasPrefix(mode, "poisoned-"):
					if err == nil || calls != 1 {
						t.Fatalf("source or cache checksum supplied its own authority: %v", err)
					}
				default:
					wantCalls := 1
					if wantEarly {
						wantCalls = 2
					}
					if wantSelection {
						wantCalls++
					}
					if err != nil || calls != wantCalls {
						t.Fatalf("warm acquisition: %v, calls=%d, want=%d", err, calls, wantCalls)
					}
				}
				if !bytes.Equal(originalSum, readFile(t, fixture.authority)) {
					t.Fatal("preparation changed the original checksum authority")
				}
				if (mode == "changed-sums" || mode == "callback-error-and-drift") && string(readFile(t, authority)) != string(originalSum)+"\n" {
					t.Fatal("preparation overwrote concurrent checksum changes")
				}
				if mode == "legacy" {
					if _, err := os.Stat(filepath.Join(fixture.downloaded.Dir, "go.mod")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("legacy source was modified to synthesize go.mod")
					}
				}
				if strings.HasPrefix(mode, "failed-") || mode == "success" {
					inputs := stage.Inputs()
					if err := stage.Close(); err != nil {
						t.Fatal(err)
					}
					writeFile(t, prefix+"-unrelated.mod", "unrelated cache entry\n")
					if err := inputs.Recheck(t.Context()); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
					if mode == "failed-manifest" {
						path = fixture.downloaded.GoMod
					} else if mode == "failed-checksum" {
						path = prefix + ".ziphash"
					}
					data := readFile(t, path)
					info, err := os.Stat(path)
					if err != nil || !os.SameFile(info, info) {
						t.Fatal(err)
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
						t.Fatal("fixture did not replace file identity")
					}
					if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
						t.Fatalf("warm input identity drift survived cleanup: %v", err)
					}
				}
			})
		}
	}
}
