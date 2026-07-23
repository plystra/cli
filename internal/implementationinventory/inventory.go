// Package implementationinventory represents discovered authored
// Implementation constructor declarations.
package implementationinventory

import (
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/implementationdecl"
)

// ErrInvalidInput reports inconsistent package or source provenance supplied
// by the shared eligible-package discovery boundary.
var ErrInvalidInput = errors.New("invalid Implementation inventory input")

// Input is one parsed constructor declaration and its compiled Go package
// provenance. Callers obtain these values from the shared eligible-package
// loader rather than scanning source independently.
type Input struct {
	ModulePath    string
	ModuleVersion string
	PackagePath   string
	Local         bool
	Declaration   implementationdecl.Declaration
	Types         *types.Package
}

// Implementation is one active authored constructor declaration with stable
// module, package, and source provenance. Constructor signature and
// assignability checks are applied by later structural-conformance stages.
type Implementation struct {
	modulePath    string
	moduleVersion string
	packagePath   string
	local         bool
	declaration   implementationdecl.Declaration
	types         *types.Package
}

// ModulePath returns the Go Module path that owns the constructor package.
func (i Implementation) ModulePath() string { return i.modulePath }

// ModuleVersion returns the effective selected module version. It is empty for
// the current Project and dependency Projects supplied by an active workspace.
func (i Implementation) ModuleVersion() string { return i.moduleVersion }

// PackagePath returns the canonical Go import path of the constructor package.
func (i Implementation) PackagePath() string { return i.packagePath }

// SourcePath returns the stable slash-separated module-relative source path.
func (i Implementation) SourcePath() string { return i.declaration.Position().Path }

// Source returns stable module-qualified constructor provenance.
func (i Implementation) Source() string {
	version := i.moduleVersion
	if version == "" {
		version = "local"
	}
	position := i.declaration.Position()
	return fmt.Sprintf("%s@%s/%s:%d:%d", i.modulePath, version, position.Path, position.Line, position.Column)
}

// Local reports whether the constructor belongs to the selected current
// Project rather than a dependency Project.
func (i Implementation) Local() bool { return i.local }

// PackageName returns the compiled Go package name.
func (i Implementation) PackageName() string { return i.declaration.PackageName() }

// FunctionName returns the exported constructor function name.
func (i Implementation) FunctionName() string { return i.declaration.FunctionName() }

// Declaration returns the immutable parsed constructor declaration.
func (i Implementation) Declaration() implementationdecl.Declaration { return i.declaration }

// Index is an immutable deterministic inventory of every active visible
// Implementation constructor declaration.
type Index struct {
	implementations []Implementation
}

// Implementations returns a defensive copy ordered by package and source
// identity. No selection priority is implied by this order.
func (i Index) Implementations() []Implementation {
	return append([]Implementation(nil), i.implementations...)
}

// Build validates shared-loader provenance and constructs a deterministic
// immutable inventory without performing another filesystem or Go graph scan.
func Build(inputs []Input) (Index, error) {
	implementations := make([]Implementation, len(inputs))
	for index, input := range inputs {
		position := input.Declaration.Position()
		switch {
		case input.ModulePath == "" || strings.IndexByte(input.ModulePath, 0) >= 0:
			return Index{}, fmt.Errorf("%w: constructor module path is empty or unsafe", ErrInvalidInput)
		case input.PackagePath == "" || strings.IndexByte(input.PackagePath, 0) >= 0:
			return Index{}, fmt.Errorf("%w: constructor package path is empty or unsafe", ErrInvalidInput)
		case input.Types == nil:
			return Index{}, fmt.Errorf("%w: constructor package %q has no compiled type information", ErrInvalidInput, input.PackagePath)
		case input.Types.Path() != input.PackagePath:
			return Index{}, fmt.Errorf("%w: constructor package path %q does not match compiled package %q", ErrInvalidInput, input.PackagePath, input.Types.Path())
		case input.Declaration.PackageName() == "" || input.Types.Name() != input.Declaration.PackageName():
			return Index{}, fmt.Errorf("%w: constructor package %q name does not match compiled package", ErrInvalidInput, input.PackagePath)
		case input.Declaration.FunctionName() == "":
			return Index{}, fmt.Errorf("%w: constructor package %q has an empty function name", ErrInvalidInput, input.PackagePath)
		case position.Path == "" || strings.IndexByte(position.Path, 0) >= 0 || position.Line <= 0 || position.Column <= 0:
			return Index{}, fmt.Errorf("%w: constructor %s.%s has unsafe source provenance", ErrInvalidInput, input.PackagePath, input.Declaration.FunctionName())
		}
		implementations[index] = Implementation{
			modulePath:    input.ModulePath,
			moduleVersion: input.ModuleVersion,
			packagePath:   input.PackagePath,
			local:         input.Local,
			declaration:   input.Declaration,
			types:         input.Types,
		}
	}

	sort.Slice(implementations, func(left, right int) bool {
		if implementations[left].packagePath != implementations[right].packagePath {
			return implementations[left].packagePath < implementations[right].packagePath
		}
		if implementations[left].FunctionName() != implementations[right].FunctionName() {
			return implementations[left].FunctionName() < implementations[right].FunctionName()
		}
		leftPosition := implementations[left].declaration.Position()
		rightPosition := implementations[right].declaration.Position()
		if leftPosition.Path != rightPosition.Path {
			return leftPosition.Path < rightPosition.Path
		}
		if leftPosition.Line != rightPosition.Line {
			return leftPosition.Line < rightPosition.Line
		}
		if leftPosition.Column != rightPosition.Column {
			return leftPosition.Column < rightPosition.Column
		}
		if implementations[left].modulePath != implementations[right].modulePath {
			return implementations[left].modulePath < implementations[right].modulePath
		}
		return implementations[left].moduleVersion < implementations[right].moduleVersion
	})
	return Index{implementations: implementations}, nil
}
