package interfacemeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacemeta"
)

func TestResolveExamplesNormalizesSuccessAndSemanticErrorOutcomes(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	data := []byte(`errors:
  - code: rejected
constraints:
  request.order_id: {min_length: 3, pattern: '^ord_'}
  request.detail.name: {min_length: 2}
  request.items: {min_items: 1}
  response.accepted: {}
examples:
  - name: rejected
    request: {order_id: ord_rejected}
    error: rejected
  - name: accepted
    request:
      labels: {z: last, "": empty}
      tags: [one]
      Legacy: legacy
      literal.name: literal
      by_id:
        b: {score: 4, name: beta}
        a: {name: alpha, score: 3}
      items: [{score: 1, name: one}]
      detail: {score: 2, name: main}
      order_id: ord_123
    response: {accepted: true}
`)
	document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	examples, err := interfacemeta.ResolveExamples(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 2 || examples[0].Name() != "accepted" || examples[1].Name() != "rejected" {
		t.Fatalf("examples = %#v", examples)
	}
	wantRequest := `{"order_id":"ord_123","detail":{"name":"main","score":2},"items":[{"name":"one","score":1}],"by_id":{"a":{"name":"alpha","score":3},"b":{"name":"beta","score":4}},"literal.name":"literal","Legacy":"legacy","tags":["one"],"labels":{"":"empty","z":"last"}}`
	if examples[0].Request().Kind() != interfacecontract.TypeMessage || examples[0].Request().CanonicalJSON() != wantRequest {
		t.Fatalf("accepted request = %s", examples[0].Request().CanonicalJSON())
	}
	response, present := examples[0].Response()
	if !present || response.Kind() != interfacecontract.TypeMessage || response.CanonicalJSON() != `{"accepted":true}` {
		t.Fatalf("accepted response = %#v, %t", response, present)
	}
	if code, present := examples[0].ErrorCode(); present || code != "" {
		t.Fatalf("accepted error = %q, %t", code, present)
	}
	if response, present := examples[1].Response(); present || response.Kind() != "" || response.CanonicalJSON() != "" {
		t.Fatalf("rejected response = %#v, %t", response, present)
	}
	if code, present := examples[1].ErrorCode(); !present || code != "rejected" {
		t.Fatalf("rejected error = %q, %t", code, present)
	}

	examples[0] = interfacemeta.Example{}
	again, err := interfacemeta.ResolveExamples(document, contract)
	if err != nil || again[0].Name() != "accepted" || again[0].Request().CanonicalJSON() != wantRequest {
		t.Fatalf("ResolveExamples exposed mutable storage: %#v, %v", again, err)
	}
}

func TestResolveExamplesAcceptsClosedCanonicalFieldGraph(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, exampleFieldGraphSource)
	document, err := interfacemeta.ParseFile("interfaces/types/interface.yaml", []byte(`examples:
  - name: every-type
    request:
      name: complete
      enabled: true
      i32: -2147483648
      i64: -9223372036854775808
      u32: 4294967295
      u64: 18446744073709551615
      f32: 1.5
      f64: -2.25
      payload: AQI=
      tags: [one, two]
      chunks: ["", AQ==]
      lookup:
        "": {label: empty, score: 1}
        b: {label: beta, score: 2}
      counts: {"-1": negative, "2": positive}
      created_at: 2026-07-23T01:02:03+08:00
      delay: 1500ms
      detail: {label: nested, score: 3}
    response:
      accepted: true
      payload: AwQ=
`))
	if err != nil {
		t.Fatal(err)
	}
	examples, err := interfacemeta.ResolveExamples(document, contract)
	if err != nil || len(examples) != 1 {
		t.Fatalf("ResolveExamples = %#v, %v", examples, err)
	}
	request := examples[0].Request().CanonicalJSON()
	for _, want := range []string{
		`"u64":18446744073709551615`,
		`"payload":"AQI="`,
		`"chunks":["","AQ=="]`,
		`"lookup":{"":{"label":"empty","score":1},"b":{"label":"beta","score":2}}`,
		`"counts":{"-1":"negative","2":"positive"}`,
		`"created_at":"2026-07-22T17:02:03Z"`,
		`"delay":"1.5s"`,
	} {
		if !strings.Contains(request, want) {
			t.Fatalf("canonical request %s does not contain %s", request, want)
		}
	}
}

func TestParseFileRejectsInvalidExampleSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "mapping container", data: "examples: {}\n", location: "interface.yaml:1:11", want: "examples must be a sequence"},
		{name: "scalar item", data: "examples: [invalid]\n", location: "interface.yaml:1:12", want: "examples[0] must be a mapping"},
		{name: "unknown field", data: "examples:\n  - name: invalid\n    request: {}\n    response: {}\n    title: Invalid\n", location: "interface.yaml:5:5", want: "examples[0].title"},
		{name: "missing name", data: "examples:\n  - request: {}\n    response: {}\n", location: "interface.yaml:2:5", want: "examples[0].name is missing"},
		{name: "invalid name", data: "examples:\n  - name: Bad_Name\n    request: {}\n    response: {}\n", location: "interface.yaml:2:11", want: "lower kebab case"},
		{name: "duplicate name", data: "examples:\n  - {name: same, request: {}, response: {}}\n  - {name: same, request: {}, response: {}}\n", location: "interface.yaml:3:12", want: "duplicates"},
		{name: "missing request", data: "examples:\n  - name: invalid\n    response: {}\n", location: "interface.yaml:2:5", want: "examples[0].request is missing"},
		{name: "missing outcome", data: "examples:\n  - name: invalid\n    request: {}\n", location: "interface.yaml:2:5", want: "exactly one of response or error"},
		{name: "two outcomes", data: "errors: [{code: rejected}]\nexamples:\n  - name: invalid\n    request: {}\n    response: {}\n    error: rejected\n", location: "interface.yaml:3:5", want: "exactly one of response or error"},
		{name: "mapping error", data: "errors: [{code: rejected}]\nexamples:\n  - name: invalid\n    request: {}\n    error: {code: rejected}\n", location: "interface.yaml:5:12", want: "one declared semantic-error code"},
		{name: "undeclared error", data: "examples:\n  - name: invalid\n    request: {}\n    error: rejected\n", location: "interface.yaml:4:12", want: "undeclared semantic-error code"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/examples/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidExamples) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %s", document, err, test.want, test.location)
			}
		})
	}
}

