package atomicfs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrWriteFiles reports a failed staged multi-file transaction.
	ErrWriteFiles = errors.New("write files transaction")
	// ErrUnsafePath reports a path that is invalid or traverses a symbolic link.
	ErrUnsafePath = errors.New("unsafe transaction path")
	// ErrConcurrentChange reports a target changed after transaction planning.
	ErrConcurrentChange = errors.New("transaction target changed concurrently")
)

// Write describes one complete file replacement.
type Write struct {
	Path               string
	Data               []byte
	Mode               fs.FileMode
	MustNotExist       bool
	ParentMustNotExist bool
	// ExpectedData, when non-nil, requires an existing target with these exact
	// bytes before any transaction staging begins.
	ExpectedData []byte
}

type plannedWrite struct {
	path           string
	osPath         string
	data           []byte
	mode           fs.FileMode
	mustNotExist   bool
	existed        bool
	original       []byte
	originalMode   fs.FileMode
	expected       []byte
	expectOriginal bool
	stagePath      string
	backupPath     string
	missingParents []string
}

type appliedWrite struct {
	path       string
	backupPath string
	existed    bool
	installed  bool
	data       []byte
	mode       fs.FileMode
}

// WriteFiles stages replacements, applies them in canonical path order, runs
// validation against the updated root, and restores every target on failure.
func WriteFiles(rootPath string, writes []Write, validate func(root string) error) (operationErr error) {
	if validate == nil {
		return fmt.Errorf("%w: validation callback is nil", ErrWriteFiles)
	}
	absoluteRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("%w: resolve root: %w", ErrWriteFiles, err)
	}
	rootInfo, err := os.Stat(absoluteRoot)
	if err != nil {
		return fmt.Errorf("%w: inspect root: %w", ErrWriteFiles, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("%w: root is not a directory", ErrWriteFiles)
	}
	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return fmt.Errorf("%w: open root: %w", ErrWriteFiles, err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			operationErr = errors.Join(operationErr, fmt.Errorf("%w: close root: %w", ErrWriteFiles, err))
		}
	}()

	planned, err := planWrites(root, writes)
	if err != nil {
		return err
	}
	transactionRoot, err := os.MkdirTemp(absoluteRoot, ".plystra-files-")
	if err != nil {
		return fmt.Errorf("%w: create staging directory: %w", ErrWriteFiles, err)
	}
	transactionName := filepath.Base(transactionRoot)
	committed := false
	var applied []appliedWrite
	var createdDirectories []string
	defer func() {
		rollbackSucceeded := true
		if !committed {
			if err := rollbackWrites(root, applied, createdDirectories); err != nil {
				rollbackSucceeded = false
				operationErr = errors.Join(operationErr, fmt.Errorf("%w: recovery data retained in %s", err, transactionName))
			}
		}
		if committed || rollbackSucceeded {
			if err := os.RemoveAll(transactionRoot); err != nil {
				operationErr = errors.Join(operationErr, fmt.Errorf("%w: clean staging directory: %w", ErrWriteFiles, err))
			}
		}
	}()

	if err := stageWrites(transactionRoot, transactionName, planned); err != nil {
		return err
	}
	createdDirectories, err = createParentDirectories(root, planned)
	if err != nil {
		return err
	}
	for index := range planned {
		write := &planned[index]
		if err := confirmUnchanged(root, *write); err != nil {
			return err
		}
		state := appliedWrite{
			path:       write.osPath,
			backupPath: write.backupPath,
			existed:    write.existed,
			data:       write.data,
			mode:       write.mode,
		}
		if write.existed {
			if err := root.Rename(write.osPath, write.backupPath); err != nil {
				return fmt.Errorf("%w: back up %s: %w", ErrWriteFiles, write.path, err)
			}
		}
		applied = append(applied, state)
		if err := root.Rename(write.stagePath, write.osPath); err != nil {
			return fmt.Errorf("%w: install %s: %w", ErrWriteFiles, write.path, err)
		}
		applied[len(applied)-1].installed = true
		info, err := root.Lstat(write.osPath)
		if err != nil {
			return fmt.Errorf("%w: inspect installed %s: %w", ErrWriteFiles, write.path, err)
		}
		applied[len(applied)-1].mode = info.Mode().Perm()
	}
	if err := validate(absoluteRoot); err != nil {
		return fmt.Errorf("%w: validate updated root: %w", ErrWriteFiles, err)
	}
	committed = true
	return nil
}

