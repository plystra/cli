package implementationinventory_test

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestBuildSeparatesMixedResourceAndInterfaceDependencies(t *testing.T) {
	t.Parallel()
	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	resource := canonicalResource(t, "data.database/v1", "example.com/resources/database")
	other := canonicalResource(t, "data.other/v1", "example.com/resources/other")
	contract := resource.Types.Scope().Lookup("Resource").Type()
	alias := types.NewAlias(types.NewTypeName(token.NoPos, types.NewPackage("example.com/alias", "alias"), "Database", nil), contract)
	for _, hasConfig := range []bool{false, true} {
		t.Run(fmt.Sprintf("config=%t", hasConfig), func(t *testing.T) {
			compiled := compiledPackage("example.com/app/service", "service", "Unused", "Run")
			var parameters []*types.Var
			if hasConfig {
				name := types.NewTypeName(token.NoPos, compiled, "Config", nil)
				config := types.NewNamed(name, types.NewStruct(nil, nil), nil)
				compiled.Scope().Insert(name)
				parameters = append(parameters, types.NewVar(token.NoPos, compiled, "", config))
			}
			offset := len(parameters)
			iface := canonical.Types.Scope().Lookup("Interface").Type()
			parameters = append(parameters,
				types.NewVar(token.NoPos, compiled, "primary", contract),
				types.NewVar(token.NoPos, compiled, "operation", iface),
				types.NewVar(token.NoPos, compiled, "Primary", alias),
				types.NewVar(token.NoPos, compiled, "optional", optionalType(t, iface)),
				types.NewVar(token.NoPos, compiled, "_other", other.Types.Scope().Lookup("Resource").Type()),
			)
			input := resourceConsumerInput(t, compiled, canonical.ID.String(), parameters...)
			resources := []implementationinventory.ResourceInput{other, resource}
			before := types.TypeString(compiled.Scope().Lookup("New").Type(), nil)
			index, err := implementationinventory.Build([]implementationinventory.Input{input}, []implementationinventory.InterfaceInput{canonical}, resources)
			if err != nil {
				t.Fatal(err)
			}
			implementation := index.Implementations()[0]
			required, optional := implementation.RequiredInterfaces(), implementation.OptionalInterfaces()
			if len(required) != 1 || required[0].ID() != canonical.ID || required[0].ParameterName() != "operation" || required[0].ParameterPosition() != offset+2 {
				t.Fatalf("required Interfaces = %#v", required)
			}
			if len(optional) != 1 || optional[0].ID() != canonical.ID || optional[0].ParameterName() != "optional" || optional[0].ParameterPosition() != offset+4 {
				t.Fatalf("optional Interfaces = %#v", optional)
			}
			got := implementation.RequiredResources()
			if len(got) != 3 {
				t.Fatalf("Resources = %#v", got)
			}
			for n, want := range []struct {
				name     string
				position int
				contract implementationinventory.ResourceInput
			}{{"primary", offset + 1, resource}, {"Primary", offset + 3, resource}, {"_other", offset + 5, other}} {
				if got[n].ParameterName() != want.name || got[n].ParameterPosition() != want.position || got[n].ID() != want.contract.ID || got[n].PackagePath() != want.contract.PackagePath {
					t.Fatalf("Resource %d = %#v", n, got[n])
				}
			}
			second, err := implementationinventory.Build([]implementationinventory.Input{input}, []implementationinventory.InterfaceInput{canonical}, []implementationinventory.ResourceInput{resource, other})
			if err != nil || !reflect.DeepEqual(got, second.Implementations()[0].RequiredResources()) {
				t.Fatalf("Resource order depends on input order: %v", err)
			}
			got[0] = implementationinventory.RequiredResource{}
			resources[1] = implementationinventory.ResourceInput{}
			bySymbol, found := index.BySymbol(implementation.Symbol())
			if !found || bySymbol.RequiredResources()[0].ID() != resource.ID || implementation.RequiredResources()[0].ParameterName() != "primary" {
				t.Fatal("Resource accessor or input exposes mutable inventory storage")
			}
			if types.TypeString(compiled.Scope().Lookup("New").Type(), nil) != before {
				t.Fatal("Build mutated the constructor type")
			}
		})
	}
}

