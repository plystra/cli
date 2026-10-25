package projectstage_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/sys/unix"
)

func changeNativeAccess(t *testing.T, path string) {
	t.Helper()
	changeLinuxACL(t, path, "system.posix_acl_access")
}

func changeLinuxACL(t *testing.T, path, name string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var before [65536]byte
	n, err := unix.Fgetxattr(int(file.Fd()), name, before[:])
	absent := errors.Is(err, unix.ENODATA)
	if err != nil && !absent {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if absent {
			err = unix.Removexattr(path, name)
		} else {
			err = unix.Setxattr(path, name, before[:n], 0)
		}
		if err != nil {
			t.Error(err)
		}
	})
	data := binary.LittleEndian.AppendUint32(nil, 2)
	mode := info.Mode().Perm()
	for _, entry := range []struct {
		tag, permissions uint16
		id               uint32
	}{{1, uint16(mode >> 6), ^uint32(0)}, {2, 4, uint32(os.Geteuid() + 1)}, {4, uint16(mode >> 3 & 7), ^uint32(0)}, {16, uint16(mode >> 3 & 7), ^uint32(0)}, {32, uint16(mode & 7), ^uint32(0)}} {
		data = binary.LittleEndian.AppendUint16(data, entry.tag)
		data = binary.LittleEndian.AppendUint16(data, entry.permissions)
		data = binary.LittleEndian.AppendUint32(data, entry.id)
	}
	if err := unix.Fsetxattr(int(file.Fd()), name, data, 0); err != nil {
		t.Fatal(err)
	}
}

func TestInputsRejectNativeDefaultACLDrift(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent)
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	changeLinuxACL(t, root, "system.posix_acl_default")
	after, err := os.Stat(root)
	if err != nil || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("default ACL fixture changed ordinary directory metadata")
	}
	if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("default ACL drift accepted: %v", err)
	}
}

func TestInputsRejectNativeOwnershipDrift(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing an input owner requires root")
	}
	for _, kind := range []string{"owner", "group"} {
		t.Run(kind, func(t *testing.T) {
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "app.go")
			t.Cleanup(func() { _ = os.Chown(path, 0, os.Getegid()) })
			uid, gid := -1, -1
			if kind == "owner" {
				uid = 60001
			} else {
				gid = 60001
			}
			if err := os.Chown(path, uid, gid); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s drift accepted: %v", kind, err)
			}
		})
	}
}

func TestStageRejectsReplacedTemporaryParent(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off")
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatal(err)
	}
	write(t, stage.Root(), "replacement", "USER_OWNED_REPLACEMENT")
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("held temporary parent concealed path replacement: %v", err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(stage.Root(), "replacement")); err != nil || string(data) != "USER_OWNED_REPLACEMENT" {
		t.Fatal("cleanup modified the replaced temporary parent")
	}
}
