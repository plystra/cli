package applicationmeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRestoreDependencyBaselineRejectsTampering(t *testing.T) {
	t.Parallel()

	composition, err := applicationmeta.Compose(nil, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("Compose empty baseline: %v", err)
	}
	baseline := composition.DependencyBaseline()
	if !baseline.Valid() || baseline.Digest() == "" || baseline.Records() == nil {
		t.Fatalf("empty baseline = %#v", baseline)
	}
	restored, err := applicationmeta.RestoreDependencyBaseline(baseline.Digest(), baseline.Records())
	if err != nil || !restored.Valid() || restored.Digest() != baseline.Digest() {
		t.Fatalf("RestoreDependencyBaseline = %#v, %v", restored, err)
	}
	_, err = applicationmeta.RestoreDependencyBaseline(strings.Repeat("0", len(baseline.Digest())), baseline.Records())
	if !errors.Is(err, applicationmeta.ErrDependencyBaseline) {
		t.Fatalf("tampered baseline error = %v", err)
	}
}
