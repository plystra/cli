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
	canonical, err := canonicalizeContract(contract, metadata, constraints)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := `{"schema":"plystra.interface.contract/v1","interface_id":"order.create/v1","method":{"name":"Create","context_type":"context.Context","request_type":"Request","response_type":"Response","error_type":"error"},"messages":[{"name":"Detail","fields":[{"number":1,"go_name":"Total","json_name":"total","required":false,"type":"int64"}]},{"name":"Request","fields":[{"number":1,"go_name":"Detail","json_name":"detail","required":false,"type":"message:Detail"},{"number":2,"go_name":"OrderID","json_name":"order_id","required":true,"type":"string"},{"number":3,"go_name":"Tags","json_name":"tags","required":false,"type":"repeated\u003cstring\u003e"}]},{"name":"Response","fields":[{"number":1,"go_name":"Accepted","json_name":"accepted","required":false,"type":"boolean"}]}],"semantics":{"kind":"command"},"semantic_errors":["invalid_order","unavailable"],"constraints":[{"path":"request.detail.total","rules":[{"name":"maximum","value":"10"},{"name":"minimum","value":"-5"}]},{"path":"request.order_id","rules":[{"name":"max_length","value":"64"},{"name":"min_length","value":"1"},{"name":"pattern","value":"^ord_[0-9]+$"}]},{"path":"request.tags","rules":[{"name":"max_items","value":"5"},{"name":"min_items","value":"1"}]}],"behavioral_conformance":{"package":"./conformance"}}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical contract =\n%s\nwant:\n%s", canonical, wantCanonical)
	}
	digest, err := CalculateContract(contract, metadata, constraints)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:bdc016cdbb62cf0a2f379fa33145ed3318002d8c243dc25d3652b4a3fc8f9ac5" {
		t.Fatalf("digest = %q", digest)
	}
}

func TestCalculateUsesSeparateVersionedDocumentationAndExampleRepresentations(t *testing.T) {
	t.Parallel()

	contract, metadata, _ := digestFixture(t, canonicalDigestSource, canonicalDigestMetadata)
	examples, err := interfacemeta.ResolveExamples(metadata, contract)
	if err != nil {
		t.Fatal(err)
	}

	documentation, err := canonicalizeDocumentation(contract, metadata)
	if err != nil {
		t.Fatal(err)
	}
	wantDocumentation := `{"schema":"plystra.interface.documentation/v1","interface_id":"order.create/v1","description":"Creates an order.","semantic_error_descriptions":[{"code":"unavailable","description":"The service is unavailable."}],"deprecation":{"message":"This Interface will be replaced.","replacement":null,"since":"next-release"}}`
	if string(documentation) != wantDocumentation {
		t.Fatalf("canonical documentation =\n%s\nwant:\n%s", documentation, wantDocumentation)
	}
	documentationDigest, err := CalculateDocumentation(contract, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if documentationDigest != "sha256:cf45e011fa06f9f3168be5f0bbe10d2f924e55c8887a98e960a01ebbebf69303" {
		t.Fatalf("documentation digest = %q", documentationDigest)
	}

	canonicalExamples, err := canonicalizeExamples(contract, examples)
	if err != nil {
		t.Fatal(err)
	}
	wantExamples := `{"schema":"plystra.interface.examples/v1","interface_id":"order.create/v1","examples":[{"name":"accepted","request":{"detail":{"total":1},"order_id":"ord_1","tags":["new"]},"response":{"accepted":true}},{"name":"unavailable","request":{"detail":{"total":0},"order_id":"ord_9","tags":["retry"]},"error_code":"unavailable"}]}`
	if string(canonicalExamples) != wantExamples {
		t.Fatalf("canonical examples =\n%s\nwant:\n%s", canonicalExamples, wantExamples)
	}
	exampleDigest, err := CalculateExamples(contract, examples)
	if err != nil {
		t.Fatal(err)
	}
	if exampleDigest != "sha256:8cd238f8e93ed9c0aa6ffce32312e0ef988c9923e2aacf82dafc8468daab49c3" {
		t.Fatalf("example digest = %q", exampleDigest)
	}
	if documentationDigest == exampleDigest {
		t.Fatal("domain-separated documentation and example representations share a digest")
	}
}

func TestCalculateNormalizesEquivalentAuthoredForms(t *testing.T) {
	t.Parallel()

	firstContract, firstMetadata, firstConstraints := digestFixture(t, canonicalDigestSource, canonicalDigestMetadata)
	first, err := CalculateContract(firstContract, firstMetadata, firstConstraints)
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
	second, err := CalculateContract(secondContract, secondMetadata, secondConstraints)
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
	base, err := CalculateContract(baseContract, metadata, constraints)
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
			digest, err := CalculateContract(contract, metadata, constraints)
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
	base, err := CalculateContract(baseContract, baseDocument, baseConstraints)
	if err != nil {
		t.Fatal(err)
	}
	changedContract, changedDocument, changedConstraints := digestFixture(t, source, changedMetadata)
	changed, err := CalculateContract(changedContract, changedDocument, changedConstraints)
	if err != nil {
		t.Fatal(err)
	}
	if changed != base {
		t.Fatalf("documentation-only metadata changed exact contract digest: %s != %s", changed, base)
	}
}

func TestCalculateClassifiesContractDocumentationAndExampleChanges(t *testing.T) {
	t.Parallel()

	source := `package contract
