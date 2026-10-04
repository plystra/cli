// Package implementationselect records an explicit Interface or named Resource
// selection, cleans up proven ownership changes, and regenerates the application.
package implementationselect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/resourcename"
)

var (
	// ErrSelect reports a failed selection and regeneration
	// transaction.
	ErrSelect = errors.New("select Interface Implementation or Resource provider")
	// ErrInvalidTarget reports neither a canonical Interface ID nor an exact
	// Resource instance name.
	ErrInvalidTarget = errors.New("invalid selection target")
	// ErrTargetNotFound reports an absent Interface or named Resource target.
	ErrTargetNotFound = errors.New("selection target is not visible")
	// ErrProviderIncompatible reports a missing provider or a provider for a
	// different exact Resource contract.
	ErrProviderIncompatible = errors.New("selection constructor is not a compatible Resource provider")
	// ErrInvalidConstructor reports a malformed fully qualified constructor
	// symbol supplied to the public selection workflow.
	ErrInvalidConstructor = errors.New("invalid selection constructor")
	// ErrConfigurationWrite reports that the selected current-Project document
	// could not safely produce the planned selection and ownership cleanup.
	ErrConfigurationWrite = errors.New("prepare constructor selection")
)

// Options contains the Project location, selected configuration, and bounded
// generation settings for one complete selection transaction.
type Options struct {
	Start                 string
	Target                string
	Constructor           string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the explicit constructor choice and selected
// configuration committed by one successful transaction.
type Result struct {
	target       string
	kind         string
	constructor  constructorsymbol.Symbol
	moduleRoot   string
	manifestPath string
	changed      bool
}

// Target returns the exact canonical Interface ID or named Resource instance.
func (r Result) Target() string { return r.target }

// Kind returns interface or resource, inferred from the validated target.
func (r Result) Kind() string { return r.kind }

// Constructor returns the selected fully qualified constructor.
func (r Result) Constructor() constructorsymbol.Symbol { return r.constructor }

// ModuleRoot returns the canonical absolute Project Go Module root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// ManifestPath returns the canonical absolute selected configuration path.
func (r Result) ManifestPath() string { return r.manifestPath }

// Changed reports whether the transaction changed the selected configuration.
func (r Result) Changed() bool { return r.changed }

// Select writes one explicit current-Project selection with proven ownership
// cleanup, then regenerates, tidies, and validates in one rollback boundary.
func Select(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrSelect)
	}
	id, idErr := interfaceid.Parse(options.Target)
	if idErr != nil && resourcename.Check(options.Target) != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, ErrInvalidTarget)
	}
	constructor, err := constructorsymbol.Parse(options.Constructor)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: parse fully qualified Implementation constructor: %w", ErrSelect, ErrInvalidConstructor, err)
	}
	inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
		GoCommand: options.GoCommand, Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	updated, kind, err := planSelection(inputs, id, options.Target, constructor)
	if err != nil {
		var missing *interfaceresolution.UnknownInterfaceError
		if errors.As(err, &missing) && missing.InterfaceID() == id {
			err = fmt.Errorf("%w: %w", ErrTargetNotFound, err)
		}
		return Result{}, fmt.Errorf("%w: %w: %w", ErrSelect, ErrConfigurationWrite, err)
	}
	module := inputs.Module()
	selection := inputs.ConfigurationSelection()
	original := inputs.SelectedSnapshot().Data()
	changed := !bytes.Equal(original, updated)
	writes := make([]atomicfs.Write, 0, 1)
	if changed {
		writes = append(writes, atomicfs.Write{Path: selection.Path(), Data: updated, ExpectedData: original})
	}
	if err := inputs.ValidateSnapshot(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	if err := atomicfs.WriteFiles(module.Path(), writes, func(updatedRoot string) error {
		return modulemutation.Tidy(ctx, updatedRoot, options.GoCommand, options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
			guardedMutation := func(ctx context.Context, root string, requirements []applicationgenerate.ModuleRequirement, validate func() error) error {
				return mutate(ctx, root, requirements, func() error {
					tidy, err := inputs.CaptureTidySnapshot(ctx)
					if err != nil {
						return err
					}
					if err := validate(); err != nil {
						return err
					}
					// Generation takes fresh snapshots; cleanup must still be bound
					// to its original ownership evidence before rollback closes.
					return inputs.ValidatePostwriteSnapshot(ctx, updated, tidy)
				})
			}
			_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:                 updatedRoot,
				ConfigurationPath:     options.ConfigurationPath,
				EnvironmentName:       options.EnvironmentName,
				GoCommand:             options.GoCommand,
				Environment:           options.Environment,
				DependencyOutputLimit: options.DependencyOutputLimit,
				Validate:              options.Validate,
				MutateModule:          guardedMutation,
				RejectUnexpected:      true,
			})
			return err
		})
	}); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return Result{
		target:       options.Target,
		kind:         kind,
		constructor:  constructor,
		moduleRoot:   module.Path(),
		manifestPath: filepath.Join(module.Path(), filepath.FromSlash(selection.Path())),
		changed:      changed,
	}, nil
}
