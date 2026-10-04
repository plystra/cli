package interfaceresolution_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestResolveNamedResourceInstancesAndBindings(t *testing.T) {
	t.Parallel()
	base := resourceResolutionInput(t)
	consumer := mustResolutionSymbol(t, "example.com/application/service.New")
	source := constructorgraph.ResourceSource{Reference: "plystra.yaml resources", ModulePath: "example.com/application", Path: "plystra.yaml", Line: 1, Column: 1}
	instance := constructorgraph.ResourceInstanceInput{Name: "primary", Provider: mustResolutionSymbol(t, "example.com/application/store.New"), Sources: []constructorgraph.ResourceSource{source}}
	binding := constructorgraph.ResourceBindingInput{Namespace: constructorgraph.ResourceConsumerImplementation, Consumer: consumer.String(), Parameter: "database", Target: "primary", Sources: []constructorgraph.ResourceSource{source}}
	base.ResourceInstances = []constructorgraph.ResourceInstanceInput{instance}

	t.Run("implicit-active", func(t *testing.T) {
		result, err := interfaceresolution.Resolve(base)
		if err != nil {
			t.Fatal(err)
		}
		graph := result.Graph()
		dependencies := graph.ResourceDependencies(consumer)
		if len(dependencies) != 2 || dependencies[0].InstanceName() != "primary" || dependencies[1].InstanceName() != "primary" || dependencies[0].Reason() != constructorgraph.SelectionUnique || dependencies[0].ParameterPosition() != 1 || dependencies[1].ParameterPosition() != 2 || dependencies[1].ParameterName() != "Database" {
			t.Fatalf("implicit dependencies = %#v", dependencies)
		}
		if len(graph.ResourceConstructionOrder()) != 1 || len(graph.ConstructionOrder()) != 1 || len(graph.Roots()) != 1 || len(graph.Bindings()) != 1 {
			t.Fatal("Resource dependency changed Interface membership")
		}
	})
	t.Run("explicit-active", func(t *testing.T) {
		input := base
		second := instance
		second.Name = "secondary"
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{second, instance}
		other := binding
		other.Parameter, other.Target = "Database", "secondary"
		input.ResourceBindings = []constructorgraph.ResourceBindingInput{other, binding}
		result, err := interfaceresolution.Resolve(input)
		if err != nil {
			t.Fatal(err)
		}
		dependencies := result.Graph().ResourceDependencies(consumer)
		if len(dependencies) != 2 || dependencies[0].InstanceName() != "primary" || dependencies[1].InstanceName() != "secondary" || dependencies[1].Reason() != constructorgraph.SelectionExplicit {
			t.Fatalf("explicit dependencies = %#v", dependencies)
		}
	})
	t.Run("unconsumed-instances-and-dormant-consumer", func(t *testing.T) {
		input := base
		input.Requirements = nil
		input.Choices = []interfaceresolution.Choice{{InterfaceID: mustResolutionID(t, "app.run/v1"), Constructor: consumer, Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource("interfaces.use", source.ModulePath, source.Path)}}}
		input.ResourceBindings = []constructorgraph.ResourceBindingInput{binding}
		result, err := interfaceresolution.Resolve(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Graph().ResourceConstructionOrder()) != 1 || len(result.Graph().ConstructionOrder()) != 0 || len(result.Selections()) != 0 || len(result.Graph().ResourceDependencies(consumer)) != 0 {
			t.Fatal("dormant explicit binding changed activation")
		}
		input.ResourceBindings = nil
		input.ResourceInstances = nil
		if _, err := interfaceresolution.Resolve(input); err != nil {
			t.Fatalf("dormant implicit dependency required a Resource: %v", err)
		}
		invalid := binding
		invalid.Parameter = "stale"
		input.ResourceBindings = []constructorgraph.ResourceBindingInput{invalid}
		_, err = interfaceresolution.Resolve(input)
		var failure *constructorgraph.ResourceBindingError
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || !errors.As(err, &failure) || failure.Constructor() != consumer || failure.ParameterName() != "stale" || failure.SourcePath() != "service/new.go" {
			t.Fatalf("dormant explicit validation = %v", err)
		}
	})
	t.Run("missing-and-ambiguous-propagation", func(t *testing.T) {
		input := base
		input.ResourceInstances = nil
		_, err := interfaceresolution.Resolve(input)
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrMissingResourceBinding) {
			t.Fatalf("missing error = %v", err)
		}
		second := instance
		second.Name = "secondary"
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{second, instance}
		_, err = interfaceresolution.Resolve(input)
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrAmbiguousResourceBinding) {
			t.Fatalf("ambiguous error = %v", err)
		}
	})
	t.Run("provider-dependencies-and-cycle-propagation", func(t *testing.T) {
		input := base
		input.Requirements = nil
		wrapper := instance
		wrapper.Name, wrapper.Provider = "a-wrapper", mustResolutionSymbol(t, "example.com/application/store.Wrap")
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{wrapper, instance}
		input.ResourceBindings = []constructorgraph.ResourceBindingInput{{Namespace: constructorgraph.ResourceConsumerInstance, Consumer: wrapper.Name, Parameter: "upstream", Target: instance.Name, Sources: []constructorgraph.ResourceSource{source}}}
		result, err := interfaceresolution.Resolve(input)
		if err != nil {
			t.Fatal(err)
		}
		nodes := result.Graph().ResourceConstructionOrder()
		if len(nodes) != 2 || nodes[0].Name() != "primary" || nodes[1].Name() != "a-wrapper" || len(nodes[1].Dependencies()) != 1 {
			t.Fatalf("provider dependency order = %#v", nodes)
		}
		input.ResourceBindings[0].Target = wrapper.Name
		_, err = interfaceresolution.Resolve(input)
		var cycle *constructorgraph.ResourceCycleError
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrCycle) || !errors.As(err, &cycle) || len(cycle.Steps()) != 1 {
			t.Fatalf("Resource cycle = %v", err)
		}
	})
}

