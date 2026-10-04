package implementationassemblygen_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationassemblygen"
)

func TestRenderResourceInstancesRemainSeparateAndDependencyOrdered(t *testing.T) {
	t.Parallel()
	options := resourceOptions(t)
	file, err := implementationassemblygen.Render(options)
	if err != nil {
		t.Fatal(err)
	}
	source := string(file.Data())
	for _, fragment := range []string{
		`ResourceConfig0`, `ResourceConfig1`,
		`kernellifecycle.NewResourceBinding("database.primary", "example.com/application/database.New", instance)`,
		`kernellifecycle.NewResourceBinding("database.replica", "example.com/application/database.New", instance)`,
		`kernellifecycle.NewResourceBinding("database.wrapped", "example.com/application/wrapper.New", instance)`,
		`(configuration.ResourceConfig0)`, `(configuration.ResourceConfig1)`, `.New(resource0)`,
		`var resource0`, `var resource1`, `var resource2`,
		`resourceValue0 != nil && ok`, `resourceValue1 != nil && ok`,
		`if resourceError != nil`, `if resourceValue0 == nil`,
		`Resource instance database.primary provider example.com/application/database.New failed`,
		`lifecycleBindings := make([]kernellifecycle.Binding, 0, 5)`,
		`.New(interface2, resource2, plystra.Optional[`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("generated Resource assembly omits %q:\n%s", fragment, source)
		}
	}
	if strings.Count(source, "resourceValue0, resourceError :=") != 1 || strings.Count(source, "resourceValue1, resourceError :=") != 1 {
		t.Fatal("same-provider instances collapsed or repeated")
	}
	if strings.Index(source, `resourceValue0, resourceError :=`) >= strings.Index(source, `resourceValue2, resourceError :=`) || strings.Index(source, `resourceValue2, resourceError :=`) >= strings.Index(source, `implementation0, constructorError :=`) {
		t.Fatal("resources are not constructed before consumers")
	}
	if strings.Index(source, `kernellifecycle.NewResourceBinding("database.primary"`) >= strings.Index(source, `if resourceError != nil`) {
		t.Fatal("partial Resource result is not registered before failure cleanup")
	}
	if len(file.Bindings()) != len(options.Bindings) || strings.Contains(source, `resource0.NewEndpoint`) || strings.Contains(source, `Constructor:     "database.`) {
		t.Fatal("Resource entered governed Interface binding catalog")
	}
	reordered := resourceOptions(t)
	slices.Reverse(reordered.Resources)
	again, err := implementationassemblygen.Render(reordered)
	if err != nil || !bytes.Equal(file.Data(), again.Data()) || !reflect.DeepEqual(file.Resources(), again.Resources()) {
		t.Fatalf("Resource input order changed frozen assembly: %v", err)
	}
	resources := file.Resources()
	resources[2].Dependencies[0].InstanceName = "changed"
	constructors := file.Constructors()
	constructors[1].ResourceDependencies[0].InstanceName = "changed"
	options.Resources[0].Dependencies[0].InstanceName = "changed"
	if file.Resources()[2].Dependencies[0].InstanceName != "database.primary" || file.Constructors()[1].ResourceDependencies[0].InstanceName != "database.wrapped" {
		t.Fatal("Resource assembly accessors or input share mutable storage")
	}
}

func TestRenderSelectedResourceWithoutInterfaceRoots(t *testing.T) {
	t.Parallel()
	options := resourceOptions(t)
	options.Bindings, options.Constructors = nil, nil
	file, err := implementationassemblygen.Render(options)
	if err != nil || len(file.Resources()) != 3 || len(file.Constructors()) != 0 || len(file.Bindings()) != 0 {
		t.Fatalf("Resource-only assembly = %v", err)
	}
	if !bytes.Contains(file.Data(), []byte("kernellifecycle.NewResourceBinding")) || !bytes.Contains(file.Data(), []byte("_ = resource1")) {
		t.Fatal("unconsumed selected Resource was not constructed")
	}
}

func TestRenderResourceOrderFollowsAuthoredDependencyPositions(t *testing.T) {
	t.Parallel()
	options := resourceOptions(t)
	options.Bindings, options.Constructors = nil, nil
	wrapper := &options.Resources[0]
	wrapper.Name = "database.a-wrapper"
	first := wrapper.Dependencies[0]
	first.InstanceName = "database.replica"
	second := first
	second.InstanceName, second.ParameterName, second.ParameterPosition = "database.primary", "primary", 2
	wrapper.Dependencies = []implementationassemblygen.ResourceDependencyInput{first, second}
	file, err := implementationassemblygen.Render(options)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, resource := range file.Resources() {
		order = append(order, resource.Name)
	}
	if !reflect.DeepEqual(order, []string{"database.replica", "database.primary", "database.a-wrapper"}) || !bytes.Contains(file.Data(), []byte(".New(resource0, resource1)")) {
		t.Fatalf("authored Resource dependency order changed: %v", order)
	}
}

