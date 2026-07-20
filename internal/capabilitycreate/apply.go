package capabilitycreate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/modulemutation"
)

var (
	// ErrCreate reports failure to create and regenerate one new Capability.
	ErrCreate = errors.New("create capability")
	// ErrImplement reports failure to implement and regenerate one existing
	// visible Capability.
	ErrImplement = errors.New("implement capability")
	// ErrActionMismatch reports a create request for an existing exact contract
	// or an implement request for a contract that is not visible.
	ErrActionMismatch = errors.New("capability authoring action does not match visible contracts")
	// ErrConfirmationRequired reports an explicit older, skipped, or otherwise
	// unusual new version that was not confirmed by the caller.
	ErrConfirmationRequired = errors.New("capability version requires confirmation")
	// ErrIntentProfile reports a missing or inapplicable explicit business
	// intent profile for Capability creation.
	ErrIntentProfile = errors.New("capability creation intent profile is invalid")
)

// AuthorOptions contains planning inputs and the bounded hooks used by one
// complete Capability authoring transaction. Confirm accepts an unusual new
// version. Expose adds the authored exact Capability to a runnable
// application's HTTP surface in the same transaction. Validate overrides
// generated-module validation in tests and specialized embedding; ordinary
// callers leave it nil.
type AuthorOptions struct {
	Options
	Confirm  bool
	Expose   bool
	Validate applicationgenerate.Validator
}

// Result identifies the exact Capability and local Plugin committed by one
// successful authoring transaction.
type Result struct {
	capability            capabilityid.Identifier
	pluginID              string
	pluginPath            string
	moduleRoot            string
	capabilityPath        string
	recommendations       []capabilityid.Identifier
	declarationCreated    bool
	implementationCreated bool
}

// Capability returns the exact authored Capability ID.
func (r Result) Capability() capabilityid.Identifier { return r.capability }

// PluginID returns the selected local Plugin ID.
func (r Result) PluginID() string { return r.pluginID }

// PluginPath returns the canonical absolute selected Plugin directory.
func (r Result) PluginPath() string { return r.pluginPath }

// ModuleRoot returns the canonical absolute Go Module containing the selected
// local Plugin.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// ApplicationManifestPath returns the root plystra.yaml path. It is useful
// when authoring was requested with HTTP exposure, which requires a runnable
// application module.
func (r Result) ApplicationManifestPath() string { return filepath.Join(r.moduleRoot, "plystra.yaml") }

// CapabilityPath returns the canonical absolute capability.yaml path.
func (r Result) CapabilityPath() string { return r.capabilityPath }

// Recommendations returns exact visible Capabilities with conservative
// typo-like names that the developer may choose to review. They are advisory
// and never redirect the authored Capability.
func (r Result) Recommendations() []capabilityid.Identifier {
	return append([]capabilityid.Identifier(nil), r.recommendations...)
}

// DeclarationCreated reports whether the transaction copied or created the
// target schema and added its provider declaration.
func (r Result) DeclarationCreated() bool { return r.declarationCreated }

// ImplementationCreated reports whether the transaction added a user-owned
// provider method scaffold. Existing implementations are never overwritten.
func (r Result) ImplementationCreated() bool { return r.implementationCreated }

// Create creates a new exact Capability version in one selected local Plugin.
// An unversioned reference chooses v1 or one above the highest visible version.
func Create(ctx context.Context, options AuthorOptions) (Result, error) {
	result, err := author(ctx, options, capabilityversion.ActionCreate)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return result, nil
}

// Implement copies one existing exact visible Capability into a selected
// local Plugin when necessary and adds a non-overwriting provider scaffold.
func Implement(ctx context.Context, options AuthorOptions) (Result, error) {
	result, err := author(ctx, options, capabilityversion.ActionImplement)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrImplement, err)
	}
	return result, nil
}

