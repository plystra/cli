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
)

var errPrivateInput = errors.New("private Project staging inputs cannot be serialized")

// Inputs retains the original authored Project and every original staging
// snapshot. It owns no open handles and remains usable after Stage.Close.
// Its zero value is invalid; it confers no lock or installation authority.
type Inputs struct {
	project  string
	trees    []inputTree
	metadata []fileSnapshot
	aliases  []directorySnapshot
	paths    []pathSnapshot
}

type inputTree struct {
	source  string
	entries map[string]treeEntry
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
	exists bool
	data   []byte
	mode   os.FileMode
}

func (FileImage) String() string                   { return "<private-project-file>" }
func (f FileImage) GoString() string               { return f.String() }
func (f FileImage) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, f.String()) }
func (f FileImage) LogValue() slog.Value           { return slog.StringValue(f.String()) }
func (FileImage) MarshalJSON() ([]byte, error)     { return nil, errPrivateInput }
func (FileImage) MarshalText() ([]byte, error)     { return nil, errPrivateInput }
func (FileImage) MarshalYAML() (any, error)        { return nil, errPrivateInput }
func (f FileImage) Exists() bool                   { return f.exists }
func (f FileImage) Data() []byte                   { return append([]byte(nil), f.data...) }
func (f FileImage) Mode() os.FileMode              { return f.mode }

// Inputs returns immutable original-side evidence, independently of the Stage's
// temporary files and lifetime. A Stage not successfully created returns zero.
func (s *Stage) Inputs() Inputs {
	if s == nil {
		return Inputs{}
	}
	return s.inputs
}

func (s *Stage) captureInputs() Inputs {
	result := Inputs{project: s.project, metadata: append([]fileSnapshot(nil), s.metadata...),
		aliases: append([]directorySnapshot(nil), s.aliases...), paths: append([]pathSnapshot(nil), s.paths...)}
	for index := range result.metadata {
		result.metadata[index].data = append([]byte(nil), result.metadata[index].data...)
	}
	for _, tree := range s.trees {
		result.trees = append(result.trees, inputTree{source: tree.source, entries: maps.Clone(tree.entries)})
	}
	return result
}

// Recheck rejects original byte, mode, identity, alias and membership drift.
// It does not inspect the temporary candidate or establish a mutation lock.
func (i Inputs) Recheck(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || i.project == "" || len(i.trees) == 0 {
		return ErrConcurrentChange
	}
	for _, file := range i.metadata {
		if ctx.Err() != nil || file.recheck() != nil {
			return ErrConcurrentChange
		}
	}
	for _, alias := range i.aliases {
		if ctx.Err() != nil {
			return ErrConcurrentChange
		}
		current, err := canonicalDirectory(alias.path)
		if err != nil || current != alias.target {
			return ErrConcurrentChange
		}
		info, err := os.Lstat(current)
		if err != nil || !sameFile(alias.info, info) {
			return ErrConcurrentChange
		}
	}
	for _, path := range i.paths {
		current, err := os.Lstat(path.path)
		if ctx.Err() != nil || err != nil && !errors.Is(err, os.ErrNotExist) || !sameFile(path.info, current) {
			return ErrConcurrentChange
		}
	}
	var used budget
	for _, before := range i.trees {
		after, err := captureTree(ctx, before.source, "", nil, &used)
		if err != nil || !sameTree(treeSnapshot{entries: before.entries}, after) {
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
// present paths must retain their captured bytes, identity and mode.
func (i Inputs) ProjectFile(ctx context.Context, path string) (FileImage, error) {
	if ctx == nil || ctx.Err() != nil || i.project == "" || !projectFilePath(path) {
		return FileImage{}, ErrConcurrentChange
	}
	for _, tree := range i.trees {
		if !within(tree.source, i.project) {
			continue
		}
		relative, err := filepath.Rel(tree.source, filepath.Join(i.project, filepath.FromSlash(path)))
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
	return FileImage{exists: true, data: data, mode: before.info.Mode()}, nil
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
	}
	return nil
}
