package privatefile_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/privatefile"
)

func TestRejectsACLAccessDespitePrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	file, err := privatefile.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if output, err := exec.Command("chmod", "+a", "everyone allow read", path).CombinedOutput(); err != nil {
		t.Fatalf("set ACL: %v: %s", err, output)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(privatefile.Check(file), privatefile.ErrPrivate) {
		t.Fatal("accepted ACL access beyond private mode")
	}
}

func TestRejectsInheritedACLBeforePrivateWrite(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("chmod", "+a", "everyone allow read,file_inherit,directory_inherit", root).CombinedOutput(); err != nil {
		t.Fatalf("set inherited ACL: %v: %s", err, output)
	}
	path := filepath.Join(root, "private")
	file, err := privatefile.Create(path)
	if file != nil {
		file.Close()
	}
	if !errors.Is(err, privatefile.ErrPrivate) {
		t.Fatalf("inherited ACL accepted: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatal("rejected creation wrote private data")
	}
}
