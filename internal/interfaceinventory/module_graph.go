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

func withOfflineGoEnvironment(options Options) Options {
	if !options.Offline {
		return options
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	for _, setting := range []string{"GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"} {
		key, _, _ := strings.Cut(setting, "=")
		filtered := make([]string, 0, len(environment)+1)
		for _, entry := range environment {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, key) {
				filtered = append(filtered, entry)
			}
		}
		environment = append(filtered, setting)
	}
	options.Environment = environment
	return options
}

func loadProjectCandidates(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, candidates []packageCandidate, options Options) (result loadedInventory, err error) {
	if len(candidates) == 0 {
		return loadedInventory{}, nil
	}
	return loadProjectPackages(ctx, application, dependencies, options, func(options Options, flags ...string) (loadedInventory, error) {
		return loadCandidatesAt(ctx, candidates, options, application.Path(), flags...)
	})
}

func loadProjectPackages(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, options Options, load func(Options, ...string) (loadedInventory, error)) (result loadedInventory, err error) {
	work, started, err := gocommand.OutputObserved(ctx, gocommand.Options{Command: options.GoCommand, Directory: application.Path(), Environment: options.Environment}, "env", "GOWORK")
	if started && options.ObserveLoad != nil {
		options.ObserveLoad(PackageLoadEvent{Module: application.ModulePath(), GoStarted: true})
	}
	if err != nil {
		return loadedInventory{}, err
	}
	if selected := strings.TrimSpace(string(work)); selected != "" && selected != "off" {
		return loadPrivateWorkspace(application, selected, options, load)
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
	if options.ObserveLoad != nil {
		options.ObserveLoad(PackageLoadEvent{Module: application.ModulePath(), TemporaryCreated: true})
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
		started, err := gocommand.RunObserved(ctx, gocommand.Options{
			Command: options.GoCommand, Directory: application.Path(), Environment: options.Environment,
		}, arguments...)
		if started && options.ObserveLoad != nil {
			options.ObserveLoad(PackageLoadEvent{Module: application.ModulePath(), GoStarted: true})
		}
		if err != nil {
			return loadedInventory{}, fmt.Errorf("prepare private discovery checksums: %w", err)
		}
	}
	return load(options, "-modfile="+path)
}

func loadPrivateWorkspace(application modulelocate.Module, workPath string, options Options, load func(Options, ...string) (loadedInventory, error)) (result loadedInventory, err error) {
	data, err := readRegularFile(workPath, maximumSourceSize)
	if err != nil {
		return result, errors.New("read selected workspace for package loading")
	}
	work, err := modfile.ParseWork("go.work", data, nil)
	if err != nil {
		return result, errors.New("parse selected workspace for package loading")
	}
	for _, use := range work.Use {
		path := use.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(workPath), path)
		}
		use.Path = filepath.ToSlash(path)
		use.Syntax.Token[len(use.Syntax.Token)-1] = modfile.AutoQuote(use.Path)
	}
	for _, replacement := range work.Replace {
		if replacement.New.Version == "" && !filepath.IsAbs(replacement.New.Path) {
			replacement.New.Path = filepath.ToSlash(filepath.Join(filepath.Dir(workPath), replacement.New.Path))
			for index, token := range replacement.Syntax.Token {
				if token == "=>" {
					replacement.Syntax.Token[index+1] = modfile.AutoQuote(replacement.New.Path)
					break
				}
			}
		}
	}
	directory, err := os.MkdirTemp("", "plystra-package-workspace-")
	if err != nil {
		return result, errors.New("create private package workspace")
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			err = errors.Join(err, errors.New("remove private package workspace"))
		}
		if err != nil {
			err = &discoveryModuleError{cause: err, message: gocommand.SanitizeOutput(err.Error(), directory)}
		}
	}()
	if options.ObserveLoad != nil {
		options.ObserveLoad(PackageLoadEvent{Module: application.ModulePath(), TemporaryCreated: true})
	}
	privateWork := filepath.Join(directory, "go.work")
	if err := os.WriteFile(privateWork, modfile.Format(work.Syntax), 0o600); err != nil {
		return result, errors.New("write private package workspace")
	}
	sums, err := readRegularFile(workPath+".sum", maximumSourceSize)
	if err == nil {
		if err := os.WriteFile(privateWork+".sum", sums, 0o600); err != nil {
			return result, errors.New("write private workspace checksums")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("read selected workspace checksums")
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	options.Environment = make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "GOWORK") {
			options.Environment = append(options.Environment, entry)
		}
	}
	options.Environment = append(options.Environment, "GOWORK="+privateWork)
	return load(options)
}

type discoveryModuleError struct {
	cause   error
	message string
}

func (e *discoveryModuleError) Error() string { return e.message }
func (e *discoveryModuleError) Unwrap() error { return e.cause }
