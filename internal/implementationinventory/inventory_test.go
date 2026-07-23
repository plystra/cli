package implementationinventory_test

import (
	"errors"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestBuildOrdersAndProtectsDiscoveredImplementations(t *testing.T) {
	t.Parallel()

	alpha := declaration(t, "alpha/new.go", "alpha", "Build", "alpha.service.run/v1")
	zeta := declaration(t, "zeta/new.go", "zeta", "New", "zeta.service.run/v1")
	index, err := implementationinventory.Build([]implementationinventory.Input{
		{
			ModulePath:    "example.com/dependency",
			ModuleVersion: "v1.2.3",
			PackagePath:   "example.com/dependency/zeta",
			Declaration:   zeta,
			Types:         compiledPackage("example.com/dependency/zeta", "zeta", "New"),
		},
		{
			ModulePath:  "example.com/app",
			PackagePath: "example.com/app/alpha",
			Local:       true,
			Declaration: alpha,
			Types:       compiledPackage("example.com/app/alpha", "alpha", "Build"),
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	implementations := index.Implementations()
	got := []string{
		implementations[0].PackagePath() + "." + implementations[0].FunctionName(),
		implementations[1].PackagePath() + "." + implementations[1].FunctionName(),
	}
	if !slices.Equal(got, []string{"example.com/app/alpha.Build", "example.com/dependency/zeta.New"}) {
		t.Fatalf("Implementation order = %v", got)
	}
	if implementations[0].Symbol().String() != got[0] || implementations[0].Symbol().PackagePath() != implementations[0].PackagePath() || implementations[0].Symbol().FunctionName() != implementations[0].FunctionName() {
		t.Fatalf("constructor Symbol = %#v for %#v", implementations[0].Symbol(), implementations[0])
	}
	if configuration, configured := implementations[0].Configuration(); configured || configuration.String() != "" || configuration.PackagePath() != "" || configuration.TypeName() != "" {
		t.Fatalf("configuration-free Implementation = %#v, %t", configuration, configured)
	}
	if found, exists := index.BySymbol(implementations[1].Symbol()); !exists || found.Symbol() != implementations[1].Symbol() {
		t.Fatalf("BySymbol = %#v, %t", found, exists)
	}
	missing, err := constructorsymbol.Parse("example.com/missing.New")
	if err != nil {
		t.Fatalf("Parse missing symbol: %v", err)
	}
	if found, exists := index.BySymbol(missing); exists || found.Symbol().String() != "" {
		t.Fatalf("BySymbol(missing) = %#v, %t", found, exists)
	}
	if !implementations[0].Local() || implementations[0].ModuleVersion() != "" || implementations[0].Source() != "example.com/app@local/alpha/new.go:4:6" {
		t.Fatalf("local Implementation = %#v, source %q", implementations[0], implementations[0].Source())
	}
	if implementations[1].Local() || implementations[1].ModuleVersion() != "v1.2.3" || implementations[1].Source() != "example.com/dependency@v1.2.3/zeta/new.go:4:6" {
		t.Fatalf("dependency Implementation = %#v, source %q", implementations[1], implementations[1].Source())
	}
	implementations[0] = implementationinventory.Implementation{}
	if index.Implementations()[0].FunctionName() != "Build" {
		t.Fatal("Implementations exposed mutable inventory storage")
	}
}

func TestBuildRejectsInconsistentCompiledPackageProvenance(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	_, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Declaration: parsed,
		Types:       types.NewPackage("example.com/other/service", "service"),
	}})
	if !errors.Is(err, implementationinventory.ErrInvalidInput) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsConstructorAbsentFromCompiledPackage(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	_, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Declaration: parsed,
		Types:       types.NewPackage("example.com/app/service", "service"),
	}})
	if !errors.Is(err, implementationinventory.ErrInvalidInput) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsDuplicateFullyQualifiedConstructorSymbol(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	compiled := compiledPackage("example.com/app/service", "service", "New")
	input := implementationinventory.Input{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Local:       true,
		Declaration: parsed,
		Types:       compiled,
	}
	_, err := implementationinventory.Build([]implementationinventory.Input{input, input})
	if !errors.Is(err, implementationinventory.ErrDuplicateSymbol) || !strings.Contains(err.Error(), "example.com/app/service.New") || !strings.Contains(err.Error(), "example.com/app@local/service/new.go:4:6") {
		t.Fatalf("Build error = %v", err)
	}
}

func declaration(t testing.TB, path, packageName, functionName, interfaceID string) implementationdecl.Declaration {
	t.Helper()
	declarations, err := implementationdecl.ParseFile(path, []byte("package "+packageName+"\n\n//plystra:implements "+interfaceID+"\nfunc "+functionName+"() {}\n"))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	return declarations[0]
}

func compiledPackage(path, name, function string) *types.Package {
	compiled := types.NewPackage(path, name)
	signature := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, function, signature))
	return compiled
}
