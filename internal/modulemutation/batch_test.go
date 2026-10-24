package modulemutation

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/gocommand"
)

func TestChangeDependenciesJointResolutionAndOrderIndependence(t *testing.T) {
	proxy := writeBatchProxy(t)
	var final moduleFiles
	for _, reverse := range []bool{false, true} {
		root := writeBatchModule(t, "package application\nimport _ \"example.com/gamma\"\n")
		environment := batchEnvironment(t, proxy)
		changes := []DependencyChange{
			{Action: "update", Module: "example.com/beta@v1.1.0"},
			{Action: "remove", Module: "example.com/gone"},
			{Action: "add", Module: "example.com/gamma@v1.0.0"},
			{Action: "update", Module: "example.com/alpha@v1.1.0"},
		}
		if reverse {
			slices.Reverse(changes)
		}
		var observations []bool
		err := ChangeDependencies(t.Context(), root, DependencyBatchOptions{
			Environment: environment, Changes: changes,
			Observe: func(started bool) { observations = append(observations, started) },
		}, func(mutate applicationgenerate.ModuleMutation) error {
			// Caller mutation must not change the already-bound final obligations.
			changes[0] = DependencyChange{Action: "remove", Module: "example.com/gamma"}
			return mutate(t.Context(), root, nil, func() error {
				return gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "build", "-mod=readonly", "./...")
			})
		})
		if err != nil || !reflect.DeepEqual(observations, []bool{true}) {
			t.Fatalf("joint change: observations=%v, error=%v", observations, err)
		}
		for _, path := range []string{"example.com/alpha", "example.com/beta", "example.com/gamma"} {
			selected, exists, err := FindRequirement(root, path)
			version := "v1.1.0"
			if path == "example.com/gamma" {
				version = "v1.0.0"
			}
			if err != nil || !exists || selected.Version() != version || path != "example.com/beta" && selected.Indirect() {
				t.Fatalf("final %s: version=%s indirect=%t exists=%t error=%v", path, selected.Version(), selected.Indirect(), exists, err)
			}
		}
		if _, exists, err := FindRequirement(root, "example.com/gone"); err != nil || exists {
			t.Fatalf("removed dependency was restored by tidy: exists=%t error=%v", exists, err)
		}
		files, err := captureModuleFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		if final == nil {
			final = files
		} else if !reflect.DeepEqual(final, files) {
			t.Fatal("request order changed selected module metadata")
		}
	}
}

func TestChangeDependenciesJointRemovalUsesOriginalSnapshot(t *testing.T) {
	proxy := writeBatchProxy(t)
	var final moduleFiles
	for _, reverse := range []bool{false, true} {
		root := writeBatchModule(t, "package application\n")
		changes := []DependencyChange{{Action: "remove", Module: "example.com/alpha"}, {Action: "remove", Module: "example.com/beta"}}
		if reverse {
			slices.Reverse(changes)
		}
		err := ChangeDependencies(t.Context(), root, DependencyBatchOptions{Changes: changes, Environment: batchEnvironment(t, proxy)}, func(mutate applicationgenerate.ModuleMutation) error {
			return mutate(t.Context(), root, nil, func() error { return nil })
		})
		if err != nil {
			t.Fatalf("joint removal rejected an originally selected target: %v", err)
		}
		for _, path := range []string{"example.com/alpha", "example.com/beta", "example.com/gone"} {
			_, exists, err := FindRequirement(root, path)
			if err != nil || exists != (path == "example.com/gone") {
				t.Fatalf("unexpected final requirement %s: exists=%t error=%v", path, exists, err)
			}
		}
		files, err := captureModuleFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		if final == nil {
			final = files
		} else if !reflect.DeepEqual(final, files) {
			t.Fatal("joint removal order changed final module metadata")
		}
	}
}

func TestChangeDependenciesRejectsGeneratedVersionConflict(t *testing.T) {
	proxy := writeBatchProxy(t)
	requirement, err := applicationgenerate.NewModuleRequirement("example.com/alpha", "v1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"add", "update"} {
		t.Run(action, func(t *testing.T) {
			root := writeBatchModule(t, "package application\n")
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			validated := false
			err = ChangeDependencies(t.Context(), root, DependencyBatchOptions{
				Changes: []DependencyChange{{Action: action, Module: "example.com/alpha@v1.0.0"}}, Environment: batchEnvironment(t, proxy),
			}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, []applicationgenerate.ModuleRequirement{requirement}, func() error {
					validated = true
					return nil
				})
			})
			if !errors.Is(err, ErrChange) || !validated {
				t.Fatalf("generated runtime requirement changed requested version: validated=%t error=%v", validated, err)
			}
			after, captureErr := captureModuleFiles(root)
			if captureErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("version conflict did not restore metadata: %v", captureErr)
			}
		})
	}
}

