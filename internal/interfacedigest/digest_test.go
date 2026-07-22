package interfacedigest

import (
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfacemeta"
)

func TestCalculateUsesVersionedCanonicalContractRepresentation(t *testing.T) {
	t.Parallel()

	contract, metadata, constraints := digestFixture(t, canonicalDigestSource, canonicalDigestMetadata)
	canonical, err := canonicalize(contract, metadata, constraints)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := `{"schema":"plystra.interface.contract/v1","interface_id":"order.create/v1","method":{"name":"Create","context_type":"context.Context","request_type":"Request","response_type":"Response","error_type":"error"},"messages":[{"name":"Detail","fields":[{"number":1,"go_name":"Total","json_name":"total","required":false,"type":"int64"}]},{"name":"Request","fields":[{"number":1,"go_name":"Detail","json_name":"detail","required":false,"type":"message:Detail"},{"number":2,"go_name":"OrderID","json_name":"order_id","required":true,"type":"string"},{"number":3,"go_name":"Tags","json_name":"tags","required":false,"type":"repeated\u003cstring\u003e"}]},{"name":"Response","fields":[{"number":1,"go_name":"Accepted","json_name":"accepted","required":false,"type":"boolean"}]}],"semantics":{"kind":"command"},"semantic_errors":["invalid_order","unavailable"],"constraints":[{"path":"request.detail.total","rules":[{"name":"maximum","value":"10"},{"name":"minimum","value":"-5"}]},{"path":"request.order_id","rules":[{"name":"max_length","value":"64"},{"name":"min_length","value":"1"},{"name":"pattern","value":"^ord_[0-9]+$"}]},{"path":"request.tags","rules":[{"name":"max_items","value":"5"},{"name":"min_items","value":"1"}]}],"behavioral_conformance":{"package":"./conformance"}}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical contract =\n%s\nwant:\n%s", canonical, wantCanonical)
	}
	digest, err := Calculate(contract, metadata, constraints)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:bdc016cdbb62cf0a2f379fa33145ed3318002d8c243dc25d3652b4a3fc8f9ac5" {
		t.Fatalf("digest = %q", digest)
	}
}

func TestCalculateNormalizesEquivalentAuthoredForms(t *testing.T) {
	t.Parallel()

	firstContract, firstMetadata, firstConstraints := digestFixture(t, canonicalDigestSource, canonicalDigestMetadata)
	first, err := Calculate(firstContract, firstMetadata, firstConstraints)
	if err != nil {
		t.Fatal(err)
	}

	reorderedSource := strings.ReplaceAll(canonicalDigestSource, `type Request struct {
	OrderID string   `+"`plystra:\"2,required\" json:\"order_id\"`"+`
	Detail  Detail   `+"`plystra:\"1\" json:\"detail\"`"+`
	Tags    []string `+"`plystra:\"3\" json:\"tags\"`"+`
}`, `type Request struct {
	Tags    []string `+"`plystra:\"3\" json:\"tags\"`"+`
	Detail  Detail   `+"`plystra:\"1\" json:\"detail\"`"+`
	OrderID string   `+"`plystra:\"2,required\" json:\"order_id\"`"+`
}`)
	equivalentMetadata := `
conformance: {package: "./conformance"}
constraints:
  request.tags: {max_items: 5, min_items: 1}
  request.order_id:
    pattern: '^ord_[0-9]+$'
    min_length: 1
    max_length: 64
  request.detail.total: {maximum: 10, minimum: -5}
errors:
  - {description: A different documentation sentence., code: unavailable}
  - {code: invalid_order}
semantics: {kind: command}
description: Changed documentation is excluded.
examples:
  - name: another-example
    request: {order_id: ord_2, detail: {total: 1}, tags: []}
    response: {accepted: false}
deprecation:
  message: Different lifecycle documentation.
  since: later
`
	secondContract, secondMetadata, secondConstraints := digestFixture(t, reorderedSource, equivalentMetadata)
	slices.Reverse(secondConstraints)
	second, err := Calculate(secondContract, secondMetadata, secondConstraints)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("equivalent authored forms changed digest: %s != %s", second, first)
	}
}

