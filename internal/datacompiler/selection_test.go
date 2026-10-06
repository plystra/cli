package datacompiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/datacompiler"
)

func TestResolveAcceptsExactVerifiedModuleSelection(t *testing.T) {
	root := writeCompilerModule(t)
	selection, err := datacompiler.Resolve(datacompiler.Source{
		ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test",
		ModuleChecksum: testModuleChecksum(), Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.ModulePath != datacompiler.ModulePath || selection.ModuleVersion != "v0.0.0-test" || selection.ModuleChecksum != testModuleChecksum() || selection.Root != root || selection.Manifest.ModulePath != datacompiler.ModulePath || selection.ManifestDigest == "" {
		t.Fatalf("selection = %#v", selection)
	}
}

func TestResolveRejectsNonImmutableOrIncompleteSelection(t *testing.T) {
	root := writeCompilerModule(t)
	tests := []datacompiler.Source{
		{ModulePath: "example.com/data", ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(), Root: root},
		{ModulePath: datacompiler.ModulePath, ModuleVersion: "", ModuleChecksum: testModuleChecksum(), Root: root},
		{ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test", Root: root},
		{ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(), Root: root, Workspace: true},
		{ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(), Root: root, Replacement: true},
		{ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(), Root: "relative"},
	}
	for _, source := range tests {
		selection, err := datacompiler.Resolve(source)
		if !errors.Is(err, datacompiler.ErrSelection) || !errors.Is(err, datacompiler.ErrInvalidSelection) || selection != (datacompiler.Selection{}) {
			t.Fatalf("Resolve(%#v) = %#v, %v", source, selection, err)
		}
	}
}

func TestResolveRejectsRootWithDifferentModuleIdentity(t *testing.T) {
	root := writeCompilerModule(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/not-data\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selection, err := datacompiler.Resolve(datacompiler.Source{
		ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test",
		ModuleChecksum: testModuleChecksum(), Root: root,
	})
	if !errors.Is(err, datacompiler.ErrSelection) || !errors.Is(err, datacompiler.ErrInvalidSelection) || selection != (datacompiler.Selection{}) {
		t.Fatalf("Resolve() = %#v, %v", selection, err)
	}
}

func TestResolveRejectsUnavailableManifestWithoutLeakingSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/plystra/data\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []datacompiler.Source{{
		ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.0.0-test",
		ModuleChecksum: testModuleChecksum(), Root: root,
	}}
	for _, source := range tests {
		selection, err := datacompiler.Resolve(source)
		if !errors.Is(err, datacompiler.ErrSelection) || !errors.Is(err, datacompiler.ErrManifestUnavailable) || selection != (datacompiler.Selection{}) {
			t.Fatalf("Resolve(%#v) = %#v, %v", source, selection, err)
		}
	}
}
