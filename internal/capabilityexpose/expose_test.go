package capabilityexpose_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/projectlocate"
)

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

func TestManifestWriteRejectsUnsafeOrInvalidInputWithoutSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, _, err := capabilityexpose.ManifestWrite(root, mustCapabilityID(t, "records.read/v1")); !errors.Is(err, capabilityexpose.ErrManifestWrite) {
		t.Fatalf("missing ManifestWrite error = %v", err)
	}

	secret := "unique-private-value"
	writeExposureFile(t, filepath.Join(root, "plystra.yaml"), []byte("config:\n  acme.records:\n    password: "+secret+"\nhttp: {expose: {}}\n"))
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
