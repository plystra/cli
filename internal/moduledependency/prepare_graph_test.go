package moduledependency_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesWorkspaceSelectedGraph(t *testing.T) {
	for _, mode := range []string{"complete", "legacy", "without-go", "authored-roots", "unused-replacement", "missing-transitive-manifest", "missing-transitive-authority", "missing-source-authority", "offline", "selection-failure", "reject", "cancel", "rejected-cancelled-drift", "self-replacement", "versioned-main-target"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			selectedPath := "example.com/Selected/Source"
			if mode == "self-replacement" {
				selectedPath = warmRequestedModule
			}
			goLine := "go 1.26\n"
			if mode == "legacy" || strings.HasPrefix(mode, "missing-transitive-") {
				goLine = "go 1.16\n"
			} else if mode == "without-go" {
				goLine = ""
			}
			writeModuleProxy(t, fixture.root, selectedPath, "v1.0.0", "module "+selectedPath+"\n"+goLine+"require "+warmSourceModule+" v1.0.0\n")
			seed := t.TempDir()
			writeModule(t, seed, "example.com/seed")
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: withoutEnvironmentKey(fixture.environment, "GOWORK")}, "mod", "download", "-json", selectedPath+"@v1.0.0")
			downloaded := fixture.downloaded
			if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Dir == fixture.downloaded.Dir {
				t.Fatalf("seed selected source: %v", err)
			}
			work := strings.ReplaceAll(string(readFile(t, fixture.work)), "replace "+warmRequestedModule+" => "+warmSourceModule+" v1.0.0", "replace "+warmRequestedModule+" v1.0.0 => "+selectedPath+" v1.0.0")
			writeFile(t, fixture.work, work)
			sums := string(readFile(t, fixture.authority)) + selectedPath + " v1.0.0 " + downloaded.Sum + "\n" + selectedPath + " v1.0.0/go.mod " + downloaded.GoModSum + "\n"
			wantEarly := 1
			switch mode {
			case "authored-roots":
				for _, path := range []string{filepath.Join(fixture.app, "go.mod"), filepath.Join(fixture.root, "member", "go.mod")} {
					source := strings.Replace(string(readFile(t, path)), "go 1.26", "go 1.16", 1)
					writeFile(t, path, source+"toolchain go1.99.0\nrequire example.com/unrelated v1.9.0\nexclude "+warmRequestedModule+" v1.0.0\n")
				}
			case "unused-replacement":
				writeFile(t, fixture.work, work+"replace "+warmRequestedModule+" v1.1.0 => example.com/unused v1.0.0\n")
			case "versioned-main-target":
				writeModule(t, filepath.Join(fixture.root, "member"), selectedPath)
			case "missing-transitive-manifest":
				if err := os.Remove(fixture.downloaded.GoMod); err != nil {
					t.Fatal(err)
				}
				wantEarly = 0
			case "missing-transitive-authority":
				sums = strings.ReplaceAll(sums, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n", "")
				wantEarly = 0
			case "missing-source-authority":
				sums = strings.ReplaceAll(sums, selectedPath+" v1.0.0 "+downloaded.Sum+"\n", "")
				wantEarly = 0
			}
			writeFile(t, fixture.authority, sums)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			if mode == "selection-failure" {
				environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("selected source rejected")
			early, all := 0, 0
			options := moduledependency.Options{Environment: environment, GoCommand: command, Offline: mode == "offline"}
			options.RetainRootManifests = func(ctx context.Context, manifests []moduledependency.RootManifest) error {
				var metadata []projectstage.MetadataInput
				for _, manifest := range manifests {
					if goLine == "go 1.26\n" && manifest.Path == fixture.downloaded.GoMod {
						t.Fatal("unqueried pruned manifest entered the read set")
					}
					metadata = append(metadata, projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum})
				}
				return stage.RetainMetadata(ctx, metadata)
			}
			options.RetainModuleInformation = func(ctx context.Context, paths []string) error {
				var metadata []projectstage.MetadataInput
				for _, path := range paths {
					if strings.Contains(filepath.ToSlash(path), "/!warm/!source/") {
						t.Fatal("unqueried transitive module information entered the read set")
					}
					metadata = append(metadata, projectstage.MetadataInput{Path: path})
				}
				return stage.RetainMetadata(ctx, metadata)
			}
			err = moduledependency.PrepareSources(ctx, locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, options, func(sources []moduledependency.Module) error {
				for _, source := range sources {
					if source.Path() != warmRequestedModule || source.SelectedVersion() != "v1.0.0" || source.SourceDirectory() != downloaded.Dir {
						t.Fatal("source capture lost requested identity or captured an unqueried tree")
					}
					all++
					if !strings.Contains(string(readFile(t, trace)), "list -m ") {
						early++
						if len(source.ProjectGoMod()) != 0 || source.Checksum() != downloaded.Sum || source.GoModChecksum() != downloaded.GoModSum {
							t.Fatal("early source capture parsed declarations or changed checksum authority")
						}
						replacement, ok := source.Replacement()
						if !ok || replacement.Path() != selectedPath || replacement.Version() != "v1.0.0" {
							t.Fatal("early source capture lost explicit replacement provenance")
						}
					}
				}
				if err := retainWarmBundle(t, stage, ctx, sources); err != nil {
					return err
				}
				if len(sources) != 0 {
					switch mode {
					case "reject":
						return rejected
					case "cancel":
						cancel()
					case "rejected-cancelled-drift":
						writeFile(t, filepath.Join(stage.Root(), "go.sum"), "\n")
						cancel()
						return rejected
					}
				}
				return nil
			})
			switch mode {
			case "selection-failure":
				if !errors.Is(err, moduledependency.ErrInvalidOutput) {
					t.Fatalf("invalid selection was accepted: %v", err)
				}
			case "reject", "cancel", "rejected-cancelled-drift":
				if mode != "cancel" && !errors.Is(err, rejected) || mode != "reject" && !errors.Is(err, context.Canceled) || mode == "rejected-cancelled-drift" && !errors.Is(err, moduledependency.ErrConcurrentChange) {
					t.Fatalf("source callback lost its failure evidence: %v", err)
				}
				if strings.Contains(string(readFile(t, trace)), "list -m ") {
					t.Fatal("selection followed failed source retention")
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if early != wantEarly || all == 0 {
				t.Fatalf("selected source boundary: early=%d, want=%d, all=%d", early, wantEarly, all)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml")
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatalf("unqueried transitive source entered the read set: %v", err)
			}
			if err := os.Chmod(filepath.Join(downloaded.Dir, "smtp", "plugin.yaml"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("selected source evidence was lost after cleanup: %v", err)
			}
		})
	}
}

