// Package plugincreate creates deterministic root-level plugin scaffolds.
package plugincreate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/pluginscan"
	"golang.org/x/mod/module"
)

var (
	// ErrCreate reports a failed plugin-creation transaction.
	ErrCreate = errors.New("create Plystra plugin")
	// ErrInvalidName reports a non-canonical plugin directory name.
	ErrInvalidName = errors.New("invalid plugin name")
	// ErrDeriveID reports a module path that cannot form a canonical Plugin ID.
	ErrDeriveID = errors.New("derive plugin ID")
)

// Options contains the explicit inputs and process environment for creation.
type Options struct {
	Start       string
	Name        string
	GoCommand   string
	Environment []string
}

// Result identifies one committed plugin scaffold.
type Result struct {
	id         string
	path       string
	moduleRoot string
}

// ID returns the derived canonical Plugin ID.
func (r Result) ID() string { return r.id }

// Path returns the absolute plugin directory path.
func (r Result) Path() string { return r.path }

// ModuleRoot returns the canonical absolute Go Module root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// DeriveID validates name and derives its canonical Plugin ID from modulePath.
func DeriveID(modulePath, name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return deriveID(modulePath, name)
}

// Create writes and validates one new plugin as an atomic module mutation.
func Create(ctx context.Context, options Options) (Result, error) {
	if err := validateName(options.Name); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	located, err := modulelocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate module: %w", ErrCreate, err)
	}
	id, err := deriveID(located.ModulePath(), options.Name)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	pluginPath := filepath.Join(located.Path(), options.Name)
	if _, err := os.Lstat(pluginPath); err == nil {
		return Result{}, fmt.Errorf("%w: plugin directory %q already exists", ErrCreate, options.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("%w: inspect plugin directory %q: %w", ErrCreate, options.Name, err)
	}

	writes, err := renderScaffold(located.ModulePath(), options.Name, id)
	if err != nil {
		return Result{}, fmt.Errorf("%w: render scaffold: %w", ErrCreate, err)
	}
	err = atomicfs.WriteFiles(located.Path(), writes, func(updatedRoot string) error {
		scan, err := pluginscan.ScanRoot(updatedRoot)
		if err != nil {
			return err
		}
		found := false
		for _, directory := range scan.Directories() {
			if directory.Name() == options.Name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("created plugin %q is not discoverable", options.Name)
		}
		return tidyModule(ctx, updatedRoot, options.GoCommand, options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
			_, err = applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:        updatedRoot,
				GoCommand:    options.GoCommand,
				Environment:  options.Environment,
				MutateModule: mutate,
			})
			return err
		})
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{id: id, path: pluginPath, moduleRoot: located.Path()}, nil
}

func validateName(name string) error {
	if name == "" || len(name) > 64 || !pluginid.ValidSegment(name) {
		return fmt.Errorf("%w %q: expected lower-case ASCII kebab-case", ErrInvalidName, name)
	}
	if pluginscan.IsReserved(name) {
		return fmt.Errorf("%w %q: name is reserved at the module root", ErrInvalidName, name)
	}
	return nil
}

func deriveID(modulePath, name string) (string, error) {
	if err := module.CheckPath(modulePath); err != nil {
		return "", fmt.Errorf("%w: invalid Go Module path %q: %v", ErrDeriveID, modulePath, err)
	}
	prefix, _, ok := module.SplitPathVersion(modulePath)
	if !ok {
		return "", fmt.Errorf("%w: invalid semantic import version in %q", ErrDeriveID, modulePath)
	}
	components := strings.Split(prefix, "/")
	if len(components) < 2 {
		return "", fmt.Errorf("%w: module path %q has no namespace below its host", ErrDeriveID, modulePath)
	}
	id := strings.Join(components[1:], ".") + "." + name
	if err := pluginid.Validate(id); err != nil {
		return "", fmt.Errorf("%w: module path %q produces %q: %v", ErrDeriveID, modulePath, id, err)
	}
	return id, nil
}
