package assemblygen_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/assemblygen"
)

func TestRenderProvidersOrdersConstructorDependencies(t *testing.T) {
	t.Parallel()
	providers := []assemblygen.ProviderInput{
		{PluginID: "a.consumer", ModulePath: wiringApplicationModule, ImportPath: wiringApplicationModule + "/consumer", Dependencies: []assemblygen.DependencyInput{{Capability: "catalog.lookup/v1", ContractJSON: []byte(wiringLookupSchema)}}},
		{PluginID: "z.catalog", ModulePath: wiringDependencyModule, ImportPath: wiringDependencyModule + "/catalog"},
	}
	invocations := []assemblygen.InvocationInput{
		{ProviderID: "a.consumer", ContractJSON: []byte(wiringOrderSchema)},
		{ProviderID: "z.catalog", ContractJSON: []byte(wiringLookupSchema)},
	}
	render := func(providers []assemblygen.ProviderInput, invocations []assemblygen.InvocationInput) ([]byte, error) {
		return assemblygen.RenderProviders(wiringApplicationModule, providers, invocations)
	}
	source, err := render(providers, invocations)
	if err != nil {
		t.Fatal(err)
	}
	for _, names := range [][2]string{
		{"providers.plugin1, err = constructPlugin1", "providers.plugin0, err = constructPlugin0"},
		{`kernellifecycle.NewBinding("` + wiringDependencyModule + `/catalog.New"`, `kernellifecycle.NewBinding("` + wiringApplicationModule + `/consumer.New"`},
	} {
		first, second := bytes.Index(source, []byte(names[0])), bytes.Index(source, []byte(names[1]))
		if first < 0 || second < 0 || first >= second {
			t.Fatalf("not dependency-first: %v", names)
		}
	}
	reversed, err := render([]assemblygen.ProviderInput{providers[1], providers[0]}, []assemblygen.InvocationInput{invocations[1], invocations[0]})
	if err != nil || !bytes.Equal(source, reversed) {
		t.Fatalf("unstable provider order: %v", err)
	}

	t.Run("missing selection", func(t *testing.T) {
		if _, err := render(providers, invocations[:1]); !errors.Is(err, assemblygen.ErrInvocationDependency) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("missing owner", func(t *testing.T) {
		bad := append([]assemblygen.InvocationInput(nil), invocations...)
		bad[1].ProviderID = "missing.provider"
		if _, err := render(providers, bad); !errors.Is(err, assemblygen.ErrInvalidProvider) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("duplicate selection", func(t *testing.T) {
		if _, err := render(providers, append(invocations, invocations[0])); !errors.Is(err, assemblygen.ErrDuplicateInvocation) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		bad := append([]assemblygen.ProviderInput(nil), providers...)
		bad[1].Dependencies = []assemblygen.DependencyInput{{Capability: "order.place/v1", ContractJSON: []byte(wiringOrderSchema)}}
		if _, err := render(bad, invocations); !errors.Is(err, assemblygen.ErrInvocationDependency) || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("self cycle", func(t *testing.T) {
		bad := append([]assemblygen.InvocationInput(nil), invocations...)
		bad[1].ProviderID = "a.consumer"
		if _, err := render(providers, bad); !errors.Is(err, assemblygen.ErrInvocationDependency) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("intrinsic", func(t *testing.T) {
		contract := intrinsicSchema(t, "kernel.health/v1")
		bad := append([]assemblygen.ProviderInput(nil), providers...)
		bad[0].Dependencies = []assemblygen.DependencyInput{{Capability: "kernel.health/v1", ContractJSON: contract}}
		inputs := append(append([]assemblygen.InvocationInput(nil), invocations...), assemblygen.InvocationInput{Intrinsic: true, ContractJSON: contract})
		if _, err := render(bad, inputs); err != nil {
			t.Fatal(err)
		}
		inputs[2].ProviderID = "a.consumer"
		if _, err := render(bad, inputs); !errors.Is(err, assemblygen.ErrInvalidProvider) {
			t.Fatalf("error = %v", err)
		}
	})
}
