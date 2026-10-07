package constructorgraph_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestResourceGraphBuildsNamedInstancesAndSharedDependencies(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	graph, err := constructorgraph.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceNodeNames(graph.ResourceConstructionOrder()); !reflect.DeepEqual(got, []string{"database.primary", "database.secondary", "orphan", "view.first", "view.second"}) {
		t.Fatalf("Resource construction order = %v", got)
	}
	nodes := graph.ResourceConstructionOrder()
	if nodes[0].Provider().Symbol() != nodes[1].Provider().Symbol() || nodes[3].Provider().Symbol() != nodes[4].Provider().Symbol() {
		t.Fatal("fixture does not reuse exact providers")
	}
	for n, target := range []string{"database.primary", "database.secondary"} {
		dependencies := nodes[n+3].Dependencies()
		if len(dependencies) != 1 || dependencies[0].Namespace() != constructorgraph.ResourceConsumerInstance || dependencies[0].Consumer() != nodes[n+3].Name() || dependencies[0].InstanceName() != target || dependencies[0].ParameterName() != "upstream" || dependencies[0].ParameterPosition() != 2 || dependencies[0].ResourceID().String() != "data.raw/v1" || dependencies[0].Reason() != constructorgraph.SelectionExplicit || len(dependencies[0].Sources()) != 1 || len(dependencies[0].SelectionSources()) != 1 {
			t.Fatalf("instance dependency = %#v", dependencies)
		}
	}
	app := mustGraphSymbol(t, "example.com/app/service.New")
	dependencies := graph.ResourceDependencies(app)
	if len(dependencies) != 3 {
		t.Fatalf("Implementation Resource dependencies = %#v", dependencies)
	}
	for index, target := range []string{"database.primary", "database.secondary", "view.second"} {
		dependency := dependencies[index]
		if dependency.Namespace() != constructorgraph.ResourceConsumerImplementation || dependency.Consumer() != app.String() || dependency.Constructor() != app || dependency.ParameterPosition() != index+3 || dependency.InstanceName() != target || dependency.DeclarationSource().Path != "service/new.go" || dependency.DeclarationSource().Line <= 0 || dependency.PackagePath() == "" || dependency.Provider().String() == "" || len(dependency.SelectionSources()) == 0 {
			t.Fatalf("Implementation dependency = %#v", dependency)
		}
	}
	if dependencies[0].ParameterName() != "database" || dependencies[1].ParameterName() != "Database" {
		t.Fatal("repeated Resource parameters lost exact case or position")
	}
	if len(graph.ConstructionOrder()) != 2 || len(graph.Bindings()) != 3 || len(graph.Roots()) != 2 || len(graph.ConstructionOrder()[1].Dependencies()) != 1 {
		t.Fatal("Resources changed Interface roots, bindings, or one-constructor sharing")
	}
	audit := graph.ResourceDependencies(mustGraphSymbol(t, "example.com/app/audit.New"))
	if len(audit) != 1 || audit[0].InstanceName() != dependencies[0].InstanceName() {
		t.Fatal("consumers failed to share the same named instance")
	}
	if len(graph.ResourceDependencies(mustGraphSymbol(t, "example.com/app/dormant.New"))) != 0 {
		t.Fatal("dormant Implementation acquired executable dependencies")
	}

	before := resourceGraphSummary(graph)
	slices.Reverse(input.ResourceInstances)
	slices.Reverse(input.ResourceBindings)
	slices.Reverse(input.Requirements)
	slices.Reverse(input.Selections)
	reordered, err := constructorgraph.Build(input)
	if err != nil || !reflect.DeepEqual(before, resourceGraphSummary(reordered)) {
		t.Fatalf("input ordering changed graph: %v", err)
	}
	nodes[0].Sources()[0].Reference = "changed"
	nodes[3].Dependencies()[0].Sources()[0].Reference = "changed"
	nodes[0] = constructorgraph.ResourceNode{}
	dependencies[0].Sources()[0].Reference = "changed"
	dependencies[0].SelectionSources()[0].Reference = "changed"
	dependencies[0] = constructorgraph.ResourceDependency{}
	input.ResourceInstances[0].Sources[0].Reference = "changed"
	input.ResourceBindings[0].Sources[0].Reference = "changed"
	if !reflect.DeepEqual(before, resourceGraphSummary(graph)) {
		t.Fatal("graph exposes mutable source or dependency storage")
	}
}

