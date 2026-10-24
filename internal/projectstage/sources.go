package projectstage

import (
	"context"
	"path/filepath"
	"slices"
)

// RetainSources captures additional read-only source roots for later candidate
// input checks. Previously captured roots are never refreshed or
// discarded, including when a later dependency selection removes them.
// Inputs obtained earlier remain immutable; obtain Inputs again after success.
func (s *Stage) RetainSources(ctx context.Context, roots []string) error {
	if err := s.Recheck(ctx); err != nil {
		return err
	}
	roots = slices.Clone(roots)
	slices.Sort(roots)
	trees := append(slices.Clone(s.trees), s.sources...)
	var used budget
	for _, tree := range trees {
		used.entries += len(tree.entries)
		for _, entry := range tree.entries {
			if entry.info.Mode().IsRegular() {
				used.bytes += entry.info.Size()
			}
		}
	}
	var added []treeSnapshot
	for _, root := range slices.Compact(roots) {
		if !filepath.IsAbs(root) {
			return ErrStage
		}
		root, err := s.captureDirectory(s.project, root)
		if err != nil {
			return err
		}
		if within(root, s.temporary) || within(s.temporary, root) {
			return ErrStage
		}
		captured := false
		for _, tree := range trees {
			if within(tree.source, root) {
				captured = true
				break
			}
		}
		if captured {
			continue
		}
		if len(s.trees)+len(s.sources)+len(added) >= MaximumRoots {
			return ErrStage
		}
		tree, err := captureTree(ctx, root, "", nil, &used)
		if err != nil {
			return err
		}
		added = append(added, tree)
		trees = append(trees, tree)
	}
	s.sources = append(s.sources, added...)
	s.inputs = s.captureInputs()
	return s.Recheck(ctx)
}
