package applicationresolve

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/generatedfiles"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestLoadGeneratedDependencyBaselinePrefersOwnershipRecovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	recoveryManifest, recoveryBaseline := renderDependencyBaseline(t, "kernel.health/v1")
	driftedManifest, driftedBaseline := renderDependencyBaseline(t, "kernel.info/v1")
	if recoveryBaseline.Digest() == driftedBaseline.Digest() {
		t.Fatal("test baselines unexpectedly have equal digests")
	}
	writeRecoveryOutput(t, root, recoveryManifest)
	writeBaselineFile(t, root, generatedfiles.ApplicationManifestPath, driftedManifest)

	baseline, _, err := loadGeneratedDependencyBaseline(root, defaultConfigurationSelector())
	if err != nil {
		t.Fatalf("loadGeneratedDependencyBaseline: %v", err)
	}
	if !baseline.Valid() || baseline.Digest() != recoveryBaseline.Digest() || baseline.Digest() == driftedBaseline.Digest() {
		t.Fatalf("baseline digest = %q, want recovery %q and not primary %q", baseline.Digest(), recoveryBaseline.Digest(), driftedBaseline.Digest())
	}
}

func TestLoadGeneratedDependencyBaselineFallsBackToPrimaryWithoutRecovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest, want := renderDependencyBaseline(t, "kernel.health/v1")
	writeBaselineFile(t, root, generatedfiles.ApplicationManifestPath, manifest)
	writeBaselineFile(t, root, generatedfiles.ManifestPath, []byte("{\"version\":2,\"files\":[]}\n"))

	baseline, _, err := loadGeneratedDependencyBaseline(root, defaultConfigurationSelector())
	if err != nil || !baseline.Valid() || baseline.Digest() != want.Digest() {
		t.Fatalf("loadGeneratedDependencyBaseline = %#v, %v", baseline.Records(), err)
	}
}

func TestLoadGeneratedDependencyBaselineRejectsMalformedRecovery(t *testing.T) {
	t.Parallel()

	validPrimary, _ := renderDependencyBaseline(t, "kernel.health/v1")
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "ownership JSON", data: []byte("{\"version\":2")},
		{name: "embedded application manifest", data: []byte("{\"version\":2,\"files\":[],\"application_manifest\":{\"invalid\":true}}\n")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeBaselineFile(t, root, generatedfiles.ApplicationManifestPath, validPrimary)
			writeBaselineFile(t, root, generatedfiles.ManifestPath, test.data)
			baseline, _, err := loadGeneratedDependencyBaseline(root, defaultConfigurationSelector())
			if err == nil || baseline.Valid() {
				t.Fatalf("loadGeneratedDependencyBaseline = %#v, %v", baseline.Records(), err)
			}
			if !bytes.Contains([]byte(err.Error()), []byte("recovery")) {
				t.Fatalf("error does not identify recovery state: %v", err)
			}
		})
	}

	t.Run("non-regular ownership manifest", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeBaselineFile(t, root, generatedfiles.ApplicationManifestPath, validPrimary)
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(generatedfiles.ManifestPath)), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		baseline, _, err := loadGeneratedDependencyBaseline(root, defaultConfigurationSelector())
		if !errors.Is(err, generatedfiles.ErrManifest) || baseline.Valid() {
			t.Fatalf("loadGeneratedDependencyBaseline = %#v, %v", baseline.Records(), err)
		}
	})
}

func renderDependencyBaseline(t testing.TB, capability string) ([]byte, applicationmeta.DependencyBaseline) {
	t.Helper()
	dependencyManifest, err := applicationmeta.Parse([]byte("capabilities:\n  require: [" + capability + "]\n"))
	if err != nil {
		t.Fatalf("Parse dependency manifest: %v", err)
	}
	currentManifest, err := applicationmeta.Parse([]byte("{}\n"))
	if err != nil {
		t.Fatalf("Parse current manifest: %v", err)
	}
	composition, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest:      dependencyManifest,
	}}, currentManifest, func(string) (kernelmanifest.Config, bool) {
		return kernelmanifest.Config{}, false
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               applicationManifestName,
		RootData:               []byte("{}\n"),
		SelectedPath:           applicationManifestName,
		SelectedData:           []byte("{}\n"),
		Composition:            composition,
		ApplicationModelDigest: "sha256:" + strings.Repeat("1", 64),
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	data, err := applicationgen.RenderManifest([]byte("{\"capability_aliases\":[]}"), provenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	return data, composition.DependencyBaseline()
}

func defaultConfigurationSelector() configurationSelector {
	return configurationSelector{mode: configurationModeDefault, path: applicationManifestName}
}

func writeRecoveryOutput(t testing.TB, root string, applicationManifest []byte) {
	t.Helper()
	file, err := generatedfiles.NewFile(generatedfiles.ApplicationManifestPath, applicationManifest)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	output, err := generatedfiles.NewOutput([]generatedfiles.File{file})
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	for _, desired := range output.Files() {
		writeBaselineFile(t, root, desired.Path(), desired.Data())
	}
	writeBaselineFile(t, root, generatedfiles.ManifestPath, output.ManifestJSON())
}

func writeBaselineFile(t testing.TB, root, name string, data []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(absolute, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
