package modulemutation

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
)

func TestMain(main *testing.M) {
	switch os.Getenv("PLYSTRA_MODULE_MUTATION_HELPER") {
	case "1":
		os.Exit(runTidyHelper())
	case "change":
		os.Exit(runChangeHelper())
	}
	os.Exit(main.Run())
}

func runTidyHelper() int {
	if len(os.Args) != 3 || os.Args[1] != "mod" || os.Args[2] != "tidy" {
		return 8
	}
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return 10
	}
	data = append(data, []byte("\nrequire example.com/temporary v1.0.0 // indirect\n")...)
	if err := os.WriteFile("go.mod", data, 0o644); err != nil {
		return 11
	}
	if err := os.WriteFile("go.sum", []byte("temporary module metadata\n"), 0o644); err != nil {
		return 12
	}
	return 0
}

func runChangeHelper() int {
	if len(os.Args) == 3 && os.Args[1] == "get" && os.Args[2] == "example.com/dependency@v1.0.0" {
		data, err := os.ReadFile("go.mod")
		if err != nil {
			return 20
		}
		data = append(data, []byte("\nrequire example.com/dependency v1.0.0\n")...)
		if err := os.WriteFile("go.mod", data, 0o644); err != nil {
			return 21
		}
		if err := os.WriteFile("go.sum", []byte("example.com/dependency v1.0.0 h1:changed\n"), 0o644); err != nil {
			return 22
		}
		if os.Getenv("PLYSTRA_MODULE_MUTATION_RESULT") == "get-failure" {
			return 23
		}
		return 0
	}
	if len(os.Args) == 3 && os.Args[1] == "mod" && os.Args[2] == "tidy" {
		data, err := os.ReadFile("go.mod")
		if err != nil {
			return 24
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "module ") {
			return 25
		}
		if err := os.WriteFile("go.mod", []byte(lines[0]+"\n\ngo 1.26\n"), 0o644); err != nil {
			return 26
		}
		if err := os.WriteFile("go.sum", []byte("example.com/transitive v1.0.0 h1:tidied\n"), 0o644); err != nil {
			return 27
		}
		return 0
	}
	return 28
}

func TestChangePreservesExplicitDependencyAndCommitsValidatedMetadata(t *testing.T) {
	t.Parallel()

	root := writeChangeModule(t)
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	environment := append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change")
	validated := false
	err = Change(t.Context(), root, ChangeOptions{
		GoCommand:          command,
		Environment:        environment,
		Arguments:          []string{"get", "example.com/dependency@v1.0.0"},
		DirectRequirements: []string{"example.com/dependency"},
	}, func(mutate applicationgenerate.ModuleMutation) error {
		return mutate(t.Context(), root, func() error {
			validated = true
			data, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), "require example.com/dependency v1.0.0") {
				return errors.New("explicit dependency was not preserved through tidy")
			}
			return nil
		})
	})
	if err != nil || !validated {
		t.Fatalf("Change = validated %t, %v", validated, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	for _, retained := range []string{"example.com/dependency v1.0.0 h1:changed", "example.com/transitive v1.0.0 h1:tidied"} {
		if !strings.Contains(string(data), retained) {
			t.Fatalf("go.sum omits %q: %s", retained, data)
		}
	}
}

func TestChangeRestoresModuleMetadataAfterEveryFailureBoundary(t *testing.T) {
	t.Parallel()

	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	laterFailure := errors.New("generation failed after dependency mutation")
	tests := []struct {
		name        string
		environment []string
		operation   func(applicationgenerate.ModuleMutation, string) error
	}{
		{
			name:        "Go command failure after mutation",
			environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change", "PLYSTRA_MODULE_MUTATION_RESULT=get-failure"),
			operation: func(applicationgenerate.ModuleMutation, string) error {
				t.Fatal("operation ran after failed Go command")
				return nil
			},
		},
		{
			name:        "generation failure before tidy",
			environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
			operation: func(applicationgenerate.ModuleMutation, string) error {
				return laterFailure
			},
		},
		{
			name:        "validation failure after tidy",
			environment: append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
			operation: func(mutate applicationgenerate.ModuleMutation, root string) error {
				return mutate(t.Context(), root, func() error { return laterFailure })
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeChangeModule(t)
			before, err := captureModuleFiles(root)
			if err != nil {
				t.Fatalf("capture before: %v", err)
			}
			err = Change(t.Context(), root, ChangeOptions{
				GoCommand:          command,
				Environment:        test.environment,
				Arguments:          []string{"get", "example.com/dependency@v1.0.0"},
				DirectRequirements: []string{"example.com/dependency"},
			}, func(mutate applicationgenerate.ModuleMutation) error {
				return test.operation(mutate, root)
			})
			if err == nil {
				t.Fatal("Change succeeded at injected failure boundary")
			}
			after, captureErr := captureModuleFiles(root)
			if captureErr != nil {
				t.Fatalf("capture after: %v", captureErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("module metadata changed after failure:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestChangePreservesConcurrentModuleEditAndRestoresOtherMetadata(t *testing.T) {
	t.Parallel()

	root := writeChangeModule(t)
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	laterFailure := errors.New("generation failed after concurrent edit")
	var concurrentGoMod []byte
	err = Change(t.Context(), root, ChangeOptions{
		GoCommand:          command,
		Environment:        append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=change"),
		Arguments:          []string{"get", "example.com/dependency@v1.0.0"},
		DirectRequirements: []string{"example.com/dependency"},
	}, func(applicationgenerate.ModuleMutation) error {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			return err
		}
		concurrentGoMod = append(data, []byte("\n// concurrent user edit\n")...)
		if err := os.WriteFile(filepath.Join(root, "go.mod"), concurrentGoMod, 0o644); err != nil {
			return err
		}
		return laterFailure
	})
	if !errors.Is(err, laterFailure) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("Change error = %v, want later failure and concurrent-change diagnostic", err)
	}
	current, readErr := os.ReadFile(filepath.Join(root, "go.mod"))
	if readErr != nil || !reflect.DeepEqual(current, concurrentGoMod) {
		t.Fatalf("concurrent go.mod = %q, %v; want preserved %q", current, readErr, concurrentGoMod)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.sum")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("go.sum was not independently restored: %v", statErr)
	}
}

func writeChangeModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/acme/change\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	return root
}

func TestTidyRestoresMetadataWhenGenerationFailsAfterMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/acme/post-mutation-rollback\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	before, err := captureModuleFiles(root)
	if err != nil {
		t.Fatalf("capture original module metadata: %v", err)
	}
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	laterErr := errors.New("generation failed after module mutation")
	mutated := false
	err = Tidy(t.Context(), root, command, append(os.Environ(), "PLYSTRA_MODULE_MUTATION_HELPER=1"), func(mutate applicationgenerate.ModuleMutation) error {
		if err := mutate(t.Context(), root, func() error { return nil }); err != nil {
			return err
		}
		normalized, err := captureModuleFiles(root)
		if err != nil {
			return err
		}
		mutated = !reflect.DeepEqual(normalized, before)
		return laterErr
	})
	if !errors.Is(err, laterErr) {
		t.Fatalf("Tidy error = %v, want later generation error", err)
	}
	if !mutated {
		t.Fatal("module helper did not mutate metadata before the later failure")
	}
	after, err := captureModuleFiles(root)
	if err != nil {
		t.Fatalf("capture restored module metadata: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("module metadata changed after later generation failure:\nbefore: %#v\nafter:  %#v", before, after)
	}
}
