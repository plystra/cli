package constructorgraph

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestBuildKeepsMissingOptionalDependencyUnavailable(t *testing.T) {
	t.Parallel()

	constructors := []normalizedConstructor{
		testConstructor("example.com/app/orders.New", "example.com/app@local/orders/new.go:8:6", []string{"orders.run/v1"},
			testDependency("storage.read/v1", "example.com/contracts/storage", "storage", 1, false),
			testDependency("audit.write/v1", "example.com/contracts/audit", "audit", 2, true),
		),
		testConstructor("example.com/app/storage.New", "example.com/app@local/storage/new.go:5:6", []string{"storage.read/v1"}),
	}
	requirements := []Requirement{
		{InterfaceID: testID("orders.run/v1"), Source: "plystra.yaml:interfaces.require[orders.run/v1]"},
		{InterfaceID: testID("orders.run/v1"), Source: "http.expose[orders.run/v1]"},
		{InterfaceID: testID("orders.run/v1"), Source: "http.expose[orders.run/v1]"},
	}
	selections := []Selection{
		testSelection("storage.read/v1", "example.com/app/storage.New", SelectionUnique, "example.com/app@local/storage/new.go:5:6"),
		testSelection("orders.run/v1", "example.com/app/orders.New", SelectionExplicit, "plystra.yaml:interfaces.use[orders.run/v1]"),
	}

	graph, err := build(constructors, requirements, selections)
	if err != nil {
		t.Fatal(err)
	}
	roots := graph.Roots()
	if len(roots) != 1 || roots[0].InterfaceID().String() != "orders.run/v1" || !slices.Equal(roots[0].Sources(), []string{"http.expose[orders.run/v1]", "plystra.yaml:interfaces.require[orders.run/v1]"}) {
		t.Fatalf("Roots = %#v, sources %v", roots, roots[0].Sources())
	}
	if got := bindingSummaries(graph.Bindings()); !slices.Equal(got, []string{
		"orders.run/v1=example.com/app/orders.New:explicit",
		"storage.read/v1=example.com/app/storage.New:unique-compatible",
	}) {
		t.Fatalf("Bindings = %v", got)
	}
	nodes := graph.ConstructionOrder()
	if got := nodeSymbols(nodes); !slices.Equal(got, []string{"example.com/app/storage.New", "example.com/app/orders.New"}) {
		t.Fatalf("ConstructionOrder = %v", got)
	}
	dependencies := nodes[1].Dependencies()
	if len(dependencies) != 2 {
		t.Fatalf("orders dependencies = %#v", dependencies)
	}
	if dependencies[0].InterfaceID().String() != "storage.read/v1" || dependencies[0].Optional() || !dependencies[0].Available() || dependencies[0].Constructor().String() != "example.com/app/storage.New" || dependencies[0].ParameterPosition() != 1 || dependencies[0].ParameterName() != "storage" {
		t.Fatalf("required dependency = %#v", dependencies[0])
	}
	if dependencies[1].InterfaceID().String() != "audit.write/v1" || !dependencies[1].Optional() || dependencies[1].Available() || dependencies[1].Constructor().String() != "" || dependencies[1].ParameterPosition() != 2 || dependencies[1].ParameterName() != "audit" {
		t.Fatalf("optional dependency = %#v", dependencies[1])
	}
	for _, binding := range graph.Bindings() {
		if binding.InterfaceID().String() == "audit.write/v1" {
			t.Fatal("missing optional Interface became a binding")
		}
	}

	roots[0].sources[0] = "changed"
	bindings := graph.Bindings()
	bindings[0].sources[0] = "changed"
	nodes[1].dependencies[0] = Dependency{}
	if graph.Roots()[0].Sources()[0] != "http.expose[orders.run/v1]" || graph.Bindings()[0].Sources()[0] != "plystra.yaml:interfaces.use[orders.run/v1]" || graph.ConstructionOrder()[1].Dependencies()[0].InterfaceID().String() != "storage.read/v1" {
		t.Fatal("Graph exposed mutable storage")
	}
}

