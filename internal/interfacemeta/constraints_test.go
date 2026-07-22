package interfacemeta_test

import (
	"errors"
	"fmt"
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
	"github.com/plystra/cli/internal/interfacemeta"
)

func TestResolveConstraintTargetsUsesCanonicalFieldGraph(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	data := []byte(`constraints:
  response.accepted: {}
  request.items.score: {minimum: 0}
  "request.literal.name": {min_length: 1}
  request.by_id.name: {min_length: 1}
  request.detail.name: {pattern: '^[a-z]+$'}
  request.order_id: {max_length: 64}
  request.Legacy: {}
`)
	document, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		path      string
		goPath    string
		fieldName string
		kind      interfacecontract.TypeKind
	}{
		{path: "request.Legacy", goPath: "Request.Legacy", fieldName: "Legacy", kind: interfacecontract.TypeString},
		{path: "request.by_id.name", goPath: "Request.ByID.Name", fieldName: "Name", kind: interfacecontract.TypeString},
		{path: "request.detail.name", goPath: "Request.Detail.Name", fieldName: "Name", kind: interfacecontract.TypeString},
		{path: "request.items.score", goPath: "Request.Items.Score", fieldName: "Score", kind: interfacecontract.TypeInt64},
		{path: "request.literal.name", goPath: "Request.Literal", fieldName: "Literal", kind: interfacecontract.TypeString},
		{path: "request.order_id", goPath: "Request.OrderID", fieldName: "OrderID", kind: interfacecontract.TypeString},
		{path: "response.accepted", goPath: "Response.Accepted", fieldName: "Accepted", kind: interfacecontract.TypeBoolean},
	}
	if len(targets) != len(want) {
		t.Fatalf("targets = %#v", targets)
	}
	for index, expected := range want {
		field := targets[index].Field()
		if targets[index].Path() != expected.path || targets[index].GoPath() != expected.goPath || field.Name() != expected.fieldName || field.Type().Kind() != expected.kind {
			t.Fatalf("target %d = path %q Go path %q field %#v", index, targets[index].Path(), targets[index].GoPath(), field)
		}
	}
	targets[0] = interfacemeta.ConstraintTarget{}
	again, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil || again[0].Path() != "request.Legacy" {
		t.Fatalf("constraint targets exposed mutable storage: %#v, %v", again, err)
	}
}

func TestResolveConstraintTargetsRejectsUnknownOrNonTraversablePaths(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "missing root", path: "order_id", want: "must begin with request. or response."},
		{name: "root only", path: "request", want: "must begin with request. or response."},
		{name: "empty remainder", path: "response.", want: "must begin with request. or response."},
		{name: "unknown root", path: "input.order_id", want: "must begin with request. or response."},
		{name: "unknown request field", path: "request.missing", want: "does not identify a canonical"},
		{name: "unknown response field", path: "response.missing", want: "does not identify a canonical"},
		{name: "scalar traversal", path: "request.order_id.value", want: "does not identify a canonical"},
		{name: "repeated scalar traversal", path: "request.tags.value", want: "does not identify a canonical"},
		{name: "map scalar traversal", path: "request.labels.value", want: "does not identify a canonical"},
		{name: "Go name instead of JSON name", path: "request.OrderID", want: "does not identify a canonical"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte("constraints:\n  " + strconv.Quote(test.path) + ": {}\n")
			document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", data)
			if err != nil {
				t.Fatal(err)
			}
			targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(targets) != 0 || !strings.Contains(err.Error(), "interface.yaml:2:3") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveConstraintTargets(%q) = %#v, %v", test.path, targets, err)
			}
		})
	}
}

func TestResolveConstraintTargetsRejectsAmbiguousDottedJSONNames(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, ambiguousConstraintInterfaceSource)
	document, err := interfacemeta.ParseFile("interfaces/ambiguous/interface.yaml", []byte("constraints:\n  request.nested.leaf: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(targets) != 0 || !strings.Contains(err.Error(), "interface.yaml:2:3") || !strings.Contains(err.Error(), "Request.Direct") || !strings.Contains(err.Error(), "Request.Nested.Leaf") || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ResolveConstraintTargets = %#v, %v", targets, err)
	}
}

