package capabilitysource_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
)

func TestWithManifestSourcePreservesTypedCauseAndTrustedProvenance(t *testing.T) {
	t.Parallel()

	id := mustID(t, "email.send/v1")
	cause := fmt.Errorf("decode contract: %w", capabilitymeta.ErrInvalidManifest)
	wrapped := capabilitysource.WithManifestSource("example.com/app", "smtp", id, cause)
	var source *capabilitysource.ManifestSourceError
	if !errors.Is(wrapped, capabilitymeta.ErrInvalidManifest) || !errors.As(wrapped, &source) || source == nil {
		t.Fatalf("WithManifestSource() = %v, want typed source and manifest cause", wrapped)
	}
	if wrapped.Error() != cause.Error() || source.CapabilityID() != id || source.ModulePath() != "example.com/app" || source.SourcePath() != "smtp/capabilities/email.send/v1/capability.yaml" || source.SourceKind() != "provider-declaration" || source.Line() != 1 || source.Column() != 1 {
		t.Fatalf("ManifestSourceError = ID %s, module %q, path %q, kind %q, position %d:%d, error %q", source.CapabilityID(), source.ModulePath(), source.SourcePath(), source.SourceKind(), source.Line(), source.Column(), wrapped)
	}
	if again := capabilitysource.WithManifestSource("example.com/other", "other", mustID(t, "other.run/v9"), wrapped); again != wrapped {
		t.Fatalf("WithManifestSource(double wrap) = %#v, want original %#v", again, wrapped)
	}

	unrelated := errors.New("unrelated")
	if got := capabilitysource.WithManifestSource("example.com/app", "smtp", id, unrelated); got != unrelated {
		t.Fatalf("WithManifestSource(unrelated) = %#v, want original %#v", got, unrelated)
	}
	if got := capabilitysource.WithManifestSource("example.com/app", "smtp", id, nil); got != nil {
		t.Fatalf("WithManifestSource(nil) = %#v, want nil", got)
	}
}

func TestManifestSourceErrorNilAccessors(t *testing.T) {
	t.Parallel()

	var source *capabilitysource.ManifestSourceError
	if source.CapabilityID().String() != "" || source.ModulePath() != "" || source.SourcePath() != "" || source.SourceKind() != "" || source.Line() != 0 || source.Column() != 0 || source.Unwrap() != nil || source.Error() != capabilitymeta.ErrInvalidManifest.Error() {
		t.Fatalf("nil ManifestSourceError accessors returned nonzero values")
	}
}