import "context"
//plystra:interface records.read/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct { ID string ` + "`plystra:\"1,required\" json:\"id\"`" + ` }
type Response struct { Value string ` + "`plystra:\"1\" json:\"value\"`" + ` }
`
	baseMetadata := `description: Reads a record.
semantics: {kind: query}
errors:
  - code: not_found
    description: The record does not exist.
constraints:
  request.id: {min_length: 1}
examples:
  - name: found
    request: {id: rec_1}
    response: {value: first}
deprecation:
  message: Use records.lookup/v1 later.
  since: next-release
`
	type digestSet struct {
		contract      string
		documentation string
		examples      string
	}
	calculate := func(t *testing.T, source, metadataSource string) digestSet {
		t.Helper()
		contract, metadata, constraints := digestFixture(t, source, metadataSource)
		examples, err := interfacemeta.ResolveExamples(metadata, contract)
		if err != nil {
			t.Fatal(err)
		}
		contractDigest, err := CalculateContract(contract, metadata, constraints)
		if err != nil {
			t.Fatal(err)
		}
		documentationDigest, err := CalculateDocumentation(contract, metadata)
		if err != nil {
			t.Fatal(err)
		}
		exampleDigest, err := CalculateExamples(contract, examples)
		if err != nil {
			t.Fatal(err)
		}
		return digestSet{contract: contractDigest, documentation: documentationDigest, examples: exampleDigest}
	}

	base := calculate(t, source, baseMetadata)
	contractChange := calculate(t, source, strings.Replace(baseMetadata, "min_length: 1", "min_length: 2", 1))
	if contractChange.contract == base.contract || contractChange.documentation != base.documentation || contractChange.examples != base.examples {
		t.Fatalf("contract change classification = %#v; base %#v", contractChange, base)
	}

	documentationChanges := []struct {
		name     string
		metadata string
	}{
		{name: "Interface description", metadata: strings.Replace(baseMetadata, "Reads a record.", "Looks up a record.", 1)},
		{name: "semantic error description", metadata: strings.Replace(baseMetadata, "The record does not exist.", "No matching record exists.", 1)},
		{name: "deprecation message", metadata: strings.Replace(baseMetadata, "Use records.lookup/v1 later.", "Migrate to records.lookup/v1.", 1)},
		{name: "deprecation replacement", metadata: strings.Replace(baseMetadata, "  since: next-release", "  replacement: records.lookup/v1\n  since: next-release", 1)},
		{name: "deprecation lifecycle label", metadata: strings.Replace(baseMetadata, "since: next-release", "since: v0.0.1", 1)},
	}
	for _, test := range documentationChanges {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := calculate(t, source, test.metadata)
			if changed.contract != base.contract || changed.documentation == base.documentation || changed.examples != base.examples {
				t.Fatalf("documentation change classification = %#v; base %#v", changed, base)
			}
		})
	}

	exampleChanges := []struct {
		name     string
		metadata string
	}{
		{name: "name", metadata: strings.Replace(baseMetadata, "name: found", "name: first-record", 1)},
		{name: "request", metadata: strings.Replace(baseMetadata, "id: rec_1", "id: rec_2", 1)},
		{name: "response", metadata: strings.Replace(baseMetadata, "value: first", "value: changed", 1)},
		{name: "semantic error outcome", metadata: strings.Replace(baseMetadata, "    response: {value: first}", "    error: not_found", 1)},
	}
	for _, test := range exampleChanges {
		test := test
		t.Run("example "+test.name, func(t *testing.T) {
			t.Parallel()
			changed := calculate(t, source, test.metadata)
			if changed.contract != base.contract || changed.documentation != base.documentation || changed.examples == base.examples {
				t.Fatalf("example change classification = %#v; base %#v", changed, base)
			}
		})
	}
}

