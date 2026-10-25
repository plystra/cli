package projectstage

import (
	"crypto/sha256"
	"encoding/binary"
	"os"

	"golang.org/x/sys/windows"
)

func nativeAccess(file *os.File, _ os.FileInfo) (accessState, error) {
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)
	if err != nil || descriptor == nil || !descriptor.IsValid() || descriptor.Length() > 256<<10 {
		return accessState{}, ErrStage
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return accessState{}, ErrStage
	}
	state := descriptor.String()
	if state == "" {
		return accessState{}, ErrStage
	}
	data := binary.LittleEndian.AppendUint16(nil, uint16(control))
	return sha256.Sum256(append(data, state...)), nil
}
