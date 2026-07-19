// Package dependencyupdate updates one selected ordinary Go Module dependency
// in a Plystra Project and closes the resulting configuration and generation
// transaction.
package dependencyupdate

import (
	"context"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
)

// ErrUpdate reports failure to update and validate one Go Module dependency.
var ErrUpdate = errors.New("update Plystra Project dependency")

// Options controls one targeted dependency-update transaction.
type Options struct {
	Start                 string
	Query                 string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the Project and ordinary Go Module query selected by a
// successful dependency-update transaction.
type Result struct {
	module modulelocate.Module
	query  string
}

// Module returns the mutated Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Query returns the validated ordinary Go Module query supplied by the user.
func (r Result) Query() string { return r.query }

// Update resolves one already-selected ordinary Go Module query with go get,
// recomposes root dependency configuration, regenerates, tidies, validates,
// and commits only when the complete Project is consistent. It updates exactly
// one selected module rather than implicitly upgrading the complete graph.
func Update(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrUpdate)
	}
	query, modulePath, err := moduleargument.ParseQuery(options.Query)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUpdate, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrUpdate, err)
	}
	before, selected, err := modulemutation.FindRequirement(project.Path(), modulePath)
	if err != nil {
		return Result{}, fmt.Errorf("%w: inspect selected dependency: %w", ErrUpdate, err)
	}
	if !selected {
		return Result{}, fmt.Errorf("%w: Go Module %q is not selected in go.mod; use plystra add", ErrUpdate, modulePath)
	}
	directRequirements := []string(nil)
	if !before.Indirect() {
		directRequirements = []string{modulePath}
	}

	err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
		GoCommand:          options.GoCommand,
		Environment:        options.Environment,
		Arguments:          []string{"get", query},
		DirectRequirements: directRequirements,
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
					updated, selected, err := modulemutation.FindRequirement(root, modulePath)
					if err != nil {
						return fmt.Errorf("confirm updated dependency: %w", err)
					}
					if !selected {
						return fmt.Errorf("Go Module %q is no longer selected after regeneration and tidy", modulePath)
					}
					if !before.Indirect() && updated.Indirect() {
						return fmt.Errorf("Go Module %q became indirect after regeneration and tidy", modulePath)
					}
					return nil
				})
			},
			RejectUnexpected: true,
		})
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUpdate, err)
	}
	return Result{module: project, query: query}, nil
}