func resourceResolutionInput(t testing.TB) interfaceresolution.Input {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                         "module example.com/application\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\nreplace github.com/plystra/kernel => ./kernel\n",
		"plystra.yaml":                   "{}\n",
		"kernel/go.mod":                  "module github.com/plystra/kernel\n\ngo 1.26\n",
		"kernel/optional.go":             "package plystra\ntype Optional[T any] struct{}\n",
		"contracts/store/resource.go":    "package store\n//plystra:resource store.value/v1\ntype Resource interface { Value() string }\n",
		"interfaces/run/interface.go":    resolutionInterfaceSource("run", "app.run/v1", "Run"),
		"interfaces/entry/interface.go":  resolutionInterfaceSource("entry", "app.entry/v1", "Enter"),
		"interfaces/middle/interface.go": resolutionInterfaceSource("middle", "app.middle/v1", "Handle"),
		"entry/new.go": `package entry
import (
 "context"
 plystra "github.com/plystra/kernel"
 "example.com/application/interfaces/entry"
 "example.com/application/interfaces/middle"
)
type Service struct{}
//plystra:implements app.entry/v1
func New(next middle.Interface) (*Service, error) { panic("constructor executed") }
//plystra:implements app.entry/v1
func Optional(next plystra.Optional[middle.Interface]) (*Service, error) { panic("constructor executed") }
func (*Service) Enter(context.Context, entry.Request) (entry.Response, error) { return entry.Response{}, nil }
`,
		"middle/new.go": `package middle
import (
 "context"
 "example.com/application/interfaces/middle"
 "example.com/application/interfaces/run"
)
type Service struct{}
//plystra:implements app.middle/v1
func New(worker run.Interface) (*Service, error) { panic("constructor executed") }
func (*Service) Handle(context.Context, middle.Request) (middle.Response, error) { return middle.Response{}, nil }
`,
		"store/new.go": `package store
import "example.com/application/contracts/store"
type Value struct{}
func (*Value) Value() string { return "value" }
//plystra:implements-resource store.value/v1
func New() (*Value, error) { panic("constructor executed") }
//plystra:implements-resource store.value/v1
func Wrap(upstream store.Resource) (*Value, error) { panic("constructor executed") }
`,
		"service/new.go": `package service
import (
 "context"
 "example.com/application/contracts/store"
 "example.com/application/interfaces/run"
)
type Service struct{}
//plystra:implements app.run/v1
func New(database, Database store.Resource) (*Service, error) { panic("constructor executed") }
func (*Service) Run(context.Context, run.Request) (run.Response, error) { return run.Response{}, nil }
`,
	}
	for name, content := range files {
		writeResolutionFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	t.Cleanup(func() {
		var actual []string
		err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(relative)
			if original, found := files[key]; !found || original != string(content) {
				t.Errorf("discovery or resolution mutated %s", key)
			}
			actual = append(actual, key)
			return nil
		})
		if err != nil || len(actual) != len(files) {
			t.Errorf("fixture changed: %v", err)
		}
	})
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	modules, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), project, modules, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	return interfaceresolution.Input{
		Interfaces: discovery.Interfaces(), Implementations: discovery.Implementations(), ResourceProviders: discovery.ResourceProviders(),
		Requirements: []interfaceresolution.Requirement{resolutionRequirement(mustResolutionID(t, "app.run/v1"), "interfaces.require", interfaceresolution.RequirementDeclaration)},
	}
}
