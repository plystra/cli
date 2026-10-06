package datacompiler_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/datacompiler"
)

func TestBuildCreatesAndReusesCompilerCache(t *testing.T) {
	root := writeCompilerModule(t)
	options := datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(),
		CacheRoot: filepath.Join(t.TempDir(), "cache"), Environment: []string{"GOWORK=bad"},
	}
	first, err := datacompiler.Build(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit || first.Path == "" || first.BinaryDigest == "" || first.ManifestDigest == "" {
		t.Fatalf("first artifact = %#v", first)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("built compiler = %v", err)
	}
	second, err := datacompiler.Build(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit || second.Path != first.Path || second.BinaryDigest != first.BinaryDigest {
		t.Fatalf("second artifact = %#v, first = %#v", second, first)
	}
	file, err := os.OpenFile(first.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := datacompiler.Build(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if third.CacheHit || third.Path != first.Path || third.BinaryDigest != first.BinaryDigest {
		t.Fatalf("tampered cache was not rebuilt: %#v, first = %#v", third, first)
	}
}

func TestBuildRejectsIncompleteIdentityWithoutCacheOutput(t *testing.T) {
	root := writeCompilerModule(t)
	cache := filepath.Join(t.TempDir(), "cache")
	_, err := datacompiler.Build(context.Background(), datacompiler.BuildOptions{ModuleRoot: root, ModuleVersion: "v0.0.0-test", CacheRoot: cache})
	if !errors.Is(err, datacompiler.ErrBuild) || !errors.Is(err, datacompiler.ErrInvalidBuildOptions) {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache exists after rejected options: %v", err)
	}
}

func TestBuildFailureDoesNotLeaveFinalArtifact(t *testing.T) {
	root := writeCompilerModule(t)
	if err := os.Remove(filepath.Join(root, "cmd", "plystra-data-compiler", "main.go")); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	_, err := datacompiler.Build(context.Background(), datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(), CacheRoot: cache,
	})
	if !errors.Is(err, datacompiler.ErrBuild) {
		t.Fatalf("Build() error = %v", err)
	}
	entries, readErr := os.ReadDir(cache)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "data-compiler-") {
			t.Fatalf("compiler cache entry after failed build = %v", entry.Name())
		}
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("read cache after failed build: %v", readErr)
	}
}

func writeCompilerModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "plystra-data-compiler"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/plystra/data\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plystra-data-compiler.json"), []byte(validManifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "plystra-data-compiler", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func testModuleChecksum() string {
	return "h1:" + base64.StdEncoding.EncodeToString(make([]byte, 32))
}
