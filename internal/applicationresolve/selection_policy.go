package applicationresolve

import (
	"context"
	"fmt"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/interfaceinventory"
)

// ValidateExecutableCandidate checks final static Interface ownership,
// configuration and policies without executing legacy generation helpers.
// Incomplete repair projections must still use structural validation; this
// method does not establish legacy-only policy or exact-plan acceptance.
func (s SelectionInputs) ValidateExecutableCandidate(ctx context.Context, composition applicationmeta.Composition) error {
	if err := s.validateSnapshotContext(ctx); err != nil {
		return err
	}
	resolution, err := s.ResolveCandidateInterfaces(composition)
	if err != nil {
		return err
	}
	sources, err := s.candidateSourceContext(composition)
	if err != nil {
		return err
	}
	if err := s.validateCandidateConfiguration(composition, resolution, sources); err != nil {
		return err
	}
	started := false
	options := Options{
		GoCommand: s.dependencyOptions.GoCommand, Environment: s.dependencyOptions.Environment,
		DependencyOutputLimit: s.dependencyOptions.OutputLimit, Offline: s.dependencyOptions.Offline,
		ObservePolicySupport: func(event interfaceinventory.PackageLoadEvent) {
			started = started || event.GoStarted
			if s.observePolicySupport != nil {
				s.observePolicySupport(event)
			}
		},
	}
	err = validateExecutablePolicies(ctx, s.module, s.dependencies, options, composition.Manifest(), resolution, generation.Context{}, sources)
	if started {
		// Compiler work can race the captured declarations and selection inputs.
		// This recheck is not complete retention of compiler-export source bytes.
		if snapshotErr := s.ValidateSnapshot(ctx); snapshotErr != nil {
			return snapshotErr
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return nil
}
