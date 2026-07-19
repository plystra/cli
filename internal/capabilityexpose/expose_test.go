package capabilityexpose_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestSelectedManifestWriteUsesEnvironmentAndReplacementTargets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rootData := []byte("http:\n  cors:\n    allowed_origins: [https://app.example.com]\n")
	overlayData := []byte("# Production.\nhttp:\n  cors:\n    # Inherit root origins.\n    allow_credentials: true\n  expose:\n    remove: [records.read/v1, records.write/v1]\n")
	replacementData := []byte("# Customer.\nhttp: {expose: []}\n")
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), rootData)
	writeExposureFile(t, filepath.Join(root, "plystra.production.yaml"), overlayData)
	writeExposureFile(t, filepath.Join(root, "deploy", "customer.yaml"), replacementData)
	id := mustCapabilityID(t, "records.read/v1")

	overlayWrite, changed, overlaySelection, err := capabilityexpose.SelectedManifestWrite(root, id, "", "production", []string{"PLYSTRA_CONFIG=ignored.yaml"})
	if err != nil || !changed || overlayWrite.Path != "plystra.production.yaml" || overlaySelection.Mode() != applicationgen.ConfigurationModeEnvironment || overlaySelection.Environment() != "production" {
		t.Fatalf("environment SelectedManifestWrite = changed %t, write %#v, selection %#v, %v", changed, overlayWrite, overlaySelection, err)
	}
	for _, retained := range [][]byte{
		[]byte("# Production."),
		[]byte("# Inherit root origins."),
		[]byte("allow_credentials: true"),
		[]byte("add:\n      - records.read/v1"),
		[]byte("remove: [records.write/v1]"),
	} {
		if !bytes.Contains(overlayWrite.Data, retained) {
			t.Fatalf("environment write omits %q:\n%s", retained, overlayWrite.Data)
		}
	}
	if !bytes.Equal(overlayWrite.ExpectedData, overlayData) {
		t.Fatalf("environment ExpectedData = %q", overlayWrite.ExpectedData)
	}

	replacementWrite, changed, replacementSelection, err := capabilityexpose.SelectedManifestWrite(root, id, "", "", []string{"PLYSTRA_CONFIG=deploy/customer.yaml"})
	if err != nil || !changed || replacementWrite.Path != "deploy/customer.yaml" || replacementSelection.Mode() != applicationgen.ConfigurationModeExplicit || replacementSelection.Path() != "deploy/customer.yaml" {
		t.Fatalf("replacement SelectedManifestWrite = changed %t, write %#v, selection %#v, %v", changed, replacementWrite, replacementSelection, err)
	}
	if !bytes.Contains(replacementWrite.Data, []byte("# Customer.")) || !bytes.Equal(replacementWrite.ExpectedData, replacementData) {
		t.Fatalf("replacement write = %#v", replacementWrite)
	}

	if _, _, _, err := capabilityexpose.SelectedManifestWrite(root, id, "deploy/customer.yaml", "production", nil); !errors.Is(err, capabilityexpose.ErrManifestWrite) || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("conflicting selector error = %v", err)
	}
	if got := readExposureFile(t, filepath.Join(root, "plystra.yaml")); !bytes.Equal(got, rootData) {
		t.Fatalf("planning changed root: %q", got)
	}
	if got := readExposureFile(t, filepath.Join(root, "plystra.production.yaml")); !bytes.Equal(got, overlayData) {
		t.Fatalf("planning changed overlay: %q", got)
	}
}

func TestManifestWriteUsesExactSafeSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := []byte("# Application.\nhttp:\n  address: \":8080\"\n")
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), original)
	id := mustCapabilityID(t, "records.read/v1")

	write, changed, err := capabilityexpose.ManifestWrite(root, id)
	if err != nil || !changed {
		t.Fatalf("ManifestWrite = changed %t, %#v, %v", changed, write, err)
	}
	if write.Path != "plystra.yaml" || !bytes.Equal(write.ExpectedData, original) || !bytes.Contains(write.Data, []byte("- records.read/v1\n")) || !bytes.Contains(write.Data, []byte("# Application.")) {
		t.Fatalf("ManifestWrite = %#v", write)
	}
	write.ExpectedData[0] = 'x'
	write.Data[0] = 'x'
	current, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("planned write changed source manifest: %q, %v", current, err)
	}

	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), []byte("http: {expose: [records.read/v1]}\n"))
	repeated, repeatedChanged, err := capabilityexpose.ManifestWrite(root, id)
	if err != nil || repeatedChanged || repeated.Path != "" || repeated.Data != nil || repeated.ExpectedData != nil {
		t.Fatalf("idempotent ManifestWrite = changed %t, %#v, %v", repeatedChanged, repeated, err)
	}
}

