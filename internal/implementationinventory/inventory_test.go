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
	"github.com/plystra/cli/internal/interfaceid"
)

func TestBuildOrdersAndProtectsDiscoveredImplementations(t *testing.T) {
	t.Parallel()

	alpha := declaration(t, "alpha/new.go", "alpha", "Build", "alpha.service.run/v1")
	zeta := declaration(t, "zeta/new.go", "zeta", "New", "zeta.service.run/v1")
	alphaInterface := canonicalInterface(t, "alpha.service.run/v1", "example.com/interfaces/alpha", "Run")
	zetaInterface := canonicalInterface(t, "zeta.service.run/v1", "example.com/interfaces/zeta", "Run")
	index, err := implementationinventory.Build([]implementationinventory.Input{
		{
			ModulePath:    "example.com/dependency",
			ModuleVersion: "v1.2.3",
			PackagePath:   "example.com/dependency/zeta",
			Declaration:   zeta,
			Types:         compiledPackage("example.com/dependency/zeta", "zeta", "New", "Run"),
		},
		{
			ModulePath:  "example.com/app",
			PackagePath: "example.com/app/alpha",
			Local:       true,
			Declaration: alpha,
			Types:       compiledPackage("example.com/app/alpha", "alpha", "Build", "Run"),
		},
	}, []implementationinventory.InterfaceInput{alphaInterface, zetaInterface})
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
	if concrete := implementations[0].ConcreteType(); concrete.String() != "*example.com/app/alpha.Service" || concrete.PackagePath() != "example.com/app/alpha" || concrete.TypeName() != "Service" {
		t.Fatalf("concrete type = %#v (%s)", concrete, concrete.String())
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
	}}, nil)
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
	}}, nil)
	if !errors.Is(err, implementationinventory.ErrInvalidInput) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsDuplicateFullyQualifiedConstructorSymbol(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	compiled := compiledPackage("example.com/app/service", "service", "New", "Run")
	input := implementationinventory.Input{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Local:       true,
		Declaration: parsed,
		Types:       compiled,
	}
	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	_, err := implementationinventory.Build([]implementationinventory.Input{input, input}, []implementationinventory.InterfaceInput{canonical})
	if !errors.Is(err, implementationinventory.ErrDuplicateSymbol) || !strings.Contains(err.Error(), "example.com/app/service.New") || !strings.Contains(err.Error(), "example.com/app@local/service/new.go:4:6") {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsInvalidCanonicalInterfaceInputs(t *testing.T) {
	t.Parallel()

	first, err := interfaceid.Parse("service.first.run/v1")
	if err != nil {
		t.Fatalf("Parse first Interface: %v", err)
	}
	second, err := interfaceid.Parse("service.second.run/v1")
	if err != nil {
		t.Fatalf("Parse second Interface: %v", err)
	}
	shared := canonicalInterface(t, "service.first.run/v1", "example.com/interfaces/shared", "Run")
	secondInSharedPackage := shared
	secondInSharedPackage.ID = second
	for _, test := range []struct {
		name   string
		inputs []implementationinventory.InterfaceInput
	}{
		{name: "empty ID", inputs: []implementationinventory.InterfaceInput{{PackagePath: "example.com/interfaces/first"}}},
		{name: "invalid package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "../interfaces"}}},
		{name: "missing compiled package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "example.com/interfaces/first"}}},
		{name: "mismatched compiled package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "example.com/interfaces/first", Types: shared.Types}}},
		{name: "duplicate package", inputs: []implementationinventory.InterfaceInput{shared, secondInSharedPackage}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := implementationinventory.Build(nil, test.inputs); !errors.Is(err, implementationinventory.ErrInvalidInput) {
				t.Fatalf("Build error = %v", err)
			}
		})
	}
}

func TestBuildRejectsMissingAndIncompatibleDeclaredInterfaces(t *testing.T) {
	t.Parallel()

	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	for _, test := range []struct {
		name        string
		interfaceID string
		methods     []string
		want        string
	}{
		{name: "not visible", interfaceID: "service.unknown.run/v1", methods: []string{"Run"}, want: "has no visible canonical Interface"},
		{name: "missing method", interfaceID: "service.operation.run/v1", want: "missing method Run"},
		{name: "wrong signature", interfaceID: "service.operation.run/v1", methods: []string{"WrongRun"}, want: "method Run has an incompatible signature"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parsed := declaration(t, "service/new.go", "service", "New", test.interfaceID)
			compiled := compiledPackage("example.com/app/service", "service", "New", test.methods...)
			_, err := implementationinventory.Build([]implementationinventory.Input{{
				ModulePath:  "example.com/app",
				PackagePath: "example.com/app/service",
				Declaration: parsed,
				Types:       compiled,
			}}, []implementationinventory.InterfaceInput{canonical})
			if !errors.Is(err, implementationinventory.ErrInvalidConformance) || !strings.Contains(err.Error(), "example.com/app/service.New") || !strings.Contains(err.Error(), "example.com/app@local/service/new.go:3:1") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build error = %v", err)
			}
		})
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

func canonicalInterface(t testing.TB, id, packagePath, method string) implementationinventory.InterfaceInput {
	t.Helper()
	identifier, err := interfaceid.Parse(id)
	if err != nil {
		t.Fatalf("Parse Interface ID: %v", err)
	}
	compiled := types.NewPackage(packagePath, "operationv1")
	operation := types.NewFunc(token.NoPos, compiled, method, types.NewSignatureType(nil, nil, nil, nil, nil, false))
	interfaceName := types.NewTypeName(token.NoPos, compiled, "Interface", nil)
	_ = types.NewNamed(interfaceName, types.NewInterfaceType([]*types.Func{operation}, nil).Complete(), nil)
	compiled.Scope().Insert(interfaceName)
	return implementationinventory.InterfaceInput{ID: identifier, PackagePath: packagePath, Types: compiled}
}

func compiledPackage(path, name, function string, methods ...string) *types.Package {
	compiled := types.NewPackage(path, name)
	serviceName := types.NewTypeName(token.NoPos, compiled, "Service", nil)
	service := types.NewNamed(serviceName, types.NewStruct(nil, nil), nil)
	compiled.Scope().Insert(serviceName)
	for _, methodName := range methods {
		name := methodName
		if methodName == "WrongRun" {
			name = "Run"
		}
		var parameters *types.Tuple
		if methodName == "WrongRun" {
			parameters = types.NewTuple(types.NewVar(token.NoPos, compiled, "value", types.Typ[types.String]))
		}
		receiver := types.NewVar(token.NoPos, compiled, "service", types.NewPointer(service))
		signature := types.NewSignatureType(receiver, nil, nil, parameters, nil, false)
		service.AddMethod(types.NewFunc(token.NoPos, compiled, name, signature))
	}
	results := types.NewTuple(
		types.NewVar(token.NoPos, compiled, "", types.NewPointer(service)),
		types.NewVar(token.NoPos, compiled, "", types.Universe.Lookup("error").Type()),
	)
	signature := types.NewSignatureType(nil, nil, nil, nil, results, false)
	compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, function, signature))
	return compiled
}
