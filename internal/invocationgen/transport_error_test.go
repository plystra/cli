package invocationgen_test

import (
	"testing"

	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/invocationgen"
)

func TestGeneratedInvocationSuppliesOneTransportSafeErrorInput(t *testing.T) {
	t.Parallel()

	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/email/send/v1/transport_error_gen_test.go", []byte(generatedTransportErrorRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

const generatedTransportErrorRuntimeTest = `package emailsendv1_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

type panickingAsError struct{}

func (panickingAsError) Error() string { return "Provider secret" }
func (panickingAsError) As(any) bool { panic("Provider secret") }

func TestTransportErrorInputIsClosedAndDataFree(t *testing.T) {
	typeOf := reflect.TypeOf(applicationinvocation.TransportErrorInput{})
	errorType := reflect.TypeFor[error]()
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.IsExported() {
			t.Fatalf("TransportErrorInput field %q is exported", field.Name)
		}
		if field.Type.Implements(errorType) || field.Type.Kind() == reflect.Interface || field.Type.Kind() == reflect.Pointer || field.Type.Kind() == reflect.Map || field.Type.Kind() == reflect.Slice {
			t.Fatalf("TransportErrorInput field %q can retain mutable or unrestricted data: %s", field.Name, field.Type)
		}
	}
	zero := applicationinvocation.TransportErrorInput{}
	if zero.Valid() || zero.SemanticErrorCode() != "" || zero.KernelErrorClass() != "" || zero.KernelDetailCode() != "" {
		t.Fatalf("zero transport input is valid: %#v", zero)
	}
}

func TestCanonicalInvocationProjectsTransportFailures(t *testing.T) {
	valid := contract.Response{MessageID: "message-1", Status: contract.ResponseStatusSent}
	response := valid
	var providerError error
	target := kernelinvocation.NewTestHandle(true, func(context.Context, contract.Request) (contract.Response, error) {
		return response, providerError
	})
	handle := applicationinvocation.New(target)

	tests := []struct {
		name string
		err error
		semantic string
		class string
		detail string
	}{
		{name: "Kernel semantic", err: kernelinvocation.NewTestSemanticError("invalid_recipient"), semantic: "invalid_recipient"},
		{name: "generated semantic", err: contract.ErrTemporarilyUnavailable, semantic: "temporarily_unavailable"},
		{name: "wrapped semantic", err: fmt.Errorf("outer Provider secret: %w", contract.ErrAuthenticationFailed), semantic: "authentication_failed"},
		{name: "undeclared semantic", err: kernelinvocation.NewTestSemanticError("provider_secret"), class: "internal"},
		{name: "panicking semantic", err: kernelinvocation.NewTestPanickingSemanticError(), class: "internal"},
		{name: "classified", err: kernelinvocation.NewTestError(kernelinvocation.ErrorDenied, "authorization.denied"), class: "denied", detail: "authorization.denied"},
		{name: "invalid classified detail", err: kernelinvocation.NewTestError(kernelinvocation.ErrorDenied, "Provider secret"), class: "internal"},
		{name: "denial without detail", err: kernelinvocation.NewTestError(kernelinvocation.ErrorDenied, ""), class: "internal"},
		{name: "deadline", err: fmt.Errorf("outer Provider secret: %w", context.DeadlineExceeded), class: "timeout"},
		{name: "cancellation", err: context.Canceled, class: "cancelled"},
		{name: "unknown", err: errors.New("Provider secret"), class: "internal"},
		{name: "panicking errors.As", err: panickingAsError{}, class: "internal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response = valid
			providerError = test.err
			gotResponse, invocationError := handle.Invoke(context.Background(), contract.Request{})
			if invocationError == nil || gotResponse != (contract.Response{}) {
				t.Fatalf("Invoke = %#v, %v", gotResponse, invocationError)
			}
			input := applicationinvocation.SafeTransportError(invocationError)
			if !input.Valid() || input.SemanticErrorCode() != test.semantic || input.KernelErrorClass() != test.class || input.KernelDetailCode() != test.detail {
				t.Fatalf("SafeTransportError = %#v; semantic %q, class %q, detail %q", input, input.SemanticErrorCode(), input.KernelErrorClass(), input.KernelDetailCode())
			}
			if rendered := fmt.Sprintf("%#v", input); strings.Contains(strings.ToLower(rendered), "secret") || strings.Contains(strings.ToLower(rendered), "provider") {
				t.Fatalf("transport input leaked Provider data: %s", rendered)
			}
		})
	}

	response = contract.Response{MessageID: "must-be-discarded", Status: contract.ResponseStatus("Provider-secret-invalid")}
	providerError = nil
	gotResponse, invocationError := handle.Invoke(context.Background(), contract.Request{})
	input := applicationinvocation.SafeTransportError(invocationError)
	if invocationError == nil || gotResponse != (contract.Response{}) || !input.Valid() || input.SemanticErrorCode() != "" || input.KernelErrorClass() != "internal" || input.KernelDetailCode() != "" {
		t.Fatalf("invalid response projection = %#v, %v, %#v", gotResponse, invocationError, input)
	}

	nilInput := applicationinvocation.SafeTransportError(nil)
	if !nilInput.Valid() || nilInput.KernelErrorClass() != "internal" || nilInput.SemanticErrorCode() != "" || nilInput.KernelDetailCode() != "" {
		t.Fatalf("nil error did not fail closed: %#v", nilInput)
	}
}
`
