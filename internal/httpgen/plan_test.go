package httpgen_test

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
	"github.com/plystra/cli/internal/httpgen"
	"github.com/plystra/cli/internal/invocationgen"
)

const (
	plannedEmailSendSchema = emailSendSchema + `extensions:
  policy:
    transport: http
`
	httpPolicyCheckSchema = `id: policy.check/v1
request:
  permission: {type: string, required: true}
  retry_count: {type: integer, required: true}
  enforce: {type: boolean, required: true}
  credential: {type: string}
response:
  allowed: {type: boolean, required: true}
errors: [unavailable]
` + httpQuerySemanticsYAML
)

func TestRenderPlanRunsOrderedHTTPContributions(t *testing.T) {
	t.Parallel()

	plan := httpContributionPlan(t)
	target := exposedTarget(t, plannedEmailSendSchema)
	if !plan.RequiresHTTPPath(target.ID()) {
		t.Fatal("HTTP contribution plan did not report its external path")
	}
	adapter, err := httpgen.RenderPlan(testModulePath, target, plan, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("RenderPlan(adapter): %v", err)
	}
	invocation, err := invocationgen.RenderPlan(testModulePath, []byte(plannedEmailSendSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan(invocation): %v", err)
	}
	alias, err := httpgen.RenderAlias(testModulePath, testHTTPAlias{
		id:         httpCapabilityID(t, "mail.deliver/v1"),
		target:     target.ID(),
		digest:     target.ContractDigest(),
		exposure:   generation.Exposure{HTTP: true},
		deprecated: "Use email.send/v1 instead.",
	}, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("RenderAlias: %v", err)
	}
	for _, required := range []string{
		"target.InvokeHTTP(ctx, input",
		"plystraAdapterCredential(request, name)",
		"MaximumAdapterCredentialBytes",
	} {
		if !bytes.Contains(adapter.Data(), []byte(required)) {
			t.Fatalf("planned HTTP adapter omits %q:\n%s", required, adapter.Data())
		}
	}
	for _, required := range []string{
		"type AdapterCredentialSource func(string) (string, bool)",
		"func (h Handle) InvokeHTTP(",
		"func plystraAdapterCredential(",
		"var invocationContext context.Context",
	} {
		if !bytes.Contains(invocation.Data(), []byte(required)) {
			t.Fatalf("planned invocation omits %q:\n%s", required, invocation.Data())
		}
	}
	assertGeneratedHTTPPlanRuns(t, adapter, alias, invocation)

	repeated, err := httpgen.RenderPlan(testModulePath, target, plan, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil || !bytes.Equal(repeated.Data(), adapter.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanValidatesPairingAndPreservesBaseOutput(t *testing.T) {
	t.Parallel()

	target := exposedTarget(t, emailSendSchema)
	empty, err := generationlowering.Lower(testModulePath, []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower(empty): %v", err)
	}
	base, err := httpgen.Render(testModulePath, target, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	planned, err := httpgen.RenderPlan(testModulePath, target, empty, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil || !bytes.Equal(planned.Data(), base.Data()) {
		t.Fatalf("empty RenderPlan differs from Render: %v\n%s", err, planned.Data())
	}

	other, err := generationlowering.Lower("example.com/other", []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower(other): %v", err)
	}
	file, err := httpgen.RenderPlan(testModulePath, target, other, httpConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if !errors.Is(err, httpgen.ErrRender) || !errors.Is(err, httpgen.ErrPlan) || !strings.Contains(err.Error(), "example.com/other") || file.Data() != nil {
		t.Fatalf("RenderPlan(module drift) = %#v, %v", file, err)
	}
}

func httpContributionPlan(t testing.TB) generationlowering.Plan {
	t.Helper()
	sourceID := httpCapabilityID(t, "email.send/v1")
	policyID := httpCapabilityID(t, "policy.check/v1")
	sourceContract := httpCanonicalContract(t, plannedEmailSendSchema)
	policyContract := httpCanonicalContract(t, httpPolicyCheckSchema)
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "example.email", ModulePath: testModulePath, Provides: []string{sourceID.String()}, BuildMetadataJSON: []byte("{}")},
			{ID: "example.policy", ModulePath: testModulePath, Provides: []string{policyID.String()}, BuildMetadataJSON: []byte("{}")},
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: sourceContract, Exposure: generation.Exposure{HTTP: true, JavaScript: true}},
			{ContractJSON: policyContract},
		},
		Requirements: []string{sourceID.String(), policyID.String()},
		Providers: []generation.ProviderInput{
			{Capability: sourceID.String(), Plugin: "example.email"},
			{Capability: policyID.String(), Plugin: "example.policy"},
		},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	egress := httpPolicyCallContribution(
		t,
		"policy.egress",
		generation.GenerationPointHTTPEgress,
		"egress",
		generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
			Source: generation.GeneratedInvocationContextValue,
			Name:   "policy.message-id",
			Type:   generation.GeneratedValueString,
		}},
	)
	egress.Nodes[0].CapabilityCall.Request[0].Value = httpInvocationValue(generation.GeneratedInvocationResponseField, "message_id")
	egress.Nodes = append([]generation.GeneratedNode{{
		ID: "reject-invocation-error",
		ConditionalFailure: &generation.GeneratedConditionalFailure{
			Condition: generation.GeneratedCondition{
				Operator: generation.GeneratedConditionError,
				Value:    httpInvocationValue(generation.GeneratedInvocationError, ""),
			},
			ErrorCode: "temporarily_unavailable",
			Message:   "Canonical email invocation failed before HTTP egress.",
		},
	}}, egress.Nodes...)
	contributions := []generation.Contribution{
		httpPolicyCallContribution(t, "policy.ingress", generation.GenerationPointHTTPIngress, "ingress", httpAdapterCredential("authorization")),
		httpPolicyCallContribution(t, "policy.prepare", generation.GenerationPointInvocationPrepare, "prepare", generation.GeneratedValue{}),
		{
			ID:        "policy.complete",
			Namespace: "policy",
			Source:    sourceID,
			Point:     generation.GenerationPointInvocationComplete,
			Nodes: []generation.GeneratedNode{
				{
					ID: "reject-invocation-error",
					ConditionalFailure: &generation.GeneratedConditionalFailure{
						Condition: generation.GeneratedCondition{
							Operator: generation.GeneratedConditionError,
							Value:    httpInvocationValue(generation.GeneratedInvocationError, ""),
						},
						ErrorCode: "temporarily_unavailable",
						Message:   "Canonical email invocation failed.",
					},
				},
				{
					ID: "remember-message",
					ContextDerivation: &generation.GeneratedContextDerivation{
						Key:          "policy.message-id",
						Value:        httpInvocationValue(generation.GeneratedInvocationResponseField, "message_id"),
						Type:         generation.GeneratedValueString,
						Presence:     generation.GeneratedContextRequired,
						MaximumBytes: 256,
					},
				},
				httpPolicyCallNode(t, "check-complete", "complete", generation.GeneratedValue{}),
			},
		},
		egress,
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

func httpPolicyCallContribution(
	t testing.TB,
	id string,
	point generation.GenerationPoint,
	permission string,
	credential generation.GeneratedValue,
) generation.Contribution {
	t.Helper()
	return generation.Contribution{
		ID:        id,
		Namespace: "policy",
		Source:    httpCapabilityID(t, "email.send/v1"),
		Point:     point,
		Nodes:     []generation.GeneratedNode{httpPolicyCallNode(t, "check-"+permission, permission, credential)},
	}
}

func httpPolicyCallNode(t testing.TB, id, permission string, credential generation.GeneratedValue) generation.GeneratedNode {
	t.Helper()
	bindings := []generation.GeneratedFieldBinding{
		{Field: "permission", Value: generation.StringValue(permission)},
		{Field: "retry_count", Value: generation.IntegerValue(1)},
		{Field: "enforce", Value: generation.BooleanValue(true)},
	}
	if credential.Invocation != nil || credential.Literal != nil || credential.Node != nil {
		bindings = append(bindings, generation.GeneratedFieldBinding{Field: "credential", Value: credential})
	}
	return generation.GeneratedNode{
		ID: id,
		CapabilityCall: &generation.GeneratedCapabilityCall{
			Capability:          httpCapabilityID(t, "policy.check/v1"),
			Request:             bindings,
			TimeoutMilliseconds: 100,
			OnError:             generation.GeneratedCallFailClosed,
		},
	}
}

func httpAdapterCredential(name string) generation.GeneratedValue {
	return httpInvocationValue(generation.GeneratedInvocationAdapterCredential, name)
}

func httpInvocationValue(source generation.GeneratedInvocationValueSource, name string) generation.GeneratedValue {
	return generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: source, Name: name}}
}

func httpCanonicalContract(t testing.TB, schema string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}

func assertGeneratedHTTPPlanRuns(t testing.TB, adapter, alias httpgen.File, invocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(plannedEmailSendSchema))
	if err != nil {
		t.Fatalf("Render(source contract): %v", err)
	}
	policyContract, err := contractgen.Render([]byte(httpPolicyCheckSchema))
	if err != nil {
		t.Fatalf("Render(policy contract): %v", err)
	}
	policyInvocation, err := invocationgen.Render(testModulePath, []byte(httpPolicyCheckSchema))
	if err != nil {
		t.Fatalf("Render(policy invocation): %v", err)
	}
	policyClient, err := clientgen.Render(testModulePath, []byte(httpPolicyCheckSchema))
	if err != nil {
		t.Fatalf("Render(policy client): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{policyContract.Path(), policyContract.Data()},
		{policyInvocation.Path(), policyInvocation.Data()},
		{policyClient.Path(), policyClient.Data()},
		{contextFile.Path(), contextFile.Data()},
		{invocation.Path(), invocation.Data()},
		{adapter.Path(), adapter.Data()},
		{alias.Path(), alias.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/code.go", []byte(testKernelInvocationCodeSource))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(testKernelInvocationSource))
	writeGeneratedFile(t, root, "generated/go/adapters/http/email/send/v1/handler_plan_gen_test.go", []byte(generatedHTTPPlanRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated planned HTTP module: %v\n%s", err, output)
	}
}

const generatedHTTPPlanRuntimeTest = `package emailsendv1_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	adapter "example.com/acme/project/generated/go/adapters/http/email/send/v1"
	aliasadapter "example.com/acme/project/generated/go/adapters/http/mail/deliver/v1"
	policyclient "example.com/acme/project/generated/go/clients/policy/check/v1"
	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/check/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestGeneratedHTTPContributionLifecycle(t *testing.T) {
	sequence := []string{}
	policyTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request policycontract.Request) (policycontract.Response, error) {
		sequence = append(sequence, "policy:"+request.Permission)
		switch request.Permission {
		case "ingress":
			if request.Credential == nil || *request.Credential != "Bearer adapter-token" {
				return policycontract.Response{}, errors.New("credential secret rejected")
			}
		case "Welcome":
			if request.Credential == nil || *request.Credential != "Welcome" {
				return policycontract.Response{}, errors.New("propagated context missing")
			}
		default:
			if request.Credential != nil {
				t.Fatalf("unexpected credential in %s: %q", request.Permission, *request.Credential)
			}
		}
		return policycontract.Response{Allowed: true}, nil
	})
	emailTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request contract.Request) (contract.Response, error) {
		sequence = append(sequence, "email:"+request.Subject)
		return contract.Response{MessageID: request.Subject, Status: contract.ResponseStatusSent}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	application := applicationinvocation.New(emailTarget, policy)
	if _, err := aliasadapter.New(adapter.Handler{}); !errors.Is(err, adapter.ErrInvalidHandler) {
		t.Fatalf("zero canonical handler error = %v", err)
	}
	canonicalHandler, err := adapter.New(func(request *http.Request) (context.Context, error) {
		return request.Context(), nil
	}, application)
	if err != nil {
		t.Fatalf("canonical New: %v", err)
	}
	handler, err := aliasadapter.New(canonicalHandler)
	if err != nil || !aliasadapter.Available(handler) {
		t.Fatalf("Alias New: %v, available %t", err, aliasadapter.Available(handler))
	}
	wrongRoute := httptest.NewRequest(http.MethodPost, adapter.RoutePath, strings.NewReader("{}"))
	wrongRoute.Header.Set("Content-Type", "application/json")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongRoute)
	if wrongResponse.Code != http.StatusNotFound || len(sequence) != 0 {
		t.Fatalf("canonical path through Alias = %d %s, sequence %v", wrongResponse.Code, wrongResponse.Body.String(), sequence)
	}

	request := httptest.NewRequest(http.MethodPost, aliasadapter.RoutePath, strings.NewReader("{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\"}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "  Bearer adapter-token  ")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !slices.Equal(sequence, []string{"policy:ingress", "policy:prepare", "email:Welcome", "policy:complete", "policy:Welcome"}) {
		t.Fatalf("HTTP response = %d %s, sequence %v", response.Code, response.Body.String(), sequence)
	}

	sequence = nil
	internal, err := application.Invoke(context.Background(), contract.Request{To: []string{"person@example.com"}, Subject: "Internal"})
	if err != nil || internal.MessageID != "Internal" || !slices.Equal(sequence, []string{"policy:prepare", "email:Internal", "policy:complete"}) {
		t.Fatalf("internal Invoke = %#v, %v, sequence %v", internal, err, sequence)
	}
	sequence = nil
	_, err = application.InvokeHTTP(context.Background(), contract.Request{To: []string{"person@example.com"}, Subject: "Panic"}, func(string) (string, bool) {
		panic("credential source secret")
	})
	if err == nil || !slices.Equal(sequence, []string{"policy:ingress"}) {
		t.Fatalf("panicking credential source = %v, sequence %v", err, sequence)
	}

	invalidCredentials := []struct {
		name string
		values []string
	}{
		{name: "missing"},
		{name: "duplicate", values: []string{"Bearer one", "Bearer two"}},
		{name: "control", values: []string{"Bearer\rsecret"}},
		{name: "oversized", values: []string{strings.Repeat("x", adapter.MaximumAdapterCredentialBytes+1)}},
	}
	for _, test := range invalidCredentials {
		t.Run(test.name, func(t *testing.T) {
			sequence = nil
			request := httptest.NewRequest(http.MethodPost, aliasadapter.RoutePath, strings.NewReader("{\"to\":[\"person@example.com\"],\"subject\":\"Blocked\"}"))
			request.Header.Set("Content-Type", "application/json")
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "\"code\":\"internal\"") || strings.Contains(response.Body.String(), "secret") || !slices.Equal(sequence, []string{"policy:ingress"}) {
				t.Fatalf("invalid credential response = %d %s, sequence %v", response.Code, response.Body.String(), sequence)
			}
		})
	}
}
`
