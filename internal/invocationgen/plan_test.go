package invocationgen_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/invocationgen"
)

const (
	orderCreateSchema = `id: order.create/v1
request:
  order_id: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [policy_failed]
extensions:
  policy:
    permission: order.create
`
	policyCheckSchema = `id: policy.check/v1
request:
  permission: {type: string, required: true}
  retry_count: {type: integer, required: true}
  enforce: {type: boolean, required: true}
response:
  allowed: {type: boolean, required: true}
errors: [unavailable]
`
)

func TestRenderPlanGoldenAndRuntimeOrder(t *testing.T) {
	t.Parallel()

	plan := prepareCallPlan(t, generation.Contribution{
		ID:        "policy.require-create",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "check",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: planCapabilityID(t, "policy.check/v1"),
				Request: []generation.GeneratedFieldBinding{
					{Field: "permission", Value: generation.StringValue("order.create")},
					{Field: "retry_count", Value: generation.IntegerValue(2)},
					{Field: "enforce", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 75,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.prepare.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedPrepareInvocationRuns(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanPreservesLoweredSemanticOrder(t *testing.T) {
	t.Parallel()

	call := func(id, permission string) generation.Contribution {
		return generation.Contribution{
			ID: id, Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: planCapabilityID(t, "policy.check/v1"),
				Request: []generation.GeneratedFieldBinding{
					{Field: "permission", Value: generation.StringValue(permission)},
					{Field: "retry_count", Value: generation.IntegerValue(1)},
					{Field: "enforce", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
			}}},
		}
	}
	plan := prepareOrderedCallPlan(t, []generation.Contribution{
		call("policy.z-first", "first"),
		call("policy.a-second", "second"),
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	source := string(file.Data())
	first := strings.Index(source, "plystraPolicyZFirstCheckResponse")
	second := strings.Index(source, "plystraPolicyASecondCheckResponse")
	dispatch := strings.Index(source, "return h.target.Invoke(ctx, request)")
	if first < 0 || second <= first || dispatch <= second {
		t.Fatalf("semantic order was not preserved: first=%d second=%d dispatch=%d\n%s", first, second, dispatch, source)
	}
	if strings.Count(source, `"example.com/acme/project/generated/go/clients/policy/check/v1"`) != 1 || strings.Count(source, "policycheckv1Client policycheckv1.Client") != 2 {
		t.Fatalf("shared dependency was not deterministically deduplicated:\n%s", source)
	}
}

func TestRenderPlanRejectsUnsupportedOrUnsafeCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contribution generation.Contribution
		want         []string
	}{
		{
			name: "captured failure",
			contribution: generation.Contribution{
				ID: "policy.capture", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
				Nodes: []generation.GeneratedNode{
					{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{Capability: planCapabilityID(t, "policy.check/v1"), Request: policyLiteralBindings(), TimeoutMilliseconds: 50, OnError: generation.GeneratedCallCapture}},
					{ID: "reject", ConditionalFailure: &generation.GeneratedConditionalFailure{Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionError, Value: planNodeValue("check", generation.GeneratedNodeError)}, ErrorCode: "policy_failed", Message: "Policy failed."}},
				},
			},
			want: []string{"policy.capture", "check", "fail-closed"},
		},
		{
			name: "invocation value binding",
			contribution: generation.Contribution{
				ID: "policy.request", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
				Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability: planCapabilityID(t, "policy.check/v1"),
					Request: []generation.GeneratedFieldBinding{
						{Field: "permission", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "order_id")},
						{Field: "retry_count", Value: generation.IntegerValue(2)},
						{Field: "enforce", Value: generation.BooleanValue(true)},
					},
					TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
				}}},
			},
			want: []string{"policy.request", "permission", "not yet renderable"},
		},
		{
			name: "complete point",
			contribution: generation.Contribution{
				ID: "policy.complete", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationComplete,
				Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{Capability: planCapabilityID(t, "policy.check/v1"), Request: policyLiteralBindings(), TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed}}},
			},
			want: []string{"policy.complete", "invocation.complete", "unsupported point"},
		},
		{
			name: "self call",
			contribution: generation.Contribution{
				ID: "policy.self", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
				Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability:          planCapabilityID(t, "order.create/v1"),
					Request:             []generation.GeneratedFieldBinding{{Field: "order_id", Value: generation.StringValue("recursive")}},
					TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
				}}},
			},
			want: []string{"policy.self", "recursively calls", "order.create/v1"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan := prepareCallPlan(t, test.contribution)
			file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
			if !errors.Is(err, invocationgen.ErrRender) || !errors.Is(err, invocationgen.ErrContribution) || file.Path() != "" || file.Data() != nil {
				t.Fatalf("RenderPlan = %#v, %v", file, err)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("RenderPlan error %q omits %q", err, want)
				}
			}
		})
	}
}

