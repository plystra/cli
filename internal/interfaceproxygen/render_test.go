package interfaceproxygen_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/interfaceproxygen"
	"github.com/plystra/cli/internal/testinterface"
	"github.com/plystra/cli/internal/testkernel"
)

func TestRenderProducesDeterministicTypedProxyPackages(t *testing.T) {
	t.Parallel()

	inputs := []interfaceproxygen.Input{
		proxyInput(t, "zeta.write/v2", "example.com/contracts/zeta/write/v2", "Write"),
		proxyInput(t, "order.create/v1", "example.com/contracts/order/create/v1", "Create"),
	}
	files, err := interfaceproxygen.Render(inputs)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := proxyPaths(files); !reflect.DeepEqual(got, []string{
		"generated/go/proxies/order/create/v1/proxy_gen.go",
		"generated/go/proxies/zeta/write/v2/proxy_gen.go",
	}) {
		t.Fatalf("paths = %v", got)
	}
	orderSource := files[0].Data()
	for _, required := range [][]byte{
		[]byte(`const InterfaceID = "order.create/v1"`),
		[]byte(`contract "example.com/contracts/order/create/v1"`),
		[]byte(`kernelinvocation "github.com/plystra/kernel/invocation"`),
		[]byte(`var _ contract.Interface = Proxy{}`),
		[]byte(`kernelinvocation.Handle[contract.Request, contract.Response]`),
		[]byte(`func (proxy Proxy) Create(ctx context.Context, request contract.Request) (contract.Response, error)`),
		[]byte(`snapshot, err := CopyRequest(value)`),
		[]byte(`response, err := proxy.handle.InvokeWithPreparation(ctx, request,`),
		[]byte(`copied, err := CopyResponse(value)`),
		[]byte(`boundary.Completion() == failure.boundary.Completion()`),
		[]byte(`copied.boundary = boundary`),
	} {
		if !bytes.Contains(orderSource, required) {
			t.Fatalf("order proxy omits %q:\n%s", required, orderSource)
		}
	}
	copyData := files[0].Data()
	copyData[0] = 'X'
	if bytes.Equal(copyData, files[0].Data()) {
		t.Fatal("File.Data exposed mutable source storage")
	}

	repeated, err := interfaceproxygen.Render([]interfaceproxygen.Input{inputs[1], inputs[0]})
	if err != nil || !reflect.DeepEqual(proxyPaths(repeated), proxyPaths(files)) {
		t.Fatalf("reordered Render paths = %v, %v", proxyPaths(repeated), err)
	}
	for index := range files {
		if files[index].InterfaceID() != repeated[index].InterfaceID() || !bytes.Equal(files[index].Data(), repeated[index].Data()) {
			t.Fatalf("reordered Render changed file %d", index)
		}
	}
}