func TestCalculateDocumentationAndExamplesNormalizeOrderAndAbsence(t *testing.T) {
	t.Parallel()

	contract, firstMetadata, _ := digestFixture(t, canonicalDigestSource, canonicalDigestMetadata)
	firstExamples, err := interfacemeta.ResolveExamples(firstMetadata, contract)
	if err != nil {
		t.Fatal(err)
	}
	firstDocumentation, err := CalculateDocumentation(contract, firstMetadata)
	if err != nil {
		t.Fatal(err)
	}
	firstExampleDigest, err := CalculateExamples(contract, firstExamples)
	if err != nil {
		t.Fatal(err)
	}

	equivalentMetadata := `deprecation: {since: next-release, message: "This Interface will be replaced."}
examples:
  - error: unavailable
    request: {tags: [retry], detail: {total: 0}, order_id: ord_9}
    name: unavailable
  - response: {accepted: true}
    request: {tags: [new], order_id: ord_1, detail: {total: 1}}
    name: accepted
errors:
  - description: The service is unavailable.
    code: unavailable
  - code: invalid_order
description: "Creates an order."
`
	equivalentContract, secondMetadata, _ := digestFixture(t, canonicalDigestSource, equivalentMetadata)
	secondExamples, err := interfacemeta.ResolveExamples(secondMetadata, equivalentContract)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(secondExamples)
	secondDocumentation, err := CalculateDocumentation(equivalentContract, secondMetadata)
	if err != nil {
		t.Fatal(err)
	}
	secondExampleDigest, err := CalculateExamples(equivalentContract, secondExamples)
	if err != nil {
		t.Fatal(err)
	}
	if secondDocumentation != firstDocumentation || secondExampleDigest != firstExampleDigest {
		t.Fatalf("equivalent documentation/examples changed digests: %s %s != %s %s", secondDocumentation, secondExampleDigest, firstDocumentation, firstExampleDigest)
	}

	emptyDocument, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", []byte("{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	emptyDocumentation, err := CalculateDocumentation(contract, emptyDocument)
	if err != nil || len(emptyDocumentation) != len("sha256:")+64 {
		t.Fatalf("empty documentation digest = %q, %v", emptyDocumentation, err)
	}
	errorCodeOnlyDocument, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", []byte("errors: [{code: unavailable}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	errorCodeOnlyDocumentation, err := CalculateDocumentation(contract, errorCodeOnlyDocument)
	if err != nil || errorCodeOnlyDocumentation != emptyDocumentation {
		t.Fatalf("undocumented semantic error changed documentation digest: %q != %q, %v", errorCodeOnlyDocumentation, emptyDocumentation, err)
	}
	emptyExamples, err := CalculateExamples(contract, nil)
	if err != nil || len(emptyExamples) != len("sha256:")+64 {
		t.Fatalf("empty example digest = %q, %v", emptyExamples, err)
	}
}

func TestCalculateRejectsIncompleteContract(t *testing.T) {
	t.Parallel()

	digest, err := CalculateContract(interfacecontract.Contract{}, interfacemeta.Document{}, nil)
	if !errors.Is(err, ErrInvalid) || digest != "" {
		t.Fatalf("Calculate = %q, %v", digest, err)
	}
	digest, err = CalculateDocumentation(interfacecontract.Contract{}, interfacemeta.Document{})
	if !errors.Is(err, ErrInvalid) || digest != "" {
		t.Fatalf("CalculateDocumentation = %q, %v", digest, err)
	}
	digest, err = CalculateExamples(interfacecontract.Contract{}, nil)
	if !errors.Is(err, ErrInvalid) || digest != "" {
		t.Fatalf("CalculateExamples = %q, %v", digest, err)
	}
	contract, _, _ := digestFixture(t, canonicalDigestSource, "{}\n")
	digest, err = CalculateExamples(contract, []interfacemeta.Example{{}})
	if !errors.Is(err, ErrInvalid) || digest != "" {
		t.Fatalf("CalculateExamples zero value = %q, %v", digest, err)
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
  - name: unavailable
    request: {order_id: ord_9, detail: {total: 0}, tags: [retry]}
    error: unavailable
deprecation:
  message: This Interface will be replaced.
  since: next-release
conformance:
  package: ./conformance
`
