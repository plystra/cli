package privatefile

import (
	"os"
	"path/filepath"
)

// CreateRoot confines creation to an opened directory and establishes private
// permissions on the opened file before the caller can write its contents.
func CreateRoot(root *os.Root, name string) (*os.File, error) {
	if root == nil || !filepath.IsLocal(name) {
		return nil, ErrPrivate
	}
	file, err := createRoot(root, name)
	if err != nil {
		return nil, err
	}
	if err := Check(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
