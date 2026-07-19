// Package dependencyremove removes one ordinary Go Module dependency from a
// Plystra Project and closes the resulting configuration and generation
// transaction.
package dependencyremove

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// ErrRemove reports failure to remove and validate one Go Module dependency.
var ErrRemove = errors.New("remove Plystra Project dependency")

// Options controls one dependency-removal transaction.
type Options struct {
	Start                 string
	ModulePath            string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the Project and ordinary Go Module path removed by a
// successful dependency transaction.
type Result struct {
	module     modulelocate.Module
	modulePath string
}

// Module returns the mutated Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// ModulePath returns the validated ordinary Go Module path supplied by the
// user.
func (r Result) ModulePath() string { return r.modulePath }

// Remove removes one selected ordinary Go Module with go get <path>@none,
// recomposes root dependency configuration, regenerates, tidies, validates,
// and commits only when the complete Project is consistent. Module metadata
// and every nested generation-owned change roll back on failure.
func Remove(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrRemove)
	}
	modulePath, err := validateModulePath(options.ModulePath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrRemove, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrRemove, err)
	}
	selected, err := requirementSelected(project.Path(), modulePath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: inspect selected dependency: %w", ErrRemove, err)
	}
	if !selected {
		return Result{}, fmt.Errorf("%w: Go Module %q is not selected in go.mod", ErrRemove, modulePath)
	}

	err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
		GoCommand:   options.GoCommand,
		Environment: options.Environment,
		Arguments:   []string{"get", modulePath + "@none"},
	}, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:                 project.Path(),
			GoCommand:             options.GoCommand,
			Environment:           options.Environment,
			DependencyOutputLimit: options.DependencyOutputLimit,
			Validate:              options.Validate,
			MutateModule: func(ctx context.Context, root string, validate func() error) error {
				return mutate(ctx, root, func() error {
					if err := validate(); err != nil {
						return err
					}
					selected, err := requirementSelected(root, modulePath)
					if err != nil {
						return fmt.Errorf("confirm removed dependency: %w", err)
					}
					if selected {
						return fmt.Errorf("Go Module %q remains selected after regeneration and tidy", modulePath)
					}
					return nil
				})
			},
			RejectUnexpected: true,
		})
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrRemove, err)
	}
	return Result{module: project, modulePath: modulePath}, nil
}

func validateModulePath(value string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		return "", errors.New("Go Module path is empty")
	}
	if path != value || strings.HasPrefix(path, "-") || strings.Contains(path, "@") || strings.IndexFunc(path, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", fmt.Errorf("Go Module path %q is invalid; provide a path without a version query", value)
	}
	if err := module.CheckPath(path); err != nil {
		return "", fmt.Errorf("Go Module path %q: %w", path, err)
	}
	return path, nil
}

func requirementSelected(rootPath, modulePath string) (selected bool, resultErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := root.Close(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	info, err := root.Lstat("go.mod")
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return false, errors.New("go.mod is not a regular non-symbolic file")
	}
	data, err := root.ReadFile("go.mod")
	if err != nil {
		return false, err
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false, err
	}
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == modulePath {
			return true, nil
		}
	}
	return false, nil
}
