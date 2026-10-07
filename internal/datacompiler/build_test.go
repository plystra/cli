package datacompiler_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/datacompiler"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestBuildCreatesAndReusesCompilerCache(t *testing.T) {
	root := writeCompilerModule(t)
	var observations []datacompiler.Observation
	options := datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(t, root),
		CacheRoot: filepath.Join(t.TempDir(), "cache"), Environment: []string{"GOWORK=bad"},
		Observe: func(observation datacompiler.Observation) { observations = append(observations, observation) },
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
	if got := observationIDs(observations); got != "data-compiler-build-temporary,data-compiler-build-execution,data-compiler-cache-materialization" {
		t.Fatalf("cold build observations = %q", got)
	}
	second, err := datacompiler.Build(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit || second.Path != first.Path || second.BinaryDigest != first.BinaryDigest {
		t.Fatalf("second artifact = %#v, first = %#v", second, first)
	}
	if len(observations) != 3 {
		t.Fatalf("warm cache observations = %#v", observations)
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
	if got := observationIDs(observations[3:]); got != "data-compiler-build-temporary,data-compiler-build-execution,data-compiler-cache-materialization" {
		t.Fatalf("rebuilt cache observations = %q", got)
	}
}

func TestBuildOfflineRequiresVerifiedExistingCompiler(t *testing.T) {
	root := writeCompilerModule(t)
	cache := filepath.Join(t.TempDir(), "cache")
	options := datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(t, root),
		CacheRoot: cache, GoCommand: "nonexistent-go-command", Offline: true,
	}
	_, err := datacompiler.Build(context.Background(), options)
	if !errors.Is(err, datacompiler.ErrOfflineUnavailable) {
		t.Fatalf("cold offline cache error = %v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cold offline cache created directory: %v", err)
	}
	online := options
	online.GoCommand = ""
	online.Offline = false
	artifact, err := datacompiler.Build(context.Background(), online)
	if err != nil {
		t.Fatalf("online build: %v", err)
	}
	warm, err := datacompiler.Build(context.Background(), options)
	if err != nil || !warm.CacheHit || warm.Path != artifact.Path || warm.BinaryDigest != artifact.BinaryDigest {
		t.Fatalf("warm offline cache = %#v, %v", warm, err)
	}
	file, err := os.OpenFile(artifact.Path, os.O_APPEND|os.O_WRONLY, 0)
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
	_, err = datacompiler.Build(context.Background(), options)
	if !errors.Is(err, datacompiler.ErrOfflineUnavailable) {
		t.Fatalf("corrupt offline cache error = %v", err)
	}
	data, err := os.ReadFile(artifact.Path)
	if err != nil || !strings.HasSuffix(string(data), "tampered") {
		t.Fatalf("offline cache was modified: %v", err)
	}
}

func TestBuildOfflineRejectsSymbolicCacheRecord(t *testing.T) {
	root := writeCompilerModule(t)
	options := datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(t, root),
		CacheRoot: filepath.Join(t.TempDir(), "cache"),
	}
	artifact, err := datacompiler.Build(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	record := artifact.Path + ".json"
	copy := record + ".copy"
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copy, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copy, record); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	options.Offline = true
	_, err = datacompiler.Build(context.Background(), options)
	if !errors.Is(err, datacompiler.ErrOfflineUnavailable) {
		t.Fatalf("symbolic cache record error = %v", err)
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

func TestBuildRejectsMismatchedModuleRootWithoutCacheOutput(t *testing.T) {
	root := writeCompilerModule(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/not-data\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	_, err := datacompiler.Build(context.Background(), datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(t, root), CacheRoot: cache,
	})
	if !errors.Is(err, datacompiler.ErrBuild) || !errors.Is(err, datacompiler.ErrSelection) || !errors.Is(err, datacompiler.ErrInvalidSelection) {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache exists after rejected root: %v", err)
	}
}

func TestBuildRejectsChangedSourceBeforeCacheOutput(t *testing.T) {
	root := writeCompilerModule(t)
	checksum := testModuleChecksum(t, root)
	if err := os.WriteFile(filepath.Join(root, "plystra-data-compiler.json"), []byte(validManifestJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache")
	_, err := datacompiler.Build(context.Background(), datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: checksum, CacheRoot: cache,
	})
	if !errors.Is(err, datacompiler.ErrInvalidSelection) {
		t.Fatalf("Build() error = %v", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache exists after changed source: %v", err)
	}
}

func TestBuildFailureDoesNotLeaveFinalArtifact(t *testing.T) {
	root := writeCompilerModule(t)
	if err := os.Remove(filepath.Join(root, "cmd", "plystra-data-compiler", "main.go")); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	_, err := datacompiler.Build(context.Background(), datacompiler.BuildOptions{
		ModuleRoot: root, ModuleVersion: "v0.0.0-test", ModuleChecksum: testModuleChecksum(t, root), CacheRoot: cache,
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

func testModuleChecksum(t *testing.T, root string) string {
	t.Helper()
	checksum, err := dirhash.HashDir(root, datacompiler.ModulePath+"@v0.0.0-test", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	return checksum
}

func observationIDs(values []datacompiler.Observation) string {
	ids := make([]string, len(values))
	for index, value := range values {
		ids[index] = value.ID
	}
	return strings.Join(ids, ",")
}
