package applicationgenerate_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/command"
)

func TestGeneratedInterfaceCallsKeepConnectExternal(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-connect"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [records.echo/v1]
http:
  transports:
    connect: false
`)
	writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"), `package echov1

import (
	"context"
	"time"
)

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Value Envelope `+"`json:\"value\" plystra:\"1,required\"`"+`
}

type Response struct {
	Value Envelope `+"`json:\"value\" plystra:\"1,required\"`"+`
}

type Envelope struct {
	Active     bool              `+"`json:\"active\" plystra:\"1,required\"`"+`
	Count32    int32             `+"`json:\"count_32\" plystra:\"2\"`"+`
	Count64    int64             `+"`json:\"count_64\" plystra:\"3\"`"+`
	Unsigned32 uint32            `+"`json:\"unsigned_32\" plystra:\"4\"`"+`
	Unsigned64 uint64            `+"`json:\"unsigned_64\" plystra:\"5\"`"+`
	Ratio32    float32           `+"`json:\"ratio_32\" plystra:\"6\"`"+`
	Ratio64    float64           `+"`json:\"ratio_64\" plystra:\"7\"`"+`
	Name       string            `+"`json:\"name\" plystra:\"8,required\"`"+`
	Payload    []byte            `+"`json:\"payload\" plystra:\"9,required\"`"+`
	Tags       []string          `+"`json:\"tags\" plystra:\"10\"`"+`
	Scores     map[string]int64  `+"`json:\"scores\" plystra:\"11\"`"+`
	Detail     Detail            `+"`json:\"detail\" plystra:\"12,required\"`"+`
	Items      []Detail          `+"`json:\"items\" plystra:\"13\"`"+`
	Lookup     map[string]Detail `+"`json:\"lookup\" plystra:\"14\"`"+`
	At         time.Time         `+"`json:\"at\" plystra:\"15,required\"`"+`
	Delay      time.Duration     `+"`json:\"delay\" plystra:\"16,required\"`"+`
}

type Detail struct {
	Code   string `+"`json:\"code\" plystra:\"1,required\"`"+`
	Amount int64  `+"`json:\"amount\" plystra:\"2\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.yaml"), `errors:
  - code: record_rejected
`)
	writeFile(t, filepath.Join(root, "records", "service.go"), `package records

import (
	"context"
	"errors"
	"sync/atomic"

	echov1 "example.com/interface-connect/interfaces/records/echo/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

type Service struct{}

var calls atomic.Int64

func Calls() int64 { return calls.Load() }

type semanticFailure string

func (failure semanticFailure) Error() string {
	return "private semantic failure: " + string(failure)
}

func (failure semanticFailure) SemanticErrorCode() string { return string(failure) }

//plystra:implements records.echo/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Echo(ctx context.Context, request echov1.Request) (echov1.Response, error) {
	if _, governed := kernelinvocation.Current(ctx); !governed {
		return echov1.Response{}, errors.New("Interface call bypassed Kernel governance")
	}
	calls.Add(1)
	switch request.Value.Detail.Code {
	case "semantic":
		return echov1.Response{}, semanticFailure("record_rejected")
	case "undeclared-semantic":
		return echov1.Response{}, semanticFailure("private_undeclared")
	case "unknown":
		return echov1.Response{}, errors.New("private unknown implementation failure")
	case "panic":
		panic("private implementation panic")
	case "invalid-response":
		request.Value.Name = string([]byte{0xff})
		return echov1.Response{Value: request.Value}, nil
	case "kernel-invalid-argument":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorInvalidArgument)
	case "kernel-not-found":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorNotFound)
	case "kernel-conflict":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorConflict)
	case "kernel-denied":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorDenied)
	case "kernel-unauthenticated":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorUnauthenticated)
	case "kernel-unavailable":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorUnavailable)
	case "kernel-timeout":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorTimeout)
	case "kernel-cancelled":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorCancelled)
	case "kernel-result-unknown":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorResultUnknown)
	case "kernel-internal":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorInternal)
	case "kernel-version-incompatible":
		return echov1.Response{}, kernelError(kernelinvocation.ErrorVersionIncompatible)
	}
	return echov1.Response{Value: request.Value}, nil
}

func kernelError(code kernelinvocation.ErrorCode) error {
	failure, err := kernelinvocation.NewError(code, "private.kernel_detail")
	if err != nil {
		panic(err)
	}
	return failure
}
`)

	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate internal-only Project = changes %#v, %v", generated.Report().Changes(), err)
	}
	internalModule := readFile(t, root, "go.mod")
	for _, externalModule := range []string{"connectrpc.com/connect", "google.golang.org/protobuf"} {
		if bytes.Contains(internalModule, []byte(externalModule)) {
			t.Fatalf("internal-only Project depends on external transport module %q:\n%s", externalModule, internalModule)
		}
	}
	const handlerPath = "generated/go/adapters/connect/records/echo/v1/handler_gen.go"
	assertFileMissing(t, root, handlerPath)
	internalPaths := []string{
		"generated/go/proxies/records/echo/v1/proxy_gen.go",
		"generated/go/adapters/implementations/records/echo/v1/adapter_gen.go",
		"generated/go/assembly/interfaces_gen.go",
	}
	internalSources := make(map[string][]byte, len(internalPaths))
	for _, internalPath := range internalPaths {
		source := readFile(t, root, internalPath)
		assertInternalInterfaceSourceIsTransportBlind(t, internalPath, source)
		internalSources[internalPath] = source
	}
	writeFile(t, filepath.Join(root, "internal_interface_test.go"), generatedInternalInterfaceRuntimeTest)
	runGeneratedInterfaceProjectTests(t, root, "internal-only")

	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [records.echo/v1]
