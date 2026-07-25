package implementationassemblygen_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationassemblygen"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestRenderBuildsDependencyFirstGovernedInterfaceRuntime(t *testing.T) {
	t.Parallel()

	options := validOptions(t)
	file, err := implementationassemblygen.Render(options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if file.Path() != implementationassemblygen.Path {
		t.Fatalf("Path = %q", file.Path())
	}
	source := file.Data()
	for _, required := range []string{
		`func NewInterfaceRuntime(configuration ConstructorConfiguration, rollbackTimeout time.Duration) (InterfaceRuntime, error)`,
		`kernelinvocation.NewHandle(dispatcher,`,
		`.Contract(), true)`,
		`kernelinvocation.BindingKindImplementation`,
		`kernelinvocation.SelectionReasonUniqueCompatible`,
		`Constructor:     "example.com/application/app.New"`,
		`ContractDigest:  [32]byte{`,
		`plystra.Optional[`,
		`kernelinvocation.NewCatalog(bindings)`,
		`dispatcher.Publish(catalog)`,
		`kernellifecycle.NewBinding("example.com/application/audit.New", instance)`,
		`kernellifecycle.NewBinding("example.com/application/app.New", instance)`,
		`kernellifecycle.NewManager(kernellifecycle.ManagerOptions{RollbackTimeout: rollbackTimeout}, lifecycleBindings)`,
		`func (runtime InterfaceRuntime) Start(ctx context.Context) error`,
		`func (runtime InterfaceRuntime) Stop(ctx context.Context) error`,
		`runtime.lifecycle.State().Valid()`,
		`func (runtime InterfaceRuntime) AppRunV1()`,
	} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, source)
		}
	}
	auditConstructor := bytes.Index(source, []byte(`.New()`))
	appConstructor := bytes.Index(source, []byte(`.New(interface2, plystra.Optional[`))
	if auditConstructor < 0 || appConstructor < 0 || auditConstructor >= appConstructor {
		t.Fatalf("constructors are not dependency-first:\n%s", source)
	}
	auditLifecycle := bytes.Index(source, []byte(`kernellifecycle.NewBinding("example.com/application/audit.New", instance)`))
	appLifecycle := bytes.Index(source, []byte(`kernellifecycle.NewBinding("example.com/application/app.New", instance)`))
	if auditLifecycle < 0 || appLifecycle < 0 || auditLifecycle >= appLifecycle {
		t.Fatalf("lifecycle bindings are not dependency-first:\n%s", source)
	}
	if bytes.Count(source, []byte(`Constructor:     "example.com/application/app.New"`)) != 2 {
		t.Fatalf("multi-Interface constructor provenance was not retained for two bindings:\n%s", source)
	}
	if !bytes.Contains(source, []byte(`.NewEndpoint(implementation1)`)) || bytes.Count(source, []byte(`.NewEndpoint(implementation1)`)) != 2 {
		t.Fatalf("one app constructor instance was not shared by both adapters:\n%s", source)
	}
	if bytes.Index(source, []byte(`kernelinvocation.NewCatalog(bindings)`)) >= bytes.Index(source, []byte(`dispatcher.Publish(catalog)`)) {
		t.Fatalf("catalog publication order is invalid:\n%s", source)
	}

	bindings := file.Bindings()
	constructors := file.Constructors()
	bindings[0] = implementationassemblygen.BindingInput{}
	constructors[0].Dependencies = nil
	if file.Bindings()[0].InterfaceID.String() == "" || len(file.Constructors()[1].Dependencies) != 2 {
		t.Fatal("File accessors share mutable storage")
	}
}

