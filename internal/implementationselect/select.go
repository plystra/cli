// Package implementationselect records one explicit current-Project
// Implementation choice and regenerates the complete application through the
// selected configuration.
package implementationselect

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
)

var (
	// ErrSelect reports a failed Implementation-selection and regeneration
	// transaction.
	ErrSelect = errors.New("select Interface Implementation")
	// ErrInvalidInterfaceID reports a malformed Interface identity supplied to
	// the public selection workflow.
	ErrInvalidInterfaceID = errors.New("invalid Interface selection ID")
	// ErrInvalidConstructor reports a malformed fully qualified constructor
	// symbol supplied to the public selection workflow.
	ErrInvalidConstructor = errors.New("invalid Implementation selection constructor")
	// ErrConfigurationWrite reports that the selected current-Project document
	// could not safely produce the planned Implementation-selection write.
	ErrConfigurationWrite = errors.New("prepare Interface Implementation selection")
)

// Options contains the Project location, selected configuration, and bounded
// generation settings for one complete Implementation-selection transaction.
type Options struct {
	Start                 string
	InterfaceID           string
	Constructor           string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the explicit Implementation choice and selected
// configuration committed by one successful transaction.
type Result struct {
	interfaceID  interfaceid.Identifier
	constructor  constructorsymbol.Symbol
	moduleRoot   string
	manifestPath string
	changed      bool
}

// InterfaceID returns the exact canonical Interface selected by the choice.
func (r Result) InterfaceID() interfaceid.Identifier { return r.interfaceID }

// Constructor returns the selected fully qualified Implementation constructor.
func (r Result) Constructor() constructorsymbol.Symbol { return r.constructor }

// ModuleRoot returns the canonical absolute Project Go Module root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// ManifestPath returns the canonical absolute selected configuration path.
func (r Result) ManifestPath() string { return r.manifestPath }

// Changed reports whether the transaction changed the selected configuration.
func (r Result) Changed() bool { return r.changed }

// SelectedConfigurationWrite plans one concurrency-protected replacement for
// the current-project document selected by the same rules as generation.
func SelectedConfigurationWrite(moduleRoot string, id interfaceid.Identifier, constructor constructorsymbol.Symbol, configurationPath, environmentName string, environment []string) (atomicfs.Write, bool, applicationresolve.ConfigurationSelection, error) {
	target, err := applicationresolve.SelectConfigurationTarget(moduleRoot, configurationPath, environmentName, environment)
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: select current-project configuration: %w", ErrConfigurationWrite, err)
	}
	snapshot := target.Snapshot()
	original := snapshot.Data()
	setChoice := applicationmeta.SetImplementationChoice
	if target.EnvironmentOverlay() {
		setChoice = applicationmeta.SetImplementationChoiceOverlay
	}
	updated, changed, err := setChoice(original, id, constructor)
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: %w", ErrConfigurationWrite, err)
	}
	if !changed {
		return atomicfs.Write{}, false, target.Selection(), nil
	}
	return atomicfs.Write{
		Path:         target.Selection().Path(),
		Data:         updated,
		ExpectedData: original,
	}, true, target.Selection(), nil
}

// Select writes one explicit current-Project Implementation replacement and
// regenerates, tidies, and validates the Project in one rollback boundary.
func Select(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrSelect)
	}
	id, err := interfaceid.Parse(options.InterfaceID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: parse exact Interface ID: %w", ErrSelect, ErrInvalidInterfaceID, err)
	}
	constructor, err := constructorsymbol.Parse(options.Constructor)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w: parse fully qualified Implementation constructor: %w", ErrSelect, ErrInvalidConstructor, err)
	}
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrSelect, err)
	}
	write, changed, selection, err := SelectedConfigurationWrite(module.Path(), id, constructor, options.ConfigurationPath, options.EnvironmentName, options.Environment)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	writes := make([]atomicfs.Write, 0, 1)
	if changed {
		writes = append(writes, write)
	}
	if err := atomicfs.WriteFiles(module.Path(), writes, func(updatedRoot string) error {
		return modulemutation.Tidy(ctx, updatedRoot, options.GoCommand, options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
			_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:                 updatedRoot,
				ConfigurationPath:     options.ConfigurationPath,
				EnvironmentName:       options.EnvironmentName,
				GoCommand:             options.GoCommand,
				Environment:           options.Environment,
				DependencyOutputLimit: options.DependencyOutputLimit,
				Validate:              options.Validate,
				MutateModule:          mutate,
				RejectUnexpected:      true,
			})
			return err
		})
	}); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSelect, err)
	}
	return Result{
		interfaceID:  id,
		constructor:  constructor,
		moduleRoot:   module.Path(),
		manifestPath: filepath.Join(module.Path(), filepath.FromSlash(selection.Path())),
		changed:      changed,
	}, nil
}
