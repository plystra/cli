package implementationadaptergen_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationadaptergen"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestRenderProducesDeterministicTypedImplementationAdapters(t *testing.T) {
	t.Parallel()

	inputs := []implementationadaptergen.Input{
		adapterInput(t, "zeta.write/v2", "example.com/contracts/zeta/write/v2", "Write", "example.com/impl/zeta.New", "*example.com/impl/zeta.service", nil),
		adapterInput(t, "order.create/v1", "example.com/contracts/order/create/v1", "Create", "example.com/impl/orders.New", "*example.com/impl/orders.service", []string{"order_invalid", "order_already_exists"}),
	}
	files, err := implementationadaptergen.Render(inputs)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := adapterPaths(files); !reflect.DeepEqual(got, []string{
		"generated/go/adapters/implementations/order/create/v1/adapter_gen.go",
		"generated/go/adapters/implementations/zeta/write/v2/adapter_gen.go",
	}) {
		t.Fatalf("paths = %v", got)
	}
	orderSource := files[0].Data()
	for _, required := range [][]byte{
		[]byte(`const InterfaceID = "order.create/v1"`),
		[]byte(`const ConstructorSymbol = "example.com/impl/orders.New"`),
		[]byte(`const ConcreteType = "*example.com/impl/orders.service"`),
		[]byte(`contract "example.com/contracts/order/create/v1"`),
		[]byte(`kernelcapability "github.com/plystra/kernel/capability"`),
		[]byte(`kernelinvocation "github.com/plystra/kernel/invocation"`),
		[]byte(`"order_already_exists"`),
		[]byte(`"order_invalid"`),
		[]byte(`func Contract() kernelcapability.Contract[contract.Request, contract.Response]`),
		[]byte(`func NewEndpoint(implementation contract.Interface) (kernelinvocation.Endpoint, error)`),
		[]byte(`return implementation.Create(ctx, request)`),
	} {
		if !bytes.Contains(orderSource, required) {
			t.Fatalf("order adapter omits %q:\n%s", required, orderSource)
		}
	}
	if bytes.Index(orderSource, []byte(`"order_already_exists"`)) > bytes.Index(orderSource, []byte(`"order_invalid"`)) {
		t.Fatalf("semantic errors are not canonical:\n%s", orderSource)
	}
	copyData := files[0].Data()
	copyData[0] = 'X'
	if bytes.Equal(copyData, files[0].Data()) {
		t.Fatal("File.Data exposed mutable source storage")
	}

	repeated, err := implementationadaptergen.Render([]implementationadaptergen.Input{inputs[1], inputs[0]})
	if err != nil || !reflect.DeepEqual(adapterPaths(repeated), adapterPaths(files)) {
		t.Fatalf("reordered Render paths = %v, %v", adapterPaths(repeated), err)
	}
	for index := range files {
		if files[index].InterfaceID() != repeated[index].InterfaceID() || files[index].Constructor() != repeated[index].Constructor() || files[index].ConcreteType() != repeated[index].ConcreteType() || !bytes.Equal(files[index].Data(), repeated[index].Data()) {
			t.Fatalf("reordered Render changed file %d", index)
		}
	}
	inputs[1].SemanticErrors[0] = "mutated_after_render"
	if bytes.Contains(files[0].Data(), []byte("mutated_after_render")) {
		t.Fatal("Render retained mutable semantic-error input")
	}
}