func TestParseFileRejectsMalformedConstraintContainers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null constraints", data: "constraints: null\n", location: "interface.yaml:1:14", want: "constraints must be a mapping"},
		{name: "sequence constraints", data: "constraints: []\n", location: "interface.yaml:1:14", want: "constraints must be a mapping"},
		{name: "scalar constraints", data: "constraints: request.value\n", location: "interface.yaml:1:14", want: "constraints must be a mapping"},
		{name: "null rules", data: "constraints:\n  request.value: null\n", location: "interface.yaml:2:18", want: "must be a mapping"},
		{name: "sequence rules", data: "constraints:\n  request.value: []\n", location: "interface.yaml:2:18", want: "must be a mapping"},
		{name: "scalar rules", data: "constraints:\n  request.value: invalid\n", location: "interface.yaml:2:18", want: "must be a mapping"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidConstraints) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v", document, err)
			}
		})
	}
}

func TestResolveConstraintTargetsRequiresCanonicalContract(t *testing.T) {
	t.Parallel()

	document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte("constraints:\n  request.value: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := interfacemeta.ResolveConstraintTargets(document, interfacecontract.Contract{})
	if !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(targets) != 0 || !strings.Contains(err.Error(), "canonical Interface contract is required") {
		t.Fatalf("ResolveConstraintTargets = %#v, %v", targets, err)
	}
}

func FuzzResolveConstraintTargets(f *testing.F) {
	contract := constraintTestContract(f, canonicalConstraintInterfaceSource)
	for _, seed := range []string{"request.order_id", "request.detail.name", "request.items.score", "response.accepted", "request.missing", "request.literal.name", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, fieldPath string) {
		if len(fieldPath) > 1024 {
			t.Skip()
		}
		data := []byte(fmt.Sprintf("constraints:\n  %s: {}\n", strconv.Quote(fieldPath)))
		document, err := interfacemeta.ParseFile("interfaces/fuzz/interface.yaml", data)
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalid) {
				t.Fatalf("ParseFile returned unexpected error: %v", err)
			}
			return
		}
		targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(targets) != 0 {
				t.Fatalf("ResolveConstraintTargets returned inconsistent error: %#v, %v", targets, err)
			}
			return
		}
		if len(targets) != 1 || targets[0].Path() != fieldPath || targets[0].GoPath() == "" || targets[0].Field().Name() == "" {
			t.Fatalf("ResolveConstraintTargets = %#v", targets)
		}
	})
}

func constraintTestContract(t testing.TB, source string) interfacecontract.Contract {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "interface.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check("example.com/constraints", fileSet, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

const canonicalConstraintInterfaceSource = `package contract

import "context"

//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }

type Detail struct {
	Name string ` + "`plystra:\"1\" json:\"name\"`" + `
	Score int64 ` + "`plystra:\"2\" json:\"score\"`" + `
}

type Request struct {
	OrderID string ` + "`plystra:\"1\" json:\"order_id\"`" + `
	Detail Detail ` + "`plystra:\"2\" json:\"detail\"`" + `
	Items []Detail ` + "`plystra:\"3\" json:\"items\"`" + `
	ByID map[string]Detail ` + "`plystra:\"4\" json:\"by_id\"`" + `
	Literal string ` + "`plystra:\"5\" json:\"literal.name\"`" + `
	Legacy string ` + "`plystra:\"6\"`" + `
	Tags []string ` + "`plystra:\"7\" json:\"tags\"`" + `
	Labels map[string]string ` + "`plystra:\"8\" json:\"labels\"`" + `
}

type Response struct {
	Accepted bool ` + "`plystra:\"1\" json:\"accepted\"`" + `
}
`

const ambiguousConstraintInterfaceSource = `package contract

import "context"

//plystra:interface ambiguous.path/v1
type Interface interface { Check(context.Context, Request) (Response, error) }

type Nested struct { Leaf string ` + "`plystra:\"1\" json:\"leaf\"`" + ` }

type Request struct {
	Direct string ` + "`plystra:\"1\" json:\"nested.leaf\"`" + `
	Nested Nested ` + "`plystra:\"2\" json:\"nested\"`" + `
}

type Response struct { Accepted bool ` + "`plystra:\"1\" json:\"accepted\"`" + ` }
`
