package resourceproviderinventory_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/resourceproviderdecl"
	"github.com/plystra/cli/internal/resourceproviderinventory"
)

type packages map[string]*types.Package

func (p packages) Import(path string) (*types.Package, error) {
	if pkg := p[path]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("unknown package %s", path)
}

func compile(t *testing.T, imports packages, path, source string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{Importer: imports}
	pkg, err := config.Check(path, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	imports[path] = pkg
	return pkg
}

const declaration = "//plystra:implements-resource data.database/v1\n"
const providerSource = "package provider\nimport api \"example.com/api\"\ntype Config struct{Limit int}\ntype database struct{}\nfunc (*database) Read() api.Value{return api.Value{}}\n" + declaration + "func New(cfg Config, primary api.Resource, Secondary api.Resource)(*database,error){panic(\"constructor-entry\")}\n"

func providerInput(t *testing.T, imports packages, source string) resourceproviderinventory.Input {
	t.Helper()
	compile(t, imports, "example.com/provider", source)
	declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("parse: %v %#v", err, declarations)
	}
	return resourceproviderinventory.Input{ModulePath: "example.com/provider", ModuleVersion: "v1.2.3", PackagePath: "example.com/provider", Declaration: declarations[0]}
}

func contracts(t *testing.T) (packages, []resourceproviderinventory.ContractInput) {
	t.Helper()
	imports := packages{}
	compile(t, imports, "example.com/api", "package api\ntype Resource interface{Read() Value}\ntype Value struct{Data string}\n")
	return imports, []resourceproviderinventory.ContractInput{{ID: "data.database/v1", PackagePath: "example.com/api"}}
}

func TestBuildResourceProviders(t *testing.T) {
	imports, canonical := contracts(t)
	input := providerInput(t, imports, providerSource)
	index, err := resourceproviderinventory.Build([]resourceproviderinventory.Input{input}, canonical, imports)
	if err != nil {
		t.Fatal(err)
	}
	view := index.Providers()
	if len(view) != 1 {
		t.Fatal(view)
	}
	provider := view[0]
	if provider.ID() != "data.database/v1" || provider.Symbol().String() != "example.com/provider.New" || provider.ModulePath() != input.ModulePath || provider.ModuleVersion() != "v1.2.3" || provider.Local() || provider.ConcreteType().String() != "*example.com/provider.database" || !strings.Contains(provider.Source(), "@v1.2.3/provider.go:") {
		t.Fatalf("provider = %#v", provider)
	}
	configuration, has := provider.Configuration()
	if !has || len(configuration.Fields()) != 1 {
		t.Fatal("missing compiled configuration")
	}
	deps := provider.Dependencies()
	for i, name := range []string{"primary", "Secondary"} {
		if len(deps) != 2 || deps[i].ID() != "data.database/v1" || deps[i].PackagePath() != "example.com/api" || deps[i].ParameterName() != name || deps[i].ParameterPosition() != i+2 {
			t.Fatalf("dependencies = %#v", deps)
		}
	}
	deps[0] = resourceproviderinventory.Dependency{}
	view[0] = resourceproviderinventory.Provider{}
	found, ok := index.BySymbol(provider.Symbol())
	if !ok || !reflect.DeepEqual(provider, found) || provider.Dependencies()[0].ParameterName() != "primary" {
		t.Fatal("inventory exposes mutable storage")
	}
}

func TestResourceProviderAcceptsExactAliasesNamesAndConcreteMethods(t *testing.T) {
	for _, source := range []string{
		strings.Replace(providerSource, "primary api.Resource, Secondary api.Resource", "_primary api.Resource, \u4e3b api.Resource", 1),
		strings.Replace(providerSource, "primary api.Resource, Secondary api.Resource", "Primary, primary api.Resource", 1),
		strings.Replace(providerSource, "cfg Config, primary api.Resource, Secondary api.Resource", "Config", 1),
		strings.Replace(providerSource, "cfg Config, primary api.Resource, Secondary api.Resource", "", 1),
		strings.Replace(providerSource, "type Config", "type Contract = api.Resource\ntype Config", 1),
		strings.Replace(providerSource, "type database struct{}", "type database struct{}\nfunc (*database) Close(){}", 1),
	} {
		imports, canonical := contracts(t)
		if strings.Contains(source, "type Contract") {
			source = strings.ReplaceAll(source, "primary api.Resource", "primary Contract")
		}
		input := providerInput(t, imports, source)
		if _, err := resourceproviderinventory.Build([]resourceproviderinventory.Input{input}, canonical, imports); err != nil {
			t.Fatalf("accepted source rejected: %v\n%s", err, source)
		}
	}
}