func planWrites(root *os.Root, writes []Write) ([]plannedWrite, error) {
	planned := make([]plannedWrite, 0, len(writes))
	seen := make(map[string]struct{}, len(writes))
	for _, write := range writes {
		if !fs.ValidPath(write.Path) || write.Path == "." || strings.ContainsRune(write.Path, '\\') {
			return nil, fmt.Errorf("%w: %q", ErrUnsafePath, write.Path)
		}
		canonical := path.Clean(write.Path)
		if canonical != write.Path {
			return nil, fmt.Errorf("%w: %q is not canonical", ErrUnsafePath, write.Path)
		}
		if write.ExpectedData != nil && (write.MustNotExist || write.ParentMustNotExist) {
			return nil, fmt.Errorf("%w: %s has conflicting existence preconditions", ErrWriteFiles, canonical)
		}
		if _, duplicate := seen[canonical]; duplicate {
			return nil, fmt.Errorf("%w: duplicate write %q", ErrWriteFiles, canonical)
		}
		seen[canonical] = struct{}{}
		mode := write.Mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
		item := plannedWrite{
			path:           canonical,
			osPath:         filepath.FromSlash(canonical),
			data:           append([]byte(nil), write.Data...),
			mode:           mode,
			mustNotExist:   write.MustNotExist,
			expectOriginal: write.ExpectedData != nil,
		}
		if item.expectOriginal {
			item.expected = append(make([]byte, 0, len(write.ExpectedData)), write.ExpectedData...)
		}
		missingParents, err := inspectParents(root, item.osPath)
		if err != nil {
			return nil, err
		}
		item.missingParents = missingParents
		if write.ParentMustNotExist {
			parent := filepath.Dir(item.osPath)
			if parent == "." {
				return nil, fmt.Errorf("%w: %s has no child parent", ErrUnsafePath, canonical)
			}
			if _, err := root.Lstat(parent); err == nil {
				return nil, fmt.Errorf("%w: parent %s", ErrTargetExists, filepath.ToSlash(parent))
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: inspect parent %s: %w", ErrWriteFiles, filepath.ToSlash(parent), err)
			}
		}
		info, err := root.Lstat(item.osPath)
		switch {
		case err == nil:
			if write.MustNotExist {
				return nil, fmt.Errorf("%w: %s", ErrTargetExists, canonical)
			}
			if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("%w: %s is not a regular file", ErrUnsafePath, canonical)
			}
			item.existed = true
			item.originalMode = info.Mode().Perm()
			item.original, err = root.ReadFile(item.osPath)
			if err != nil {
				return nil, fmt.Errorf("%w: read %s: %w", ErrWriteFiles, canonical, err)
			}
			if item.expectOriginal && !bytes.Equal(item.original, item.expected) {
				return nil, fmt.Errorf("%w: %w: %s does not match the planned source snapshot", ErrWriteFiles, ErrConcurrentChange, canonical)
			}
			if write.Mode.Perm() == 0 {
				item.mode = item.originalMode
			}
		case errors.Is(err, fs.ErrNotExist):
			if item.expectOriginal {
				return nil, fmt.Errorf("%w: %w: %s is missing from the planned source snapshot", ErrWriteFiles, ErrConcurrentChange, canonical)
			}
		case err != nil:
			return nil, fmt.Errorf("%w: inspect %s: %w", ErrWriteFiles, canonical, err)
		}
		planned = append(planned, item)
	}
	sort.Slice(planned, func(left, right int) bool { return planned[left].path < planned[right].path })
	return planned, nil
}

func inspectParents(root *os.Root, target string) ([]string, error) {
	parent := filepath.Dir(target)
	if parent == "." {
		return nil, nil
	}
	missing := false
	var missingParents []string
	current := ""
	for _, component := range splitPath(parent) {
		current = filepath.Join(current, component)
		if missing {
			missingParents = append(missingParents, current)
			continue
		}
		info, err := root.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			missing = true
			missingParents = append(missingParents, current)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: inspect parent %s: %w", ErrWriteFiles, filepath.ToSlash(current), err)
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: parent %s is not a regular directory", ErrUnsafePath, filepath.ToSlash(current))
		}
	}
	return missingParents, nil
}

func stageWrites(transactionRoot, transactionName string, planned []plannedWrite) error {
	stagedDirectory := filepath.Join(transactionRoot, "staged")
	backupDirectory := filepath.Join(transactionRoot, "backup")
	if err := os.Mkdir(stagedDirectory, 0o700); err != nil {
		return fmt.Errorf("%w: create staged-file directory: %w", ErrWriteFiles, err)
	}
	if err := os.Mkdir(backupDirectory, 0o700); err != nil {
		return fmt.Errorf("%w: create backup directory: %w", ErrWriteFiles, err)
	}
	for index := range planned {
		name := fmt.Sprintf("%06d", index)
		absoluteStage := filepath.Join(stagedDirectory, name)
		if err := writeSyncedFile(absoluteStage, planned[index].data, planned[index].mode); err != nil {
			return fmt.Errorf("%w: stage %s: %w", ErrWriteFiles, planned[index].path, err)
		}
		planned[index].stagePath = filepath.Join(transactionName, "staged", name)
		planned[index].backupPath = filepath.Join(transactionName, "backup", name)
	}
	return nil
}

