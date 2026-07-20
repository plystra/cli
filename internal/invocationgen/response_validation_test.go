package invocationgen_test

import (
	"testing"

	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

const responseValidationSchema = `id: report.describe/v1
response:
  active: {type: boolean, required: true}
  count: {type: integer, required: true}
  labels: {type: array, items: string, required: true}
  metadata: {type: object, required: true}
  mode: {type: string, enum: [full, summary], required: true}
  optional_labels: {type: array, items: string}
  optional_metadata: {type: object}
  optional_ratio: {type: number}
  optional_title: {type: string}
  ratio: {type: number, required: true}
  records: {type: array, items: object, required: true}
  scores: {type: array, items: number, required: true}
  title: {type: string, required: true}
`

func TestGeneratedInvocationValidatesEveryCanonicalResponseShape(t *testing.T) {
	t.Parallel()

	contract, err := contractgen.Render([]byte(responseValidationSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(responseValidationSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/report/describe/v1/response_validation_gen_test.go", []byte(generatedResponseValidationRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

const generatedResponseValidationRuntimeTest = `package reportdescribev1_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	contract "example.com/acme/project/generated/go/contracts/report/describe/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/report/describe/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestProviderResponseValidation(t *testing.T) {
	next := validResponse()
	var providerError error
	target := kernelinvocation.NewTestHandle(true, func(context.Context, contract.Request) (contract.Response, error) {
		return next, providerError
	})
	handle := applicationinvocation.New(target)

	response, err := handle.Invoke(context.Background(), contract.Request{})
	if err != nil || !reflect.DeepEqual(response, next) {
		t.Fatalf("valid response = %#v, %v", response, err)
	}

	tests := []struct {
		name   string
		mutate func(*contract.Response)
	}{
		{name: "enum", mutate: func(value *contract.Response) { value.Mode = contract.ResponseMode("secret-invalid-enum") }},
		{name: "required string UTF-8", mutate: func(value *contract.Response) { value.Title = "secret-\xff" }},
		{name: "optional string UTF-8", mutate: func(value *contract.Response) { value.OptionalTitle = pointer("secret-\xff") }},
		{name: "required number NaN", mutate: func(value *contract.Response) { value.Ratio = math.NaN() }},
		{name: "required number infinity", mutate: func(value *contract.Response) { value.Ratio = math.Inf(1) }},
		{name: "optional number", mutate: func(value *contract.Response) { value.OptionalRatio = pointer(math.Inf(-1)) }},
		{name: "required object absent", mutate: func(value *contract.Response) { value.Metadata = nil }},
		{name: "optional object absent value", mutate: func(value *contract.Response) { value.OptionalMetadata = pointer(map[string]any(nil)) }},
		{name: "object unsupported value", mutate: func(value *contract.Response) { value.Metadata["secret"] = []string{"unsupported"} }},
		{name: "object invalid key", mutate: func(value *contract.Response) { value.Metadata["secret-\xff"] = true }},
		{name: "object non-finite value", mutate: func(value *contract.Response) { value.Metadata["secret"] = math.NaN() }},
		{name: "object invalid JSON number", mutate: func(value *contract.Response) { value.Metadata["secret"] = json.Number("NaN") }},
		{name: "object cycle", mutate: func(value *contract.Response) {
			cycle := map[string]any{}
			cycle["secret"] = cycle
			value.Metadata = cycle
		}},
		{name: "object excessive depth", mutate: func(value *contract.Response) {
			root := map[string]any{}
			current := root
			for depth := 0; depth < 66; depth++ {
				next := map[string]any{}
				current["secret"] = next
				current = next
			}
			value.Metadata = root
		}},
		{name: "required string array absent", mutate: func(value *contract.Response) { value.Labels = nil }},
		{name: "string array item", mutate: func(value *contract.Response) { value.Labels = []string{"secret-\xff"} }},
		{name: "optional string array item", mutate: func(value *contract.Response) { value.OptionalLabels = pointer([]string{"secret-\xff"}) }},
		{name: "number array item", mutate: func(value *contract.Response) { value.Scores = []float64{math.Inf(1)} }},
		{name: "object array absent", mutate: func(value *contract.Response) { value.Records = nil }},
		{name: "object array nil item", mutate: func(value *contract.Response) { value.Records = []map[string]any{nil} }},
		{name: "object array invalid item", mutate: func(value *contract.Response) { value.Records = []map[string]any{{"secret": make(chan int)}} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next = validResponse()
			test.mutate(&next)
			response, err := handle.Invoke(context.Background(), contract.Request{})
			if err == nil || err.Error() != "invalid canonical Provider response" || !reflect.DeepEqual(response, contract.Response{}) {
				t.Fatalf("invalid response = %#v, %v", response, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("validation error leaked Provider data: %q", err)
			}
		})
	}

	next = validResponse()
	providerError = errors.New("provider failure")
	response, err = handle.Invoke(context.Background(), contract.Request{})
	if !errors.Is(err, providerError) || !reflect.DeepEqual(response, contract.Response{}) {
		t.Fatalf("Provider failure = %#v, %v", response, err)
	}
}

func validResponse() contract.Response {
	return contract.Response{
		Active:           true,
		Count:            4,
		Labels:           []string{"alpha", "beta"},
		Metadata:         map[string]any{"enabled": true, "count": int64(2), "ratio": json.Number("1.5"), "payload": []byte{1, 2}, "nested": []any{nil, "value", map[string]any{"ok": true}}},
		Mode:             contract.ResponseModeFull,
		OptionalLabels:   pointer([]string{}),
		OptionalMetadata: pointer(map[string]any{"present": true}),
		OptionalRatio:    pointer(0.5),
		OptionalTitle:    pointer("optional"),
		Ratio:            1.25,
		Records:          []map[string]any{{"id": "one"}},
		Scores:           []float64{1, 2.5},
		Title:            "report",
	}
}

func pointer[T any](value T) *T { return &value }
`