func TestRenderPlanRejectsModuleDrift(t *testing.T) {
	t.Parallel()

	plan, err := generationlowering.Lower("example.com/other", []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if !errors.Is(err, invocationgen.ErrRender) || !errors.Is(err, invocationgen.ErrContribution) || !strings.Contains(err.Error(), "example.com/other") || file.Data() != nil {
		t.Fatalf("RenderPlan(module drift) = %#v, %v", file, err)
	}
}

func prepareCallPlan(t *testing.T, contribution generation.Contribution) generationlowering.Plan {
	t.Helper()
	return prepareOrderedCallPlan(t, []generation.Contribution{contribution})
}

func prepareOrderedCallPlan(t *testing.T, contributions []generation.Contribution) generationlowering.Plan {
	t.Helper()
	order := planCanonicalContract(t, orderCreateSchema)
	policy := planCanonicalContract(t, policyCheckSchema)
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "example.orders", ModulePath: testModulePath, Provides: []string{"order.create/v1"}, BuildMetadataJSON: []byte("{}")},
			{ID: "example.policy", ModulePath: testModulePath, Provides: []string{"policy.check/v1"}, BuildMetadataJSON: []byte("{}")},
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: order}, {ContractJSON: policy}},
		Requirements: []string{"order.create/v1", "policy.check/v1"},
		Providers: []generation.ProviderInput{
			{Capability: "order.create/v1", Plugin: "example.orders"},
			{Capability: "policy.check/v1", Plugin: "example.policy"},
		},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: contributions})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	byID := make(map[string]generation.NormalizedContribution, len(contributions))
	for _, contribution := range output.Contributions() {
		byID[contribution.ID()] = contribution
	}
	ordered := make([]generation.NormalizedContribution, len(contributions))
	for index, contribution := range contributions {
		ordered[index] = byID[contribution.ID]
	}
	plan, err := generationlowering.Lower(testModulePath, ordered)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	return plan
}

func policyLiteralBindings() []generation.GeneratedFieldBinding {
	return []generation.GeneratedFieldBinding{
		{Field: "permission", Value: generation.StringValue("order.create")},
		{Field: "retry_count", Value: generation.IntegerValue(2)},
		{Field: "enforce", Value: generation.BooleanValue(true)},
	}
}

func planCanonicalContract(t *testing.T, schema string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}

func planCapabilityID(t *testing.T, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", value, err)
	}
	return id
}

func planNodeValue(id string, output generation.GeneratedNodeOutput) generation.GeneratedValue {
	return generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: id, Output: output}}
}

func planInvocationValue(source generation.GeneratedInvocationValueSource, name string) generation.GeneratedValue {
	return generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: source, Name: name}}
}

func assertGeneratedPrepareInvocationRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(source contract): %v", err)
	}
	targetContract, err := contractgen.Render([]byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target contract): %v", err)
	}
	targetInvocation, err := invocationgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target invocation): %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target client): %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{targetContract.Path(), targetContract.Data()},
		{targetInvocation.Path(), targetInvocation.Data()},
		{targetClient.Path(), targetClient.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(`package invocation

import "context"

type Handle[Request, Response any] struct {
	available bool
	invoke func(context.Context, Request) (Response, error)
}

func NewTestHandle[Request, Response any](available bool, invoke func(context.Context, Request) (Response, error)) Handle[Request, Response] {
	return Handle[Request, Response]{available: available, invoke: invoke}
}

func (h Handle[Request, Response]) Available() bool { return h.available }

func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	if h.invoke == nil {
		var response Response
		return response, context.Canceled
	}
	return h.invoke(ctx, request)
}
`))
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	policyclient "example.com/acme/project/generated/go/clients/policy/check/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/check/v1"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestPrepareCallRunsBeforeCanonicalDispatchAndFailsClosed(t *testing.T) {
	sequence := []string{}
	failPolicy := false
	policyTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request policycontract.Request) (policycontract.Response, error) {
		sequence = append(sequence, "policy")
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > 100*time.Millisecond {
			t.Fatalf("policy deadline = %v, %t, remaining %v", deadline, ok, remaining)
		}
		if request.Permission != "order.create" || request.RetryCount != 2 || !request.Enforce {
			t.Fatalf("policy request = %#v", request)
		}
		if failPolicy {
			return policycontract.Response{}, errors.New("policy unavailable")
		}
		return policycontract.Response{Allowed: true}, nil
	})
	orderTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		sequence = append(sequence, "order:"+request.OrderID)
		return ordercontract.Response{Accepted: true}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	handle := orderinvocation.New(orderTarget, policy)
	response, err := handle.Invoke(context.Background(), ordercontract.Request{OrderID: "one"})
	if err != nil || !response.Accepted || !slices.Equal(sequence, []string{"policy", "order:one"}) {
		t.Fatalf("Invoke = %#v, %v, sequence %v", response, err, sequence)
	}

	failPolicy = true
	sequence = nil
	response, err = handle.Invoke(context.Background(), ordercontract.Request{OrderID: "two"})
	if err == nil || response.Accepted || !slices.Equal(sequence, []string{"policy"}) {
		t.Fatalf("failed Invoke = %#v, %v, sequence %v", response, err, sequence)
	}
	sequence = nil
	if _, err := handle.Invoke(nil, ordercontract.Request{OrderID: "three"}); err == nil || len(sequence) != 0 {
		t.Fatalf("nil-context Invoke = %v, sequence %v", err, sequence)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated prepare invocation: %v\n%s", err, output)
	}
}
