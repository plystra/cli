// Package atomicfs provides scoped filesystem transactions for CLI mutations.
package atomicfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrCreateDirectory reports a failed staged directory creation.
	ErrCreateDirectory = errors.New("create directory transaction")
	// ErrTargetExists reports a target that cannot be replaced.
	ErrTargetExists = errors.New("transaction target already exists")
)

// CreateDirectory populates a same-parent staging directory and renames it to
// target only after populate succeeds. Existing targets are never modified.
func CreateDirectory(target string, populate func(stagingRoot string) error) (operationErr error) {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("%w: target is empty", ErrCreateDirectory)
	}
	if populate == nil {
		return fmt.Errorf("%w: populate callback is nil", ErrCreateDirectory)
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("%w: resolve target: %w", ErrCreateDirectory, err)
	}
	absoluteTarget = filepath.Clean(absoluteTarget)
	parent := filepath.Dir(absoluteTarget)
	base := filepath.Base(absoluteTarget)
	if parent == absoluteTarget || base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("%w: target must name a child directory", ErrCreateDirectory)
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("%w: inspect parent: %w", ErrCreateDirectory, err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("%w: parent is not a directory", ErrCreateDirectory)
	}
	if err := requireMissingTarget(absoluteTarget); err != nil {
		return err
	}

	stagingRoot, err := os.MkdirTemp(parent, "."+base+".plystra-")
	if err != nil {
		return fmt.Errorf("%w: create staging directory: %w", ErrCreateDirectory, err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := os.RemoveAll(stagingRoot); err != nil {
			cleanupErr := fmt.Errorf("%w: clean staging directory: %w", ErrCreateDirectory, err)
			operationErr = errors.Join(operationErr, cleanupErr)
		}
	}()

	if err := populate(stagingRoot); err != nil {
		return fmt.Errorf("%w: populate staging directory: %w", ErrCreateDirectory, err)
	}
	stagingInfo, err := os.Lstat(stagingRoot)
	if err != nil {
		return fmt.Errorf("%w: inspect populated staging directory: %w", ErrCreateDirectory, err)
	}
	if !stagingInfo.IsDir() || stagingInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: populate callback replaced the staging directory", ErrCreateDirectory)
	}
	if err := requireMissingTarget(absoluteTarget); err != nil {
		return err
	}
	if err := os.Chmod(stagingRoot, 0o755); err != nil {
		return fmt.Errorf("%w: set target directory permissions: %w", ErrCreateDirectory, err)
	}
	if err := os.Rename(stagingRoot, absoluteTarget); err != nil {
		return fmt.Errorf("%w: commit staged directory: %w", ErrCreateDirectory, err)
	}
	committed = true
	return nil
}

func requireMissingTarget(target string) error {
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%w: %s", ErrTargetExists, target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect target: %w", ErrCreateDirectory, err)
	}
	return nil
}
