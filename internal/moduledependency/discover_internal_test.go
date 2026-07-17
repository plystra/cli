package moduledependency

import (
	"fmt"
	"strings"
	"testing"
)

func TestRequirementBatchesPreserveOrderWithinPlatformBound(t *testing.T) {
	t.Parallel()

	requirements := make([]requirement, 200)
	for index := range requirements {
		requirements[index] = requirement{path: fmt.Sprintf("example.com/%03d/%s", index, strings.Repeat("segment", 12))}
	}
	batches := requirementBatches(requirements)
	if len(batches) < 2 {
		t.Fatalf("requirementBatches returned %d batch, want multiple", len(batches))
	}
	position := 0
	for _, batch := range batches {
		used := len("list -m -json -mod=readonly ")
		for _, modulePath := range batch {
			if modulePath != requirements[position].path {
				t.Fatalf("batch path %q at %d, want %q", modulePath, position, requirements[position].path)
			}
			used += len(modulePath) + 1
			position++
		}
		if used > maximumArgumentListBytes {
			t.Fatalf("batch uses %d bytes, limit %d", used, maximumArgumentListBytes)
		}
	}
	if position != len(requirements) {
		t.Fatalf("batches contain %d requirements, want %d", position, len(requirements))
	}
}
