package moduledependency

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/mod/module"
)

func preparationInformationPaths(ctx context.Context, root string, versions []module.Version, options Options, environment []string) ([]string, error) {
	if options.RetainModuleInformation == nil || len(versions) == 0 {
		return nil, nil
	}
	seen := map[module.Version]bool{}
	var paths []string
	for _, version := range versions {
		if version.Version == "" || seen[version] {
			continue
		}
		path, pathErr := module.EscapePath(version.Path)
		value, versionErr := module.EscapeVersion(version.Version)
		if pathErr != nil || versionErr != nil {
			return nil, ErrInvalidOutput
		}
		seen[version] = true
		paths = append(paths, filepath.Join("cache", "download", filepath.FromSlash(path), "@v", value+".info"))
	}
	if len(paths) == 0 {
		return nil, nil
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return nil, err
	}
	for index := range paths {
		paths[index] = filepath.Join(cache, paths[index])
	}
	return paths, nil
}

func retainModuleInformation(ctx context.Context, paths []string, options Options) error {
	if options.RetainModuleInformation == nil {
		return nil
	}
	var present []string
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // Go may legitimately materialize a missing information file.
		} else if err != nil {
			return errors.Join(ErrSourceIntegrity, err)
		} else if !info.Mode().IsRegular() {
			return ErrSourceIntegrity
		}
		present = append(present, path)
	}
	if len(present) == 0 {
		return nil
	}
	return errors.Join(options.RetainModuleInformation(present), ctx.Err())
}
