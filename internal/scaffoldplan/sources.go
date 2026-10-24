package scaffoldplan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func retainDependencySources(ctx context.Context, stage *projectstage.Stage, command string) error {
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		return err
	}
	dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{
		GoCommand: command, Environment: stage.Environment(), RequireSourceChecksums: true,
		RetainSelectedSources:   func(sources []moduledependency.Module) error { return retainSources(ctx, stage, sources) },
		RetainModuleInformation: func(ctx context.Context, paths []string) error { return retainInformation(ctx, stage, paths) },
		RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
			inputs := make([]projectstage.MetadataInput, len(manifests))
			for index, manifest := range manifests {
				inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
			}
			return stage.RetainMetadata(ctx, inputs)
		},
	})
	if err != nil {
		return errors.Join(moduledependency.ErrSourceIntegrity, err)
	}
	return retainSources(ctx, stage, dependencies.Modules())
}

func prepareDependencySources(ctx context.Context, stage *projectstage.Stage, command string, versions []module.Version) error {
	if len(versions) == 0 {
		return stage.Recheck(ctx)
	}
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		return err
	}
	err = moduledependency.PrepareSources(ctx, project, versions, moduledependency.Options{GoCommand: command, Environment: stage.Environment(),
		RetainModuleInformation: func(ctx context.Context, paths []string) error { return retainInformation(ctx, stage, paths) },
		RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
			inputs := make([]projectstage.MetadataInput, len(manifests))
			for index, manifest := range manifests {
				inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
			}
			return stage.RetainMetadata(ctx, inputs)
		},
	}, func(dependencies []moduledependency.Module) error {
		return retainSources(ctx, stage, dependencies)
	})
	if err != nil {
		return errors.Join(moduledependency.ErrSourceIntegrity, err)
	}
	return stage.Recheck(ctx)
}

func retainSources(ctx context.Context, stage *projectstage.Stage, dependencies []moduledependency.Module) error {
	var sources []projectstage.Source
	var metadata []projectstage.MetadataInput
	for _, dependency := range dependencies {
		replacement, _ := dependency.Replacement()
		if dependency.Workspace() || replacement.Local() {
			continue // Original workspace and local sources are already captured.
		}
		path, version := dependency.Path(), dependency.SelectedVersion()
		if replacement.Path() != "" {
			path, version = replacement.Path(), replacement.Version()
		}
		escaped, err := module.EscapeVersion(version)
		manifest := dependency.GoModFile()
		if err != nil || !filepath.IsAbs(manifest) || filepath.Base(manifest) != escaped+".mod" || dependency.GoModChecksum() == "" {
			return moduledependency.ErrSourceIntegrity
		}
		metadata = append(metadata, projectstage.MetadataInput{Path: manifest, GoModChecksum: dependency.GoModChecksum()}, projectstage.MetadataInput{
			Path: strings.TrimSuffix(manifest, ".mod") + ".ziphash", Expected: []byte(dependency.Checksum()),
		})
		information := strings.TrimSuffix(manifest, ".mod") + ".info"
		if _, err := os.Lstat(information); err == nil {
			metadata = append(metadata, projectstage.MetadataInput{Path: information})
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.Join(moduledependency.ErrSourceIntegrity, err)
		}
		sources = append(sources, projectstage.Source{Directory: filepath.Clean(dependency.SourceDirectory()),
			Module: module.Version{Path: path, Version: version}, Checksum: dependency.Checksum()})
	}
	return stage.RetainSources(ctx, sources, metadata...)
}

func retainInformation(ctx context.Context, stage *projectstage.Stage, paths []string) error {
	metadata := make([]projectstage.MetadataInput, len(paths))
	for index, path := range paths {
		metadata[index] = projectstage.MetadataInput{Path: path}
	}
	return stage.RetainMetadata(ctx, metadata)
}
