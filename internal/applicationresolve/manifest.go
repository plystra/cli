package applicationresolve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/applicationmeta"
)

const applicationManifestName = "plystra.yaml"

// ManifestSnapshot is one bounded, non-symbolic plystra.yaml read together
// with the filesystem identity needed to detect replacement during a longer
// operation.
type ManifestSnapshot struct {
	root fs.FileInfo
	file fs.FileInfo
	data []byte
}

// Data returns defensive manifest bytes suitable for an ExpectedData write
// precondition.
func (s ManifestSnapshot) Data() []byte { return append([]byte(nil), s.data...) }

func loadManifest(moduleRoot string) (ManifestSnapshot, applicationmeta.Manifest, error) {
	snapshot, err := ReadManifestSnapshot(moduleRoot)
	if err != nil {
		return ManifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	manifest, err := applicationmeta.Parse(snapshot.data)
	if err != nil {
		return ManifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	return snapshot, manifest, nil
}

// ReadManifestSnapshot safely reads the root plystra.yaml without following a
// symbolic module root or manifest and rejects replacement during the read.
func ReadManifestSnapshot(moduleRoot string) (result ManifestSnapshot, snapshotErr error) {
	if moduleRoot == "" {
		return ManifestSnapshot{}, fmt.Errorf("%w: module root is empty", ErrUnsafeManifest)
	}
	absolute, err := filepath.Abs(moduleRoot)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("resolve module root: %w", err)
	}
	rootBefore, err := os.Lstat(absolute)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect module root: %w", err)
	}
	if !rootBefore.IsDir() || rootBefore.Mode()&fs.ModeSymlink != 0 {
		return ManifestSnapshot{}, fmt.Errorf("%w: module root is not a regular non-symbolic directory", ErrUnsafeManifest)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("open module root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			snapshotErr = errors.Join(snapshotErr, fmt.Errorf("close module root: %w", err))
		}
	}()
	openedRoot, err := root.Lstat(".")
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect opened module root: %w", err)
	}
	if !sameDirectory(rootBefore, openedRoot) {
		return ManifestSnapshot{}, fmt.Errorf("%w: %w: module root was replaced before open", ErrUnsafeManifest, ErrConcurrentChange)
	}

	before, err := root.Lstat(applicationManifestName)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect %s: %w", applicationManifestName, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return ManifestSnapshot{}, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrUnsafeManifest, applicationManifestName)
	}
	if before.Size() > applicationmeta.MaximumSize {
		return ManifestSnapshot{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeManifest, applicationManifestName, applicationmeta.MaximumSize)
	}
	file, err := root.Open(applicationManifestName)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("open %s: %w", applicationManifestName, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return ManifestSnapshot{}, fmt.Errorf("inspect opened %s: %w", applicationManifestName, err)
	}
	if !opened.Mode().IsRegular() || !sameFile(before, opened) {
		_ = file.Close()
		return ManifestSnapshot{}, fmt.Errorf("%w: %s was replaced before open", ErrConcurrentChange, applicationManifestName)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, applicationmeta.MaximumSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return ManifestSnapshot{}, fmt.Errorf("read %s: %w", applicationManifestName, readErr)
	}
	if closeErr != nil {
		return ManifestSnapshot{}, fmt.Errorf("close %s: %w", applicationManifestName, closeErr)
	}
	rootAfter, rootErr := root.Lstat(".")
	after, fileErr := root.Lstat(applicationManifestName)
	if rootErr != nil || fileErr != nil || !sameDirectory(openedRoot, rootAfter) || !after.Mode().IsRegular() || after.Mode()&fs.ModeSymlink != 0 || !sameFile(opened, after) {
		return ManifestSnapshot{}, fmt.Errorf("%w: %s or its module root changed while it was read", ErrConcurrentChange, applicationManifestName)
	}
	if len(data) > applicationmeta.MaximumSize {
		return ManifestSnapshot{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeManifest, applicationManifestName, applicationmeta.MaximumSize)
	}
	return ManifestSnapshot{
		root: rootAfter,
		file: after,
		data: append([]byte(nil), data...),
	}, nil
}

func sameManifestSnapshot(left, right ManifestSnapshot) bool {
	return sameDirectory(left.root, right.root) && sameFile(left.file, right.file) && bytes.Equal(left.data, right.data)
}

func sameDirectory(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() && left.Mode() == right.Mode() && os.SameFile(left, right)
}

func sameFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
