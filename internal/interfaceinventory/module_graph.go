package interfaceinventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/modfile"
)

func loadProjectCandidates(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, candidates []packageCandidate, options Options) (result loadedInventory, err error) {
	if len(candidates) == 0 {
		return loadedInventory{}, nil
	}
	work, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: application.Path(), Environment: options.Environment}, "env", "GOWORK")
	if err != nil {
		return loadedInventory{}, err
	}
	if selected := strings.TrimSpace(string(work)); selected != "" && selected != "off" {
		return loadCandidatesAt(ctx, candidates, options, application.Path())
	}
	data, err := readRegularFile(filepath.Join(application.Path(), "go.mod"), 1<<20)
	if err != nil {
		return loadedInventory{}, fmt.Errorf("read current Project module for discovery")
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return loadedInventory{}, fmt.Errorf("parse current Project module for discovery")
	}
	// Listing transitive Project packages directly would promote their selected
	// modules in authored go.mod. Reify the already selected graph only in a
	// private alternate module file, keeping discovery strictly read-only.
	for _, dependency := range dependencies.Modules() {
		if err := file.AddRequire(dependency.Path(), dependency.SelectedVersion()); err != nil {
			return loadedInventory{}, fmt.Errorf("record selected module %s for discovery", dependency.Path())
		}
	}
	file.Cleanup()
	encoded, err := file.Format()
	if err != nil {
		return loadedInventory{}, fmt.Errorf("format selected discovery module")
	}
	directory, err := os.MkdirTemp("", "plystra-discovery-")
	if err != nil {
		return loadedInventory{}, fmt.Errorf("create private discovery module directory")
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			err = errors.Join(err, errors.New("remove private discovery module directory"))
		}
		if err != nil {
			err = &discoveryModuleError{cause: err, message: gocommand.SanitizeOutput(err.Error(), directory)}
		}
	}()
	path := filepath.Join(directory, "go.mod")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return loadedInventory{}, fmt.Errorf("write private discovery module")
	}
	sum, err := readRegularFile(filepath.Join(application.Path(), "go.sum"), maximumSourceSize)
	if err == nil {
		if err := os.WriteFile(filepath.Join(directory, "go.sum"), sum, 0o600); err != nil {
			return loadedInventory{}, fmt.Errorf("write private discovery checksums")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return loadedInventory{}, fmt.Errorf("read current Project checksums for discovery")
	}
	// Promoted transitive requirements can lack full checksums in the authored
	// go.sum. Let Go verify the selected modules in the private copy only.
	arguments := []string{"mod", "download", "-modfile=" + path}
	for _, dependency := range dependencies.Modules() {
		modulePath, version := dependency.Path(), dependency.SelectedVersion()
		if replacement, exists := dependency.Replacement(); exists {
			if replacement.Local() {
				continue
			}
			modulePath, version = replacement.Path(), replacement.Version()
		}
		arguments = append(arguments, modulePath+"@"+version)
	}
	if len(arguments) > 3 {
		if err := gocommand.Run(ctx, gocommand.Options{
			Command: options.GoCommand, Directory: application.Path(), Environment: options.Environment,
		}, arguments...); err != nil {
			return loadedInventory{}, fmt.Errorf("prepare private discovery checksums: %w", err)
		}
	}
	return loadCandidatesAt(ctx, candidates, options, application.Path(), "-modfile="+path)
}

type discoveryModuleError struct {
	cause   error
	message string
}

func (e *discoveryModuleError) Error() string { return e.message }
func (e *discoveryModuleError) Unwrap() error { return e.cause }
