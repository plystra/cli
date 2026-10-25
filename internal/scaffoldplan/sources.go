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
		MetadataBatch:     metadataBatch(stage),
		SourceBatch:       sourceBatch(stage),
		AcquisitionCache:  stage.AcquisitionCache,
		RetainSourceTrees: func(ctx context.Context, paths []string) error { return retainSourceTrees(ctx, stage, paths) },
		RetainModuleArchives: func(ctx context.Context, archives []moduledependency.ModuleArchive) error {
			return retainArchives(ctx, stage, archives)
		},
		RetainSelectedSources: func(ctx context.Context, sources []moduledependency.Module) error {
			return retainSources(ctx, stage, sources)
		},
		RetainModuleInformation: func(ctx context.Context, paths []string) error { return retainMetadataFiles(ctx, stage, paths) },
		RetainManifestFiles:     func(ctx context.Context, paths []string) error { return retainMetadataFiles(ctx, stage, paths) },
		RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
			inputs := make([]projectstage.MetadataInput, len(manifests))
			for index, manifest := range manifests {
				inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
			}
			return retainStageMetadata(ctx, stage, inputs)
		},
	})
	if err != nil {
		return errors.Join(moduledependency.ErrSourceIntegrity, err)
	}
	if err := retainSources(ctx, stage, dependencies.Modules()); err != nil {
		return err
	}
	return stage.CheckSources(ctx)
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
		MetadataBatch:     metadataBatch(stage),
		SourceBatch:       sourceBatch(stage),
		AcquisitionCache:  stage.AcquisitionCache,
		RetainSourceTrees: func(ctx context.Context, paths []string) error { return retainSourceTrees(ctx, stage, paths) },
		RetainModuleArchives: func(ctx context.Context, archives []moduledependency.ModuleArchive) error {
			return retainArchives(ctx, stage, archives)
		},
		RetainModuleInformation: func(ctx context.Context, paths []string) error { return retainMetadataFiles(ctx, stage, paths) },
		RetainManifestFiles:     func(ctx context.Context, paths []string) error { return retainMetadataFiles(ctx, stage, paths) },
		RetainRootManifests: func(ctx context.Context, manifests []moduledependency.RootManifest) error {
			inputs := make([]projectstage.MetadataInput, len(manifests))
			for index, manifest := range manifests {
				inputs[index] = projectstage.MetadataInput{Path: manifest.Path, GoModChecksum: manifest.Checksum}
			}
			return retainStageMetadata(ctx, stage, inputs)
		},
	}, func(ctx context.Context, dependencies []moduledependency.Module) error {
		return retainSources(ctx, stage, dependencies)
	})
	if err != nil {
		return errors.Join(moduledependency.ErrSourceIntegrity, err)
	}
	return stage.CheckSources(ctx)
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
		if err != nil || !filepath.IsAbs(manifest) || filepath.Base(manifest) != escaped+".mod" || !dependency.IncompleteSourceMetadata() && dependency.GoModChecksum() == "" {
			return moduledependency.ErrSourceIntegrity
		}
		for _, input := range []projectstage.MetadataInput{{Path: manifest, GoModChecksum: dependency.GoModChecksum()}, {
			Path: strings.TrimSuffix(manifest, ".mod") + ".ziphash", Expected: []byte(dependency.Checksum()),
		}} {
			if input.Path == manifest && input.GoModChecksum == "" {
				continue // Archive authority does not authenticate the cache manifest.
			}
			if dependency.IncompleteSourceMetadata() {
				if _, err := os.Lstat(input.Path); errors.Is(err, os.ErrNotExist) {
					continue
				} else if err != nil {
					return errors.Join(moduledependency.ErrSourceIntegrity, err)
				}
			}
			metadata = append(metadata, input)
		}
		information := strings.TrimSuffix(manifest, ".mod") + ".info"
		if _, err := os.Lstat(information); err == nil {
			metadata = append(metadata, projectstage.MetadataInput{Path: information})
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.Join(moduledependency.ErrSourceIntegrity, err)
		}
		sources = append(sources, projectstage.Source{Directory: filepath.Clean(dependency.SourceDirectory()),
			Module: module.Version{Path: path, Version: version}, Checksum: dependency.Checksum()})
	}
	if batch, ok := ctx.Value(sourceBatchKey{}).(stageSourceBatch); ok && batch.stage == stage {
		return batch.retain.Sources(sources, metadata...)
	}
	return stage.RetainSources(ctx, sources, metadata...)
}

func retainSourceTrees(ctx context.Context, stage *projectstage.Stage, paths []string) error {
	if batch, ok := ctx.Value(sourceBatchKey{}).(stageSourceBatch); ok && batch.stage == stage {
		return batch.retain.Trees(paths)
	}
	return stage.RetainSourceTrees(ctx, paths)
}

func retainMetadataFiles(ctx context.Context, stage *projectstage.Stage, paths []string) error {
	metadata := make([]projectstage.MetadataInput, len(paths))
	for index, path := range paths {
		metadata[index] = projectstage.MetadataInput{Path: path}
	}
	return retainStageMetadata(ctx, stage, metadata)
}

type metadataBatchKey struct{}

type sourceBatchKey struct{}

type stageSourceBatch struct {
	stage  *projectstage.Stage
	retain projectstage.SourceRetention
}

func sourceBatch(stage *projectstage.Stage) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, capture func(context.Context) error) error {
		return stage.WithSourceBatch(ctx, func(retain projectstage.SourceRetention) error {
			ctx = context.WithValue(ctx, metadataBatchKey{}, stageMetadataBatch{stage, retain.MetadataRetention})
			return capture(context.WithValue(ctx, sourceBatchKey{}, stageSourceBatch{stage, retain}))
		})
	}
}

type stageMetadataBatch struct {
	stage  *projectstage.Stage
	retain projectstage.MetadataRetention
}

func metadataBatch(stage *projectstage.Stage) func(context.Context, func(context.Context) error) error {
	return func(ctx context.Context, capture func(context.Context) error) error {
		return stage.WithMetadataBatch(ctx, func(retain projectstage.MetadataRetention) error {
			return capture(context.WithValue(ctx, metadataBatchKey{}, stageMetadataBatch{stage, retain}))
		})
	}
}

func retainStageMetadata(ctx context.Context, stage *projectstage.Stage, inputs []projectstage.MetadataInput) error {
	if batch, ok := ctx.Value(metadataBatchKey{}).(stageMetadataBatch); ok && batch.stage == stage {
		return batch.retain.Files(inputs)
	}
	return stage.RetainMetadata(ctx, inputs)
}

func retainArchives(ctx context.Context, stage *projectstage.Stage, archives []moduledependency.ModuleArchive) error {
	inputs := make([]projectstage.ArchiveInput, len(archives))
	for index, archive := range archives {
		inputs[index] = projectstage.ArchiveInput{Path: archive.Path, Module: archive.Module}
	}
	if batch, ok := ctx.Value(metadataBatchKey{}).(stageMetadataBatch); ok && batch.stage == stage {
		return batch.retain.Archives(inputs)
	}
	return stage.RetainArchives(ctx, inputs)
}
