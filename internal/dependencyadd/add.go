// Package dependencyadd adds one ordinary Go Module dependency to a Plystra
// Project and closes the resulting configuration and generation transaction.
package dependencyadd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/module"
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

// Add resolves one ordinary Go Module query with go get, recomposes root
// dependency configuration, regenerates, tidies, validates, and commits only
// when the complete Project is consistent. Module metadata and every nested
// generation-owned change roll back on failure.
func Add(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrAdd)
	}
	query, modulePath, err := validateQuery(options.Query)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrAdd, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrAdd, err)
	}
	err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
		GoCommand:          options.GoCommand,
		Environment:        options.Environment,
		Arguments:          []string{"get", query},
		DirectRequirements: []string{modulePath},
	}, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:                 project.Path(),
			GoCommand:             options.GoCommand,
			Environment:           options.Environment,
			DependencyOutputLimit: options.DependencyOutputLimit,
			Validate:              options.Validate,
			MutateModule:          mutate,
			RejectUnexpected:      true,
		})
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrAdd, err)
	}
	return Result{module: project, query: query}, nil
}

func validateQuery(value string) (string, string, error) {
	query := strings.TrimSpace(value)
	if query == "" {
		return "", "", errors.New("Go Module query is empty")
	}
	if query != value || strings.HasPrefix(query, "-") || strings.IndexFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", "", fmt.Errorf("Go Module query %q is invalid", value)
	}
	path := query
	if separator := strings.LastIndexByte(query, '@'); separator >= 0 {
		path = query[:separator]
		version := query[separator+1:]
		if version == "" {
			return "", "", fmt.Errorf("Go Module query %q has an empty version query", query)
		}
		if version == "none" {
			return "", "", errors.New("Go Module query @none removes a dependency; use plystra remove")
		}
	}
	if err := module.CheckPath(path); err != nil {
		return "", "", fmt.Errorf("Go Module path %q: %w", path, err)
	}
	return query, path, nil
}