func TestRenderRejectsInvalidAndDuplicateImplementationAdapterInputs(t *testing.T) {
	t.Parallel()

	valid := adapterInput(t, "order.create/v1", "example.com/contracts/order/create/v1", "Create", "example.com/impl/orders.New", "*example.com/impl/orders.service", []string{"order_invalid"})
	tests := []struct {
		name  string
		input implementationadaptergen.Input
	}{
		{name: "missing Interface ID", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.InterfaceID = interfaceid.Identifier{} })},
		{name: "invalid package", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.PackagePath = "../contract" })},
		{name: "Kernel capability package", input: withAdapterField(valid, func(input *implementationadaptergen.Input) {
			input.PackagePath = "github.com/plystra/kernel/capability"
		})},
		{name: "unexported method", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.MethodName = "create" })},
		{name: "invalid request", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.RequestName = "request" })},
		{name: "invalid response", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.ResponseName = "for" })},
		{name: "missing constructor", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.Constructor = constructorsymbol.Symbol{} })},
		{name: "missing concrete type", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.ConcreteType = "" })},
		{name: "non-pointer concrete type", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.ConcreteType = "example.com/impl/orders.service" })},
		{name: "unsafe concrete type", input: withAdapterField(valid, func(input *implementationadaptergen.Input) {
			input.ConcreteType = "*example.com/impl/orders.service\nsecret"
		})},
		{name: "invalid semantic error", input: withAdapterField(valid, func(input *implementationadaptergen.Input) { input.SemanticErrors = []string{"OrderInvalid"} })},
		{name: "duplicate semantic error", input: withAdapterField(valid, func(input *implementationadaptergen.Input) {
			input.SemanticErrors = []string{"order_invalid", "order_invalid"}
		})},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files, err := implementationadaptergen.Render([]implementationadaptergen.Input{test.input})
			if len(files) != 0 || !errors.Is(err, implementationadaptergen.ErrRender) || !errors.Is(err, implementationadaptergen.ErrInvalidInput) {
				t.Fatalf("Render = %#v, %v", files, err)
			}
		})
	}
	if files, err := implementationadaptergen.Render([]implementationadaptergen.Input{valid, valid}); len(files) != 0 || !errors.Is(err, implementationadaptergen.ErrRender) || !errors.Is(err, implementationadaptergen.ErrDuplicateInterface) {
		t.Fatalf("Render duplicate = %#v, %v", files, err)
	}

	allInvalid := valid
	allInvalid.MethodName = "method"
	allInvalid.RequestName = "request"
	allInvalid.ResponseName = "response"
	allInvalid.Constructor = constructorsymbol.Symbol{}
	allInvalid.ConcreteType = ""
	for attempt := 0; attempt < 32; attempt++ {
		_, err := implementationadaptergen.Render([]implementationadaptergen.Input{allInvalid})
		if err == nil || !bytes.Contains([]byte(err.Error()), []byte(`method name "method"`)) {
			t.Fatalf("Render invalid field order on attempt %d = %v", attempt, err)
		}
	}
}