func TestResourceGraphProjectsGeneratedAccessProviderAndBindsItsDatabase(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.GeneratedResources = []constructorgraph.GeneratedResourceInput{{
		MemberID: "example.records/v1", Name: "access.records", ResourceID: mustGraphID(t, "data.access/v1"),
		PackagePath: "example.com/app/access", TypeName: "Resource", Constructor: mustGraphSymbol(t, "example.com/app/generated/data/access/records.New"),
		DatabaseResourceID: mustGraphID(t, "data.raw/v1"), DatabasePackagePath: "example.com/app/contracts/raw",
		DatabaseParameter: "database", DatabaseParameterPosition: 1, Sources: []constructorgraph.ResourceSource{resourceSource("data.members.example.records")},
	}}
	input.ResourceBindings = append(input.ResourceBindings, resourceBinding(constructorgraph.ResourceConsumerInstance, "access.records", "database", "database.primary"))
	graph, err := constructorgraph.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	generated := graph.GeneratedResourceConstructionOrder()
	if len(generated) != 1 || generated[0].Name() != "access.records" || generated[0].MemberID() != "example.records/v1" || generated[0].ResourceID().String() != "data.access/v1" || generated[0].PackagePath() != "example.com/app/access" || generated[0].TypeName() != "Resource" {
		t.Fatalf("generated Resource nodes = %#v", generated)
	}
	if len(graph.ResourceConstructionOrder()) != 5 {
		t.Fatalf("generated provider became a selected database instance: %#v", graph.ResourceConstructionOrder())
	}
	constructor := mustGraphSymbol(t, "example.com/app/generated/data/access/records.New")
	dependencies := graph.ResourceDependencies(constructor)
	if len(dependencies) != 1 || dependencies[0].InstanceName() != "database.primary" || dependencies[0].Provider().String() != "example.com/app/raw.New" || dependencies[0].Reason() != constructorgraph.SelectionExplicit || dependencies[0].ParameterName() != "database" {
		t.Fatalf("generated Resource dependency = %#v", dependencies)
	}
	if got := generated[0].Dependencies(); len(got) != 1 || got[0].InstanceName() != "database.primary" {
		t.Fatalf("generated node dependencies = %#v", got)
	}
}

func TestResourceGraphRejectsImplementationBindingToUninstalledGeneratedResource(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database.primary", "raw.New")}
	input.ResourceBindings = nil
	input.GeneratedResources = []constructorgraph.GeneratedResourceInput{generatedResourceInput(t, "access.view", "data.view/v1", "data.raw/v1")}
	_, err := constructorgraph.Build(input)
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || !errors.As(err, &failure) || len(failure.GeneratedCandidates()) != 1 || !strings.Contains(err.Error(), "requires Data emission and installation") {
		t.Fatalf("uninstalled generated binding = %v", err)
	}
}

func TestResourceGraphReportsGeneratedCandidateInAmbiguity(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.GeneratedResources = []constructorgraph.GeneratedResourceInput{generatedResourceInput(t, "access.view", "data.view/v1", "data.raw/v1")}
	bindings := input.ResourceBindings[:0]
	for _, binding := range input.ResourceBindings {
		if binding.Consumer == "example.com/app/service.New" && binding.Parameter == "view" {
			continue
		}
		bindings = append(bindings, binding)
	}
	input.ResourceBindings = append(bindings, resourceBinding(constructorgraph.ResourceConsumerInstance, "access.view", "database", "database.primary"))
	_, err := constructorgraph.Build(input)
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrAmbiguousResourceBinding) || !errors.As(err, &failure) || len(failure.Candidates()) != 2 || len(failure.GeneratedCandidates()) != 1 || !strings.Contains(err.Error(), "generated instance access.view") {
		t.Fatalf("generated ambiguity = %v", err)
	}
}

func TestResourceGraphRejectsSelectedGeneratedResourceCycle(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "view.first", "view.New")}
	input.ResourceBindings = nil
	input.GeneratedResources = []constructorgraph.GeneratedResourceInput{generatedResourceInput(t, "access.raw", "data.raw/v1", "data.view/v1")}
	_, err := constructorgraph.Build(input)
	var cycle *constructorgraph.ResourceCycleError
	if !errors.Is(err, constructorgraph.ErrCycle) || !errors.As(err, &cycle) || len(cycle.Steps()) < 2 || !strings.Contains(err.Error(), "view.first") || !strings.Contains(err.Error(), "access.raw") {
		t.Fatalf("selected/generated cycle = %v", err)
	}
}

