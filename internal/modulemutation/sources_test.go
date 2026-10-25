package modulemutation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
	"golang.org/x/mod/module"
)

func TestPrepareRuntimeSourcesMatchesNormalization(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connect, _ := applicationgenerate.NewModuleRequirement("connectrpc.com/connect", "v1.20.0")
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	olderProtobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.0")
	for _, selected := range []string{"", "v1.35.0", "v1.36.11", "v1.37.0"} {
		t.Run("selected="+selected, func(t *testing.T) {
			root := t.TempDir()
			manifest := "module example.com/runtime\ngo 1.26\nrequire connectrpc.com/connect v1.21.0\n"
			if selected != "" {
				manifest += "require google.golang.org/protobuf " + selected + " // indirect\n"
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			requirements := []applicationgenerate.ModuleRequirement{protobuf, connect, olderProtobuf}
			var captured []module.Version
			calls := 0
			err := Tidy(t.Context(), root, TidyOptions{
				GoCommand: command, Environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=runtime"),
				PrepareRuntime: func(versions []module.Version) error {
					calls++
					if _, err := os.Stat(filepath.Join(root, "runtime-get.txt")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("runtime selection started before preparation")
					}
					captured = append([]module.Version(nil), versions...)
					if len(versions) != 0 {
						versions[0] = module.Version{Path: "example.com/changed", Version: "v9.0.0"}
					}
					return nil
				},
			}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, requirements, func() error { return nil })
			})
			wantedCalls := 0
			if selected == "" || selected == "v1.35.0" {
				wantedCalls = 1
			}
			if err != nil || calls != wantedCalls {
				t.Fatalf("prepared runtime normalization: calls=%d, %v", calls, err)
			}
			var wanted []module.Version
			if selected == "" || selected == "v1.35.0" {
				wanted = []module.Version{{Path: protobuf.Path(), Version: protobuf.MinimumVersion()}}
			}
			if !reflect.DeepEqual(captured, wanted) {
				t.Fatalf("prepared versions = %v, want %v", captured, wanted)
			}
			query, err := os.ReadFile(filepath.Join(root, "runtime-get.txt"))
			if len(wanted) == 0 {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatal("sufficient version unexpectedly selected a new runtime", err)
				}
			} else if err != nil || string(query) != wanted[0].String()+"\n" {
				t.Fatalf("actual runtime query = %q, %v", query, err)
			}
		})
	}
}

func TestPrepareRuntimeSourcesRejectsFailureAndMetadataChanges(t *testing.T) {
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	for _, mode := range []string{"reject", "cancel", "edit-mod", "edit-sum", "create-sum", "remove-sum", "joined"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/runtime\ngo 1.26\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if mode != "create-sum" {
				if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("original sums\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("source retention rejected")
			var afterCallback moduleFiles
			err := Tidy(ctx, root, TidyOptions{GoCommand: "not-a-go-command", PrepareRuntime: func([]module.Version) error {
				name, data := "go.sum", "changed sums\n"
				if mode == "edit-mod" {
					name, data = "go.mod", "module example.com/runtime\ngo 1.26\n// concurrent edit\n"
				}
				if mode == "remove-sum" {
					if err := os.Remove(filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
				} else if mode != "reject" && mode != "cancel" {
					if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				var err error
				afterCallback, err = captureModuleFiles(root)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" || mode == "joined" {
					cancel()
				}
				if mode == "reject" || mode == "joined" {
					return rejected
				}
				return nil
			}}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(ctx, root, []applicationgenerate.ModuleRequirement{protobuf}, func() error { t.Fatal("validation ran after preparation failure"); return nil })
			})
			if !errors.Is(err, ErrTidy) || (mode == "reject" || mode == "joined") && !errors.Is(err, rejected) || (mode == "cancel" || mode == "joined") && !errors.Is(err, context.Canceled) || mode != "reject" && mode != "cancel" && !errors.Is(err, atomicfs.ErrConcurrentChange) {
				t.Fatalf("preparation lost failure evidence: %v", err)
			}
			after, captureErr := captureModuleFiles(root)
			if captureErr != nil || !reflect.DeepEqual(after, afterCallback) {
				t.Fatal("preparation overwrote callback metadata", captureErr)
			}
		})
	}
}

func TestPrepareRuntimeSourcesRejectsInvalidInputBeforeCallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/runtime\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name         string
		ctx          context.Context
		requirements []applicationgenerate.ModuleRequirement
	}{
		{name: "nil-context"},
		{name: "cancelled", ctx: cancelled},
		{name: "invalid-requirement", ctx: t.Context(), requirements: []applicationgenerate.ModuleRequirement{{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			err := Tidy(test.ctx, root, TidyOptions{PrepareRuntime: func([]module.Version) error { called = true; return nil }}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(test.ctx, root, test.requirements, func() error { return nil })
			})
			if !errors.Is(err, ErrTidy) || called {
				t.Fatalf("invalid runtime preparation = %v, callback=%t", err, called)
			}
		})
	}
}

