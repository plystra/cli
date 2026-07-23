package interfaceresolution_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestResolveSelectsDeterministicReachableConstructorClosure(t *testing.T) {
	t.Parallel()

	fixture := discoverResolutionFixture(t)
	runID := mustResolutionID(t, "app.run/v1")
	auditID := mustResolutionID(t, "audit.write/v1")
	cacheID := mustResolutionID(t, "cache.read/v1")
	runConstructor := mustResolutionSymbol(t, "example.com/application/app.New")
	auditConstructor := mustResolutionSymbol(t, "example.com/application/audit.New")
	cacheConstructor := mustResolutionSymbol(t, "example.com/application/cache.New")

	input := interfaceresolution.Input{
		Interfaces:      fixture.Interfaces(),
		Implementations: fixture.Implementations(),
		Requirements: []interfaceresolution.Requirement{
			{InterfaceID: runID, Source: "example.com/application@local/plystra.yaml interfaces.require[app.run/v1]"},
			{InterfaceID: runID, Source: "example.com/application@local/plystra.yaml http.expose[app.run/v1]"},
		},
	}
	result, err := interfaceresolution.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	assertSelectionSummary(t, result.Selections(), []string{
		"app.run/v1=example.com/application/app.New:unique-compatible",
		"audit.write/v1=example.com/application/audit.New:unique-compatible",
	})
	bindings := result.Graph().Bindings()
	if got := bindingSummaries(bindings); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/application/app.New:unique-compatible",
		"audit.write/v1=example.com/application/audit.New:unique-compatible",
	}) {
		t.Fatalf("Bindings = %v", got)
	}
	roots := result.Graph().Roots()
	if len(roots) != 1 || roots[0].InterfaceID() != runID || !reflect.DeepEqual(roots[0].Sources(), []string{
		"example.com/application@local/plystra.yaml http.expose[app.run/v1]",
		"example.com/application@local/plystra.yaml interfaces.require[app.run/v1]",
	}) {
		t.Fatalf("Roots = %#v", roots)
	}
	nodes := result.Graph().ConstructionOrder()
	if got := nodeSymbols(nodes); !reflect.DeepEqual(got, []string{auditConstructor.String(), runConstructor.String()}) {
		t.Fatalf("ConstructionOrder = %v", got)
	}
	runNode := findNode(t, nodes, runConstructor)
	dependencies := runNode.Dependencies()
	if len(dependencies) != 2 || dependencies[0].InterfaceID() != auditID || dependencies[0].Optional() || !dependencies[0].Available() || dependencies[0].Constructor() != auditConstructor || dependencies[1].InterfaceID() != cacheID || !dependencies[1].Optional() || dependencies[1].Available() || dependencies[1].Constructor().String() != "" {
		t.Fatalf("app.New dependencies = %#v", dependencies)
	}

	withCache := input
	withCache.Requirements = append(append([]interfaceresolution.Requirement(nil), input.Requirements...), interfaceresolution.Requirement{
		InterfaceID: cacheID,
		Source:      "example.com/application@local/plystra.yaml interfaces.require[cache.read/v1]",
	})
	cacheResult, err := interfaceresolution.Resolve(withCache)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodeSymbols(cacheResult.Graph().ConstructionOrder()); !reflect.DeepEqual(got, []string{auditConstructor.String(), cacheConstructor.String(), runConstructor.String()}) {
		t.Fatalf("ConstructionOrder with cache = %v", got)
	}
	cacheDependency := findNode(t, cacheResult.Graph().ConstructionOrder(), runConstructor).Dependencies()[1]
	if !cacheDependency.Optional() || !cacheDependency.Available() || cacheDependency.Constructor() != cacheConstructor {
		t.Fatalf("available cache dependency = %#v", cacheDependency)
	}

	firstSelections := result.Selections()
	firstSelections[0].Sources[0] = "mutated"
	if repeated := result.Selections(); repeated[0].Sources[0] == "mutated" {
		t.Fatal("Selections returned aliased source storage")
	}
	repeated, err := interfaceresolution.Resolve(input)
	if err != nil || !reflect.DeepEqual(selectionSummaries(result.Selections()), selectionSummaries(repeated.Selections())) || !reflect.DeepEqual(nodeSymbols(result.Graph().ConstructionOrder()), nodeSymbols(repeated.Graph().ConstructionOrder())) {
		t.Fatalf("repeated resolution = %#v, %v", repeated, err)
	}
}

