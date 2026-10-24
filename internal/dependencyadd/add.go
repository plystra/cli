// Package dependencyadd adds one ordinary Go Module dependency to a Plystra
// Project and closes the resulting configuration and generation transaction.
package dependencyadd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/dependencycleanup"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
)

// ErrAdd reports failure to add and validate one Go Module dependency.
var ErrAdd = errors.New("add Plystra Project dependency")

// Options controls one dependency-add transaction.
type Options struct {
	Start                 string
	Query                 string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the Project and ordinary Go Module query selected by a
// successful dependency-add transaction.
type Result struct {
	module modulelocate.Module
	query  string
}

// Module returns the mutated Plystra Project Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Query returns the validated ordinary Go Module query supplied by the user.
func (r Result) Query() string { return r.query }

// Add resolves one ordinary Go Module query with go get, recomputes visible
// declarations and current-Project configuration, regenerates, tidies, validates, and
// commits only when the complete Project is consistent. Module metadata and
// every nested generation-owned change roll back on failure.
func Add(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrAdd)
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	options.Environment = append([]string(nil), options.Environment...)
	query, modulePath, err := moduleargument.ParseQuery(options.Query)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrAdd, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrAdd, err)
	}
	ownership, err := dependencycleanup.Capture(ctx, dependencycleanup.Options{
		Start: project.Path(), GoCommand: options.GoCommand,
		Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: capture pre-change ownership: %w", ErrAdd, err)
	}
	if err := ownership.Validate(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrAdd, err)
	}

	err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
		GoCommand:          options.GoCommand,
		Environment:        options.Environment,
		Arguments:          []string{"get", query},
		DirectRequirements: []string{modulePath},
		Observe:            ownership.ObserveChange,
	}, func(mutate applicationgenerate.ModuleMutation) error {
		plan, err := dependencycleanup.Plan(ctx, ownership, changerequest.DependencyAdd, query)
		if err != nil {
			return err
		}
		return dependencycleanup.Commit(ctx, project.Path(), plan, func(root string) error {
			_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:                 root,
				GoCommand:             options.GoCommand,
				Environment:           options.Environment,
				DependencyOutputLimit: options.DependencyOutputLimit,
				Validate:              options.Validate,
				MutateModule:          plan.GuardMutation(mutate),
				RejectUnexpected:      true,
			})
			return err
		})
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrAdd, err)
	}
	return Result{module: project, query: query}, nil
}