func TestResourceGraphResolvesOnlyActiveImplicitConsumers(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.Requirements, input.Selections, input.ResourceBindings = nil, nil, nil
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New"), resourceInstance(t, "view", "view.New")}
	graph, err := constructorgraph.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.ConstructionOrder()) != 0 || len(graph.Bindings()) != 0 || len(graph.Roots()) != 0 || !reflect.DeepEqual(resourceNodeNames(graph.ResourceConstructionOrder()), []string{"database", "view"}) {
		t.Fatal("selected Resource instances did not remain active without Interface roots")
	}
	dependency := graph.ResourceConstructionOrder()[1].Dependencies()[0]
	if dependency.InstanceName() != "database" || dependency.Reason() != constructorgraph.SelectionUnique || len(dependency.Sources()) != 0 || len(dependency.SelectionSources()) != 1 {
		t.Fatalf("implicit edge = %#v", dependency)
	}
	input.ResourceInstances = nil
	graph, err = constructorgraph.Build(input)
	if err != nil || len(graph.ResourceConstructionOrder()) != 0 {
		t.Fatalf("dormant implicit dependencies resolved: %v", err)
	}
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "view", "view.New")}
	_, err = constructorgraph.Build(input)
	var missing *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrMissingResourceBinding) || !errors.As(err, &missing) || missing.Namespace() != constructorgraph.ResourceConsumerInstance || missing.Consumer() != "view" || missing.ParameterName() != "upstream" || missing.SourcePath() != "view/new.go" {
		t.Fatalf("unconsumed instance dependency must resolve: %v", err)
	}
}

func TestResourceGraphMissingAndAmbiguousActiveBindings(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.ResourceBindings = nil
	input.ResourceInstances = nil
	_, err := constructorgraph.Build(input)
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrBuild) || !errors.Is(err, constructorgraph.ErrMissingResourceBinding) || !errors.As(err, &failure) || failure.ResourceID().String() != "data.raw/v1" || failure.ParameterName() != "database" || failure.ParameterPosition() != 1 || failure.Constructor().String() != "example.com/app/audit.New" || failure.SourcePath() != "audit/new.go" {
		t.Fatalf("missing dependency evidence = %v", err)
	}
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "second", "raw.New"), resourceInstance(t, "first", "raw.New")}
	_, err = constructorgraph.Build(input)
	if !errors.Is(err, constructorgraph.ErrAmbiguousResourceBinding) || !errors.As(err, &failure) || !reflect.DeepEqual(resourceNodeNames(failure.Candidates()), []string{"first", "second"}) {
		t.Fatalf("ambiguous binding = %v", err)
	}
	message := err.Error()
	failure.Candidates()[0].Sources()[0].Reference = "changed"
	slices.Reverse(input.ResourceInstances)
	_, repeated := constructorgraph.Build(input)
	if repeated == nil || repeated.Error() != message || !strings.Contains(message, "raw/new.go") || !strings.Contains(message, "resources.instances.first") || !strings.Contains(message, "correction:") {
		t.Fatalf("unstable or incomplete ambiguity: %v", repeated)
	}
}