func TestResolveAppliesExplicitChoiceBeforeAmbiguity(t *testing.T) {
	t.Parallel()

	fixture := discoverResolutionFixture(t)
	emailID := mustResolutionID(t, "email.send/v1")
	one := mustResolutionSymbol(t, "example.com/application/emailone.New")
	two := mustResolutionSymbol(t, "example.com/application/emailtwo.New")
	base := interfaceresolution.Input{
		Interfaces:      fixture.Interfaces(),
		Implementations: fixture.Implementations(),
		Requirements: []interfaceresolution.Requirement{{
			InterfaceID: emailID,
			Source:      "example.com/application@local/plystra.yaml interfaces.require[email.send/v1]",
		}},
	}

	_, err := interfaceresolution.Resolve(base)
	if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, interfaceresolution.ErrAmbiguousImplementation) {
		t.Fatalf("ambiguous Resolve error = %v", err)
	}
	var ambiguous *interfaceresolution.AmbiguousImplementationError
	if !errors.As(err, &ambiguous) || ambiguous.InterfaceID() != emailID {
		t.Fatalf("ambiguous error = %#v", ambiguous)
	}
	candidates := ambiguous.Candidates()
	if len(candidates) != 2 || candidates[0].Constructor() != one || candidates[1].Constructor() != two || candidates[0].Source() == "" || candidates[1].Source() == "" || !containsAll(err.Error(), one.String(), two.String(), "interfaces.use") {
		t.Fatalf("ambiguous candidates/error = %#v / %v", candidates, err)
	}
	candidates[0] = interfaceresolution.Candidate{}
	if ambiguous.Candidates()[0].Constructor() != one {
		t.Fatal("AmbiguousImplementationError returned aliased candidates")
	}

	base.Choices = []interfaceresolution.Choice{
		{InterfaceID: emailID, Constructor: two, Sources: []string{"z-source", "a-source"}},
		{InterfaceID: emailID, Constructor: two, Sources: []string{"a-source"}},
	}
	result, err := interfaceresolution.Resolve(base)
	if err != nil {
		t.Fatal(err)
	}
	selections := result.Selections()
	if len(selections) != 1 || selections[0].InterfaceID != emailID || selections[0].Constructor != two || selections[0].Reason != constructorgraph.SelectionExplicit || !reflect.DeepEqual(selections[0].Sources, []string{"a-source", "z-source"}) {
		t.Fatalf("explicit selections = %#v", selections)
	}
	bindings := result.Graph().Bindings()
	if len(bindings) != 1 || bindings[0].Constructor() != two || bindings[0].Reason() != constructorgraph.SelectionExplicit || !reflect.DeepEqual(bindings[0].Sources(), []string{"a-source", "z-source"}) {
		t.Fatalf("explicit bindings = %#v", bindings)
	}
}

