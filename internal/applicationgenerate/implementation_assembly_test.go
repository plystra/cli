package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGeneratedStaticAssemblyPreservesOrdinaryTypedBusinessCalls(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const modulePath = "example.com/acme/static-interface-runtime"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.check/v1, app.run/v1]}\n")
	writeAssemblyInterface(t, root, "app/check/v1", "checkv1", "app.check/v1", "Check", `
type Request struct{}
type Response struct { Instance int64 `+"`plystra:\"1,required\"`"+` }
`)
	writeAssemblyInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run", `
type Request struct { Value string `+"`plystra:\"1,required\"`"+` }
type Response struct {
	Value string `+"`plystra:\"1,required\"`"+`
	Instance int64 `+"`plystra:\"2,required\"`"+`
	NotifyAvailable bool `+"`plystra:\"3,required\"`"+`
}
`)
	writeAssemblyInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write", `
type Request struct { Value string `+"`plystra:\"1,required\"`"+` }
type Response struct { Value string `+"`plystra:\"1,required\"`"+` }
`)
	writeAssemblyInterface(t, root, "notify/send/v1", "sendv1", "notify.send/v1", "Send", `
type Request struct{}
type Response struct{}
`)
	writeFile(t, filepath.Join(root, "probe", "probe.go"), `package probe

import "sync"

var (
	mu sync.Mutex
	order []string
	next int64
)

func Constructed(name string) int64 {
	mu.Lock()
	defer mu.Unlock()
	order = append(order, name)
	next++
	return next
}

func Order() []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), order...)
}
`)
	writeFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"

	"example.com/acme/static-interface-runtime/probe"
	writev1 "example.com/acme/static-interface-runtime/interfaces/audit/write/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

type service struct{}

//plystra:implements audit.write/v1
func New() (*service, error) {
	probe.Constructed("audit")
	return &service{}, nil
}

func (*service) Write(ctx context.Context, request writev1.Request) (writev1.Response, error) {
	if _, governed := kernelinvocation.Current(ctx); !governed {
		panic("audit call bypassed Kernel governance")
	}
	return writev1.Response{Value: "audit:" + request.Value}, nil
}
`)
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	plystra "github.com/plystra/kernel"
	checkv1 "example.com/acme/static-interface-runtime/interfaces/app/check/v1"
	runv1 "example.com/acme/static-interface-runtime/interfaces/app/run/v1"
	writev1 "example.com/acme/static-interface-runtime/interfaces/audit/write/v1"
	sendv1 "example.com/acme/static-interface-runtime/interfaces/notify/send/v1"
	"example.com/acme/static-interface-runtime/probe"
)

type service struct {
	audit writev1.Interface
	notify plystra.Optional[sendv1.Interface]
	instance int64
}

//plystra:implements app.check/v1
//plystra:implements app.run/v1
func New(audit writev1.Interface, notify plystra.Optional[sendv1.Interface]) (*service, error) {
	return &service{audit: audit, notify: notify, instance: probe.Constructed("app")}, nil
}

func (service *service) Check(context.Context, checkv1.Request) (checkv1.Response, error) {
	return checkv1.Response{Instance: service.instance}, nil
}

func (service *service) Run(ctx context.Context, request runv1.Request) (runv1.Response, error) {
	audit, err := service.audit.Write(ctx, writev1.Request{Value: request.Value})
	if err != nil {
		return runv1.Response{}, err
	}
	return runv1.Response{
		Value: audit.Value,
		Instance: service.instance,
		NotifyAvailable: service.notify.Available(),
	}, nil
}
`)

	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = %#v, %v", generated.Report().Changes(), err)
	}
	assemblyPath := "generated/go/assembly/interfaces_gen.go"
	assemblySource := readFile(t, root, assemblyPath)
	for _, required := range [][]byte{
		[]byte(`interface1 :=`),
		[]byte(`plystra.Optional[`),
		[]byte(`Constructor:     "example.com/acme/static-interface-runtime/app.New"`),
		[]byte(`kernelinvocation.NewCatalog(bindings)`),
		[]byte(`dispatcher.Publish(catalog)`),
	} {
		if !bytes.Contains(assemblySource, required) {
			t.Fatalf("generated static assembly omits %q:\n%s", required, assemblySource)
		}
	}
	writeFile(t, filepath.Join(root, "acceptance", "runtime_test.go"), `package acceptance_test

import (
	"context"
	"reflect"
	"testing"

	assembly "example.com/acme/static-interface-runtime/generated/go/assembly"
	checkv1 "example.com/acme/static-interface-runtime/interfaces/app/check/v1"
	runv1 "example.com/acme/static-interface-runtime/interfaces/app/run/v1"
	"example.com/acme/static-interface-runtime/probe"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestRuntime(t *testing.T) {
	runtime, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{})
	if err != nil || !runtime.Valid() {
		t.Fatalf("NewInterfaceRuntime = %#v, %v", runtime, err)
	}
	if order := probe.Order(); !reflect.DeepEqual(order, []string{"audit", "app"}) {
		t.Fatalf("constructor order = %v", order)
	}
	checked, err := runtime.AppCheckV1().Check(context.Background(), checkv1.Request{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	run, err := runtime.AppRunV1().Run(context.Background(), runv1.Request{Value: "request"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Value != "audit:request" || run.Instance != checked.Instance || run.NotifyAvailable {
		t.Fatalf("ordinary typed call = %#v; check = %#v", run, checked)
	}
	seen := map[string]bool{}
	for _, binding := range runtime.Catalog().Bindings() {
		if binding.Kind() == kernelinvocation.BindingKindImplementation {
			seen[binding.InterfaceID().String()] = true
			if binding.Constructor() == "" || binding.SelectionReason() != kernelinvocation.SelectionReasonUniqueCompatible || binding.ContractDigest() == [32]byte{} {
				t.Fatalf("binding provenance = %#v", binding)
			}
		}
	}
	for _, id := range []string{"app.check/v1", "app.run/v1", "audit.write/v1"} {
		if !seen[id] {
			t.Fatalf("catalog omits %s: %v", id, seen)
		}
	}
}
`)

	command := exec.CommandContext(t.Context(), "go", "test", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go test generated static runtime: %v\n%s", err, output)
	}

	check, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if err != nil || !check.Report().Clean() {
		t.Fatalf("Generate --check = %#v, %v", check.Report().Changes(), err)
	}
}

func writeAssemblyInterface(t testing.TB, root, relative, packageName, identifier, method, messages string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), `package `+packageName+`

import "context"

//plystra:interface `+identifier+`
type Interface interface {
	`+method+`(context.Context, Request) (Response, error)
}
`+messages)
}
