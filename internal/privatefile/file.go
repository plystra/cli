// Package privatefile enforces owner-private permissions for deployment inputs.
package privatefile

import (
	"crypto/sha256"
	"errors"
	"os"
)

var ErrPrivate = errors.New("file must be regular and readable only by its owner")

// Create creates a new file with private permissions before any data is written.
func Create(name string) (*os.File, error) { return create(name) }

// Check inspects the opened file, avoiding a second path lookup for permissions.
func Check(file *os.File) error {
	_, err := Snapshot(file)
	return err
}

// Snapshot returns private concurrency evidence, never public artifact identity.
func Snapshot(file *os.File) ([32]byte, error) {
	if file == nil {
		return [32]byte{}, ErrPrivate
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return [32]byte{}, ErrPrivate
	}
	state, err := permissions(file, info)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(state)), nil
}