func TestBuildIncludesAvailableOptionalDependency(t *testing.T) {
	t.Parallel()

	constructors := []normalizedConstructor{
		testConstructor("example.com/app/orders.New", "app@local/orders.go:4:6", []string{"orders.run/v1"},
			testDependency("audit.write/v1", "example.com/contracts/audit", "audit", 1, true),
		),
		testConstructor("example.com/app/audit.New", "app@local/audit.go:4:6", []string{"audit.write/v1"}),
	}
	graph, err := build(constructors, []Requirement{{InterfaceID: testID("orders.run/v1"), Source: "root"}}, []Selection{
		testSelection("orders.run/v1", "example.com/app/orders.New", SelectionUnique, "orders"),
		testSelection("audit.write/v1", "example.com/app/audit.New", SelectionUnique, "audit"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeSymbols(graph.ConstructionOrder()); !slices.Equal(got, []string{"example.com/app/audit.New", "example.com/app/orders.New"}) {
		t.Fatalf("ConstructionOrder = %v", got)
	}
	dependency := graph.ConstructionOrder()[1].Dependencies()[0]
	if !dependency.Optional() || !dependency.Available() || dependency.Constructor().String() != "example.com/app/audit.New" {
		t.Fatalf("available optional dependency = %#v", dependency)
	}
}

func TestBuildReportsCompleteMissingRequiredPath(t *testing.T) {
	t.Parallel()

	constructors := []normalizedConstructor{
		testConstructor("example.com/app/orders.New", "app@local/orders.go:4:6", []string{"orders.run/v1"},
			testDependency("storage.read/v1", "example.com/contracts/storage", "storage", 1, false),
		),
	}
	_, err := build(constructors, []Requirement{{InterfaceID: testID("orders.run/v1"), Source: "plystra.yaml:3:5"}}, []Selection{
		testSelection("orders.run/v1", "example.com/app/orders.New", SelectionUnique, "orders.go:3:1"),
	})
	if !errors.Is(err, ErrBuild) || !errors.Is(err, ErrMissingBinding) {
		t.Fatalf("build error = %v", err)
	}
	var missing *MissingBindingError
	if !errors.As(err, &missing) || missing.InterfaceID().String() != "storage.read/v1" || missing.Root().InterfaceID().String() != "orders.run/v1" || !slices.Equal(missing.Root().Sources(), []string{"plystra.yaml:3:5"}) {
		t.Fatalf("MissingBindingError = %#v", missing)
	}
	steps := missing.Steps()
	if len(steps) != 1 || steps[0].RequiringConstructor().String() != "example.com/app/orders.New" || steps[0].RequiringSource() != "app@local/orders.go:4:6" || steps[0].InterfaceID().String() != "storage.read/v1" || steps[0].ParameterPosition() != 1 || steps[0].ParameterName() != "storage" || steps[0].Optional() || steps[0].SelectedConstructor().String() != "" {
		t.Fatalf("missing path = %#v", steps)
	}
	for _, fragment := range []string{"orders.run/v1", "plystra.yaml:3:5", "example.com/app/orders.New", "storage.read/v1", "parameter 1", "select one compatible visible Implementation"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("missing error omits %q: %v", fragment, err)
		}
	}
	steps[0].selectionSources = []string{"changed"}
	if len(missing.Steps()[0].SelectionSources()) != 0 {
		t.Fatal("MissingBindingError exposed mutable path")
	}
}

func TestBuildReportsCompleteDeterministicCycle(t *testing.T) {
	t.Parallel()

	constructors := []normalizedConstructor{
		testConstructor("example.com/app/b.New", "app@local/b.go:8:6", []string{"beta.run/v1"},
			testDependency("alpha.run/v1", "example.com/contracts/alpha", "alpha", 1, false),
		),
		testConstructor("example.com/app/a.New", "app@local/a.go:8:6", []string{"alpha.run/v1"},
			testDependency("beta.run/v1", "example.com/contracts/beta", "beta", 1, false),
		),
	}
	selections := []Selection{
		testSelection("beta.run/v1", "example.com/app/b.New", SelectionUnique, "b.go:6:1"),
		testSelection("alpha.run/v1", "example.com/app/a.New", SelectionExplicit, "plystra.yaml:4:5"),
	}
	requirements := []Requirement{{InterfaceID: testID("alpha.run/v1"), Source: "root"}}
	_, firstErr := build(constructors, requirements, selections)
	slices.Reverse(constructors)
	slices.Reverse(selections)
	_, secondErr := build(constructors, requirements, selections)
	if !errors.Is(firstErr, ErrBuild) || !errors.Is(firstErr, ErrCycle) || firstErr.Error() != secondErr.Error() {
		t.Fatalf("cycle errors:\nfirst:  %v\nsecond: %v", firstErr, secondErr)
	}
	var cycle *CycleError
	if !errors.As(firstErr, &cycle) {
		t.Fatalf("cycle error type = %T", firstErr)
	}
	steps := cycle.Steps()
	if len(steps) != 2 || steps[0].RequiringConstructor().String() != "example.com/app/a.New" || steps[0].InterfaceID().String() != "beta.run/v1" || steps[0].SelectedConstructor().String() != "example.com/app/b.New" || steps[0].SelectionReason() != SelectionUnique || steps[1].RequiringConstructor().String() != "example.com/app/b.New" || steps[1].InterfaceID().String() != "alpha.run/v1" || steps[1].SelectedConstructor().String() != "example.com/app/a.New" || steps[1].SelectionReason() != SelectionExplicit {
		t.Fatalf("cycle steps = %#v", steps)
	}
	for _, fragment := range []string{"example.com/app/a.New", "app@local/a.go:8:6", "beta.run/v1", "example.com/app/b.New", "app@local/b.go:8:6", "alpha.run/v1", "unique-compatible", "explicit", "acyclic compatible Implementation"} {
		if !strings.Contains(firstErr.Error(), fragment) {
			t.Fatalf("cycle error omits %q: %v", fragment, firstErr)
		}
	}
	steps[0].selectionSources[0] = "changed"
	if cycle.Steps()[0].SelectionSources()[0] != "b.go:6:1" {
		t.Fatal("CycleError exposed mutable steps")
	}
}

func TestBuildSharesOneConstructorAcrossSeveralBindings(t *testing.T) {
	t.Parallel()

	constructor := testConstructor("example.com/app/service.New", "app@local/service.go:5:6", []string{"alpha.run/v1", "beta.run/v1"})
	graph, err := build([]normalizedConstructor{constructor}, []Requirement{
		{InterfaceID: testID("beta.run/v1"), Source: "beta root"},
		{InterfaceID: testID("alpha.run/v1"), Source: "alpha root"},
	}, []Selection{
		testSelection("beta.run/v1", "example.com/app/service.New", SelectionUnique, "service.go"),
		testSelection("alpha.run/v1", "example.com/app/service.New", SelectionUnique, "service.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Bindings()) != 2 || len(graph.ConstructionOrder()) != 1 || graph.ConstructionOrder()[0].Symbol().String() != "example.com/app/service.New" {
		t.Fatalf("Graph = bindings %#v construction %#v", graph.Bindings(), graph.ConstructionOrder())
	}
}

func TestBuildRejectsInvalidSelectionInput(t *testing.T) {
	t.Parallel()

	constructors := []normalizedConstructor{testConstructor("example.com/app/service.New", "app@local/service.go:5:6", []string{"alpha.run/v1"})}
	tests := []struct {
		name      string
		selection []Selection
		want      string
	}{
		{name: "invisible constructor", selection: []Selection{testSelection("alpha.run/v1", "example.com/app/missing.New", SelectionUnique, "source")}, want: "invisible constructor"},
		{name: "undeclared Interface", selection: []Selection{testSelection("beta.run/v1", "example.com/app/service.New", SelectionUnique, "source")}, want: "does not declare"},
		{name: "invalid reason", selection: []Selection{{InterfaceID: testID("alpha.run/v1"), Constructor: testSymbol("example.com/app/service.New"), Reason: "priority", Sources: []string{"source"}}}, want: "invalid reason"},
		{name: "invalid source", selection: []Selection{{InterfaceID: testID("alpha.run/v1"), Constructor: testSymbol("example.com/app/service.New"), Reason: SelectionUnique, Sources: []string{"bad\nsource"}}}, want: "single-line"},
		{name: "conflict", selection: []Selection{
			testSelection("alpha.run/v1", "example.com/app/service.New", SelectionUnique, "first"),
			{InterfaceID: testID("alpha.run/v1"), Constructor: testSymbol("example.com/app/service.New"), Reason: SelectionExplicit, Sources: []string{"second"}},
		}, want: "conflicting"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := build(constructors, nil, test.selection)
			if !errors.Is(err, ErrBuild) || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("build error = %v", err)
			}
		})
	}
}

func testConstructor(symbol, source string, interfaces []string, dependencies ...normalizedDependency) normalizedConstructor {
	implemented := make(map[string]struct{}, len(interfaces))
	for _, identifier := range interfaces {
		implemented[testID(identifier).String()] = struct{}{}
	}
	return normalizedConstructor{
		symbol:       testSymbol(symbol),
		source:       source,
		implements:   implemented,
		dependencies: append([]normalizedDependency(nil), dependencies...),
	}
}

func testDependency(identifier, packagePath, parameterName string, parameterPosition int, optional bool) normalizedDependency {
	return normalizedDependency{
		interfaceID:       testID(identifier),
		packagePath:       packagePath,
		parameterName:     parameterName,
		parameterPosition: parameterPosition,
		optional:          optional,
	}
}

func testSelection(identifier, constructor string, reason SelectionReason, sources ...string) Selection {
	return Selection{
		InterfaceID: testID(identifier),
		Constructor: testSymbol(constructor),
		Reason:      reason,
		Sources:     append([]string(nil), sources...),
	}
}

func testID(value string) interfaceid.Identifier {
	identifier, err := interfaceid.Parse(value)
	if err != nil {
		panic(err)
	}
	return identifier
}

func testSymbol(value string) constructorsymbol.Symbol {
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		panic(err)
	}
	return symbol
}

func bindingSummaries(bindings []Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = binding.InterfaceID().String() + "=" + binding.Constructor().String() + ":" + string(binding.Reason())
	}
	return result
}

func nodeSymbols(nodes []Node) []string {
	result := make([]string, len(nodes))
	for index, node := range nodes {
		result[index] = node.Symbol().String()
	}
	return result
}
