// Package dependencycleanup applies ownership-derived edits within dependency
// primitives using the same captured old/final planner as staged previews.
package dependencycleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/implementationselect"
)

// Options controls discovery for one dependency mutation.
type Options struct {
	Start                 string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
}

// Result contains the post-mutation inputs and selected document postimage.
type Result struct {
	Inputs  applicationresolve.SelectionInputs
	Data    []byte
	Changed bool
	impact  configurationimpact.Impact
	before  Snapshot
}

// ConfigurationImpact contains ownership consequences without configured values.
func (r Result) ConfigurationImpact() configurationimpact.Impact { return r.impact }

// Plan discovers the final graph using the captured selector and authored
// preimage. Only old declarations or literal authored ownership prove cleanup.
func Plan(ctx context.Context, before Snapshot, action changerequest.DependencyAction, target string) (Result, error) {
	if err := before.validate(ctx, nil); err != nil {
		return Result{}, err
	}
	if err := before.validateChange(); err != nil {
		return Result{}, err
	}
	options := before.options
	inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
		GoCommand: options.GoCommand, Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("discover post-change ownership: %w", err)
	}
	if err := before.validateDependencies(inputs); err != nil {
		return Result{}, err
	}
	data, err := json.Marshal(map[string]any{"schema": changerequest.SchemaV1, "operations": []any{
		map[string]any{"kind": "dependency", "action": action, "module": target},
	}})
	if err != nil {
		return Result{}, err
	}
	request, err := changerequest.Parse(data)
	if err != nil {
		return Result{}, err
	}
	plan, err := implementationselect.PreviewDependencyConfigurations(ctx, before.inputs, inputs, request)
	if err != nil {
		return Result{}, fmt.Errorf("plan dependency ownership: %w", err)
	}
	if err := before.validate(ctx, nil); err != nil {
		return Result{}, err
	}
	if err := before.validateChange(); err != nil {
		return Result{}, err
	}
	return Result{Inputs: inputs, Data: plan.Updated(), Changed: plan.Changed(), impact: plan.ConfigurationImpact(), before: before}, nil
}

// Commit installs the selected document while generation/module rollback stays
// open. Even an unchanged document requires the original snapshot checks.
func Commit(ctx context.Context, root string, plan Result, operation func(string) error) error {
	if operation == nil {
		return errors.New("dependency cleanup operation is nil")
	}
	if err := plan.before.validate(ctx, nil); err != nil {
		return err
	}
	if err := plan.before.validateChange(); err != nil {
		return err
	}
	if err := plan.Inputs.ValidateSnapshot(ctx); err != nil {
		return err
	}
	var writes []atomicfs.Write
	if plan.Changed {
		writes = []atomicfs.Write{{Path: plan.Inputs.SelectedSnapshot().Path(), Data: plan.Data, ExpectedData: plan.Inputs.SelectedSnapshot().Data()}}
	}
	return atomicfs.WriteFiles(root, writes, operation)
}

// GuardMutation binds generation validation to post-change discovery and old
// authored evidence. CaptureTidySnapshot sees only the new graph; it never
// authorizes the preceding intentional dependency version change.
func (r Result) GuardMutation(mutate applicationgenerate.ModuleMutation) applicationgenerate.ModuleMutation {
	return func(ctx context.Context, root string, requirements []applicationgenerate.ModuleRequirement, validate func() error) error {
		if err := r.before.validate(ctx, r.Data); err != nil {
			return err
		}
		if err := r.before.validateChange(); err != nil {
			return err
		}
		return mutate(ctx, root, requirements, func() error {
			tidy, err := r.Inputs.CaptureTidySnapshot(ctx)
			if err != nil {
				return err
			}
			if err := r.before.validate(ctx, r.Data); err != nil {
				return err
			}
			if err := validate(); err != nil {
				return err
			}
			if err := r.before.validate(ctx, r.Data); err != nil {
				return err
			}
			return r.Inputs.ValidatePostwriteSnapshot(ctx, r.Data, tidy)
		})
	}
}
