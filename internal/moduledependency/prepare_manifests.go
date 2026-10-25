package moduledependency

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func (workspace *preparationWorkspace) retainPreparationInputs(ctx context.Context, selection *preparationWorkspace, requested []module.Version, options Options, environment []string, inputs preparationInputs, retain func([]Module) error) error {
	if len(requested) == 0 {
		return nil
	}
	root := selection.members[0].root
	var memberSums [][]byte
	mainPaths := map[string]bool{}
	replacementCount := len(workspace.work.Replace)
	for _, member := range workspace.members {
		replacementCount += len(member.mod.Replace)
		original, exists := inputs[filepath.Join(member.root, "go.mod")]
		if !exists {
			return ErrInvalidGoMod
		}
		parsed, err := modfile.Parse("go.mod", original.data, exactManifestVersion)
		if err != nil {
			return nil // Query spellings require ordinary Go selection first.
		}
		if parsed.Module == nil {
			return ErrInvalidGoMod
		}
		mainPaths[parsed.Module.Mod.Path] = true
		memberSums = append(memberSums, member.sum)
	}
	if err := workspace.validateRootManifestReplacements(); err != nil {
		if options.RetainRootManifests != nil || options.RetainModuleInformation != nil {
			return err
		}
		return nil // Preserve ordinary Go diagnostics when optional inference fails.
	}
	if options.RetainRootManifests != nil || options.RetainModuleInformation != nil {
		checksums, err := rootManifestChecksums(workspace.sum, memberSums)
		if err != nil {
			return err
		}
		var versions []module.Version
		for _, requested := range requested {
			if mainPaths[requested.Path] {
				continue
			}
			effective, err := workspace.rootManifestVersion(requested)
			if err != nil {
				return err
			}
			versions = append(versions, effective)
		}
		information, err := preparationInformationPaths(ctx, root, versions, options, environment)
		if err != nil {
			return err
		}
		if err := errors.Join(retainModuleInformation(ctx, information, options), inputs.recheck()); err != nil {
			return err
		}
		if err := retainPreparationManifests(ctx, root, versions, checksums, options, environment, inputs); err != nil {
			return err
		}
	}
	sourceChecksums, err := originalSourceChecksums(root, workspace, inputs)
	if err != nil {
		return errors.Join(ctx.Err(), inputs.recheck()) // Let Go diagnose malformed authority without guessing a source.
	}
	indices := map[module.Version]int{}
	var groups [][]Module
	pending := map[string]bool{}
	proofBudget := 65_536
	for _, version := range requested {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, inputs.recheck())
		}
		if mainPaths[version.Path] {
			continue
		}
		effective, uniform := workspace.uniformPreparationSource(ctx, version.Path, replacementCount, &proofBudget)
		if !uniform {
			pending[version.Path] = true
			continue
		}
		if effective.Version == "" {
			continue // Every possible selection is local and needs no cache probe.
		}
		index, exists := indices[effective]
		if !exists {
			index = len(groups)
			indices[effective] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], Module{path: version.Path, selectedVersion: version.Version, sourcePath: effective.Path,
			replacement: Replacement{path: effective.Path, version: effective.Version}})
	}
	for _, group := range groups {
		err := retainWarmPreparedSources(ctx, root, group, sourceChecksums, options, environment, inputs, retain)
		if err := errors.Join(err, ctx.Err(), inputs.recheck()); err != nil {
			return err
		}
	}
	if len(pending) != 0 {
		checksums, err := rootManifestChecksums(workspace.sum, memberSums)
		if err != nil {
			return errors.Join(ctx.Err(), inputs.recheck())
		}
		output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root,
			Environment: environment, OutputLimit: maximumGoModSize}, "env", "-json", "GOVERSION")
		if err != nil {
			return errors.Join(err, ctx.Err(), inputs.recheck())
		}
		var selected struct{ GOVERSION string }
		if json.Unmarshal(output, &selected) != nil {
			return errors.Join(ErrInvalidOutput, ctx.Err(), inputs.recheck())
		}
		var mains []*modfile.File
		for _, member := range selection.members {
			mains = append(mains, member.mod)
		}
		graph := manifestGraph{
			root: root, mains: mains, workspace: selection, inputs: inputs,
			checksums: checksums, archives: sourceChecksums.archives,
			loadCache: func(ctx context.Context) (string, error) {
				return preparationModuleCache(ctx, root, options, environment)
			},
			toolchain: selected.GOVERSION, queryPaths: pending,
			retain: options.RetainRootManifests, retainInformation: options.RetainModuleInformation,
			retainSources: retain, retainTrees: options.RetainSourceTrees,
			retained: map[string]bool{}, parsed: map[string]*modfile.File{},
		}
		return errors.Join(graph.capture(ctx), ctx.Err(), inputs.recheck())
	}
	return errors.Join(ctx.Err(), inputs.recheck())
}

