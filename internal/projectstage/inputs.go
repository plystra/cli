package projectstage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
)

var errPrivateInput = errors.New("private Project staging inputs cannot be serialized")

// Inputs retains the original authored Project and every original staging
// snapshot. It owns no open handles and remains usable after Stage.Close.
// Its zero value is invalid; it confers no lock or installation authority.
type Inputs struct {
	// fmt skips methods on unexported wrapper fields. Its invalid-verb fallback
	// also dereferences a single struct pointer; two levels keep contents opaque.
	state **inputState
}

type inputState struct {
	project  string
	trees    []inputTree
	metadata []fileSnapshot
	aliases  []directorySnapshot
	paths    []pathSnapshot
}

type inputTree struct {
	source   string
	entries  map[string]treeEntry
	allFiles bool
	module   module.Version
	checksum string
}

func (Inputs) String() string                   { return "<private-project-inputs>" }
func (i Inputs) GoString() string               { return i.String() }
func (i Inputs) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, i.String()) }
func (i Inputs) LogValue() slog.Value           { return slog.StringValue(i.String()) }
func (Inputs) MarshalJSON() ([]byte, error)     { return nil, errPrivateInput }
func (Inputs) MarshalText() ([]byte, error)     { return nil, errPrivateInput }
func (Inputs) MarshalYAML() (any, error)        { return nil, errPrivateInput }

// FileImage is an exact private original file preimage. The zero value means
// absence. Data returns an independent copy, including for repeated calls.
type FileImage struct {
	state **fileImageState
}

type fileImageState struct {
	data []byte
	mode os.FileMode
}

func (FileImage) String() string                   { return "<private-project-file>" }
func (f FileImage) GoString() string               { return f.String() }
func (f FileImage) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, f.String()) }
func (f FileImage) LogValue() slog.Value           { return slog.StringValue(f.String()) }
func (FileImage) MarshalJSON() ([]byte, error)     { return nil, errPrivateInput }
func (FileImage) MarshalText() ([]byte, error)     { return nil, errPrivateInput }
func (FileImage) MarshalYAML() (any, error)        { return nil, errPrivateInput }
func (f FileImage) Exists() bool                   { return f.captured() != nil }
func (f FileImage) Data() []byte {
	state := f.captured()
	if state == nil {
		return nil
	}
	return append([]byte(nil), state.data...)
}
func (f FileImage) Mode() os.FileMode {
	state := f.captured()
	if state == nil {
		return 0
	}
	return state.mode
}

func (f FileImage) captured() *fileImageState {
	if f.state == nil {
		return nil
	}
	return *f.state
}

func (i Inputs) captured() *inputState {
	if i.state == nil {
		return nil
	}
	return *i.state
}

// Inputs returns immutable original-side evidence, independently of the Stage's
// temporary files and lifetime. A Stage not successfully created returns zero.
func (s *Stage) Inputs() Inputs {
	if s == nil {
		return Inputs{}
	}
	return s.inputs
}

func (s *Stage) captureInputs() Inputs {
	state := &inputState{project: s.project, metadata: append([]fileSnapshot(nil), s.metadata...),
		aliases: append([]directorySnapshot(nil), s.aliases...), paths: append([]pathSnapshot(nil), s.paths...)}
	for index := range state.metadata {
		state.metadata[index].data = append([]byte(nil), state.metadata[index].data...)
	}
	for _, tree := range append(append([]treeSnapshot(nil), s.trees...), s.sources...) {
		state.trees = append(state.trees, inputTree{source: tree.source, entries: maps.Clone(tree.entries),
			allFiles: tree.allFiles, module: tree.module, checksum: tree.checksum})
	}
	return Inputs{state: &state}
}

