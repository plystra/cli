package applicationmeta_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestComposeCORSFailureSourceOwnership(t *testing.T) {
	t.Parallel()
	const (
		origins       = "http: {cors: {allowed_origins: [https://shared.example]}}"
		credentialed  = "http: {cors: {allowed_origins: [https://shared.example], allow_credentials: true}}"
		wildcard      = "http: {cors: {allowed_origins: ['*']}}"
		credentials   = "http: {cors: {allow_credentials: true}}"
		invalid       = "http: {cors: {allowed_origins: ['*'], allow_credentials: true}}"
		removeOrigins = "http: {cors: {allowed_origins: {$remove: true}}}"
		removeCORS    = "http: {cors: {$remove: true}}"
		emptyCORS     = "http: {cors: {}}"
		unrelated     = "http: {address: ':8080'}"
		rootModule    = "example.com/application"
		oldModule     = "example.com/oldest"
		nearModule    = "example.com/nearest"
	)
	for _, tc := range []struct {
		name, oldest, nearest, root string
		overlays                    []string
		replacement                 bool
		missingOrigins              bool
		wantModule, wantPath        string
		wantOverlay                 bool
	}{
		{name: "overlay wildcard", root: credentialed, overlays: []string{wildcard}, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "overlay credentials", root: wildcard, overlays: []string{credentials}, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "overlay conflicts with template", oldest: credentialed, overlays: []string{wildcard}, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "root credentials", oldest: wildcard, root: credentials, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "root wildcard", oldest: credentialed, root: wildcard, overlays: []string{unrelated}, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "unrelated overlay", root: invalid, overlays: []string{unrelated}, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "empty overlay", root: invalid, overlays: []string{emptyCORS}, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "oldest template", oldest: invalid, nearest: emptyCORS, root: emptyCORS, overlays: []string{unrelated}, wantModule: oldModule, wantPath: "plystra.yaml"},
		{name: "nearest credentials", oldest: wildcard, nearest: credentials, overlays: []string{emptyCORS}, wantModule: nearModule, wantPath: "plystra.yaml"},
		{name: "nearest wildcard", oldest: credentialed, nearest: wildcard, wantModule: nearModule, wantPath: "plystra.yaml"},
		{name: "explicit equal replacement still owns", oldest: invalid, root: wildcard, overlays: []string{emptyCORS}, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "replacement is not overlay", oldest: wildcard, root: credentials, replacement: true, wantModule: rootModule, wantPath: "deploy/customer.yaml"},
		{name: "overlay removes origins", root: credentialed, overlays: []string{removeOrigins}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "root removes origins", oldest: credentialed, root: removeOrigins, overlays: []string{credentials}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "nearest removes origins", oldest: origins, nearest: removeOrigins, root: credentials, overlays: []string{emptyCORS}, missingOrigins: true, wantModule: nearModule, wantPath: "plystra.yaml"},
		{name: "partial template", oldest: credentials, overlays: []string{emptyCORS}, missingOrigins: true, wantModule: oldModule, wantPath: "plystra.yaml"},
		{name: "partial root", root: credentials, overlays: []string{unrelated}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "partial overlay", overlays: []string{credentials}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "empty object declaration", root: emptyCORS, overlays: []string{emptyCORS}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "overlay reintroduces removed object", oldest: credentialed, root: removeCORS, overlays: []string{emptyCORS}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "root reintroduces removed object", oldest: credentialed, nearest: removeCORS, root: credentials, overlays: []string{emptyCORS}, missingOrigins: true, wantModule: rootModule, wantPath: "plystra.yaml"},
		{name: "earlier overlay remains responsible", root: credentialed, overlays: []string{wildcard, unrelated}, wantModule: rootModule, wantPath: "plystra.production.yaml", wantOverlay: true},
		{name: "later overlay becomes responsible", root: credentialed, overlays: []string{wildcard, credentials}, wantModule: rootModule, wantPath: "plystra.override.yaml", wantOverlay: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := composeSchemaLookup(nil)
			var dependencies []applicationmeta.Dependency
			for index, data := range []string{tc.oldest, tc.nearest} {
				if data == "" {
					continue
				}
				manifest, err := applicationmeta.ParseTemplateSource("plystra.yaml", []byte(data))
				if err != nil {
					t.Fatalf("ParseTemplateSource: %v", err)
				}
				dependencies = append(dependencies, applicationmeta.Dependency{ModulePath: []string{oldModule, nearModule}[index], ModuleVersion: "v1.2.3", Manifest: manifest})
			}
			if tc.root == "" {
				tc.root = "{}"
			}
			path := "plystra.yaml"
			if tc.replacement {
				path = "deploy/customer.yaml"
			}
			selected, err := applicationmeta.ParseSource(path, []byte(tc.root))
			if err != nil {
				t.Fatalf("ParseSource: %v", err)
			}
			if tc.replacement {
				root := composeManifest(t, "template: "+oldModule)
				selected = applicationmeta.WithRootMetadata(selected, root)
			}
			for index, data := range tc.overlays {
				overlay, err := applicationmeta.ParseOverlaySource([]string{"plystra.production.yaml", "plystra.override.yaml"}[index], []byte(data))
				if err != nil {
					t.Fatalf("ParseOverlaySource: %v", err)
				}
				selected, err = applicationmeta.ApplyOverlay(selected, overlay, lookup)
				if err != nil {
					t.Fatalf("CORS validated before ancestry: %v", err)
				}
			}
			selected, err = applicationmeta.WithProjectModule(selected, rootModule)
			if err != nil {
				t.Fatal(err)
			}
			_, err = applicationmeta.Compose(dependencies, selected, lookup)
			if !errors.Is(err, applicationmeta.ErrCompose) || !errors.Is(err, applicationmeta.ErrInvalidManifest) {
				t.Fatalf("Compose = %v", err)
			}
			message := "http.cors cannot combine wildcard origin"
			if tc.missingOrigins {
				message = "http.cors.allowed_origins is required"
			}
			if !strings.Contains(err.Error(), message) {
				t.Fatalf("Compose = %v, want %s", err, message)
			}
			var overlayError *applicationmeta.EnvironmentOverlayError
			if errors.Is(err, applicationmeta.ErrApplyOverlay) != tc.wantOverlay || errors.As(err, &overlayError) != tc.wantOverlay {
				t.Fatalf("overlay classification = %v, want overlay %t", err, tc.wantOverlay)
			}
			var compositionError *applicationmeta.ConfigurationCompositionError
			if errors.As(err, &compositionError) == tc.wantOverlay {
				t.Fatalf("current/template classification = %v, want overlay %t", err, tc.wantOverlay)
			}
			var located interface {
				ModulePath() string
				SourcePath() string
				SourceKind() string
				Line() int
				Column() int
			}
			if !errors.As(err, &located) {
				t.Fatalf("Compose lost declaration source: %v", err)
			}
			got := fmt.Sprintf("%s:%s:%d:%d (%s)", located.ModulePath(), located.SourcePath(), located.Line(), located.Column(), located.SourceKind())
			want := tc.wantModule + ":" + tc.wantPath + ":1:1 (configuration-declaration)"
			if got != want {
				t.Fatalf("source = %s, want %s", got, want)
			}
			repair, err := applicationmeta.ParseOverlaySource("plystra.repaired.yaml", []byte(credentialed))
			if err != nil {
				t.Fatal(err)
			}
			repaired, err := applicationmeta.ApplyOverlay(selected, repair, lookup)
			if err != nil {
				t.Fatalf("repair rejected before final composition: %v", err)
			}
			if _, err := applicationmeta.Compose(dependencies, repaired, lookup); err != nil {
				t.Fatalf("repaired CORS rejected: %v", err)
			}
		})
	}
}

func TestComposeCORSOverlayInheritsCurrentModuleOwnership(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(nil)
	root, err := applicationmeta.WithProjectModule(composeManifest(t, "http: {cors: {allow_credentials: true}}"), "example.com/application")
	if err != nil {
		t.Fatal(err)
	}
	// ApplyOverlay also accepts parsed layers before their module is attached.
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("http: {cors: {allowed_origins: ['*']}}"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	_, err = applicationmeta.Compose(nil, selected, lookup)
	var invalid *applicationmeta.EnvironmentOverlayError
	if !errors.As(err, &invalid) || invalid.ModulePath() != "example.com/application" || invalid.SourcePath() != "plystra.production.yaml" {
		t.Fatalf("unbound overlay source = %#v, error %v", invalid, err)
	}
}
