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

func (workspace *preparationWorkspace) retainPreparationMetadata(ctx context.Context, root string, requested []module.Version, options Options, environment []string, inputs preparationInputs) error {
	if options.RetainRootManifests == nil && options.RetainModuleInformation == nil || len(requested) == 0 {
		return nil
	}
	var memberSums [][]byte
	mainPaths := map[string]bool{}
	for _, member := range workspace.members {
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
		return err
	}
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
	if options.RetainRootManifests == nil {
		return nil
	}
	return retainPreparationManifests(ctx, root, versions, checksums, options, environment, inputs)
}

func retainPreparationManifests(ctx context.Context, root string, versions []module.Version, checksums map[module.Version]string, options Options, environment []string, inputs preparationInputs) error {
	if options.RetainRootManifests == nil || len(versions) == 0 || len(checksums) == 0 {
		return inputs.recheck()
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return err
	}
	graph := manifestGraph{
		inputs:    inputs,
		checksums: checksums, cache: cache,
		retain: options.RetainRootManifests, retained: map[string]bool{},
	}
	return errors.Join(graph.retainEffectiveBatch(ctx, versions), ctx.Err(), inputs.recheck())
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
