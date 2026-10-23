package privatefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// MkdirTemp creates an owner-private directory before any private material is
// written. Children inherit owner-only access on Windows.
func MkdirTemp(parent, prefix string) (string, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name, err := MkdirTempRoot(root, prefix)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

// MkdirTempRoot allocates through a held parent, returning a relative name.
func MkdirTempRoot(root *os.Root, prefix string) (string, error) {
	if root == nil {
		return "", ErrPrivate
	}
	if prefix == "" || filepath.Base(prefix) != prefix || prefix == "." || prefix == ".." {
		return "", ErrPrivate
	}
	for range 100 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(random[:])
		if err := mkdirRoot(root, name); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", err
		}
		return name, nil
	}
	return "", ErrPrivate
}