func TestBuildRejectsNoncanonicalResourceParameters(t *testing.T) {
	t.Parallel()
	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	resource := canonicalResource(t, "data.database/v1", "example.com/resources/database")
	contract := resource.Types.Scope().Lookup("Resource").Type()
	unshared := canonicalResource(t, "data.database/v1", resource.PackagePath)
	invisible := canonicalResource(t, "data.invisible/v1", "example.com/ordinary/database")
	local := types.NewPackage("example.com/app/service", "service")
	lookalike := types.NewNamed(types.NewTypeName(token.NoPos, local, "Copy", nil), contract.Underlying(), nil)
	resourceCopy := types.NewNamed(types.NewTypeName(token.NoPos, resource.Types, "Resource", nil), contract.Underlying(), nil)
	private := types.NewStruct([]*types.Var{types.NewField(token.NoPos, local, "Value", types.Typ[types.String], false)}, []string{`plystra-default:"PRIVATE_RESOURCE_DEFAULT"`})
	malformed := types.NewNamed(types.NewTypeName(token.NoPos, resource.Types, "Resource", nil), private, nil)
	for _, test := range []struct {
		name     string
		value    types.Type
		sentinel error
	}{
		{"pointer", types.NewPointer(contract), implementationinventory.ErrInvalidRequiredResource},
		{"slice", types.NewSlice(contract), implementationinventory.ErrInvalidRequiredResource},
		{"array", types.NewArray(contract, 1), implementationinventory.ErrInvalidRequiredResource},
		{"pointer-alias", types.NewAlias(types.NewTypeName(token.NoPos, local, "Pointer", nil), types.NewPointer(contract)), implementationinventory.ErrInvalidRequiredResource},
		{"unshared-go-identity", unshared.Types.Scope().Lookup("Resource").Type(), implementationinventory.ErrInvalidRequiredResource},
		{"same-package-copy", resourceCopy, implementationinventory.ErrInvalidRequiredResource},
		{"invisible", invisible.Types.Scope().Lookup("Resource").Type(), implementationinventory.ErrInvalidRequiredResource},
		{"struct-resource", malformed, implementationinventory.ErrInvalidRequiredResource},
		{"optional-resource", optionalType(t, contract), implementationinventory.ErrInvalidOptionalInterface},
		{"lookalike", lookalike, implementationinventory.ErrInvalidRequiredInterface},
		{"anonymous-interface", contract.Underlying(), implementationinventory.ErrInvalidRequiredInterface},
		{"anonymous-struct", private, implementationinventory.ErrInvalidRequiredInterface},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled := compiledPackage("example.com/app/service", "service", "Unused", "Run")
			input := resourceConsumerInput(t, compiled, canonical.ID.String(), types.NewVar(token.NoPos, compiled, "database", test.value))
			index, err := implementationinventory.Build([]implementationinventory.Input{input}, []implementationinventory.InterfaceInput{canonical}, []implementationinventory.ResourceInput{resource})
			if !errors.Is(err, test.sentinel) || len(index.Implementations()) != 0 {
				t.Fatalf("Build = %#v, %v", index, err)
			}
			var invalid *implementationinventory.ValidationError
			if !errors.As(err, &invalid) || invalid.ModulePath() != input.ModulePath || invalid.SourcePath() != "service/new.go" || invalid.Line() != 4 || invalid.Column() != 6 || !strings.Contains(err.Error(), "parameter 1") {
				t.Fatalf("missing constructor source: %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE_RESOURCE_DEFAULT") {
				t.Fatal("Resource rejection disclosed a private default")
			}
		})
	}
}

