package privatefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirTempIsPrivateBeforeWriting(t *testing.T) {
	parent := t.TempDir()
	first, err := MkdirTemp(parent, "stage-")
	if err != nil {
		t.Fatal(err)
	}
	second, err := MkdirTemp(parent, "stage-")
	if err != nil || first == second || filepath.Dir(first) != parent {
		t.Fatalf("independent allocation: %v", err)
	}
	directory, err := os.Open(first)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		t.Fatalf("not a directory: %v", err)
	}
	if _, err := permissions(directory, info); err != nil {
		t.Fatalf("staging directory is not private: %v", err)
	}
	for _, prefix := range []string{"", ".", "..", "../escape", "child/stage"} {
		if path, err := MkdirTemp(parent, prefix); err == nil {
			t.Fatalf("unsafe prefix %q accepted at %q", prefix, path)
		}
	}
}

func TestCreateRootIsPrivateAndConfined(t *testing.T) {
	parent := t.TempDir()
	directory, err := MkdirTemp(parent, "private-")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := CreateRoot(root, "value")
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(file); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"value", "../escape", filepath.Join(parent, "absolute")} {
		if file, err := CreateRoot(root, name); err == nil {
			file.Close()
			t.Fatalf("unsafe creation accepted: %s", name)
		}
	}
}
