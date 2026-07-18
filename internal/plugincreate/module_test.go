package plugincreate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestTidyModuleRestoresMetadataWhenGenerationFailsAfterMutation(t *testing.T) {
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
	err = tidyModule(t.Context(), root, command, append(os.Environ(), "PLYSTRA_PLUGIN_CREATE_ROLLBACK_HELPER=1"), func(mutate applicationgenerate.ModuleMutation) error {
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
		t.Fatalf("tidyModule error = %v, want later generation error", err)
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
