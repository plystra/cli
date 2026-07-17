package httpgen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/httpgen"
	"github.com/plystra/cli/internal/invocationgen"
)

const (
	testModulePath  = "example.com/acme/project"
	emailSendSchema = `id: email.send/v1
description: Sends an email message.
request:
  to: {type: array, items: string, required: true}
  subject: {type: string, required: true}
  priority: {type: string, enum: [normal, urgent]}
  text: {type: string}
  html: {type: string}
response:
  message_id: {type: string, required: true}
  status: {type: string, enum: [queued, sent], required: true}
errors: [invalid_recipient, authentication_failed, temporarily_unavailable]
`
)

func TestRenderCanonicalHTTPAdapter(t *testing.T) {
	t.Parallel()

	target := exposedTarget(t, emailSendSchema)
	file, err := httpgen.Render(testModulePath, target)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if file.Path() != "generated/go/adapters/http/email/send/v1/handler_gen.go" || file.PackageName() != "emailsendv1" {
		t.Fatalf("generated file = path %q, package %q", file.Path(), file.PackageName())
	}
	generated := strings.Join(strings.Fields(string(file.Data())), " ")
	for _, required := range []string{
		`RoutePath = "/api/v1/capabilities/email.send/v1/invoke"`,
		`applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"`,
		`func New(root RootContext, target applicationinvocation.Handle) (Handler, error)`,
		`response, err := plystraInvoke(ctx, h.target, decoded)`,
		`decoder.Token()`,
		`bytes.Equal(bytes.TrimSpace(value), []byte("null"))`,
		`http.MaxBytesReader(writer, request.Body, MaximumRequestBytes)`,
		`plystraWriteError(writer, http.StatusUnprocessableEntity, "capability_error", semantic)`,
	} {
		if !strings.Contains(generated, required) {
			t.Fatalf("generated adapter omits %q:\n%s", required, file.Data())
		}
	}
	if bytes.Contains(file.Data(), []byte("Dispatcher")) || bytes.Contains(file.Data(), []byte("kernelinvocation.Handle")) {
		t.Fatalf("generated adapter bypasses canonical application invocation:\n%s", file.Data())
	}
	want, err := os.ReadFile("testdata/email.send.v1.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated adapter:\n%s\nwant:\n%s", file.Data(), want)
	}
	contract, err := contractgen.Render([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(emailSendSchema))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	assertGeneratedHTTPAdapterRuns(t, contract, invocation, file)
	returned := file.Data()
	returned[0] = 'x'
	if bytes.Equal(returned, file.Data()) {
		t.Fatal("Data exposed mutable generated storage")
	}
	repeated, err := httpgen.Render(testModulePath, target)
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated Render = %#v, %v", repeated, err)
	}
}

