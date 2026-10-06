package applicationresolve

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strconv"

	"github.com/plystra/cli/internal/moduledependency"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// SelectionTidySnapshot is private evidence of the exact module normalization
// observed immediately after modulemutation.Tidy, before generation validation.
// It cannot authorize a dependency edit or be reused for different inputs.
type SelectionTidySnapshot struct {
	originalModule       ManifestSnapshot
	originalDependencies moduledependency.Index
	dependencies         moduledependency.Index
	moduleMetadata       []ModuleMetadataSnapshot
}

func (SelectionTidySnapshot) String() string   { return "<private-selection-tidy-snapshot>" }
func (SelectionTidySnapshot) GoString() string { return "<private-selection-tidy-snapshot>" }
func (SelectionTidySnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private-selection-tidy-snapshot>"))
}
func (SelectionTidySnapshot) LogValue() slog.Value {
	return slog.StringValue("<private-selection-tidy-snapshot>")
}

// CaptureTidySnapshot must be called inside modulemutation.Tidy's supplied
// mutation callback, immediately after normalization and BEFORE invoking the
// generation validation/confirmation operation. It permits formatting/comments,
// indirect-to-direct promotion, materialized requirements from the original
// graph (including captured workspace members), and newly introduced
// ordinary modules. Existing requirements, other go.mod directives (allowing
// equivalent Go version spelling and removal of its exactly implied toolchain),
// preexisting selected sources/versions/replacements, and workspace inputs
// cannot change; new Project or workspace modules are rejected.
// It does not establish candidate validity or refresh cleanup declarations.
func (s SelectionInputs) CaptureTidySnapshot(ctx context.Context) (SelectionTidySnapshot, error) {
	if err := s.validateSnapshotContext(ctx); err != nil {
		return SelectionTidySnapshot{}, err
	}
	if err := s.recheckWorkspace(); err != nil {
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	before := s.moduleMetadata[0]
	after, err := readModuleMetadata(before.modulePath, before.root, before.version, before.required)
	if err != nil || !samePostwritePath(before.snapshot, after.snapshot) {
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(s.module.ModulePath(), "module metadata path changed"))
	}
	current, err := moduledependency.Discover(ctx, s.module, s.dependencyOptions)
	if err != nil || !sameModuleGraph(s.dependencies, current, true) || !permittedTidy(before.snapshot.data, after.snapshot.data, s.dependencies, current) {
		if ctx.Err() != nil {
			return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, ctx.Err())
		}
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(s.module.ModulePath(), "module metadata changed beyond permitted tidy normalization"))
	}
	metadata := append([]ModuleMetadataSnapshot(nil), s.moduleMetadata...)
	metadata[0] = after
	for _, dependency := range current.Modules() {
		if _, exists := s.dependencies.ByPath(dependency.Path()); exists {
			continue
		}
		added, err := readModuleMetadata(dependency.Path(), dependency.Root(), dependency.SelectedVersion(), false)
		if err != nil {
			return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(dependency.Path(), "cannot capture normalized module metadata"))
		}
		metadata = append(metadata, added)
	}
	result := SelectionTidySnapshot{
		originalModule: before.snapshot, originalDependencies: s.dependencies,
		dependencies: current, moduleMetadata: metadata,
	}
	s.moduleMetadata = metadata
	if err := s.recheckModuleMetadata(); err != nil {
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := s.recheckWorkspace(); err != nil {
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := ctx.Err(); err != nil {
		return SelectionTidySnapshot{}, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	return result, nil
}

// ValidatePostwriteSnapshot rechecks the original cleanup evidence after the
// selected document has been installed. Call it inside generation's module
// mutation callback, after validation/confirmation, before rollback closes.
// Only the exact expectedSelected bytes may replace the selected document;
// its path directories and file mode must remain unchanged.
//
// Module metadata and the graph must match the exact CaptureTidySnapshot
// captured before generation validation, not a freshly accepted current state.
// Preexisting dependency source roots, selected versions, replacements,
// workspace inputs, other documents, and declaration/ownership/Config-schema
// semantics remain bound to discovery. Upgrading an existing runtime dependency
// requires fresh planning.
//
// Like ValidateSnapshot this is read-only, not a lock. Function bodies,
// generated bytes, and go.sum normalization belong to enclosing validation,
// not this cleanup proof. It does not validate the candidate application or
// reset the original snapshot, and errors never include private postimages.
func (s SelectionInputs) ValidatePostwriteSnapshot(ctx context.Context, expectedSelected []byte, tidy SelectionTidySnapshot) error {
	if err := s.validateSnapshotContext(ctx); err != nil {
		return err
	}
	if len(tidy.moduleMetadata) == 0 || !sameManifestSnapshot(s.moduleMetadata[0].snapshot, tidy.originalModule) ||
		!sameModuleGraph(s.dependencies, tidy.originalDependencies, false) {
		return fmt.Errorf("%w: %w", ErrResolve, moduleSnapshotError(s.module.ModulePath(), "tidy snapshot does not belong to the original inputs"))
	}
	selected, err := readManifestSnapshot(s.module.Path(), s.selectedSnapshot.path)
	if err != nil || !samePostwritePath(s.selectedSnapshot, selected) || !bytes.Equal(selected.data, expectedSelected) {
		return fmt.Errorf("%w: %w", ErrResolve, configurationSourceError(s.module.ModulePath(), s.selectedSnapshot.path, 0, 0,
			fmt.Errorf("%w: selected configuration does not match the expected postimage", ErrConcurrentChange)))
	}
	// Pin the permitted postimages across rediscovery without replacing any of
	// the original declaration, dependency, or workspace evidence.
	if s.rootSnapshot.path == s.selectedSnapshot.path {
		s.rootSnapshot = selected
	}
	s.selectedSnapshot = selected
	s.moduleMetadata = tidy.moduleMetadata
	s.dependencies = tidy.dependencies
	return s.ValidateSnapshot(ctx)
}

func samePostwritePath(before, after ManifestSnapshot) bool {
	if before.path != after.path || !sameDirectory(before.root, after.root) ||
		before.file == nil || after.file == nil || before.file.Mode() != after.file.Mode() ||
		len(before.components) != len(after.components) {
		return false
	}
	for index := 0; index < len(before.components)-1; index++ {
		a, b := before.components[index], after.components[index]
		if a.name != b.name || !sameDirectory(a.info, b.info) {
			return false
		}
	}
	return true
}

func permittedTidy(before, after []byte, original, dependencies moduledependency.Index) bool {
	a, err := modfile.Parse("go.mod", before, nil)
	if err != nil {
		return false
	}
	b, err := modfile.Parse("go.mod", after, nil)
	if err != nil {
		return false
	}
	// Go removes a toolchain directive exactly implied by the go directive.
	// Omit only that old directive when it was removed, leaving additions,
	// replacements, and removal of a meaningful toolchain selection visible.
	if a.Go != nil && a.Toolchain != nil && b.Toolchain == nil && a.Toolchain.Name == "go"+a.Go.Version {
		a.DropToolchainStmt()
	}
	if !reflect.DeepEqual(nonRequirementDirectives(a), nonRequirementDirectives(b)) {
		return false
	}
	remaining := make(map[string]*modfile.Require, len(b.Require))
	for _, requirement := range b.Require {
		if _, exists := remaining[requirement.Mod.Path]; exists {
			return false
		}
		remaining[requirement.Mod.Path] = requirement
	}
	for _, old := range a.Require {
		current, exists := remaining[old.Mod.Path]
		if !exists || old.Mod != current.Mod || (!old.Indirect && current.Indirect) {
			return false
		}
		delete(remaining, old.Mod.Path)
	}
	for path, added := range remaining {
		selected, exists := dependencies.ByPath(path)
		if !exists {
			return false
		}
		if selected.Workspace() {
			// Workspace members have no selected version. The surrounding graph
			// and metadata checks still bind their original source and identity.
			previous, captured := original.ByPath(path)
			if !captured || !previous.Workspace() || previous.Direct() {
				return false
			}
			continue
		}
		if selected.SelectedVersion() == "" || selected.SelectedVersion() != added.Mod.Version {
			return false
		}
	}
	return true
}

// The parsed syntax covers every Go directive, including future supported
// directives, without treating comments or block formatting as semantics.
func nonRequirementDirectives(file *modfile.File) [][]string {
	var result [][]string
	for _, statement := range file.Syntax.Stmt {
		switch statement := statement.(type) {
		case *modfile.Line:
			if len(statement.Token) != 0 && statement.Token[0] != "require" {
				result = append(result, statement.Token)
			}
		case *modfile.LineBlock:
			if len(statement.Token) != 0 && statement.Token[0] != "require" {
				for _, line := range statement.Line {
					tokens := append([]string(nil), statement.Token...)
					result = append(result, append(tokens, line.Token...))
				}
			}
		}
	}
	for index, directive := range result {
		result[index] = append([]string(nil), directive...)
		for tokenIndex, token := range directive {
			if unquoted, err := strconv.Unquote(token); err == nil {
				result[index][tokenIndex] = unquoted
			}
		}
		if len(directive) == 2 && directive[0] == "go" && semver.IsValid("v"+directive[1]) {
			result[index][1] = semver.Canonical("v" + directive[1])
		}
	}
	slices.SortFunc(result, slices.Compare[[]string])
	return result
}
