// Package implementationcreate scaffolds ordinary Go Implementation packages.
package implementationcreate

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
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/module"
)

var (
	// ErrCreate reports a failed Implementation-package creation transaction.
	ErrCreate = errors.New("create Implementation package")
	// ErrInvalidInterface reports a malformed canonical Interface ID.
	ErrInvalidInterface = errors.New("invalid Interface ID")
	// ErrInvalidPackage reports an unsafe or noncanonical target package path.
	ErrInvalidPackage = errors.New("invalid Implementation package")
	// ErrInterfaceNotFound reports an Interface absent from the visible Project graph.
	ErrInterfaceNotFound = errors.New("Interface is not visible")
	// ErrTargetExists reports an authored target package that must not be overwritten.
	ErrTargetExists = errors.New("Implementation target already exists")
)

// Options controls one ordinary Implementation-package creation transaction.
type Options struct {
	Start                 string
	InterfaceID           string
	Package               string
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
}

// Result identifies one committed ordinary Go Implementation package.
type Result struct {
	interfaceID interfaceid.Identifier
	moduleRoot  string
	packagePath string
	importPath  string
	sourcePath  string
	constructor constructorsymbol.Symbol
}

// InterfaceID returns the exact canonical Interface implemented by the scaffold.
func (r Result) InterfaceID() interfaceid.Identifier { return r.interfaceID }

// ModuleRoot returns the canonical absolute current-Project root.
func (r Result) ModuleRoot() string { return r.moduleRoot }

// PackagePath returns the canonical absolute authored package directory.
func (r Result) PackagePath() string { return r.packagePath }

// ImportPath returns the canonical Go import path of the authored package.
func (r Result) ImportPath() string { return r.importPath }

// SourcePath returns the slash-separated Project-relative authored Go path.
func (r Result) SourcePath() string { return r.sourcePath }

// Constructor returns the fully qualified constructor symbol created by the scaffold.
func (r Result) Constructor() constructorsymbol.Symbol { return r.constructor }

// Create writes and validates one new ordinary Go package implementing a visible
// canonical Interface. It does not copy the contract, edit configuration, or
// create authored registration source.
func Create(ctx context.Context, options Options) (Result, error) {
	return create(ctx, options, nil)
}

type postValidate func(root string, discovery interfaceinventory.Discovery) error

func create(ctx context.Context, options Options, after postValidate) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrCreate)
	}
	identifier, err := interfaceid.Parse(options.InterfaceID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w %q: %v", ErrCreate, ErrInvalidInterface, options.InterfaceID, err)
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: locate Project: %w", ErrCreate, err)
	}
	target, err := deriveTarget(project, options.Package)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	if err := requireAbsentPackage(target); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}

	before, err := discover(ctx, project, options)
	if err != nil {
		return Result{}, fmt.Errorf("%w: validate visible Interfaces before mutation: %w", ErrCreate, err)
	}
	canonical, found := findInterface(before.Interfaces(), identifier.String())
	if !found {
		return Result{}, fmt.Errorf("%w: %w: %s; add the Go Module that defines it or create the Interface first", ErrCreate, ErrInterfaceNotFound, identifier)
	}
	source, err := render(identifier, canonical, target.packageName)
	if err != nil {
		return Result{}, fmt.Errorf("%w: render source: %w", ErrCreate, err)
	}
	constructor, err := constructorsymbol.New(target.importPath, "New")
	if err != nil {
		return Result{}, fmt.Errorf("%w: derive constructor symbol: %w", ErrCreate, err)
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
		discovery, err := discover(ctx, updatedProject, options)
		if err != nil {
			return err
		}
		implementation, found := discovery.Implementations().BySymbol(constructor)
		if !found || !implementation.Local() || implementation.PackagePath() != target.importPath || implementation.SourcePath() != target.sourcePath {
			return fmt.Errorf("created Implementation %s was not discovered at source %s", constructor, target.sourcePath)
		}
		declared := implementation.Declaration().ImplementedInterfaces()
		if len(declared) != 1 || declared[0].ID() != identifier {
			return fmt.Errorf("created Implementation %s does not declare only Interface %s", constructor, identifier)
		}
		if after != nil {
			return after(updatedRoot, discovery)
		}
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}

	return Result{
		interfaceID: identifier,
		moduleRoot:  project.Path(),
		packagePath: target.packagePath,
		importPath:  target.importPath,
		sourcePath:  target.sourcePath,
		constructor: constructor,
	}, nil
}