func TestResolveReportsCompleteMissingPathAndConstructorCycle(t *testing.T) {
	t.Parallel()

	fixture := discoverResolutionFixture(t)

	t.Run("missing required", func(t *testing.T) {
		jobID := mustResolutionID(t, "job.run/v1")
		missingID := mustResolutionID(t, "missing.need/v1")
		jobConstructor := mustResolutionSymbol(t, "example.com/application/job.New")
		_, err := interfaceresolution.Resolve(interfaceresolution.Input{
			Interfaces:      fixture.Interfaces(),
			Implementations: fixture.Implementations(),
			Requirements: []interfaceresolution.Requirement{{
				InterfaceID: jobID,
				Source:      "example.com/application@local/plystra.yaml interfaces.require[job.run/v1]",
			}},
		})
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrBuild) || !errors.Is(err, constructorgraph.ErrMissingBinding) {
			t.Fatalf("missing Resolve error = %v", err)
		}
		var missing *constructorgraph.MissingBindingError
		if !errors.As(err, &missing) || missing.InterfaceID() != missingID || missing.Root().InterfaceID() != jobID {
			t.Fatalf("missing binding = %#v", missing)
		}
		steps := missing.Steps()
		if len(steps) != 1 || steps[0].RequiringConstructor() != jobConstructor || steps[0].InterfaceID() != missingID || steps[0].ParameterPosition() != 1 || steps[0].ParameterName() != "dependency" || steps[0].SelectedConstructor().String() != "" || !containsAll(err.Error(), jobConstructor.String(), "missing.need/v1", "parameter 1", "before generation") {
			t.Fatalf("missing path/error = %#v / %v", steps, err)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		cycleAID := mustResolutionID(t, "cycle.a/v1")
		cycleBID := mustResolutionID(t, "cycle.b/v1")
		cycleA := mustResolutionSymbol(t, "example.com/application/cyclea.New")
		cycleB := mustResolutionSymbol(t, "example.com/application/cycleb.New")
		_, err := interfaceresolution.Resolve(interfaceresolution.Input{
			Interfaces:      fixture.Interfaces(),
			Implementations: fixture.Implementations(),
			Requirements: []interfaceresolution.Requirement{{
				InterfaceID: cycleAID,
				Source:      "example.com/application@local/plystra.yaml interfaces.require[cycle.a/v1]",
			}},
		})
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, constructorgraph.ErrCycle) {
			t.Fatalf("cycle Resolve error = %v", err)
		}
		var cycle *constructorgraph.CycleError
		if !errors.As(err, &cycle) {
			t.Fatalf("cycle error = %v", err)
		}
		steps := cycle.Steps()
		if len(steps) != 2 || steps[0].RequiringConstructor() != cycleA || steps[0].InterfaceID() != cycleBID || steps[0].SelectedConstructor() != cycleB || steps[0].SelectionReason() != constructorgraph.SelectionUnique || steps[1].RequiringConstructor() != cycleB || steps[1].InterfaceID() != cycleAID || steps[1].SelectedConstructor() != cycleA || steps[1].SelectionReason() != constructorgraph.SelectionUnique || !containsAll(err.Error(), cycleA.String(), cycleB.String(), "cycle.a/v1", "cycle.b/v1", "unique-compatible") {
			t.Fatalf("cycle path/error = %#v / %v", steps, err)
		}
	})
}

func TestResolveValidatesEveryExplicitChoiceWithoutMakingItARoot(t *testing.T) {
	t.Parallel()

	fixture := discoverResolutionFixture(t)
	auditID := mustResolutionID(t, "audit.write/v1")
	runConstructor := mustResolutionSymbol(t, "example.com/application/app.New")
	auditConstructor := mustResolutionSymbol(t, "example.com/application/audit.New")

	result, err := interfaceresolution.Resolve(interfaceresolution.Input{
		Interfaces:      fixture.Interfaces(),
		Implementations: fixture.Implementations(),
		Choices: []interfaceresolution.Choice{{
			InterfaceID: auditID,
			Constructor: auditConstructor,
			Sources:     []string{"plystra.yaml interfaces.use[audit.write/v1]"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Selections()) != 0 || len(result.Graph().Roots()) != 0 || len(result.Graph().Bindings()) != 0 || len(result.Graph().ConstructionOrder()) != 0 {
		t.Fatalf("unused explicit choice created a root: %#v", result)
	}

	_, err = interfaceresolution.Resolve(interfaceresolution.Input{
		Interfaces:      fixture.Interfaces(),
		Implementations: fixture.Implementations(),
		Choices: []interfaceresolution.Choice{{
			InterfaceID: auditID,
			Constructor: runConstructor,
			Sources:     []string{"plystra.yaml interfaces.use[audit.write/v1]"},
		}},
	})
	if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, interfaceresolution.ErrIncompatibleChoice) || !containsAll(err.Error(), auditID.String(), runConstructor.String()) {
		t.Fatalf("incompatible choice error = %v", err)
	}
}

func discoverResolutionFixture(t testing.TB) interfaceinventory.Discovery {
	t.Helper()
	parent := t.TempDir()
	kernelRoot := filepath.Join(parent, "kernel")
	writeResolutionFile(t, filepath.Join(kernelRoot, "go.mod"), "module github.com/plystra/kernel\n\ngo 1.26\n")
	writeResolutionFile(t, filepath.Join(kernelRoot, "optional.go"), "package plystra\n\ntype Optional[T any] struct{}\n")

	root := filepath.Join(parent, "application")
	writeResolutionFile(t, filepath.Join(root, "go.mod"), "module example.com/application\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ../kernel\n")
	writeResolutionFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	interfaces := []struct {
		path        string
		packageName string
		id          string
		method      string
	}{
		{"app/run/v1", "runv1", "app.run/v1", "Run"},
		{"audit/write/v1", "writev1", "audit.write/v1", "Write"},
		{"cache/read/v1", "readv1", "cache.read/v1", "Read"},
		{"email/send/v1", "sendv1", "email.send/v1", "Send"},
		{"job/run/v1", "jobv1", "job.run/v1", "Run"},
		{"missing/need/v1", "needv1", "missing.need/v1", "Need"},
		{"cycle/a/v1", "av1", "cycle.a/v1", "A"},
		{"cycle/b/v1", "bv1", "cycle.b/v1", "B"},
	}
	for _, definition := range interfaces {
		writeResolutionFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(definition.path), "interface.go"), resolutionInterfaceSource(definition.packageName, definition.id, definition.method))
	}
	writeResolutionFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	plystra "github.com/plystra/kernel"
	runv1 "example.com/application/interfaces/app/run/v1"
	writev1 "example.com/application/interfaces/audit/write/v1"
	readv1 "example.com/application/interfaces/cache/read/v1"
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
	writeSimpleImplementation(t, root, "audit", "audit.write/v1", "audit/write/v1", "Write")
	writeSimpleImplementation(t, root, "cache", "cache.read/v1", "cache/read/v1", "Read")
	writeSimpleImplementation(t, root, "emailone", "email.send/v1", "email/send/v1", "Send")
	writeSimpleImplementation(t, root, "emailtwo", "email.send/v1", "email/send/v1", "Send")
	writeResolutionFile(t, filepath.Join(root, "job", "service.go"), `package job

import (
	"context"

	jobv1 "example.com/application/interfaces/job/run/v1"
	needv1 "example.com/application/interfaces/missing/need/v1"
)

type Service struct{}

//plystra:implements job.run/v1
func New(dependency needv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, jobv1.Request) (jobv1.Response, error) {
	return jobv1.Response{}, nil
}
`)
	writeResolutionFile(t, filepath.Join(root, "cyclea", "service.go"), `package cyclea

import (
	"context"

	av1 "example.com/application/interfaces/cycle/a/v1"
	bv1 "example.com/application/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.a/v1
func New(dependency bv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) A(context.Context, av1.Request) (av1.Response, error) {
	return av1.Response{}, nil
}
`)
	writeResolutionFile(t, filepath.Join(root, "cycleb", "service.go"), `package cycleb

import (
	"context"

	av1 "example.com/application/interfaces/cycle/a/v1"
	bv1 "example.com/application/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.b/v1
func New(dependency av1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) B(context.Context, bv1.Request) (bv1.Response, error) {
	return bv1.Response{}, nil
}
`)

	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	dependencies, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), project, dependencies, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	return discovery
}