func TestResourceGraphValidatesDormantExplicitBindingAddresses(t *testing.T) {
	t.Parallel()
	base := resourceGraphInput(t)
	base.Requirements, base.Selections = nil, nil
	base.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New"), resourceInstance(t, "other", "other.New")}
	for _, test := range []struct{ name, namespace, consumer, parameter, target string }{
		{"namespace", "providers", "example.com/app/dormant.New", "database", "database"},
		{"missing-constructor", "implementations", "example.com/app/missing.New", "database", "database"},
		{"missing-instance", "instances", "missing", "upstream", "database"},
		{"provider-symbol-not-instance", "instances", "example.com/app/raw.New", "database", "database"},
		{"instance-not-symbol", "implementations", "database", "database", "database"},
		{"unknown-parameter", "implementations", "example.com/app/dormant.New", "missing", "database"},
		{"case", "implementations", "example.com/app/dormant.New", "Database", "database"},
		{"blank", "implementations", "example.com/app/dormant.New", "_", "database"},
		{"unnamed", "implementations", "example.com/app/dormant.New", "", "database"},
		{"invalid-identifier", "implementations", "example.com/app/dormant.New", "data-base", "database"},
		{"config-parameter", "implementations", "example.com/app/service.New", "cfg", "database"},
		{"interface-parameter", "implementations", "example.com/app/service.New", "audit", "database"},
		{"missing-target", "implementations", "example.com/app/dormant.New", "database", "missing"},
		{"malformed-target", "implementations", "example.com/app/dormant.New", "database", "Bad"},
		{"wrong-contract", "implementations", "example.com/app/dormant.New", "database", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := base
			input.ResourceBindings = []constructorgraph.ResourceBindingInput{resourceBinding(constructorgraph.ResourceConsumerNamespace(test.namespace), test.consumer, test.parameter, test.target)}
			_, err := constructorgraph.Build(input)
			var failure *constructorgraph.ResourceBindingError
			if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || !errors.As(err, &failure) || len(failure.Sources()) == 0 || failure.ModulePath() == "" || failure.SourcePath() == "" || failure.Line() < 1 || failure.Column() < 1 {
				t.Fatalf("dormant invalid binding = %v", err)
			}
		})
	}
	base.ResourceBindings = []constructorgraph.ResourceBindingInput{resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/dormant.New", "database", "database")}
	graph, err := constructorgraph.Build(base)
	if err != nil || len(graph.ConstructionOrder()) != 0 {
		t.Fatalf("valid dormant binding activated consumer: %v", err)
	}
	base.ResourceInstances = nil
	if _, err := constructorgraph.Build(base); !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
		t.Fatalf("removed explicit target silently rebound: %v", err)
	}
}

func TestResourceGraphReportsCompleteInstanceCycles(t *testing.T) {
	t.Parallel()
	base := resourceGraphInput(t)
	base.Requirements, base.Selections = nil, nil
	for _, names := range [][]string{{"self"}, {"first", "second", "third"}} {
		input := base
		input.ResourceInstances, input.ResourceBindings = nil, nil
		for index, name := range names {
			input.ResourceInstances = append(input.ResourceInstances, resourceInstance(t, name, "wrap.New"))
			input.ResourceBindings = append(input.ResourceBindings, resourceBinding(constructorgraph.ResourceConsumerInstance, name, "upstream", names[(index+1)%len(names)]))
		}
		_, err := constructorgraph.Build(input)
		var cycle *constructorgraph.ResourceCycleError
		if !errors.Is(err, constructorgraph.ErrCycle) || !errors.As(err, &cycle) || len(cycle.Steps()) != len(names) {
			t.Fatalf("cycle = %v", err)
		}
		steps := cycle.Steps()
		for index, step := range steps {
			if step.Consumer() != names[index] || step.InstanceName() != names[(index+1)%len(names)] || step.Constructor().String() != "example.com/app/wrap.New" || step.Provider() != step.Constructor() || step.ResourceID().String() != "data.raw/v1" || step.ParameterName() != "upstream" || step.ParameterPosition() != 1 || step.DeclarationSource().Path != "wrap/new.go" || len(step.Sources()) != 1 || len(step.SelectionSources()) != 1 {
				t.Fatalf("cycle step = %#v", step)
			}
		}
		message := err.Error()
		steps[0].Sources()[0].Reference = "changed"
		steps[0].SelectionSources()[0].Reference = "changed"
		slices.Reverse(input.ResourceInstances)
		slices.Reverse(input.ResourceBindings)
		_, repeated := constructorgraph.Build(input)
		if repeated == nil || repeated.Error() != message || !strings.Contains(message, "correction:") {
			t.Fatalf("unstable cycle evidence: %v", repeated)
		}
	}
}

