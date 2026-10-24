package moduledependency_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestPrepareSourcesWorkspaceSelectionBoundary(t *testing.T) {
	for _, mode := range []string{"wildcard", "exact-same", "exact-different", "member-wildcard", "member-exact-same", "member-exact-different", "work-override", "no-wildcard", "replacement-once", "versioned-main-target", "requested-main", "local", "query-spelling", "primary-authority", "split-authority", "selection-failure", "mvs-upgrade", "proof-budget"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			fixture := warmPreparation(t, true, false)
			project := filepath.Join(fixture.app, "go.mod")
			member := filepath.Join(fixture.root, "member", "go.mod")
			work := string(readFile(t, fixture.work))
			requested := []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}
			wantBeforeList := 1
			if strings.HasPrefix(mode, "member-") {
				writeFile(t, project, "module example.com/app\ngo 1.26\nreplace "+warmRequestedModule+" => "+warmSourceModule+" v1.0.0\n")
				writeFile(t, filepath.Join(fixture.app, "go.sum"), string(readFile(t, fixture.authority)))
				work = strings.ReplaceAll(work, "replace "+warmRequestedModule+" => "+warmSourceModule+" v1.0.0\n", "")
				wantBeforeList = 3 // The module phase retains before and after acquisition.
			}
			switch mode {
			case "exact-same":
				work += "replace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.0.0\n"
			case "exact-different":
				work += "replace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.1.0\n"
				wantBeforeList = 0
			case "proof-budget":
				for index := range 256 {
					work += fmt.Sprintf("replace %s v1.1.%d => %s v1.0.0\n", warmRequestedModule, index, warmSourceModule)
				}
				wantBeforeList = 0
			case "member-exact-same", "member-exact-different":
				version := "v1.0.0"
				if mode == "member-exact-different" {
					version, wantBeforeList = "v1.1.0", 2
				}
				writeFile(t, member, string(readFile(t, member))+"replace "+warmRequestedModule+" v1.1.0 => "+warmSourceModule+" "+version+"\n")
			case "work-override":
				writeFile(t, member, string(readFile(t, member))+"replace "+warmRequestedModule+" v1.1.0 => example.com/unused v1.0.0\n")
			case "no-wildcard":
				work = strings.ReplaceAll(work, "replace "+warmRequestedModule+" =>", "replace "+warmRequestedModule+" v1.0.0 =>")
				wantBeforeList = 0
			case "replacement-once":
				work += "replace " + warmSourceModule + " => example.com/unused v1.0.0\n"
			case "versioned-main-target":
				writeModule(t, filepath.Dir(member), warmSourceModule)
			case "requested-main":
				writeModule(t, filepath.Dir(member), warmRequestedModule)
				work = strings.ReplaceAll(work, "replace "+warmRequestedModule+" => "+warmSourceModule+" v1.0.0\n", "")
				wantBeforeList = 0
			case "local":
				work = strings.ReplaceAll(work, warmSourceModule+" v1.0.0", "./local")
				wantBeforeList = 0
			case "query-spelling":
				writeFile(t, member, string(readFile(t, member))+"require example.com/unused v1\n")
				wantBeforeList = 0
			case "primary-authority":
				writeFile(t, fixture.work+".sum", string(readFile(t, fixture.authority)))
				writeFile(t, fixture.authority, strings.NewReplacer(fixture.downloaded.Sum, "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=", fixture.downloaded.GoModSum, "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=").Replace(string(readFile(t, fixture.authority))))
			case "split-authority":
				writeFile(t, fixture.work+".sum", warmSourceModule+" v1.0.0 "+fixture.downloaded.Sum+"\n")
				writeFile(t, fixture.authority, warmSourceModule+" v1.0.0/go.mod "+fixture.downloaded.GoModSum+"\n")
			case "mvs-upgrade":
				const other = "example.com/other"
				writeModuleProxy(t, fixture.root, warmSourceModule, "v1.1.0")
				writeModuleProxy(t, fixture.root, other, "v1.0.0", "module "+other+"\ngo 1.16\nrequire "+warmRequestedModule+" v1.1.0\n")
				writeModule(t, filepath.Join(fixture.root, "local-other"), other)
				writeFile(t, project, string(readFile(t, project))+"replace "+other+" => ../local-other\n")
				work += "replace " + other + " => " + other + " v1.0.0\nreplace " + warmRequestedModule + " v1.1.0 => " + warmSourceModule + " v1.1.0\n"
				requested = append(requested, module.Version{Path: other, Version: "v1.0.0"})
				wantBeforeList = 0
			}
			writeFile(t, fixture.work, work)
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: fixture.app, Environment: fixture.environment})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			command, environment, trace := tracePreparation(t, fixture.root, stage.Environment())
			if mode == "selection-failure" {
				environment = append(environment, "PLYSTRA_MODULE_LIST_SUFFIX={")
			}
			beforeList, calls := 0, 0
			err = moduledependency.PrepareSources(t.Context(), locate(t, stage.Root()), requested, moduledependency.Options{Environment: environment, GoCommand: command}, func(sources []moduledependency.Module) error {
				for _, source := range sources {
					if source.Path() != warmRequestedModule {
						continue
					}
					calls++
					if !strings.Contains(string(readFile(t, trace)), "list -m ") {
						beforeList++
						if source.SelectedVersion() != "v1.0.0" || source.SourceDirectory() != fixture.downloaded.Dir || source.Checksum() != fixture.downloaded.Sum || source.GoModChecksum() != fixture.downloaded.GoModSum || len(source.ProjectGoMod()) != 0 {
							t.Fatal("early source capture lost exact request provenance or original authority")
						}
						replacement, ok := source.Replacement()
						if !ok || replacement.Path() != warmSourceModule || replacement.Version() != "v1.0.0" {
							t.Fatal("early source capture reapplied or omitted its replacement")
						}
					}
				}
				return retainWarmBundle(t, stage, t.Context(), sources)
			})
			if mode == "selection-failure" || mode == "mvs-upgrade" {
				if !errors.Is(err, moduledependency.ErrInvalidOutput) {
					t.Fatalf("selection did not reject invalid output or an MVS upgrade: %v", err)
				}
				if strings.Contains(string(readFile(t, trace)), "mod download -json "+warmSourceModule+"@") {
					t.Fatal("source acquisition followed failed selection")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if beforeList != wantBeforeList || (mode == "local" || mode == "requested-main") && calls != 0 {
				t.Fatalf("source selection boundary: early=%d, want=%d, all=%d", beforeList, wantBeforeList, calls)
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
			recheckErr := inputs.Recheck(t.Context())
			if mode == "local" || mode == "requested-main" || mode == "mvs-upgrade" {
				if recheckErr != nil {
					t.Fatalf("unconsumed source entered the staged read set: %v", recheckErr)
				}
			} else if !errors.Is(recheckErr, projectstage.ErrConcurrentChange) {
				t.Fatalf("completed source evidence was lost after cleanup: %v", recheckErr)
			}
		})
	}
}

func TestPrepareSourcesWorkspaceSelectionJoinsRetentionFailures(t *testing.T) {
	fixture := warmPreparation(t, true, false)
	command, environment, trace := tracePreparation(t, fixture.root, fixture.environment)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rejected := errors.New("workspace source rejected")
	err := moduledependency.PrepareSources(ctx, locate(t, fixture.app), []module.Version{{Path: warmRequestedModule, Version: "v1.0.0"}}, moduledependency.Options{Environment: environment, GoCommand: command}, func(sources []moduledependency.Module) error {
		if len(sources) == 0 {
			return nil
		}
		writeFile(t, fixture.authority, string(readFile(t, fixture.authority))+"\n")
		cancel()
		return rejected
	})
	if !errors.Is(err, rejected) || !errors.Is(err, context.Canceled) || !errors.Is(err, moduledependency.ErrConcurrentChange) {
		t.Fatalf("workspace retention lost rejection, cancellation, or original metadata drift: %v", err)
	}
	if strings.Contains(string(readFile(t, trace)), "list -m ") {
		t.Fatal("workspace selection followed a failed source callback")
	}
}
