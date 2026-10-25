package projectstage

import (
	"crypto/sha256"
	"os"
)

// Native permission evidence stays private and fixed-size, independently of
// the number or identities of principals in an input's access-control list.
type accessState [sha256.Size]byte

func fileAccess(file *os.File, expected os.FileInfo) (accessState, error) {
	opened, err := file.Stat()
	if err != nil || !sameFile(expected, opened) {
		return accessState{}, ErrConcurrentChange
	}
	return nativeAccess(file, opened)
}

func readAccessInfo(root *os.Root, path string) (os.FileInfo, accessState, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		return nil, accessState{}, ErrStage
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, accessState{}, ErrStage
	}
	defer file.Close()
	before, err := fileAccess(file, info)
	if err != nil {
		return nil, accessState{}, err
	}
	after, accessErr := fileAccess(file, info)
	current, statErr := root.Lstat(path)
	if accessErr != nil || statErr != nil || before != after || !sameFile(info, current) {
		return nil, accessState{}, ErrConcurrentChange
	}
	return info, before, nil
}

func directoryAccess(path string) (os.FileInfo, accessState, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() {
		return nil, accessState{}, ErrStage
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, accessState{}, ErrStage
	}
	defer root.Close()
	info, access, err := readAccessInfo(root, ".")
	after, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !sameFile(before, info) || !sameFile(info, after) {
		return nil, accessState{}, ErrConcurrentChange
	}
	return info, access, nil
}
