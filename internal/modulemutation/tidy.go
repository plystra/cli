// Package modulemutation normalizes Go module metadata inside a larger CLI
// transaction without weakening rollback or concurrent-edit protection.
package modulemutation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/modfile"
)

// ErrTidy reports failure to normalize or restore module metadata inside a
// compound CLI transaction.
var ErrTidy = errors.New("tidy Plystra module")

type moduleFile struct {
	exists bool
	data   []byte
	mode   fs.FileMode
}

type moduleFiles map[string]moduleFile

// Tidy runs operation with a generation mutation that tidies Go module
// metadata after generated imports are installed. Existing explicit
// requirements and checksum entries are retained. Metadata is restored when
// operation fails after the mutation, including by panic, and concurrent edits
// are never overwritten during restoration.
func Tidy(ctx context.Context, root, goCommand string, environment []string, operation func(applicationgenerate.ModuleMutation) error) (operationErr error) {
	if operation == nil {
		return fmt.Errorf("%w: generation operation is nil", ErrTidy)
	}
	before, err := captureModuleFiles(root)
	if err != nil {
		return fmt.Errorf("%w: capture module metadata: %w", ErrTidy, err)
	}
	var normalized moduleFiles
	committed := false
	defer func() {
		if committed || normalized == nil {
			return
		}
		if err := restoreModuleFiles(root, before, normalized); err != nil {
			operationErr = errors.Join(operationErr, fmt.Errorf("%w: restore module metadata: %w", ErrTidy, err))
		}
	}()

	mutate := func(_ context.Context, mutationRoot string, validate func() error) error {
		if validate == nil {
			return fmt.Errorf("%w: validation callback is nil", ErrTidy)
		}
		if mutationRoot != root {
			return fmt.Errorf("%w: generation changed module root from %q to %q", ErrTidy, root, mutationRoot)
		}
		tidyErr := gocommand.Run(ctx, gocommand.Options{
			Command:     goCommand,
			Directory:   root,
			Environment: environment,
		}, "mod", "tidy")
		if tidyErr == nil {
			tidyErr = mergeOriginalModuleMetadata(root, before)
		}
		normalized, err = captureModuleFiles(root)
		if err != nil {
			return errors.Join(tidyErr, fmt.Errorf("%w: capture normalized module metadata: %w", ErrTidy, err))
		}
		if tidyErr != nil {
			return fmt.Errorf("%w: %w", ErrTidy, tidyErr)
		}
		if err := validate(); err != nil {
			return fmt.Errorf("%w: validate normalized module: %w", ErrTidy, err)
		}
		return nil
	}
	if err := operation(mutate); err != nil {
		return err
	}
	committed = true
	return nil
}

func mergeOriginalModuleMetadata(root string, original moduleFiles) error {
	tidied, err := captureModuleFiles(root)
	if err != nil {
		return fmt.Errorf("%w: capture tidied module metadata: %w", ErrTidy, err)
	}
	mergedMod, err := mergeGoMod(original["go.mod"].data, tidied["go.mod"].data)
	if err != nil {
		return fmt.Errorf("%w: preserve original requirements: %w", ErrTidy, err)
	}
	mergedSum := mergeGoSum(original["go.sum"], tidied["go.sum"])
	writes := make([]atomicfs.Write, 0, 2)
	if !bytes.Equal(mergedMod, tidied["go.mod"].data) {
		writes = append(writes, atomicfs.Write{
			Path:         "go.mod",
			Data:         mergedMod,
			Mode:         tidied["go.mod"].mode,
			ExpectedData: tidied["go.mod"].data,
		})
	}
	if mergedSum.exists && (!tidied["go.sum"].exists || !bytes.Equal(mergedSum.data, tidied["go.sum"].data)) {
		write := atomicfs.Write{Path: "go.sum", Data: mergedSum.data, Mode: mergedSum.mode}
		if tidied["go.sum"].exists {
			write.ExpectedData = tidied["go.sum"].data
		} else {
			write.MustNotExist = true
		}
		writes = append(writes, write)
	}
	if len(writes) == 0 {
		return nil
	}
	if err := atomicfs.WriteFiles(root, writes, func(string) error { return nil }); err != nil {
		return fmt.Errorf("%w: install preserved module metadata: %w", ErrTidy, err)
	}
	return nil
}

