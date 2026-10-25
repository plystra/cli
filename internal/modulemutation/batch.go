package modulemutation

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/moduleargument"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const (
	maximumDependencyChanges     = 1_024
	maximumDependencyModuleBytes = 4_096
)

// DependencyChange names one explicit add, remove, or update. Module is a Go
// module query for add/update and an exact module path for remove.
type DependencyChange struct {
	Action string
	Module string
}

// DependencyBatchOptions controls one jointly resolved dependency transaction.
type DependencyBatchOptions struct {
	GoCommand   string
	Environment []string
	Changes     []DependencyChange
	Observe     func(started bool)
	// Prepare captures exact canonical-version targets before the initial get
	// and any generated-runtime get. Mutable queries retain ordinary Go semantics.
	Prepare func([]module.Version) error
}

type dependencyTarget struct {
	path, query, version string
	remove               bool
	direct               bool
}

// ChangeDependencies resolves one bounded, order-independent dependency batch
// with a single go get before operation examines the complete candidate. Remove
// and update preconditions use one original go.mod snapshot. The supplied
// generation mutation preserves post-selection metadata through tidy and checks
// final dependency obligations inside generation validation. Resolved target
// versions cannot silently change when generated runtime requirements are added.
// Only module metadata is rolled back here; the caller owns its enclosing
// Project stage or file transaction.
func ChangeDependencies(ctx context.Context, root string, options DependencyBatchOptions, operation func(applicationgenerate.ModuleMutation) error) error {
	if ctx == nil || operation == nil || len(options.Changes) == 0 || len(options.Changes) > maximumDependencyChanges {
		return ErrChange
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrChange, err)
	}
	before, err := captureModuleFiles(root)
	if err != nil {
		return errors.Join(ErrChange, err)
	}
	modulePath, selected, err := parseDependencySelections(before)
	if err != nil {
		return errors.Join(ErrChange, err)
	}
	targets := make([]dependencyTarget, 0, len(options.Changes))
	seen := make(map[string]bool, len(options.Changes))
	for _, change := range options.Changes {
		if len(change.Module) > maximumDependencyModuleBytes || !utf8.ValidString(change.Module) {
			return ErrChange
		}
		var target dependencyTarget
		switch change.Action {
		case "add", "update":
			target.query, target.path, err = moduleargument.ParseQuery(change.Module)
		case "remove":
			target.path, err = moduleargument.ParsePath(change.Module)
			target.query, target.remove = target.path+"@none", true
		default:
			return ErrChange
		}
		if err != nil || target.path == modulePath || seen[target.path] {
			return errors.Join(ErrChange, err)
		}
		seen[target.path] = true
		before, exists := selected[target.path]
		if change.Action != "add" && !exists {
			return ErrChange
		}
		target.direct = !target.remove && (change.Action == "add" || !before.Indirect())
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].path < targets[j].path })
	if options.Prepare != nil {
		var versions []module.Version
		for _, target := range targets {
			_, version, explicit := strings.Cut(target.query, "@")
			if !target.remove && explicit && version != "" && module.CanonicalVersion(version) == version && module.Check(target.path, version) == nil {
				versions = append(versions, module.Version{Path: target.path, Version: version})
			}
		}
		if err := options.Prepare(versions); err != nil {
			return errors.Join(ErrChange, err)
		}
		current, err := captureModuleFiles(root)
		if err != nil {
			return errors.Join(ErrChange, err)
		}
		for name, original := range before {
			observed := current[name]
			if original.exists != observed.exists || original.mode != observed.mode || !bytes.Equal(original.data, observed.data) {
				return ErrChange
			}
		}
	}
	arguments := []string{"get"}
	var direct []string
	for _, target := range targets {
		arguments = append(arguments, target.query)
		if target.direct {
			direct = append(direct, target.path)
		}
	}
	confirm := func() error {
		_, final, err := dependencySelections(root)
		if err != nil {
			return errors.Join(ErrChange, err)
		}
		for _, target := range targets {
			selection, exists := final[target.path]
			if target.remove && exists || !target.remove && (!exists || target.direct && selection.Indirect() || selection.Version() != target.version) {
				return ErrChange
			}
		}
		return nil
	}
	return Change(ctx, root, ChangeOptions{
		GoCommand: options.GoCommand, Environment: append([]string(nil), options.Environment...),
		Arguments: arguments, DirectRequirements: direct, Observe: options.Observe, PrepareRuntime: options.Prepare,
	}, func(mutate applicationgenerate.ModuleMutation) error {
		_, resolved, err := dependencySelections(root)
		if err != nil {
			return errors.Join(ErrChange, err)
		}
		for index := range targets {
			if !targets[index].remove {
				selection, exists := resolved[targets[index].path]
				if !exists {
					return ErrChange
				}
				targets[index].version = selection.Version()
			}
		}
		if err := operation(func(ctx context.Context, root string, requirements []applicationgenerate.ModuleRequirement, validate func() error) error {
			if validate == nil {
				return ErrChange
			}
			return mutate(ctx, root, requirements, func() error {
				if err := validate(); err != nil {
					return err
				}
				return confirm()
			})
		}); err != nil {
			return err
		}
		return confirm()
	})
}

func dependencySelections(root string) (string, map[string]Requirement, error) {
	files, err := captureModuleFiles(root)
	if err != nil {
		return "", nil, err
	}
	return parseDependencySelections(files)
}

func parseDependencySelections(files moduleFiles) (string, map[string]Requirement, error) {
	parsed, err := modfile.Parse("go.mod", files["go.mod"].data, nil)
	if err != nil || parsed.Module == nil {
		return "", nil, ErrChange
	}
	selected := make(map[string]Requirement, len(parsed.Require))
	for _, entry := range parsed.Require {
		if _, exists := selected[entry.Mod.Path]; exists {
			return "", nil, ErrChange
		}
		selected[entry.Mod.Path] = Requirement{version: entry.Mod.Version, indirect: entry.Indirect}
	}
	return parsed.Module.Mod.Path, selected, nil
}
