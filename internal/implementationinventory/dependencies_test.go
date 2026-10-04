package implementationinventory_test

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationinventory"
)

func TestBuildRequiresExactDependencyParameterIdentifiers(t *testing.T) {
	t.Parallel()
	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	contract := canonical.Types.Scope().Lookup("Interface").Type()
	resource := canonicalResource(t, "data.database/v1", "example.com/resources/database")
	kernel := types.NewPackage("github.com/plystra/kernel", "plystra")
	optional := types.NewNamed(types.NewTypeName(token.NoPos, kernel, "Optional", nil), types.NewStruct(nil, nil), nil)
	optional.SetTypeParams([]*types.TypeParam{types.NewTypeParam(types.NewTypeName(token.NoPos, kernel, "T", nil), types.Universe.Lookup("any").Type())})
	optionalContract, err := types.Instantiate(nil, optional, []types.Type{contract}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []struct {
		name string
		kind types.Type
		err  error
	}{
		{name: "required", kind: contract, err: implementationinventory.ErrInvalidRequiredInterface},
		{name: "optional", kind: optionalContract, err: implementationinventory.ErrInvalidOptionalInterface},
		{name: "resource", kind: resource.Types.Scope().Lookup("Resource").Type(), err: implementationinventory.ErrInvalidRequiredResource},
	} {
		for _, hasConfig := range []bool{false, true} {
			for _, name := range []string{"", "_", "bad-name", "dependency", "Dependency", "_dependency", "\u03b4"} {
				t.Run(fmt.Sprintf("%s/config=%t/name=%q", dependency.name, hasConfig, name), func(t *testing.T) {
					compiled := compiledPackage("example.com/app/service", "service", "Unused", "Run")
					var parameters []*types.Var
					if hasConfig {
						configName := types.NewTypeName(token.NoPos, compiled, "Config", nil)
						config := types.NewNamed(configName, types.NewStruct(nil, nil), nil)
						compiled.Scope().Insert(configName)
						parameters = append(parameters, types.NewVar(token.NoPos, compiled, "_", config))
					}
					parameters = append(parameters, types.NewVar(token.NoPos, compiled, name, dependency.kind))
					results := compiled.Scope().Lookup("Unused").Type().(*types.Signature).Results()
					compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, "New", types.NewSignatureType(nil, nil, nil, types.NewTuple(parameters...), results, false)))
					index, err := implementationinventory.Build([]implementationinventory.Input{{
						ModulePath: "example.com/app", PackagePath: compiled.Path(), Types: compiled,
						Declaration: declaration(t, "service/new.go", "service", "New", canonical.ID.String()),
					}}, []implementationinventory.InterfaceInput{canonical}, []implementationinventory.ResourceInput{resource})
					if name == "_" || !token.IsIdentifier(name) {
						if !errors.Is(err, dependency.err) || !strings.Contains(err.Error(), "explicit nonblank Go identifier") || len(index.Implementations()) != 0 {
							t.Fatalf("Build = %#v, %v", index, err)
						}
						var source *implementationinventory.ValidationError
						if !errors.As(err, &source) || source.SourcePath() != "service/new.go" || source.ModulePath() != "example.com/app" || source.Line() != 4 || source.Column() != 6 {
							t.Fatalf("missing constructor source: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					implementation := index.Implementations()[0]
					var gotName string
					var gotPosition int
					if dependency.name == "required" {
						got := implementation.RequiredInterfaces()[0]
						gotName, gotPosition = got.ParameterName(), got.ParameterPosition()
					} else if dependency.name == "optional" {
						got := implementation.OptionalInterfaces()[0]
						gotName, gotPosition = got.ParameterName(), got.ParameterPosition()
					} else {
						got := implementation.RequiredResources()[0]
						gotName, gotPosition = got.ParameterName(), got.ParameterPosition()
					}
					if gotName != name || gotPosition != len(parameters) {
						t.Fatalf("dependency identity = %q at %d, want %q at %d", gotName, gotPosition, name, len(parameters))
					}
				})
			}
		}
	}
}