http:
  transports:
    connect: true
  expose: [records.echo/v1]
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); exitCode != 0 {
		t.Fatalf("plystra generate externally exposed Project = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	exposedModule := readFile(t, root, "go.mod")
	for _, externalModule := range []string{"connectrpc.com/connect v1.20.0", "google.golang.org/protobuf v1.36.11"} {
		if !bytes.Contains(exposedModule, []byte(externalModule)) {
			t.Fatalf("externally exposed Project omits direct transport requirement %q:\n%s", externalModule, exposedModule)
		}
	}
	for _, internalPath := range internalPaths {
		source := readFile(t, root, internalPath)
		assertInternalInterfaceSourceIsTransportBlind(t, internalPath, source)
		if !bytes.Equal(source, internalSources[internalPath]) {
			t.Fatalf("enabling external Connect exposure changed internal Interface path %s:\nbefore:\n%s\nafter:\n%s", internalPath, internalSources[internalPath], source)
		}
	}

	handlerSource := readFile(t, root, handlerPath)
	for _, required := range []string{
		"target contract.Interface",
		"return target.Echo(ctx, request)",
		"connect.NewUnaryHandler(",
		`kernelinvocation "github.com/plystra/kernel/invocation"`,
		"errors.As(err, &semantic)",
		"errors.As(err, &kernel)",
		`case "record_rejected":`,
		`"requested_interface_id"`,
		`"canonical_interface_id"`,
		"connect.NewErrorDetail(message)",
	} {
		if !bytes.Contains(handlerSource, []byte(required)) {
			t.Fatalf("%s omits %q:\n%s", handlerPath, required, handlerSource)
		}
	}
	for _, forbidden := range []string{
		"applicationinvocation",
		modulePath + "/generated/go/invocations/",
		modulePath + "/records\"",
		"NewHandle(",
		"DetailCode()",
		"err.Error()",
	} {
		if bytes.Contains(handlerSource, []byte(forbidden)) {
			t.Fatalf("%s contains forbidden direct runtime dependency %q:\n%s", handlerPath, forbidden, handlerSource)
		}
	}
	for _, private := range []string{
		"private semantic failure",
		"private_undeclared",
		"private unknown implementation failure",
		"private implementation panic",
		"private.kernel_detail",
	} {
		for _, artifact := range []struct {
			name string
			data []byte
		}{
			{name: handlerPath, data: handlerSource},
			{name: "generated/manifest.json", data: readFile(t, root, "generated/manifest.json")},
		} {
			if bytes.Contains(artifact.data, []byte(private)) {
				t.Fatalf("%s leaks private implementation text %q", artifact.name, private)
			}
		}
	}
	assemblySource := internalSources["generated/go/assembly/interfaces_gen.go"]
	if !bytes.Contains(assemblySource, []byte("func (runtime InterfaceRuntime) RecordsEchoV1()")) {
		t.Fatalf("generated assembly omits the governed records.echo/v1 accessor:\n%s", assemblySource)
	}

	runtimeTestPath := filepath.Join(root, "generated", "go", "adapters", "connect", "records", "echo", "v1", "handler_integration_test.go")
	writeFile(t, runtimeTestPath, generatedConnectInterfaceRuntimeTest)
	runGeneratedInterfaceProjectTests(t, root, "externally exposed")
	if err := os.Remove(runtimeTestPath); err != nil {
		t.Fatalf("remove temporary generated Interface integration test: %v", err)
	}

	check, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if err != nil || !check.Report().Clean() {
		t.Fatalf("Generate --check = changes %#v, %v", check.Report().Changes(), err)
	}
}

func assertInternalInterfaceSourceIsTransportBlind(t testing.TB, name string, source []byte) {
	t.Helper()
	for _, forbidden := range []string{
		"connectrpc.com/connect",
		"google.golang.org/protobuf",
		"generated/go/adapters/connect",
		"net/http",
		"dynamicpb",
		"protoreflect",
	} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("internal Interface path %s depends on external transport %q:\n%s", name, forbidden, source)
		}
	}
}