func TestPrepareSourcesWorkspaceSelectedGraphUpgrade(t *testing.T) {
	for _, mode := range []string{"fixed-root", "local-root", "requested-local", "unknown-frontier"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const other = "example.com/other"
			fixture := warmPreparation(t, true, false)
			writeModuleProxy(t, fixture.root, warmSourceModule, "v1.1.0")
			otherManifest := "module " + other + "\ngo 1.16\nrequire " + warmRequestedModule + " v1.1.0\n"
			writeModuleProxy(t, fixture.root, other, "v1.0.0", otherManifest)
			seed := t.TempDir()
			writeModule(t, seed, "example.com/seed")
			sums := string(readFile(t, fixture.authority))
			upgraded := fixture.downloaded
			for _, version := range []module.Version{{Path: warmSourceModule, Version: "v1.1.0"}, {Path: other, Version: "v1.0.0"}} {
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: seed, Environment: withoutEnvironmentKey(fixture.environment, "GOWORK")}, "mod", "download", "-json", version.String())
				downloaded := fixture.downloaded
				if err != nil || json.Unmarshal(output, &downloaded) != nil {
					t.Fatalf("seed upgraded graph: %v", err)
				}
				sums += version.Path + " " + version.Version + " " + downloaded.Sum + "\n"
				if version.Path != other || mode != "unknown-frontier" {
					sums += version.Path + " " + version.Version + "/go.mod " + downloaded.GoModSum + "\n"
				}
				if version.Path == warmSourceModule {
					upgraded = downloaded
				}
			}
			writeFile(t, fixture.authority, sums)
			local := filepath.Join(fixture.root, "local-other")
			writeFile(t, filepath.Join(local, "go.mod"), otherManifest)
			project := filepath.Join(fixture.app, "go.mod")
			writeFile(t, project, string(readFile(t, project))+"replace "+other+" => ../local-other\n")
			work := strings.ReplaceAll(string(readFile(t, fixture.work)), "replace "+warmRequestedModule+" =>", "replace "+warmRequestedModule+" v1.0.0 =>")
			if mode == "requested-local" {
				work = strings.Replace(work, warmSourceModule+" v1.0.0", "./local", 1)
			}
			work += "replace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.1.0\n"
			if mode == "local-root" {
				work += "replace " + other + " => ./local-other\n"
			} else {
				work += "replace " + other + " => " + other + " v1.0.0\n"
			}
			writeFile(t, fixture.work, work)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			selected := 0
			err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}, {Path: other, Version: "v1.0.0"}}, moduledependency.Options{Environment: environment, GoCommand: command}, func(sources []moduledependency.Module) error {
				for _, source := range sources {
					if source.Path() != warmRequestedModule {
						continue
					}
					selected++
					if strings.Contains(string(readFile(t, trace)), "list -m ") || source.SelectedVersion() != "v1.1.0" || source.SourceDirectory() != upgraded.Dir {
						t.Fatal("graph retained the requested version instead of the actual MVS selection")
					}
				}
				return retainWarmBundle(t, stage, t.Context(), sources)
			})
			want := 1
			if mode == "unknown-frontier" {
				want = 0
			}
			if !errors.Is(err, moduledependency.ErrInvalidOutput) || selected != want {
				t.Fatalf("MVS upgrade: selected captures=%d, want=%d, error=%v", selected, want, err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(fixture.downloaded.Dir, "smtp", "plugin.yaml"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatalf("unselected requested source entered the read set: %v", err)
			}
			if err := os.Chmod(filepath.Join(upgraded.Dir, "smtp", "plugin.yaml"), 0o600); err != nil {
				t.Fatal(err)
			}
			recheckErr := inputs.Recheck(t.Context())
			if want == 0 && recheckErr != nil || want != 0 && !errors.Is(recheckErr, projectstage.ErrConcurrentChange) {
				t.Fatalf("inferred selected evidence changed after cleanup: %v", recheckErr)
			}
		})
	}
}
