package scaffoldplan

import (
	"context"
	"path/filepath"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
)

func retainDependencySources(ctx context.Context, stage *projectstage.Stage, command string) error {
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		return err
	}
	dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{GoCommand: command, Environment: stage.Environment()})
	if err != nil {
		return err
	}
	var roots []string
	for _, dependency := range dependencies.Modules() {
		replacement, _ := dependency.Replacement()
		if dependency.Workspace() || replacement.Local() {
			continue // Original workspace and local sources are already captured.
		}
		roots = append(roots, filepath.Clean(dependency.SourceDirectory()))
	}
	return stage.RetainSources(ctx, roots)
}
