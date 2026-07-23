// Package capabilityexpose adds exact canonical Capabilities to an
// application's explicit HTTP surface and regenerates the complete module.
package capabilityexpose

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
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
)

var (
	// ErrExpose reports a failed HTTP-exposure and regeneration transaction.
	ErrExpose = errors.New("expose capability")
	// ErrManifestWrite reports that plystra.yaml could not safely produce the
	// planned HTTP-exposure write.
	ErrManifestWrite = errors.New("prepare capability HTTP exposure")
)

// Options contains the application location and bounded generation settings
// for one complete exposure transaction. Validate overrides generated-module
// validation in tests and specialized embedding; ordinary callers leave it
// nil.
type Options struct {
	Start                 string
	Reference             string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	Validate              applicationgenerate.Validator
}

// Result identifies the exact exposure and application manifest committed by
// one successful transaction.
type Result struct {
	capability   capabilityid.Identifier
	moduleRoot   string
	manifestPath string
	changed      bool
}

// Capability returns the exact canonical Capability exposed over HTTP.
func (r Result) Capability() capabilityid.Identifier { return r.capability }

// ModuleRoot returns the canonical absolute application Go Module root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// ManifestPath returns the canonical absolute selected configuration path.
func (r Result) ManifestPath() string { return r.manifestPath }

// Changed reports whether the transaction added the exposure declaration.
// False means the exact Capability was already exposed and the module was
// still regenerated and validated.
func (r Result) Changed() bool { return r.changed }

// ManifestWrite plans one concurrency-protected plystra.yaml replacement.
// The zero write and false are returned when id is already exposed.
func ManifestWrite(moduleRoot string, id capabilityid.Identifier) (atomicfs.Write, bool, error) {
	write, changed, _, err := SelectedManifestWrite(moduleRoot, id, "", "", nil)
	return write, changed, err
}

// SelectedManifestWrite plans one concurrency-protected replacement for the
// current-project document selected by the same explicit and ambient rules as
// generation. The returned selection identifies the file even when no write
// is required.
func SelectedManifestWrite(moduleRoot string, id capabilityid.Identifier, configurationPath, environmentName string, environment []string) (atomicfs.Write, bool, applicationresolve.ConfigurationSelection, error) {
	target, err := applicationresolve.SelectConfigurationTarget(moduleRoot, configurationPath, environmentName, environment)
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: select current-project configuration: %w", ErrManifestWrite, err)
	}
	snapshot := target.Snapshot()
	original := snapshot.Data()
	exposureID, err := interfaceid.Parse(id.String())
	if err != nil {
		return atomicfs.Write{}, false, applicationresolve.ConfigurationSelection{}, fmt.Errorf("%w: adapt legacy Capability ID to Interface exposure: %w", ErrManifestWrite, err)
	}
	addExposure := applicationmeta.AddHTTPExposure
	if target.EnvironmentOverlay() {
		addExposure = applicationmeta.AddHTTPExposureOverlay
	}
	updated, changed, err := addExposure(original, exposureID)
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

// Expose adds one exact canonical Capability to http.expose and regenerates,
// tidies, and validates the Plystra Project in one rollback boundary.
func Expose(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrExpose)
	}
	id, err := capabilityid.Parse(options.Reference)
	if err != nil {
		return Result{}, fmt.Errorf("%w: parse exact Capability ID: %w", ErrExpose, err)
	}
	module, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrExpose, err)
	}
	write, changed, selection, err := SelectedManifestWrite(module.Path(), id, options.ConfigurationPath, options.EnvironmentName, options.Environment)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrExpose, err)
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
		return Result{}, fmt.Errorf("%w: %w", ErrExpose, err)
	}
	return Result{
		capability:   id,
		moduleRoot:   module.Path(),
		manifestPath: filepath.Join(module.Path(), filepath.FromSlash(selection.Path())),
		changed:      changed,
	}, nil
}