func TestResourceProviderRejectsInvalidSignaturesAndConformance(t *testing.T) {
	for _, test := range []struct{ name, old, replacement, want string }{
		{"unnamed", "cfg Config, primary api.Resource, Secondary api.Resource", "api.Resource", "explicit nonblank"},
		{"blank", "primary api.Resource", "_ api.Resource", "explicit nonblank"},
		{"noncanonical", "primary api.Resource", "primary interface{Read() api.Value}", "exact canonical"},
		{"copied", "type Config struct{Limit int}", "type Resource interface{Read() api.Value}\ntype Config struct{Limit int}", "not a visible"},
		{"pointer", "primary api.Resource", "primary *api.Resource", "exact canonical"},
		{"slice", "primary api.Resource", "primary []api.Resource", "exact canonical"},
		{"variadic", "Secondary api.Resource", "Secondary ...api.Resource", "non-variadic"},
		{"optional", "primary api.Resource", "primary Optional[api.Resource]", "exact canonical"},
		{"config-pointer", "cfg Config", "cfg *Config", "struct value"},
		{"config-position", "cfg Config, primary api.Resource", "primary api.Resource, cfg Config", "first constructor"},
		{"config-kind", "type Config struct{Limit int}", "type Config int", "must be a struct"},
		{"config-alias", "type Config struct{Limit int}", "type Config = struct{Limit int}", "type alias"},
		{"config-field", "Limit int", "Limit chan int", "not supported"},
		{"no-pointer", "(*database,error)", "(database,error)", "pointer"},
		{"interface-result", "(*database,error)", "(api.Resource,error)", "pointer"},
		{"pointer-interface", "(*database,error)", "(*api.Resource,error)", "concrete type"},
		{"anonymous-pointer", "(*database,error)", "(*struct{},error)", "defined concrete"},
		{"error-kind", "(*database,error)", "(*database,int)", "predeclared error"},
		{"error-wrapper", "(*database,error)", "(*database,Failure)", "predeclared error"},
		{"one-result", "(*database,error)", "*database", "exactly one"},
		{"unknown", "data.database/v1", "data.missing/v1", "no visible"},
		{"method-missing", "Read() api.Value", "ReadOther() api.Value", "not assignable"},
		{"method-result", "Read() api.Value{return api.Value{}}", "Read() string{return \"\"}", "not assignable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			imports, canonical := contracts(t)
			source := strings.Replace(providerSource, test.old, test.replacement, 1)
			if test.name == "copied" {
				source = strings.Replace(source, "primary api.Resource", "primary Resource", 1)
			}
			if test.name == "optional" {
				source = strings.Replace(source, "type Config", "type Optional[T any] struct{Value T}\ntype Config", 1)
			}
			if test.name == "error-wrapper" {
				source = strings.Replace(source, "type Config", "type Failure interface{Error()string}\ntype Config", 1)
			}
			input := providerInput(t, imports, source)
			_, err := resourceproviderinventory.Build([]resourceproviderinventory.Input{input}, canonical, imports)
			var invalid *resourceproviderinventory.ValidationError
			if !errors.Is(err, resourceproviderinventory.ErrInvalid) || !errors.As(err, &invalid) || !strings.Contains(err.Error(), test.want) || invalid.ModulePath() != input.ModulePath || invalid.SourcePath() != "provider.go" || invalid.Line() <= 0 || invalid.Column() <= 0 {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestProviderConformanceUsesSharedNamedTypeIdentityWithoutContractImport(t *testing.T) {
	imports := packages{}
	compile(t, imports, "example.com/value", "package value\ntype Value struct{Data string}\n")
	compile(t, imports, "example.com/api", "package api\nimport \"example.com/value\"\ntype Resource interface{Read() value.Value}\n")
	source := "package provider\nimport \"example.com/value\"\ntype database struct{}\nfunc (*database) Read() value.Value{return value.Value{}}\n" + declaration + "func New()(*database,error){panic(\"constructor-entry\")}\n"
	input := providerInput(t, imports, source)
	if _, err := resourceproviderinventory.Build([]resourceproviderinventory.Input{input}, []resourceproviderinventory.ContractInput{{ID: "data.database/v1", PackagePath: "example.com/api"}}, imports); err != nil {
		t.Fatal(err)
	}
}
