package invocationgen_test

import (
	"bytes"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

const constraintValidationSchema = `id: constraint.validate/v1
request:
  code: {type: string, required: true, constraints: {min_length: 2, max_length: 2, pattern: '^A.$'}}
  count: {type: integer, required: true, constraints: {minimum: 1, maximum: 3}}
  optional_code: {type: string, constraints: {max_length: 1}}
  ratio: {type: number, required: true, constraints: {minimum: 0.5, maximum: 1.5}}
  single: {type: string, required: true, constraints: {max_length: 1}}
  tags: {type: array, items: string, required: true, constraints: {min_items: 1, max_items: 2}}
response:
  code: {type: string, required: true, constraints: {min_length: 2, max_length: 2, pattern: '^A.$'}}
  count: {type: integer, required: true, constraints: {minimum: 1, maximum: 3}}
  optional_code: {type: string, constraints: {max_length: 1}}
  ratio: {type: number, required: true, constraints: {minimum: 0.5, maximum: 1.5}}
  single: {type: string, required: true, constraints: {max_length: 1}}
  tags: {type: array, items: string, required: true, constraints: {min_items: 1, max_items: 2}}
extensions:
  policy: {constraint: true}
` + invocationQuerySemanticsYAML

func TestGeneratedInvocationEnforcesCanonicalFieldConstraints(t *testing.T) {
	t.Parallel()

	contract, err := contractgen.Render([]byte(constraintValidationSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(constraintValidationSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	for _, required := range [][]byte{
		[]byte("utf8.RuneCountInString(request.Code)"),
		[]byte("utf8.RuneCountInString(response.Code)"),
		[]byte("regexp.MustCompile(\"^A.$\")"),
		[]byte("kernelinvocation.ErrorInvalidArgument"),
	} {
		if !bytes.Contains(invocation.Data(), required) {
			t.Fatalf("generated invocation omits %q", required)
		}
	}

	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/constraint/validate/v1/constraint_validation_gen_test.go", []byte(generatedConstraintValidationRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func TestGeneratedConstraintValidationPrecedesContributions(t *testing.T) {
	t.Parallel()

	sourceID := planCapabilityID(t, "constraint.validate/v1")
	targetID := planCapabilityID(t, "policy.check/v1")
	plan := prepareCallPlanForSchemas(t, constraintValidationSchema, policyCheckSchema, sourceID, targetID, generation.Contribution{
		ID:        "policy.check-constraint",
		Namespace: "policy",
		Source:    sourceID,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "check",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: targetID,
				Request: []generation.GeneratedFieldBinding{
					{Field: "permission", Value: generation.StringValue("constraint.validate")},
					{Field: "retry_count", Value: generation.IntegerValue(1)},
					{Field: "enforce", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 50,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(constraintValidationSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	requestValidation := bytes.Index(file.Data(), []byte("if requestError := ValidateRequest(request)"))
	contribution := bytes.Index(file.Data(), []byte("h.policycheckv1Client.Check"))
	provider := bytes.Index(file.Data(), []byte("h.target.Invoke(ctx, request)"))
	if requestValidation < 0 || contribution < 0 || provider < 0 || requestValidation > contribution || contribution > provider {
		t.Fatalf("generated order = validation %d, contribution %d, Provider %d", requestValidation, contribution, provider)
	}
}

const generatedConstraintValidationRuntimeTest = `package constraintvalidatev1_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	contract "example.com/acme/project/generated/go/contracts/constraint/validate/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/constraint/validate/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestCanonicalConstraintValidation(t *testing.T) {
	calls := 0
	next := validResponse()
	target := kernelinvocation.NewTestHandle(true, func(context.Context, contract.Request) (contract.Response, error) {
		calls++
		return next, nil
	})
	handle := applicationinvocation.New(target)

	response, err := handle.Invoke(context.Background(), validRequest())
	if err != nil || !reflect.DeepEqual(response, next) || calls != 1 {
		t.Fatalf("valid invocation = %#v, %v, calls %d", response, err, calls)
	}

	requestTests := []struct {
		name string
		mutate func(*contract.Request)
	}{
		{name: "minimum Unicode scalar length", mutate: func(value *contract.Request) { value.Code = "😀" }},
		{name: "maximum Unicode scalar length", mutate: func(value *contract.Request) { value.Code = "AA😀" }},
		{name: "combining sequence has two scalars", mutate: func(value *contract.Request) { value.Single = "e\u0301" }},
		{name: "pattern", mutate: func(value *contract.Request) { value.Code = "B😀" }},
		{name: "invalid UTF-8", mutate: func(value *contract.Request) { value.Code = string([]byte{0xff, 'A'}) }},
		{name: "integer minimum", mutate: func(value *contract.Request) { value.Count = 0 }},
		{name: "integer maximum", mutate: func(value *contract.Request) { value.Count = 4 }},
		{name: "number minimum", mutate: func(value *contract.Request) { value.Ratio = 0.25 }},
		{name: "number maximum", mutate: func(value *contract.Request) { value.Ratio = 2 }},
		{name: "non-finite number", mutate: func(value *contract.Request) { value.Ratio = math.NaN() }},
		{name: "minimum items", mutate: func(value *contract.Request) { value.Tags = []string{} }},
		{name: "maximum items", mutate: func(value *contract.Request) { value.Tags = []string{"one", "two", "three"} }},
		{name: "required array absent", mutate: func(value *contract.Request) { value.Tags = nil }},
		{name: "optional scalar count", mutate: func(value *contract.Request) { value.OptionalCode = pointer("e\u0301") }},
	}
	for _, test := range requestTests {
		t.Run("request "+test.name, func(t *testing.T) {
			request := validRequest()
			test.mutate(&request)
			response, err := handle.Invoke(context.Background(), request)
			var classified *kernelinvocation.Error
			if !errors.As(err, &classified) || classified.Code() != kernelinvocation.ErrorInvalidArgument || classified.DetailCode() != "contract.invalid_request" || !reflect.DeepEqual(response, contract.Response{}) {
				t.Fatalf("invalid request = %#v, %v", response, err)
			}
			if calls != 1 {
				t.Fatalf("invalid request reached Provider: calls %d", calls)
			}
		})
	}

	responseTests := []struct {
		name string
		mutate func(*contract.Response)
	}{
		{name: "minimum Unicode scalar length", mutate: func(value *contract.Response) { value.Code = "😀" }},
		{name: "maximum Unicode scalar length", mutate: func(value *contract.Response) { value.Code = "AA😀" }},
		{name: "combining sequence has two scalars", mutate: func(value *contract.Response) { value.Single = "e\u0301" }},
		{name: "pattern", mutate: func(value *contract.Response) { value.Code = "B😀" }},
		{name: "integer minimum", mutate: func(value *contract.Response) { value.Count = 0 }},
		{name: "integer maximum", mutate: func(value *contract.Response) { value.Count = 4 }},
		{name: "number minimum", mutate: func(value *contract.Response) { value.Ratio = 0.25 }},
		{name: "number maximum", mutate: func(value *contract.Response) { value.Ratio = 2 }},
		{name: "minimum items", mutate: func(value *contract.Response) { value.Tags = []string{} }},
		{name: "maximum items", mutate: func(value *contract.Response) { value.Tags = []string{"one", "two", "three"} }},
		{name: "optional scalar count", mutate: func(value *contract.Response) { value.OptionalCode = pointer("e\u0301") }},
	}
	for _, test := range responseTests {
		t.Run("response "+test.name, func(t *testing.T) {
			next = validResponse()
			test.mutate(&next)
			response, err := handle.Invoke(context.Background(), validRequest())
			if err == nil || err.Error() != "invalid canonical Provider response" || !reflect.DeepEqual(response, contract.Response{}) {
				t.Fatalf("invalid response = %#v, %v", response, err)
			}
			if strings.Contains(err.Error(), "😀") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("response validation leaked data: %q", err)
			}
		})
	}
}

func validRequest() contract.Request {
	return contract.Request{
		Code:         "A😀",
		Count:        1,
		OptionalCode: pointer("😀"),
		Ratio:        0.5,
		Single:       "😀",
		Tags:         []string{"one"},
	}
}

func validResponse() contract.Response {
	return contract.Response{
		Code:         "A😀",
		Count:        3,
		OptionalCode: nil,
		Ratio:        1.5,
		Single:       "😀",
		Tags:         []string{"one", "two"},
	}
}

func pointer[T any](value T) *T { return &value }
`
