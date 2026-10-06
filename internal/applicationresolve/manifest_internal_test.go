package applicationresolve

import (
	"errors"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
)

func TestGeneratedManifestConcurrentChangeUsesAffectedArtifactSource(t *testing.T) {
	t.Parallel()

	cause := atomicfs.NewConcurrentChangeError(
		[]string{generatedApplicationManifestName},
		fmt.Errorf("%w: generated manifest changed", ErrConcurrentChange),
	)
	err := generatedManifestSourceError("example.com/application", cause)
	var source *ManifestSourceError
	if !errors.As(err, &source) || source == nil || source.ModulePath() != "example.com/application" || source.SourcePath() != generatedApplicationManifestName || source.SourceKind() != generatedArtifactSourceKind || source.Line() != 0 || source.Column() != 0 {
		t.Fatalf("generatedManifestSourceError source = %#v, %v", source, err)
	}
	if !errors.Is(err, ErrConcurrentChange) || err.Error() != cause.Error() {
		t.Fatalf("generatedManifestSourceError = %v, want unchanged concurrent failure", err)
	}
}
