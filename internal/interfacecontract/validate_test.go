package interfacecontract_test

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
)

func TestValidateCanonicalMethodShape(t *testing.T) {
	t.Parallel()

	source := `package createv1

import ctx "context"

//plystra:interface order.create/v1
type Interface interface {
	Create(ctx.Context, CreateRequest) (CreateResponse, error)
}

type CreateRequest struct{}
type CreateResponse struct{}
`
	contract, err := validateSource(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if contract.ID().String() != "order.create/v1" || contract.PackagePath() != "example.com/interfaces/order/create/v1" || contract.MethodName() != "Create" || contract.RequestName() != "CreateRequest" || contract.ResponseName() != "CreateResponse" {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestValidateRejectsInvalidMethodShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		imports      string
		declarations string
		method       string
		want         string
	}{
		{name: "no method", imports: "", method: "", want: "exactly one operation method, found 0"},
		{name: "two methods", imports: `import "context"`, method: "Create(context.Context, Request) (Response, error)\nCancel(context.Context, Request) (Response, error)", want: "exactly one operation method, found 2"},
		{name: "unexported method", imports: `import "context"`, method: "create(context.Context, Request) (Response, error)", want: "operation method must be exported"},
		{name: "no parameters", imports: "", method: "Create() (Response, error)", want: "found 0 parameters"},
		{name: "one parameter", imports: `import "context"`, method: "Create(context.Context) (Response, error)", want: "found 1 parameters"},
		{name: "extra parameter", imports: `import "context"`, method: "Create(context.Context, Request, string) (Response, error)", want: "found 3 parameters"},
		{name: "wrong context", imports: "", method: "Create(string, Request) (Response, error)", want: "first parameter must be context.Context"},
		{name: "variadic request", imports: `import "context"`, method: "Create(context.Context, ...Request) (Response, error)", want: "must not be variadic"},
		{name: "request pointer", imports: `import "context"`, method: "Create(context.Context, *Request) (Response, error)", want: "request must be a defined exported same-package struct"},
		{name: "request scalar", imports: `import "context"`, method: "Create(context.Context, string) (Response, error)", want: "request must be a defined exported same-package struct"},
		{name: "request external", imports: "import (\"context\"; \"time\")", method: "Create(context.Context, time.Time) (Response, error)", want: "request must be a defined exported same-package struct"},
		{name: "request unexported", imports: `import "context"`, declarations: "type request struct{}", method: "Create(context.Context, request) (Response, error)", want: "request must be a defined exported same-package struct"},
		{name: "request generic", imports: `import "context"`, declarations: "type Generic[T any] struct{}", method: "Create(context.Context, Generic[string]) (Response, error)", want: "request must not be generic"},
		{name: "no results", imports: `import "context"`, method: "Create(context.Context, Request)", want: "found 0 results"},
		{name: "one result", imports: `import "context"`, method: "Create(context.Context, Request) Response", want: "found 1 results"},
		{name: "extra result", imports: `import "context"`, method: "Create(context.Context, Request) (Response, bool, error)", want: "found 3 results"},
		{name: "response pointer", imports: `import "context"`, method: "Create(context.Context, Request) (*Response, error)", want: "response must be a defined exported same-package struct"},
		{name: "response scalar", imports: `import "context"`, method: "Create(context.Context, Request) (string, error)", want: "response must be a defined exported same-package struct"},
		{name: "response external", imports: "import (\"context\"; \"time\")", method: "Create(context.Context, Request) (time.Time, error)", want: "response must be a defined exported same-package struct"},
		{name: "response unexported", imports: `import "context"`, declarations: "type response struct{}", method: "Create(context.Context, Request) (response, error)", want: "response must be a defined exported same-package struct"},
		{name: "response generic", imports: `import "context"`, declarations: "type Generic[T any] struct{}", method: "Create(context.Context, Request) (Generic[string], error)", want: "response must not be generic"},
		{name: "wrong error", imports: `import "context"`, method: "Create(context.Context, Request) (Response, bool)", want: "second result must be error"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := "type Request struct{}"
			response := "type Response struct{}"
			source := "package createv1\n" + test.imports + "\n//plystra:interface order.create/v1\ntype Interface interface {\n" + test.method + "\n}\n" + request + "\n" + response + "\n" + test.declarations + "\n"
			contract, err := validateSource(t, source)
			if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %#v, %v; want %q", contract, err, test.want)
			}
		})
	}
}

func TestValidateRejectsEmbeddedAndConstraintInterfaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "embedded",
			source: `package createv1
import "context"
type Operation interface { Create(context.Context, Request) (Response, error) }
//plystra:interface order.create/v1
type Interface interface { Operation }
type Request struct{}
type Response struct{}
`,
			want: "without embedding another interface",
		},
		{
			name: "constraint",
			source: `package createv1
//plystra:interface order.create/v1
type Interface interface { ~string }
`,
			want: "method-only Go interface",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, err := validateSource(t, test.source)
			if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %#v, %v; want %q", contract, err, test.want)
			}
		})
	}
}

func TestValidateRequiresCheckedPackage(t *testing.T) {
	t.Parallel()

	declarations, err := interfacedecl.ParseFile("interface.go", []byte("package test\n//plystra:interface order.create/v1\ntype Interface interface{}\n"))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	contract, err := interfacecontract.Validate(declarations[0], nil)
	if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" || !strings.Contains(err.Error(), "type-checked Go package is required") {
		t.Fatalf("Validate = %#v, %v", contract, err)
	}
}

func validateSource(t *testing.T, source string) (interfacecontract.Contract, error) {
	t.Helper()
	const path = "interfaces/order/create/v1/interface.go"
	declarations, err := interfacedecl.ParseFile(path, []byte(source))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(declarations) != 1 {
		t.Fatalf("declarations = %d, want 1", len(declarations))
	}

	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	checkedPackage, err := (&types.Config{Importer: importer.Default()}).Check(
		"example.com/interfaces/order/create/v1",
		files,
		[]*ast.File{file},
		nil,
	)
	if err != nil {
		t.Fatalf("types.Check: %v", err)
	}
	return interfacecontract.Validate(declarations[0], checkedPackage)
}
