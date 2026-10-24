package interfacecreate

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
)

// Materialize writes an exact first-version Interface and its operation
// semantics only into a live private Project stage. The returned Result is an
// unvalidated staged identity: the caller must finish the complete candidate's
// discovery, generation, and build before treating it as a valid Interface.
// Authored imports may refer to other not-yet-materialized same-request packages.
func Materialize(ctx context.Context, stage *projectstage.Stage, name string, semantics interfacemeta.OperationKind) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrCreate)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	// Match the bounded change-request string vocabulary without accepting an
	// unversioned name or relying on the caller's normalization.
	if len(name) > 4_096 {
		return Result{}, fmt.Errorf("%w: %w: exact Interface ID exceeds 4096 bytes", ErrCreate, ErrInvalidName)
	}
	identifier, err := interfaceid.Parse(name)
	if err != nil || identifier.Major() != 1 || identifier.String() != name {
		return Result{}, fmt.Errorf("%w: %w: expected an exact canonical first-version Interface ID", ErrCreate, ErrInvalidName)
	}
	if semantics != interfacemeta.OperationKindQuery && semantics != interfacemeta.OperationKindCommand {
		return Result{}, fmt.Errorf("%w: %w: expected query or command", ErrCreate, interfacemeta.ErrInvalidSemantics)
	}
	if err := stage.Recheck(ctx); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate staged Project: %w", ErrCreate, err)
	}
	if project.Path() != stage.Root() {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, projectstage.ErrConcurrentChange)
	}
	target, err := deriveTarget(project, identifier)
	if err != nil {
		return Result{}, fmt.Errorf("%w: derive target: %w", ErrCreate, err)
	}
	directory := path.Dir(target.sourcePath)
	if err := requireAbsentDirectory(target.packagePath, directory); err != nil {
		if errors.Is(err, ErrTargetExists) {
			err = &TargetExistsError{modulePath: project.ModulePath(), sourcePath: directory, cause: err}
		}
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	source, err := render(identifier, target.packageName, target.methodName)
	if err != nil {
		return Result{}, fmt.Errorf("%w: render source: %w", ErrCreate, err)
	}
	metadataPath := path.Join(directory, interfacemeta.Name)
	metadata := []byte(fmt.Sprintf("semantics:\n  kind: %s\n", semantics))
	if _, err := interfacemeta.ParseFile(metadataPath, metadata); err != nil {
		return Result{}, fmt.Errorf("%w: validate metadata: %w", ErrCreate, err)
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
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	writes := []atomicfs.Write{
		{Path: target.sourcePath, Data: source, OwnerPrivate: true, MustNotExist: true, ParentMustNotExist: true},
		{Path: metadataPath, Data: metadata, OwnerPrivate: true, MustNotExist: true, ParentMustNotExist: true},
	}
	if err := atomicfs.WriteFiles(stage.Root(), writes, func(string) error { return recheck() }); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{
		id:          identifier,
		moduleRoot:  project.Path(),
		packagePath: target.packagePath,
		importPath:  target.importPath,
		sourcePath:  target.sourcePath,
	}, nil
}
