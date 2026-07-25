package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGeneratedConnectInterfaceHandlerUsesGovernedRuntime(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-connect"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [records.echo/v1]
http:
  expose: [records.echo/v1]
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

//plystra:implements records.echo/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Echo(ctx context.Context, request echov1.Request) (echov1.Response, error) {
	if _, governed := kernelinvocation.Current(ctx); !governed {
		return echov1.Response{}, errors.New("Interface call bypassed Kernel governance")
	}
	calls.Add(1)
	return echov1.Response{Value: request.Value}, nil
}
`)

	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = changes %#v, %v", generated.Report().Changes(), err)
	}

	handlerPath := "generated/go/adapters/connect/records/echo/v1/handler_gen.go"
	handlerSource := readFile(t, root, handlerPath)
	for _, required := range []string{
		"target contract.Interface",
		"return target.Echo(ctx, request)",
		"connect.NewUnaryHandler(",
	} {
		if !bytes.Contains(handlerSource, []byte(required)) {
			t.Fatalf("%s omits %q:\n%s", handlerPath, required, handlerSource)
		}
	}
	for _, forbidden := range []string{
		"applicationinvocation",
		modulePath + "/generated/go/invocations/",
		modulePath + "/records\"",
		"kernelinvocation",
		"NewHandle(",
	} {
		if bytes.Contains(handlerSource, []byte(forbidden)) {
			t.Fatalf("%s contains forbidden direct runtime dependency %q:\n%s", handlerPath, forbidden, handlerSource)
		}
	}
	assemblySource := readFile(t, root, "generated/go/assembly/interfaces_gen.go")
	if !bytes.Contains(assemblySource, []byte("func (runtime InterfaceRuntime) RecordsEchoV1()")) {
		t.Fatalf("generated assembly omits the governed records.echo/v1 accessor:\n%s", assemblySource)
	}

	writeFile(t, filepath.Join(root, "interface_connect_test.go"), generatedConnectInterfaceRuntimeTest)
	command := exec.CommandContext(t.Context(), "go", "test", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go test generated Connect Interface Project: %v\n%s", err, output)
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

const generatedConnectInterfaceRuntimeTest = `package interfaceconnect_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	connectadapter "example.com/interface-connect/generated/go/adapters/connect/records/echo/v1"
	bootstrap "example.com/interface-connect/generated/go/bootstrap"
	echov1 "example.com/interface-connect/interfaces/records/echo/v1"
	"example.com/interface-connect/records"
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

	status, encoded := callConnect(t, server.Client(), context.Background(), server.URL+connectadapter.Procedure)
	if status != http.StatusOK || !equalJSON(encoded, []byte(requestJSON)) {
		t.Fatalf("Connect response = status %d, body %s; want %s", status, encoded, requestJSON)
	}
	if calls := records.Calls(); calls != 2 {
		t.Fatalf("selected Implementation calls = %d, want 2", calls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if code := directConnectErrorCode(t, handler, canceled); code != "canceled" {
		t.Fatalf("canceled Connect invocation code = %q", code)
	}
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	if code := directConnectErrorCode(t, handler, expired); code != "deadline_exceeded" {
		t.Fatalf("expired Connect invocation code = %q", code)
	}
}

func callConnect(t *testing.T, client *http.Client, ctx context.Context, target string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(requestJSON))
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

func directConnectErrorCode(t *testing.T, handler http.Handler, ctx context.Context) string {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, connectadapter.Procedure, strings.NewReader(requestJSON))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var response struct {
		Code string ` + "`" + `json:"code"` + "`" + `
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode Connect error response %d %q: %v", recorder.Code, recorder.Body.Bytes(), err)
	}
	return response.Code
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
		`code != "canceled"`,
		`code != "deadline_exceeded"`,
	} {
		if !strings.Contains(generatedConnectInterfaceRuntimeTest, fragment) {
			t.Fatalf("generated runtime acceptance omits %q", fragment)
		}
	}
}
