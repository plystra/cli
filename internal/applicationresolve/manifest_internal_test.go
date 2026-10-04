package applicationresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/atomicfs"
)

func TestRuntimeTemplatesAreCapturedPrivateSnapshots(t *testing.T) {
	const contents = "config: {example.com/dependency/service.New: {label: PRIVATE_TEMPLATE}}\n"
	result := Result{dependencySnapshots: []dependencyManifestSnapshot{{modulePath: "example.com/dependency", version: "v1.2.3", snapshot: ManifestSnapshot{data: []byte(contents)}}}}
	exports, err := result.RuntimeTemplates()
	want, wantErr := applicationmeta.PrivateTemplateYAML([]byte(contents))
	if err != nil || wantErr != nil || len(exports) != 1 || exports[0].Module != "example.com/dependency" || exports[0].Version != "v1.2.3" || exports[0].YAML != string(want) {
		t.Fatal("captured templates lost identity or data")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if strings.Contains(fmt.Sprintf(format, exports), "PRIVATE_TEMPLATE") {
			t.Fatal("formatted templates are not redacted")
		}
	}
	exports[0].YAML = "changed"
	repeated, err := result.RuntimeTemplates()
	if err != nil || repeated[0].YAML != string(want) {
		t.Fatal("mutated resolver snapshot")
	}
}

func TestChangedDependencyConfigurationModulesUsesPrivateSnapshots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, applicationManifestName)
	if err := os.WriteFile(path, []byte("config: {private: first}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ReadManifestSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("config: {private: second}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := ReadManifestSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	before := Result{dependencySnapshots: []dependencyManifestSnapshot{
		{modulePath: "example.com/changed", snapshot: first},
		{modulePath: "example.com/removed", snapshot: first},
		{modulePath: "example.com/unchanged", snapshot: first},
	}}
	after := Result{dependencySnapshots: []dependencyManifestSnapshot{
		{modulePath: "example.com/unchanged", snapshot: first},
		{modulePath: "example.com/changed", snapshot: second},
		{modulePath: "example.com/added", snapshot: second},
	}}
	want := []string{"example.com/added", "example.com/changed", "example.com/removed"}
	if got := before.ChangedDependencyConfigurationModules(after); !reflect.DeepEqual(got, want) {
		t.Fatalf("changed dependency modules = %v, want %v", got, want)
	}
	if got := before.ChangedDependencyConfigurationModules(before); len(got) != 0 {
		t.Fatalf("identical private snapshots changed: %v", got)
	}
}

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