func runGeneratedInterfaceProjectTests(t testing.TB, root, phase string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "test", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go test generated %s Interface Project: %v\n%s", phase, err, output)
	}
}

const generatedInternalInterfaceRuntimeTest = `package interfaceconnect_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	bootstrap "example.com/interface-connect/generated/go/bootstrap"
	echov1 "example.com/interface-connect/interfaces/records/echo/v1"
)

func TestInternalInterfaceCallWithoutConnect(t *testing.T) {
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Application.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Errorf("Application.Stop: %v", err)
		}
	})

	input := echov1.Request{Value: echov1.Envelope{
		Active: true, Name: "internal", Payload: []byte{1},
		Detail: echov1.Detail{Code: "in-process"},
		At: time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
		Delay: time.Second,
	}}
	response, err := application.Interfaces().RecordsEchoV1().Echo(context.Background(), input)
	if err != nil || !reflect.DeepEqual(response, echov1.Response{Value: input.Value}) {
		t.Fatalf("internal governed call = %#v, %v", response, err)
	}
}
`

const generatedConnectInterfaceRuntimeTest = `package recordsechov1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	connectadapter "example.com/interface-connect/generated/go/adapters/connect/records/echo/v1"
	bootstrap "example.com/interface-connect/generated/go/bootstrap"
	connectschema "example.com/interface-connect/generated/go/internal/connectschema"
	echov1 "example.com/interface-connect/interfaces/records/echo/v1"
	"example.com/interface-connect/records"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const requestJSON = ` + "`" + `{
	"value": {
		"active": true,
		"count_32": -32,
		"count_64": "-6400000000",
		"unsigned_32": 32,
		"unsigned_64": "6400000000",
		"ratio_32": 1.25,
		"ratio_64": -9.5,
		"name": "governed",
		"payload": "AAEC",
		"tags": ["one", "two"],
		"scores": {"first": "10", "second": "-20"},
		"detail": {"code": "root", "amount": "7"},
		"items": [{"code": "item", "amount": "8"}],
		"lookup": {"nested": {"code": "map", "amount": "9"}},
		"at": "2026-07-25T01:02:03.456Z",
		"delay": "1.500s"
	}
}` + "`" + `

func TestConnectAndInternalCallsUseTheSameGovernedInterface(t *testing.T) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	projectRoot := filepath.Clean(filepath.Join("..", "..", "..", "..", "..", "..", ".."))
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("os.Chdir(Project root): %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	callsBefore := records.Calls()
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Application.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Errorf("Application.Stop: %v", err)
		}
	})

	input := echov1.Request{Value: echov1.Envelope{
		Active: true, Count32: -32, Count64: -6400000000,
		Unsigned32: 32, Unsigned64: 6400000000,
		Ratio32: 1.25, Ratio64: -9.5, Name: "governed",
		Payload: []byte{0, 1, 2}, Tags: []string{"one", "two"},
		Scores: map[string]int64{"first": 10, "second": -20},
		Detail: echov1.Detail{Code: "root", Amount: 7},
		Items: []echov1.Detail{{Code: "item", Amount: 8}},
		Lookup: map[string]echov1.Detail{"nested": {Code: "map", Amount: 9}},
		At: time.Date(2026, 7, 25, 1, 2, 3, 456000000, time.UTC),
		Delay: 1500 * time.Millisecond,
	}}
	internal, err := application.Interfaces().RecordsEchoV1().Echo(context.Background(), input)
	if err != nil || !reflect.DeepEqual(internal, echov1.Response{Value: input.Value}) {
		t.Fatalf("internal governed call = %#v, %v", internal, err)
	}

	handler, err := connectadapter.New(
		func(context.Context, http.Header) (context.Context, error) { return context.Background(), nil },
		application.Interfaces().RecordsEchoV1(),
	)
	if err != nil || !connectadapter.Available(handler) {
		t.Fatalf("connectadapter.New = %#v, %v", handler, err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	status, encoded := callConnect(t, server.Client(), context.Background(), server.URL+connectadapter.Procedure, "root")
	if status != http.StatusOK || !equalJSON(encoded, []byte(requestJSON)) {
		t.Fatalf("Connect response = status %d, body %s; want %s", status, encoded, requestJSON)
	}
	if calls := records.Calls(); calls != callsBefore+2 {
		t.Fatalf("selected Implementation calls = %d, want %d", calls, callsBefore+2)
	}

	errorCases := []struct {
		name     string
		behavior string
		code     connect.Code
		semantic string
		kernel   string
	}{
		{name: "semantic", behavior: "semantic", code: connect.CodeFailedPrecondition, semantic: "record_rejected"},
		{name: "undeclared semantic", behavior: "undeclared-semantic", code: connect.CodeInternal, kernel: "internal"},
		{name: "unknown", behavior: "unknown", code: connect.CodeInternal, kernel: "internal"},
		{name: "panic", behavior: "panic", code: connect.CodeInternal, kernel: "internal"},
		{name: "invalid response", behavior: "invalid-response", code: connect.CodeInternal, kernel: "internal"},
		{name: "invalid argument", behavior: "kernel-invalid-argument", code: connect.CodeInvalidArgument, kernel: "invalid_argument"},
		{name: "not found", behavior: "kernel-not-found", code: connect.CodeNotFound, kernel: "not_found"},
		{name: "conflict", behavior: "kernel-conflict", code: connect.CodeAborted, kernel: "conflict"},
		{name: "denied", behavior: "kernel-denied", code: connect.CodePermissionDenied, kernel: "denied"},
		{name: "unauthenticated", behavior: "kernel-unauthenticated", code: connect.CodeUnauthenticated, kernel: "unauthenticated"},
		{name: "unavailable", behavior: "kernel-unavailable", code: connect.CodeUnavailable, kernel: "unavailable"},
		{name: "timeout", behavior: "kernel-timeout", code: connect.CodeDeadlineExceeded, kernel: "timeout"},
		{name: "cancelled", behavior: "kernel-cancelled", code: connect.CodeCanceled, kernel: "cancelled"},
		{name: "result unknown", behavior: "kernel-result-unknown", code: connect.CodeUnavailable, kernel: "result_unknown"},
		{name: "internal", behavior: "kernel-internal", code: connect.CodeInternal, kernel: "internal"},
		{name: "version incompatible", behavior: "kernel-version-incompatible", code: connect.CodeUnimplemented, kernel: "version_incompatible"},
	}
	for _, test := range errorCases {
		t.Run("safe error/"+test.name, func(t *testing.T) {
			response, err := handler.Invoke(t.Context(), directRequest(t, test.behavior))
			if response != nil {
				t.Fatalf("error response = %#v", response)
			}
			assertSafeConnectError(t, err, test.code, test.semantic, test.kernel)
			status, body := callConnect(t, server.Client(), t.Context(), server.URL+connectadapter.Procedure, test.behavior)
			if status == http.StatusOK {
				t.Fatalf("HTTP error status = %d, body %s", status, body)
			}
			assertNoPrivateText(t, body)
		})
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	response, err := handler.Invoke(canceled, directRequest(t, "root"))
	if response != nil {
		t.Fatalf("canceled response = %#v", response)
	}
	assertSafeConnectError(t, err, connect.CodeCanceled, "", "cancelled")
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	response, err = handler.Invoke(expired, directRequest(t, "root"))
	if response != nil {
		t.Fatalf("expired response = %#v", response)
	}
	assertSafeConnectError(t, err, connect.CodeDeadlineExceeded, "", "timeout")

	errorDescriptor, err := connectschema.Message("plystra.generated.transport.v1.PlystraErrorDetail")
	if err != nil {
		t.Fatalf("safe error descriptor: %v", err)
	}
	response, err = handler.Invoke(t.Context(), connect.NewRequest(dynamicpb.NewMessage(errorDescriptor)))
	if response != nil {
		t.Fatalf("invalid request response = %#v", response)
	}
	assertSafeConnectError(t, err, connect.CodeInvalidArgument, "", "invalid_argument")

	response, err = handler.InvokeRequested(t.Context(), "../private/interface", directRequest(t, "root"))
	if response != nil {
		t.Fatalf("invalid requested Interface response = %#v", response)
	}
	assertSafeConnectError(t, err, connect.CodeInternal, "", "internal")

	rootCases := []struct {
		name   string
		root   connectadapter.RootContext
		code   connect.Code
		kernel string
	}{
		{name: "unknown", root: func(context.Context, http.Header) (context.Context, error) {
			return nil, errors.New("private root failure")
		}, code: connect.CodeInternal, kernel: "internal"},
		{name: "nil context", root: func(context.Context, http.Header) (context.Context, error) {
			return nil, nil
		}, code: connect.CodeInternal, kernel: "internal"},
		{name: "panic", root: func(context.Context, http.Header) (context.Context, error) {
			panic("private root panic")
		}, code: connect.CodeInternal, kernel: "internal"},
		{name: "cancelled", root: func(context.Context, http.Header) (context.Context, error) {
			return nil, context.Canceled
		}, code: connect.CodeCanceled, kernel: "cancelled"},
		{name: "deadline", root: func(context.Context, http.Header) (context.Context, error) {
			return nil, context.DeadlineExceeded
		}, code: connect.CodeDeadlineExceeded, kernel: "timeout"},
	}
	for _, test := range rootCases {
		t.Run("root/"+test.name, func(t *testing.T) {
			rootHandler, err := connectadapter.New(test.root, application.Interfaces().RecordsEchoV1())
			if err != nil {
				t.Fatalf("connectadapter.New(root): %v", err)
			}
			response, err := rootHandler.Invoke(t.Context(), directRequest(t, "root"))
			if response != nil {
				t.Fatalf("root error response = %#v", response)
			}
			assertSafeConnectError(t, err, test.code, "", test.kernel)
		})
	}
}

func callConnect(t *testing.T, client *http.Client, ctx context.Context, target, behavior string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(requestJSONFor(behavior)))
	if err != nil {
		t.Fatalf("http.NewRequestWithContext: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Connect request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Connect response: %v", err)
	}
	return response.StatusCode, body
}

func requestJSONFor(behavior string) string {
	return strings.Replace(requestJSON, "\"code\": \"root\"", "\"code\": \""+behavior+"\"", 1)
}

func directRequest(t *testing.T, behavior string) *connect.Request[dynamicpb.Message] {
	t.Helper()
	method, err := connectschema.Method("plystra.generated.records.echo.v1.RecordsEchoV1Service.Invoke")
	if err != nil {
		t.Fatalf("Connect method: %v", err)
	}
	message := dynamicpb.NewMessage(method.Input())
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal([]byte(requestJSONFor(behavior)), message); err != nil {
		t.Fatalf("decode direct request: %v", err)
	}
	return connect.NewRequest(message)
}

func assertSafeConnectError(t *testing.T, err error, code connect.Code, semanticErrorCode, kernelErrorClass string) {
	t.Helper()
	var connectError *connect.Error
	if !errors.As(err, &connectError) || connectError == nil || connectError.Code() != code {
		t.Fatalf("Connect error = %#v, want code %s", err, code)
	}
	details := connectError.Details()
	if len(details) != 1 || details[0] == nil || details[0].Type() != "plystra.generated.transport.v1.PlystraErrorDetail" {
		t.Fatalf("Connect error details = %#v", details)
	}
	descriptor, descriptorErr := connectschema.Message("plystra.generated.transport.v1.PlystraErrorDetail")
	if descriptorErr != nil {
		t.Fatalf("safe error descriptor: %v", descriptorErr)
	}
	wantFields := []struct {
		number protoreflect.FieldNumber
		name   protoreflect.Name
	}{
		{number: 1, name: "requested_interface_id"},
		{number: 2, name: "canonical_interface_id"},
		{number: 3, name: "semantic_error_code"},
		{number: 4, name: "kernel_error_class"},
		{number: 5, name: "trace_id"},
	}
	for _, expected := range wantFields {
		if field := descriptor.Fields().ByNumber(expected.number); field == nil || field.Name() != expected.name {
			t.Fatalf("safe error field %d = %#v, want %s", expected.number, field, expected.name)
		}
	}
	message := dynamicpb.NewMessage(descriptor)
	if decodeErr := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(details[0].Bytes(), message); decodeErr != nil {
		t.Fatalf("decode safe error detail: %v", decodeErr)
	}
	reflected := message.ProtoReflect()
	if len(reflected.GetUnknown()) != 0 {
		t.Fatalf("safe error detail contains unknown fields: %x", reflected.GetUnknown())
	}
	field := func(number protoreflect.FieldNumber) string {
		return reflected.Get(descriptor.Fields().ByNumber(number)).String()
	}
	if field(1) != connectadapter.InterfaceID ||
		field(2) != connectadapter.InterfaceID ||
		field(3) != semanticErrorCode ||
		field(4) != kernelErrorClass ||
		field(5) != "" ||
		(semanticErrorCode == "") == (kernelErrorClass == "") {
		t.Fatalf("safe error detail = requested %q canonical %q semantic %q kernel %q trace %q", field(1), field(2), field(3), field(4), field(5))
	}
	assertNoPrivateText(t, []byte(err.Error()))
	assertNoPrivateText(t, details[0].Bytes())
}

func assertNoPrivateText(t *testing.T, data []byte) {
	t.Helper()
	for _, private := range [][]byte{
		[]byte("private semantic failure"),
		[]byte("private_undeclared"),
		[]byte("private unknown implementation failure"),
		[]byte("private implementation panic"),
		[]byte("private.kernel_detail"),
		[]byte("private root failure"),
		[]byte("private root panic"),
		[]byte("../private/interface"),
	} {
		if bytes.Contains(data, private) {
			t.Fatalf("private text %q crossed the Connect boundary in %q", private, data)
		}
	}
}

func equalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}
`

func TestGeneratedConnectInterfaceRuntimeTestIsSelfContained(t *testing.T) {
	for _, fragment := range []string{
		"application.Interfaces().RecordsEchoV1()",
		"connectadapter.New(",
		"callConnect(",
		"assertSafeConnectError(",
		`kernel: "result_unknown"`,
		`name: "undeclared semantic"`,
		`name: "invalid response"`,
		`name: "panic"`,
		`name: "deadline"`,
		`InvokeRequested(t.Context(), "../private/interface"`,
	} {
		if !strings.Contains(generatedConnectInterfaceRuntimeTest, fragment) {
			t.Fatalf("generated runtime acceptance omits %q", fragment)
		}
	}
}