func TestManifestWriteReplacesExactSparseRemoval(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := []byte("# Selected environment.\nhttp:\n  expose:\n    remove:\n      - records.read/v1\n      - records.write/v1\n")
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), original)

	write, changed, err := capabilityexpose.ManifestWrite(root, mustCapabilityID(t, "records.read/v1"))
	if err != nil || !changed {
		t.Fatalf("ManifestWrite = changed %t, %#v, %v", changed, write, err)
	}
	for _, expected := range [][]byte{
		[]byte("# Selected environment."),
		[]byte("add:\n      - records.read/v1"),
		[]byte("remove:\n      - records.write/v1"),
	} {
		if !bytes.Contains(write.Data, expected) {
			t.Fatalf("ManifestWrite data omits %q:\n%s", expected, write.Data)
		}
	}
	if !bytes.Equal(write.ExpectedData, original) {
		t.Fatalf("ManifestWrite ExpectedData = %q", write.ExpectedData)
	}
}

func TestManifestWriteRejectsUnsafeOrInvalidInputWithoutSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, _, err := capabilityexpose.ManifestWrite(root, mustCapabilityID(t, "records.read/v1")); !errors.Is(err, capabilityexpose.ErrManifestWrite) {
		t.Fatalf("missing ManifestWrite error = %v", err)
	}

	secret := "unique-private-value"
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), []byte("config:\n  acme.records:\n    password: "+secret+"\nhttp: {expose: {add: invalid}}\n"))
	if _, _, err := capabilityexpose.ManifestWrite(root, mustCapabilityID(t, "records.read/v1")); !errors.Is(err, capabilityexpose.ErrManifestWrite) || strings.Contains(err.Error(), secret) {
		t.Fatalf("invalid ManifestWrite error = %v", err)
	}

	if _, _, err := capabilityexpose.ManifestWrite(root, capabilityid.Identifier{}); !errors.Is(err, capabilityexpose.ErrManifestWrite) {
		t.Fatalf("empty Capability error = %v", err)
	}
}

func TestExposeRequiresExactCapabilityAndPlystraProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := capabilityexpose.Expose(t.Context(), capabilityexpose.Options{Start: root, Reference: "records.read"}); !errors.Is(err, capabilityexpose.ErrExpose) || !strings.Contains(err.Error(), "exact Capability ID") {
		t.Fatalf("unversioned Expose error = %v", err)
	}
	writeExposureFile(t, filepath.Join(root, "go.mod"), []byte("module example.com/acme/app\n\ngo 1.26\n"))
	if _, err := capabilityexpose.Expose(t.Context(), capabilityexpose.Options{Start: root, Reference: "records.read/v1"}); !errors.Is(err, capabilityexpose.ErrExpose) || !errors.Is(err, projectlocate.ErrNotFound) || !strings.Contains(err.Error(), "plystra.yaml") {
		t.Fatalf("ordinary module Expose error = %v", err)
	}
	var nilContext context.Context
	if _, err := capabilityexpose.Expose(nilContext, capabilityexpose.Options{}); !errors.Is(err, capabilityexpose.ErrExpose) {
		t.Fatalf("nil-context Expose error = %v", err)
	}
}

func mustCapabilityID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}

func writeExposureFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func readExposureFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func TestReadManifestSnapshotDataIsDefensive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := []byte("{}\n")
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), original)
	snapshot, err := applicationresolve.ReadManifestSnapshot(root)
	if err != nil {
		t.Fatalf("ReadManifestSnapshot: %v", err)
	}
	first := snapshot.Data()
	first[0] = 'x'
	if second := snapshot.Data(); !bytes.Equal(second, original) {
		t.Fatalf("Snapshot.Data exposed storage: %q", second)
	}
}
