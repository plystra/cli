package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
)

func TestResolveNamedResourcesAcrossSelectedLayers(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "direct")
			writeResourceProvider(t, root)
			document := `interfaces: {require: [app.resource/v1]}
resources:
  instances:
    database.primary:
      use: example.com/resource-consumer/provider.New
      config: {value: private-primary, nested: {label: private-label}}
    database.replica:
      use: example.com/resource-consumer/provider.New
      config: {value: private-replica}
  bind:
    implementations:
      example.com/resource-consumer/consumer.New: {primary: database.primary, Replica: database.primary}
`
			options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
			path := "plystra.yaml"
			switch mode {
			case "environment":
				path, options.EnvironmentName = "plystra.test.yaml", "test"
			case "replacement":
				path, options.ConfigurationPath = "selected.yaml", "selected.yaml"
			}
			writeFile(t, filepath.Join(root, path), document)
			before := snapshotTree(t, root)
			result, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			graph := result.InterfaceResolution().Graph()
			resources := graph.ResourceConstructionOrder()
			if len(resources) != 2 || resources[0].Name() != "database.primary" || resources[1].Name() != "database.replica" || resources[0].Provider().Symbol() != resources[1].Provider().Symbol() {
				t.Fatal("same-provider instances were not independently selected")
			}
			if len(graph.ConstructionOrder()) != 1 || len(graph.Bindings()) != 1 {
				t.Fatal("Resource changed Interface membership")
			}
			dependencies := graph.ResourceDependencies(graph.ConstructionOrder()[0].Symbol())
			if len(dependencies) != 2 || dependencies[0].InstanceName() != "database.primary" || dependencies[1].InstanceName() != "database.primary" || dependencies[0].ParameterName() != "primary" || dependencies[1].ParameterName() != "Replica" {
				t.Fatal("exact shared Resource bindings were lost")
			}
			evidence := result.ResolutionEvidence()
			if len(evidence.Resources()) != 2 || len(evidence.ResourceBindings()) != 2 {
				t.Fatal("Resource evidence lost selected instances or bindings")
			}
			for _, private := range []string{"private-primary", "private-replica", "private-label", "PRIVATE_PROVIDER_ENTRY"} {
				if strings.Contains(string(evidence.CanonicalJSON()), private) {
					t.Fatal("Resource evidence exposed private input")
				}
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("Resource resolution changed authored input")
			}
		})
	}
}

func TestResolveResourcesValidatesUnconsumedAndDormantBindings(t *testing.T) {
	t.Parallel()
	for name, document := range map[string]string{
		"unconsumed":                "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New, config: {value: private}}}}\n",
		"dormant binding":           "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New, config: {value: private}}}, bind: {implementations: {example.com/resource-consumer/consumer.New: {primary: database.primary}}}}\n",
		"invalid dormant parameter": "resources: {instances: {database.primary: {use: example.com/resource-consumer/provider.New, config: {value: private}}}, bind: {implementations: {example.com/resource-consumer/consumer.New: {replica: database.primary}}}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "direct")
			writeResourceProvider(t, root)
			writeFile(t, filepath.Join(root, "plystra.yaml"), document)
			result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: goEnvironment(nil)})
			if name == "invalid dormant parameter" {
				if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
					t.Fatalf("dormant invalid binding = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.InterfaceResolution().Graph().ResourceConstructionOrder()) != 1 || len(result.InterfaceResolution().Graph().ConstructionOrder()) != 0 {
				t.Fatal("selected Resource was inactive or dormant consumer activated")
			}
		})
	}
}

func TestResolveResourceProviderCannotOwnOrdinaryConfiguration(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"{value: PRIVATE_INVALID_OWNER}", "{$remove: true}"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(mode+"/"+value, func(t *testing.T) {
				t.Parallel()
				root := writeResourceConsumerProject(t, "direct")
				writeResourceProvider(t, root)
				options := applicationresolve.Options{Start: root, Environment: goEnvironment(nil)}
				document, path := "config: {example.com/resource-consumer/provider.New: "+value+"}\n", "plystra.yaml"
				switch mode {
				case "environment":
					options.EnvironmentName, path = "test", "plystra.test.yaml"
				case "replacement":
					options.ConfigurationPath, path = "selected.yaml", "selected.yaml"
				}
				writeFile(t, filepath.Join(root, path), document)
				before := snapshotTree(t, root)
				_, err := applicationresolve.Resolve(t.Context(), options)
				if value == "{$remove: true}" && mode != "environment" {
					if !errors.Is(err, applicationmeta.ErrInvalidManifest) {
						t.Fatalf("root or replacement Resource tombstone was not rejected as invalid metadata: %v", err)
					}
				} else if !errors.Is(err, applicationmeta.ErrConfigurationSchema) || strings.Contains(err.Error(), "PRIVATE_INVALID_OWNER") {
					t.Fatalf("Resource provider accepted under ordinary config: %v", err)
				}
				if !reflect.DeepEqual(before, snapshotTree(t, root)) {
					t.Fatal("namespace failure mutated Project")
				}
			})
		}
	}
}

func writeResourceProvider(t testing.TB, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "provider/provider.go"), strings.ReplaceAll(`package provider
type Nested struct {
 Label string @@yaml:"label"@@
 Count int @@yaml:"count"@@
}
type Config struct {
 Value string @@yaml:"value" plystra:"required"@@
 Nested Nested @@yaml:"nested"@@
}
type value struct{}
//plystra:implements-resource storage.database/v1
func New(c Config) (*value,error) { panic("PRIVATE_PROVIDER_ENTRY") }
func (*value) Health() error { return nil }
`, "@@", "`"))
}