func TestRenderDerivesHierarchicalCanonicalRoute(t *testing.T) {
	t.Parallel()

	file, err := httpgen.Render("example.com/acme/project/v3", exposedTarget(t, "id: authn.login.oidc.complete/v12\n"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	generated := strings.Join(strings.Fields(string(file.Data())), " ")
	if file.Path() != "generated/go/adapters/http/authn/login/oidc/complete/v12/handler_gen.go" || file.PackageName() != "authnloginoidccompletev12" || !strings.Contains(generated, `RoutePath = "/api/v1/capabilities/authn.login.oidc.complete/v12/invoke"`) {
		t.Fatalf("hierarchical generated file = path %q, package %q\n%s", file.Path(), file.PackageName(), file.Data())
	}
}

func TestRenderRejectsInvalidOrUnexposedTarget(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymeta.NormalizeSchema([]byte(emailSendSchema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	valid := testTarget{
		id:       httpCapabilityID(t, "email.send/v1"),
		contract: canonical,
		digest:   digest(canonical),
		exposure: generation.Exposure{HTTP: true},
	}
	tests := []struct {
		name       string
		modulePath string
		target     httpgen.CanonicalTargetView
		want       string
		also       error
	}{
		{name: "invalid module", modulePath: "../application", target: valid, want: "Go Module path"},
		{name: "absent target", modulePath: testModulePath, target: testTarget{}, want: "target view is absent", also: httpgen.ErrTarget},
		{name: "internal target", modulePath: testModulePath, target: withTarget(valid, func(value *testTarget) { value.exposure.HTTP = false }), want: "not explicitly exposed", also: httpgen.ErrTarget},
		{name: "noncanonical contract", modulePath: testModulePath, target: withTarget(valid, func(value *testTarget) { value.contract = append([]byte(" "), value.contract...) }), want: "not canonical", also: httpgen.ErrTarget},
		{name: "identity mismatch", modulePath: testModulePath, target: withTarget(valid, func(value *testTarget) { value.id = httpCapabilityID(t, "mail.send/v1") }), want: "contract declares", also: httpgen.ErrTarget},
		{name: "digest mismatch", modulePath: testModulePath, target: withTarget(valid, func(value *testTarget) { value.digest = "sha256:" + strings.Repeat("0", 64) }), want: "digest", also: httpgen.ErrTarget},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file, err := httpgen.Render(test.modulePath, test.target)
			if !errors.Is(err, httpgen.ErrRender) || test.also != nil && !errors.Is(err, test.also) || !strings.Contains(err.Error(), test.want) || file.Path() != "" || file.PackageName() != "" || file.Data() != nil {
				t.Fatalf("Render = %#v, %v; want %q", file, err, test.want)
			}
		})
	}
}

func FuzzRender(f *testing.F) {
	for _, seed := range []string{emailSendSchema, "id: kernel.health/v1\n", "id: workflow.retry--now-/v2\n", "[]\n", "id: &x example.call/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		canonical, err := capabilitymeta.NormalizeSchema([]byte(input))
		if err != nil {
			return
		}
		manifest, err := capabilitymeta.Parse(canonical)
		if err != nil {
			t.Fatalf("Parse canonical schema: %v", err)
		}
		identifier, err := generation.ParseCapabilityID(manifest.ID().String())
		if err != nil {
			t.Fatalf("ParseCapabilityID: %v", err)
		}
		target := testTarget{id: identifier, contract: canonical, digest: digest(canonical), exposure: generation.Exposure{HTTP: true}}
		first, err := httpgen.Render(testModulePath, target)
		if err != nil {
			if !errors.Is(err, httpgen.ErrRender) {
				t.Fatalf("Render returned unexpected error: %v", err)
			}
			return
		}
		second, err := httpgen.Render(testModulePath, target)
		if err != nil || first.Path() != second.Path() || first.PackageName() != second.PackageName() || !bytes.Equal(first.Data(), second.Data()) {
			t.Fatalf("Render is not deterministic: %#v then %#v, %v", first, second, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), first.Path(), first.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse generated Go: %v\n%s", err, first.Data())
		}
	})
}

type testTarget struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
	exposure generation.Exposure
}

func (t testTarget) ID() generation.CapabilityID   { return t.id }
func (t testTarget) ContractJSON() []byte          { return append([]byte(nil), t.contract...) }
func (t testTarget) ContractDigest() string        { return t.digest }
func (t testTarget) Exposure() generation.Exposure { return t.exposure }

func withTarget(value testTarget, mutate func(*testTarget)) testTarget {
	mutate(&value)
	return value
}

