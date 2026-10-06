// Package dependencycleanup applies only ownership-derived edits caused by a
// changed dependency declaration. It deliberately does not choose providers,
// remove Interface selections, or implement product-specific behavior.
package dependencycleanup

import (
	"context"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorsymbol"
)

// Options controls discovery for one dependency mutation's post-change graph.
type Options struct {
	Start                 string
	ConfigurationPath     string
	EnvironmentName       string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
}

// Result contains the post-mutation inputs and the selected document postimage.
type Result struct {
	Inputs  applicationresolve.SelectionInputs
	Data    []byte
	Changed bool
}

// Commit installs the planned selected-document postimage for the duration of
// operation. A failed operation restores the exact preimage and a concurrent
// edit is never overwritten.
func Commit(root string, plan Result, operation func(string) error) error {
	if operation == nil {
		return errors.New("dependency cleanup operation is nil")
	}
	if !plan.Changed {
		return operation(root)
	}
	return atomicfs.WriteFiles(root, []atomicfs.Write{{
		Path:         plan.Inputs.SelectedSnapshot().Path(),
		Data:         plan.Data,
		ExpectedData: plan.Inputs.SelectedSnapshot().Data(),
	}}, operation)
}

// Plan discovers the changed application and removes only Resource binding
// leaves whose consumer no longer declares that exact parameter. Lower-layer
// leaves become sparse tombstones; local leaves are removed. The caller owns
// the surrounding filesystem transaction.
func Plan(ctx context.Context, options Options) (Result, error) {
	inputs, err := applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
		Start:                 options.Start,
		ConfigurationPath:     options.ConfigurationPath,
		EnvironmentName:       options.EnvironmentName,
		GoCommand:             options.GoCommand,
		Environment:           options.Environment,
		DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Result{}, fmt.Errorf("discover post-change ownership: %w", err)
	}
	original := inputs.SelectedSnapshot().Data()
	composition, err := inputs.ComposeSelectionCandidate(original)
	if err != nil {
		return Result{}, fmt.Errorf("compose post-change ownership: %w", err)
	}
	removals, err := obsoleteBindings(inputs, composition.Manifest())
	if err != nil {
		return Result{}, fmt.Errorf("derive Resource binding cleanup: %w", err)
	}
	if len(removals) == 0 {
		return Result{Inputs: inputs, Data: original}, nil
	}

	edit := applicationmeta.CleanupSelectionOwnership
	if inputs.ConfigurationSelection().Mode() == "environment" {
		edit = applicationmeta.CleanupSelectionOwnershipOverlay
	}
	updated, _, err := edit(original, nil, removals)
	if err != nil {
		return Result{}, fmt.Errorf("remove local Resource bindings: %w", err)
	}
	lower, err := inputs.ComposeSelectionCandidate(updated)
	if err != nil {
		return Result{}, fmt.Errorf("compose Resource binding cleanup: %w", err)
	}
	for index := range removals {
		removals[index].Tombstone = hasBinding(lower.Manifest().ResourceBindings(), removals[index])
	}
	updated, changed, err := edit(original, nil, removals)
	if err != nil {
		return Result{}, fmt.Errorf("materialize Resource binding cleanup: %w", err)
	}
	return Result{Inputs: inputs, Data: updated, Changed: changed}, nil
}

func obsoleteBindings(inputs applicationresolve.SelectionInputs, manifest applicationmeta.Manifest) ([]applicationmeta.ResourceBindingRemoval, error) {
	var removals []applicationmeta.ResourceBindingRemoval
	for _, binding := range manifest.ResourceBindings() {
		parameters, known, err := resourceParameters(inputs, manifest, binding)
		if err != nil {
			return nil, err
		}
		if !known {
			continue
		}
		if _, exists := parameters[binding.ParameterName()]; !exists {
			removals = append(removals, applicationmeta.ResourceBindingRemoval{
				Namespace:     binding.Namespace(),
				Consumer:      binding.Consumer(),
				ParameterName: binding.ParameterName(),
			})
		}
	}
	return removals, nil
}

func resourceParameters(inputs applicationresolve.SelectionInputs, manifest applicationmeta.Manifest, binding applicationmeta.ResourceBinding) (map[string]string, bool, error) {
	if binding.Namespace() == "implementations" {
		constructor, err := constructorsymbol.Parse(binding.Consumer())
		if err != nil {
			return nil, false, err
		}
		if implementation, exists := inputs.Declarations().Implementations().BySymbol(constructor); exists {
			parameters := make(map[string]string, len(implementation.RequiredResources()))
			for _, dependency := range implementation.RequiredResources() {
				parameters[dependency.ParameterName()] = dependency.ID().String()
			}
			return parameters, true, nil
		}
		if provider, exists := inputs.Declarations().ResourceProviders().BySymbol(constructor); exists {
			parameters := make(map[string]string, len(provider.Dependencies()))
			for _, dependency := range provider.Dependencies() {
				parameters[dependency.ParameterName()] = dependency.ID()
			}
			return parameters, true, nil
		}
		return nil, false, nil
	}

	for _, instance := range manifest.ResourceInstances() {
		if instance.Name() != binding.Consumer() {
			continue
		}
		provider, exists := inputs.Declarations().ResourceProviders().BySymbol(instance.Provider())
		if !exists {
			return nil, false, nil
		}
		parameters := make(map[string]string, len(provider.Dependencies()))
		for _, dependency := range provider.Dependencies() {
			parameters[dependency.ParameterName()] = dependency.ID()
		}
		return parameters, true, nil
	}
	return nil, false, nil
}

func hasBinding(bindings []applicationmeta.ResourceBinding, removal applicationmeta.ResourceBindingRemoval) bool {
	for _, binding := range bindings {
		if binding.Namespace() == removal.Namespace && binding.Consumer() == removal.Consumer && binding.ParameterName() == removal.ParameterName {
			return true
		}
	}
	return false
}
