package applicationresolve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/version"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
)

// DependencyOwnershipSnapshot preserves old authored evidence across an
// intentional current-module change, without validating against new schemas.
// It is a concurrency guard, not a candidate validator or mutation lock.
type DependencyOwnershipSnapshot struct {
	inputs    SelectionInputs
	known     bool
	sources   interfaceinventory.OwnershipSnapshot
	workspace dependencyGoSelection
	plugins   []selectionWorkspaceSnapshot
}

func (DependencyOwnershipSnapshot) String() string     { return "<private-dependency-ownership-snapshot>" }
func (s DependencyOwnershipSnapshot) GoString() string { return s.String() }
func (s DependencyOwnershipSnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(s.String()))
}
func (s DependencyOwnershipSnapshot) LogValue() slog.Value { return slog.StringValue(s.String()) }

type dependencyGoSelection struct{ GOWORK, GOVERSION, GOFLAGS, GOOS, GOARCH, CGO_ENABLED string }

// CaptureDependencyOwnership retains ordinary root/selector/module/workspace
// discovery even when old declarations cannot load. Only a complete successful
// old discovery supplies declaration ownership proof.
func CaptureDependencyOwnership(ctx context.Context, options Options) (DependencyOwnershipSnapshot, error) {
	if ctx == nil {
		return DependencyOwnershipSnapshot{}, ErrResolve
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	options.Environment = append([]string(nil), options.Environment...)
	inputs, oldErr := discoverSelectionInputs(ctx, options)
	if ctx.Err() != nil {
		return DependencyOwnershipSnapshot{}, ctx.Err()
	}
	if inputs.module.Path() == "" || errors.Is(oldErr, ErrConcurrentChange) || errors.Is(oldErr, moduledependency.ErrConcurrentChange) {
		return DependencyOwnershipSnapshot{}, oldErr
	}
	s := DependencyOwnershipSnapshot{inputs: inputs, known: oldErr == nil}
	selection, err := s.goSelection(ctx)
	if err != nil {
		return DependencyOwnershipSnapshot{}, err
	}
	s.workspace = selection
	// The Go tool may select a workspace through GOENV. Retain its exact file
	// in addition to ordinary explicit/automatic selection and absence checks.
	if selection.GOWORK != "" && selection.GOWORK != "off" {
		root, name := filepath.Dir(selection.GOWORK), filepath.Base(selection.GOWORK)
		snapshot, err := readManifestSnapshot(root, name)
		if err != nil {
			return DependencyOwnershipSnapshot{}, selectionWorkspaceError(inputs.module.ModulePath())
		}
		s.inputs.workspaceSnapshots = append(s.inputs.workspaceSnapshots, selectionWorkspaceSnapshot{root: root, snapshot: snapshot})
	}
	if s.known {
		environment := append([]string(nil), options.Environment...)
		if version.IsValid(selection.GOVERSION) {
			environment = dependencyEnvironment(environment, "GOTOOLCHAIN", selection.GOVERSION)
		}
		s.sources, err = inputs.declarations.CaptureOwnershipSources(ctx, inputs.module, inputs.dependencies, interfaceinventory.Options{GoCommand: options.GoCommand, Environment: environment, OutputLimit: options.DependencyOutputLimit, Offline: options.Offline})
		if err != nil {
			return DependencyOwnershipSnapshot{}, fmt.Errorf("%w: %w", ErrConcurrentChange, err)
		}
		for _, plugin := range inputs.inventory.Plugins() {
			snapshot, err := readManifestSnapshot(plugin.ModuleRoot(), filepath.Join(plugin.Path(), "plugin.yaml"))
			if err != nil {
				return DependencyOwnershipSnapshot{}, ErrConcurrentChange
			}
			s.plugins = append(s.plugins, selectionWorkspaceSnapshot{root: plugin.ModuleRoot(), snapshot: snapshot})
		}
	}
	if err := s.ValidateSnapshot(ctx); err != nil {
		return DependencyOwnershipSnapshot{}, err
	}
	return s, nil
}

// Previous returns zero when old declaration loading supplied no proof.
func (s DependencyOwnershipSnapshot) Previous() SelectionInputs {
	if !s.known {
		return SelectionInputs{}
	}
	return s.inputs
}

// ValidateSnapshot must precede the explicit module mutation. Unlike the
// post-change guard, it requires the exact old module metadata and graph.
func (s DependencyOwnershipSnapshot) ValidateSnapshot(ctx context.Context) error {
	if err := s.inputs.validateSnapshotContext(ctx); err != nil {
		return err
	}
	if err := s.inputs.recheckSnapshots(); err != nil {
		return err
	}
	if s.known {
		if err := s.inputs.ValidateSnapshot(ctx); err != nil {
			return err
		}
	} else {
		current, err := moduledependency.Discover(ctx, s.inputs.module, s.inputs.dependencyOptions)
		if err != nil || !sameModuleGraph(s.inputs.dependencies, current, false) {
			return errors.Join(ErrConcurrentChange, ctx.Err())
		}
	}
	return s.ValidateAuthoredSnapshot(ctx, nil)
}

// ValidateAuthoredSnapshot permits only a changed current go.mod and the exact
// expected selected-document postimage. Nil expectedSelected requires the old
// selected preimage. All old declaration/type inputs, other module metadata,
// root/selected path identities, plugins, and effective workspace stay bound.
func (s DependencyOwnershipSnapshot) ValidateAuthoredSnapshot(ctx context.Context, expectedSelected []byte) error {
	if err := s.inputs.validateSnapshotContext(ctx); err != nil {
		return err
	}
	if err := s.inputs.recheckWorkspace(); err != nil {
		return err
	}
	selection, err := s.goSelection(ctx)
	if err != nil {
		return errors.Join(ErrConcurrentChange, ctx.Err())
	}
	// An intentional dependency update may select a newer toolchain. Old file
	// membership still uses the captured toolchain; workspace/build selectors
	// themselves cannot change during this transaction.
	selection.GOVERSION = s.workspace.GOVERSION
	if selection != s.workspace {
		return selectionWorkspaceError(s.inputs.module.ModulePath())
	}
	if err := s.recheckAuthored(expectedSelected); err != nil {
		return err
	}
	if s.known {
		if err := s.sources.ValidateSnapshot(ctx); err != nil {
			return fmt.Errorf("%w: %w", ErrConcurrentChange, err)
		}
	}
	if err := s.inputs.recheckWorkspace(); err != nil {
		return err
	}
	if err := s.recheckAuthored(expectedSelected); err != nil {
		return err
	}
	return ctx.Err()
}

func (s DependencyOwnershipSnapshot) recheckAuthored(expectedSelected []byte) error {
	for _, before := range []ManifestSnapshot{s.inputs.rootSnapshot, s.inputs.selectedSnapshot} {
		after, err := readManifestSnapshot(s.inputs.module.Path(), before.path)
		if err != nil {
			return ErrConcurrentChange
		}
		if before.path == s.inputs.selectedSnapshot.path && expectedSelected != nil {
			if !samePostwritePath(before, after) || !bytes.Equal(after.data, expectedSelected) {
				return ErrConcurrentChange
			}
		} else if !sameManifestSnapshot(before, after) {
			return ErrConcurrentChange
		}
	}
	before := s.inputs.moduleMetadata[0]
	after, err := readModuleMetadata(before.modulePath, before.root, before.version, before.required)
	if err != nil || !samePostwritePath(before.snapshot, after.snapshot) {
		return ErrConcurrentChange
	}
	other := s.inputs
	other.moduleMetadata = other.moduleMetadata[1:]
	if err := other.recheckModuleMetadata(); err != nil {
		return err
	}
	for _, before := range s.plugins {
		after, err := readManifestSnapshot(before.root, before.snapshot.path)
		if err != nil || !sameManifestSnapshot(before.snapshot, after) {
			return ErrConcurrentChange
		}
	}
	return nil
}

// ValidateModuleSources permits new immutable cache roots for version changes,
// while rejecting replacement or workspace source changes accepted by a fresh
// post-change discovery. Removed modules retain their old file evidence.
func (s DependencyOwnershipSnapshot) ValidateModuleSources(current SelectionInputs) error {
	for _, old := range s.inputs.dependencies.Modules() {
		next, exists := current.dependencies.ByPath(old.Path())
		if !exists {
			continue
		}
		a, ar := old.Replacement()
		b, br := next.Replacement()
		if old.Workspace() || next.Workspace() || a.Local() || b.Local() || old.SelectedVersion() == next.SelectedVersion() {
			if old.Root() != next.Root() || old.Workspace() != next.Workspace() || old.Project() != next.Project() || ar != br || a != b {
				return ErrConcurrentChange
			}
		}
	}
	return nil
}

func (s DependencyOwnershipSnapshot) goSelection(ctx context.Context) (dependencyGoSelection, error) {
	options := s.inputs.dependencyOptions
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: s.inputs.module.Path(), Environment: options.Environment, OutputLimit: options.OutputLimit}, "env", "-json", "GOWORK", "GOVERSION", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED")
	if err != nil {
		return dependencyGoSelection{}, err
	}
	var result dependencyGoSelection
	if err := json.Unmarshal(output, &result); err != nil {
		return dependencyGoSelection{}, err
	}
	return result, nil
}

func dependencyEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, key) {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}
