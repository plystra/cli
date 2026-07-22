package interfacecontract_test

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
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

func TestValidateNormalizesFieldMetadata(t *testing.T) {
	t.Parallel()

	source := `package createv1
import "context"
//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}
type Request struct {
	OrderID string ` + "`json:\"order_id,omitempty\" plystra:\"7,required\"`" + `
	Note string ` + "`json:\",omitempty\" plystra:\"2\"`" + `
}
type Response struct {
	Accepted bool ` + "`json:\"accepted\" plystra:\"1\"`" + `
}
`
	contract, err := validateSource(t, source)
	if err != nil {
		t.Fatal(err)
	}
	request := contract.RequestFields()
	if len(request) != 2 || request[0].Name() != "Note" || request[0].Number() != 2 || request[0].Required() || request[0].HasExplicitJSONName() || request[0].JSONName() != "" || request[1].Name() != "OrderID" || request[1].Number() != 7 || !request[1].Required() || !request[1].HasExplicitJSONName() || request[1].JSONName() != "order_id" {
		t.Fatalf("request fields = %#v", request)
	}
	response := contract.ResponseFields()
	if len(response) != 1 || response[0].Name() != "Accepted" || response[0].Number() != 1 || response[0].Required() || !response[0].HasExplicitJSONName() || response[0].JSONName() != "accepted" {
		t.Fatalf("response fields = %#v", response)
	}

	request[0] = response[0]
	response[0] = contract.RequestFields()[1]
	if contract.RequestFields()[0].Name() != "Note" || contract.ResponseFields()[0].Name() != "Accepted" {
		t.Fatal("field accessors exposed mutable contract storage")
	}
}

func TestValidateRejectsInvalidFieldMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		declarations string
		request      string
		response     string
		want         string
	}{
		{name: "missing tag", request: "Value string", want: "missing plystra field-number tag"},
		{name: "zero", request: "Value string `plystra:\"0\"`", want: "canonical positive decimal integer"},
		{name: "leading zero", request: "Value string `plystra:\"01\"`", want: "canonical positive decimal integer"},
		{name: "negative", request: "Value string `plystra:\"-1\"`", want: "canonical positive decimal integer"},
		{name: "whitespace", request: "Value string `plystra:\" 1\"`", want: "canonical positive decimal integer"},
		{name: "overflow", request: "Value string `plystra:\"18446744073709551616\"`", want: "canonical positive decimal integer"},
		{name: "empty option", request: "Value string `plystra:\"1,\"`", want: "unknown plystra field option"},
		{name: "unknown option", request: "Value string `plystra:\"1,optional\"`", want: "unknown plystra field option"},
		{name: "duplicate required", request: "Value string `plystra:\"1,required,required\"`", want: "duplicate required field option"},
		{name: "duplicate number", request: "First string `plystra:\"2\"`; Second string `plystra:\"2\"`", want: "duplicate field number 2"},
		{name: "malformed struct tag", request: "Value string `plystra:\"1\"json:\"value\"`", want: "invalid Go struct tag syntax"},
		{name: "duplicate plystra tag", request: "Value string `plystra:\"1\" plystra:\"2\"`", want: "duplicate Go struct tag key \"plystra\""},
		{name: "duplicate JSON tag", request: "Value string `json:\"first\" json:\"second\" plystra:\"1\"`", want: "duplicate Go struct tag key \"json\""},
		{name: "unexported", request: "value string `plystra:\"1\"`", want: "must be an exported named field"},
		{name: "embedded", declarations: "type Embedded struct{}", request: "Embedded `plystra:\"1\"`", want: "must be an exported named field"},
		{name: "JSON omission", request: "Value string `json:\"-\" plystra:\"1\"`", want: "cannot be omitted from JSON"},
		{name: "invalid JSON name", request: "Value string `json:\"value🙂\" plystra:\"1\"`", want: "invalid explicit JSON name"},
		{name: "duplicate explicit JSON", request: "First string `json:\"same\" plystra:\"1\"`; Second string `json:\"same\" plystra:\"2\"`", want: "duplicate JSON name \"same\""},
		{name: "explicit and default JSON collision", request: "First string `json:\"Second\" plystra:\"1\"`; Second string `plystra:\"2\"`", want: "duplicate JSON name \"Second\""},
		{name: "response field", response: "value string `plystra:\"1\"`", want: "Interface response field value"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := test.request
			if request == "" {
				request = "Value string `plystra:\"1\"`"
			}
			response := test.response
			if response == "" {
				response = "Value string `plystra:\"1\"`"
			}
			source := "package createv1\nimport \"context\"\n//plystra:interface order.create/v1\ntype Interface interface { Create(context.Context, Request) (Response, error) }\ntype Request struct { " + request + " }\ntype Response struct { " + response + " }\n" + test.declarations + "\n"
			contract, err := validateSource(t, source)
			if !errors.Is(err, interfacecontract.ErrInvalid) || len(contract.RequestFields()) != 0 || len(contract.ResponseFields()) != 0 || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %#v, %v; want %q", contract, err, test.want)
			}
		})
	}
}

func FuzzValidateFieldMetadata(f *testing.F) {
	for _, tag := range []string{
		`plystra:"1"`,
		`json:"value" plystra:"7,required"`,
		`json:",omitempty" plystra:"2"`,
		`plystra:"0"`,
		`plystra:"1" plystra:"2"`,
		`json:"-" plystra:"1"`,
	} {
		f.Add(tag)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		if len(tag) > 512 {
			t.Skip()
		}
		source := "package createv1\nimport \"context\"\n//plystra:interface order.create/v1\ntype Interface interface { Create(context.Context, Request) (Response, error) }\ntype Request struct { Value string " + strconv.Quote(tag) + " }\ntype Response struct { Value string `plystra:\"1\"` }\n"
		contract, err := validateSource(t, source)
		if err != nil {
			if !errors.Is(err, interfacecontract.ErrInvalid) {
				t.Fatalf("Validate returned unexpected error: %v", err)
			}
			return
		}
		fields := contract.RequestFields()
		if len(fields) != 1 || fields[0].Name() != "Value" || fields[0].Number() == 0 {
			t.Fatalf("request fields = %#v", fields)
		}
	})
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
