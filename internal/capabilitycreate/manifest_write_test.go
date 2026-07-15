package capabilitycreate_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
)

func TestRenderManifestWriteUsesRetainedSnapshotWithoutMutation(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	snapshot := []byte("# Account plugin.\nid: acme.app.account\nconfig: {}\n")
	writePlugin(t, root, "account", string(snapshot))
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	write, changed, err := capabilitycreate.RenderManifestWrite(plan)
	if err != nil || !changed {
		t.Fatalf("RenderManifestWrite = changed %t, %#v, %v", changed, write, err)
	}
	wantData := []byte("# Account plugin.\nid: acme.app.account\nprovides:\n  - account.register/v1\nconfig: {}\n")
	if write.Path != "account/plugin.yaml" || !bytes.Equal(write.Data, wantData) || !bytes.Equal(write.ExpectedData, snapshot) || write.Mode != 0 || write.MustNotExist || write.ParentMustNotExist {
		t.Fatalf("manifest write = %#v", write)
	}
	manifestPath := filepath.Join(root, "account", "plugin.yaml")
	if current, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(current, snapshot) {
		t.Fatalf("manifest changed = %q, %v", current, err)
	}

	write.Data[0] = 'x'
	write.ExpectedData[0] = 'x'
	repeated, repeatedChanged, err := capabilitycreate.RenderManifestWrite(plan)
	if err != nil || !repeatedChanged || !bytes.Equal(repeated.Data, wantData) || !bytes.Equal(repeated.ExpectedData, snapshot) {
		t.Fatalf("repeated RenderManifestWrite = changed %t, %#v, %v", repeatedChanged, repeated, err)
	}
}

func TestRenderManifestWriteOmitsAlreadyDeclaredCapability(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	manifest := "id: acme.app.account\r\nprovides: [account.register/v1]\r\n"
	writePlugin(t, root, "account", manifest)
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register/v1"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	write, changed, err := capabilitycreate.RenderManifestWrite(plan)
	if err != nil || changed || write.Path != "" || write.Data != nil || write.ExpectedData != nil {
		t.Fatalf("RenderManifestWrite(existing) = changed %t, %#v, %v", changed, write, err)
	}
	current, err := os.ReadFile(filepath.Join(root, "account", "plugin.yaml"))
	if err != nil || string(current) != manifest {
		t.Fatalf("manifest changed = %q, %v", current, err)
	}
}

func TestRenderManifestWriteRejectsEmptyPlan(t *testing.T) {
	t.Parallel()

	write, changed, err := capabilitycreate.RenderManifestWrite(capabilitycreate.Plan{})
	if !errors.Is(err, capabilitycreate.ErrRenderManifest) || changed || write.Path != "" || write.Data != nil || write.ExpectedData != nil {
		t.Fatalf("RenderManifestWrite(empty) = changed %t, %#v, %v", changed, write, err)
	}
}
