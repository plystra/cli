// Package resourceproviderinventory validates authored Resource provider
// constructors without selecting, configuring, or constructing instances.
package resourceproviderinventory

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourceproviderdecl"
)

var ErrInvalid = errors.New("invalid Resource provider")

// Input retains the declaration and owning source from shared package discovery.
type Input struct {
	ModulePath, ModuleVersion, PackagePath string
	Local                                  bool
	Declaration                            resourceproviderdecl.Declaration
}

// ContractInput names an already validated, uniquely visible Resource contract.
type ContractInput struct{ ID, PackagePath string }

// ValidationError retains the safe owning constructor source for diagnostics.
type ValidationError struct {
	input  Input
	detail string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s at %s: %s", ErrInvalid, e.input.PackagePath+"."+e.input.Declaration.FunctionName(), inputSource(e.input), e.detail)
}
func (*ValidationError) Unwrap() error        { return ErrInvalid }
func (e *ValidationError) ModulePath() string { return e.input.ModulePath }
func (e *ValidationError) SourcePath() string { return e.input.Declaration.Position().Path }
func (e *ValidationError) Line() int          { return e.input.Declaration.Position().Line }
func (e *ValidationError) Column() int        { return e.input.Declaration.Position().Column }

// Dependency is an exact canonical Resource parameter, not an Interface root.
type Dependency struct {
	id, packagePath, parameterName string
	parameterPosition              int
}

func (d Dependency) ID() string             { return d.id }
func (d Dependency) PackagePath() string    { return d.packagePath }
func (d Dependency) ParameterName() string  { return d.parameterName }
func (d Dependency) ParameterPosition() int { return d.parameterPosition }

// Provider is a validated declaration. Its configuration schema is inert until
// a separately selected named instance uses it.
type Provider struct {
	input         Input
	symbol        constructorsymbol.Symbol
	configuration implementationinventory.Configuration
	hasConfig     bool
	concrete      implementationinventory.ConcreteType
	dependencies  []Dependency
}

func (p Provider) ID() string                                    { return p.input.Declaration.ID() }
func (p Provider) Symbol() constructorsymbol.Symbol              { return p.symbol }
func (p Provider) ModulePath() string                            { return p.input.ModulePath }
func (p Provider) ModuleVersion() string                         { return p.input.ModuleVersion }
func (p Provider) PackagePath() string                           { return p.input.PackagePath }
func (p Provider) Local() bool                                   { return p.input.Local }
func (p Provider) Source() string                                { return inputSource(p.input) }
func (p Provider) Declaration() resourceproviderdecl.Declaration { return p.input.Declaration }
func (p Provider) Configuration() (implementationinventory.Configuration, bool) {
	return p.configuration, p.hasConfig
}
func (p Provider) ConcreteType() implementationinventory.ConcreteType { return p.concrete }
func (p Provider) Dependencies() []Dependency                         { return append([]Dependency(nil), p.dependencies...) }

type Index struct{ providers []Provider }

func (i Index) Providers() []Provider { return append([]Provider(nil), i.providers...) }
func (i Index) BySymbol(symbol constructorsymbol.Symbol) (Provider, bool) {
	n := sort.Search(len(i.providers), func(n int) bool { return i.providers[n].symbol.String() >= symbol.String() })
	if n < len(i.providers) && i.providers[n].symbol == symbol {
		return i.providers[n], true
	}
	return Provider{}, false
}

// Build uses one export-data importer for both contracts and constructors so
// cross-Project signatures share Go type identity even without a direct import
// of the implemented contract package. It performs no filesystem discovery.
func Build(inputs []Input, contracts []ContractInput, importer types.Importer) (Index, error) {
	byID, byPackage := map[string]ContractInput{}, map[string]ContractInput{}
	for _, contract := range contracts {
		if _, err := interfaceid.Parse(contract.ID); err != nil || contract.PackagePath == "" {
			return Index{}, fmt.Errorf("%w: invalid canonical contract input", ErrInvalid)
		}
		if _, exists := byID[contract.ID]; exists {
			return Index{}, fmt.Errorf("%w: duplicate canonical contract input", ErrInvalid)
		}
		if _, exists := byPackage[contract.PackagePath]; exists {
			return Index{}, fmt.Errorf("%w: duplicate canonical package input", ErrInvalid)
		}
		byID[contract.ID], byPackage[contract.PackagePath] = contract, contract
	}
	providers := make([]Provider, 0, len(inputs))
	for _, input := range inputs {
		provider, err := validate(input, byID, byPackage, importer)
		if err != nil {
			return Index{}, &ValidationError{input: input, detail: err.Error()}
		}
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].symbol.String() < providers[j].symbol.String() })
	for n := 1; n < len(providers); n++ {
		if providers[n-1].symbol == providers[n].symbol {
			return Index{}, &ValidationError{input: providers[n].input, detail: "duplicate constructor symbol"}
		}
	}
	return Index{providers: providers}, nil
}

