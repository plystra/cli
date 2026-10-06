package applicationmeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestComposeCORSFailureSourceOwnership(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(nil)
	manifest, err := applicationmeta.ParseSource("plystra.yaml", []byte("http: {cors: {allowed_origins: ['*'], allow_credentials: true}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/application")
	if err != nil {
		t.Fatal(err)
	}
	_, err = applicationmeta.Compose(nil, manifest, lookup)
	if !errors.Is(err, applicationmeta.ErrCompose) || !strings.Contains(err.Error(), "http.cors cannot combine wildcard origin") {
		t.Fatalf("Compose = %v", err)
	}
	var located interface {
		ModulePath() string
		SourcePath() string
	}
	if !errors.As(err, &located) || located.ModulePath() != "example.com/application" || located.SourcePath() != "plystra.yaml" {
		t.Fatalf("Compose lost root declaration source: %v", err)
	}
}

func TestComposeCORSOverlayFailureRetainsOverlaySource(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(nil)
	root, err := applicationmeta.ParseSource("plystra.yaml", []byte("http: {cors: {allowed_origins: [https://shared.example], allow_credentials: true}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	root, err = applicationmeta.WithProjectModule(root, "example.com/application")
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("http: {cors: {allowed_origins: ['*']}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	_, err = applicationmeta.Compose(nil, selected, lookup)
	var overlayError *applicationmeta.EnvironmentOverlayError
	if !errors.As(err, &overlayError) || overlayError.SourcePath() != "plystra.production.yaml" {
		t.Fatalf("Compose overlay error = %v", err)
	}
}
