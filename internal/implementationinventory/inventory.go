// Package implementationinventory represents discovered authored
// Implementation constructor declarations.
package implementationinventory

import (
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationdecl"
)

var (
	// ErrInvalidInput reports inconsistent package or source provenance supplied
	// by the shared eligible-package discovery boundary.
	ErrInvalidInput = errors.New("invalid Implementation inventory input")
	// ErrDuplicateSymbol reports an identity collision that would prevent one
	// constructor symbol from naming exactly one visible Implementation.
	ErrDuplicateSymbol = errors.New("duplicate Implementation constructor symbol")
	// ErrInvalidConfiguration reports a constructor Config parameter that does
	// not use the exact optional same-package exported struct form.
	ErrInvalidConfiguration = errors.New("invalid Implementation Config parameter")
	// ErrInvalidRequiredInterface reports a non-Config constructor parameter
	// that is not one exact visible canonical Interface type.
	ErrInvalidRequiredInterface = errors.New("invalid required Interface constructor parameter")
)

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
	function      *types.Func
	symbol        constructorsymbol.Symbol
	configuration Configuration
	hasConfig     bool
	required      []RequiredInterface
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

// Symbol returns the canonical fully qualified constructor identity.
func (i Implementation) Symbol() constructorsymbol.Symbol { return i.symbol }

// Configuration returns the optional normalized same-package Config type used
// as the constructor's first parameter.
func (i Implementation) Configuration() (Configuration, bool) {
	return i.configuration, i.hasConfig
}

// RequiredInterfaces returns a defensive parameter-ordered view of exact
// canonical Interface dependencies required by the constructor.
func (i Implementation) RequiredInterfaces() []RequiredInterface {
	return append([]RequiredInterface(nil), i.required...)
}

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

// BySymbol returns the one Implementation identified by symbol.
func (i Index) BySymbol(symbol constructorsymbol.Symbol) (Implementation, bool) {
	value := symbol.String()
	if value == "" {
		return Implementation{}, false
	}
	index := sort.Search(len(i.implementations), func(index int) bool {
		return i.implementations[index].symbol.String() >= value
	})
	if index == len(i.implementations) || i.implementations[index].symbol != symbol {
		return Implementation{}, false
	}
	return i.implementations[index], true
}

// Build validates shared-loader provenance and constructs a deterministic
// immutable inventory without performing another filesystem or Go graph scan.
func Build(inputs []Input, interfaces []InterfaceInput) (Index, error) {
	interfacePackages, err := indexInterfacePackages(interfaces)
	if err != nil {
		return Index{}, err
	}
	implementations := make([]Implementation, len(inputs))
	for index, input := range inputs {
		position := input.Declaration.Position()
		symbol, symbolErr := constructorsymbol.New(input.PackagePath, input.Declaration.FunctionName())
		switch {
		case input.ModulePath == "" || strings.IndexByte(input.ModulePath, 0) >= 0:
			return Index{}, fmt.Errorf("%w: constructor module path is empty or unsafe", ErrInvalidInput)
		case symbolErr != nil:
			return Index{}, fmt.Errorf("%w: %v", ErrInvalidInput, symbolErr)
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
		object := input.Types.Scope().Lookup(input.Declaration.FunctionName())
		function, validFunction := object.(*types.Func)
		if !validFunction || function.Pkg() != input.Types || !function.Exported() {
			return Index{}, fmt.Errorf("%w: constructor symbol %s does not identify an exported package-level function in compiled type information", ErrInvalidInput, symbol)
		}
		configuration, hasConfig, configurationErr := validateConfiguration(input.Types, function)
		if configurationErr != nil {
			return Index{}, fmt.Errorf("%w: %s at %s: %v", ErrInvalidConfiguration, symbol, inputSource(input), configurationErr)
		}
		required, requiredErr := validateRequiredInterfaces(function, hasConfig, interfacePackages)
		if requiredErr != nil {
			return Index{}, fmt.Errorf("%w: %s at %s: %v", ErrInvalidRequiredInterface, symbol, inputSource(input), requiredErr)
		}
		implementations[index] = Implementation{
			modulePath:    input.ModulePath,
			moduleVersion: input.ModuleVersion,
			packagePath:   input.PackagePath,
			local:         input.Local,
			declaration:   input.Declaration,
			types:         input.Types,
			function:      function,
			symbol:        symbol,
			configuration: configuration,
			hasConfig:     hasConfig,
			required:      required,
		}
	}

	sort.Slice(implementations, func(left, right int) bool {
		if implementations[left].symbol != implementations[right].symbol {
			return implementations[left].symbol.String() < implementations[right].symbol.String()
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
	for index := 1; index < len(implementations); index++ {
		if implementations[index-1].symbol == implementations[index].symbol {
			return Index{}, fmt.Errorf("%w: %s is declared at %s and %s", ErrDuplicateSymbol, implementations[index].symbol, implementations[index-1].Source(), implementations[index].Source())
		}
	}
	return Index{implementations: implementations}, nil
}

func inputSource(input Input) string {
	version := input.ModuleVersion
	if version == "" {
		version = "local"
	}
	position := input.Declaration.Position()
	return fmt.Sprintf("%s@%s/%s:%d:%d", input.ModulePath, version, position.Path, position.Line, position.Column)
}