func mergeGoMod(original, tidied []byte) ([]byte, error) {
	originalFile, err := modfile.Parse("go.mod", original, nil)
	if err != nil {
		return nil, fmt.Errorf("parse original go.mod: %w", err)
	}
	tidiedFile, err := modfile.Parse("go.mod", tidied, nil)
	if err != nil {
		return nil, fmt.Errorf("parse tidied go.mod: %w", err)
	}
	requirements := make([]*modfile.Require, 0, len(tidiedFile.Require)+len(originalFile.Require))
	byPath := make(map[string]*modfile.Require, len(tidiedFile.Require)+len(originalFile.Require))
	for _, requirement := range tidiedFile.Require {
		copy := &modfile.Require{Mod: requirement.Mod, Indirect: requirement.Indirect}
		if previous, duplicate := byPath[copy.Mod.Path]; duplicate {
			if previous.Mod.Version != copy.Mod.Version {
				return nil, fmt.Errorf("tidied go.mod contains conflicting versions for %s", copy.Mod.Path)
			}
			if !copy.Indirect {
				previous.Indirect = false
			}
			continue
		}
		byPath[copy.Mod.Path] = copy
		requirements = append(requirements, copy)
	}
	for _, requirement := range originalFile.Require {
		if retained, exists := byPath[requirement.Mod.Path]; exists {
			if retained.Mod.Version != requirement.Mod.Version {
				return nil, fmt.Errorf("go mod tidy changed %s from %s to %s", requirement.Mod.Path, requirement.Mod.Version, retained.Mod.Version)
			}
			if !requirement.Indirect {
				retained.Indirect = false
			}
			continue
		}
		copy := &modfile.Require{Mod: requirement.Mod, Indirect: requirement.Indirect}
		byPath[copy.Mod.Path] = copy
		requirements = append(requirements, copy)
	}
	tidiedFile.SetRequire(requirements)
	tidiedFile.Cleanup()
	merged, err := tidiedFile.Format()
	if err != nil {
		return nil, fmt.Errorf("format merged go.mod: %w", err)
	}
	return merged, nil
}

func mergeGoSum(original, tidied moduleFile) moduleFile {
	if !original.exists {
		return tidied
	}
	lines := make(map[string]struct{})
	for _, data := range [][]byte{original.data, tidied.data} {
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if line != "" {
				lines[line] = struct{}{}
			}
		}
	}
	ordered := make([]string, 0, len(lines))
	for line := range lines {
		ordered = append(ordered, line)
	}
	sort.Strings(ordered)
	mode := tidied.mode
	if !tidied.exists {
		mode = original.mode
	}
	return moduleFile{exists: true, data: []byte(strings.Join(ordered, "\n") + "\n"), mode: mode}
}

func captureModuleFiles(rootPath string) (result moduleFiles, captureErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := root.Close(); err != nil {
			captureErr = errors.Join(captureErr, err)
		}
	}()
	result = make(moduleFiles, 2)
	for _, name := range []string{"go.mod", "go.sum"} {
		info, err := root.Lstat(name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			result[name] = moduleFile{}
			continue
		case err != nil:
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is not a regular non-symbolic file", name)
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		result[name] = moduleFile{exists: true, data: data, mode: info.Mode().Perm()}
	}
	return result, nil
}

func restoreModuleFiles(root string, before, normalized moduleFiles) error {
	writes := make([]atomicfs.Write, 0, 2)
	removes := make([]atomicfs.Remove, 0, 1)
	for _, name := range []string{"go.mod", "go.sum"} {
		original := before[name]
		installed := normalized[name]
		if original.exists == installed.exists && original.mode == installed.mode && bytes.Equal(original.data, installed.data) {
			continue
		}
		switch {
		case original.exists && installed.exists:
			writes = append(writes, atomicfs.Write{Path: name, Data: original.data, Mode: original.mode, ExpectedData: installed.data})
		case original.exists:
			writes = append(writes, atomicfs.Write{Path: name, Data: original.data, Mode: original.mode, MustNotExist: true})
		case installed.exists:
			removes = append(removes, atomicfs.Remove{Path: name, ExpectedData: installed.data})
		}
	}
	if len(writes) == 0 && len(removes) == 0 {
		return nil
	}
	return atomicfs.ApplyFiles(root, writes, removes, func(string) error { return nil })
}
