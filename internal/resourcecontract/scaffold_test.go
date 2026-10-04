package resourcecontract_test

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/resourcecontract"
	"github.com/plystra/cli/internal/resourcedecl"
)

func checkedResource(t *testing.T, packagePath, source string) (resourcecontract.Contract, *types.Package) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "resource.go", "package api\n"+source, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check(packagePath, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	decls, err := resourcedecl.ParseFile("resource.go", []byte("package api\n"+source))
	if err != nil || len(decls) != 1 {
		t.Fatalf("Resource declarations = %v, %v", decls, err)
	}
	contract, err := resourcecontract.Validate(decls[0], pkg)
	if err != nil {
		t.Fatal(err)
	}
	return contract, pkg
}

func TestImplementationMethodsAreDeterministicAndCompilable(t *testing.T) {
	contract, resourcePackage := checkedResource(t, "example.com/resource/api", `
type hidden struct { value int }
type Public = hidden
type Box[T any] struct { Value T }
//plystra:resource data.database/v1
type Resource interface {
	Zed(first Public, values ...Box[Public]) (result map[string]chan<- Public, err error)
	Alpha() error
}`)

	methods, err := contract.ImplementationMethods("example.com/app/impl", func(pkg *types.Package) string {
		if pkg.Path() == resourcePackage.Path() {
			return "api"
		}
		return pkg.Name()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0].Name() != "Alpha" || methods[1].Name() != "Zed" {
		t.Fatalf("methods = %#v", methods)
	}
	if got, want := methods[0].Signature(), "func() error"; got != want {
		t.Fatalf("Alpha signature = %q, want %q", got, want)
	}
	if got, want := methods[1].Signature(), "func(api.Public, ...api.Box[api.Public]) (map[string]chan<- api.Public, error)"; got != want {
		t.Fatalf("Zed signature = %q, want %q", got, want)
	}
	if strings.Contains(methods[1].Signature(), "first ") || strings.Contains(methods[1].Signature(), "values ") || strings.Contains(methods[1].Signature(), "result ") || strings.Contains(methods[1].Signature(), "err ") {
		t.Fatal("top-level parameter or result names leaked into signature")
	}

	providerSource := `package impl
import api "example.com/resource/api"
type Service struct{}
func (Service) Alpha() error { return nil }
func (Service) Zed(api.Public, ...api.Box[api.Public]) (map[string]chan<- api.Public, error) { return nil, nil }
var _ api.Resource = Service{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "provider.go", providerSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&types.Config{Importer: mapImporter{packages: map[string]*types.Package{
		resourcePackage.Path(): resourcePackage,
	}}}).Check("example.com/app/impl", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("generated provider did not type-check: %v", err)
	}
}

func TestImplementationMethodsRejectsInaccessibleShapes(t *testing.T) {
	for _, test := range []struct {
		name, packagePath, target, source string
	}{
		{
			name:        "internal root",
			packagePath: "example.com/owner/internal/resource",
			target:      "example.com/consumer",
			source:      "//plystra:resource data.database/v1\ntype Resource interface{}",
		},
		{
			name:        "sealed method",
			packagePath: "example.com/resource/api",
			target:      "example.com/consumer",
			source:      "//plystra:resource data.database/v1\ntype Resource interface { Read(); sealed() }",
		},
		{
			name:        "private result type",
			packagePath: "example.com/resource/api",
			target:      "example.com/consumer",
			source:      "type hidden struct{}\n//plystra:resource data.database/v1\ntype Resource interface { Read() hidden }",
		},
		{
			name:        "private anonymous field",
			packagePath: "example.com/resource/api",
			target:      "example.com/consumer",
			source:      "//plystra:resource data.database/v1\ntype Resource interface { Read() struct { hidden int } }",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			contract, _ := checkedResource(t, test.packagePath, test.source)
			_, err := contract.ImplementationMethods(test.target, nil)
			if !errors.Is(err, resourcecontract.ErrImplementationMethods) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestImplementationMethodsAcceptsExportedAliasToPrivateShape(t *testing.T) {
	contract, _ := checkedResource(t, "example.com/resource/api", `
type hidden struct { value int }
type Public = hidden
//plystra:resource data.database/v1
type Resource interface { Read() Public }`)
	methods, err := contract.ImplementationMethods("example.com/consumer", func(pkg *types.Package) string {
		if pkg.Path() == "example.com/resource/api" {
			return "api"
		}
		return pkg.Name()
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := methods[0].Signature(), "func() api.Public"; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

type mapImporter struct {
	packages map[string]*types.Package
}

func (i mapImporter) Import(path string) (*types.Package, error) {
	if pkg := i.packages[path]; pkg != nil {
		return pkg, nil
	}
	return nil, errors.New("package not found: " + path)
}
