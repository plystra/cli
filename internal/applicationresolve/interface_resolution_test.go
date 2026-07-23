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
	if roots := graph.Roots(); len(roots) != 2 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{
		"example.com/interface-app@local/app/service.go:14:1 //plystra:implements app.run/v1",
		`plystra.yaml interfaces.require["app.run/v1"]`,
	}) || roots[1].InterfaceID().String() != "audit.write/v1" || !reflect.DeepEqual(roots[1].Sources(), []string{
		"example.com/interface-app@local/auditone/service.go:11:1 //plystra:implements audit.write/v1",
		"example.com/interface-app@local/audittwo/service.go:11:1 //plystra:implements audit.write/v1",
	}) {
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
		"example.com/interface-cache/cache.New",
		"example.com/interface-app/app.New",
	}) {
		t.Fatalf("production construction order = %v", got)
	}
	if got := resolvedSelectionSummaries(production.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/audittwo.New:explicit",
		"cache.read/v1=example.com/interface-cache/cache.New:unique-compatible",
	}) {
		t.Fatalf("production selections = %v", got)
	}
	productionDependencies := productionGraph.ConstructionOrder()[2].Dependencies()
	if !productionDependencies[1].Optional() || !productionDependencies[1].Available() || productionDependencies[1].Constructor().String() != "example.com/interface-cache/cache.New" {
		t.Fatalf("production optional cache = %#v", productionDependencies[1])
	}
	if selection := production.ConfigurationSelection(); selection.Mode() != "environment" || selection.Environment() != "production" || selection.Path() != "plystra.production.yaml" {
		t.Fatalf("production selection = mode %q environment %q path %q", selection.Mode(), selection.Environment(), selection.Path())
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatalf("Interface application resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveCollectsLocalImplementationDeclarationsAsApplicationRoots(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	dependencyRoot := filepath.Join(parent, "dependency")
	writeModule(t, dependencyRoot, "example.com/interface-dependency")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, dependencyRoot, "unused/read/v1", "readv1", "unused.read/v1", "Read")
	writeResolvedSimpleImplementationForModule(t, dependencyRoot, "example.com/interface-dependency", "unused", "unused.read/v1", "unused/read/v1", "Read")

	root := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/local-application

go 1.26

require example.com/interface-dependency v1.2.3

replace example.com/interface-dependency => ../dependency
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeResolvedSimpleImplementationForModule(t, root, "example.com/local-application", "app", "app.run/v1", "app/run/v1", "Run")
	before := snapshotTree(t, parent)

	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	roots := resolved.InterfaceResolution().Graph().Roots()
	if len(roots) != 1 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{
		"example.com/local-application@local/app/service.go:11:1 //plystra:implements app.run/v1",
	}) {
		t.Fatalf("local application roots = %#v", roots)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/local-application/app.New:unique-compatible",
	}) {
		t.Fatalf("local application selections = %v", got)
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("local application-root resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveAppliesNoDiscoveryOrFilesystemOrderSelectionPriority(t *testing.T) {
	t.Parallel()

	orders := [][]string{
		{"zeta", "alpha"},
		{"alpha", "zeta"},
	}
	var baseline string
	for index, order := range orders {
		root := t.TempDir()
		writeModule(t, root, "example.com/order-independent")
		writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
		writeResolvedInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
		for _, packageName := range order {
			writeResolvedSimpleImplementationForModule(t, root, "example.com/order-independent", packageName, "app.run/v1", "app/run/v1", "Run")
		}
		before := snapshotTree(t, root)
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start: root,
			Environment: goEnvironment(map[string]string{
				"GOWORK":  "off",
				"GOPROXY": "off",
				"GOSUMDB": "off",
			}),
		})
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, interfaceresolution.ErrAmbiguousImplementation) {
			t.Fatalf("Resolve order %v error = %v", order, err)
		}
		var ambiguous *interfaceresolution.AmbiguousImplementationError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("Resolve order %v omitted typed ambiguity: %v", order, err)
		}
		candidates := ambiguous.Candidates()
		if len(candidates) != 2 || candidates[0].Constructor().String() != "example.com/order-independent/alpha.New" || candidates[1].Constructor().String() != "example.com/order-independent/zeta.New" {
			t.Fatalf("Resolve order %v candidates = %#v", order, candidates)
		}
		if index == 0 {
			baseline = err.Error()
		} else if err.Error() != baseline {
			t.Fatalf("filesystem creation order changed ambiguity:\nfirst:  %s\nsecond: %s", baseline, err)
		}
		if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("Resolve order %v mutated Project:\nbefore: %#v\nafter: %#v", order, before, after)
		}
	}
}