func TestRenderExposesKernelOwnedIntrinsicInterfacesThroughGovernedProxies(t *testing.T) {
	t.Parallel()

	options := validOptions(t)
	options.IntrinsicBindings = []implementationassemblygen.IntrinsicBindingInput{
		{
			InterfaceID: mustInterfaceID(t, "kernel.info/v1"),
			PackagePath: "github.com/plystra/kernel/interfaces/kernel/info/v1",
			MethodName:  "Info",
		},
		{
			InterfaceID: mustInterfaceID(t, "kernel.health/v1"),
			PackagePath: "github.com/plystra/kernel/interfaces/kernel/health/v1",
			MethodName:  "Health",
		},
	}
	file, err := implementationassemblygen.Render(options)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, required := range []string{
		"kernelintrinsic.HealthContract()",
		"kernelintrinsic.InfoContract()",
		"func (runtime InterfaceRuntime) KernelHealthV1()",
		"func (runtime InterfaceRuntime) KernelInfoV1()",
		"return runtime.intrinsic0",
		"return runtime.intrinsic1",
	} {
		if !bytes.Contains(file.Data(), []byte(required)) {
			t.Fatalf("generated intrinsic assembly omits %q:\n%s", required, file.Data())
		}
	}
	intrinsics := file.IntrinsicBindings()
	if len(intrinsics) != 2 || intrinsics[0].InterfaceID.String() != "kernel.health/v1" || intrinsics[1].InterfaceID.String() != "kernel.info/v1" {
		t.Fatalf("IntrinsicBindings = %#v", intrinsics)
	}
	intrinsics[0] = implementationassemblygen.IntrinsicBindingInput{}
	if file.IntrinsicBindings()[0].InterfaceID.String() == "" {
		t.Fatal("IntrinsicBindings shares mutable storage")
	}

	reversed := options
	reversed.IntrinsicBindings = append([]implementationassemblygen.IntrinsicBindingInput(nil), options.IntrinsicBindings...)
	reversed.IntrinsicBindings[0], reversed.IntrinsicBindings[1] = reversed.IntrinsicBindings[1], reversed.IntrinsicBindings[0]
	repeated, err := implementationassemblygen.Render(reversed)
	if err != nil || !bytes.Equal(file.Data(), repeated.Data()) || !reflect.DeepEqual(file.IntrinsicBindings(), repeated.IntrinsicBindings()) {
		t.Fatalf("reordered intrinsic bindings changed assembly: %v", err)
	}
}

func TestRenderIsDeterministicAcrossInputOrder(t *testing.T) {
	t.Parallel()

	forward := validOptions(t)
	reverse := validOptions(t)
	for left, right := 0, len(reverse.Bindings)-1; left < right; left, right = left+1, right-1 {
		reverse.Bindings[left], reverse.Bindings[right] = reverse.Bindings[right], reverse.Bindings[left]
	}
	for left, right := 0, len(reverse.Constructors)-1; left < right; left, right = left+1, right-1 {
		reverse.Constructors[left], reverse.Constructors[right] = reverse.Constructors[right], reverse.Constructors[left]
	}
	first, err := implementationassemblygen.Render(forward)
	if err != nil {
		t.Fatalf("Render(forward): %v", err)
	}
	second, err := implementationassemblygen.Render(reverse)
	if err != nil {
		t.Fatalf("Render(reverse): %v", err)
	}
	if !bytes.Equal(first.Data(), second.Data()) || !reflect.DeepEqual(first.Bindings(), second.Bindings()) || !reflect.DeepEqual(first.Constructors(), second.Constructors()) {
		t.Fatal("input order changed normalized static assembly")
	}
}

