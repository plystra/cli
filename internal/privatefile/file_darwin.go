package privatefile

import (
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func aclPermissions(file *os.File) (string, error) {
	// Darwin ACLs can grant access independently of mode 0600. Request the
	// opened handle's extended security and accept only an absent ACL.
	// XNU returns a uint32 length and one attrreference (offset, byte count).
	attributes := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var result [12]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&result[0])), uintptr(len(result)), 0, 0)
	runtime.KeepAlive(file)
	runtime.KeepAlive(attributes)
	if errno != 0 || binary.NativeEndian.Uint32(result[:4]) != uint32(len(result)) || binary.NativeEndian.Uint32(result[8:]) != 0 {
		return "", ErrPrivate
	}
	return "", nil
}
