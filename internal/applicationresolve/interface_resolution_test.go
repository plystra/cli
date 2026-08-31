package applicationresolve_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/transporttoolchain"
)

func TestResolveBuildsSelectedInterfaceConstructorGraphFromConfiguration(t *testing.T) {
	t.Parallel()

	root := writeResolvedInterfaceProject(t)
	writeFile(t, filepath.Join(filepath.Dir(root), "cache", "plystra.production.yaml"), "this: [dependency overlay is deliberately invalid\n")
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
	if policies := resolved.Composition().Manifest().InterfacePolicies(); len(policies) != 1 || policies[0].InterfaceID().String() != "audit.write/v1" || policies[0].Timeout().String() != "5s" || policies[0].Source() != `plystra.yaml interfaces.policies["audit.write/v1"].timeout` {
		t.Fatalf("default Interface policies = %#v", policies)
	}
	app := graph.ConstructionOrder()[1]
	dependencies := app.Dependencies()
	if len(dependencies) != 2 || dependencies[0].InterfaceID().String() != "audit.write/v1" || dependencies[0].Optional() || !dependencies[0].Available() || dependencies[0].Constructor().String() != "example.com/interface-app/auditone.New" || dependencies[1].InterfaceID().String() != "cache.read/v1" || !dependencies[1].Optional() || dependencies[1].Available() {
		t.Fatalf("default app dependencies = %#v", dependencies)
	}
	if roots := graph.Roots(); len(roots) != 1 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{
		`plystra.yaml interfaces.require["app.run/v1"]`,
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
	if policies := production.Composition().Manifest().InterfacePolicies(); len(policies) != 1 || policies[0].InterfaceID().String() != "audit.write/v1" || policies[0].Timeout().String() != "2s" || policies[0].Source() != `plystra.production.yaml interfaces.policies["audit.write/v1"].timeout` {
		t.Fatalf("production Interface policies = %#v", policies)
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

func TestResolveUsesCompleteReplacementForInterfaceConfiguration(t *testing.T) {
	t.Parallel()

	root := writeResolvedInterfaceProject(t)
	parent := filepath.Dir(root)
	rootConfiguration := `http: {address: ":8080"}
interfaces:
  require: [missing.read/v1]
  use:
    audit.write/v1: example.com/interface-app/auditone.New
  policies:
    app.run/v1: {timeout: 7s}
    audit.write/v1: {timeout: 5s}
config:
  example.com/excluded/root.New: {ignored: root-only}
`
	selectedConfiguration := `# Complete customer configuration.
http: {address: ":9090"}
interfaces:
  require: [app.run/v1]
  use:
    audit.write/v1: example.com/interface-app/audittwo.New
  policies:
    audit.write/v1: {timeout: 2s}
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), rootConfiguration)
	writeFile(t, filepath.Join(parent, "cache", "plystra.yaml"), "interfaces: {require: [cache.read/v1]}\n")
	writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), selectedConfiguration)
	before := snapshotTree(t, parent)

	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:             filepath.Join(root, "app"),
		ConfigurationPath: "deploy/customer.yaml",
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
			"GOFLAGS": "-mod=readonly",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection := resolved.ConfigurationSelection(); selection.Mode() != applicationgen.ConfigurationModeExplicit || selection.Path() != "deploy/customer.yaml" || selection.Environment() != "" {
		t.Fatalf("full-replacement selection = mode %q path %q environment %q", selection.Mode(), selection.Path(), selection.Environment())
	}
	if address, exists := resolved.Composition().Manifest().HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("full-replacement HTTP address = %q, %t", address, exists)
	}
	requirements := resolved.Composition().Manifest().InterfaceRequirements()
	if len(requirements) != 2 || requirements[0].ID().String() != "app.run/v1" || requirements[0].Source() != `deploy/customer.yaml interfaces.require["app.run/v1"]` || requirements[1].ID().String() != "cache.read/v1" || requirements[1].Source() != `deploy/customer.yaml interfaces.require["cache.read/v1"]` {
		t.Fatalf("full-replacement Interface requirements = %#v", requirements)
	}
	if maintenance := resolved.ConfigurationMaintenance(); !maintenance.Changed() || resolved.ConfigurationMaintenancePath() != "deploy/customer.yaml" || !strings.Contains(string(maintenance.Data()), "cache.read/v1") {
		t.Fatalf("full-replacement dependency maintenance = changed %t path %q data %q", maintenance.Changed(), resolved.ConfigurationMaintenancePath(), maintenance.Data())
	}
	if policies := resolved.Composition().Manifest().InterfacePolicies(); len(policies) != 1 || policies[0].InterfaceID().String() != "audit.write/v1" || policies[0].Timeout().String() != "2s" || policies[0].Source() != `deploy/customer.yaml interfaces.policies["audit.write/v1"].timeout` {
		t.Fatalf("full-replacement Interface policies = %#v", policies)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/audittwo.New:explicit",
		"cache.read/v1=example.com/interface-cache/cache.New:unique-compatible",
	}) {
		t.Fatalf("full-replacement selections = %v", got)
	}
	graph := resolved.InterfaceResolution().Graph()
	if got := resolvedGraphNodes(graph); !reflect.DeepEqual(got, []string{
		"example.com/interface-app/audittwo.New",
		"example.com/interface-cache/cache.New",
		"example.com/interface-app/app.New",
	}) {
		t.Fatalf("full-replacement construction order = %v", got)
	}
	dependencies := graph.ConstructionOrder()[2].Dependencies()
	if len(dependencies) != 2 || dependencies[1].InterfaceID().String() != "cache.read/v1" || !dependencies[1].Optional() || !dependencies[1].Available() || dependencies[1].Constructor().String() != "example.com/interface-cache/cache.New" {
		t.Fatalf("full-replacement optional dependency = %#v", dependencies)
	}
	for _, rootRequirement := range graph.Roots() {
		for _, source := range rootRequirement.Sources() {
			if strings.Contains(source, "missing.read/v1") || strings.HasPrefix(source, "plystra.yaml interfaces.require") {
				t.Fatalf("full replacement retained root current-project source %q", source)
			}
		}
	}
	if !reflect.DeepEqual(resolved.RootConfigurationData(), []byte(rootConfiguration)) || !reflect.DeepEqual(resolved.ConfigurationSource(), []byte(selectedConfiguration)) {
		t.Fatal("full replacement did not preserve root and selected documents independently")
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("full-replacement resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveKeepsGeneratedManifestFromOverridingCurrentInterfaceSelection(t *testing.T) {
	t.Parallel()

	root := writeResolvedInterfaceProject(t)
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
	})
	initial, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Resolve initial selection: %v", err)
	}
	if got := resolvedSelectionSummaries(initial.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/auditone.New:explicit",
	}) {
		t.Fatalf("initial selections = %v", got)
	}

	const staleApplicationModelDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("transporttoolchain.Current: %v", err)
	}
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   initial.ConfigurationSelection().Mode(),
		Environment:            initial.ConfigurationSelection().Environment(),
		RootPath:               "plystra.yaml",
		RootDigest:             initial.RootConfigurationDigest(),
		SelectedPath:           initial.ConfigurationSelection().Path(),
		SelectedDigest:         initial.ConfigurationSelection().Digest(),
		Composition:            initial.Composition(),
		ProtobufWireMapDigest:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		ApplicationModelDigest: staleApplicationModelDigest,
		InterfaceProvenance:    emptyResolvedInterfaceProvenance(t),
		TransportToolchain:     toolchain,
	})
	if err != nil {
		t.Fatalf("NewManifestProvenance: %v", err)
	}
	staleManifest, err := applicationgen.RenderManifest([]byte(`{"capability_aliases":[]}`), initial.Resolution().Context(), provenance)
	if err != nil {
		t.Fatalf("RenderManifest: %v", err)
	}
	writeFile(t, filepath.Join(root, "generated", "manifest.json"), string(staleManifest))
	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [app.run/v1]
  use:
    audit.write/v1: example.com/interface-app/audittwo.New
`)
	before := snapshotTree(t, filepath.Dir(root))

	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Resolve current selection: %v", err)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/interface-app/app.New:unique-compatible",
		"audit.write/v1=example.com/interface-app/audittwo.New:explicit",
	}) {
		t.Fatalf("current selections = %v", got)
	}
	previous := resolved.PreviousManifestProvenance()
	if previous.ApplicationModelDigest() != staleApplicationModelDigest || previous.RootDigest() == resolved.ConfigurationSelection().Digest() {
		t.Fatalf("previous manifest was not retained as stale non-authoritative provenance: previous model %q root %q; current root %q", previous.ApplicationModelDigest(), previous.RootDigest(), resolved.ConfigurationSelection().Digest())
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated Project or stale generated state:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRetainsUnrequiredImplementationsAsCandidatesOnly(t *testing.T) {
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
	writeResolvedInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/local-application/interfaces/app/run/v1"
	writev1 "example.com/local-application/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(write writev1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeResolvedSimpleImplementationForModule(t, root, "example.com/local-application", "alternate", "app.run/v1", "app/run/v1", "Run")
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
	resolution := resolved.InterfaceResolution()
	if roots := resolution.Graph().Roots(); len(roots) != 0 {
		t.Fatalf("unrequired Implementation candidates created roots = %#v", roots)
	}
	if selections := resolution.Selections(); len(selections) != 0 {
		t.Fatalf("unrequired Implementation candidates created selections = %#v", selections)
	}
	if constructors := resolution.Graph().ConstructionOrder(); len(constructors) != 0 {
		t.Fatalf("unrequired Implementation candidates entered the constructor graph = %#v", constructors)
	}
	candidates := make(map[string]bool)
	for _, implementation := range resolved.Implementations().Implementations() {
		candidates[implementation.Symbol().String()] = implementation.Local()
	}
	wantCandidates := map[string]bool{
		"example.com/interface-dependency/unused.New": false,
		"example.com/local-application/alternate.New": true,
		"example.com/local-application/app.New":       true,
	}
	if !reflect.DeepEqual(candidates, wantCandidates) {
		t.Fatalf("visible Implementation candidates = %#v, want %#v", candidates, wantCandidates)
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("candidate-only resolution mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveValidatesDormantExplicitSelectionWithoutActivation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const modulePath = "example.com/dormant-selection"
	const selectedConstructor = modulePath + "/smtp.New"
	writeModule(t, root, modulePath)
	writeResolvedInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeResolvedInterface(t, root, "reports/read/v1", "readv1", "reports.read/v1", "Read")
	writeResolvedSimpleImplementationForModule(t, root, modulePath, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeResolvedSimpleImplementationForModule(t, root, modulePath, "reports", "reports.read/v1", "reports/read/v1", "Read")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+selectedConstructor+"}}\n")
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
	})
	before := snapshotTree(t, root)

	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Resolve dormant selection: %v", err)
	}
	choices := resolved.Manifest().ImplementationChoices()
	if len(choices) != 1 || choices[0].InterfaceID().String() != "email.send/v1" || choices[0].Constructor().String() != selectedConstructor {
		t.Fatalf("effective dormant choices = %#v", choices)
	}
	resolution := resolved.InterfaceResolution()
	if len(resolution.Selections()) != 0 || len(resolution.Graph().Roots()) != 0 || len(resolution.Graph().Bindings()) != 0 || len(resolution.Graph().ConstructionOrder()) != 0 {
		t.Fatalf("dormant selection entered executable resolution = %#v", resolution)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("dormant selection resolution mutated files:\nbefore: %#v\nafter:  %#v", before, after)
	}

	invalid := []struct {
		name        string
		constructor string
		wantError   error
	}{
		{name: "invisible constructor", constructor: modulePath + "/missing.New", wantError: interfaceresolution.ErrUnknownConstructor},
		{name: "incompatible constructor", constructor: modulePath + "/reports.New", wantError: interfaceresolution.ErrIncompatibleChoice},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+test.constructor+"}}\n")
			before := snapshotTree(t, root)
			_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
				Start:       root,
				Environment: environment,
			})
			if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, test.wantError) || !containsResolutionFragments(err.Error(), "email.send/v1", test.constructor) {
				t.Fatalf("Resolve invalid dormant selection = %v", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid dormant selection mutated files:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestResolvePreservesInheritedReplacementAndRemovalForDormantSelections(t *testing.T) {
	t.Parallel()

	const (
		interfaceID          = "email.send/v1"
		inheritedConstructor = "example.com/dormant-platform/smtp.New"
		localConstructor     = "example.com/dormant-consumer/local.New"
	)
	tests := []struct {
		name            string
		configuration   string
		wantConstructor string
	}{
		{
			name:            "replacement",
			configuration:   "interfaces: {use: {email.send/v1: " + localConstructor + "}}\n",
			wantConstructor: localConstructor,
		},
		{
			name:          "removal",
			configuration: "interfaces: {use: {email.send/v1: null}}\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			dependencyRoot := filepath.Join(parent, "platform")
			writeModule(t, dependencyRoot, "example.com/dormant-platform")
			writeResolvedInterface(t, dependencyRoot, "email/send/v1", "sendv1", interfaceID, "Send")
			writeResolvedSimpleImplementationForModule(t, dependencyRoot, "example.com/dormant-platform", "smtp", interfaceID, "email/send/v1", "Send")
			writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+inheritedConstructor+"}}\n")

			applicationRoot := filepath.Join(parent, "application")
			writeFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/dormant-consumer

go 1.26

require example.com/dormant-platform v1.2.3

replace example.com/dormant-platform => ../platform
`)
			writeResolvedSimpleImplementationForInterfaceModule(t, applicationRoot, "example.com/dormant-platform", "local", interfaceID, "email/send/v1", "Send")
			writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), test.configuration)
			before := snapshotTree(t, parent)

			resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
				Start: applicationRoot,
				Environment: goEnvironment(map[string]string{
					"GOWORK":  "off",
					"GOPROXY": "off",
					"GOSUMDB": "off",
					"GOFLAGS": "-mod=readonly",
				}),
			})
			if err != nil {
				t.Fatalf("Resolve dormant inherited %s: %v", test.name, err)
			}
			choices := resolved.Manifest().ImplementationChoices()
			if test.wantConstructor == "" {
				if len(choices) != 0 {
					t.Fatalf("removed dormant choice remains effective: %#v", choices)
				}
			} else if len(choices) != 1 || choices[0].InterfaceID().String() != interfaceID || choices[0].Constructor().String() != test.wantConstructor || choices[0].Source() != `plystra.yaml interfaces.use["email.send/v1"]` {
				t.Fatalf("replacement dormant choice = %#v", choices)
			}
			resolution := resolved.InterfaceResolution()
			if len(resolution.Selections()) != 0 || len(resolution.Graph().Roots()) != 0 || len(resolution.Graph().Bindings()) != 0 || len(resolution.Graph().ConstructionOrder()) != 0 {
				t.Fatalf("dormant inherited %s entered executable resolution = %#v", test.name, resolution)
			}
			const selectionPath = `interfaces.use["email.send/v1"]`
			baseline := compositionProvenance(resolved.Composition().Provenance(), selectionPath)
			if len(baseline) != 1 || baseline[0].Removed() || len(baseline[0].Sources()) != 1 || !strings.Contains(baseline[0].Sources()[0], "example.com/dormant-platform@v1.2.3/plystra.yaml") {
				t.Fatalf("inherited dormant baseline = %#v", baseline)
			}
			if effective := compositionProvenance(resolved.Composition().ResolutionSources(), selectionPath); len(effective) != 0 {
				t.Fatalf("superseded dormant selection retained dependency resolution authority: %#v", effective)
			}
			if test.wantConstructor == "" && !strings.Contains(string(resolved.ConfigurationMaintenance().Data()), "email.send/v1: null") {
				t.Fatalf("dormant removal tombstone was not preserved:\n%s", resolved.ConfigurationMaintenance().Data())
			}
			if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
				t.Fatalf("dormant inherited %s resolution mutated files:\nbefore: %#v\nafter: %#v", test.name, before, after)
			}
		})
	}
}

func TestResolveOwnsAndValidatesDormantConstructorConfigurationWithoutRuntimeMembership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const modulePath = "example.com/dormant-configuration"
	const selectedConstructor = modulePath + "/smtp.New"
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire (\n\tgithub.com/plystra/kernel v0.0.0\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n)\n\nreplace github.com/plystra/kernel => %s\n", modulePath, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeResolvedInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeResolvedConfigurableImplementationForModule(t, root, modulePath, "smtp", "email.send/v1", "email/send/v1", "Send")
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
	})
	valid := "interfaces: {use: {email.send/v1: " + selectedConstructor + "}}\nconfig: {" + selectedConstructor + ": {endpoint: smtp.internal, password: {env: PLYSTRA_DORMANT_SECRET}}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), valid)
	before := snapshotTree(t, root)

	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatalf("Resolve dormant constructor configuration without a Secret value: %v", err)
	}
	configured, exists := resolved.Manifest().Configuration(mustResolvedConstructorSymbol(t, selectedConstructor))
	if !exists || !strings.Contains(string(configured.YAML()), "endpoint: smtp.internal") || !strings.Contains(string(configured.YAML()), "PLYSTRA_DORMANT_SECRET") {
		t.Fatalf("effective dormant constructor configuration = %s, %t", configured.YAML(), exists)
	}
	resolution := resolved.InterfaceResolution()
	if len(resolution.Selections()) != 0 || len(resolution.Graph().Roots()) != 0 || len(resolution.Graph().Bindings()) != 0 || len(resolution.Graph().ConstructionOrder()) != 0 {
		t.Fatalf("dormant constructor configuration entered executable resolution = %#v", resolution)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("dormant constructor configuration resolution mutated files:\nbefore: %#v\nafter:  %#v", before, after)
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "config: {"+selectedConstructor+": {endpoint: smtp.internal}}\n")
	before = snapshotTree(t, root)
	_, err = applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
	if !errors.Is(err, applicationresolve.ErrUnownedConstructorConfiguration) || !strings.Contains(err.Error(), selectedConstructor) || strings.Contains(err.Error(), "smtp.internal") {
		t.Fatalf("Resolve unowned constructor configuration error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unowned constructor configuration resolution mutated files:\nbefore: %#v\nafter:  %#v", before, after)
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+selectedConstructor+"}}\nconfig: {"+selectedConstructor+": {endpoint: true}}\n")
	before = snapshotTree(t, root)
	_, err = applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
	if !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || strings.Contains(err.Error(), "true") {
		t.Fatalf("Resolve invalid dormant constructor configuration error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid dormant constructor configuration resolution mutated files:\nbefore: %#v\nafter:  %#v", before, after)
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [email.send/v1]}\nconfig: {"+selectedConstructor+": {endpoint: smtp.internal}}\n")
	resolved, err = applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatalf("Resolve reachable uniquely selected constructor configuration: %v", err)
	}
	if got := resolvedGraphNodes(resolved.InterfaceResolution().Graph()); !reflect.DeepEqual(got, []string{selectedConstructor}) {
		t.Fatalf("reachable configured constructor graph = %v", got)
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
		writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
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

func TestResolveAppliesNoModuleDirectnessOrDepthSelectionPriority(t *testing.T) {
	t.Parallel()

	topologies := []struct {
		name   string
		direct string
		deep   string
	}{
		{name: "alpha direct and zeta deeply transitive", direct: "alpha", deep: "zeta"},
		{name: "zeta direct and alpha deeply transitive", direct: "zeta", deep: "alpha"},
	}
	var baseline string
	for index, topology := range topologies {
		parent, root := writeModuleTopologyAmbiguityProject(t, topology.direct, topology.deep)
		before := snapshotTree(t, parent)
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start: root,
			Environment: goEnvironment(map[string]string{
				"GOWORK":  "off",
				"GOPROXY": "off",
				"GOSUMDB": "off",
			}),
		})
		if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, interfaceresolution.ErrAmbiguousImplementation) {
			t.Fatalf("Resolve topology %q error = %v", topology.name, err)
		}
		var ambiguous *interfaceresolution.AmbiguousImplementationError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("Resolve topology %q omitted typed ambiguity: %v", topology.name, err)
		}
		candidates := ambiguous.Candidates()
		if len(candidates) != 2 || candidates[0].Constructor().String() != "example.com/topology-alpha/alpha.New" || candidates[1].Constructor().String() != "example.com/topology-zeta/zeta.New" {
			t.Fatalf("Resolve topology %q candidates = %#v", topology.name, candidates)
		}
		if index == 0 {
			baseline = err.Error()
		} else if err.Error() != baseline {
			t.Fatalf("module directness or transitive depth changed ambiguity:\nfirst:  %s\nsecond: %s", baseline, err)
		}
		if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
			t.Fatalf("Resolve topology %q mutated Projects:\nbefore: %#v\nafter: %#v", topology.name, before, after)
		}
	}
}

func TestResolveAppliesNoOfficialOwnershipOrTemplateOriginSelectionPriority(t *testing.T) {
	t.Parallel()

	parent, root := writeOwnershipAmbiguityProject(t)
	before := snapshotTree(t, parent)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: root,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
		}),
	})
	if !errors.Is(err, interfaceresolution.ErrResolve) || !errors.Is(err, interfaceresolution.ErrAmbiguousImplementation) {
		t.Fatalf("Resolve error = %v", err)
	}
	var ambiguous *interfaceresolution.AmbiguousImplementationError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("Resolve omitted typed ambiguity: %v", err)
	}
	candidates := ambiguous.Candidates()
	if len(candidates) != 2 || candidates[0].Constructor().String() != "example.com/template-origin/template.New" || candidates[1].Constructor().String() != "github.com/plystra/official-implementation/official.New" {
		t.Fatalf("candidates = %#v", candidates)
	}
	if !containsResolutionFragments(err.Error(),
		"example.com/template-origin/template.New",
		"github.com/plystra/official-implementation/official.New",
		`interfaces.use["app.run/v1"]`,
	) {
		t.Fatalf("ambiguity omitted equal candidates or explicit correction: %v", err)
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated Projects:\nbefore: %#v\nafter: %#v", before, after)
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

func TestResolveIgnoresDependencyExposureWithoutConsumerResolutionState(t *testing.T) {
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
	if roots := resolved.InterfaceResolution().Graph().Roots(); len(roots) != 0 {
		t.Fatalf("dependency exposure created consumer roots = %#v", roots)
	}
	if got := resolvedSelectionSummaries(resolved.InterfaceResolution()); len(got) != 0 {
		t.Fatalf("dependency exposure created consumer selections = %v", got)
	}
	if exposures := resolved.Manifest().HTTPExposures(); len(exposures) != 0 {
		t.Fatalf("dependency exposure entered the effective consumer manifest = %#v", exposures)
	}
	if resolved.ConfigurationMaintenance().Changed() {
		t.Fatal("dependency exposure entered planned consumer maintenance")
	}
	for _, record := range resolved.Composition().Provenance() {
		if record.Path() == `http.expose["app.run/v1"]` {
			t.Fatalf("dependency exposure entered consumer composition provenance: %#v", record)
		}
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("dependency exposure resolution mutated current Project:\nbefore: %#v\nafter: %#v", before, after)
	}
	if after := snapshotTree(t, dependencyRoot); !reflect.DeepEqual(after, dependencyBefore) {
		t.Fatalf("dependency exposure resolution mutated dependency Project:\nbefore: %#v\nafter: %#v", dependencyBefore, after)
	}
}

func TestResolveComposesEveryDependencyProjectRootThroughInterfaceModel(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	applicationRoot := filepath.Join(parent, "application")
	directRoot := filepath.Join(parent, "direct")
	transitiveRoot := filepath.Join(parent, "transitive")
	ordinaryRoot := filepath.Join(parent, "ordinary")

	writeModule(t, transitiveRoot, "example.com/transitive")
	writeResolvedInterface(t, transitiveRoot, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(transitiveRoot, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/transitive/interfaces/audit/write/v1"
)

type Config struct {
	Endpoint string `+"`plystra:\"required\"`"+`
}

type Service struct{}

//plystra:implements audit.write/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(transitiveRoot, "plystra.yaml"), `http:
  address: ":7101"
interfaces:
  require: [audit.write/v1]
  use:
    audit.write/v1: example.com/transitive/audit.New
config:
  example.com/transitive/audit.New:
    endpoint: audit.internal
`)
	writeFile(t, filepath.Join(transitiveRoot, "plystra.production.yaml"), "this: [dependency overlay is deliberately invalid\n")

	writeFile(t, filepath.Join(directRoot, "go.mod"), `module example.com/direct

go 1.26

require example.com/transitive v1.4.0
`)
	writeResolvedInterface(t, directRoot, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeFile(t, filepath.Join(directRoot, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/direct/interfaces/app/run/v1"
)

type Config struct {
	Message string
}

type Service struct{}

//plystra:implements app.run/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(directRoot, "plystra.yaml"), `http:
  address: ":7102"
  expose: [app.run/v1]
interfaces:
  use:
    app.run/v1: example.com/direct/app.New
  policies:
    app.run/v1: {timeout: 5s}
config:
  example.com/direct/app.New:
    message: direct-root
`)
	writeFile(t, filepath.Join(directRoot, "plystra.production.yaml"), "this: [dependency overlay is deliberately invalid\n")

	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writeFile(t, filepath.Join(ordinaryRoot, "plystra.production.yaml"), "this: [ordinary module overlay is deliberately invalid\n")
	writeFile(t, filepath.Join(ordinaryRoot, "broken.go"), "package ordinary\n")

	writeFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/application

go 1.26

require (
	example.com/direct v1.2.0
	example.com/ordinary v1.0.0
)

replace example.com/direct => ../direct
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
`)
	writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), `http:
  address: ":9090"
  transports: {connect: true}
  expose: [app.run/v1]
timeouts:
  startup: 7s
`)

	applicationBefore := snapshotTree(t, applicationRoot)
	directBefore := snapshotTree(t, directRoot)
	transitiveBefore := snapshotTree(t, transitiveRoot)
	ordinaryBefore := snapshotTree(t, ordinaryRoot)
	options := applicationresolve.Options{
		Start: applicationRoot,
		Environment: goEnvironment(map[string]string{
			"GOWORK":  "off",
			"GOPROXY": "off",
			"GOSUMDB": "off",
			"GOFLAGS": "-mod=readonly",
		}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	projects := first.Dependencies().Projects()
	if len(projects) != 2 || projects[0].Path() != "example.com/direct" || projects[0].SelectedVersion() != "v1.2.0" || projects[1].Path() != "example.com/transitive" || projects[1].SelectedVersion() != "v1.4.0" || projects[1].Direct() {
		t.Fatalf("dependency Projects = %#v", projects)
	}
	if address, exists := first.Manifest().HTTPAddress(); !exists || address != ":9090" || first.Manifest().StartupTimeout().String() != "7s" {
		t.Fatalf("current-project process settings = address %q/%t startup %s", address, exists, first.Manifest().StartupTimeout())
	}
	if got := resolvedSelectionSummaries(first.InterfaceResolution()); !reflect.DeepEqual(got, []string{
		"app.run/v1=example.com/direct/app.New:explicit",
		"audit.write/v1=example.com/transitive/audit.New:explicit",
	}) {
		t.Fatalf("dependency selections = %v", got)
	}
	roots := first.InterfaceResolution().Graph().Roots()
	if len(roots) != 2 || roots[0].InterfaceID().String() != "app.run/v1" || !reflect.DeepEqual(roots[0].Sources(), []string{`plystra.yaml http.expose["app.run/v1"]`}) || roots[1].InterfaceID().String() != "audit.write/v1" {
		t.Fatalf("current and inherited Interface roots = %#v", roots)
	}
	policies := first.Manifest().InterfacePolicies()
	if len(policies) != 1 || policies[0].InterfaceID().String() != "app.run/v1" || policies[0].Timeout().String() != "5s" {
		t.Fatalf("dependency policies = %#v", policies)
	}
	for _, selection := range first.InterfaceResolution().Selections() {
		configured, exists := first.Manifest().Configuration(selection.Constructor)
		switch selection.InterfaceID.String() {
		case "app.run/v1":
			if !exists || !strings.Contains(string(configured.YAML()), "message: direct-root") {
				t.Fatalf("direct constructor configuration = %s, %t", configured.YAML(), exists)
			}
		case "audit.write/v1":
			if !exists || !strings.Contains(string(configured.YAML()), "endpoint: audit.internal") {
				t.Fatalf("transitive constructor configuration = %s, %t", configured.YAML(), exists)
			}
		default:
			t.Fatalf("unexpected selected Interface %s", selection.InterfaceID)
		}
	}
	for path, module := range map[string]string{
		`interfaces.require["audit.write/v1"]`:                   "example.com/transitive@v1.4.0",
		`interfaces.use["app.run/v1"]`:                           "example.com/direct@v1.2.0",
		`interfaces.use["audit.write/v1"]`:                       "example.com/transitive@v1.4.0",
		`interfaces.policies["app.run/v1"].timeout`:              "example.com/direct@v1.2.0",
		`config["example.com/direct/app.New"]["message"]`:        "example.com/direct@v1.2.0",
		`config["example.com/transitive/audit.New"]["endpoint"]`: "example.com/transitive@v1.4.0",
	} {
		records := compositionProvenance(first.Composition().Provenance(), path)
		if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.Contains(records[0].Sources()[0], module+"/plystra.yaml") {
			t.Fatalf("dependency provenance for %s = %#v", path, records)
		}
	}
	if records := compositionProvenance(first.Composition().Provenance(), `http.expose["app.run/v1"]`); len(records) != 0 {
		t.Fatalf("dependency-owned exposure entered composition provenance: %#v", records)
	}
	if first.Composition().DependencyDigest() == "" || first.Composition().DependencyDigest() != second.Composition().DependencyDigest() || !reflect.DeepEqual(first.Composition().Provenance(), second.Composition().Provenance()) {
		t.Fatalf("dependency composition is not deterministic: first %#v second %#v", first.Composition().Provenance(), second.Composition().Provenance())
	}
	for name, snapshot := range map[string]struct {
		root   string
		before []treeEntry
	}{
		"application": {root: applicationRoot, before: applicationBefore},
		"direct":      {root: directRoot, before: directBefore},
		"transitive":  {root: transitiveRoot, before: transitiveBefore},
		"ordinary":    {root: ordinaryRoot, before: ordinaryBefore},
	} {
		if after := snapshotTree(t, snapshot.root); !reflect.DeepEqual(after, snapshot.before) {
			t.Fatalf("Resolve mutated %s module:\nbefore: %#v\nafter: %#v", name, snapshot.before, after)
		}
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
  policies:
    audit.write/v1: {timeout: 5s}
`)
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), `interfaces:
  require: [cache.read/v1]
  use:
    audit.write/v1: example.com/interface-app/audittwo.New
  policies:
    audit.write/v1: {timeout: 2s}
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

func writeModuleTopologyAmbiguityProject(t testing.TB, direct, deep string) (string, string) {
	t.Helper()
	parent := t.TempDir()

	contractRoot := filepath.Join(parent, "contract")
	writeModule(t, contractRoot, "example.com/topology-contract")
	writeFile(t, filepath.Join(contractRoot, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, contractRoot, "app/run/v1", "runv1", "app.run/v1", "Run")

	for _, name := range []string{"alpha", "zeta"} {
		root := filepath.Join(parent, name)
		modulePath := "example.com/topology-" + name
		writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module %s

go 1.26

require example.com/topology-contract v1.0.0

replace example.com/topology-contract => ../contract
`, modulePath))
		writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
		writeResolvedSimpleImplementationForInterfaceModule(t, root, "example.com/topology-contract", name, "app.run/v1", "app/run/v1", "Run")
	}

	middleRoot := filepath.Join(parent, "middle")
	writeFile(t, filepath.Join(middleRoot, "go.mod"), fmt.Sprintf(`module example.com/topology-middle

go 1.16

require example.com/topology-%s v1.0.0
`, deep))
	writeFile(t, filepath.Join(middleRoot, "middle.go"), "package middle\n")

	bridgeRoot := filepath.Join(parent, "bridge")
	writeFile(t, filepath.Join(bridgeRoot, "go.mod"), `module example.com/topology-bridge

go 1.16

require example.com/topology-middle v1.0.0
`)
	writeFile(t, filepath.Join(bridgeRoot, "bridge.go"), "package bridge\n")

	applicationRoot := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(applicationRoot, "go.mod"), fmt.Sprintf(`module example.com/topology-application

go 1.26

require (
	example.com/topology-%s v1.0.0
	example.com/topology-bridge v1.0.0
)

replace example.com/topology-alpha => ../alpha

replace example.com/topology-bridge => ../bridge

replace example.com/topology-contract => ../contract

replace example.com/topology-middle => ../middle

replace example.com/topology-zeta => ../zeta
`, direct))
	writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	return parent, applicationRoot
}

func writeOwnershipAmbiguityProject(t testing.TB) (string, string) {
	t.Helper()
	parent := t.TempDir()

	contractRoot := filepath.Join(parent, "contract")
	writeModule(t, contractRoot, "example.com/ownership-contract")
	writeFile(t, filepath.Join(contractRoot, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, contractRoot, "app/run/v1", "runv1", "app.run/v1", "Run")

	implementations := []struct {
		directory   string
		modulePath  string
		packageName string
	}{
		{directory: "template", modulePath: "example.com/template-origin", packageName: "template"},
		{directory: "official", modulePath: "github.com/plystra/official-implementation", packageName: "official"},
	}
	for _, implementation := range implementations {
		root := filepath.Join(parent, implementation.directory)
		writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module %s

go 1.26

require example.com/ownership-contract v1.0.0

replace example.com/ownership-contract => ../contract
`, implementation.modulePath))
		writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
		writeResolvedSimpleImplementationForInterfaceModule(t, root, "example.com/ownership-contract", implementation.packageName, "app.run/v1", "app/run/v1", "Run")
	}

	applicationRoot := filepath.Join(parent, "application")
	writeFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/ownership-application

go 1.26

require (
	example.com/ownership-contract v1.0.0
	example.com/template-origin v1.0.0
	github.com/plystra/official-implementation v1.0.0
)

replace example.com/ownership-contract => ../contract

replace example.com/template-origin => ../template

replace github.com/plystra/official-implementation => ../official
`)
	writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	return parent, applicationRoot
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
	writeResolvedSimpleImplementationForInterfaceModule(t, root, modulePath, packageName, identifier, interfacePath, method)
}

func writeResolvedSimpleImplementationForInterfaceModule(t testing.TB, root, interfaceModulePath, packageName, identifier, interfacePath, method string) {
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
`, packageName, interfaceModulePath, interfacePath, identifier, method))
}

func writeResolvedConfigurableImplementationForModule(t testing.TB, root, modulePath, packageName, identifier, interfacePath, method string) {
	t.Helper()
	writeFile(t, filepath.Join(root, packageName, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "%s/interfaces/%s"
	"github.com/plystra/kernel/configuration"
)

type Config struct {
	Endpoint string `+"`plystra:\"required\"`"+`
	Password configuration.Secret
}

type Service struct{}

//plystra:implements %s
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) %s(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, packageName, modulePath, interfacePath, identifier, method))
}

func mustResolvedConstructorSymbol(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatalf("parse constructor symbol %q: %v", value, err)
	}
	return symbol
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

func emptyResolvedInterfaceProvenance(t testing.TB) interfaceprovenance.Provenance {
	t.Helper()
	provenance, err := interfaceprovenance.New(interfaceprovenance.Input{})
	if err != nil {
		t.Fatalf("interfaceprovenance.New: %v", err)
	}
	return provenance
}
