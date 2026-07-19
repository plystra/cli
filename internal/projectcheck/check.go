// Package projectcheck owns the reusable read-only Plystra Project validation
// performed by the public check command and qualified-template creation.
package projectcheck

import (
	"context"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
)

var (
	// ErrCheck reports failure to resolve, compare, or test one Plystra
	// Project through the read-only check workflow.
	ErrCheck = errors.New("check Plystra Project")
)

// Options contains the Project location, configuration selection, and Go
// command process boundary used by one check.
type Options struct {
	Start             string
	ConfigurationPath string
	EnvironmentName   string
	GoCommand         string
	Environment       []string
}

// Result identifies the checked Project and any read-only configuration or
// generated-output drift. Go tests run only when that state is current.
type Result struct {
	generation applicationgenerate.Result
}

// Module returns the nearest enclosing Go Module.
func (r Result) Module() modulelocate.Module { return r.generation.Module() }

// Report returns deterministic generated-output drift.
func (r Result) Report() generatedfiles.Report { return r.generation.Report() }

// ConfigurationChanged reports dependency-composition drift in the selected
// current-project document.
func (r Result) ConfigurationChanged() bool { return r.generation.ConfigurationChanged() }

// ConfigurationMaintenancePath returns the Project-relative document whose
// dependency-derived baseline is stale.
func (r Result) ConfigurationMaintenancePath() string {
	return r.generation.ConfigurationMaintenancePath()
}

// Clean reports whether Project configuration and generated output are
// current. A clean result has also passed read-only Go package tests.
func (r Result) Clean() bool {
	return !r.ConfigurationChanged() && r.Report().Clean()
}

// Check resolves and compares the selected application without mutation. A
// current Project then runs `go test -mod=readonly ./...` from its module root.
func Check(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrCheck)
	}
	generation, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
		Start:             options.Start,
		Check:             true,
		ConfigurationPath: options.ConfigurationPath,
		EnvironmentName:   options.EnvironmentName,
		GoCommand:         options.GoCommand,
		Environment:       options.Environment,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: compare selected application: %w", ErrCheck, err)
	}
	result := Result{generation: generation}
	if !result.Clean() {
		return result, nil
	}
	if err := gocommand.Run(ctx, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   result.Module().Path(),
		Environment: options.Environment,
	}, "test", "-mod=readonly", "./..."); err != nil {
		return Result{}, fmt.Errorf("%w: test Go packages: %w", ErrCheck, err)
	}
	return result, nil
}