func TestResourceGraphRejectsInvalidInstancesAndConflictingInputs(t *testing.T) {
	t.Parallel()
	base := resourceGraphInput(t)
	base.Requirements, base.Selections, base.ResourceBindings = nil, nil, nil
	for _, name := range []string{"", "A", "a..b", "a-", "a_b", strings.Repeat("a", 129), "\u03b4"} {
		input := base
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, name, "raw.New")}
		if _, err := constructorgraph.Build(input); !errors.Is(err, constructorgraph.ErrInvalidResourceInstance) {
			t.Fatalf("invalid name accepted: %v", err)
		}
	}
	for _, provider := range []string{"missing.New", "service.New"} {
		input := base
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", provider)}
		if _, err := constructorgraph.Build(input); !errors.Is(err, constructorgraph.ErrInvalidResourceInstance) {
			t.Fatalf("invalid provider accepted: %v", err)
		}
	}
	base.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New"), resourceInstance(t, "database", "raw.Alternate")}
	if _, err := constructorgraph.Build(base); !errors.Is(err, constructorgraph.ErrInvalidResourceInstance) {
		t.Fatalf("conflicting provider accepted: %v", err)
	}
	base.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New"), resourceInstance(t, "second", "raw.New")}
	base.ResourceBindings = []constructorgraph.ResourceBindingInput{resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/dormant.New", "database", "database"), resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/dormant.New", "database", "second")}
	if _, err := constructorgraph.Build(base); !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
		t.Fatalf("conflicting binding accepted: %v", err)
	}
	base.ResourceBindings = base.ResourceBindings[:1]
	base.ResourceBindings = append(base.ResourceBindings, base.ResourceBindings[0])
	base.ResourceInstances = append(base.ResourceInstances, base.ResourceInstances[0])
	graph, err := constructorgraph.Build(base)
	if err != nil || len(graph.ResourceConstructionOrder()) != 2 || len(graph.ResourceConstructionOrder()[0].Sources()) != 1 {
		t.Fatalf("identical evidence not normalized: %v", err)
	}
	base.ResourceInstances[0].Sources = nil
	if _, err := constructorgraph.Build(base); !errors.Is(err, constructorgraph.ErrInvalidInput) {
		t.Fatalf("missing provenance accepted: %v", err)
	}
}

func TestResourceGraphImplicitBindingsKeepParameterOrder(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.ResourceBindings = nil
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "a-view", "view.New"), resourceInstance(t, "z-database", "raw.New")}
	graph, err := constructorgraph.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceNodeNames(graph.ResourceConstructionOrder()); !reflect.DeepEqual(got, []string{"z-database", "a-view"}) {
		t.Fatalf("dependencies did not precede consumers: %v", got)
	}
	dependencies := graph.ResourceDependencies(mustGraphSymbol(t, "example.com/app/service.New"))
	for index, target := range []string{"z-database", "z-database", "a-view"} {
		if dependencies[index].InstanceName() != target || dependencies[index].ParameterPosition() != index+3 || dependencies[index].Reason() != constructorgraph.SelectionUnique {
			t.Fatalf("implicit parameter edge = %#v", dependencies[index])
		}
	}
	instanceEdge := graph.ResourceConstructionOrder()[1].Dependencies()[0]
	if len(instanceEdge.ConsumerSelectionSources()) != 1 {
		t.Fatal("consumer selection evidence missing")
	}
	instanceEdge.ConsumerSelectionSources()[0].Reference = "changed"
	if graph.ResourceConstructionOrder()[1].Dependencies()[0].ConsumerSelectionSources()[0].Reference == "changed" {
		t.Fatal("consumer selection evidence is mutable")
	}
}

func TestResourceGraphCycleExcludesPrefixAndIncludesImplicitSelfEdge(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.Requirements, input.Selections = nil, nil
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "a-prefix", "view.New"), resourceInstance(t, "b-cycle", "wrap.New"), resourceInstance(t, "c-cycle", "wrap.New")}
	input.ResourceBindings = []constructorgraph.ResourceBindingInput{
		resourceBinding(constructorgraph.ResourceConsumerInstance, "a-prefix", "upstream", "b-cycle"),
		resourceBinding(constructorgraph.ResourceConsumerInstance, "b-cycle", "upstream", "c-cycle"),
		resourceBinding(constructorgraph.ResourceConsumerInstance, "c-cycle", "upstream", "b-cycle"),
	}
	_, err := constructorgraph.Build(input)
	var cycle *constructorgraph.ResourceCycleError
	if !errors.As(err, &cycle) || len(cycle.Steps()) != 2 || cycle.Steps()[0].Consumer() != "b-cycle" || cycle.Steps()[1].InstanceName() != "b-cycle" || strings.Contains(err.Error(), "a-prefix") {
		t.Fatalf("cycle path = %v", err)
	}
	input.ResourceInstances = input.ResourceInstances[1:2]
	input.ResourceBindings = nil
	_, err = constructorgraph.Build(input)
	if !errors.As(err, &cycle) || len(cycle.Steps()) != 1 || cycle.Steps()[0].Reason() != constructorgraph.SelectionUnique || cycle.Steps()[0].Consumer() != cycle.Steps()[0].InstanceName() {
		t.Fatalf("implicit self-cycle = %v", err)
	}
}

