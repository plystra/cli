// Package providerselect records one explicit current-Project Provider choice
// and regenerates the complete application through the selected configuration.
package providerselect

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/projectlocate"
)

var (
	// ErrSelect reports a failed Provider-selection and regeneration
	// transaction.
	ErrSelect = errors.New("select Capability Provider")
	// ErrManifestWrite reports that the selected current-Project document could
	// not safely produce the planned Provider-selection write.
	ErrManifestWrite = errors.New("prepare Capability Provider selection")
)

// Options contains the Project location, selected configuration, and bounded
// generation settings for one complete Provider-selection transaction.
type Options struct {
	Start                 string
	Capability            string
	PluginID              string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the explicit Provider choice and selected configuration
// committed by one successful transaction.
type Result struct {
	capability   capabilityid.Identifier
	pluginID     string
	moduleRoot   string
	manifestPath string
	changed      bool
}

// Capability returns the exact canonical Capability selected by the choice.
func (r Result) Capability() capabilityid.Identifier { return r.capability }

// PluginID returns the selected canonical Plugin ID.
func (r Result) PluginID() string { return r.pluginID }

// ModuleRoot returns the canonical absolute Project Go Module root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// ManifestPath returns the canonical absolute selected configuration path.
func (r Result) ManifestPath() string { return r.manifestPath }

// Changed reports whether the transaction changed the selected configuration.
func (r Result) Changed() bool { return r.changed }

// SelectedManifestWrite plans one concurrency-protected replacement for the
// current-project document selected by the same rules as generation.
func SelectedManifestWrite(moduleRoot string, capability capabilityid.Identifier, pluginID, configurationPath, environmentName string, environment []string) (atomicfs.Write, bool, applicationresolve.ConfigurationSelection, error) {
	target, err := applicationresolve.SelectConfigurationTarget(moduleRoot, configurationPath, environmentName, environment)
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: select current-project configuration: %w", ErrManifestWrite, err)
	}
	snapshot := target.Snapshot()
	original := snapshot.Data()
	setChoice := applicationmeta.SetProviderChoice
	if target.EnvironmentOverlay() {
		setChoice = applicationmeta.SetProviderChoiceOverlay
	}
	updated, changed, err := setChoice(original, capability, pluginID)
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: %w", ErrManifestWrite, err)
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

// Select writes one explicit current-Project Provider replacement and
// regenerates, tidies, and validates the Project in one rollback boundary.
func Select(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrSelect)
	}
	capability, err := capabilityid.Parse(options.Capability)
	if err != nil {
		return Result{}, fmt.Errorf("%w: parse exact Capability ID: %w", ErrSelect, err)
	}
	if err := pluginid.Validate(options.PluginID); err != nil {
		return Result{}, fmt.Errorf("%w: parse Plugin ID %q: %w", ErrSelect, options.PluginID, err)
	}
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrSelect, err)
	}
	write, changed, selection, err := SelectedManifestWrite(module.Path(), capability, options.PluginID, options.ConfigurationPath, options.EnvironmentName, options.Environment)
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
		capability:   capability,
		pluginID:     options.PluginID,
		moduleRoot:   module.Path(),
		manifestPath: filepath.Join(module.Path(), filepath.FromSlash(selection.Path())),
		changed:      changed,
	}, nil
}
