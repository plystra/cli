package projectstage

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func nativeAccess(file *os.File, info os.FileInfo) (accessState, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return accessState{}, ErrStage
	}
	attributes := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [65536]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, file.Fd(), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0, 0)
	runtime.KeepAlive(file)
	runtime.KeepAlive(attributes)
	length := binary.NativeEndian.Uint32(buffer[:4])
	if errno != 0 || length < 12 || length > uint32(len(buffer)) {
		return accessState{}, ErrStage
	}
	offset := int64(int32(binary.NativeEndian.Uint32(buffer[4:8]))) + 4
	count := int64(binary.NativeEndian.Uint32(buffer[8:12]))
	data := binary.LittleEndian.AppendUint32(nil, stat.Uid)
	data = binary.LittleEndian.AppendUint32(data, stat.Gid)
	data = binary.LittleEndian.AppendUint32(data, stat.Flags)
	if count != 0 {
		if offset < 12 || offset+count > int64(length) {
			return accessState{}, ErrStage
		}
		data = append(data, buffer[offset:offset+count]...)
	}
	return sha256.Sum256(data), nil
}