func TestResourceGraphRevalidatesReplacedProviderAndUnicodeParameter(t *testing.T) {
	t.Parallel()
	input := resourceGraphInput(t)
	input.Requirements, input.Selections = nil, nil
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New"), resourceInstance(t, "consumer", "wrap.New")}
	input.ResourceBindings = []constructorgraph.ResourceBindingInput{resourceBinding(constructorgraph.ResourceConsumerInstance, "consumer", "upstream", "database")}
	if _, err := constructorgraph.Build(input); err != nil {
		t.Fatal(err)
	}
	input.ResourceInstances[1].Provider = mustGraphSymbol(t, "example.com/app/raw.Alternate")
	_, err := constructorgraph.Build(input)
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || !errors.As(err, &failure) || failure.Constructor() != input.ResourceInstances[1].Provider || failure.SourcePath() != "raw/new.go" || failure.ParameterName() != "upstream" {
		t.Fatalf("replaced provider retained stale binding: %v", err)
	}
	input.ResourceBindings = []constructorgraph.ResourceBindingInput{resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/unicode.New", "\u03b4", "database")}
	if _, err := constructorgraph.Build(input); err != nil {
		t.Fatalf("valid Unicode identifier rejected: %v", err)
	}
	input.ResourceBindings[0].Parameter = "\u0394"
	if _, err := constructorgraph.Build(input); !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
		t.Fatalf("Unicode case was not exact: %v", err)
	}
}

func TestResourceGraphValidatesAndNormalizesProvenance(t *testing.T) {
	t.Parallel()
	base := resourceGraphInput(t)
	base.Requirements, base.Selections, base.ResourceBindings = nil, nil, nil
	base.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New")}
	for _, change := range []func(*constructorgraph.ResourceSource){
		func(s *constructorgraph.ResourceSource) { s.Reference = "" },
		func(s *constructorgraph.ResourceSource) { s.Reference = "a\nb" },
		func(s *constructorgraph.ResourceSource) { s.Path = "../outside.yaml" },
		func(s *constructorgraph.ResourceSource) { s.Path = "C:/outside.yaml" },
		func(s *constructorgraph.ResourceSource) { s.ModulePath = "invalid module" },
		func(s *constructorgraph.ResourceSource) { s.Line = 0 },
		func(s *constructorgraph.ResourceSource) { s.Column = -1 },
	} {
		source := resourceSource("selection")
		change(&source)
		input := base
		input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "database", "raw.New")}
		input.ResourceInstances[0].Sources = []constructorgraph.ResourceSource{source}
		if _, err := constructorgraph.Build(input); !errors.Is(err, constructorgraph.ErrInvalidInput) {
			t.Fatalf("invalid instance source accepted: %v", err)
		}
		input.ResourceInstances = base.ResourceInstances
		binding := resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/dormant.New", "database", "database")
		binding.Sources = []constructorgraph.ResourceSource{source}
		input.ResourceBindings = []constructorgraph.ResourceBindingInput{binding}
		if _, err := constructorgraph.Build(input); !errors.Is(err, constructorgraph.ErrInvalidInput) {
			t.Fatalf("invalid binding source accepted: %v", err)
		}
	}
	base.ResourceInstances[0].Sources = []constructorgraph.ResourceSource{resourceSource("z-source"), resourceSource("a-source"), resourceSource("z-source")}
	graph, err := constructorgraph.Build(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := graph.ResourceConstructionOrder()[0].Sources(); len(got) != 2 || got[0].Reference != "a-source" || got[1].Reference != "z-source" {
		t.Fatalf("normalized sources = %#v", got)
	}
	base.ResourceInstances[0].Sources[2].Line++
	if _, err := constructorgraph.Build(base); !errors.Is(err, constructorgraph.ErrInvalidInput) {
		t.Fatalf("conflicting typed source accepted: %v", err)
	}
}