func TestResolveExamplesRejectsValuesOutsideCanonicalGoTypes(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, exampleFieldGraphSource)
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "request sequence", data: "    request: []\n    response: {accepted: true}\n", want: "request must be a mapping"},
		{name: "unknown request field", data: "    request: {name: valid, unknown: true}\n    response: {accepted: true}\n", want: `unknown field "unknown"`},
		{name: "missing required request", data: "    request: {}\n    response: {accepted: true}\n", want: `missing required field "name"`},
		{name: "null field", data: "    request: {name: null}\n    response: {accepted: true}\n", want: "request.name must not be null"},
		{name: "boolean string", data: "    request: {name: valid, enabled: \"true\"}\n    response: {accepted: true}\n", want: "canonical boolean value"},
		{name: "int32 overflow", data: "    request: {name: valid, i32: 2147483648}\n    response: {accepted: true}\n", want: "canonical int32 value"},
		{name: "negative uint", data: "    request: {name: valid, u64: -1}\n    response: {accepted: true}\n", want: "canonical uint64 value"},
		{name: "nonfinite float", data: "    request: {name: valid, f32: .inf}\n    response: {accepted: true}\n", want: "canonical float32 value"},
		{name: "invalid bytes", data: "    request: {name: valid, payload: not-base64}\n    response: {accepted: true}\n", want: "canonical padded base64"},
		{name: "repeated mapping", data: "    request: {name: valid, tags: {one: two}}\n    response: {accepted: true}\n", want: "canonical repeated<string> value"},
		{name: "invalid map key", data: "    request: {name: valid, counts: {\"01\": bad}}\n    response: {accepted: true}\n", want: "not a canonical int32 value"},
		{name: "nested missing required", data: "    request: {name: valid, detail: {}}\n    response: {accepted: true}\n", want: `missing required field "label"`},
		{name: "invalid timestamp", data: "    request: {name: valid, created_at: never}\n    response: {accepted: true}\n", want: "RFC 3339 timestamp"},
		{name: "invalid duration", data: "    request: {name: valid, delay: 5}\n    response: {accepted: true}\n", want: "canonical duration value"},
		{name: "missing required response", data: "    request: {name: valid}\n    response: {}\n", want: `missing required field "accepted"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte("examples:\n  - name: invalid\n" + test.data)
			document, err := interfacemeta.ParseFile("interfaces/types/interface.yaml", data)
			if err != nil {
				t.Fatal(err)
			}
			examples, err := interfacemeta.ResolveExamples(document, contract)
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidExamples) || len(examples) != 0 || !strings.Contains(err.Error(), "interfaces/types/interface.yaml:") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveExamples = %#v, %v; want %q", examples, err, test.want)
			}
		})
	}
}

func TestResolveExamplesEnforcesCanonicalFieldConstraints(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, exampleFieldGraphSource)
	constraints := `constraints:
  request.name: {min_length: 3, max_length: 8, pattern: '^[a-z]+$'}
  request.payload: {min_length: 2, max_length: 3}
  request.i32: {minimum: -2, maximum: 2}
  request.f64: {minimum: -2.5, maximum: 2.5}
  request.tags: {min_items: 1, max_items: 2}
  request.lookup: {min_items: 1, max_items: 1}
  request.detail.label: {min_length: 2}
  response.result: {pattern: '^ok$'}
examples:
  - name: invalid
`
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "minimum length", body: "    request: {name: ab}\n    response: {accepted: true, result: ok}\n", want: "min_length"},
		{name: "maximum length", body: "    request: {name: toolongname}\n    response: {accepted: true, result: ok}\n", want: "max_length"},
		{name: "pattern", body: "    request: {name: BAD}\n    response: {accepted: true, result: ok}\n", want: "pattern"},
		{name: "byte length", body: "    request: {name: valid, payload: AQ==}\n    response: {accepted: true, result: ok}\n", want: "min_length"},
		{name: "integer minimum", body: "    request: {name: valid, i32: -3}\n    response: {accepted: true, result: ok}\n", want: "minimum"},
		{name: "float maximum", body: "    request: {name: valid, f64: 3}\n    response: {accepted: true, result: ok}\n", want: "maximum"},
		{name: "item minimum", body: "    request: {name: valid, tags: []}\n    response: {accepted: true, result: ok}\n", want: "min_items"},
		{name: "map maximum", body: "    request: {name: valid, lookup: {a: {label: aa}, b: {label: bb}}}\n    response: {accepted: true, result: ok}\n", want: "max_items"},
		{name: "nested length", body: "    request: {name: valid, detail: {label: x}}\n    response: {accepted: true, result: ok}\n", want: "min_length"},
		{name: "response pattern", body: "    request: {name: valid}\n    response: {accepted: true, result: bad}\n", want: "response.result violates pattern"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte(constraints+test.body))
			if err != nil {
				t.Fatal(err)
			}
			examples, err := interfacemeta.ResolveExamples(document, contract)
			if !errors.Is(err, interfacemeta.ErrInvalidExamples) || len(examples) != 0 || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "canonical field") {
				t.Fatalf("ResolveExamples = %#v, %v; want %q", examples, err, test.want)
			}
		})
	}
}

func TestResolveExamplesBoundsRecursiveDepthAndWork(t *testing.T) {
	t.Parallel()

	recursiveContract := constraintTestContract(t, recursiveExampleSource)
	var deep strings.Builder
	deep.WriteString("examples:\n  - name: deep\n    request:\n      root:\n")
	for index := 0; index < interfacemeta.MaximumExampleDepth; index++ {
		deep.WriteString(strings.Repeat(" ", 8+index*4))
		deep.WriteString("name: node\n")
		deep.WriteString(strings.Repeat(" ", 8+index*4))
		deep.WriteString("children:\n")
		deep.WriteString(strings.Repeat(" ", 10+index*4))
		deep.WriteString("-\n")
	}
	deep.WriteString(strings.Repeat(" ", 8+interfacemeta.MaximumExampleDepth*4))
	deep.WriteString("name: leaf\n    response: {accepted: true}\n")
	document, err := interfacemeta.ParseFile("interfaces/recursive/interface.yaml", []byte(deep.String()))
	if err != nil {
		t.Fatal(err)
	}
	if examples, err := interfacemeta.ResolveExamples(document, recursiveContract); !errors.Is(err, interfacemeta.ErrInvalidExamples) || len(examples) != 0 || !strings.Contains(err.Error(), "maximum example depth") {
		t.Fatalf("deep ResolveExamples = %#v, %v", examples, err)
	}

	fieldContract := constraintTestContract(t, exampleFieldGraphSource)
	var wide strings.Builder
	wide.WriteString("examples:\n  - name: wide\n    request:\n      name: valid\n      tags:\n")
	for index := 0; index < interfacemeta.MaximumExampleNodes; index++ {
		wide.WriteString("        - x\n")
	}
	wide.WriteString("    response: {accepted: true}\n")
	document, err = interfacemeta.ParseFile("interfaces/wide/interface.yaml", []byte(wide.String()))
	if err != nil {
		t.Fatal(err)
	}
	if examples, err := interfacemeta.ResolveExamples(document, fieldContract); !errors.Is(err, interfacemeta.ErrInvalidExamples) || len(examples) != 0 || !strings.Contains(err.Error(), "maximum example node count") {
		t.Fatalf("wide ResolveExamples = %#v, %v", examples, err)
	}
}

func TestResolveExamplesNormalizesEquivalentYAMLDeterministically(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	first := []byte("examples:\n  - name: accepted\n    request: {order_id: ord_1, tags: [a, b]}\n    response: {accepted: true}\n")
	second := []byte("examples: [{response: {accepted: TRUE}, request: {tags: [a, b], order_id: ord_1}, name: accepted}]\n")
	if got, want := exampleSummary(t, first, contract), exampleSummary(t, second, contract); !reflect.DeepEqual(got, want) {
		t.Fatalf("equivalent examples differ:\nfirst: %#v\nsecond: %#v", got, want)
	}
}

func FuzzResolveExamples(f *testing.F) {
	contract := constraintTestContract(f, exampleFieldGraphSource)
	for _, seed := range []string{
		"{}\n",
		"examples: []\n",
		"examples: [{name: accepted, request: {name: valid}, response: {accepted: true}}]\n",
		"errors: [{code: rejected}]\nexamples: [{name: rejected, request: {name: valid}, error: rejected}]\n",
		"examples: [{name: invalid, request: {name: null}, response: {accepted: true}}]\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > interfacemeta.MaximumSize+1 {
			t.Skip()
		}
		document, err := interfacemeta.ParseFile("interfaces/fuzz/interface.yaml", []byte(data))
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalid) || document.Path() != "" {
				t.Fatalf("ParseFile returned inconsistent error: %#v, %v", document, err)
			}
			return
		}
		first, err := interfacemeta.ResolveExamples(document, contract)
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalid) || len(first) != 0 {
				t.Fatalf("ResolveExamples returned inconsistent error: %#v, %v", first, err)
			}
			return
		}
		second, err := interfacemeta.ResolveExamples(document, contract)
		if err != nil || !reflect.DeepEqual(normalizedExampleSummary(first), normalizedExampleSummary(second)) {
			t.Fatalf("ResolveExamples is nondeterministic: %#v, %#v, %v", first, second, err)
		}
		previous := ""
		for _, example := range first {
			if example.Name() == "" || previous >= example.Name() || example.Request().Kind() != interfacecontract.TypeMessage {
				t.Fatalf("ResolveExamples returned invalid ordering: %#v", normalizedExampleSummary(first))
			}
			previous = example.Name()
		}
	})
}

func exampleSummary(t testing.TB, data []byte, contract interfacecontract.Contract) []string {
	t.Helper()
	document, err := interfacemeta.ParseFile("interfaces/examples/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	examples, err := interfacemeta.ResolveExamples(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	return normalizedExampleSummary(examples)
}

func normalizedExampleSummary(examples []interfacemeta.Example) []string {
	result := make([]string, 0, len(examples))
	for _, example := range examples {
		outcome := "error:"
		if response, exists := example.Response(); exists {
			outcome = "response:" + response.CanonicalJSON()
		} else if code, exists := example.ErrorCode(); exists {
			outcome += code
		}
		result = append(result, example.Name()+"|"+example.Request().CanonicalJSON()+"|"+outcome)
	}
	return result
}

const exampleFieldGraphSource = `package contract

import (
	"context"
	"time"
)

//plystra:interface examples.validate/v1
type Interface interface { Validate(context.Context, Request) (Response, error) }

type Detail struct {
	Label string ` + "`plystra:\"1,required\" json:\"label\"`" + `
	Score int64 ` + "`plystra:\"2\" json:\"score\"`" + `
}

type Request struct {
	Name string ` + "`plystra:\"1,required\" json:\"name\"`" + `
	Enabled bool ` + "`plystra:\"2\" json:\"enabled\"`" + `
	I32 int32 ` + "`plystra:\"3\" json:\"i32\"`" + `
	I64 int64 ` + "`plystra:\"4\" json:\"i64\"`" + `
	U32 uint32 ` + "`plystra:\"5\" json:\"u32\"`" + `
	U64 uint64 ` + "`plystra:\"6\" json:\"u64\"`" + `
	F32 float32 ` + "`plystra:\"7\" json:\"f32\"`" + `
	F64 float64 ` + "`plystra:\"8\" json:\"f64\"`" + `
	Payload []byte ` + "`plystra:\"9\" json:\"payload\"`" + `
	Tags []string ` + "`plystra:\"10\" json:\"tags\"`" + `
	Chunks [][]byte ` + "`plystra:\"11\" json:\"chunks\"`" + `
	Lookup map[string]Detail ` + "`plystra:\"12\" json:\"lookup\"`" + `
	Counts map[int32]string ` + "`plystra:\"13\" json:\"counts\"`" + `
	CreatedAt time.Time ` + "`plystra:\"14\" json:\"created_at\"`" + `
	Delay time.Duration ` + "`plystra:\"15\" json:\"delay\"`" + `
	Detail Detail ` + "`plystra:\"16\" json:\"detail\"`" + `
}

type Response struct {
	Accepted bool ` + "`plystra:\"1,required\" json:\"accepted\"`" + `
	Payload []byte ` + "`plystra:\"2\" json:\"payload\"`" + `
	Result string ` + "`plystra:\"3\" json:\"result\"`" + `
}
`

const recursiveExampleSource = `package contract

import "context"

//plystra:interface examples.recursive/v1
type Interface interface { Validate(context.Context, Request) (Response, error) }

type Node struct {
	Name string ` + "`plystra:\"1,required\" json:\"name\"`" + `
	Children []Node ` + "`plystra:\"2\" json:\"children\"`" + `
}

type Request struct { Root Node ` + "`plystra:\"1,required\" json:\"root\"`" + ` }
type Response struct { Accepted bool ` + "`plystra:\"1,required\" json:\"accepted\"`" + ` }
`