func validate(input Input, byID, byPackage map[string]ContractInput, importer types.Importer) (Provider, error) {
	position := input.Declaration.Position()
	symbol, err := constructorsymbol.New(input.PackagePath, input.Declaration.FunctionName())
	if err != nil || input.ModulePath == "" || strings.ContainsRune(input.ModulePath, 0) || position.Path == "" || position.Line <= 0 || position.Column <= 0 || importer == nil {
		return Provider{}, errors.New("invalid constructor identity or source input")
	}
	pkg, err := importer.Import(input.PackagePath)
	if err != nil || pkg == nil || pkg.Path() != input.PackagePath || pkg.Name() != input.Declaration.PackageName() {
		return Provider{}, errors.New("cannot load the exact compiled constructor package")
	}
	function, ok := pkg.Scope().Lookup(input.Declaration.FunctionName()).(*types.Func)
	if !ok || function.Pkg() != pkg || !function.Exported() {
		return Provider{}, errors.New("constructor must identify an exported package-level function")
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Recv() != nil || signature.Variadic() || signature.TypeParams().Len() != 0 {
		return Provider{}, errors.New("constructor must be a non-generic, non-variadic package-level function")
	}
	configuration, hasConfig, err := implementationinventory.CompileConfiguration(pkg, function)
	if err != nil {
		return Provider{}, err
	}
	concrete, err := implementationinventory.CompileResult(function)
	if err != nil {
		return Provider{}, err
	}
	contract, found := byID[input.Declaration.ID()]
	if !found {
		return Provider{}, errors.New("declared Resource ID has no visible canonical contract")
	}
	canonical, err := loadResource(importer, contract.PackagePath)
	if err != nil {
		return Provider{}, err
	}
	if !types.AssignableTo(signature.Results().At(0).Type(), canonical) {
		return Provider{}, fmt.Errorf("concrete result is not assignable to Resource %s in %s", contract.ID, contract.PackagePath)
	}
	var dependencies []Dependency
	for n := 0; n < signature.Params().Len(); n++ {
		if n == 0 && hasConfig {
			continue
		}
		parameter := signature.Params().At(n)
		if parameter.Name() == "_" || !token.IsIdentifier(parameter.Name()) {
			return Provider{}, fmt.Errorf("parameter %d must have an explicit nonblank Go identifier", n+1)
		}
		named, ok := types.Unalias(parameter.Type()).(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Name() != "Resource" {
			return Provider{}, fmt.Errorf("parameter %d must be an exact canonical Resource type", n+1)
		}
		dependency, visible := byPackage[named.Obj().Pkg().Path()]
		if !visible {
			return Provider{}, fmt.Errorf("parameter %d Resource is not a visible canonical contract", n+1)
		}
		target, err := loadResource(importer, dependency.PackagePath)
		if err != nil {
			return Provider{}, err
		}
		if !types.Identical(named, target) {
			return Provider{}, fmt.Errorf("parameter %d does not use its exact canonical Resource type", n+1)
		}
		dependencies = append(dependencies, Dependency{id: dependency.ID, packagePath: dependency.PackagePath, parameterName: parameter.Name(), parameterPosition: n + 1})
	}
	return Provider{input: input, symbol: symbol, configuration: configuration, hasConfig: hasConfig, concrete: concrete, dependencies: dependencies}, nil
}

func loadResource(importer types.Importer, path string) (*types.Named, error) {
	pkg, err := importer.Import(path)
	if err != nil || pkg == nil || pkg.Path() != path {
		return nil, errors.New("cannot load the exact compiled Resource package")
	}
	object, ok := pkg.Scope().Lookup("Resource").(*types.TypeName)
	if !ok || object.IsAlias() {
		return nil, errors.New("canonical Resource must be a defined type")
	}
	named, ok := object.Type().(*types.Named)
	if !ok || named.TypeParams().Len() != 0 {
		return nil, errors.New("canonical Resource must not be generic")
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !iface.Complete().IsMethodSet() {
		return nil, errors.New("canonical Resource must be an ordinary Go interface")
	}
	return named, nil
}

func inputSource(input Input) string {
	version := input.ModuleVersion
	if version == "" {
		version = "local"
	}
	position := input.Declaration.Position()
	return fmt.Sprintf("%s@%s/%s:%d:%d", input.ModulePath, version, position.Path, position.Line, position.Column)
}
