package applicationresolve_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
)

func selectionOptions(root string) applicationresolve.Options {
	return applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
}

func TestSelectionInputsRepairGraphBeforeRequiredValueValidation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement", "explicit-root"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			writeResourceProvider(t, root)
			options, path := selectionOptions(root), "plystra.yaml"
			switch mode {
			case "environment":
				options.EnvironmentName, path = "test", "plystra.test.yaml"
			case "replacement":
				options.ConfigurationPath, path = "deploy/app.yaml", "deploy/app.yaml"
			case "explicit-root":
				options.ConfigurationPath = path
			}
			old := "interfaces: {require: [app.entry/v1]}\n"
			writeFile(t, filepath.Join(root, path), old)
			// Discovery must not depend on old generation output or run package init.
			writeFile(t, filepath.Join(root, "generated/manifest.json"), "PRIVATE_INVALID_HISTORY")
			writeFile(t, filepath.Join(root, "provider/init.go"), "package provider\nfunc init() { panic(\"PRIVATE_INIT_ENTRY\") }\n")
			before := snapshotTree(t, root)
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			if inputs.SelectedSnapshot().Path() != path || string(inputs.SelectedSnapshot().Data()) != old || inputs.ConfigurationSelection().Digest() != "" {
				t.Fatal("selected preimage or uncomposed selector changed")
			}
			composition, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
			if err != nil {
				t.Fatal(err)
			}
			owners, err := inputs.CandidateOwners(composition)
			if err != nil || len(owners) != 2 {
				t.Fatalf("Resource-independent owners = %v, %v", owners, err)
			}
			_, err = inputs.ResolveCandidateInterfaces(composition)
			var missing *constructorgraph.ResourceBindingError
			if !errors.Is(err, constructorgraph.ErrMissingResourceBinding) || !errors.As(err, &missing) || len(missing.RequirementSources()) != 1 || missing.RequirementSources()[0].Path != path || len(missing.Steps()) != 1 {
				t.Fatalf("old missing Resource evidence = %v", err)
			}
			candidate := old + "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New}}}\n"
			composition, err = inputs.ComposeCandidate([]byte(candidate))
			if err != nil {
				t.Fatal(err)
			}
			graph, err := inputs.ResolveCandidateInterfaces(composition)
			if err != nil || len(graph.Graph().ResourceConstructionOrder()) != 1 {
				t.Fatalf("graph requires Config values: %v", err)
			}
			if err := inputs.ValidateCandidate(composition); !errors.Is(err, applicationmeta.ErrConfigurationRequired) {
				t.Fatalf("final validation missed required Resource value: %v", err)
			}
			candidate = strings.Replace(candidate, "provider.New}", "provider.New, config: {value: private-planned-value}}", 1)
			composition, err = inputs.ComposeCandidate([]byte(candidate))
			if err != nil {
				t.Fatal(err)
			}
			if err := inputs.ValidateCandidate(composition); err != nil {
				t.Fatal(err)
			}
			if err := inputs.ValidateSnapshot(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("candidate planning mutated authored input or executed package code")
			}
		})
	}
}

func TestSelectionInputsOwnersIgnoreInvalidDormantResourceBindings(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "direct")
	writeResourceProvider(t, root)
	document := `interfaces: {use: {app.resource/v1: example.com/resource-consumer/consumer.New}}
resources:
  bind:
    implementations:
      example.com/resource-consumer/consumer.New: {obsolete: missing.instance}
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	composition, err := inputs.ComposeCandidate([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	owners, err := inputs.CandidateOwners(composition)
	if err != nil || len(owners) != 1 || owners[0].String() != "example.com/resource-consumer/consumer.New" {
		t.Fatalf("dormant ownership blocked by stale Resource binding: %v, %v", owners, err)
	}
	if err := inputs.ValidateCandidate(composition); !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
		t.Fatalf("final validation accepted invalid dormant binding: %v", err)
	}
}

func TestSelectionInputsDeferConfigOwnershipAndRejectItFinally(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "direct")
	path := filepath.Join(root, "consumer/service.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "type Service struct{}", "type Config struct { Value string `yaml:\"value\" plystra:\"required\"` }\ntype Service struct{}", 1)
	source = strings.Replace(source, "func New(primary", "func New(c Config, primary", 1)
	writeFile(t, path, source)
	document := "config: {example.com/resource-consumer/consumer.New: {value: private-unowned}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	composition, err := inputs.ComposeCandidate([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inputs.ResolveCandidateInterfaces(composition); err != nil {
		t.Fatalf("graph-only resolution enforced ownership: %v", err)
	}
	if err := inputs.ValidateCandidate(composition); !errors.Is(err, applicationresolve.ErrUnownedConstructorConfiguration) || strings.Contains(err.Error(), "private-unowned") {
		t.Fatalf("final owner validation = %v", err)
	}
	composition, err = inputs.ComposeCandidate([]byte(document + "interfaces: {use: {app.resource/v1: example.com/resource-consumer/consumer.New}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inputs.ValidateCandidate(composition); err != nil {
		t.Fatalf("dormant explicit ownership rejected: %v", err)
	}
}

