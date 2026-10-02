package privatefile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func aclPermissions(file *os.File) (string, error) {
	// POSIX ACL grants are bounded by the already-checked group mode mask.
	// Retain the ACL itself to detect even masked concurrent permission edits.
	data := make([]byte, 65536)
	n, err := unix.Fgetxattr(int(file.Fd()), "system.posix_acl_access", data)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
		return "", nil
	}
	if err != nil {
		return "", ErrPrivate
	}
	return string(data[:n]), nil
}
