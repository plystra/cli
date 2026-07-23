package applicationresolve_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func TestResolveBuildsSelectedInterfaceConstructorGraphFromConfiguration(t *testing.T) {
	t.Parallel()

	root := writeResolvedInterfaceProject(t)
	before := snapshotTree(t, filepath.Dir(root))
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOFLAGS": "-mod=readonly",
	})
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       filepath.Join(root, "app"),
		Environment: environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := resolved.InterfaceResolution().Graph()
	if got := resolvedGraphNodes(graph); !reflect.DeepEqual(got, []string{
		"example.com/interface-app/auditone.New",
		"example.com/interface-app/app.New",
	}) {
		t.Fatalf("default construction order = %v", got)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/auditone.New:explicit",
	}) {
		t.Fatalf("default selections = %v", got)
	}
	app := graph.ConstructionOrder()[1]
	dependencies := app.Dependencies()
	if len(dependencies) != 2 || dependencies[0].InterfaceID().String() != "audit.write/v1" || dependencies[0].Optional() || !dependencies[0].Available() || dependencies[0].Constructor().String() != "example.com/interface-app/auditone.New" || dependencies[1].InterfaceID().String() != "cache.read/v1" || !dependencies[1].Optional() || dependencies[1].Available() {
		t.Fatalf("default app dependencies = %#v", dependencies)
	}
	if roots := graph.Roots(); len(roots) != 1 || roots[0].InterfaceID().String() != "app.run/v1" || len(roots[0].Sources()) != 1 || roots[0].Sources()[0] != `plystra.yaml interfaces.require["app.run/v1"]` {
		t.Fatalf("default roots = %#v", roots)
	}

	production, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           filepath.Join(root, "app"),
		EnvironmentName: "production",
		Environment:     environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	productionGraph := production.InterfaceResolution().Graph()
	if got := resolvedGraphNodes(productionGraph); !reflect.DeepEqual(got, []string{
		"example.com/interface-app/audittwo.New",
		"example.com/interface-app/cache.New",
		"example.com/interface-app/app.New",
	}) {
		t.Fatalf("production construction order = %v", got)
	}
	if got := resolvedSelectionSummaries(production.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/audittwo.New:explicit",
		"cache.read/v1=example.com/interface-app/cache.New:unique-compatible",
	}) {
		t.Fatalf("production selections = %v", got)
	}
	productionDependencies := productionGraph.ConstructionOrder()[2].Dependencies()
	if !productionDependencies[1].Optional() || !productionDependencies[1].Available() || productionDependencies[1].Constructor().String() != "example.com/interface-app/cache.New" {
		t.Fatalf("production optional cache = %#v", productionDependencies[1])
	}
	if selection := production.ConfigurationSelection(); selection.Mode() != "environment" || selection.Environment() != "production" || selection.Path() != "plystra.production.yaml" {
		t.Fatalf("production selection = mode %q environment %q path %q", selection.Mode(), selection.Environment(), selection.Path())
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatalf("Interface application resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsMissingInterfaceImplementationWithCompletePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/missing-interface")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	writeResolvedInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeResolvedInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/missing-interface/interfaces/app/run/v1"
	writev1 "example.com/missing-interface/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(audit writev1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrMissingBinding) {
		t.Fatalf("Resolve error = %v", err)
	}
	var missing *constructorgraph.MissingBindingError
	if !errors.As(err, &missing) || missing.InterfaceID().String() != "audit.write/v1" || missing.Root().InterfaceID().String() != "app.run/v1" || len(missing.Steps()) != 1 || missing.Steps()[0].RequiringConstructor().String() != "example.com/missing-interface/app.New" || missing.Steps()[0].RequiringSource() == "" || missing.Steps()[0].ParameterName() != "audit" || !containsResolutionFragments(err.Error(), "plystra.yaml", "example.com/missing-interface/app.New", "audit.write/v1", "before generation") {
		t.Fatalf("missing path/error = %#v / %v", missing, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsSelectedConstructorCycleBeforeLegacyResolution(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/cyclic-interface")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [cycle.a/v1]}\n")
	writeResolvedInterface(t, root, "cycle/a/v1", "av1", "cycle.a/v1", "A")
	writeResolvedInterface(t, root, "cycle/b/v1", "bv1", "cycle.b/v1", "B")
	writeFile(t, filepath.Join(root, "cyclea", "service.go"), `package cyclea

import (
	"context"

	av1 "example.com/cyclic-interface/interfaces/cycle/a/v1"
	bv1 "example.com/cyclic-interface/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.a/v1
func New(b bv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) A(context.Context, av1.Request) (av1.Response, error) { return av1.Response{}, nil }
`)
	writeFile(t, filepath.Join(root, "cycleb", "service.go"), `package cycleb

import (
	"context"

	av1 "example.com/cyclic-interface/interfaces/cycle/a/v1"
	bv1 "example.com/cyclic-interface/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.b/v1
func New(a av1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) B(context.Context, bv1.Request) (bv1.Response, error) { return bv1.Response{}, nil }
`)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, constructorgraph.ErrCycle) {
		t.Fatalf("Resolve error = %v", err)
	}
	var cycle *constructorgraph.CycleError
	if !errors.As(err, &cycle) || len(cycle.Steps()) != 2 || !containsResolutionFragments(err.Error(), "cycle.a/v1", "cycle.b/v1", "cyclea.New", "cycleb.New", "unique-compatible", "correction") {
		t.Fatalf("cycle/error = %#v / %v", cycle, err)
	}
}

