package moduledependency

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
)

type preparationChecksums struct {
	archives  map[module.Version]string
	manifests map[module.Version]string
}

func originalSourceChecksums(root string, workspace *preparationWorkspace, inputs preparationInputs) (preparationChecksums, error) {
	var primary []byte
	var members [][]byte
	if workspace == nil {
		sums, err := inputs.read(filepath.Join(root, "go.sum"), true)
		if err != nil {
			return preparationChecksums{}, err
		}
		primary = sums.data
	} else {
		primary = workspace.sum
		for _, member := range workspace.members {
			members = append(members, member.sum)
		}
	}
	archives, err := rootChecksums(primary, members, false)
	if err != nil {
		return preparationChecksums{}, err
	}
	manifests, err := rootManifestChecksums(primary, members)
	return preparationChecksums{archives: archives, manifests: manifests}, err
}

func retainSelectedSourcesOnce(retain func([]Module) error) func([]Module) error {
	type key struct {
		version                                                      module.Version
		path, selected, directory, manifest, archiveSum, manifestSum string
	}
	retained := map[key]bool{}
	return func(sources []Module) error {
		keys := make([]key, len(sources))
		metadataComplete := make([]bool, len(sources))
		complete := true
		for index, source := range sources {
			keys[index] = key{sourceVersion(source), source.path, source.selectedVersion, source.sourceDirectory, source.goModFile, source.checksum, source.goModChecksum}
			metadataComplete[index] = !source.incompleteSourceMetadata
			complete = complete && metadataComplete[index] && retained[keys[index]]
		}
		if complete {
			return nil
		}
		if err := retain(sources); err != nil {
			return err
		}
		for index, key := range keys {
			if metadataComplete[index] {
				retained[key] = true
			}
		}
		return nil
	}
}

func retainListedSources(ctx context.Context, root string, sources []Module, checksums preparationChecksums, options Options, inputs preparationInputs, retain func([]Module) error) error {
	type key struct {
		version             module.Version
		directory, manifest string
	}
	indices := map[key]int{}
	var groups [][]Module
	for _, source := range sources {
		version := sourceVersion(source)
		if version.Version == "" || !validSourceChecksum(checksums.archives[version]) {
			continue
		}
		identity := key{version, source.sourceDirectory, source.goModFile}
		index, exists := indices[identity]
		if !exists {
			index = len(groups)
			indices[identity] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], source)
	}
	var cache string
	for _, group := range groups {
		directory, manifest := group[0].sourceDirectory, group[0].goModFile
		if directory == "" || manifest == "" {
			if cache == "" {
				var err error
				cache, err = preparationModuleCache(ctx, root, options, options.Environment)
				if err != nil {
					return err
				}
			}
			cachedDirectory, cachedManifest, err := cachedSourcePaths(cache, sourceVersion(group[0]))
			if err != nil {
				return err
			}
			if directory == "" {
				directory = cachedDirectory
			}
			if manifest == "" {
				manifest = cachedManifest
			}
		}
		if err := retainWarmSourceAt(ctx, directory, manifest, group, checksums, inputs, retain); err != nil {
			return err
		}
	}
	return errors.Join(ctx.Err(), inputs.recheck())
}

func retainWarmPreparedSources(ctx context.Context, root string, group []Module, checksums preparationChecksums, options Options, environment []string, inputs preparationInputs, retain func([]Module) error) error {
	version := sourceVersion(group[0])
	if !validSourceChecksum(checksums.archives[version]) {
		return inputs.recheck()
	}
	cache, err := preparationModuleCache(ctx, root, options, environment)
	if err != nil {
		return err
	}
	return retainWarmSourceBundle(ctx, cache, group, checksums, inputs, retain)
}

func retainWarmSourceBundle(ctx context.Context, cache string, group []Module, checksums preparationChecksums, inputs preparationInputs, retain func([]Module) error) error {
	directory, manifest, err := cachedSourcePaths(cache, sourceVersion(group[0]))
	if err != nil {
		return err
	}
	return retainWarmSourceAt(ctx, directory, manifest, group, checksums, inputs, retain)
}

func cachedSourcePaths(cache string, version module.Version) (string, string, error) {
	path, pathErr := module.EscapePath(version.Path)
	value, versionErr := module.EscapeVersion(version.Version)
	if pathErr != nil || versionErr != nil {
		return "", "", ErrInvalidOutput
	}
	directory := filepath.Join(cache, filepath.FromSlash(path)+"@"+value)
	manifest := filepath.Join(cache, "cache", "download", filepath.FromSlash(path), "@v", value+".mod")
	return directory, manifest, nil
}

func retainWarmSourceAt(ctx context.Context, directory, manifestPath string, group []Module, checksums preparationChecksums, inputs preparationInputs, retain func([]Module) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	version := sourceVersion(group[0])
	archive, manifest := checksums.archives[version], checksums.manifests[version]
	if !validSourceChecksum(archive) || !strings.HasSuffix(manifestPath, ".mod") {
		return inputs.recheck()
	}
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
		return inputs.recheck()
	} else if err != nil {
		return err
	}
	metadata := strings.TrimSuffix(manifestPath, ".mod")
	// Go replaces incomplete extractions instead of consuming their files.
	if _, err := os.Stat(metadata + ".partial"); err == nil {
		return inputs.recheck()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	incomplete := !validSourceChecksum(manifest)
	for _, suffix := range []string{".mod", ".ziphash"} {
		info, err := os.Lstat(metadata + suffix)
		if errors.Is(err, os.ErrNotExist) {
			incomplete = true
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
		warm[index].incompleteSourceMetadata = incomplete
	}
	if err := retain(warm); err != nil {
		return err
	}
	return errors.Join(ctx.Err(), inputs.recheck())
}