func TestResolveCollectsEnvironmentExposureAsInterfaceRequirement(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	dependencyRoot := filepath.Join(parent, "implementations")
	writeModule(t, dependencyRoot, "example.com/interface-implementations")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, dependencyRoot, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeResolvedInterface(t, dependencyRoot, "info/read/v1", "readv1", "info.read/v1", "Read")
	writeResolvedSimpleImplementationForModule(t, dependencyRoot, "example.com/interface-implementations", "app", "app.run/v1", "app/run/v1", "Run")
	writeResolvedSimpleImplementationForModule(t, dependencyRoot, "example.com/interface-implementations", "info", "info.read/v1", "info/read/v1", "Read")

	root := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/interface-app

go 1.26

require example.com/interface-implementations v1.2.3

replace example.com/interface-implementations => ../implementations
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {expose: [app.run/v1]}\n")
	before := snapshotTree(t, parent)
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
	})

	defaultResult, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Resolve default: %v", err)
	}
	if roots := defaultResult.InterfaceResolution().Graph().Roots(); len(roots) != 0 {
		t.Fatalf("default Interface roots = %#v", roots)
	}
	if selections := defaultResult.InterfaceResolution().Selections(); len(selections) != 0 {
		t.Fatalf("default Interface selections = %#v", selections)
	}

	production, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           root,
		EnvironmentName: "production",
		Environment:     environment,
	})
	if err != nil {
		t.Fatalf("Resolve production: %v", err)
	}
	roots := production.InterfaceResolution().Graph().Roots()
	if len(roots) != 1 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{`plystra.production.yaml http.expose["app.run/v1"]`}) {
		t.Fatalf("production Interface roots = %#v", roots)
	}
	if got := resolvedSelectionSummaries(production.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-implementations/app.New:unique-compatible",
	}) {
		t.Fatalf("production selections = %v", got)
	}
	if requirements := production.Resolution().Context().Requirements(); len(requirements) != 0 {
		t.Fatalf("Interface-only exposure entered legacy Capability requirements: %v", requirements)
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("selected Interface exposure resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveCollectsIntrinsicKernelRequirementsWithApplicationProvenance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/intrinsic-application")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: [kernel.health/v1]
interfaces:
  require: [kernel.info/v1]
`)
	before := snapshotTree(t, root)
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	requirements := resolved.InterfaceResolution().IntrinsicRequirements()
	if len(requirements) != 2 || requirements[0].InterfaceID().String() != "kernel.health/v1" || requirements[0].PackagePath() != "github.com/plystra/kernel/interfaces/kernel/health/v1" || !reflect.DeepEqual(requirements[0].Sources(), []string{
		"github.com/plystra/kernel/interfaces/kernel/health/v1 //plystra:interface kernel.health/v1",
		`plystra.yaml http.expose["kernel.health/v1"]`,
	}) || requirements[1].InterfaceID().String() != "kernel.info/v1" || requirements[1].PackagePath() != "github.com/plystra/kernel/interfaces/kernel/info/v1" || !reflect.DeepEqual(requirements[1].Sources(), []string{
		"github.com/plystra/kernel/interfaces/kernel/info/v1 //plystra:interface kernel.info/v1",
		`plystra.yaml interfaces.require["kernel.info/v1"]`,
	}) {
		t.Fatalf("intrinsic requirements = %#v", requirements)
	}
	if len(resolved.InterfaceResolution().Selections()) != 0 || len(resolved.InterfaceResolution().Graph().Roots()) != 0 {
		t.Fatalf("intrinsic requirements entered ordinary selection: %#v", resolved.InterfaceResolution())
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("intrinsic resolution mutated Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsUnknownOrShadowedIntrinsicKernelInterfaceWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(testing.TB, string)
		want    []string
	}{
		{
			name: "unknown reserved Interface",
			prepare: func(t testing.TB, root string) {
				writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.missing/v1]}\n")
			},
			want: []string{"kernel.missing/v1", "selected Kernel API"},
		},
		{
			name: "application shadow",
			prepare: func(t testing.TB, root string) {
				writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
				writeResolvedInterface(t, root, "kernel/health/v1", "healthv1", "kernel.health/v1", "Health")
			},
			want: []string{"kernel.health/v1", "reserved kernel.* namespace", "canonical Kernel Interface package"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeModule(t, root, "example.com/intrinsic-application")
			test.prepare(t, root)
			before := snapshotTree(t, root)
			_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
				Start: root,
				Environment: goEnvironment(map[string]string{
					"GOWORK":  "off",
					"GOPROXY": "off",
					"GOSUMDB": "off",
				}),
			})
			if !errors.Is(err, applicationresolve.ErrResolve) || !containsResolutionFragments(err.Error(), test.want...) {
				t.Fatalf("Resolve error = %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed intrinsic resolution mutated Project:\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func TestResolveCollectsDependencyExposureWithComposedProvenance(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	dependencyRoot := filepath.Join(parent, "platform")
	writeModule(t, dependencyRoot, "example.com/interface-platform")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "http: {expose: [app.run/v1]}\n")
	writeResolvedInterface(t, dependencyRoot, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeFile(t, filepath.Join(dependencyRoot, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/interface-platform/interfaces/app/run/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)

	root := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/interface-consumer

go 1.26

require example.com/interface-platform v1.2.3

replace example.com/interface-platform => ../platform
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := snapshotTree(t, root)
	dependencyBefore := snapshotTree(t, dependencyRoot)
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	roots := resolved.InterfaceResolution().Graph().Roots()
	wantSource := `example.com/interface-platform@v1.2.3/plystra.yaml http.expose["app.run/v1"]`
	if len(roots) != 1 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{wantSource}) {
		t.Fatalf("dependency exposure roots = %#v", roots)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-platform/app.New:unique-compatible",
	}) {
		t.Fatalf("dependency exposure selections = %v", got)
	}
	if !resolved.ConfigurationMaintenance().Changed() {
		t.Fatal("dependency exposure was not represented in planned root maintenance")
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("dependency exposure resolution mutated current Project:\nbefore: %#v\nafter: %#v", before, after)
	}
	if after := snapshotTree(t, dependencyRoot); !reflect.DeepEqual(after, dependencyBefore) {
		t.Fatalf("dependency exposure resolution mutated dependency Project:\nbefore: %#v\nafter: %#v", dependencyBefore, after)
	}
}

func TestResolveRejectsInvalidExposedInterfaceBeforeLegacyResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(testing.TB, string)
		wantError error
		want      []string
	}{
		{
			name:      "missing canonical Interface",
			wantError: interfaceresolution.ErrUnknownInterface,
			want:      []string{"missing.run/v1", "visible canonical package"},
		},
		{
			name: "ambiguous Implementation",
			prepare: func(t testing.TB, root string) {
				writeResolvedInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
				writeResolvedSimpleImplementation(t, root, "appone", "app.run/v1", "app/run/v1", "Run")
				writeResolvedSimpleImplementation(t, root, "apptwo", "app.run/v1", "app/run/v1", "Run")
			},
			wantError: interfaceresolution.ErrAmbiguousImplementation,
			want:      []string{"app.run/v1", "appone.New", "apptwo.New", `interfaces.use["app.run/v1"]`},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeModule(t, root, "example.com/interface-app")
			identifier := "missing.run/v1"
			if test.prepare != nil {
				identifier = "app.run/v1"
				test.prepare(t, root)
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), "http: {expose: ["+identifier+"]}\n")
			before := snapshotTree(t, root)
			_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
				Start: root,
				Environment: goEnvironment(map[string]string{
					"GOWORK":  "off",
					"GOPROXY": "off",
					"GOSUMDB": "off",
				}),
			})
			if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, test.wantError) || !containsResolutionFragments(err.Error(), test.want...) {
				t.Fatalf("Resolve error = %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed exposed Interface resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
			}
		})
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
	cacheRoot := filepath.Join(parent, "cache")
	writeModule(t, cacheRoot, "example.com/interface-cache")
	writeFile(t, filepath.Join(cacheRoot, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, cacheRoot, "cache/read/v1", "readv1", "cache.read/v1", "Read")
	writeResolvedSimpleImplementationForModule(t, cacheRoot, "example.com/interface-cache", "cache", "cache.read/v1", "cache/read/v1", "Read")

	root := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/interface-app

go 1.26

require (
	example.com/interface-cache v1.0.0
	github.com/plystra/kernel v0.0.0
)

replace example.com/interface-cache => ../cache

replace github.com/plystra/kernel => ../kernel
`)
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
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	plystra "github.com/plystra/kernel"
	readv1 "example.com/interface-cache/interfaces/cache/read/v1"
	runv1 "example.com/interface-app/interfaces/app/run/v1"
	writev1 "example.com/interface-app/interfaces/audit/write/v1"
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
	writeResolvedSimpleImplementationForModule(t, root, "example.com/interface-app", packageName, identifier, interfacePath, method)
}

func writeResolvedSimpleImplementationForModule(t testing.TB, root, modulePath, packageName, identifier, interfacePath, method string) {
	t.Helper()
	writeFile(t, filepath.Join(root, packageName, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "%s/interfaces/%s"
)

type Service struct{}

//plystra:implements %s
func New() (*Service, error) { return &Service{}, nil }

func (*Service) %s(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, packageName, modulePath, interfacePath, identifier, method))
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