func TestRenderRejectsContradictoryOrCyclicGraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*implementationassemblygen.Options)
		want   error
	}{
		{
			name: "missing selected constructor",
			mutate: func(options *implementationassemblygen.Options) {
				options.Constructors = options.Constructors[:1]
			},
			want: implementationassemblygen.ErrConstructorGraph,
		},
		{
			name: "required dependency unavailable",
			mutate: func(options *implementationassemblygen.Options) {
				options.Constructors[1].Dependencies[0].Available = false
			},
			want: implementationassemblygen.ErrConstructorGraph,
		},
		{
			name: "cycle",
			mutate: func(options *implementationassemblygen.Options) {
				options.Constructors[0].Dependencies = []implementationassemblygen.DependencyInput{{
					InterfaceID:       mustInterfaceID(t, "app.run/v1"),
					PackagePath:       "example.com/application/interfaces/app/run/v1",
					ParameterPosition: 1,
					Available:         true,
				}}
			},
			want: implementationassemblygen.ErrConstructorGraph,
		},
		{
			name: "non-Kernel intrinsic binding",
			mutate: func(options *implementationassemblygen.Options) {
				options.IntrinsicBindings = []implementationassemblygen.IntrinsicBindingInput{{
					InterfaceID: mustInterfaceID(t, "kernel.health/v1"),
					PackagePath: "example.com/application/interfaces/kernel/health/v1",
					MethodName:  "Health",
				}}
			},
			want: implementationassemblygen.ErrInvalidInput,
		},
		{
			name: "duplicate intrinsic binding",
			mutate: func(options *implementationassemblygen.Options) {
				binding := implementationassemblygen.IntrinsicBindingInput{
					InterfaceID: mustInterfaceID(t, "kernel.health/v1"),
					PackagePath: "github.com/plystra/kernel/interfaces/kernel/health/v1",
					MethodName:  "Health",
				}
				options.IntrinsicBindings = []implementationassemblygen.IntrinsicBindingInput{binding, binding}
			},
			want: implementationassemblygen.ErrInvalidInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validOptions(t)
			test.mutate(&options)
			file, err := implementationassemblygen.Render(options)
			if !errors.Is(err, implementationassemblygen.ErrRender) || !errors.Is(err, test.want) || len(file.Data()) != 0 {
				t.Fatalf("Render = %#v, %v", file, err)
			}
		})
	}
}

func validOptions(t testing.TB) implementationassemblygen.Options {
	t.Helper()
	app := mustSymbol(t, "example.com/application/app.New")
	audit := mustSymbol(t, "example.com/application/audit.New")
	return implementationassemblygen.Options{
		ModulePath:               "example.com/application",
		ApplicationBuildIdentity: "sha256:0123456789abcdef",
		KernelModuleVersion:      "v0.1.0",
		DefaultTimeout:           time.Second,
		Bindings: []implementationassemblygen.BindingInput{
			binding(t, "app.run/v1", "example.com/application/interfaces/app/run/v1", app),
			binding(t, "audit.write/v1", "example.com/application/interfaces/audit/write/v1", audit),
			binding(t, "app.check/v1", "example.com/application/interfaces/app/check/v1", app),
		},
		Constructors: []implementationassemblygen.ConstructorInput{
			{Symbol: audit, ModulePath: "example.com/application"},
			{
				Symbol:     app,
				ModulePath: "example.com/application",
				Dependencies: []implementationassemblygen.DependencyInput{
					{
						InterfaceID:       mustInterfaceID(t, "audit.write/v1"),
						PackagePath:       "example.com/application/interfaces/audit/write/v1",
						ParameterName:     "audit",
						ParameterPosition: 1,
						Available:         true,
					},
					{
						InterfaceID:       mustInterfaceID(t, "notify.send/v1"),
						PackagePath:       "example.com/application/interfaces/notify/send/v1",
						ParameterName:     "notify",
						ParameterPosition: 2,
						Optional:          true,
					},
				},
			},
		},
	}
}

func binding(t testing.TB, id, packagePath string, constructor constructorsymbol.Symbol) implementationassemblygen.BindingInput {
	t.Helper()
	return implementationassemblygen.BindingInput{
		InterfaceID:     mustInterfaceID(t, id),
		PackagePath:     packagePath,
		Constructor:     constructor,
		SelectionReason: implementationassemblygen.SelectionUniqueCompatible,
		ContractDigest:  sha256.Sum256([]byte(id)),
	}
}

func mustInterfaceID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	identifier, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatalf("Parse Interface ID %q: %v", value, err)
	}
	return identifier
}

func mustSymbol(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatalf("Parse constructor %q: %v", value, err)
	}
	return symbol
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