func TestSelectionInputsExposeRawInvalidConfigWithoutRelaxingComposition(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "direct")
	writeResourceProvider(t, root)
	document := "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New, config: {value: [PRIVATE_WRONG_TYPE]}}}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	before := snapshotTree(t, root)
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := inputs.CurrentLayers()[0].ResourceInstances()[0]; got.Name() != "database.primary" || got.Provider().String() != "example.com/resource-consumer/provider.New" {
		t.Fatal("invalid Config hid raw selected identities")
	}
	if _, err := inputs.ComposeCandidate([]byte(document)); !errors.Is(err, applicationmeta.ErrConfigurationValues) || strings.Contains(err.Error(), "PRIVATE_WRONG_TYPE") {
		t.Fatalf("typed composition accepted old invalid Config: %v", err)
	}
	composition, err := inputs.ComposeCandidate([]byte(strings.Replace(document, "[PRIVATE_WRONG_TYPE]", "private-fixed", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := inputs.ValidateCandidate(composition); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{inputs, inputs.RootSnapshot(), inputs.SelectedSnapshot(), inputs.ModuleMetadata(), inputs.RootManifest()} {
		formatted := fmt.Sprintf("%v %+v %#v", value, value, value)
		if strings.Contains(formatted, "PRIVATE_WRONG_TYPE") || strings.Contains(formatted, root) {
			t.Fatal("selection input formatting disclosed private data")
		}
	}
	data := inputs.SelectedSnapshot().Data()
	data[0] = '!'
	layers := inputs.CurrentLayers()
	layers[0] = applicationmeta.Manifest{}
	metadata := inputs.ModuleMetadata()
	metadata[0] = applicationresolve.ModuleMetadataSnapshot{}
	if string(inputs.SelectedSnapshot().Data()) != document || len(inputs.CurrentLayers()[0].ResourceInstances()) != 1 || inputs.ModuleMetadata()[0].ModulePath() != "example.com/resource-consumer" {
		t.Fatal("selection input accessors are not defensive")
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("invalid Config repair planning mutated input")
	}
}

func TestSelectionInputsStrictSyntaxAndGoValidation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"syntax", "schema", "Go"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "direct")
			options := selectionOptions(root)
			switch scenario {
			case "syntax":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: [\n")
			case "schema":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: PRIVATE_INVALID_SHAPE\n")
			case "Go":
				writeFile(t, filepath.Join(root, "consumer/service.go"), "package consumer\n//plystra:implements app.resource/v1\nfunc New() bool { return false }\n")
			}
			before := snapshotTree(t, root)
			_, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err == nil || !errors.Is(err, applicationresolve.ErrResolve) || strings.Contains(err.Error(), "PRIVATE_INVALID_SHAPE") {
				t.Fatalf("invalid authored input accepted or exposed: %v", err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("invalid discovery mutated input")
			}
		})
	}
}

func TestSelectionInputsRejectEmptyAndCancelledCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, c := range []context.Context{nil, ctx} {
		if _, err := applicationresolve.DiscoverSelectionInputs(c, applicationresolve.Options{}); !errors.Is(err, applicationresolve.ErrResolve) {
			t.Fatalf("invalid discovery context = %v", err)
		}
		if err := (applicationresolve.SelectionInputs{}).ValidateSnapshot(c); !errors.Is(err, applicationresolve.ErrResolve) {
			t.Fatalf("invalid snapshot context = %v", err)
		}
	}
	var inputs applicationresolve.SelectionInputs
	if _, err := inputs.ComposeCandidate([]byte("{}")); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty input composed")
	}
	if _, err := inputs.CandidateOwners(applicationmeta.Composition{}); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty input resolved owners")
	}
	if err := inputs.ValidateCandidate(applicationmeta.Composition{}); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty input validated candidate")
	}
	if err := inputs.ValidateSnapshot(t.Context()); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatal("empty input validated snapshot")
	}
}

