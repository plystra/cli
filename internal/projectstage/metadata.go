package projectstage

import (
	"bytes"
	"context"
	"io"
	"path/filepath"

	"golang.org/x/mod/sumdb/dirhash"
)

// MetadataInput names one external file. Non-nil Expected bytes must match the
// retained read itself, rather than a separate read before or after capture.
type MetadataInput struct {
	Path          string
	Expected      []byte
	GoModChecksum string // Optional Go h1 checksum over one file named go.mod.
}

// RetainMetadata retains regular external input files without modifying them
// or refreshing earlier evidence. Its private evidence outlives stage cleanup.
func (s *Stage) RetainMetadata(ctx context.Context, files []MetadataInput) error {
	defer s.lock()()
	if err := s.recheck(ctx); err != nil {
		return err
	}
	if err := s.retainMetadata(ctx, files); err != nil {
		return err
	}
	return s.recheck(ctx)
}

func (s *Stage) retainMetadata(ctx context.Context, files []MetadataInput) error {
	if len(files) > MaximumEntries {
		return ErrStage
	}
	used := s.inputBudget()
	for _, input := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !filepath.IsAbs(input.Path) {
			return ErrStage
		}
		parent, err := s.captureDirectory(s.project, filepath.Dir(input.Path))
		if err != nil {
			return err
		}
		if within(s.temporary, parent) || within(parent, s.temporary) {
			return ErrStage
		}
		name := filepath.Base(input.Path)
		captured := false
		for _, file := range s.metadata {
			if file.parent == parent && file.name == name {
				if !input.matches(file.data) {
					return ErrStage
				}
				captured = true
				break
			}
		}
		if captured {
			continue
		}
		file, err := readMetadata(parent, name, false)
		if err != nil {
			return err
		}
		if !input.matches(file.data) {
			return ErrStage
		}
		used.entries++
		used.bytes += int64(len(file.data))
		if used.entries > MaximumEntries || used.bytes > MaximumTotalBytes {
			return ErrStage
		}
		s.metadata = append(s.metadata, file)
	}
	s.inputs = s.captureInputs()
	return nil
}

func (input MetadataInput) matches(data []byte) bool {
	if input.Expected != nil && !bytes.Equal(input.Expected, data) {
		return false
	}
	if input.GoModChecksum == "" {
		return true
	}
	checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	return err == nil && checksum == input.GoModChecksum
}

func (s *Stage) inputBudget() budget {
	var used budget
	for _, artifact := range s.artifacts {
		used.entries += 1 + len(artifact.inventory)
		used.bytes += int64(len(artifact.file.data)) + artifact.expandedBytes
	}
	for _, file := range s.metadata {
		used.entries++
		used.bytes += int64(len(file.data))
	}
	for _, trees := range [][]treeSnapshot{s.trees, s.sources} {
		for _, tree := range trees {
			used.entries += len(tree.entries)
			for _, entry := range tree.entries {
				if entry.info.Mode().IsRegular() {
					used.bytes += entry.info.Size()
				}
			}
		}
	}
	return used
}