func createParentDirectories(root *os.Root, planned []plannedWrite) ([]string, error) {
	created := make([]string, 0)
	known := make(map[string]struct{})
	expectedMissing := make(map[string]struct{})
	for _, write := range planned {
		for _, parent := range write.missingParents {
			expectedMissing[parent] = struct{}{}
		}
	}
	for _, write := range planned {
		parent := filepath.Dir(write.osPath)
		if parent == "." {
			continue
		}
		current := ""
		for _, component := range splitPath(parent) {
			current = filepath.Join(current, component)
			if _, alreadyKnown := known[current]; alreadyKnown {
				continue
			}
			known[current] = struct{}{}
			info, err := root.Lstat(current)
			switch {
			case err == nil:
				if _, appeared := expectedMissing[current]; appeared {
					return created, fmt.Errorf("%w: parent %s appeared", ErrConcurrentChange, filepath.ToSlash(current))
				}
				if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
					return created, fmt.Errorf("%w: parent %s is not a regular directory", ErrUnsafePath, filepath.ToSlash(current))
				}
			case errors.Is(err, fs.ErrNotExist):
				if err := root.Mkdir(current, 0o755); err != nil {
					if errors.Is(err, fs.ErrExist) {
						return created, fmt.Errorf("%w: parent %s appeared", ErrConcurrentChange, filepath.ToSlash(current))
					}
					return created, fmt.Errorf("%w: create parent %s: %w", ErrWriteFiles, filepath.ToSlash(current), err)
				}
				created = append(created, current)
			case err != nil:
				return created, fmt.Errorf("%w: inspect parent %s: %w", ErrWriteFiles, filepath.ToSlash(current), err)
			}
		}
	}
	return created, nil
}

func confirmUnchanged(root *os.Root, write plannedWrite) error {
	info, err := root.Lstat(write.osPath)
	if !write.existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err == nil {
			return fmt.Errorf("%w: %s appeared", ErrConcurrentChange, write.path)
		}
		return fmt.Errorf("%w: inspect %s: %v", ErrWriteFiles, write.path, err)
	}
	if err != nil {
		return fmt.Errorf("%w: %s disappeared", ErrConcurrentChange, write.path)
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 || info.Mode().Perm() != write.originalMode {
		return fmt.Errorf("%w: %s metadata changed", ErrConcurrentChange, write.path)
	}
	current, err := root.ReadFile(write.osPath)
	if err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrWriteFiles, write.path, err)
	}
	if !bytes.Equal(current, write.original) {
		return fmt.Errorf("%w: %s contents changed", ErrConcurrentChange, write.path)
	}
	return nil
}

func rollbackWrites(root *os.Root, applied []appliedWrite, createdDirectories []string) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		write := applied[index]
		if write.installed {
			matches, err := fileMatches(root, write.path, write.data, write.mode)
			if err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect replacement %s: %w", filepath.ToSlash(write.path), err))
				continue
			}
			if !matches {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%w: %s changed during validation", ErrConcurrentChange, filepath.ToSlash(write.path)))
				continue
			}
			if err := root.Remove(write.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove replacement %s: %w", filepath.ToSlash(write.path), err))
				continue
			}
		}
		if write.existed {
			if _, err := root.Lstat(write.path); err == nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%w: %s appeared before restore", ErrConcurrentChange, filepath.ToSlash(write.path)))
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect restore target %s: %w", filepath.ToSlash(write.path), err))
				continue
			}
			if err := root.Rename(write.backupPath, write.path); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", filepath.ToSlash(write.path), err))
			}
		}
	}
	for index := len(createdDirectories) - 1; index >= 0; index-- {
		if err := root.Remove(createdDirectories[index]); err != nil && !errors.Is(err, fs.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove created directory %s: %w", filepath.ToSlash(createdDirectories[index]), err))
		}
	}
	if rollbackErr != nil {
		return fmt.Errorf("%w: rollback: %w", ErrWriteFiles, rollbackErr)
	}
	return nil
}

func fileMatches(root *os.Root, name string, data []byte, mode fs.FileMode) (bool, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 || mode != 0 && info.Mode().Perm() != mode.Perm() {
		return false, nil
	}
	current, err := root.ReadFile(name)
	if err != nil {
		return false, err
	}
	return bytes.Equal(current, data), nil
}

func writeSyncedFile(name string, data []byte, mode fs.FileMode) (writeErr error) {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			writeErr = errors.Join(writeErr, err)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func splitPath(value string) []string {
	var components []string
	for value != "." && value != string(filepath.Separator) {
		parent, base := filepath.Split(value)
		components = append(components, base)
		value = filepath.Clean(parent)
	}
	for left, right := 0, len(components)-1; left < right; left, right = left+1, right-1 {
		components[left], components[right] = components[right], components[left]
	}
	return components
}