func TestChangeRuntimePreparationKeepsConcurrentMetadata(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	for _, name := range []string{"go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			root := writeChangeModule(t)
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			rejected := errors.New("reject runtime source")
			var changed []byte
			err = Change(t.Context(), root, ChangeOptions{
				GoCommand: command, Environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
				Arguments: []string{"get", "example.com/dependency@v1.0.0"},
				PrepareRuntime: func([]module.Version) error {
					data, err := os.ReadFile(filepath.Join(root, name))
					if err != nil {
						t.Fatal(err)
					}
					changed = append(data, []byte("\n// concurrent runtime preparation edit\n")...)
					if err := os.WriteFile(filepath.Join(root, name), changed, 0o644); err != nil {
						t.Fatal(err)
					}
					return rejected
				},
			}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, []applicationgenerate.ModuleRequirement{protobuf}, func() error { t.Fatal("validated after rejected preparation"); return nil })
			})
			if !errors.Is(err, rejected) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
				t.Fatalf("runtime preparation lost concurrent edit evidence: %v", err)
			}
			after, captureErr := captureModuleFiles(root)
			if captureErr != nil || string(after[name].data) != string(changed) {
				t.Fatal("runtime failure overwrote a concurrent edit", captureErr)
			}
			other := "go.mod"
			if name == other {
				other = "go.sum"
			}
			if !reflect.DeepEqual(after[other], before[other]) {
				t.Fatal("runtime failure did not independently restore owned metadata")
			}
		})
	}
}

func TestChangeRuntimePreparationPreservesModeOnlyEdits(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	for _, test := range []struct {
		name      string
		panicking bool
	}{
		{"go.mod", false}, {"go.sum", false}, {"go.mod", true}, {"go.sum", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := test.name
			root := writeChangeModule(t)
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, name), 0o644) })
			var edited moduleFiles
			panicValue := errors.New("runtime callback panic")
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				err = Change(t.Context(), root, ChangeOptions{
					GoCommand: command, Environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
					Arguments: []string{"get", "example.com/dependency@v1.0.0"},
					PrepareRuntime: func([]module.Version) error {
						if err := os.Chmod(filepath.Join(root, name), 0o444); err != nil {
							t.Fatal(err)
						}
						var err error
						edited, err = captureModuleFiles(root)
						if test.panicking {
							panic(panicValue)
						}
						return err
					},
				}, func(mutate applicationgenerate.ModuleMutation) error {
					return mutate(t.Context(), root, []applicationgenerate.ModuleRequirement{protobuf}, func() error {
						t.Fatal("validation ran after a concurrent mode edit")
						return nil
					})
				})
			}()
			if test.panicking && recovered != panicValue || !test.panicking && recovered != nil {
				t.Fatal("runtime callback panic was replaced or swallowed")
			}
			if !test.panicking && !errors.Is(err, atomicfs.ErrConcurrentChange) {
				t.Fatalf("mode drift was not rejected: %v", err)
			}
			after, err := captureModuleFiles(root)
			if err != nil || !reflect.DeepEqual(after[name], edited[name]) {
				t.Fatal("rollback overwrote the callback's mode-only edit", err)
			}
			other := "go.mod"
			if name == other {
				other = "go.sum"
			}
			if !reflect.DeepEqual(after[other], before[other]) {
				t.Fatal("untouched module metadata was not restored independently")
			}
		})
	}
}

func TestChangeRuntimePreparationRestoresOtherFileAfterCaptureFailure(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	for _, name := range []string{"go.mod", "go.sum"} {
		t.Run(name, func(t *testing.T) {
			root := writeChangeModule(t)
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			err = Change(t.Context(), root, ChangeOptions{
				GoCommand: command, Environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
				Arguments: []string{"get", "example.com/dependency@v1.0.0"},
				PrepareRuntime: func([]module.Version) error {
					if err := os.Remove(filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
					return os.Mkdir(filepath.Join(root, name), 0o700)
				},
			}, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, []applicationgenerate.ModuleRequirement{protobuf}, func() error {
					t.Fatal("validation ran after invalid metadata capture")
					return nil
				})
			})
			if !errors.Is(err, atomicfs.ErrConcurrentChange) {
				t.Fatalf("invalid metadata was not rejected: %v", err)
			}
			if info, err := os.Stat(filepath.Join(root, name)); err != nil || !info.IsDir() {
				t.Fatal("callback directory was overwritten", err)
			}
			other := "go.mod"
			if name == other {
				other = "go.sum"
			}
			files, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			after, err := captureModuleFile(files, other)
			if err != nil || !reflect.DeepEqual(after, before[other]) {
				t.Fatal("unaffected metadata was not independently restored", err)
			}
		})
	}
}

func TestDependencyBatchPreparesGeneratedRuntimeTargets(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := writeChangeModule(t)
	before, err := captureModuleFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	protobuf, _ := applicationgenerate.NewModuleRequirement("google.golang.org/protobuf", "v1.36.11")
	rejected := errors.New("reject runtime target")
	var batches [][]module.Version
	err = ChangeDependencies(t.Context(), root, DependencyBatchOptions{
		GoCommand: command, Environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
		Changes: []DependencyChange{{Action: "add", Module: "example.com/dependency@v1.0.0"}},
		Prepare: func(versions []module.Version) error {
			batches = append(batches, append([]module.Version(nil), versions...))
			if len(batches) == 2 {
				return rejected
			}
			return nil
		},
	}, func(mutate applicationgenerate.ModuleMutation) error {
		return mutate(t.Context(), root, []applicationgenerate.ModuleRequirement{protobuf}, func() error { t.Fatal("validated rejected runtime target"); return nil })
	})
	wanted := [][]module.Version{{{Path: "example.com/dependency", Version: "v1.0.0"}}, {{Path: protobuf.Path(), Version: protobuf.MinimumVersion()}}}
	if !errors.Is(err, rejected) || !reflect.DeepEqual(batches, wanted) {
		t.Fatalf("runtime target not prepared: batches=%v, %v", batches, err)
	}
	after, captureErr := captureModuleFiles(root)
	if captureErr != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("rejected runtime preparation did not restore the dependency transaction", captureErr)
	}
}
