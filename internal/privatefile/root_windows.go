package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createRoot(root *os.Root, name string) (*os.File, error) {
	return createRootKind(root, name, false)
}

func createRootKind(root *os.Root, name string, directoryKind bool) (*os.File, error) {
	directory, err := root.Open(filepath.Dir(name))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, ErrPrivate
	}
	sid := user.User.Sid.String()
	inherit := ""
	options := uint32(windows.FILE_NON_DIRECTORY_FILE | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	if directoryKind {
		inherit = "OICI"
		options = windows.FILE_DIRECTORY_FILE | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + inherit + ";FA;;;" + sid + ")")
	if err != nil {
		return nil, ErrPrivate
	}
	objectName, err := windows.NewNTUnicodeString(filepath.Base(name))
	if err != nil {
		return nil, ErrPrivate
	}
	attributes := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(directory.Fd()), ObjectName: objectName, SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.SYNCHRONIZE,
		&attributes, &windows.IO_STATUS_BLOCK{}, nil, windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_CREATE,
		options, 0, 0)
	runtime.KeepAlive(descriptor)
	if err != nil {
		var status windows.NTStatus
		if errors.As(err, &status) {
			err = status.Errno()
		}
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}