func TestGeneratedAdapterInvokesUnexportedConcretePointerAndNormalizesSemanticErrors(t *testing.T) {
	input := adapterInput(
		t,
		"order.create/v1",
		"example.com/adapterfixture/interfaces/order/create/v1",
		"Create",
		"example.com/adapterfixture/implementation.New",
		"*example.com/adapterfixture/implementation.service",
		[]string{"order_invalid"},
	)
	files, err := implementationadaptergen.Render([]implementationadaptergen.Input{input})
	if err != nil || len(files) != 1 {
		t.Fatalf("Render = %#v, %v", files, err)
	}

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeAdapterFile(t, root, "go.mod", fmt.Sprintf(`module example.com/adapterfixture

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
	writeAdapterBytes(t, root, "go.sum", goSum)
	writeAdapterFile(t, root, "interfaces/order/create/v1/interface.go", `package createv1

import "context"

type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct { Value string }
type Response struct { Value string }
`)
	writeAdapterFile(t, root, "implementation/service.go", `package implementation

import (
	"context"

	contract "example.com/adapterfixture/interfaces/order/create/v1"
)

type service struct{}

func New() (*service, error) { return &service{}, nil }

func (*service) Create(_ context.Context, request contract.Request) (contract.Response, error) {
	if request.Value == "semantic" {
		return contract.Response{Value: "must not escape"}, semanticFailure("order_invalid")
	}
	return contract.Response{Value: "handled:" + request.Value}, nil
}

type semanticFailure string

func (failure semanticFailure) Error() string { return "implementation secret: " + string(failure) }
func (failure semanticFailure) SemanticErrorCode() string { return string(failure) }
`)
	writeAdapterBytes(t, root, files[0].Path(), files[0].Data())
	writeAdapterFile(t, root, "generated/go/adapters/implementations/order/create/v1/adapter_gen_test.go", generatedAdapterRuntimeTest)

	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS=-mod=readonly", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("test generated adapter module: %v\n%s", err, output)
	}
}

func adapterInput(t testing.TB, identifier, packagePath, method, constructor, concrete string, semanticErrors []string) implementationadaptergen.Input {
	t.Helper()
	parsedID, err := interfaceid.Parse(identifier)
	if err != nil {
		t.Fatalf("interfaceid.Parse(%q): %v", identifier, err)
	}
	parsedConstructor, err := constructorsymbol.Parse(constructor)
	if err != nil {
		t.Fatalf("constructorsymbol.Parse(%q): %v", constructor, err)
	}
	return implementationadaptergen.Input{
		InterfaceID:    parsedID,
		PackagePath:    packagePath,
		MethodName:     method,
		RequestName:    "Request",
		ResponseName:   "Response",
		Constructor:    parsedConstructor,
		ConcreteType:   concrete,
		SemanticErrors: append([]string(nil), semanticErrors...),
	}
}

func withAdapterField(input implementationadaptergen.Input, change func(*implementationadaptergen.Input)) implementationadaptergen.Input {
	input.SemanticErrors = append([]string(nil), input.SemanticErrors...)
	change(&input)
	return input
}

func adapterPaths(files []implementationadaptergen.File) []string {
	paths := make([]string, len(files))
	for index, file := range files {
		paths[index] = file.Path()
	}
	return paths
}

func writeAdapterFile(t testing.TB, root, relative, data string) {
	t.Helper()
	writeAdapterBytes(t, root, relative, []byte(data))
}

func writeAdapterBytes(t testing.TB, root, relative string, data []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

const generatedAdapterRuntimeTest = `package adapter_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	adapter "example.com/adapterfixture/generated/go/adapters/implementations/order/create/v1"
	implementation "example.com/adapterfixture/implementation"
	contract "example.com/adapterfixture/interfaces/order/create/v1"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestAdapterUsesSelectedUnexportedConcretePointer(t *testing.T) {
	selected, err := implementation.New()
	if err != nil { t.Fatal(err) }
	endpoint, err := adapter.NewEndpoint(selected)
	if err != nil { t.Fatal(err) }
	build, err := invocation.NewModuleBuild("example.com/adapterfixture", "v1.0.0", "")
	if err != nil { t.Fatal(err) }
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		ProviderKind: invocation.ProviderKindKernel,
		ProviderPackage: "example.com/adapterfixture/implementation",
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
	handle, err := invocation.NewHandle(dispatcher, adapter.Contract(), true)
	if err != nil { t.Fatal(err) }
	response, err := handle.Invoke(context.Background(), contract.Request{Value: "request"})
	if err != nil || response.Value != "handled:request" {
		t.Fatalf("Invoke = %#v, %v", response, err)
	}

	response, err = handle.Invoke(context.Background(), contract.Request{Value: "semantic"})
	var semantic *invocation.SemanticError
	if response != (contract.Response{}) || !errors.As(err, &semantic) || semantic.SemanticErrorCode() != "order_invalid" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("semantic Invoke = %#v, %T %v", response, err, err)
	}

	independent := capability.MustParseContractWithSemanticErrors[contract.Request, contract.Response](adapter.InterfaceID, "order_invalid")
	wrongHandle, err := invocation.NewHandle(dispatcher, independent, true)
	if err != nil { t.Fatal(err) }
	_, err = wrongHandle.Invoke(context.Background(), contract.Request{})
	var mismatch *invocation.Error
	if !errors.As(err, &mismatch) || mismatch.Code() != invocation.ErrorInternal || mismatch.DetailCode() != "runtime.contract_mismatch" {
		t.Fatalf("independently parsed contract Invoke error = %v", err)
	}
}
`