func exposedTarget(t testing.TB, schema string) generation.CapabilityView {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	manifest, err := capabilitymeta.Parse(canonical)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	context, err := generation.NewContext(generation.Input{
		Capabilities: []generation.CapabilityInput{{ContractJSON: canonical, Intrinsic: strings.HasPrefix(manifest.ID().Name(), "kernel."), Exposure: generation.Exposure{HTTP: true, JavaScript: true}}},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	id := httpCapabilityID(t, manifest.ID().String())
	target, exists := context.Capability(id)
	if !exists {
		t.Fatalf("Capability(%s) is absent", id)
	}
	return target
}

func httpCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assertGeneratedHTTPAdapterRuns(t testing.TB, contract contractgen.File, invocation invocationgen.File, adapter httpgen.File) {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	writeGeneratedFile(t, root, adapter.Path(), adapter.Data())
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/audit/error.go", []byte(testKernelAuditSource))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(testKernelInvocationSource))
	writeGeneratedFile(t, root, "generated/go/adapters/http/email/send/v1/handler_gen_test.go", []byte(generatedHTTPRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated HTTP module: %v\n%s", err, output)
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

const testKernelAuditSource = `package audit

import "strings"

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorNotFound ErrorCode = "not_found"
	ErrorConflict ErrorCode = "conflict"
	ErrorDenied ErrorCode = "denied"
	ErrorUnauthenticated ErrorCode = "unauthenticated"
	ErrorUnavailable ErrorCode = "unavailable"
	ErrorTimeout ErrorCode = "timeout"
	ErrorCancelled ErrorCode = "cancelled"
	ErrorResultUnknown ErrorCode = "result_unknown"
	ErrorInternal ErrorCode = "internal"
	ErrorVersionIncompatible ErrorCode = "version_incompatible"
)

func (c ErrorCode) String() string { return string(c) }
func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorDenied, ErrorUnauthenticated, ErrorUnavailable, ErrorTimeout, ErrorCancelled, ErrorResultUnknown, ErrorInternal, ErrorVersionIncompatible:
		return true
	default:
		return false
	}
}
func ValidDetailCode(value string) bool {
	return value == "" || len(value) <= 128 && !strings.ContainsAny(value, " \r\n\x00")
}
`

const testKernelInvocationSource = `package invocation

import (
	"context"

	"github.com/plystra/kernel/audit"
)

type Handle[Request, Response any] struct {
	available bool
	invoke func(context.Context, Request) (Response, error)
}

func NewTestHandle[Request, Response any](available bool, invoke func(context.Context, Request) (Response, error)) Handle[Request, Response] {
	return Handle[Request, Response]{available: available, invoke: invoke}
}
func (h Handle[Request, Response]) Available() bool { return h.available }
func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	if h.invoke != nil {
		return h.invoke(ctx, request)
	}
	var response Response
	return response, nil
}

type Error struct {
	code audit.ErrorCode
	detail string
}
func NewTestError(code audit.ErrorCode, detail string) *Error { return &Error{code: code, detail: detail} }
func (e *Error) Error() string { return "classified secret must not cross transport" }
func (e *Error) Code() audit.ErrorCode { return e.code }
func (e *Error) DetailCode() string { return e.detail }
`

const generatedHTTPRuntimeTest = `package emailsendv1_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adapter "example.com/acme/project/generated/go/adapters/http/email/send/v1"
	contract "example.com/acme/project/generated/go/contracts/email/send/v1"
	applicationinvocation "example.com/acme/project/generated/go/invocation/email/send/v1"
	kernelaudit "github.com/plystra/kernel/audit"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestGeneratedHTTPHandlerValidatesAndInvokesCanonicalPath(t *testing.T) {
	calls := 0
	rootCalls := 0
	target := kernelinvocation.NewTestHandle(true, func(_ context.Context, request contract.Request) (contract.Response, error) {
		calls++
		switch request.Subject {
		case "semantic":
			return contract.Response{}, contract.ErrInvalidRecipient
		case "denied":
			return contract.Response{}, kernelinvocation.NewTestError(kernelaudit.ErrorDenied, "authorization.denied")
		case "cancelled":
			return contract.Response{}, context.Canceled
		case "unknown-secret":
			return contract.Response{}, errors.New("provider secret must not cross transport")
		case "panic":
			panic("panic secret must not cross transport")
		case "bad-response":
			return contract.Response{MessageID: "message", Status: contract.ResponseStatus("invalid")}, nil
		case "oversized-response":
			return contract.Response{MessageID: strings.Repeat("x", adapter.MaximumResponseBytes), Status: contract.ResponseStatusSent}, nil
		default:
			if len(request.To) != 1 || request.To[0] != "person@example.com" {
				t.Fatalf("request = %#v", request)
			}
			return contract.Response{MessageID: request.Subject, Status: contract.ResponseStatusSent}, nil
		}
	})
	handler, err := adapter.New(func(request *http.Request) (context.Context, error) {
		rootCalls++
		return request.Context(), nil
	}, applicationinvocation.New(target))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	valid := httptest.NewRequest(http.MethodPost, adapter.RoutePath, strings.NewReader("{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\",\"priority\":\"urgent\"}"))
	valid.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, valid)
	if response.Code != http.StatusOK || response.Body.String() != "{\"message_id\":\"Welcome\",\"status\":\"sent\"}" || calls != 1 || rootCalls != 1 {
		t.Fatalf("valid response = %d %s, calls %d/%d", response.Code, response.Body.String(), calls, rootCalls)
	}
	for name, want := range map[string]string{"Content-Type":"application/json", "Cache-Control":"no-store", "X-Content-Type-Options":"nosniff"} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}

	tests := []struct {
		name string
		method string
		path string
		body string
		contentType string
		mutate func(*http.Request)
		status int
		code string
	}{
		{name:"wrong path", method:http.MethodPost, path:"/wrong", body:"{}", contentType:"application/json", status:http.StatusNotFound, code:"not_found"},
		{name:"wrong method", method:http.MethodGet, path:adapter.RoutePath, body:"{}", contentType:"application/json", status:http.StatusMethodNotAllowed, code:"method_not_allowed"},
		{name:"query", method:http.MethodPost, path:adapter.RoutePath+"?debug=true", body:"{}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"encoded path", method:http.MethodPost, path:adapter.RoutePath, body:"{}", contentType:"application/json", mutate:func(request *http.Request){ request.URL.RawPath = "/api/v1/capabilities/email%2Esend/v1/invoke" }, status:http.StatusNotFound, code:"not_found"},
		{name:"missing content type", method:http.MethodPost, path:adapter.RoutePath, body:"{}", status:http.StatusUnsupportedMediaType, code:"unsupported_media_type"},
		{name:"wrong charset", method:http.MethodPost, path:adapter.RoutePath, body:"{}", contentType:"application/json; charset=iso-8859-1", status:http.StatusUnsupportedMediaType, code:"unsupported_media_type"},
		{name:"duplicate content type", method:http.MethodPost, path:adapter.RoutePath, body:"{}", contentType:"application/json", mutate:func(request *http.Request){ request.Header.Add("Content-Type", "application/json") }, status:http.StatusUnsupportedMediaType, code:"unsupported_media_type"},
		{name:"encoded body", method:http.MethodPost, path:adapter.RoutePath, body:"{}", contentType:"application/json", mutate:func(request *http.Request){ request.Header.Set("Content-Encoding", "gzip") }, status:http.StatusUnsupportedMediaType, code:"unsupported_media_type"},
		{name:"empty", method:http.MethodPost, path:adapter.RoutePath, body:"", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"null", method:http.MethodPost, path:adapter.RoutePath, body:"null", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"array", method:http.MethodPost, path:adapter.RoutePath, body:"[]", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"missing required", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":[\"person@example.com\"]}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"required null", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":null,\"subject\":\"Welcome\"}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"unknown field", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\",\"unknown\":true}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"duplicate field", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\",\"subject\":\"Again\"}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"wrong type", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":true,\"subject\":\"Welcome\"}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"invalid enum", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\",\"priority\":\"immediate\"}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"trailing value", method:http.MethodPost, path:adapter.RoutePath, body:"{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\"}{}", contentType:"application/json", status:http.StatusBadRequest, code:"invalid_request"},
		{name:"oversized", method:http.MethodPost, path:adapter.RoutePath, body:strings.Repeat("x", int(adapter.MaximumRequestBytes)+1), contentType:"application/json", status:http.StatusRequestEntityTooLarge, code:"payload_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeCalls, beforeRoots := calls, rootCalls
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.contentType != "" { request.Header.Set("Content-Type", test.contentType) }
			if test.mutate != nil { test.mutate(request) }
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertError(t, response, test.status, test.code)
			if calls != beforeCalls || rootCalls != beforeRoots {
				t.Fatalf("invalid request reached root/invocation: %d/%d -> %d/%d", beforeCalls, beforeRoots, calls, rootCalls)
			}
		})
	}

	for _, test := range []struct{name string; subject string; status int; code string; detail string}{
		{name:"semantic", subject:"semantic", status:http.StatusUnprocessableEntity, code:"capability_error", detail:"invalid_recipient"},
		{name:"classified", subject:"denied", status:http.StatusForbidden, code:"denied", detail:"authorization.denied"},
		{name:"cancelled", subject:"cancelled", status:499, code:"cancelled"},
		{name:"unknown", subject:"unknown-secret", status:http.StatusInternalServerError, code:"internal"},
		{name:"panic", subject:"panic", status:http.StatusInternalServerError, code:"internal"},
		{name:"invalid response", subject:"bad-response", status:http.StatusInternalServerError, code:"internal"},
		{name:"oversized response", subject:"oversized-response", status:http.StatusInternalServerError, code:"internal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, adapter.RoutePath, strings.NewReader("{\"to\":[\"person@example.com\"],\"subject\":\""+test.subject+"\"}"))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertError(t, response, test.status, test.code)
			if test.detail != "" && !strings.Contains(response.Body.String(), "\"detail_code\":\""+test.detail+"\"") {
				t.Fatalf("detail = %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "panic") {
				t.Fatalf("unsafe error body = %s", response.Body.String())
			}
		})
	}
}

func TestGeneratedHTTPHandlerRejectsInvalidConstructionAndRoot(t *testing.T) {
	target := kernelinvocation.NewTestHandle(true, func(_ context.Context, request contract.Request) (contract.Response, error) {
		return contract.Response{MessageID: request.Subject, Status: contract.ResponseStatusSent}, nil
	})
	if _, err := adapter.New(nil, applicationinvocation.New(target)); !errors.Is(err, adapter.ErrInvalidHandler) {
		t.Fatalf("nil root error = %v", err)
	}
	unavailable := kernelinvocation.NewTestHandle[contract.Request, contract.Response](false, nil)
	if _, err := adapter.New(func(request *http.Request) (context.Context, error) { return request.Context(), nil }, applicationinvocation.New(unavailable)); !errors.Is(err, adapter.ErrInvalidHandler) {
		t.Fatalf("unavailable target error = %v", err)
	}
	for name, root := range map[string]adapter.RootContext{
		"error": func(*http.Request) (context.Context, error) { return nil, errors.New("root secret") },
		"panic": func(*http.Request) (context.Context, error) { panic("root panic secret") },
		"nil": func(*http.Request) (context.Context, error) { return nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			handler, err := adapter.New(root, applicationinvocation.New(target))
			if err != nil { t.Fatalf("New: %v", err) }
			request := httptest.NewRequest(http.MethodPost, adapter.RoutePath, strings.NewReader("{\"to\":[\"person@example.com\"],\"subject\":\"Welcome\"}"))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertError(t, response, http.StatusInternalServerError, "internal")
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "panic") { t.Fatalf("unsafe body = %s", response.Body.String()) }
		})
	}
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Body.String(), "\"code\":\""+code+"\"") {
		t.Fatalf("response = %d %s, want %d/%s", response.Code, response.Body.String(), status, code)
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %#v", response.Header())
	}
}
`
