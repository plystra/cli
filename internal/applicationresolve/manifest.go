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

type manifestSnapshot struct {
	root fs.FileInfo
	file fs.FileInfo
	data []byte
}

func loadManifest(moduleRoot string) (manifestSnapshot, applicationmeta.Manifest, error) {
	snapshot, err := readManifestSnapshot(moduleRoot)
	if err != nil {
		return manifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	manifest, err := applicationmeta.Parse(snapshot.data)
	if err != nil {
		return manifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	return snapshot, manifest, nil
}

func readManifestSnapshot(moduleRoot string) (result manifestSnapshot, snapshotErr error) {
	if moduleRoot == "" {
		return manifestSnapshot{}, fmt.Errorf("%w: module root is empty", ErrUnsafeManifest)
	}
	absolute, err := filepath.Abs(moduleRoot)
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("resolve module root: %w", err)
	}
	rootBefore, err := os.Lstat(absolute)
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("inspect module root: %w", err)
	}
	if !rootBefore.IsDir() || rootBefore.Mode()&fs.ModeSymlink != 0 {
		return manifestSnapshot{}, fmt.Errorf("%w: module root is not a regular non-symbolic directory", ErrUnsafeManifest)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("open module root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			snapshotErr = errors.Join(snapshotErr, fmt.Errorf("close module root: %w", err))
		}
	}()
	openedRoot, err := root.Lstat(".")
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("inspect opened module root: %w", err)
	}
	if !sameDirectory(rootBefore, openedRoot) {
		return manifestSnapshot{}, fmt.Errorf("%w: %w: module root was replaced before open", ErrUnsafeManifest, ErrConcurrentChange)
	}

	before, err := root.Lstat(applicationManifestName)
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("inspect %s: %w", applicationManifestName, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return manifestSnapshot{}, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrUnsafeManifest, applicationManifestName)
	}
	if before.Size() > applicationmeta.MaximumSize {
		return manifestSnapshot{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeManifest, applicationManifestName, applicationmeta.MaximumSize)
	}
	file, err := root.Open(applicationManifestName)
	if err != nil {
		return manifestSnapshot{}, fmt.Errorf("open %s: %w", applicationManifestName, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return manifestSnapshot{}, fmt.Errorf("inspect opened %s: %w", applicationManifestName, err)
	}
	if !opened.Mode().IsRegular() || !sameFile(before, opened) {
		_ = file.Close()
		return manifestSnapshot{}, fmt.Errorf("%w: %s was replaced before open", ErrConcurrentChange, applicationManifestName)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, applicationmeta.MaximumSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return manifestSnapshot{}, fmt.Errorf("read %s: %w", applicationManifestName, readErr)
	}
	if closeErr != nil {
		return manifestSnapshot{}, fmt.Errorf("close %s: %w", applicationManifestName, closeErr)
	}
	rootAfter, rootErr := root.Lstat(".")
	after, fileErr := root.Lstat(applicationManifestName)
	if rootErr != nil || fileErr != nil || !sameDirectory(openedRoot, rootAfter) || !after.Mode().IsRegular() || after.Mode()&fs.ModeSymlink != 0 || !sameFile(opened, after) {
		return manifestSnapshot{}, fmt.Errorf("%w: %s or its module root changed while it was read", ErrConcurrentChange, applicationManifestName)
	}
	if len(data) > applicationmeta.MaximumSize {
		return manifestSnapshot{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrUnsafeManifest, applicationManifestName, applicationmeta.MaximumSize)
	}
	return manifestSnapshot{
		root: rootAfter,
		file: after,
		data: append([]byte(nil), data...),
	}, nil
}

func sameManifestSnapshot(left, right manifestSnapshot) bool {
	return sameDirectory(left.root, right.root) && sameFile(left.file, right.file) && bytes.Equal(left.data, right.data)
}

func sameDirectory(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() && left.Mode() == right.Mode() && os.SameFile(left, right)
}

func sameFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
