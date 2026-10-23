package projectstage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureTreeBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "value"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, used := range []budget{{entries: MaximumEntries - 1}, {bytes: MaximumTotalBytes}} {
		if _, err := captureTree(t.Context(), root, "", nil, &used); err == nil {
			t.Fatal("copy budget was exceeded")
		}
	}
	for _, used := range []budget{{entries: MaximumEntries - 2}, {bytes: MaximumTotalBytes - 1}} {
		if _, err := captureTree(t.Context(), root, "", nil, &used); err != nil {
			t.Fatalf("exact boundary rejected: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := captureTree(ctx, root, "", nil, &budget{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled copy: %v", err)
	}
	large, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	err = large.Truncate(MaximumFileBytes + 1)
	closeErr := large.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("large input: %v, %v", err, closeErr)
	}
	if _, err := captureTree(t.Context(), root, "", nil, &budget{}); err == nil {
		t.Fatal("oversized input was accepted")
	}
}

func TestCaptureTreeWritesThroughHeldRoot(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "stage")
	source := filepath.Join(parent, "source")
	external := filepath.Join(parent, "external")
	for _, path := range []string{destination, source, external} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "value"), []byte("copied"), 0o600); err != nil {
		t.Fatal(err)
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRoot.Close()
	root, err := parentRoot.OpenRoot("stage")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(destination, destination+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := captureTree(t.Context(), source, "module", root, &budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(external, "module")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("copy escaped its opened destination root")
	}
	if data, err := os.ReadFile(filepath.Join(destination+"-moved", "module", "value")); err != nil || string(data) != "copied" {
		t.Fatalf("held destination: %q, %v", data, err)
	}
}