func writeResolvedInterfaceProject(t testing.TB) string {
	t.Helper()
	parent := t.TempDir()
	kernelRoot := filepath.Join(parent, "kernel")
	writeModule(t, kernelRoot, "github.com/plystra/kernel")
	writeFile(t, filepath.Join(kernelRoot, "optional.go"), "package plystra\n\ntype Optional[T any] struct{}\n")

	root := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/interface-app\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ../kernel\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [app.run/v1]
  use:
    audit.write/v1: example.com/interface-app/auditone.New
`)
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), `interfaces:
  require: [cache.read/v1]
  use:
    audit.write/v1: example.com/interface-app/audittwo.New
`)
	writeResolvedInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeResolvedInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeResolvedInterface(t, root, "cache/read/v1", "readv1", "cache.read/v1", "Read")
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	plystra "github.com/plystra/kernel"
	runv1 "example.com/interface-app/interfaces/app/run/v1"
	writev1 "example.com/interface-app/interfaces/audit/write/v1"
	readv1 "example.com/interface-app/interfaces/cache/read/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(audit writev1.Interface, cache plystra.Optional[readv1.Interface]) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeResolvedSimpleImplementation(t, root, "auditone", "audit.write/v1", "audit/write/v1", "Write")
	writeResolvedSimpleImplementation(t, root, "audittwo", "audit.write/v1", "audit/write/v1", "Write")
	writeResolvedSimpleImplementation(t, root, "cache", "cache.read/v1", "cache/read/v1", "Read")
	return root
}

func writeResolvedInterface(t testing.TB, root, relative, packageName, identifier, method string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`, packageName, identifier, method))
}

func writeResolvedSimpleImplementation(t testing.TB, root, packageName, identifier, interfacePath, method string) {
	t.Helper()
	writeFile(t, filepath.Join(root, packageName, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/interface-app/interfaces/%s"
)

type Service struct{}

//plystra:implements %s
func New() (*Service, error) { return &Service{}, nil }

func (*Service) %s(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, packageName, interfacePath, identifier, method))
}

func resolvedGraphNodes(graph constructorgraph.Graph) []string {
	nodes := graph.ConstructionOrder()
	result := make([]string, len(nodes))
	for index, node := range nodes {
		result[index] = node.Symbol().String()
	}
	return result
}

func resolvedSelectionSummaries(result interfaceresolution.Result) []string {
	selections := result.Selections()
	values := make([]string, len(selections))
	for index, selection := range selections {
		values[index] = fmt.Sprintf("%s=%s:%s", selection.InterfaceID, selection.Constructor, selection.Reason)
	}
	return values
}

func containsResolutionFragments(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
