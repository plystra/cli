package interfaceinventory

import (
	"context"
	"errors"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
)

// ErrSelectedPackageUnavailable means Go selected no source for this package.
var ErrSelectedPackageUnavailable = errors.New("selected package has no applicable Go sources")

// LoadSelectedPackage reads compiler exports from one package of an already
// selected module. It shares discovery's private module preparation and never
// executes package initialization or changes authored module files.
func LoadSelectedPackage(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, selected moduledependency.Module, packagePath string, options Options) (*types.Package, error) {
	if ctx == nil || selected.Root() == "" || !strings.HasPrefix(packagePath, selected.Path()+"/") {
		return nil, fmt.Errorf("%w: invalid selected package", ErrInvalidOutput)
	}
	options = withOfflineGoEnvironment(options)
	loaded, err := loadProjectPackages(ctx, application, dependencies, options, func(options Options, flags ...string) (loadedInventory, error) {
		arguments := append([]string{"list", "-deps", "-export", "-json", "-e", "-mod=readonly"}, flags...)
		arguments = append(arguments, packagePath)
		limit := options.OutputLimit
		if limit <= 0 {
			limit = defaultOutputLimit
		}
		output, started, err := gocommand.OutputObserved(ctx, gocommand.Options{
			Command: options.GoCommand, Directory: application.Path(), Environment: options.Environment, OutputLimit: limit,
		}, arguments...)
		if started && options.ObserveLoad != nil {
			options.ObserveLoad(PackageLoadEvent{Module: application.ModulePath(), GoStarted: true})
		}
		if err != nil {
			return loadedInventory{}, err
		}
		packages, exports, err := decodePackages(output)
		if err != nil {
			return loadedInventory{}, err
		}
		pkg, exists := packages[packagePath]
		noGo := pkg.Error != nil && (pkg.Error.Err == "build constraints exclude all Go files in "+pkg.Dir || pkg.Error.Err == "no Go files in "+pkg.Dir)
		if exists && noGo && len(pkg.GoFiles) == 0 && len(pkg.CgoFiles) == 0 && len(pkg.InvalidGoFiles) == 0 && len(pkg.DepsErrors) == 0 {
			return loadedInventory{}, ErrSelectedPackageUnavailable
		}
		if !exists || pkg.Module == nil || pkg.Module.Path != selected.Path() || pkg.Module.Version != selected.SelectedVersion() ||
			!sameDirectory(pkg.Module.Dir, selected.Root()) || !sameDirectory(pkg.Dir, filepath.Join(selected.Root(), filepath.FromSlash(strings.TrimPrefix(packagePath, selected.Path()+"/")))) {
			return loadedInventory{}, fmt.Errorf("%w: selected package provenance changed", ErrInvalidOutput)
		}
		replacement, replaced := selected.Replacement()
		if replaced != (pkg.Module.Replace != nil) {
			return loadedInventory{}, fmt.Errorf("%w: selected package replacement changed", ErrInvalidOutput)
		}
		if replaced {
			matches := pkg.Module.Replace.Path == replacement.Path() && pkg.Module.Replace.Version == replacement.Version()
			if replacement.Local() {
				matches = pkg.Module.Replace.Version == "" && sameDirectory(pkg.Module.Replace.Dir, selected.Root())
			}
			if !matches {
				return loadedInventory{}, fmt.Errorf("%w: selected package replacement changed", ErrInvalidOutput)
			}
		}
		for _, dependency := range packages {
			if dependency.Incomplete || dependency.Error != nil || len(dependency.DepsErrors) != 0 || dependency.Name == "" || dependency.Name == "main" || dependency.Export == "" && dependency.ImportPath != "unsafe" {
				return loadedInventory{}, fmt.Errorf("%w: selected package exports are unavailable", ErrPackage)
			}
		}
		return loadedInventory{importer: importer.ForCompiler(token.NewFileSet(), runtime.Compiler, exportLookup(exports))}, nil
	})
	if err != nil {
		return nil, err
	}
	checked, err := loaded.importer.Import(packagePath)
	if err != nil {
		// Export paths and compiler diagnostics are private implementation inputs.
		return nil, &discoveryModuleError{cause: errors.Join(ErrInvalidOutput, err), message: "invalid selected package exports"}
	}
	return checked, nil
}