func TestBuildRejectsInvalidResourceInputs(t *testing.T) {
	t.Parallel()
	resource := canonicalResource(t, "data.database/v1", "example.com/resources/database")
	other := canonicalResource(t, "data.other/v1", "example.com/resources/other")
	duplicateID := other
	duplicateID.ID = resource.ID
	duplicatePackage := resource
	duplicatePackage.ID = other.ID
	for _, test := range []struct {
		name   string
		inputs []implementationinventory.ResourceInput
	}{
		{"empty-id", []implementationinventory.ResourceInput{{PackagePath: resource.PackagePath, Types: resource.Types}}},
		{"invalid-path", []implementationinventory.ResourceInput{{ID: resource.ID, PackagePath: "../database", Types: resource.Types}}},
		{"missing-types", []implementationinventory.ResourceInput{{ID: resource.ID, PackagePath: resource.PackagePath}}},
		{"wrong-package", []implementationinventory.ResourceInput{{ID: resource.ID, PackagePath: resource.PackagePath, Types: other.Types}}},
		{"missing-resource", []implementationinventory.ResourceInput{{ID: resource.ID, PackagePath: resource.PackagePath, Types: types.NewPackage(resource.PackagePath, "database")}}},
		{"duplicate-package", []implementationinventory.ResourceInput{resource, duplicatePackage}},
		{"duplicate-id", []implementationinventory.ResourceInput{resource, duplicateID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := implementationinventory.Build(nil, nil, test.inputs); !errors.Is(err, implementationinventory.ErrInvalidInput) {
				t.Fatalf("Build = %v", err)
			}
		})
	}
	for _, shape := range []string{"alias", "struct", "generic", "constraint", "variable", "foreign-owner"} {
		t.Run(shape, func(t *testing.T) {
			pkg := types.NewPackage(resource.PackagePath, "database")
			object := types.NewTypeName(token.NoPos, pkg, "Resource", nil)
			underlying := types.Type(types.NewInterfaceType(nil, nil).Complete())
			switch shape {
			case "struct":
				underlying = types.NewStruct(nil, nil)
			case "constraint":
				underlying = types.NewInterfaceType(nil, []types.Type{types.Typ[types.String]}).Complete()
			}
			switch shape {
			case "alias":
				types.NewAlias(object, resource.Types.Scope().Lookup("Resource").Type())
			case "variable":
				pkg.Scope().Insert(types.NewVar(token.NoPos, pkg, "Resource", underlying))
			case "foreign-owner":
				pkg.Scope().Insert(resource.Types.Scope().Lookup("Resource"))
			default:
				named := types.NewNamed(object, underlying, nil)
				if shape == "generic" {
					named.SetTypeParams([]*types.TypeParam{types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "T", nil), types.Universe.Lookup("any").Type())})
				}
			}
			pkg.Scope().Insert(object)
			if _, err := implementationinventory.Build(nil, nil, []implementationinventory.ResourceInput{{ID: resource.ID, PackagePath: pkg.Path(), Types: pkg}}); !errors.Is(err, implementationinventory.ErrInvalidInput) {
				t.Fatalf("Build = %v", err)
			}
		})
	}
}

func canonicalResource(t testing.TB, id, packagePath string) implementationinventory.ResourceInput {
	t.Helper()
	identifier, err := interfaceid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	compiled := types.NewPackage(packagePath, "database")
	method := types.NewFunc(token.NoPos, compiled, "Read", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	name := types.NewTypeName(token.NoPos, compiled, "Resource", nil)
	types.NewNamed(name, types.NewInterfaceType([]*types.Func{method}, nil).Complete(), nil)
	compiled.Scope().Insert(name)
	return implementationinventory.ResourceInput{ID: identifier, PackagePath: packagePath, Types: compiled}
}

func optionalType(t testing.TB, argument types.Type) types.Type {
	t.Helper()
	kernel := types.NewPackage("github.com/plystra/kernel", "plystra")
	optional := types.NewNamed(types.NewTypeName(token.NoPos, kernel, "Optional", nil), types.NewStruct(nil, nil), nil)
	optional.SetTypeParams([]*types.TypeParam{types.NewTypeParam(types.NewTypeName(token.NoPos, kernel, "T", nil), types.Universe.Lookup("any").Type())})
	instance, err := types.Instantiate(nil, optional, []types.Type{argument}, true)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func resourceConsumerInput(t testing.TB, compiled *types.Package, interfaceID string, parameters ...*types.Var) implementationinventory.Input {
	t.Helper()
	results := compiled.Scope().Lookup("Unused").Type().(*types.Signature).Results()
	compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, "New", types.NewSignatureType(nil, nil, nil, types.NewTuple(parameters...), results, false)))
	return implementationinventory.Input{
		ModulePath: "example.com/app", PackagePath: compiled.Path(), Types: compiled,
		Declaration: declaration(t, "service/new.go", compiled.Name(), "New", interfaceID),
	}
}
