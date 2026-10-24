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
func (s *Stage) RetainSources(ctx context.Context, sources []Source) (resultErr error) {
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(ErrSourceIntegrity, resultErr)
		}
	}()
	if err := s.Recheck(ctx); err != nil {
		return err
	}
	sources = slices.Clone(sources)
	slices.SortFunc(sources, func(a, b Source) int { return strings.Compare(a.Directory, b.Directory) })
	trees := append(slices.Clone(s.trees), s.sources...)
	used := s.inputBudget()
	var added []treeSnapshot
	for _, source := range sources {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(source.Checksum, "h1:"))
		if !filepath.IsAbs(source.Directory) || module.Check(source.Module.Path, source.Module.Version) != nil ||
			!strings.HasPrefix(source.Checksum, "h1:") || err != nil || len(decoded) != sha256.Size ||
			base64.StdEncoding.EncodeToString(decoded) != source.Checksum[3:] {
			return ErrStage
		}
		root, err := s.captureDirectory(s.project, source.Directory)
		if err != nil {
			return err
		}
		if within(root, s.temporary) || within(s.temporary, root) {
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
			if err := captured.checkSource(ctx, source); err != nil {
				return err
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
		if err := tree.checkSource(ctx, source); err != nil {
			return err
		}
		tree.module, tree.checksum = source.Module, source.Checksum
		added = append(added, tree)
		trees = append(trees, tree)
	}
	s.sources = append(s.sources, added...)
	s.inputs = s.captureInputs()
	return s.Recheck(ctx)
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
