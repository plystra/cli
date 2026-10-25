package moduledependency

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/mod/module"
)

func retainModuleFilesOnce(inputs preparationInputs, retain func(context.Context, []string) error) func(context.Context, []string) error {
	retained := map[string]bool{}
	return func(ctx context.Context, paths []string) (resultErr error) {
		// Keep independent path and file evidence when a caller installs only the
		// metadata hook and there is no source-bundle recheck.
		defer func() { resultErr = errors.Join(resultErr, inputs.recheck()) }()
		var pending []string
		seen := map[string]bool{}
		for _, path := range paths {
			if !retained[path] && !seen[path] {
				pending = append(pending, path)
				seen[path] = true
			}
		}
		if len(pending) == 0 {
			return nil
		}
		for _, path := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			parent, err := filepath.EvalSymlinks(filepath.Dir(path))
			if err != nil {
				return ErrConcurrentChange
			}
			info, err := os.Lstat(parent)
			if err != nil || !info.IsDir() || !os.SameFile(info, info) {
				return ErrConcurrentChange
			}
			snapshot, err := inputs.read(path, false)
			if err != nil {
				return err
			}
			snapshot.parent, snapshot.parentInfo = parent, info
			inputs[path] = snapshot
		}
		if err := retain(ctx, append([]string(nil), pending...)); err != nil {
			return err
		}
		for _, path := range pending {
			retained[path] = true
		}
		return nil
	}
}

func preparationInformationPaths(ctx context.Context, root string, versions []module.Version, options Options, environment []string) ([]string, error) {
	if options.RetainModuleInformation == nil || len(versions) == 0 {
		return nil, nil
	}
	return preparationModuleFilePaths(ctx, root, versions, options, environment, ".info")
}

func preparationModuleFilePaths(ctx context.Context, root string, versions []module.Version, options Options, environment []string, suffix string) ([]string, error) {
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
		paths = append(paths, filepath.Join("cache", "download", filepath.FromSlash(path), "@v", value+suffix))
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

func retainPresentModuleFiles(ctx context.Context, paths []string, retain func(context.Context, []string) error) error {
	if retain == nil {
		return nil
	}
	seen := map[string]bool{}
	for {
		var present []string
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return err
			}
			if seen[path] {
				continue
			}
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue // Go may legitimately materialize a missing metadata file.
			} else if err != nil {
				return errors.Join(ErrSourceIntegrity, err)
			} else if !info.Mode().IsRegular() {
				return ErrSourceIntegrity
			}
			seen[path] = true
			present = append(present, path)
		}
		if len(present) == 0 {
			return ctx.Err()
		}
		if err := errors.Join(retain(ctx, present), ctx.Err()); err != nil {
			return err
		}
		// A callback may materialize another known path. Drain those finite
		// targets before returning to Go without refreshing earlier captures.
	}
}
