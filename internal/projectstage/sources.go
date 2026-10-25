package projectstage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/module"
)

// ErrSourceIntegrity reports invalid source identity or archive checksum evidence.
var ErrSourceIntegrity = errors.New("dependency source integrity invalid")

// Source binds a versioned effective module source to an independently resolved
// Go archive checksum. Directory preserves Go's original symbolic path spelling.
type Source struct {
	Directory string
	Module    module.Version
	Checksum  string
}

// RetainSources validates and retains complete versioned source trees against
// independently resolved h1 checksums. Previously captured roots are never
// refreshed or discarded, including after a dependency is removed.
// Inputs obtained earlier remain immutable; obtain Inputs again after success.
// Registered acquisition outputs are checked during the Stage's lifetime;
// only original-source evidence participates in Inputs after cleanup.
// Optional metadata shares the operation's complete before/after rechecks,
// avoiding repeated scans of earlier sources between related captures.
func (s *Stage) RetainSources(ctx context.Context, sources []Source, metadata ...MetadataInput) (resultErr error) {
	return s.retainSources(ctx, sources, true, metadata...)
}

// RetainSourceTrees preserves existing complete trees before their independent
// archive checksums are available. It records only private concurrency evidence,
// not source authentication. Call RetainSources with independently obtained
// checksums before accepting those trees as resolved module sources.
func (s *Stage) RetainSourceTrees(ctx context.Context, directories []string) error {
	sources := make([]Source, len(directories))
	for index, directory := range directories {
		sources[index].Directory = directory
	}
	return s.retainSources(ctx, sources, false)
}

// CheckSources rejects an accepting path that has not authenticated every
// retained source tree. Pending evidence remains recheckable on failure.
func (s *Stage) CheckSources(ctx context.Context) error {
	defer s.lock()()
	if err := s.recheck(ctx); err != nil {
		return err
	}
	for _, tree := range s.sources {
		if tree.checksum == "" {
			return ErrSourceIntegrity
		}
	}
	return nil
}

func (s *Stage) retainSources(ctx context.Context, sources []Source, authenticate bool, metadata ...MetadataInput) (resultErr error) {
	resultErr = s.WithSourceBatch(ctx, func(retain SourceRetention) error {
		if authenticate {
			return retain.Sources(sources, metadata...)
		}
		directories := make([]string, len(sources))
		for index, source := range sources {
			directories[index] = source.Directory
		}
		return retain.Trees(directories)
	})
	if resultErr != nil {
		resultErr = errors.Join(ErrSourceIntegrity, resultErr)
	}
	return resultErr
}

func (s *Stage) captureSources(ctx context.Context, sources []Source, authenticate bool, metadata ...MetadataInput) (resultErr error) {
	defer func() {
		s.inputs = s.captureInputs()
		if resultErr != nil {
			resultErr = errors.Join(ErrSourceIntegrity, resultErr)
		}
	}()
	if len(metadata) != 0 {
		if err := s.retainMetadata(ctx, metadata); err != nil {
			return err
		}
	}
	sources = slices.Clone(sources)
	slices.SortFunc(sources, func(a, b Source) int { return strings.Compare(a.Directory, b.Directory) })
	trees := append(slices.Clone(s.trees), s.sources...)
	used := s.inputBudget()
	var added []treeSnapshot
	verified := map[string]Source{}
	for _, source := range sources {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(source.Checksum, "h1:"))
		if !filepath.IsAbs(source.Directory) || authenticate && (module.Check(source.Module.Path, source.Module.Version) != nil ||
			!strings.HasPrefix(source.Checksum, "h1:") || err != nil || len(decoded) != sha256.Size ||
			base64.StdEncoding.EncodeToString(decoded) != source.Checksum[3:]) {
			return ErrStage
		}
		root, staged, err := s.acquisitionDirectory(source.Directory)
		if err == nil && !staged {
			root, err = s.captureDirectory(s.project, source.Directory)
		}
		if err != nil {
			return err
		}
		if !staged && (within(root, s.temporary) || within(s.temporary, root)) {
			return ErrStage
		}
		var captured *treeSnapshot
		for _, tree := range trees {
			if tree.source == root && tree.allFiles {
				captured = &tree
				break
			}
		}
		if captured != nil {
			if authenticate {
				if err := captured.checkSource(ctx, source); err != nil {
					return err
				}
				verified[root] = source
			}
			continue
		}
		if len(s.trees)+len(s.sources)+len(added) >= MaximumRoots {
			return ErrStage
		}
		tree, err := captureTree(ctx, root, "", nil, &used, true)
		if err != nil {
			return err
		}
		if authenticate {
			if err := tree.checkSource(ctx, source); err != nil {
				return err
			}
		}
		tree.module, tree.checksum = source.Module, source.Checksum
		tree.staged = staged
		added = append(added, tree)
		trees = append(trees, tree)
	}
	s.sources = append(s.sources, added...)
	for index, tree := range s.sources {
		if source, exists := verified[tree.source]; exists {
			s.sources[index].module, s.sources[index].checksum = source.Module, source.Checksum
		}
	}
	return nil
}

func (tree treeSnapshot) checkSource(ctx context.Context, source Source) error {
	var names []string
	for name, entry := range tree.entries {
		if entry.info.Mode().IsRegular() {
			if strings.ContainsRune(name, '\n') {
				return ErrSourceIntegrity
			}
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(filepath.ToSlash(a), filepath.ToSlash(b)) })
	hash := sha256.New()
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Go Hash1 hashes sorted summary lines. Reuse the captured file hashes
		// rather than rereading mutable source bytes for checksum validation.
		fmt.Fprintf(hash, "%x  %s/%s\n", tree.entries[name].digest, source.Module.String(), filepath.ToSlash(name))
	}
	if "h1:"+base64.StdEncoding.EncodeToString(hash.Sum(nil)) != source.Checksum {
		return ErrSourceIntegrity
	}
	return nil
}
