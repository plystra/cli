package moduledependency

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/mod/module"
)

type preparationChecksums struct {
	archives  map[module.Version]string
	manifests map[module.Version]string
}

func retainWarmPreparedSources(ctx context.Context, root string, group []Module, checksums preparationChecksums, options Options, environment []string, inputs preparationInputs, retain func([]Module) error) error {
	version := sourceVersion(group[0])
	archive, manifest := checksums.archives[version], checksums.manifests[version]
	if !validSourceChecksum(archive) || !validSourceChecksum(manifest) {
		return inputs.recheck()
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return err
	}
	path, pathErr := module.EscapePath(version.Path)
	value, versionErr := module.EscapeVersion(version.Version)
	if pathErr != nil || versionErr != nil {
		return ErrInvalidOutput
	}
	directory := filepath.Join(cache, filepath.FromSlash(path)+"@"+value)
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
		return inputs.recheck()
	} else if err != nil {
		return err
	}
	metadata := filepath.Join(cache, "cache", "download", filepath.FromSlash(path), "@v", value)
	// Go replaces incomplete extractions instead of consuming their files.
	if _, err := os.Stat(metadata + ".partial"); err == nil {
		return inputs.recheck()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, suffix := range []string{".mod", ".ziphash"} {
		info, err := os.Lstat(metadata + suffix)
		if errors.Is(err, os.ErrNotExist) {
			return inputs.recheck()
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() {
			return ErrSourceIntegrity
		}
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	warm := append([]Module(nil), group...)
	for index := range warm {
		warm[index].root, warm[index].sourceDirectory = canonical, directory
		warm[index].goModFile, warm[index].goModChecksum = metadata+".mod", manifest
		warm[index].checksum = archive
	}
	if err := retain(warm); err != nil {
		return err
	}
	return errors.Join(ctx.Err(), inputs.recheck())
}