func TestChangeDependenciesFailureRestoresMetadata(t *testing.T) {
	proxy := writeBatchProxy(t)
	for _, failure := range []string{"get", "constraint", "operation", "tidy", "validation", "retained removal", "cancellation", "nil validation"} {
		t.Run(failure, func(t *testing.T) {
			root := writeBatchModule(t, "package application\n")
			environment := batchEnvironment(t, proxy)
			change := DependencyChange{Action: "add", Module: "example.com/gamma@v1.0.0"}
			if failure == "get" {
				change.Module = "example.com/gamma@v9.0.0+incompatible"
			} else if failure == "retained removal" {
				change = DependencyChange{Action: "remove", Module: "example.com/gone"}
				writeBatchFile(t, root, "application.go", "package application\nimport _ \"example.com/gone\"\n")
			} else if failure == "tidy" {
				writeBatchFile(t, root, "application.go", "package application\nimport _ \"example.com/missing\"\n")
			}
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			injected := errors.New("candidate validation failed")
			var observations []bool
			changes := []DependencyChange{change}
			if failure == "constraint" {
				changes = append(changes, DependencyChange{Action: "update", Module: "example.com/alpha@v1.0.0"})
			}
			err = ChangeDependencies(ctx, root, DependencyBatchOptions{
				Changes: changes, Environment: environment,
				Observe: func(started bool) { observations = append(observations, started) },
			}, func(mutate applicationgenerate.ModuleMutation) error {
				if failure == "get" || failure == "constraint" {
					t.Fatal("operation ran after go get failure")
				}
				if failure == "operation" {
					return injected
				}
				if failure == "cancellation" {
					cancel()
				}
				validate := func() error {
					if failure == "validation" {
						return injected
					}
					return nil
				}
				if failure == "nil validation" {
					validate = nil
				}
				return mutate(ctx, root, nil, validate)
			})
			if err == nil || !reflect.DeepEqual(observations, []bool{true}) {
				t.Fatalf("failure=%s: observations=%v error=%v", failure, observations, err)
			}
			if failure == "cancellation" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			after, captureErr := captureModuleFiles(root)
			if captureErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("module metadata not restored: %v", captureErr)
			}
		})
	}
}

func TestChangeDependenciesPreconditionsRunBeforeGoCommand(t *testing.T) {
	root := writeBatchModule(t, "package application\n")
	tooMany := make([]DependencyChange, maximumDependencyChanges+1)
	for index := range tooMany {
		tooMany[index] = DependencyChange{Action: "add", Module: fmt.Sprintf("example.com/module%d@v1.0.0", index)}
	}
	for _, changes := range [][]DependencyChange{
		nil,
		tooMany,
		{{Action: "add", Module: "-u"}},
		{{Action: "add", Module: "example.com/new@none"}},
		{{Action: "add", Module: "example.com/new@bad\xffrevision"}},
		{{Action: "add", Module: "example.com/new@" + strings.Repeat("a", maximumDependencyModuleBytes)}},
		{{Action: "remove", Module: "example.com/gone@v1.0.0"}},
		{{Action: "update", Module: "example.com/missing@v1.0.0"}},
		{{Action: "remove", Module: "example.com/missing"}},
		{{Action: "replace", Module: "example.com/gone"}},
		{{Action: "add", Module: "example.com/application@v1.0.0"}},
		{{Action: "add", Module: "example.com/new@v1.0.0"}, {Action: "update", Module: "example.com/new@v1.1.0"}},
		{{Action: "add", Module: "example.com/gamma@v1.0.0"}, {Action: "update", Module: "example.com/new@v1.0.0"}},
		{{Action: "update", Module: "example.com/alpha@v1.1.0"}, {Action: "remove", Module: "example.com/alpha"}},
	} {
		observed := false
		before, _ := captureModuleFiles(root)
		err := ChangeDependencies(t.Context(), root, DependencyBatchOptions{
			GoCommand: filepath.Join(t.TempDir(), "missing-go.exe"), Changes: changes,
			Observe: func(bool) { observed = true },
		}, func(applicationgenerate.ModuleMutation) error { t.Fatal("invalid batch reached operation"); return nil })
		after, captureErr := captureModuleFiles(root)
		if !errors.Is(err, ErrChange) || observed || captureErr != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("invalid batch crossed preflight boundary: observed=%t error=%v capture=%v", observed, err, captureErr)
		}
	}
	valid := DependencyBatchOptions{Changes: []DependencyChange{{Action: "add", Module: "example.com/new@v1.0.0"}}}
	if err := ChangeDependencies(nil, root, valid, func(applicationgenerate.ModuleMutation) error { return nil }); !errors.Is(err, ErrChange) {
		t.Fatalf("nil context accepted: %v", err)
	}
	if err := ChangeDependencies(t.Context(), root, valid, nil); !errors.Is(err, ErrChange) {
		t.Fatalf("nil operation accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ChangeDependencies(ctx, root, valid, func(applicationgenerate.ModuleMutation) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preflight lost its cause: %v", err)
	}
}

