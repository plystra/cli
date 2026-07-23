package interfaceproxygen_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceproxygen"
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
		[]byte(`return proxy.handle.Invoke(ctx, request)`),
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
	tests := []struct {
		name  string
		input interfaceproxygen.Input
	}{
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
	input := proxyInput(t, "order.create/v1", "example.com/proxyfixture/interfaces/order/create/v1", "Create")
	files, err := interfaceproxygen.Render([]interfaceproxygen.Input{input})
	if err != nil || len(files) != 1 {
		t.Fatalf("Render = %#v, %v", files, err)
	}

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
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
	writeProxyFile(t, root, "interfaces/order/create/v1/interface.go", `package createv1

import "context"

type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct { Value string }
type Response struct { Value string }
`)
	writeProxyBytes(t, root, files[0].Path(), files[0].Data())
	writeProxyFile(t, root, "generated/go/proxies/order/create/v1/proxy_gen_test.go", generatedProxyRuntimeTest)

	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("test generated proxy module: %v\n%s", err, output)
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
		return contract.Response{Value: "handled:" + request.Value}, nil
	})
	if err != nil { t.Fatal(err) }
	build, err := invocation.NewModuleBuild("example.com/proxyfixture", "v1.0.0", "")
	if err != nil { t.Fatal(err) }
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		ProviderKind: invocation.ProviderKindKernel,
		ProviderPackage: "example.com/proxyfixture/implementation",
		ProviderBuild: build,
		SelectionReason: invocation.SelectionReasonIntrinsic,
		SchemaDigest: sha256.Sum256([]byte("order.create/v1")),
	}, endpoint)
	if err != nil { t.Fatal(err) }
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil { t.Fatal(err) }
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil { t.Fatal(err) }
	if err := dispatcher.Publish(catalog); err != nil { t.Fatal(err) }
	handle, err := invocation.NewHandle(dispatcher, contractToken, true)
	if err != nil { t.Fatal(err) }
	var implementation contract.Interface = proxy.New(handle)
	response, err := implementation.Create(context.Background(), contract.Request{Value: "request"})
	if err != nil || response.Value != "handled:request" {
		t.Fatalf("Create = %#v, %v", response, err)
	}
}
`
