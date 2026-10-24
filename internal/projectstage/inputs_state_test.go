package projectstage

import (
	"errors"
	"testing"
)

func TestPrivateStateNilIndirection(t *testing.T) {
	var snapshot *inputState
	inputs := Inputs{state: &snapshot}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
		t.Fatalf("nil snapshot Recheck = %v", err)
	}
	if image, err := inputs.ProjectFile(t.Context(), "go.mod"); !errors.Is(err, ErrConcurrentChange) || image.Exists() {
		t.Fatalf("nil snapshot ProjectFile = %v, %v", image, err)
	}
	if data, err := (&Stage{inputs: inputs}).OriginalProjectBytes("go.mod", nil); !errors.Is(err, ErrStage) || data != nil {
		t.Fatalf("nil snapshot OriginalProjectBytes = %v", err)
	}
	var imageState *fileImageState
	image := FileImage{state: &imageState}
	if image.Exists() || image.Data() != nil || image.Mode() != 0 {
		t.Fatal("nil image state is not absent")
	}
}
