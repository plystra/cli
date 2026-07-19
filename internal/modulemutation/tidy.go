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

var (
	// ErrTidy reports failure to normalize or restore module metadata inside a
	// compound CLI transaction.
	ErrTidy = errors.New("tidy Plystra module")
	// ErrChange reports failure to apply or restore one explicit Go Module
	// dependency mutation inside a compound CLI transaction.
	ErrChange = errors.New("change Plystra module dependencies")
)

type moduleFile struct {
	exists bool
	data   []byte
	mode   fs.FileMode
}

type moduleFiles map[string]moduleFile

// ChangeOptions describes one explicit Go Module dependency command and any
// requirements that command must leave as direct project dependencies.
type ChangeOptions struct {
	GoCommand          string
	Environment        []string
	Arguments          []string
	DirectRequirements []string
}

// Change runs one explicit Go Module dependency command, then operation with a
// generation mutation that tidies metadata while preserving the dependency
// command's selected requirements. Any failure or panic restores the original
// go.mod and go.sum bytes and modes. A concurrent edit is never overwritten;
// unaffected module files are still restored under independent exact-byte
// preconditions.
func Change(ctx context.Context, root string, options ChangeOptions, operation func(applicationgenerate.ModuleMutation) error) (operationErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrChange)
	}
	if len(options.Arguments) == 0 {
		return fmt.Errorf("%w: Go dependency command is empty", ErrChange)
	}
	if operation == nil {
		return fmt.Errorf("%w: generation operation is nil", ErrChange)
	}
	before, err := captureModuleFiles(root)
	if err != nil {
		return fmt.Errorf("%w: capture module metadata: %w", ErrChange, err)
	}
	var installed moduleFiles
	committed := false
	defer func() {
		if committed || installed == nil {
			return
		}
		if err := restoreModuleFiles(root, before, installed); err != nil {
			operationErr = errors.Join(operationErr, fmt.Errorf("%w: restore module metadata: %w", ErrChange, err))
		}
	}()

	changeErr := gocommand.Run(ctx, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   root,
		Environment: options.Environment,
	}, options.Arguments...)
	if changeErr == nil && len(options.DirectRequirements) != 0 {
		changeErr = markDirectRequirements(root, options.DirectRequirements)
	}
	installed, err = captureModuleFiles(root)
	if err != nil {
		return errors.Join(changeErr, fmt.Errorf("%w: capture changed module metadata: %w", ErrChange, err))
	}
	if changeErr != nil {
		return fmt.Errorf("%w: %w", ErrChange, changeErr)
	}
	preserved := installed

	mutate := func(_ context.Context, mutationRoot string, validate func() error) error {
		if validate == nil {
			return fmt.Errorf("%w: validation callback is nil", ErrChange)
		}
		if mutationRoot != root {
			return fmt.Errorf("%w: generation changed module root from %q to %q", ErrChange, root, mutationRoot)
		}
		normalized, normalizeErr := normalizeModuleMetadata(ctx, root, options.GoCommand, options.Environment, preserved)
		if normalized != nil {
			installed = normalized
		}
		if normalizeErr != nil {
			return fmt.Errorf("%w: %w", ErrChange, normalizeErr)
		}
		if err := validate(); err != nil {
			return fmt.Errorf("%w: validate normalized module: %w", ErrChange, err)
		}
		return nil
	}
	if err := operation(mutate); err != nil {
		return err
	}
	committed = true
	return nil
}

func markDirectRequirements(root string, direct []string) error {
	files, err := captureModuleFiles(root)
	if err != nil {
		return fmt.Errorf("capture dependency command metadata: %w", err)
	}
	current := files["go.mod"]
	parsed, err := modfile.Parse("go.mod", current.data, nil)
	if err != nil {
		return fmt.Errorf("parse changed go.mod: %w", err)
	}
	wanted := make(map[string]struct{}, len(direct))
	for _, path := range direct {
		if path == "" {
			return errors.New("direct requirement path is empty")
		}
		wanted[path] = struct{}{}
	}
	requirements := make([]*modfile.Require, 0, len(parsed.Require))
	for _, requirement := range parsed.Require {
		copy := &modfile.Require{Mod: requirement.Mod, Indirect: requirement.Indirect}
		if _, exists := wanted[copy.Mod.Path]; exists {
			copy.Indirect = false
			delete(wanted, copy.Mod.Path)
		}
		requirements = append(requirements, copy)
	}
	if len(wanted) != 0 {
		missing := make([]string, 0, len(wanted))
		for path := range wanted {
			missing = append(missing, path)
		}
		sort.Strings(missing)
		return fmt.Errorf("dependency command did not select direct requirement %s", strings.Join(missing, ", "))
	}
	parsed.SetRequire(requirements)
	parsed.Cleanup()
	updated, err := parsed.Format()
	if err != nil {
		return fmt.Errorf("format direct requirements: %w", err)
	}
	if bytes.Equal(updated, current.data) {
		return nil
	}
	return atomicfs.WriteFiles(root, []atomicfs.Write{{
		Path:         "go.mod",
		Data:         updated,
		Mode:         current.mode,
		ExpectedData: current.data,
	}}, func(string) error { return nil })
}

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
		normalized, err = normalizeModuleMetadata(ctx, root, goCommand, environment, before)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrTidy, err)
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

func normalizeModuleMetadata(ctx context.Context, root, goCommand string, environment []string, preserved moduleFiles) (moduleFiles, error) {
	tidyErr := gocommand.Run(ctx, gocommand.Options{
		Command:     goCommand,
		Directory:   root,
		Environment: environment,
	}, "mod", "tidy")
	if tidyErr == nil {
		tidyErr = mergeOriginalModuleMetadata(root, preserved)
	}
	normalized, captureErr := captureModuleFiles(root)
	if captureErr != nil {
		return nil, errors.Join(tidyErr, fmt.Errorf("capture normalized module metadata: %w", captureErr))
	}
	if tidyErr != nil {
		return normalized, tidyErr
	}
	return normalized, nil
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
	var restoreErr error
	for _, name := range []string{"go.mod", "go.sum"} {
		original := before[name]
		installed := normalized[name]
		if original.exists == installed.exists && original.mode == installed.mode && bytes.Equal(original.data, installed.data) {
			continue
		}
		var writes []atomicfs.Write
		var removes []atomicfs.Remove
		switch {
		case original.exists && installed.exists:
			writes = []atomicfs.Write{{Path: name, Data: original.data, Mode: original.mode, ExpectedData: installed.data}}
		case original.exists:
			writes = []atomicfs.Write{{Path: name, Data: original.data, Mode: original.mode, MustNotExist: true}}
		case installed.exists:
			removes = []atomicfs.Remove{{Path: name, ExpectedData: installed.data}}
		}
		if err := atomicfs.ApplyFiles(root, writes, removes, func(string) error { return nil }); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore %s: %w", name, err))
		}
	}
	return restoreErr
}