// Recheck rejects original byte, mode, native access, identity, alias and membership drift.
// It does not inspect the temporary candidate or establish a mutation lock.
func (i Inputs) Recheck(ctx context.Context) error {
	state := i.captured()
	if ctx == nil || ctx.Err() != nil || state == nil || state.project == "" || len(state.trees) == 0 {
		return ErrConcurrentChange
	}
	for _, file := range state.metadata {
		if ctx.Err() != nil || file.recheck() != nil {
			return ErrConcurrentChange
		}
	}
	for _, alias := range state.aliases {
		if ctx.Err() != nil {
			return ErrConcurrentChange
		}
		current, err := canonicalDirectory(alias.path)
		if err != nil || current != alias.target {
			return ErrConcurrentChange
		}
		info, access, err := directoryAccess(current)
		if err != nil || !sameFile(alias.info, info) || alias.access != access {
			return ErrConcurrentChange
		}
	}
	for _, path := range state.paths {
		current, err := os.Lstat(path.path)
		if ctx.Err() != nil || err != nil && !errors.Is(err, os.ErrNotExist) || !sameFile(path.info, current) {
			return ErrConcurrentChange
		}
		if current != nil {
			root, err := os.OpenRoot(filepath.Dir(path.path))
			if err != nil {
				return ErrConcurrentChange
			}
			opened, access, err := readAccessInfo(root, filepath.Base(path.path))
			root.Close()
			if err != nil || !sameFile(current, opened) || path.access != access {
				return ErrConcurrentChange
			}
		}
	}
	var used budget
	for _, before := range state.trees {
		after, err := captureTree(ctx, before.source, "", nil, &used, before.allFiles)
		if err != nil || !sameTree(treeSnapshot{entries: before.entries}, after) {
			return ErrConcurrentChange
		}
		if before.checksum != "" && after.checkSource(ctx, Source{Module: before.module, Checksum: before.checksum}) != nil {
			return ErrConcurrentChange
		}
	}
	if ctx.Err() != nil {
		return ErrConcurrentChange
	}
	return nil
}

func projectFilePath(path string) bool {
	if path == "." || !fs.ValidPath(path) || strings.ContainsAny(path, "\\:\x00") || !filepath.IsLocal(filepath.FromSlash(path)) {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".git" || part == ".hg" || part == ".svn" {
			return false
		}
	}
	return true
}

// ProjectFile reads an original Project-relative slash path against the captured
// inventory, never the relocated candidate. Missing paths must still be absent;
// present paths must retain captured bytes, identity, mode and native access.
func (i Inputs) ProjectFile(ctx context.Context, path string) (FileImage, error) {
	state := i.captured()
	if ctx == nil || ctx.Err() != nil || state == nil || state.project == "" || !projectFilePath(path) {
		return FileImage{}, ErrConcurrentChange
	}
	for _, tree := range state.trees {
		if !within(tree.source, state.project) {
			continue
		}
		relative, err := filepath.Rel(tree.source, filepath.Join(state.project, filepath.FromSlash(path)))
		if err != nil || !filepath.IsLocal(relative) {
			return FileImage{}, ErrConcurrentChange
		}
		return tree.file(ctx, relative)
	}
	return FileImage{}, ErrConcurrentChange
}

func (t inputTree) file(ctx context.Context, path string) (FileImage, error) {
	root, err := os.OpenRoot(t.source)
	if err != nil {
		return FileImage{}, ErrConcurrentChange
	}
	defer root.Close()
	if err := t.checkPath(ctx, root, path); err != nil {
		return FileImage{}, err
	}
	before, exists := t.entries[path]
	if !exists {
		return FileImage{}, t.checkPath(ctx, root, path)
	}
	if !before.info.Mode().IsRegular() {
		return FileImage{}, ErrConcurrentChange
	}
	file, err := root.Open(path)
	if err != nil {
		return FileImage{}, ErrConcurrentChange
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameFile(before.info, opened) {
		return FileImage{}, ErrConcurrentChange
	}
	data, err := io.ReadAll(io.LimitReader(file, MaximumFileBytes+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) > MaximumFileBytes || int64(len(data)) != before.info.Size() ||
		!sameFile(before.info, after) || sha256.Sum256(data) != before.digest || t.checkPath(ctx, root, path) != nil {
		return FileImage{}, ErrConcurrentChange
	}
	state := &fileImageState{data: data, mode: before.info.Mode()}
	return FileImage{state: &state}, nil
}

func (t inputTree) checkPath(ctx context.Context, root *os.Root, path string) error {
	info, err := os.Lstat(t.source)
	if err != nil || !sameFile(t.entries["."].info, info) {
		return ErrConcurrentChange
	}
	parts := append([]string{"."}, strings.Split(path, string(filepath.Separator))...)
	relative := "."
	for index, part := range parts {
		if ctx.Err() != nil {
			return ErrConcurrentChange
		}
		relative = filepath.Join(relative, part)
		before, exists := t.entries[relative]
		current, err := root.Lstat(relative)
		if !exists {
			if !errors.Is(err, os.ErrNotExist) {
				return ErrConcurrentChange
			}
			continue
		}
		if err != nil || !sameFile(before.info, current) || index < len(parts)-1 && !current.IsDir() {
			return ErrConcurrentChange
		}
		opened, access, err := readAccessInfo(root, relative)
		if err != nil || !sameFile(before.info, opened) || before.access != access {
			return ErrConcurrentChange
		}
	}
	return nil
}