func resourceGraphInput(t testing.TB) constructorgraph.Input {
	t.Helper()
	root := t.TempDir()
	writeGraphFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeGraphFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	for _, contract := range []struct{ name, method string }{{"raw", "Raw"}, {"view", "View"}, {"other", "Raw"}} {
		writeGraphFile(t, filepath.Join(root, "contracts", contract.name, "resource.go"), fmt.Sprintf("package %s\n//plystra:resource data.%s/v1\ntype Resource interface{ %s() }\n", contract.name, contract.name, contract.method))
	}
	for _, provider := range []struct{ pkg, contract, parameter, method string }{{"raw", "raw", "", "Raw"}, {"view", "view", "cfg Config, upstream raw.Resource", "View"}, {"other", "other", "", "Raw"}, {"wrap", "raw", "upstream raw.Resource", "Raw"}} {
		imports := ""
		if provider.parameter != "" {
			imports = "import raw \"example.com/app/contracts/raw\"\n"
		}
		source := fmt.Sprintf("package %s\n%s type Config struct{ Label string }\ntype Value struct{}\nfunc (*Value) %s(){}\n//plystra:implements-resource data.%s/v1\nfunc New(%s)(*Value,error){ panic(\"constructor executed\") }\n", provider.pkg, imports, provider.method, provider.contract, provider.parameter)
		if provider.pkg == "raw" {
			source += "//plystra:implements-resource data.raw/v1\nfunc Alternate()(*Value,error){return &Value{},nil}\n"
		}
		writeGraphFile(t, filepath.Join(root, provider.pkg, "new.go"), source)
	}
	for _, item := range []struct{ name, method string }{{"run", "Run"}, {"report", "Report"}, {"audit", "Audit"}, {"dormant", "Dormant"}} {
		writeGraphFile(t, filepath.Join(root, "interfaces", item.name, "interface.go"), graphInterfaceSource(item.name, "app."+item.name+"/v1", item.method))
	}
	writeGraphFile(t, filepath.Join(root, "service", "new.go"), `package service
import (
 "context"
 run "example.com/app/interfaces/run"
 report "example.com/app/interfaces/report"
 audit "example.com/app/interfaces/audit"
 raw "example.com/app/contracts/raw"
 view "example.com/app/contracts/view"
)
type Config struct{ Label string }
type Service struct{}
//plystra:implements app.run/v1
//plystra:implements app.report/v1
func New(cfg Config, audit audit.Interface, database, Database raw.Resource, view view.Resource)(*Service,error){panic("constructor executed")}
func (*Service) Run(context.Context,run.Request)(run.Response,error){return run.Response{},nil}
func (*Service) Report(context.Context,report.Request)(report.Response,error){return report.Response{},nil}
`)
	for _, item := range []struct{ name, method string }{{"audit", "Audit"}, {"dormant", "Dormant"}} {
		writeGraphFile(t, filepath.Join(root, item.name, "new.go"), fmt.Sprintf("package %s\nimport (\"context\"; api \"example.com/app/interfaces/%s\"; raw \"example.com/app/contracts/raw\")\ntype Service struct{}\n//plystra:implements app.%s/v1\nfunc New(database raw.Resource)(*Service,error){panic(\"constructor executed\")}\nfunc (*Service) %s(context.Context,api.Request)(api.Response,error){return api.Response{},nil}\n", item.name, item.name, item.name, item.method))
	}
	writeGraphFile(t, filepath.Join(root, "unicode", "new.go"), "package unicode\nimport (\"context\"; api \"example.com/app/interfaces/dormant\"; raw \"example.com/app/contracts/raw\")\ntype Service struct{}\n//plystra:implements app.dormant/v1\nfunc New(\u03b4 raw.Resource)(*Service,error){panic(\"constructor executed\")}\nfunc (*Service) Dormant(context.Context,api.Request)(api.Response,error){return api.Response{},nil}\n")
	before := snapshotGraphTree(t, root)
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
	t.Cleanup(func() {
		if !reflect.DeepEqual(before, snapshotGraphTree(t, root)) {
			t.Error("graph discovery/resolution mutated authored input")
		}
	})
	input := constructorgraph.Input{Implementations: discovery.Implementations(), ResourceProviders: discovery.ResourceProviders()}
	for _, id := range []string{"run", "report"} {
		input.Requirements = append(input.Requirements, constructorgraph.Requirement{InterfaceID: mustGraphID(t, "app."+id+"/v1"), Source: constructorgraph.RequirementSource{Kind: constructorgraph.RequirementDeclaration, Reference: "interfaces.require." + id, ModulePath: "example.com/app", Path: "plystra.yaml", Line: 1, Column: 1}})
	}
	for _, item := range []struct{ id, pkg string }{{"run", "service"}, {"report", "service"}, {"audit", "audit"}} {
		input.Selections = append(input.Selections, constructorgraph.Selection{InterfaceID: mustGraphID(t, "app."+item.id+"/v1"), Constructor: mustGraphSymbol(t, "example.com/app/"+item.pkg+".New"), Reason: constructorgraph.SelectionUnique, Sources: []string{"selected " + item.pkg}})
	}
	input.ResourceInstances = []constructorgraph.ResourceInstanceInput{resourceInstance(t, "view.second", "view.New"), resourceInstance(t, "database.secondary", "raw.New"), resourceInstance(t, "orphan", "other.New"), resourceInstance(t, "view.first", "view.New"), resourceInstance(t, "database.primary", "raw.New")}
	input.ResourceBindings = []constructorgraph.ResourceBindingInput{
		resourceBinding(constructorgraph.ResourceConsumerInstance, "view.first", "upstream", "database.primary"),
		resourceBinding(constructorgraph.ResourceConsumerInstance, "view.second", "upstream", "database.secondary"),
		resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/service.New", "database", "database.primary"),
		resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/service.New", "Database", "database.secondary"),
		resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/service.New", "view", "view.second"),
		resourceBinding(constructorgraph.ResourceConsumerImplementation, "example.com/app/audit.New", "database", "database.primary"),
	}
	return input
}