func author(ctx context.Context, options AuthorOptions, expected capabilityversion.Action) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("context is nil")
	}
	plan, err := PrepareVisible(ctx, options.Options)
	if err != nil {
		return Result{}, err
	}
	version := plan.Version()
	identifier := version.Target()
	target := plan.Target()
	root := target.ModuleRoot()
	if version.Action() != expected {
		switch expected {
		case capabilityversion.ActionCreate:
			return Result{}, fmt.Errorf("%w: %s is already visible; implement the existing exact contract instead", ErrActionMismatch, version.Target())
		case capabilityversion.ActionImplement:
			return Result{}, fmt.Errorf("%w: %s is not visible; create a new contract instead", ErrActionMismatch, version.Target())
		default:
			return Result{}, fmt.Errorf("%w: unsupported requested action %q", ErrActionMismatch, expected)
		}
	}
	if expected == capabilityversion.ActionCreate && version.RequiresConfirmation() && !options.Confirm {
		highest, hasHighest := version.HighestVisible()
		if hasHighest {
			return Result{}, fmt.Errorf("%w: %s is %s relative to highest visible %s", ErrConfirmationRequired, version.Target(), version.Caution(), highest)
		}
		return Result{}, fmt.Errorf("%w: %s is %s without visible version history", ErrConfirmationRequired, version.Target(), version.Caution())
	}
	if expected == capabilityversion.ActionCreate {
		_, hasSource := version.Source()
		switch {
		case !hasSource && plan.Intent() == "":
			return Result{}, fmt.Errorf("%w: %s is a new Capability identity; select one explicit profile such as --query", ErrIntentProfile, version.Target())
		case hasSource && plan.Intent() != "":
			return Result{}, fmt.Errorf("%w: %s copies semantics from %s; omit --%s", ErrIntentProfile, version.Target(), mustSource(version), plan.Intent())
		}
	}

	sources, err := ResolveSources(plan)
	if err != nil {
		return Result{}, err
	}
	manifestWrite, declarationCreated, err := RenderManifestWrite(plan)
	if err != nil {
		return Result{}, err
	}
	writes := make([]atomicfs.Write, 0, 4)
	if declarationCreated {
		schemaWrite, err := RenderSchemaWrite(plan, sources)
		if err != nil {
			return Result{}, err
		}
		writes = append(writes, schemaWrite, manifestWrite)
	} else if expected == capabilityversion.ActionCreate {
		return Result{}, fmt.Errorf("%w: target plugin %s already declares %s", ErrActionMismatch, plan.Target().ID(), version.Target())
	}
	implementationWrite, implementationCreated, err := RenderImplementationWrite(plan)
	if err != nil {
		return Result{}, err
	}
	if implementationCreated {
		writes = append(writes, implementationWrite)
	}
	if options.Expose {
		exposureWrite, changed, err := capabilityexpose.ManifestWrite(root, identifier)
		if err != nil {
			return Result{}, err
		}
		if changed {
			writes = append(writes, exposureWrite)
		}
	}

	if err := atomicfs.WriteFiles(root, writes, func(updatedRoot string) error {
		if err := validateDeclarations(updatedRoot, plan, sources); err != nil {
			return fmt.Errorf("validate authored declarations: %w", err)
		}
		return modulemutation.Tidy(ctx, updatedRoot, options.GoCommand, options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
			_, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:                 updatedRoot,
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
		return Result{}, err
	}

	capabilityPath := filepath.Join(
		target.Path(),
		"capabilities",
		filepath.FromSlash(identifier.Name()),
		"v"+strconv.FormatUint(identifier.Major(), 10),
		"capability.yaml",
	)
	return Result{
		capability:            identifier,
		pluginID:              target.ID(),
		pluginPath:            target.Path(),
		moduleRoot:            root,
		capabilityPath:        capabilityPath,
		recommendations:       plan.Recommendations(),
		declarationCreated:    declarationCreated,
		implementationCreated: implementationCreated,
	}, nil
}

func mustSource(version capabilityversion.Plan) capabilityid.Identifier {
	source, ok := version.Source()
	if !ok {
		panic("capability version source is absent")
	}
	return source
}
