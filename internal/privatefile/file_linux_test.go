package privatefile_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/privatefile"
	"golang.org/x/sys/unix"
)

func TestTransactionPreservesMaskedACLChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "private", Data: []byte("private-value"), OwnerPrivate: true}}, func(string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		// Linux POSIX ACL xattr version 2. The zero group mask blocks this
		// named user's read grant, leaving the file private with mode 0600.
		data := binary.LittleEndian.AppendUint32(nil, 2)
		for _, entry := range []struct {
			tag, permissions uint16
			id               uint32
		}{{1, 6, ^uint32(0)}, {2, 4, uint32(os.Geteuid() + 1)}, {4, 0, ^uint32(0)}, {16, 0, ^uint32(0)}, {32, 0, ^uint32(0)}} {
			data = binary.LittleEndian.AppendUint16(data, entry.tag)
			data = binary.LittleEndian.AppendUint16(data, entry.permissions)
			data = binary.LittleEndian.AppendUint32(data, entry.id)
		}
		if err := unix.Fsetxattr(int(file.Fd()), "system.posix_acl_access", data, 0); err != nil {
			return err
		}
		return privatefile.Check(file)
	})
	if !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("masked ACL edit not detected: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "private-value" {
		t.Fatal("rollback removed edited file")
	}
}
