package moduledependency

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"

	"golang.org/x/mod/module"
)

// ModuleArchive names the original cached ZIP for an effective module version.
type ModuleArchive struct {
	Path   string
	Module module.Version
}

func preparationArchiveRetainer(ctx context.Context, root string, versions []module.Version, options Options, environment []string) (func(context.Context) error, error) {
	if options.RetainModuleArchives == nil || len(versions) == 0 {
		return nil, nil
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return nil, err
	}
	var archives []ModuleArchive
	seen := map[module.Version]bool{}
	retain := func(captureCtx context.Context) error {
		return errors.Join(options.RetainModuleArchives(captureCtx, append([]ModuleArchive(nil), archives...)), captureCtx.Err())
	}
	for {
		previous := len(archives)
		for _, version := range versions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if seen[version] || version.Version == "" {
				continue
			}
			directory, manifest, err := cachedSourcePaths(cache, version)
			if err != nil {
				return nil, err
			}
			archive := strings.TrimSuffix(manifest, ".mod") + ".zip"
			if _, err := os.Lstat(archive); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, errors.Join(ErrSourceIntegrity, err)
			}
			complete, err := existingSourceTree(directory, manifest)
			if err != nil {
				return nil, err
			}
			if complete {
				checksum, err := readGoMod(archive + "hash")
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, errors.Join(ErrSourceIntegrity, err)
				}
				if validSourceChecksum(string(bytes.TrimSpace(checksum.data))) {
					continue
				}
			}
			seen[version] = true
			archives = append(archives, ModuleArchive{Path: archive, Module: version})
		}
		if len(archives) == previous {
			break
		}
		// A callback may materialize another requested archive or make a warm
		// tree need checksum repair. Drain those finite targets before Go runs,
		// while rechecking every earlier capture without refreshing it.
		if err := retain(ctx); err != nil {
			return nil, err
		}
	}
	if len(archives) == 0 {
		return nil, ctx.Err()
	}
	return retain, ctx.Err()
}
