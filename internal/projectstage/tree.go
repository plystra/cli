package projectstage

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/plystra/cli/internal/privatefile"
	"golang.org/x/mod/module"
)

type budget struct {
	entries int
	bytes   int64
}

func privateOutput(root *os.Root, path string, mode os.FileMode) (*os.File, error) {
	file, err := privatefile.CreateRoot(root, path)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600 | mode.Perm()&0o100); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type treeSnapshot struct {
	staged      bool
	source      string
	destination string
	entries     map[string]treeEntry
	output      *os.Root
	outputPath  string
	allFiles    bool
	module      module.Version
	checksum    string
}

type treeEntry struct {
	info   os.FileInfo
	access accessState
	digest [sha256.Size]byte
}

func captureTree(ctx context.Context, source, destination string, output *os.Root, used *budget, allFiles bool) (treeSnapshot, error) {
	result := treeSnapshot{source: source, output: output, outputPath: destination, entries: map[string]treeEntry{}, allFiles: allFiles}
	if output != nil {
		result.destination = filepath.Join(output.Name(), destination)
	}
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return treeSnapshot{}, ErrStage
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return treeSnapshot{}, ErrStage
	}
	defer root.Close()
	if err := result.capture(ctx, root, ".", used); err != nil {
		return treeSnapshot{}, err
	}
	after, err := os.Lstat(source)
	if err != nil || !sameFile(info, after) || !os.SameFile(info, result.entries["."].info) {
		return treeSnapshot{}, ErrConcurrentChange
	}
	result.output = nil
	return result, nil
}

func (s *treeSnapshot) capture(ctx context.Context, root *os.Root, relative string, used *budget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	used.entries++
	if used.entries > MaximumEntries {
		return ErrStage
	}
	info, err := root.Lstat(relative)
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		return ErrStage
	}
	entry := treeEntry{info: info}
	file, err := root.Open(relative)
	if err != nil {
		return ErrStage
	}
	defer file.Close()
	entry.access, err = fileAccess(file, info)
	if err != nil {
		return err
	}
	var children []os.DirEntry
	if info.IsDir() {
		if s.output != nil {
			if err := s.output.MkdirAll(filepath.Join(s.outputPath, relative), 0o700); err != nil {
				return ErrStage
			}
		}
		children, err = boundedChildren(ctx, file, MaximumEntries-used.entries, s.allFiles)
		if err != nil {
			return ErrConcurrentChange
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	} else if info.Mode().IsRegular() {
		if info.Size() > MaximumFileBytes {
			return ErrStage
		}
		hash := sha256.New()
		var output *os.File
		var writer io.Writer = hash
		if s.output != nil {
			output, err = privateOutput(s.output, filepath.Join(s.outputPath, relative), info.Mode())
			if err != nil {
				file.Close()
				return ErrStage
			}
			writer = io.MultiWriter(hash, output)
		}
		count, copyErr := io.Copy(writer, io.LimitReader(file, min(MaximumFileBytes, MaximumTotalBytes-used.bytes)+1))
		used.bytes += count
		var closeErr error
		if output != nil {
			closeErr = errors.Join(closeErr, output.Close())
		}
		if copyErr != nil || closeErr != nil || count > MaximumFileBytes || used.bytes > MaximumTotalBytes || count != info.Size() {
			return ErrStage
		}
		copy(entry.digest[:], hash.Sum(nil))
	} else {
		return ErrStage
	}
	access, accessErr := fileAccess(file, info)
	closeErr := file.Close()
	if accessErr != nil || closeErr != nil || access != entry.access {
		return ErrConcurrentChange
	}
	after, err := root.Lstat(relative)
	if err != nil || !sameFile(info, after) {
		return ErrConcurrentChange
	}
	for _, child := range children {
		if err := s.capture(ctx, root, filepath.Join(relative, child.Name()), used); err != nil {
			return err
		}
	}
	if info.IsDir() {
		current, access, err := readAccessInfo(root, relative)
		if err != nil || !sameFile(info, current) || entry.access != access {
			return ErrConcurrentChange
		}
	}
	s.entries[relative] = entry
	return nil
}

func boundedChildren(ctx context.Context, directory *os.File, remaining int, allFiles bool) ([]os.DirEntry, error) {
	var result []os.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := directory.ReadDir(min(128, remaining-len(result)+1))
		for _, child := range batch {
			if !allFiles && (child.Name() == ".git" || child.Name() == ".hg" || child.Name() == ".svn") {
				continue
			}
			result = append(result, child)
		}
		if len(result) > remaining {
			return nil, ErrStage
		}
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return nil, ErrStage
		}
	}
}

func sameFile(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if !os.SameFile(left, right) || left.Mode() != right.Mode() {
		return false
	}
	return left.IsDir() || left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func sameTree(left, right treeSnapshot) bool {
	if len(left.entries) != len(right.entries) {
		return false
	}
	for path, before := range left.entries {
		after, found := right.entries[path]
		if !found || !sameFile(before.info, after.info) || before.access != after.access || before.digest != after.digest {
			return false
		}
	}
	return true
}