func TestRenderRejectsInvalidAndDuplicateInterfaceInputs(t *testing.T) {
	t.Parallel()

	valid := proxyInput(t, "order.create/v1", "example.com/contracts/order/create/v1", "Create")
	missing := valid
	missing.Contract = interfacecontract.Contract{}
	mismatched := valid
	mismatched.Contract = testinterface.Simple(t, "order.create/v1", "example.com/another/contract", "Create")
	tests := []struct {
		name  string
		input interfaceproxygen.Input
	}{
		{name: "missing canonical contract", input: missing},
		{name: "mismatched canonical contract", input: mismatched},
		{name: "missing Interface ID", input: interfaceproxygen.Input{PackagePath: valid.PackagePath, MethodName: "Create", RequestName: "Request", ResponseName: "Response"}},
		{name: "invalid package", input: interfaceproxygen.Input{InterfaceID: valid.InterfaceID, PackagePath: "../contract", MethodName: "Create", RequestName: "Request", ResponseName: "Response"}},
		{name: "Kernel invocation package", input: interfaceproxygen.Input{InterfaceID: valid.InterfaceID, PackagePath: "github.com/plystra/kernel/invocation", MethodName: "Create", RequestName: "Request", ResponseName: "Response"}},
		{name: "unexported method", input: interfaceproxygen.Input{InterfaceID: valid.InterfaceID, PackagePath: valid.PackagePath, MethodName: "create", RequestName: "Request", ResponseName: "Response"}},
		{name: "invalid request", input: interfaceproxygen.Input{InterfaceID: valid.InterfaceID, PackagePath: valid.PackagePath, MethodName: "Create", RequestName: "request", ResponseName: "Response"}},
		{name: "invalid response", input: interfaceproxygen.Input{InterfaceID: valid.InterfaceID, PackagePath: valid.PackagePath, MethodName: "Create", RequestName: "Request", ResponseName: "for"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files, err := interfaceproxygen.Render([]interfaceproxygen.Input{test.input})
			if len(files) != 0 || !errors.Is(err, interfaceproxygen.ErrRender) || !errors.Is(err, interfaceproxygen.ErrInvalidInput) {
				t.Fatalf("Render = %#v, %v", files, err)
			}
		})
	}
	if files, err := interfaceproxygen.Render([]interfaceproxygen.Input{valid, valid}); len(files) != 0 || !errors.Is(err, interfaceproxygen.ErrRender) || !errors.Is(err, interfaceproxygen.ErrDuplicateInterface) {
		t.Fatalf("Render duplicate = %#v, %v", files, err)
	}
	allInvalid := valid
	allInvalid.MethodName = "method"
	allInvalid.RequestName = "request"
	allInvalid.ResponseName = "response"
	for attempt := 0; attempt < 32; attempt++ {
		_, err := interfaceproxygen.Render([]interfaceproxygen.Input{allInvalid})
		if err == nil || !bytes.Contains([]byte(err.Error()), []byte(`method name "method"`)) {
			t.Fatalf("Render invalid field order on attempt %d = %v", attempt, err)
		}
	}
}

func TestGeneratedProxyImplementsAndInvokesAuthoredInterface(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared_response_%t", shared), func(t *testing.T) { testGeneratedProxy(t, shared) })
	}
}

