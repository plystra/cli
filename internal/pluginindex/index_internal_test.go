package pluginindex

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/pluginscan"
)

func TestStableIndexComparisonsDetectFileAndInventoryChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	account := filepath.Join(root, "account")
	if err := os.Mkdir(account, 0o755); err != nil {
		t.Fatalf("Mkdir(account): %v", err)
	}
	marker := filepath.Join(account, "plugin.yaml")
	if err := os.WriteFile(marker, []byte("id: acme.app.account\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(account): %v", err)
	}
	beforeFile, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("Stat(before): %v", err)
	}
	beforeDirectories, err := pluginscan.ScanRoot(root)
	if err != nil {
		t.Fatalf("ScanRoot(before): %v", err)
	}
	if !sameFileState(beforeFile, beforeFile) {
		t.Fatal("sameFileState rejected identical metadata")
	}

	replacement := filepath.Join(account, "replacement.yaml")
	if err := os.WriteFile(replacement, []byte("id: acme.app.profile\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(replacement): %v", err)
	}
	afterFile, err := os.Stat(replacement)
	if err != nil {
		t.Fatalf("Stat(after): %v", err)
	}
	if sameFileState(beforeFile, afterFile) {
		t.Fatal("sameFileState accepted different file identity")
	}

	profile := filepath.Join(root, "profile")
	if err := os.Mkdir(profile, 0o755); err != nil {
		t.Fatalf("Mkdir(profile): %v", err)
	}
	if err := os.WriteFile(filepath.Join(profile, "plugin.yaml"), []byte("id: acme.app.profile\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(profile): %v", err)
	}
	afterDirectories, err := pluginscan.ScanRoot(root)
	if err != nil {
		t.Fatalf("ScanRoot(after): %v", err)
	}
	if sameDirectories(beforeDirectories.Directories(), afterDirectories.Directories()) {
		t.Fatal("sameDirectories accepted an added plugin")
	}
	if !sameDirectories(afterDirectories.Directories(), afterDirectories.Directories()) {
		t.Fatal("sameDirectories rejected an identical inventory")
	}
}

func TestInspectGenerationPackageRejectsSymbolicComponents(t *testing.T) {
	t.Parallel()

	generation := mustGeneration(t, "./generation/nested")
	source := fstest.MapFS{
		"account":            {Mode: fs.ModeDir},
		"account/generation": {Mode: fs.ModeSymlink, Data: []byte("outside")},
	}
	snapshot, err := inspectGenerationPackage(source, "acme.app.account", "account", generation)
	if !errors.Is(err, ErrInvalidGenerationPackage) {
		t.Fatalf("inspectGenerationPackage error = %v, want ErrInvalidGenerationPackage", err)
	}
	if snapshot.modulePath != "" || len(snapshot.components) != 0 {
		t.Fatalf("invalid inspectGenerationPackage returned %#v", snapshot)
	}
}

func TestGenerationPackageSnapshotsComparePathAndIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	account := filepath.Join(root, "account")
	generationPath := filepath.Join(account, "generation")
	if err := os.MkdirAll(generationPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(generation): %v", err)
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer rootHandle.Close()
	generation := mustGeneration(t, "./generation")
	before, err := inspectGenerationPackage(rootHandle, "acme.app.account", "account", generation)
	if err != nil {
		t.Fatalf("inspectGenerationPackage(before): %v", err)
	}
	after, err := inspectGenerationPackage(rootHandle, "acme.app.account", "account", generation)
	if err != nil || !sameGenerationPackage(before, after) {
		t.Fatalf("sameGenerationPackage(identical) = false, error %v", err)
	}
	if err := os.Remove(generationPath); err != nil {
		t.Fatalf("Remove(generation): %v", err)
	}
	if err := os.Mkdir(generationPath, 0o755); err != nil {
		t.Fatalf("Mkdir(replacement): %v", err)
	}
	replacement, err := inspectGenerationPackage(rootHandle, "acme.app.account", "account", generation)
	if err != nil {
		t.Fatalf("inspectGenerationPackage(replacement): %v", err)
	}
	if sameGenerationPackage(before, replacement) {
		t.Fatal("sameGenerationPackage accepted a replaced directory")
	}
}

func mustGeneration(t *testing.T, packagePath string) pluginmeta.Generation {
	t.Helper()
	manifest, err := pluginmeta.Parse([]byte("id: acme.app.account\nprovides: [authn.session.verify/v1]\ngeneration: {api: v1, package: " + packagePath + ", activations: [{namespace: authn, capability: authn.session.verify/v1}]}\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	generation, ok := manifest.Generation()
	if !ok {
		t.Fatal("Parse returned no generation declaration")
	}
	return generation
}
