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

// MetadataRetention captures related inputs immediately under one Stage lock.
// Its functions expire when the WithMetadataBatch callback returns.
type MetadataRetention struct {
	Files    func([]MetadataInput) error
	Archives func([]ArchiveInput) error
}

// WithMetadataBatch shares complete before/after rechecks across related file
// and archive captures, including failure and cancellation. The callback must
// use the supplied functions rather than calling other Stage methods.
func (s *Stage) WithMetadataBatch(ctx context.Context, capture func(MetadataRetention) error) error {
	if capture == nil {
		return ErrStage
	}
	return s.WithSourceBatch(ctx, func(retain SourceRetention) error {
		return capture(retain.MetadataRetention)
	})
}

// RetainMetadata retains regular original inputs or registered private cache
// outputs without refreshing earlier evidence. Only original-side evidence
// outlives stage cleanup.
func (s *Stage) RetainMetadata(ctx context.Context, files []MetadataInput) error {
	return s.WithMetadataBatch(ctx, func(retain MetadataRetention) error { return retain.Files(files) })
}

func (s *Stage) retainMetadata(ctx context.Context, files []MetadataInput) error {
	defer func() { s.inputs = s.captureInputs() }()
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
		parent, staged, err := s.acquisitionDirectory(filepath.Dir(input.Path))
		if err == nil && !staged {
			parent, err = s.captureDirectory(s.project, filepath.Dir(input.Path))
		}
		if err != nil {
			return err
		}
		if !staged && (within(s.temporary, parent) || within(parent, s.temporary)) {
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
		file.staged = staged
		used.entries++
		used.bytes += int64(len(file.data))
		if used.entries > MaximumEntries || used.bytes > MaximumTotalBytes {
			return ErrStage
		}
		s.metadata = append(s.metadata, file)
	}
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