func TestSelectionInputsPreservePartialOwnersAndMissingInterfaceSources(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	if err := os.Remove(filepath.Join(root, "consumer/service.go")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.entry/v1]}\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	composition, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
	if err != nil {
		t.Fatal(err)
	}
	owners, err := inputs.CandidateOwners(composition)
	if !errors.Is(err, constructorgraph.ErrMissingBinding) || len(owners) != 1 || owners[0].String() != "example.com/resource-consumer/entry.New" {
		t.Fatalf("partial known-positive owner lost: %v, %v", owners, err)
	}
	if err := inputs.ValidateCandidate(composition); !errors.Is(err, constructorgraph.ErrMissingBinding) {
		t.Fatalf("partial closure treated as final validity: %v", err)
	}
}

func TestSelectionInputsUseCurrentProjectLayersAndIgnoreDependencyConfiguration(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement", "ambient"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			for _, name := range []string{"app", "oldest", "nearest", "ordinary"} {
				writeModule(t, filepath.Join(parent, name), "example.com/"+name)
			}
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\ngo 1.26\nrequire (\n example.com/oldest v1.0.0\n example.com/nearest v1.0.0\n example.com/ordinary v1.0.0\n)\nreplace example.com/oldest => ../oldest\nreplace example.com/nearest => ../nearest\nreplace example.com/ordinary => ../ordinary\n")
			writeFile(t, filepath.Join(parent, "oldest/plystra.yaml"), "config: PRIVATE_INACTIVE_OLDEST\n")
			writeFile(t, filepath.Join(parent, "nearest/plystra.yaml"), "config: PRIVATE_INACTIVE_NEAREST\n")
			writeFile(t, filepath.Join(parent, "ordinary/plystra.yaml"), "config: PRIVATE_INACTIVE_ORDINARY\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.info/v1]}\n")
			writePlugin(t, root, "legacy", "id: example.legacy\nprovides: [legacy.run/v1]\ngeneration:\n  api: v1\n  package: ./generation\n  activations:\n    - namespace: legacy\n      capability: legacy.run/v1\n")
			writeFile(t, filepath.Join(root, "legacy/generation/generate.go"), "package generation\nfunc init() { panic(\"MUST_NOT_EXECUTE_LEGACY\") }\n")
			options := selectionOptions(root)
			selected := "http: {expose: {legacy.run/v1: {transport: connect}}}\n"
			switch mode {
			case "default":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.info/v1]}\n"+selected)
			case "environment", "ambient":
				writeFile(t, filepath.Join(root, "plystra.test.yaml"), "interfaces: {require: {add: [kernel.health/v1]}}\n"+selected)
				if mode == "ambient" {
					options.Environment = append(options.Environment, "PLYSTRA_ENV=test")
				} else {
					options.EnvironmentName = "test"
				}
			case "replacement":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.info/v1]}\nconfig: PRIVATE_EXCLUDED_WRONG_TYPE\n")
				writeFile(t, filepath.Join(root, "selected.yaml"), "interfaces: {require: [kernel.health/v1]}\n"+selected)
				options.ConfigurationPath = "selected.yaml"
			}
			before := snapshotTree(t, parent)
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			wantLayers := 1
			if mode == "environment" || mode == "ambient" {
				wantLayers = 2
			}
			if len(inputs.CurrentLayers()) != wantLayers || len(inputs.ModuleMetadata()) != 4 {
				t.Fatal("captured layers or module read set incomplete")
			}
			composition, err := inputs.ComposeCandidate(inputs.SelectedSnapshot().Data())
			if err != nil {
				t.Fatal(err)
			}
			result, err := inputs.ResolveCandidateInterfaces(composition)
			if err != nil {
				t.Fatalf("legacy exposure was treated as an authored Interface: %v", err)
			}
			wantRoots := 1
			if mode == "environment" || mode == "ambient" {
				wantRoots = 2
			}
			if mode == "replacement" {
				wantRoots = 1
			}
			if len(composition.Manifest().InterfaceRequirements()) != wantRoots || len(result.Graph().Roots()) != 0 {
				t.Fatalf("current-project requirements or legacy exposure changed")
			}
			if !reflect.DeepEqual(before, snapshotTree(t, parent)) {
				t.Fatal("discovery changed authored files")
			}
		})
	}
}
