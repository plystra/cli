package projectstage_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestRetainedMetadataRejectsDriftAfterCleanup(t *testing.T) {
	for _, change := range []string{"bytes", "identity", "mode", "removed", "directory"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			metadata := filepath.Join(t.TempDir(), "cache")
			write(t, metadata, "v1.0.0.mod", "module example.com/dependency\n")
			write(t, metadata, "v1.0.0.ziphash", "private checksum reference")
			stage := capturedStage(t, root, parent)
			earlier := stage.Inputs()
			path := filepath.Join(metadata, "v1.0.0.mod")
			paths := []projectstage.MetadataInput{{Path: path}, {Path: filepath.Join(metadata, "v1.0.0.ziphash")}, {Path: path}}
			if err := stage.RetainMetadata(t.Context(), paths); err != nil {
				t.Fatal(err)
			}
			if err := stage.RetainMetadata(t.Context(), paths); err != nil {
				t.Fatal(err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			write(t, metadata, "v2.0.0.mod", "unselected cache metadata")
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatalf("unrelated cache materialization invalidated selected files: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bytes":
				write(t, metadata, "v1.0.0.mod", "module example.com/alteration\n")
			case "identity", "removed":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				if change == "identity" {
					write(t, metadata, "v1.0.0.mod", "module example.com/dependency\n")
				}
			case "mode":
				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			case "directory":
				if err := os.Rename(metadata, metadata+".old"); err != nil {
					t.Fatal(err)
				}
				write(t, metadata, "v1.0.0.mod", "module example.com/dependency\n")
				write(t, metadata, "v1.0.0.ziphash", "private checksum reference")
			}
			if change == "bytes" || change == "identity" {
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("retained metadata %s drift accepted: %v", change, err)
			}
			if err := earlier.Recheck(t.Context()); err != nil {
				t.Fatal("new metadata changed an earlier immutable input snapshot")
			}
			if _, err := inputs.ProjectFile(t.Context(), path); err == nil {
				t.Fatal("external metadata became readable as an authored Project file")
			}
		})
	}
}

func TestRetainMetadataKeepsEarlierVersionsAndRejectsRefresh(t *testing.T) {
	root, parent := fixture(t)
	metadata := t.TempDir()
	stage := capturedStage(t, root, parent)
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		write(t, metadata, version+".mod", "module example.com/dependency\n")
		if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{{Path: filepath.Join(metadata, version+".mod")}}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, metadata, "v1.0.0.mod", "module example.com/alteration\n")
	if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{{Path: filepath.Join(metadata, "v1.0.0.mod")}}); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("earlier metadata was refreshed: %v", err)
	}
}

func TestRetainMetadataRejectsUnsafeInputs(t *testing.T) {
	for _, kind := range []string{"relative", "missing", "directory", "oversized", "candidate", "too-many"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			metadata := t.TempDir()
			path := filepath.Join(metadata, "v1.0.0.mod")
			paths := []projectstage.MetadataInput{{Path: path}}
			switch kind {
			case "relative":
				paths[0].Path = "relative.mod"
			case "directory":
				paths[0].Path = metadata
			case "oversized":
				write(t, metadata, "v1.0.0.mod", strings.Repeat("x", (1<<20)+1))
			case "candidate":
				paths[0].Path = filepath.Join(stage.Root(), "go.mod")
			case "too-many":
				paths = make([]projectstage.MetadataInput, projectstage.MaximumEntries+1)
			}
			if err := stage.RetainMetadata(t.Context(), paths); err == nil {
				t.Fatalf("accepted %s metadata", kind)
			}
		})
	}
}

func TestRetainMetadataValidatesExpectedCapturedBytes(t *testing.T) {
	for _, mode := range []string{"first", "repeated"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			metadata := t.TempDir()
			write(t, metadata, "v1.0.0.ziphash", "expected checksum")
			stage := capturedStage(t, root, parent)
			input := projectstage.MetadataInput{Path: filepath.Join(metadata, "v1.0.0.ziphash"), Expected: []byte("expected checksum")}
			if mode == "repeated" {
				if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{input}); err != nil {
					t.Fatal(err)
				}
			}
			input.Expected = []byte("conflicting checksum")
			if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{input}); err == nil {
				t.Fatal("conflicting expected metadata accepted")
			}
		})
	}
}

func TestRetainMetadataValidatesGoModChecksum(t *testing.T) {
	for _, mode := range []string{"valid", "changed", "wrong-name", "malformed", "repeated-conflict", "expected-conflict"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			metadata := t.TempDir()
			data := []byte("module example.com/dependency\n")
			write(t, metadata, "v1.0.0.mod", string(data))
			stage := capturedStage(t, root, parent)
			name := "go.mod"
			if mode == "wrong-name" {
				name = "v1.0.0.mod"
			}
			checksum, err := dirhash.Hash1([]string{name}, func(string) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(data)), nil
			})
			if err != nil {
				t.Fatal(err)
			}
			input := projectstage.MetadataInput{Path: filepath.Join(metadata, "v1.0.0.mod"), GoModChecksum: checksum}
			switch mode {
			case "changed":
				write(t, metadata, "v1.0.0.mod", string(data)+"// changed before capture\n")
			case "malformed":
				input.GoModChecksum = "h1:invalid"
			case "repeated-conflict":
				if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{input}); err != nil {
					t.Fatal(err)
				}
				input.GoModChecksum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			case "expected-conflict":
				input.Expected = []byte("conflicting expected bytes")
			}
			err = stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{input})
			if mode != "valid" {
				if !errors.Is(err, projectstage.ErrStage) {
					t.Fatalf("invalid %s manifest checksum accepted: %v", mode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{input}); err != nil {
				t.Fatal(err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatalf("checksum-validated metadata was not retained: %v", err)
			}
		})
	}
}
