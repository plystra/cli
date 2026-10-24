package scaffoldplan

import (
	"context"
	"errors"
	"path/filepath"

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
	})
	if err != nil {
		return errors.Join(moduledependency.ErrSourceIntegrity, err)
	}
	var sources []projectstage.Source
	for _, dependency := range dependencies.Modules() {
		replacement, _ := dependency.Replacement()
		if dependency.Workspace() || replacement.Local() {
			continue // Original workspace and local sources are already captured.
		}
		path, version := dependency.Path(), dependency.SelectedVersion()
		if replacement.Path() != "" {
			path, version = replacement.Path(), replacement.Version()
		}
		sources = append(sources, projectstage.Source{Directory: filepath.Clean(dependency.SourceDirectory()),
			Module: module.Version{Path: path, Version: version}, Checksum: dependency.Checksum()})
	}
	return stage.RetainSources(ctx, sources)
}