// A wildcard must map every possible MVS choice to the same versioned source,
// or keep every choice local. Exact rules partition that mapping; an unresolved
// partition does not establish whether a cache source participates in selection.
func (workspace *preparationWorkspace) uniformPreparationSource(ctx context.Context, path string, replacementCount int, budget *int) (module.Version, bool) {
	spend := func(count int) bool {
		if ctx.Err() != nil || count > *budget {
			return false
		}
		*budget -= count
		return true
	}
	// Existing precedence lookup may inspect every declaration. Bound the whole
	// batch conservatively rather than duplicating that resolver for inference.
	if !spend(replacementCount) {
		return module.Version{}, false
	}
	effective, err := workspace.rootManifestVersion(module.Version{Path: path})
	if err != nil || effective.Version == "" && !filepath.IsAbs(effective.Path) {
		return module.Version{}, false
	}
	seen := map[string]bool{}
	check := func(replacements []*modfile.Replace) bool {
		for _, replacement := range replacements {
			if !spend(1) {
				return false
			}
			version := replacement.Old
			if version.Path != path || version.Version == "" || seen[version.Version] {
				continue
			}
			seen[version.Version] = true
			if !spend(replacementCount) {
				return false
			}
			selected, err := workspace.rootManifestVersion(version)
			if err != nil || selected != effective && (selected.Version != "" || effective.Version != "") {
				return false
			}
		}
		return true
	}
	if !check(workspace.work.Replace) {
		return module.Version{}, false
	}
	for _, member := range workspace.members {
		if !check(member.mod.Replace) {
			return module.Version{}, false
		}
	}
	return effective, true
}

func retainPreparationManifests(ctx context.Context, root string, versions []module.Version, checksums map[module.Version]string, options Options, environment []string, inputs preparationInputs) error {
	retain, err := preparationManifestRetainer(ctx, root, versions, checksums, options, environment, inputs)
	if err != nil {
		return err
	}
	return retain(ctx)
}

func preparationManifestRetainer(ctx context.Context, root string, versions []module.Version, checksums map[module.Version]string, options Options, environment []string, inputs preparationInputs) (func(context.Context) error, error) {
	eligible := false
	for _, version := range versions {
		if version.Version != "" && validSourceChecksum(checksums[version]) {
			eligible = true
			break
		}
	}
	if options.RetainRootManifests == nil || !eligible {
		return func(ctx context.Context) error { return errors.Join(ctx.Err(), inputs.recheck()) }, nil
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		graph := manifestGraph{
			inputs:    inputs,
			checksums: checksums, cache: cache,
			retain: options.RetainRootManifests, retained: map[string]bool{},
		}
		return errors.Join(graph.retainEffectiveBatch(ctx, versions), ctx.Err(), inputs.recheck())
	}, nil
}

func preparationModuleCache(ctx context.Context, root string, options Options, environment []string) (string, error) {
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root,
		Environment: environment, OutputLimit: maximumGoModSize}, "env", "-json", "GOMODCACHE")
	if err != nil {
		return "", err
	}
	var selected struct{ GOMODCACHE string }
	if json.Unmarshal(output, &selected) != nil || !filepath.IsAbs(selected.GOMODCACHE) {
		return "", ErrInvalidOutput
	}
	return selected.GOMODCACHE, nil
}
