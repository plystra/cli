package projectstage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/gocommand"
)

type goEnvironment struct{ GOWORK, GOFLAGS, GOENV string }

func (s *Stage) captureGoEnvironment(ctx context.Context, command string, selected goEnvironment) error {
	if selected.GOENV == "" {
		return nil
	}
	name := selected.GOENV
	if !filepath.IsAbs(name) {
		if filepath.VolumeName(name) != "" || os.IsPathSeparator(name[0]) {
			return ErrStage
		}
		name = filepath.Join(s.project, name)
	}
	name = filepath.Clean(name)
	parent := filepath.Dir(name)
	var missing []string
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := os.Lstat(parent)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
			return ErrStage
		}
		missing = append(missing, parent)
		parent = filepath.Dir(parent)
	}
	resolved, err := s.captureDirectory(s.project, parent)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(parent, name)
	if err != nil || !filepath.IsLocal(relative) {
		return ErrStage
	}
	file, err := readMetadata(resolved, relative, true)
	if err != nil {
		return err
	}
	s.metadata = append(s.metadata, file)
	s.goEnvironment = &file
	if len(missing) != 0 {
		for _, path := range append(missing, name) {
			s.paths = append(s.paths, pathSnapshot{path: path})
		}
	}
	// GOENV was located by the first Go invocation. Re-read its selected flags
	// and workspace after retaining the file, so an intervening save cannot
	// silently pair old selection with a new private preimage.
	output, err := gocommand.Output(ctx, gocommand.Options{Command: command, Directory: s.project, Environment: s.environment, OutputLimit: maximumMetadataBytes}, "env", "-json", "GOWORK", "GOFLAGS", "GOENV")
	var current goEnvironment
	if err != nil || json.Unmarshal(output, &current) != nil || current != selected {
		return errors.Join(ErrConcurrentChange, ctx.Err())
	}
	return nil
}

func (s *Stage) stageGoEnvironment() error {
	if s.goEnvironment == nil {
		return nil
	}
	if err := writePrivate(s.files, "go.env", s.goEnvironment.data); err != nil {
		return err
	}
	file, err := readMetadata(s.temporary, "go.env", false)
	if err != nil {
		return err
	}
	s.goEnvironment = &file
	s.environment = replaceEnvironment(s.environment, "GOENV", filepath.Join(s.temporary, "go.env"))
	return nil
}
