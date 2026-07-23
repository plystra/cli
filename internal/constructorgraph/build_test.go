package constructorgraph_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestBuildConsumesValidatedDiscoveredImplementationInventory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	kernelRoot := filepath.Join(parent, "kernel")
	writeGraphFile(t, filepath.Join(kernelRoot, "go.mod"), "module github.com/plystra/kernel\n\ngo 1.26\n")
	writeGraphFile(t, filepath.Join(kernelRoot, "optional.go"), "package plystra\n\ntype Optional[T any] struct{}\n")

	root := filepath.Join(parent, "app")
	writeGraphFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ../kernel\n")
	writeGraphFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeGraphFile(t, filepath.Join(root, "interfaces", "orders", "run", "v1", "interface.go"), graphInterfaceSource("runv1", "orders.run/v1", "Run"))
	writeGraphFile(t, filepath.Join(root, "interfaces", "audit", "write", "v1", "interface.go"), graphInterfaceSource("writev1", "audit.write/v1", "Write"))
	writeGraphFile(t, filepath.Join(root, "orders", "implementation.go"), `package orders

import (
	"context"

	plystra "github.com/plystra/kernel"
	auditv1 "example.com/app/interfaces/audit/write/v1"
	ordersv1 "example.com/app/interfaces/orders/run/v1"
)

type Service struct{}

//plystra:implements orders.run/v1
func New(audit plystra.Optional[auditv1.Interface]) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Run(context.Context, ordersv1.Request) (ordersv1.Response, error) {
	return ordersv1.Response{}, nil
}
`)

	before := snapshotGraphTree(t, parent)
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	dependencies, err := moduledependency.Discover(context.Background(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(context.Background(), project, dependencies, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	identifier := mustGraphID(t, "orders.run/v1")
	constructor := mustGraphSymbol(t, "example.com/app/orders.New")
	graph, err := constructorgraph.Build(constructorgraph.Input{
		Implementations: discovery.Implementations(),
		Requirements: []constructorgraph.Requirement{{
			InterfaceID: identifier,
			Source:      "example.com/app@local/plystra.yaml:1:1 interfaces.require[orders.run/v1]",
		}},
		Selections: []constructorgraph.Selection{{
			InterfaceID: identifier,
			Constructor: constructor,
			Reason:      constructorgraph.SelectionUnique,
			Sources:     []string{"example.com/app@local/orders/implementation.go:15:6"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes := graph.ConstructionOrder()
	if len(nodes) != 1 || nodes[0].Symbol() != constructor || nodes[0].Implementation().Symbol() != constructor || nodes[0].Source() == "" {
		t.Fatalf("ConstructionOrder = %#v", nodes)
	}
	dependenciesView := nodes[0].Dependencies()
	if len(dependenciesView) != 1 || dependenciesView[0].InterfaceID().String() != "audit.write/v1" || !dependenciesView[0].Optional() || dependenciesView[0].Available() {
		t.Fatalf("Dependencies = %#v", dependenciesView)
	}
	if after := snapshotGraphTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("graph discovery or build mutated sources:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func graphInterfaceSource(packageName, identifier, method string) string {
	return `package ` + packageName + `

import "context"

//plystra:interface ` + identifier + `
type Interface interface {
	` + method + `(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`
}

func mustGraphID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	identifier, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func mustGraphSymbol(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return symbol
}

func writeGraphFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type graphFileState struct {
	data string
	mode os.FileMode
}

func snapshotGraphTree(t testing.TB, root string) map[string]graphFileState {
	t.Helper()
	result := make(map[string]graphFileState)
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = graphFileState{data: string(data), mode: info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
