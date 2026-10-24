package implementationcreate

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
)

// MaximumStageRequests bounds one atomic Implementation materialization batch.
const MaximumStageRequests = 1_024

// StageRequest names one exact visible Interface or Resource and an absent
// canonical Project-relative package, beginning with ./.
type StageRequest struct {
	ContractID string
	Package    string
}

// Materialize atomically writes a bounded batch of unfinished Implementations
// into a live private Project stage using its captured Go environment. All
// same-request Interfaces must already be materialized. Only contract packages
// are discovered and type checked; pending Implementation imports may be absent.
// Results are ordered by package path and identify unvalidated staged scaffolds.
// The caller must validate, generate, and build the complete application candidate.
func Materialize(ctx context.Context, stage *projectstage.Stage, requests []StageRequest) ([]Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrCreate)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	if len(requests) == 0 || len(requests) > MaximumStageRequests {
		return nil, fmt.Errorf("%w: expected between 1 and %d staged requests", ErrCreate, MaximumStageRequests)
	}
	requests = append([]StageRequest(nil), requests...)
	sort.Slice(requests, func(i, j int) bool { return requests[i].Package < requests[j].Package })
	if err := stage.Recheck(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		return nil, fmt.Errorf("%w: locate staged Project: %w", ErrCreate, err)
	}
	if project.Path() != stage.Root() {
		return nil, fmt.Errorf("%w: %w", ErrCreate, projectstage.ErrConcurrentChange)
	}
	targets := make([]targetPackage, len(requests))
	identifiers := make([]interfaceid.Identifier, len(requests))
	paths := make(map[string]struct{}, len(requests))
	for index, request := range requests {
		if len(request.ContractID) > 4_096 {
			return nil, fmt.Errorf("%w: %w: exact contract ID exceeds 4096 bytes", ErrCreate, ErrInvalidContract)
		}
		identifier, err := interfaceid.Parse(request.ContractID)
		if err != nil || identifier.String() != request.ContractID {
			return nil, fmt.Errorf("%w: %w: expected an exact canonical contract ID", ErrCreate, ErrInvalidContract)
		}
		if len(request.Package) > 1_024 {
			return nil, fmt.Errorf("%w: %w: package exceeds 1024 bytes", ErrCreate, ErrInvalidPackage)
		}
		target, err := deriveTarget(project, request.Package)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCreate, err)
		}
		key := stagedPackageKey(target.relative)
		if _, duplicate := paths[key]; duplicate {
			return nil, fmt.Errorf("%w: %w: duplicate staged target %s", ErrCreate, ErrInvalidPackage, target.relative)
		}
		paths[key] = struct{}{}
		if err := requireAbsentPackage(target); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCreate, err)
		}
		targets[index], identifiers[index] = target, identifier
	}
	for _, target := range targets {
		key := stagedPackageKey(target.relative)
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if _, overlaps := paths[parent]; overlaps {
				return nil, fmt.Errorf("%w: %w: overlapping staged targets %s and %s", ErrCreate, ErrInvalidPackage, parent, key)
			}
		}
	}
	environment := stage.Environment()
	dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{Environment: environment})
	if err != nil {
		return nil, fmt.Errorf("%w: discover staged modules: %w", ErrCreate, err)
	}
	contracts, err := interfaceinventory.DiscoverContracts(ctx, project, dependencies, interfaceinventory.Options{Environment: environment})
	if err != nil {
		return nil, fmt.Errorf("%w: validate staged contracts: %w", ErrCreate, err)
	}
	if err := interfaceinventory.ValidateUniqueIDs(contracts.Interfaces()); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	writes := make([]atomicfs.Write, len(requests))
	results := make([]Result, len(requests))
	for index, identifier := range identifiers {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCreate, err)
		}
		canonical, err := findContract(contracts, identifier.String())
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCreate, err)
		}
		target := targets[index]
		source, err := canonical.render(identifier, target)
		if err != nil {
			return nil, fmt.Errorf("%w: render source: %w", ErrCreate, err)
		}
		constructor, err := constructorsymbol.New(target.importPath, "New")
		if err != nil {
			return nil, fmt.Errorf("%w: derive constructor symbol: %w", ErrCreate, err)
		}
		writes[index] = atomicfs.Write{Path: target.sourcePath, Data: source, OwnerPrivate: true, MustNotExist: true, ParentMustNotExist: true}
		results[index] = Result{contractID: identifier, kind: canonical.kind, moduleRoot: project.Path(), packagePath: target.packagePath, importPath: target.importPath, sourcePath: target.sourcePath, constructor: constructor}
	}
	recheck := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := stage.Recheck(ctx); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := recheck(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	if err := atomicfs.WriteFiles(stage.Root(), writes, func(string) error { return recheck() }); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return results, nil
}

func stagedPackageKey(relative string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(relative)
	}
	return relative
}