func resourceInstance(t testing.TB, name, provider string) constructorgraph.ResourceInstanceInput {
	t.Helper()
	return constructorgraph.ResourceInstanceInput{Name: name, Provider: mustGraphSymbol(t, "example.com/app/"+provider), Sources: []constructorgraph.ResourceSource{resourceSource("resources.instances." + name)}}
}

func generatedResourceInput(t testing.TB, name, resourceID, databaseID string) constructorgraph.GeneratedResourceInput {
	t.Helper()
	return constructorgraph.GeneratedResourceInput{
		MemberID: name + "/member", Name: name, ResourceID: mustGraphID(t, resourceID),
		PackagePath: "example.com/app/contracts/" + strings.TrimPrefix(resourceID, "data."), TypeName: "Resource",
		Constructor:        mustGraphSymbol(t, "example.com/app/generated/"+strings.ReplaceAll(name, ".", "/")+".New"),
		DatabaseResourceID: mustGraphID(t, databaseID), DatabasePackagePath: "example.com/app/contracts/" + strings.TrimPrefix(databaseID, "data."),
		DatabaseParameter: "database", DatabaseParameterPosition: 1, Sources: []constructorgraph.ResourceSource{resourceSource("data.members." + name)},
	}
}
func resourceBinding(namespace constructorgraph.ResourceConsumerNamespace, consumer, parameter, target string) constructorgraph.ResourceBindingInput {
	return constructorgraph.ResourceBindingInput{Namespace: namespace, Consumer: consumer, Parameter: parameter, Target: target, Sources: []constructorgraph.ResourceSource{resourceSource("resources.bind." + string(namespace) + "." + consumer + "." + parameter)}}
}
func resourceSource(reference string) constructorgraph.ResourceSource {
	return constructorgraph.ResourceSource{Reference: reference, ModulePath: "example.com/app", Path: "plystra.yaml", Line: 1, Column: 1}
}
func resourceNodeNames(nodes []constructorgraph.ResourceNode) []string {
	result := make([]string, len(nodes))
	for index, node := range nodes {
		result[index] = node.Name()
	}
	return result
}
func resourceGraphSummary(graph constructorgraph.Graph) []string {
	var result []string
	for _, node := range graph.ResourceConstructionOrder() {
		result = append(result, fmt.Sprintf("%s %s %s %v", node.Name(), node.ResourceID(), node.Provider().Symbol(), node.Sources()))
		for _, dependency := range node.Dependencies() {
			result = append(result, fmt.Sprintf("%s %s %d %s %v %v", dependency.Consumer(), dependency.ParameterName(), dependency.ParameterPosition(), dependency.InstanceName(), dependency.Sources(), dependency.SelectionSources()))
		}
	}
	for _, node := range graph.ConstructionOrder() {
		for _, dependency := range graph.ResourceDependencies(node.Symbol()) {
			result = append(result, fmt.Sprintf("%s %s %d %s %v %v", dependency.Consumer(), dependency.ParameterName(), dependency.ParameterPosition(), dependency.InstanceName(), dependency.Sources(), dependency.SelectionSources()))
		}
	}
	return result
}
