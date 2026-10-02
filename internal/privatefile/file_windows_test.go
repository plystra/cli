package privatefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/privatefile"
	"golang.org/x/sys/windows"
)

func makePublic(t *testing.T, path string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionPreservesPrivateACLChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "private", Data: []byte("private-value"), OwnerPrivate: true}}, func(string) error {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FR;;;" + user.User.Sid.String() + ")")
		if err != nil {
			return err
		}
		dacl, _, err := descriptor.DACL()
		if err != nil {
			return err
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		return privatefile.Check(file)
	})
	if !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("private ACL edit not detected: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "private-value" {
		t.Fatal("rollback removed edited file")
	}
}