type targetPackage struct {
	packageName string
	relative    string
	packagePath string
	importPath  string
	sourcePath  string
}

func deriveTarget(project modulelocate.Module, value string) (targetPackage, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\\') || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || !strings.HasPrefix(value, "./") {
		return targetPackage{}, fmt.Errorf("%w %q: expected ./ followed by one safe Project-relative Go package path", ErrInvalidPackage, value)
	}
	relative := strings.TrimPrefix(value, "./")
	if relative == "" || !fs.ValidPath(relative) || relative == "." || path.Clean(relative) != relative {
		return targetPackage{}, fmt.Errorf("%w %q: expected a canonical child package path without traversal", ErrInvalidPackage, value)
	}
	for _, segment := range strings.Split(relative, "/") {
		if interfaceinventory.ReservedDirectory(segment) {
			return targetPackage{}, fmt.Errorf("%w %q: directory %q is excluded from authored package discovery", ErrInvalidPackage, value, segment)
		}
	}
	packageName := path.Base(relative)
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return targetPackage{}, fmt.Errorf("%w %q: final path segment %q must be a Go package identifier", ErrInvalidPackage, value, packageName)
	}
	importPath := path.Join(project.ModulePath(), relative)
	if err := module.CheckImportPath(importPath); err != nil {
		return targetPackage{}, fmt.Errorf("%w %q: invalid Go import path %q: %v", ErrInvalidPackage, value, importPath, err)
	}
	return targetPackage{
		packageName: packageName,
		relative:    relative,
		packagePath: filepath.Join(project.Path(), filepath.FromSlash(relative)),
		importPath:  importPath,
		sourcePath:  path.Join(relative, "implementation.go"),
	}, nil
}

func requireAbsentPackage(target targetPackage) error {
	_, err := os.Lstat(target.packagePath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect target package %s: %w", target.relative, err)
	default:
		return fmt.Errorf("%w: package directory %s already exists; choose a new --package path", ErrTargetExists, target.relative)
	}
}

func discover(ctx context.Context, project modulelocate.Module, options Options) (interfaceinventory.Discovery, error) {
	dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return interfaceinventory.Discovery{}, err
	}
	discovery, err := interfaceinventory.DiscoverApplication(ctx, project, dependencies, interfaceinventory.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return interfaceinventory.Discovery{}, err
	}
	if err := interfaceinventory.ValidateUniqueIDs(discovery.Interfaces()); err != nil {
		return interfaceinventory.Discovery{}, err
	}
	return discovery, nil
}

func findInterface(index interfaceinventory.Index, identifier string) (interfaceinventory.Interface, bool) {
	for _, candidate := range index.Interfaces() {
		if candidate.ID() == identifier {
			return candidate, true
		}
	}
	return interfaceinventory.Interface{}, false
}

func render(identifier interfaceid.Identifier, canonical interfaceinventory.Interface, packageName string) ([]byte, error) {
	contract := canonical.Contract()
	source := fmt.Sprintf(`package %s

import (
	"context"
	"errors"

	contract %q
)

var errNotImplemented = errors.New(%q)

type Service struct{}

//plystra:implements %s
func New() (*Service, error) {
	return &Service{}, nil
}

func (*Service) %s(ctx context.Context, request contract.%s) (contract.%s, error) {
	return contract.%s{}, errNotImplemented
}

var _ contract.Interface = (*Service)(nil)
`, packageName, canonical.PackagePath(), identifier.String()+" implementation is not implemented", identifier, contract.MethodName(), contract.RequestName(), contract.ResponseName(), contract.ResponseName())
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, err
	}
	return formatted, nil
}