func TestChangeObservesStartedCommandAndRestoresAfterObserverPanic(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "failure", "absent", "panic"} {
		t.Run(mode, func(t *testing.T) {
			root := writeChangeModule(t)
			before, _ := captureModuleFiles(root)
			environment := append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change")
			goCommand := command
			if mode == "failure" {
				environment = append(environment, "PLYSTRA_MODULE_MUTATION_RESULT=get-failure")
			} else if mode == "absent" {
				goCommand = filepath.Join(t.TempDir(), "missing-go.exe")
			}
			var observations []bool
			panicked := false
			var changeErr error
			func() {
				defer func() { panicked = recover() != nil }()
				changeErr = Change(t.Context(), root, ChangeOptions{
					GoCommand: goCommand, Environment: environment, Arguments: []string{"get", "example.com/dependency@v1.0.0"},
					Observe: func(started bool) {
						observations = append(observations, started)
						if mode == "panic" {
							panic("observer failed")
						}
					},
				}, func(applicationgenerate.ModuleMutation) error { return nil })
			}()
			if !reflect.DeepEqual(observations, []bool{mode != "absent"}) || panicked != (mode == "panic") || (changeErr == nil) != (mode == "success" || mode == "panic") {
				t.Fatalf("observation mode=%s observed=%v panic=%t error=%v", mode, observations, panicked, changeErr)
			}
			if mode != "success" {
				after, captureErr := captureModuleFiles(root)
				if captureErr != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("observer boundary did not restore metadata: %v", captureErr)
				}
			}
		})
	}
}

func writeBatchModule(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	writeBatchFile(t, root, "go.mod", "module example.com/application\n\ngo 1.26\n\nrequire (\n example.com/alpha v1.0.0\n example.com/beta v1.0.0 // indirect\n example.com/gone v1.0.0\n)\n")
	writeBatchFile(t, root, "application.go", source)
	return root
}

func writeBatchFile(t *testing.T, root, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o640); err != nil {
		t.Fatal(err)
	}
}

func writeBatchProxy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, candidate := range []struct{ path, version, requirements, imports string }{
		{"example.com/alpha", "v1.0.0", "", ""},
		{"example.com/alpha", "v1.1.0", "", ""},
		{"example.com/beta", "v1.0.0", "require example.com/alpha v1.0.0\n", "import _ \"example.com/alpha\"\n"},
		{"example.com/beta", "v1.1.0", "require example.com/alpha v1.1.0\n", "import _ \"example.com/alpha\"\n"},
		{"example.com/gamma", "v1.0.0", "require example.com/beta v1.1.0\n", "import _ \"example.com/beta\"\n"},
		{"example.com/gone", "v1.0.0", "", ""},
	} {
		versionRoot := filepath.Join(candidate.path, "@v")
		list, err := os.ReadFile(filepath.Join(root, versionRoot, "list"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		writeBatchFile(t, root, filepath.Join(versionRoot, "list"), string(list)+candidate.version+"\n")
		writeBatchFile(t, root, filepath.Join(versionRoot, candidate.version+".info"), fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-10-24T00:00:00Z\"}\n", candidate.version))
		manifest := "module " + candidate.path + "\n\ngo 1.26\n\n" + candidate.requirements
		writeBatchFile(t, root, filepath.Join(versionRoot, candidate.version+".mod"), manifest)
		archiveFile, err := os.Create(filepath.Join(root, versionRoot, candidate.version+".zip"))
		if err != nil {
			t.Fatal(err)
		}
		archive := zip.NewWriter(archiveFile)
		for _, file := range []struct{ name, content string }{{"go.mod", manifest}, {"dependency.go", "package dependency\n" + candidate.imports}} {
			entry, err := archive.Create(candidate.path + "@" + candidate.version + "/" + file.name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte(file.content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := archiveFile.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func batchEnvironment(t *testing.T, proxy string) []string {
	t.Helper()
	proxy = filepath.ToSlash(proxy)
	if runtime.GOOS == "windows" {
		proxy = "/" + proxy
	}
	settings := map[string]string{
		"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-modcacherw -buildvcs=false",
		"GOPROXY": (&url.URL{Scheme: "file", Path: proxy}).String(), "GOSUMDB": "off",
		"GONOPROXY": "none", "GONOSUMDB": "", "GOPRIVATE": "", "GOMODCACHE": t.TempDir(),
	}
	var result []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := settings[strings.ToUpper(key)]; !replaced {
			result = append(result, item)
		}
	}
	for key, value := range settings {
		result = append(result, key+"="+value)
	}
	return result
}
