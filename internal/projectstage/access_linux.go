package projectstage

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func nativeAccess(file *os.File, info os.FileInfo) (accessState, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return accessState{}, ErrStage
	}
	hash := sha256.New()
	_, _ = hash.Write(binary.LittleEndian.AppendUint32(binary.LittleEndian.AppendUint32(nil, stat.Uid), stat.Gid))
	var buffer [65536]byte
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		n, err := unix.Fgetxattr(int(file.Fd()), name, buffer[:])
		if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EOPNOTSUPP) {
			n = 0
		} else if err != nil {
			return accessState{}, ErrStage
		}
		_, _ = hash.Write(binary.LittleEndian.AppendUint32(nil, uint32(n)))
		_, _ = hash.Write(buffer[:n])
	}
	var state accessState
	copy(state[:], hash.Sum(nil))
	return state, nil
}
