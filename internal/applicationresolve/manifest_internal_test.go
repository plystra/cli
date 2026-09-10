package applicationresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
)

func TestRecheckDependencyManifestsRejectsConcurrentChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestPath := filepath.Join(root, applicationManifestName)
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snapshot, err := ReadManifestSnapshot(root)
	if err != nil {
		t.Fatalf("ReadManifestSnapshot: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("capabilities: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile changed manifest: %v", err)
	}

	err = recheckDependencyManifests([]dependencyManifestSnapshot{{
		modulePath: "example.com/dependency",
		identity:   "example.com/dependency@v1.2.3",
		root:       root,
		snapshot:   snapshot,
	}})
	if !errors.Is(err, ErrConcurrentChange) || !strings.Contains(err.Error(), "example.com/dependency@v1.2.3") || !strings.Contains(err.Error(), "plystra.yaml changed") {
		t.Fatalf("recheckDependencyManifests error = %v", err)
	}
	var source *ManifestSourceError
	if !errors.As(err, &source) || source == nil || source.ModulePath() != "example.com/dependency" || source.SourcePath() != "plystra.yaml" || source.SourceKind() != configurationSourceKind || source.Line() != 0 || source.Column() != 0 {
		t.Fatalf("recheckDependencyManifests source = %#v, %v", source, err)
	}
}

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