func resolutionInterfaceSource(packageName, identifier, method string) string {
	return fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`, packageName, identifier, method)
}

func writeSimpleImplementation(t testing.TB, root, packageName, identifier, interfacePath, method string) {
	t.Helper()
	writeResolutionFile(t, filepath.Join(root, packageName, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/application/interfaces/%s"
)

type Service struct{}

//plystra:implements %s
func New() (*Service, error) { return &Service{}, nil }

func (*Service) %s(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, packageName, interfacePath, identifier, method))
}

func mustResolutionID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	identifier, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func mustResolutionSymbol(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return symbol
}

func writeResolutionFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findNode(t testing.TB, nodes []constructorgraph.Node, symbol constructorsymbol.Symbol) constructorgraph.Node {
	t.Helper()
	for _, node := range nodes {
		if node.Symbol() == symbol {
			return node
		}
	}
	t.Fatalf("node %s is absent from %v", symbol, nodeSymbols(nodes))
	return constructorgraph.Node{}
}

func nodeSymbols(nodes []constructorgraph.Node) []string {
	result := make([]string, len(nodes))
	for index, node := range nodes {
		result[index] = node.Symbol().String()
	}
	return result
}

func selectionSummaries(selections []constructorgraph.Selection) []string {
	result := make([]string, len(selections))
	for index, selection := range selections {
		result[index] = fmt.Sprintf("%s=%s:%s:%v", selection.InterfaceID, selection.Constructor, selection.Reason, selection.Sources)
	}
	return result
}

func assertSelectionSummary(t testing.TB, selections []constructorgraph.Selection, want []string) {
	t.Helper()
	got := make([]string, len(selections))
	for index, selection := range selections {
		got[index] = fmt.Sprintf("%s=%s:%s", selection.InterfaceID, selection.Constructor, selection.Reason)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Selections = %v; want %v", got, want)
	}
}

func bindingSummaries(bindings []constructorgraph.Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = fmt.Sprintf("%s=%s:%s", binding.InterfaceID(), binding.Constructor(), binding.Reason())
	}
	return result
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
