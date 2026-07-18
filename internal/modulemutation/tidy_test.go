package modulemutation

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_MODULE_MUTATION_HELPER") == "1" {
		os.Exit(runTidyHelper())
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
