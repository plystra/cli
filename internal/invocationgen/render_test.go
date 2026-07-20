package invocationgen_test

import (
	"bytes"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

const (
	testModulePath  = "example.com/acme/project"
	emailSendSchema = `id: email.send/v1
description: Sends an email message.
request:
  to: {type: array, items: string, required: true}
  subject: {type: string, required: true}
  text: {type: string}
  html: {type: string}
response:
  message_id: {type: string, required: true}
  status: {type: string, enum: [queued, sent], required: true}
errors: [invalid_recipient, authentication_failed, temporarily_unavailable]
`
)

func TestRenderGoldenCanonicalApplicationInvocation(t *testing.T) {
	t.Parallel()

	file, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want, err := os.ReadFile("testdata/email.send.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/invocation/email/send/v1/invocation_gen.go" || file.PackageName() != "emailsendv1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated file = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	assertGeneratedInvocationRuns(t, contract, file)
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated Render = %#v, %v", repeated, err)
	}
}

func TestRenderDerivesHierarchicalCapabilityPath(t *testing.T) {
	t.Parallel()

	file, err := invocationgen.Render("example.com/acme/project/v3", []byte("id: authn.login.oidc.complete/v12\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`contract "example.com/acme/project/v3/generated/go/contracts/authn/login/oidc/complete/v12"`,
		`target kernelinvocation.Handle[contract.Request, contract.Response]`,
		`func (h Handle) Invoke(ctx context.Context, request contract.Request) (contract.Response, error)`,
		`response, invocationError := h.target.Invoke(ctx, request)`,
		`if responseError := plystraValidateResponse(response); responseError != nil`,
		`type TransportErrorInput struct`,
		`func SafeTransportError(err error) (input TransportErrorInput)`,
		`SemanticErrorCode() string`,
		`KernelErrorClass() string`,
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("generated invocation does not contain %q:\n%s", required, file.Data())
		}
	}
	if file.Path() != "generated/go/invocation/authn/login/oidc/complete/v12/invocation_gen.go" || file.PackageName() != "authnloginoidccompletev12" {
		t.Fatalf("hierarchical generated file = path %q, package %q", file.Path(), file.PackageName())
	}
}

func TestRenderIgnoresNonSemanticSourceDifferences(t *testing.T) {
	t.Parallel()

	first, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render(first): %v", err)
	}
	second, err := invocationgen.Render(testModulePath, []byte("errors: [temporarily_unavailable, authentication_failed, invalid_recipient]\r\nresponse: {status: {required: true, enum: [sent, queued], type: string}, message_id: {required: true, type: string}}\r\nrequest: {html: {type: string}, text: {type: string}, subject: {required: true, type: string}, to: {required: true, items: string, type: array}}\r\ndescription: Different words.\r\nid: email.send/v1\r\n"))
	if err != nil || first.Path() != second.Path() || !bytes.Equal(first.Data(), second.Data()) {
		t.Fatalf("non-semantic rendering differs: %v\nfirst:\n%s\nsecond:\n%s", err, first.Data(), second.Data())
	}
}

func TestRenderRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modulePath string
		schema     string
		also       error
	}{
		{name: "empty module path", schema: emailSendSchema},
		{name: "invalid module path", modulePath: "example.com/acme/../project", schema: emailSendSchema},
		{name: "invalid schema", modulePath: testModulePath, schema: "id: example.call/v1\nrequest: []\n", also: capabilitymeta.ErrInvalidManifest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := invocationgen.Render(test.modulePath, []byte(test.schema))
			if !errors.Is(err, invocationgen.ErrRender) || test.also != nil && !errors.Is(err, test.also) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("Render = %#v, %v", file, err)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{emailSendSchema, "id: kernel.health/v1\n", "id: workflow.retry--now-/v2\n", "id: order.cancel/v1\nextensions: {authn: {authenticated: true}}\n", "[]\n", "id: &x example.call/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		first, err := invocationgen.Render(testModulePath, []byte(input))
		if err != nil {
			if !errors.Is(err, invocationgen.ErrRender) {
				t.Fatalf("Render returned unexpected error: %v", err)
			}
			return
		}
		second, err := invocationgen.Render(testModulePath, []byte(input))
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("Render is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Go: %v", err)
		}
	})
}

func assertGeneratedInvocationRuns(t testing.TB, contract contractgen.File, invocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/email/send/v1/invocation_gen_test.go", []byte(`package emailsendv1_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestCanonicalApplicationInvocationDelegates(t *testing.T) {
	calls := 0
	target := kernelinvocation.NewTestHandle(true, func(_ context.Context, request contract.Request) (contract.Response, error) {
		calls++
		return contract.Response{MessageID: request.Subject, Status: contract.ResponseStatusSent}, nil
	})
	handle := applicationinvocation.New(target)
	if !applicationinvocation.Available(handle) {
		t.Fatal("application invocation is unavailable")
	}
	response, err := handle.Invoke(context.Background(), contract.Request{Subject: "message-1", To: []string{"user@example.com"}})
	if err != nil || response.MessageID != "message-1" || response.Status != contract.ResponseStatusSent || calls != 1 {
		t.Fatalf("Invoke = %#v, %v, calls %d", response, err, calls)
	}
	invalid := kernelinvocation.NewTestHandle(true, func(context.Context, contract.Request) (contract.Response, error) {
		return contract.Response{MessageID: "must-be-discarded", Status: contract.ResponseStatus("invalid-secret-value")}, nil
	})
	response, err = applicationinvocation.New(invalid).Invoke(context.Background(), contract.Request{})
	if err == nil || err.Error() != "invalid canonical Provider response" || response != (contract.Response{}) || strings.Contains(err.Error(), "invalid-secret-value") {
		t.Fatalf("invalid Provider response = %#v, %v", response, err)
	}
	providerFailure := errors.New("provider failure")
	failing := kernelinvocation.NewTestHandle(true, func(context.Context, contract.Request) (contract.Response, error) {
		return contract.Response{MessageID: "must-be-discarded", Status: contract.ResponseStatusSent}, providerFailure
	})
	response, err = applicationinvocation.New(failing).Invoke(context.Background(), contract.Request{})
	if !errors.Is(err, providerFailure) || response != (contract.Response{}) {
		t.Fatalf("failed Provider response = %#v, %v", response, err)
	}
	if applicationinvocation.Available(applicationinvocation.Handle{}) {
		t.Fatal("zero application invocation is available")
	}
}
`))
	moduleFile := "module " + testModulePath + "\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"
	writeGeneratedFile(t, root, "go.mod", []byte(moduleFile))
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated module: %v\n%s", err, output)
	}
}

func writeGeneratedFile(t testing.TB, root, relative string, data []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