func TestRenderRejectsInvalidResourcePlans(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*implementationassemblygen.Options){
		"invalid name":   func(o *implementationassemblygen.Options) { o.Resources[0].Name = "Database.Bad" },
		"duplicate name": func(o *implementationassemblygen.Options) { o.Resources[1].Name = o.Resources[0].Name },
		"missing digest": func(o *implementationassemblygen.Options) { o.Resources[0].ContractDigest = [sha256.Size]byte{} },
		"changed contract": func(o *implementationassemblygen.Options) {
			o.Resources[0].ContractDigest = sha256.Sum256([]byte("other"))
		},
		"provider schema":  func(o *implementationassemblygen.Options) { o.Resources[1].HasConfiguration = false },
		"wrong module":     func(o *implementationassemblygen.Options) { o.Resources[0].ModulePath = "example.com/other" },
		"missing upstream": func(o *implementationassemblygen.Options) { o.Resources[0].Dependencies[0].InstanceName = "absent" },
		"upstream contract": func(o *implementationassemblygen.Options) {
			o.Resources[0].Dependencies[0].ResourceID = mustInterfaceID(t, "data.other/v1")
		},
		"cycle": func(o *implementationassemblygen.Options) {
			o.Resources[0].Dependencies[0].InstanceName = o.Resources[0].Name
		},
		"blank resource parameter": func(o *implementationassemblygen.Options) { o.Resources[0].Dependencies[0].ParameterName = "_" },
		"absent position":          func(o *implementationassemblygen.Options) { o.Resources[0].Dependencies[0].ParameterPosition = 0 },
		"missing consumer target": func(o *implementationassemblygen.Options) {
			o.Constructors[1].ResourceDependencies[0].InstanceName = "absent"
		},
		"consumer contract": func(o *implementationassemblygen.Options) {
			o.Constructors[1].ResourceDependencies[0].PackagePath = "example.com/other"
		},
		"overlapping parameters": func(o *implementationassemblygen.Options) {
			o.Constructors[1].ResourceDependencies[0].ParameterPosition = 1
		},
		"position gap": func(o *implementationassemblygen.Options) {
			o.Constructors[1].ResourceDependencies[0].ParameterPosition = 5
		},
		"blank consumer parameter": func(o *implementationassemblygen.Options) {
			o.Constructors[1].ResourceDependencies[0].ParameterName = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			options := resourceOptions(t)
			mutate(&options)
			if _, err := implementationassemblygen.Render(options); !errors.Is(err, implementationassemblygen.ErrRender) {
				t.Fatalf("accepted invalid Resource plan: %v", err)
			}
		})
	}
}

func resourceOptions(t testing.TB) implementationassemblygen.Options {
	t.Helper()
	options := validOptions(t)
	resource := implementationassemblygen.ResourceInput{
		Name: "database.primary", ResourceID: mustInterfaceID(t, "data.database/v1"),
		PackagePath: "example.com/application/resources/database", ContractDigest: sha256.Sum256([]byte("database-contract")),
		Provider: mustSymbol(t, "example.com/application/database.New"), ModulePath: "example.com/application", HasConfiguration: true,
	}
	dependency := implementationassemblygen.ResourceDependencyInput{
		ResourceID: resource.ResourceID, PackagePath: resource.PackagePath,
		InstanceName: resource.Name, ParameterName: "upstream", ParameterPosition: 1,
	}
	replica := resource
	replica.Name = "database.replica"
	wrapper := resource
	wrapper.Name, wrapper.Provider, wrapper.HasConfiguration = "database.wrapped", mustSymbol(t, "example.com/application/wrapper.New"), false
	wrapper.Dependencies = []implementationassemblygen.ResourceDependencyInput{dependency}
	options.Resources = []implementationassemblygen.ResourceInput{wrapper, replica, resource}
	options.Constructors[1].Dependencies[1].ParameterPosition = 3
	dependency.InstanceName, dependency.ParameterName, dependency.ParameterPosition = wrapper.Name, "database", 2
	options.Constructors[1].ResourceDependencies = []implementationassemblygen.ResourceDependencyInput{dependency}
	return options
}