func testGeneratedProxy(t *testing.T, shared bool) {
	t.Helper()
	input := proxyInput(t, "order.create/v1", "example.com/proxyfixture/interfaces/order/create/v1", "Create")
	source := "package createv1\nimport \"context\"\n//plystra:interface order.create/v1\ntype Interface interface { Create(context.Context, Request) (Response, error) }\ntype Request struct { Value string `plystra:\"1\"` }\ntype Response struct { Value string `plystra:\"1\"` }\n"
	tests := generatedProxyRuntimeTest
	if shared {
		source = strings.Replace(source, "(Response, error)", "(Request, error)", 1)
		input.ResponseName = "Request"
		tests = strings.ReplaceAll(tests, "contract.Response", "contract.Request")
	}
	input.Contract = testinterface.Parse(t, input.PackagePath, source)
	metadata, err := interfacemeta.ParseFile("interface.yaml", []byte("constraints:\n  request.Value: {min_length: 7}\n  response.Value: {min_length: 8}\n"))
	if err != nil {
		t.Fatal(err)
	}
	input.Metadata = metadata
	files, err := interfaceproxygen.Render([]interfaceproxygen.Input{input})
	if err != nil || len(files) != 1 {
		t.Fatalf("Render = %#v, %v", files, err)
	}

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := testkernel.Root(t)
	writeProxyFile(t, root, "go.mod", fmt.Sprintf(`module example.com/proxyfixture

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeProxyBytes(t, root, "go.sum", goSum)
	writeProxyFile(t, root, "interfaces/order/create/v1/interface.go", source)
	writeProxyBytes(t, root, files[0].Path(), files[0].Data())
	writeProxyFile(t, root, "generated/go/proxies/order/create/v1/proxy_gen_test.go", tests)

	command := exec.CommandContext(t.Context(), "go", "mod", "tidy")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare generated proxy module: %v\n%s", err, output)
	}
	command = exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("test generated proxy module: %v\n%s", err, output)
	}

	// Hold the exact generated processor at its read and return boundaries; no
	// production test hook or replacement Kernel is needed to control scheduling.
	instrumented := strings.Replace(string(files[0].Data()), "copied, err := CopyResponse(value)", "TestResponseBarrier(\"copy\")\n\t\tcopied, err := CopyResponse(value)", 1)
	instrumented = strings.Replace(instrumented, "return copied, err", "TestResponseBarrier(\"return\")\n\t\treturn copied, err", 1)
	instrumented = strings.Replace(instrumented, "return copied, err", "return copied, TestResponseOutcome(err)", 1)
	// Kernel normalization may copy a boundary to attach per-call evidence.
	instrumented = strings.Replace(instrumented, "\tif failure := validation.Load();", "\tif boundary, ok := err.(*kernelinvocation.Error); ok { copied := *boundary; err = &copied; TestReturnedBoundary.Store(&copied) }\n\tif failure := validation.Load();", 1)
	instrumented += "\nvar TestResponseBarrier = func(string) {}\n"
	instrumented += "\nvar TestReturnedBoundary atomic.Pointer[kernelinvocation.Error]\n"
	instrumented += "\nvar TestResponseOutcome = func(err error) error { return err }\n"
	instrumented = strings.Replace(instrumented, "snapshot, err := CopyRequest(value)", "TestRequestBarrier()\n\t\tsnapshot, err := CopyRequest(value)", 1)
	instrumented += "\nvar TestRequestBarrier = func() {}\n"
	writeProxyFile(t, root, files[0].Path(), instrumented)
	lifetimeTests := generatedResponseLifetimeTests + generatedPreparationTests
	if shared {
		lifetimeTests = strings.ReplaceAll(lifetimeTests, "contract.Response", "contract.Request")
	}
	writeProxyFile(t, root, "generated/go/proxies/order/create/v1/response_lifetime_test.go", lifetimeTests)
	command = exec.CommandContext(t.Context(), "go", "test", "-race", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated response lifetime: %v\n%s", err, output)
	}
}

func proxyInput(t testing.TB, identifier, packagePath, method string) interfaceproxygen.Input {
	t.Helper()
	parsed, err := interfaceid.Parse(identifier)
	if err != nil {
		t.Fatalf("interfaceid.Parse(%q): %v", identifier, err)
	}
	return interfaceproxygen.Input{
		InterfaceID:  parsed,
		PackagePath:  packagePath,
		MethodName:   method,
		RequestName:  "Request",
		ResponseName: "Response",
		Contract:     testinterface.Simple(t, identifier, packagePath, method),
	}
}

func proxyPaths(files []interfaceproxygen.File) []string {
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path()
	}
	return paths
}

func writeProxyFile(t testing.TB, root, relative, data string) {
	t.Helper()
	writeProxyBytes(t, root, relative, []byte(data))
}

func writeProxyBytes(t testing.TB, root, relative string, data []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

const generatedProxyRuntimeTest = `package proxy_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	contract "example.com/proxyfixture/interfaces/order/create/v1"
	proxy "example.com/proxyfixture/generated/go/proxies/order/create/v1"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestProxyUsesGovernedHandle(t *testing.T) {
	contractToken := capability.MustParseContract[contract.Request, contract.Response]("order.create/v1")
	endpoint, err := invocation.NewEndpoint(contractToken, func(_ context.Context, request contract.Request) (contract.Response, error) {
		if request.Value == "failure" {
			boundary, _ := invocation.NewError(invocation.ErrorInternal, "contract.response_invalid")
			return contract.Response{}, boundary
		}
		return contract.Response{Value: "handled:" + request.Value}, nil
	})
	if err != nil { t.Fatal(err) }
	build, err := invocation.NewModuleBuild("example.com/proxyfixture", "v1.0.0", "")
	if err != nil { t.Fatal(err) }
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		Policy: invocation.Policy{SchemaVersion: 1, CompilerVersion: 1, DefaultsVersion: 1, Timeout: time.Second, ConcurrencyLimit: 64, Retry: invocation.RetryPolicy{MaxAttempts: 1}},
		Kind: invocation.BindingKindImplementation,
		Constructor: "example.com/proxyfixture/implementation.New",
		ModuleBuild: build,
		SelectionReason: invocation.SelectionReasonUniqueCompatible,
		ContractDigest: sha256.Sum256([]byte("order.create/v1")),
	}, endpoint)
	if err != nil { t.Fatal(err) }
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil { t.Fatal(err) }
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil { t.Fatal(err) }
	if err := dispatcher.Publish(catalog); err != nil { t.Fatal(err) }
	if err := dispatcher.OpenAdmission(); err != nil { t.Fatal(err) }
	handle, err := invocation.NewHandle(dispatcher, contractToken, true)
	if err != nil { t.Fatal(err) }
	var implementation contract.Interface = proxy.New(handle)
	response, err := implementation.Create(context.Background(), contract.Request{Value: "request"})
	if err != nil || response.Value != "handled:request" {
		t.Fatalf("Create = %#v, %v", response, err)
	}
	response, err = implementation.Create(context.Background(), contract.Request{Value: "failure"})
	var validation *proxy.ValueError
	var boundary *invocation.Error
	if response != (contract.Response{}) || errors.As(err, &validation) || !errors.As(err, &boundary) || boundary.DetailCode() != "contract.response_invalid" {
		t.Fatalf("target error gained fabricated validation details: %#v, %v", response, err)
	}
}

func TestRequestAndResponseConstraintsAreIndependent(t *testing.T) {
	if _, err := proxy.CopyRequest(contract.Request{Value: "request"}); err != nil { t.Fatal(err) }
	if _, err := proxy.CopyResponse(contract.Response{Value: "request"}); err == nil { t.Fatal("response constraint was not applied") }
	var zero *proxy.ValueError
	if zero.Error() == "" || zero.Side() != "" || zero.Path() != "" || zero.Rule() != "" || zero.Unwrap() != nil { t.Fatal("nil validation error is unsafe") }
}
`

const generatedResponseLifetimeTests = `package proxy_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	contract "example.com/proxyfixture/interfaces/order/create/v1"
	proxy "example.com/proxyfixture/generated/go/proxies/order/create/v1"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestGeneratedResponseProcessingRetainsAttempt(t *testing.T) {
	for _, stage := range []string{"copy", "return"} {
		for _, mode := range []string{"normal", "cancel", "deadline", "shutdown", "panic", "unknown"} {
			for _, value := range []string{"valid-response", "invalid"} {
				t.Run(stage+"/"+mode+"/"+value, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
					entered, release := make(chan struct{}), make(chan struct{})
					var released sync.Once
					unblock := func() { released.Do(func() { close(release) }) }
					proxy.TestResponseBarrier = func(at string) { if at == stage { close(entered); <-release; if mode == "panic" { panic("private processor panic") } } }
					proxy.TestResponseOutcome = func(err error) error { if mode == "unknown" && err != nil { return invocation.NewResultUnknown(err) }; return err }
					token := capability.MustParseContract[contract.Request, contract.Response]("order.create/v1")
					endpoint, err := invocation.NewEndpoint(token, func(context.Context, contract.Request) (contract.Response, error) {
						return contract.Response{Value: value}, nil
					})
					if err != nil { t.Fatal(err) }
					build, err := invocation.NewModuleBuild("example.com/proxyfixture", "v1.0.0", "")
					if err != nil { t.Fatal(err) }
					binding, err := invocation.NewBinding(invocation.BindingOptions{
						Policy: invocation.Policy{SchemaVersion: 1, CompilerVersion: 1, DefaultsVersion: 1, Timeout: 5 * time.Second, ConcurrencyLimit: 1, Retry: invocation.RetryPolicy{MaxAttempts: 1}},
						Kind: invocation.BindingKindImplementation, Constructor: "example.com/proxyfixture/implementation.New",
						ModuleBuild: build, SelectionReason: invocation.SelectionReasonUniqueCompatible,
						ContractDigest: sha256.Sum256([]byte("order.create/v1")),
					}, endpoint)
					if err != nil { t.Fatal(err) }
					catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
					if err != nil { t.Fatal(err) }
					dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
					if err != nil { t.Fatal(err) }
					defer func() {
						unblock()
						ctx, cancel := context.WithTimeout(context.Background(), time.Second); defer cancel()
						if err := dispatcher.Drain(ctx); err != nil { t.Error(err) }
						proxy.TestResponseBarrier = func(string) {}
						proxy.TestResponseOutcome = func(err error) error { return err }
					}()
					if err := dispatcher.Publish(catalog); err != nil { t.Fatal(err) }
					if err := dispatcher.OpenAdmission(); err != nil { t.Fatal(err) }
					handle, err := invocation.NewHandle(dispatcher, token, true)
					if err != nil { t.Fatal(err) }
					ctx, cancel := context.WithCancel(context.Background()); defer cancel()
					if mode == "deadline" {
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, 200*time.Millisecond); defer stop()
					}
					type result struct { response contract.Response; err error }
					done := make(chan result, 1)
					go func() { response, err := proxy.New(handle).Create(ctx, contract.Request{Value: "request"}); done <- result{response, err} }()
					select { case <-entered: case <-time.After(time.Second): t.Fatal("processor was not entered") }
					if dispatcher.ActiveAttempts() != 1 { t.Fatal("response processing lost attempt ownership") }
					assertCapacity := func() {
						response, err := handle.Invoke(context.Background(), contract.Request{Value: "request"})
						var failure *invocation.Error
						if response != (contract.Response{}) || !errors.As(err, &failure) || failure.Code() != invocation.ErrorResourceExhausted || invocation.CompletionOf(err) != invocation.CompletionNotStarted { t.Fatalf("processor released admission: %#v, %v", response, err) }
					}
					assertCapacity()
					if mode == "normal" || mode == "panic" || mode == "unknown" {
						unblock()
					} else {
						if mode == "cancel" { cancel() }
						if mode == "deadline" { <-ctx.Done() }
						synctest.Wait()
						assertCapacity()
						bounded, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
						err := dispatcher.Drain(bounded); stop()
						if err == nil || dispatcher.ActiveAttempts() != 1 || !dispatcher.AdmissionClosed() { t.Fatal("drain ignored active response processing") }
					}
					var got result
					select { case got = <-done: case <-time.After(time.Second): t.Fatal("caller did not complete") }
					var validation *proxy.ValueError
					if mode == "panic" || (mode == "unknown" && value == "invalid") {
						var boundary *invocation.Error
						wantCompletion := invocation.CompletionResultKnown
						wantDetail := "runtime.response_processing_failed"
						if mode == "unknown" { wantCompletion = invocation.CompletionResultUnknown; wantDetail = "contract.response_invalid" }
						if errors.As(got.err, &validation) || !errors.As(got.err, &boundary) || boundary.Code() != invocation.ErrorInternal || boundary.DetailCode() != wantDetail || invocation.CompletionOf(got.err) != wantCompletion { t.Fatalf("processor failure replaced by validation: %v", got.err) }
					} else if mode == "normal" || mode == "unknown" {
						if value == "invalid" {
							if !errors.As(got.err, &validation) || validation.Side() != "response" || invocation.CompletionOf(got.err) != invocation.CompletionResultKnown { t.Fatalf("validation not restored: %v", got.err) }
							if validation.Unwrap() != proxy.TestReturnedBoundary.Load() { t.Fatal("restoration lost the returned Kernel evidence") }
						} else if got.err != nil || got.response.Value != value { t.Fatalf("successful response = %#v", got) }
					} else {
						want := context.Canceled
						if mode == "deadline" { want = context.DeadlineExceeded }
						if !errors.Is(got.err, want) || errors.As(got.err, &validation) || invocation.CompletionOf(got.err) != invocation.CompletionResultUnknown { t.Fatalf("cancellation replaced by validation: %v", got.err) }
						if dispatcher.ActiveAttempts() != 1 { t.Fatal("caller completion released the processor") }
					}
					if got.err != nil && got.response != (contract.Response{}) { t.Fatal("failed caller received a response") }
					})
				})
			}
		}
	}
}
`
