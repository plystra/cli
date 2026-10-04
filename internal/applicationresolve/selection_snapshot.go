package applicationresolve

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/moduledependency"
)

// ModuleMetadataSnapshot captures one module's source identity and private
// go.mod preimage. Ordinary pre-module dependencies may have no root go.mod;
// their Snapshot has nil Data, and validation also checks continued absence.
type ModuleMetadataSnapshot struct {
	modulePath string
	root       string
	version    string
	required   bool
	snapshot   ManifestSnapshot
}

func (ModuleMetadataSnapshot) String() string   { return "<private-module-metadata>" }
func (ModuleMetadataSnapshot) GoString() string { return "<private-module-metadata>" }
func (ModuleMetadataSnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private-module-metadata>"))
}
func (ModuleMetadataSnapshot) LogValue() slog.Value {
	return slog.StringValue("<private-module-metadata>")
}

func (s ModuleMetadataSnapshot) ModulePath() string         { return s.modulePath }
func (s ModuleMetadataSnapshot) Root() string               { return s.root }
func (s ModuleMetadataSnapshot) Version() string            { return s.version }
func (s ModuleMetadataSnapshot) Snapshot() ManifestSnapshot { return s.snapshot }

// ModuleMetadata returns the current module first, followed by dependencies in
// module-path order. Filesystem paths and snapshot bytes are private read-set
// evidence, never public model identity.
func (s SelectionInputs) ModuleMetadata() []ModuleMetadataSnapshot {
	return append([]ModuleMetadataSnapshot(nil), s.moduleMetadata...)
}

func readModuleMetadata(modulePath, root, version string, required bool) (ModuleMetadataSnapshot, error) {
	snapshot, err := readManifestSnapshot(root, "go.mod")
	if !required && errors.Is(err, fs.ErrNotExist) {
		info, rootErr := os.Lstat(root)
		_, fileErr := os.Lstat(filepath.Join(root, "go.mod"))
		if rootErr == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0 && errors.Is(fileErr, fs.ErrNotExist) {
			snapshot, err = ManifestSnapshot{path: "go.mod", root: info}, nil
		}
	}
	if err != nil {
		return ModuleMetadataSnapshot{}, newManifestSourceError(modulePath, "go.mod", "module-dependency", 0, 0,
			fmt.Errorf("%w: cannot capture module metadata", ErrManifest))
	}
	return ModuleMetadataSnapshot{modulePath: modulePath, root: root, version: version, required: required, snapshot: snapshot}, nil
}

// ValidateSnapshot rechecks the captured root, selected document, template
// documents, module metadata, effective Go Module graph, and rediscovered
// declaration semantics (including dependency parameters and Config schemas).
// It performs no writes or application execution. It is not a lock or a byte
// snapshot of function bodies/generated artifacts; the enclosing transaction
// must protect and validate its complete read/write set before committing.
func (s SelectionInputs) ValidateSnapshot(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrResolve)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if s.module.Path() == "" {
		return fmt.Errorf("%w: selection inputs are empty", ErrResolve)
	}
	if err := s.recheckSnapshots(); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	current, err := moduledependency.Discover(ctx, s.module, s.dependencyOptions)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: %w", ErrResolve, ctx.Err())
		}
		return fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(s.module.ModulePath(), "cannot revalidate effective module graph"))
	}
	if !sameModuleGraph(s.dependencies, current) {
		return fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(s.module.ModulePath(), "effective module graph changed"))
	}
	if err := s.recheckDeclarations(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	// Module discovery invokes Go tooling. Recheck after it as well so a change
	// during that operation cannot hide behind the first document comparison.
	if err := s.recheckSnapshots(); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return nil
}

func (s SelectionInputs) recheckSnapshots() error {
	for _, before := range []ManifestSnapshot{s.rootSnapshot, s.selectedSnapshot} {
		after, err := readManifestSnapshot(s.module.Path(), before.path)
		if err != nil || !sameManifestSnapshot(before, after) {
			return configurationSourceError(s.module.ModulePath(), before.path, 0, 0,
				fmt.Errorf("%w: configuration %s changed before resolution completed", ErrConcurrentChange, before.path))
		}
	}
	if err := recheckDependencyManifests(s.dependencySnapshots); err != nil {
		return err
	}
	for _, before := range s.moduleMetadata {
		after, err := readModuleMetadata(before.modulePath, before.root, before.version, before.required)
		if err != nil {
			return moduleSnapshotError(before.modulePath, "module metadata changed")
		}
		equal := sameManifestSnapshot(before.snapshot, after.snapshot)
		if before.snapshot.file == nil && after.snapshot.file == nil {
			equal = sameDirectory(before.snapshot.root, after.snapshot.root)
		}
		if !equal {
			return moduleSnapshotError(before.modulePath, "module metadata changed")
		}
	}
	return nil
}

func moduleSnapshotError(modulePath, reason string) error {
	return newManifestSourceError(modulePath, "go.mod", "module-dependency", 0, 0,
		fmt.Errorf("%w: %w: %s", ErrConcurrentChange, moduledependency.ErrConcurrentChange, reason))
}

func sameModuleGraph(left, right moduledependency.Index) bool {
	l, r := left.Modules(), right.Modules()
	if len(l) != len(r) {
		return false
	}
	for index, a := range l {
		b := r[index]
		aReplacement, aReplaced := a.Replacement()
		bReplacement, bReplaced := b.Replacement()
		if a.Path() != b.Path() || a.Root() != b.Root() || a.RequiredVersion() != b.RequiredVersion() || a.SelectedVersion() != b.SelectedVersion() ||
			a.Direct() != b.Direct() || a.Indirect() != b.Indirect() || a.Workspace() != b.Workspace() || a.Project() != b.Project() ||
			aReplaced != bReplaced || aReplacement != bReplacement {
			return false
		}
	}
	return true
}
