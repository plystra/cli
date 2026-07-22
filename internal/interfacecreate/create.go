// Package interfacecreate scaffolds canonical authored Interface packages.
package interfacecreate

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/projectlocate"
)

var (
	// ErrCreate reports a failed Interface-package creation transaction.
	ErrCreate = errors.New("create Interface package")
	// ErrInvalidName reports an invalid unversioned Interface name.
	ErrInvalidName = errors.New("invalid Interface name")
	// ErrTargetExists reports an existing target package or visible Interface ID.
	ErrTargetExists = errors.New("Interface target already exists")
)

// Options controls one Interface-package creation transaction.
type Options struct {
	Start                 string
	Name                  string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
}

// Result identifies one committed canonical Interface package.
type Result struct {
	id          interfaceid.Identifier
	moduleRoot  string
	packagePath string
	importPath  string
	sourcePath  string
}

// ID returns the exact canonical Interface ID created by the transaction.
func (r Result) ID() interfaceid.Identifier { return r.id }

// ModuleRoot returns the canonical absolute Plystra Project root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// PackagePath returns the canonical absolute authored package directory.
func (r Result) PackagePath() string { return r.packagePath }

// ImportPath returns the canonical Go import path of the authored package.
func (r Result) ImportPath() string { return r.importPath }

// SourcePath returns the slash-separated Project-relative authored Go path.
func (r Result) SourcePath() string { return r.sourcePath }

// Create scaffolds and validates the initial v1 package for an unversioned
// Interface name. It does not create metadata or mutate application
// configuration; those operations have separate ownership.
func Create(ctx context.Context, options Options) (Result, error) {
	return create(ctx, options, nil)
}

type postValidate func(root string, index interfaceinventory.Index) error

func create(ctx context.Context, options Options, after postValidate) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrCreate)
	}
	identifier, err := interfaceid.New(options.Name, 1)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w %q: expected an unversioned canonical name with two or more dot-separated segments: %v", ErrCreate, ErrInvalidName, options.Name, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrCreate, err)
	}

	target, err := deriveTarget(project, identifier)
	if err != nil {
		return Result{}, fmt.Errorf("%w: derive target: %w", ErrCreate, err)
	}
	if err := requireAbsentDirectory(target.packagePath, path.Dir(target.sourcePath)); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}

	before, err := discover(ctx, project, options)
	if err != nil {
		return Result{}, fmt.Errorf("%w: validate visible Interfaces before mutation: %w", ErrCreate, err)
	}
	if existing, found := findID(before, identifier.String()); found {
		return Result{}, fmt.Errorf("%w: %w: %s is already defined by package %q at %s", ErrCreate, ErrTargetExists, identifier, existing.PackagePath(), existing.Source())
	}

	source, err := render(identifier, target.packageName, target.methodName)
	if err != nil {
		return Result{}, fmt.Errorf("%w: render source: %w", ErrCreate, err)
	}
	write := atomicfs.Write{
		Path:               target.sourcePath,
		Data:               source,
		Mode:               0o644,
		MustNotExist:       true,
		ParentMustNotExist: true,
	}
	if err := atomicfs.WriteFiles(project.Path(), []atomicfs.Write{write}, func(updatedRoot string) error {
		updatedProject, err := projectlocate.Find(updatedRoot)
		if err != nil {
			return fmt.Errorf("locate updated Project: %w", err)
		}
		index, err := discover(ctx, updatedProject, options)
		if err != nil {
			return err
		}
		created, found := findID(index, identifier.String())
		if !found || !created.Local() || created.PackagePath() != target.importPath || created.SourcePath() != target.sourcePath {
			return fmt.Errorf("created Interface %s was not discovered at package %q source %s", identifier, target.importPath, target.sourcePath)
		}
		if after != nil {
			return after(updatedRoot, index)
		}
		return nil
	}); err != nil {
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

type target struct {
	packageName string
	methodName  string
	packagePath string
	importPath  string
	sourcePath  string
}

func deriveTarget(project modulelocate.Module, identifier interfaceid.Identifier) (target, error) {
	segments := strings.Split(identifier.Name(), ".")
	if len(segments) < 2 {
		return target{}, errors.New("Interface name has fewer than two segments")
	}
	final := segments[len(segments)-1]
	packageName := strings.ReplaceAll(final, "-", "") + "v1"
	methodName := exportedIdentifier(final)
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return target{}, fmt.Errorf("derived package name %q is not a Go identifier", packageName)
	}
	if !token.IsIdentifier(methodName) || token.Lookup(methodName).IsKeyword() {
		return target{}, fmt.Errorf("derived method name %q is not a Go identifier", methodName)
	}
	directoryParts := append([]string{"interfaces"}, segments...)
	directoryParts = append(directoryParts, "v1")
	relativeDirectory := path.Join(directoryParts...)
	return target{
		packageName: packageName,
		methodName:  methodName,
		packagePath: filepath.Join(project.Path(), filepath.FromSlash(relativeDirectory)),
		importPath:  path.Join(project.ModulePath(), relativeDirectory),
		sourcePath:  path.Join(relativeDirectory, "interface.go"),
	}, nil
}

func exportedIdentifier(segment string) string {
	parts := strings.Split(segment, "-")
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		if part[0] >= 'a' && part[0] <= 'z' {
			result.WriteByte(part[0] - 'a' + 'A')
		} else {
			result.WriteByte(part[0])
		}
		result.WriteString(part[1:])
	}
	return result.String()
}

func requireAbsentDirectory(directory, relative string) error {
	_, err := os.Lstat(directory)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect target package %s: %w", relative, err)
	default:
		return fmt.Errorf("%w: package directory %s already exists", ErrTargetExists, relative)
	}
}

func discover(ctx context.Context, project modulelocate.Module, options Options) (interfaceinventory.Index, error) {
	dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return interfaceinventory.Index{}, err
	}
	index, err := interfaceinventory.Discover(ctx, project, dependencies, interfaceinventory.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return interfaceinventory.Index{}, err
	}
	if err := interfaceinventory.ValidateUniqueIDs(index); err != nil {
		return interfaceinventory.Index{}, err
	}
	return index, nil
}

func findID(index interfaceinventory.Index, identifier string) (interfaceinventory.Interface, bool) {
	for _, candidate := range index.Interfaces() {
		if candidate.ID() == identifier {
			return candidate, true
		}
	}
	return interfaceinventory.Interface{}, false
}

func render(identifier interfaceid.Identifier, packageName, methodName string) ([]byte, error) {
	source := fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

// Request contains the %s request fields.
type Request struct{}

// Response contains the %s response fields.
type Response struct{}
`, packageName, identifier, methodName, identifier, identifier)
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, err
	}
	return formatted, nil
}