func TestCalculateChangesForEveryExactCompatibilityInput(t *testing.T) {
	t.Parallel()

	baseSource := `package contract

import "context"

//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }

type Detail struct { Total int64 ` + "`plystra:\"1\" json:\"total\"`" + ` }
type Request struct {
	OrderID string ` + "`plystra:\"1,required\" json:\"order_id\"`" + `
	Detail Detail ` + "`plystra:\"2\" json:\"detail\"`" + `
}
type Response struct { Accepted bool ` + "`plystra:\"1\" json:\"accepted\"`" + ` }
`
	baseMetadata := "semantics: {kind: command}\nerrors: [{code: invalid_order}]\nconstraints:\n  request.order_id: {min_length: 1}\nconformance: {package: ./conformance}\n"
	baseContract, metadata, constraints := digestFixture(t, baseSource, baseMetadata)
	base, err := Calculate(baseContract, metadata, constraints)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		source   string
		metadata string
	}{
		{name: "Interface ID", source: strings.Replace(baseSource, "order.create/v1", "order.submit/v1", 1), metadata: baseMetadata},
		{name: "method name", source: strings.Replace(baseSource, "Create(context.Context", "Submit(context.Context", 1), metadata: baseMetadata},
		{name: "request type", source: strings.ReplaceAll(baseSource, "Request", "CreateRequest"), metadata: baseMetadata},
		{name: "response type", source: strings.ReplaceAll(baseSource, "Response", "CreateResponse"), metadata: baseMetadata},
		{name: "Go field name", source: strings.Replace(baseSource, "OrderID string", "Identifier string", 1), metadata: baseMetadata},
		{name: "field number", source: strings.Replace(baseSource, `plystra:"1,required"`, `plystra:"7,required"`, 1), metadata: baseMetadata},
		{name: "required marker", source: strings.Replace(baseSource, `plystra:"1,required"`, `plystra:"1"`, 1), metadata: baseMetadata},
		{name: "JSON name", source: strings.Replace(baseSource, `json:"order_id"`, `json:"id"`, 1), metadata: strings.Replace(baseMetadata, "request.order_id", "request.id", 1)},
		{name: "field type", source: strings.Replace(baseSource, "OrderID string", "OrderID []byte", 1), metadata: baseMetadata},
		{name: "nested message graph", source: strings.Replace(baseSource, "Total int64", "Total int32", 1), metadata: baseMetadata},
		{name: "operation semantics", source: baseSource, metadata: strings.Replace(baseMetadata, "kind: command", "kind: query", 1)},
		{name: "semantic error code", source: baseSource, metadata: strings.Replace(baseMetadata, "invalid_order", "order_rejected", 1)},
		{name: "constraint rule", source: baseSource, metadata: strings.Replace(baseMetadata, "min_length: 1", "min_length: 2", 1)},
		{name: "Behavioral Conformance declaration", source: baseSource, metadata: strings.Replace(baseMetadata, "conformance: {package: ./conformance}\n", "", 1)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, metadata, constraints := digestFixture(t, test.source, test.metadata)
			digest, err := Calculate(contract, metadata, constraints)
			if err != nil {
				t.Fatal(err)
			}
			if digest == base {
				t.Fatalf("%s change preserved contract digest %s", test.name, digest)
			}
		})
	}
}

func TestCalculateIgnoresDocumentationOnlyMetadata(t *testing.T) {
	t.Parallel()

	source := `package contract
import "context"
//plystra:interface records.read/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct { ID string ` + "`plystra:\"1\" json:\"id\"`" + ` }
type Response struct { Value string ` + "`plystra:\"1\" json:\"value\"`" + ` }
`
	baseMetadata := "errors: [{code: not_found, description: First description.}]\n"
	changedMetadata := `description: Different Interface description.
errors:
  - code: not_found
    description: Changed error description.
examples:
  - name: missing
    request: {id: absent}
    error: not_found
deprecation:
  message: Use another Interface later.
  since: next-release
`
	baseContract, baseDocument, baseConstraints := digestFixture(t, source, baseMetadata)
	base, err := Calculate(baseContract, baseDocument, baseConstraints)
	if err != nil {
		t.Fatal(err)
	}
	changedContract, changedDocument, changedConstraints := digestFixture(t, source, changedMetadata)
	changed, err := Calculate(changedContract, changedDocument, changedConstraints)
	if err != nil {
		t.Fatal(err)
	}
	if changed != base {
		t.Fatalf("documentation-only metadata changed exact contract digest: %s != %s", changed, base)
	}
}

func TestCalculateRejectsIncompleteContract(t *testing.T) {
	t.Parallel()

	digest, err := Calculate(interfacecontract.Contract{}, interfacemeta.Document{}, nil)
	if !errors.Is(err, ErrInvalid) || digest != "" {
		t.Fatalf("Calculate = %q, %v", digest, err)
	}
}

func digestFixture(t *testing.T, source, metadataSource string) (interfacecontract.Contract, interfacemeta.Document, []interfacemeta.ConstraintTarget) {
	t.Helper()
	const sourcePath = "interfaces/order/create/v1/interface.go"
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, sourcePath, source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check("example.com/contracts/order/create/v1", fileSet, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	declarations, err := interfacedecl.ParseFile(sourcePath, []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatal(err)
	}
	document, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", []byte(metadataSource))
	if err != nil {
		t.Fatal(err)
	}
	constraints, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	return contract, document, constraints
}

const canonicalDigestSource = `package contract

import "context"

//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }

type Detail struct {
	Total int64 ` + "`plystra:\"1\" json:\"total\"`" + `
}

type Request struct {
	OrderID string   ` + "`plystra:\"2,required\" json:\"order_id\"`" + `
	Detail  Detail   ` + "`plystra:\"1\" json:\"detail\"`" + `
	Tags    []string ` + "`plystra:\"3\" json:\"tags\"`" + `
}

type Response struct {
	Accepted bool ` + "`plystra:\"1\" json:\"accepted\"`" + `
}
`

const canonicalDigestMetadata = `description: Creates an order.
semantics:
  kind: command
errors:
  - code: unavailable
    description: The service is unavailable.
  - code: invalid_order
constraints:
  request.tags:
    min_items: 1
    max_items: 5
  request.order_id:
    pattern: ^ord_[0-9]+$
    max_length: 64
    min_length: 1
  response.accepted: {}
  request.detail.total:
    minimum: -5
    maximum: 10
examples:
  - name: accepted
    request: {order_id: ord_1, detail: {total: 1}, tags: [new]}
    response: {accepted: true}
deprecation:
  message: This Interface will be replaced.
  since: next-release
conformance:
  package: ./conformance
`
