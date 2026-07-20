package invocationgen_test

import (
	"bytes"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

func TestRenderPlanAdapterCredentialDerivationFailsClosedInternally(t *testing.T) {
	t.Parallel()

	contribution := generation.Contribution{
		ID:        "policy.credential",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "derive-credential",
			ContextDerivation: &generation.GeneratedContextDerivation{
				Key:          "policy.credential",
				Value:        planInvocationValue(generation.GeneratedInvocationAdapterCredential, "authorization"),
				Type:         generation.GeneratedValueString,
				Presence:     generation.GeneratedContextRequired,
				MaximumBytes: 64,
			},
		}},
	}
	plan := prepareSourcePlan(t, orderCreateSchema, []generation.Contribution{contribution})
	if !plan.RequiresHTTPPath(contribution.Source) || !plan.Contributions()[0].Nodes()[0].UsesAdapterCredential() {
		t.Fatal("adapter credential did not select the generated HTTP path")
	}
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	for _, required := range []string{
		"func (h Handle) InvokeHTTP(",
		`plystraAdapterCredential(adapterCredentials, "authorization")`,
		"if responseError := plystraValidateResponse(response); responseError != nil",
		"return contract.Response{}, invocationcontext.ErrInvalidValue",
	} {
		if !bytes.Contains(file.Data(), []byte(required)) {
			t.Fatalf("generated invocation omits %q:\n%s", required, file.Data())
		}
	}
	assertGeneratedAdapterCredentialDerivationRuns(t, file)
}

func assertGeneratedAdapterCredentialDerivationRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(source contract): %v", err)
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
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_http_gen_test.go", []byte(generatedAdapterCredentialRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

const generatedAdapterCredentialRuntimeTest = `package ordercreatev1_test

import (
	"context"
	"errors"
	"testing"

	contract "example.com/acme/project/generated/go/contracts/order/create/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	applicationinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestAdapterCredentialIsExternalAndFailClosed(t *testing.T) {
	dispatches := 0
	target := kernelinvocation.NewTestHandle(true, func(ctx context.Context, _ contract.Request) (contract.Response, error) {
		dispatches++
		credential, ok := invocationcontext.Value[string](ctx, "policy.credential")
		if !ok || credential != "Bearer adapter-token" {
			t.Fatalf("credential = %q, %t", credential, ok)
		}
		return contract.Response{Accepted: true}, nil
	})
	handle := applicationinvocation.New(target)
	request := contract.Request{OrderID: "order-1", Reject: false}

	response, err := handle.Invoke(context.Background(), request)
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || dispatches != 0 {
		t.Fatalf("internal Invoke = %#v, %v, dispatches %d", response, err, dispatches)
	}
	response, err = handle.InvokeHTTP(context.Background(), request, func(name string) (string, bool) {
		if name != "authorization" {
			t.Fatalf("credential name = %q", name)
		}
		return "Bearer adapter-token", true
	})
	if err != nil || !response.Accepted || dispatches != 1 {
		t.Fatalf("InvokeHTTP = %#v, %v, dispatches %d", response, err, dispatches)
	}
	response, err = handle.InvokeHTTP(context.Background(), request, nil)
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || dispatches != 1 {
		t.Fatalf("missing credential = %#v, %v, dispatches %d", response, err, dispatches)
	}
	response, err = handle.InvokeHTTP(context.Background(), request, func(string) (string, bool) {
		panic("credential source secret")
	})
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || dispatches != 1 {
		t.Fatalf("panicking credential source = %#v, %v, dispatches %d", response, err, dispatches)
	}
}
`
