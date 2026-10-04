package bootstrapgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/resourceproviderdecl"
	"github.com/plystra/cli/internal/resourceproviderinventory"
	"github.com/plystra/cli/internal/runtimebaseline"
	"github.com/plystra/cli/internal/transportprovenance"
	"go.yaml.in/yaml/v3"
)

type resourcePackages map[string]*types.Package

func (p resourcePackages) Import(path string) (*types.Package, error) {
	if pkg := p[path]; pkg != nil {
		return pkg, nil
	}
	return nil, fmt.Errorf("unknown package %s", path)
}

const resourceConfigSource = "type Nested struct { Left string `yaml:\"left\"`; Right string `yaml:\"right\"` }\n" +
	"type Config struct {\n" +
	"Value string `yaml:\"value\" plystra:\"required\"`\n" +
	"Count int32 `yaml:\"count\" plystra:\"build-visible\" plystra-default:\"7\"`\n" +
	"Private string `yaml:\"private\" plystra-default:\"PRIVATE_DEFAULT\"`\n" +
	"Nested Nested `yaml:\"nested\"`\n" +
	"Pointer *Nested `yaml:\"pointer\"`\n" +
	"Labels map[string]string `yaml:\"labels\"`\n" +
	"Items []string `yaml:\"items\"`\n" +
	"Token configuration.Secret `yaml:\"token\"`\n" +
	"}\n"

func resourceOptions(t *testing.T) Options {
	t.Helper()
	packages := resourcePackages{}
	compile := func(path, source string) {
		t.Helper()
		files := token.NewFileSet()
		parsed, err := parser.ParseFile(files, "source.go", source, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := (&types.Config{Importer: packages}).Check(path, files, []*ast.File{parsed}, nil)
		if err != nil {
			t.Fatal(err)
		}
		packages[path] = pkg
	}
	compile("github.com/plystra/kernel/configuration", "package configuration\ntype Secret struct{}\n")
	compile("example.com/database", "package database\ntype Resource interface { Read() string }\n")
	compile("example.com/cache", "package cache\ntype Resource interface { Read() string }\n")
	providers := []resourceproviderinventory.Input{}
	for _, test := range []struct{ path, imports, config, id, args string }{
		{"example.com/provider", "import \"github.com/plystra/kernel/configuration\"\n", resourceConfigSource, "data.database/v1", "Config"},
		{"example.com/alternate", "import \"github.com/plystra/kernel/configuration\"\n", resourceConfigSource, "data.database/v1", "Config"},
		{"example.com/empty", "", "", "data.database/v1", ""},
		{"example.com/cached", "import \"example.com/database\"\n", "", "data.cache/v1", "database database.Resource"},
	} {
		source := "package provider\n" + test.imports + test.config + "type value struct{}\nfunc (*value) Read() string { return \"\" }\n//plystra:implements-resource " + test.id + "\nfunc New(" + test.args + ") (*value, error) { return nil, nil }\n"
		compile(test.path, source)
		declarations, err := resourceproviderdecl.ParseFile("provider.go", []byte(source))
		if err != nil || len(declarations) != 1 {
			t.Fatal(err)
		}
		providers = append(providers, resourceproviderinventory.Input{ModulePath: test.path, PackagePath: test.path, Declaration: declarations[0]})
	}
	inventory, err := resourceproviderinventory.Build(providers, []resourceproviderinventory.ContractInput{{ID: "data.database/v1", PackagePath: "example.com/database"}, {ID: "data.cache/v1", PackagePath: "example.com/cache"}}, packages)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{ModulePath: "example.com/app", ResourceInventory: inventory.Providers(), Template: "example.com/near", ExecutableConstructors: []string{"example.com/consumer.New"}, ExecutableInterfaceChoices: []string{"records.read/v1"}}
	options.ResourceInstances = []ResourceInstanceInput{{"database.secondary", "example.com/provider.New"}, {"database.primary", "example.com/provider.New"}, {"cache", "example.com/cached.New"}}
	options.ResourceOrder = []string{"database.secondary", "database.primary", "cache"}
	options.ResourceBindings = []ResourceBindingInput{{"instances", "cache", "database", "database.primary"}, {"implementations", "example.com/consumer.New", "database", "database.primary"}}
	for _, provider := range options.ResourceInventory {
		if provider.Symbol().String() != "example.com/provider.New" {
			continue
		}
		schema, _ := provider.Configuration()
		options.ResourceConfigurations = []ResourceConfigurationInput{
			{Name: "database.primary", Provider: provider.Symbol().String(), Schema: schema, YAML: []byte("value: initial-primary\n")},
			{Name: "database.secondary", Provider: provider.Symbol().String(), Schema: schema, YAML: []byte("value: initial-secondary\n")},
		}
	}
	manifest, err := applicationmeta.Parse([]byte("interfaces: {require: [records.read/v1], use: {records.read/v1: example.com/consumer.New}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	options.ApplicationModelCompatibility, err = NewExecutableApplicationModelCompatibility("sha256:"+strings.Repeat("a", 64), manifest, options.ExecutableInterfaceChoices)
	if err != nil {
		t.Fatal(err)
	}
	options.ApplicationModelCompatibility, err = options.ApplicationModelCompatibility.WithResources(options.ResourceInstances, options.ResourceBindings)
	if err != nil {
		t.Fatal(err)
	}
	options.Templates = []runtimebaseline.Template{{Module: "example.com/near", Version: "v1.0.0", YAML: resourceTemplate}}
	return options
}

const resourceTemplate = `interfaces:
  require: [records.read/v1]
  use: {records.read/v1: example.com/consumer.New}
resources:
  instances:
    database.primary:
      use: example.com/provider.New
      config:
        value: inherited-primary
        nested: {left: inherited, right: inherited}
        pointer: {left: inherited, right: inherited}
        labels: {lower: discarded}
        items: [lower]
        token: {env: RESOURCE_PRIMARY_SECRET}
    database.secondary:
      use: example.com/provider.New
      config: {value: inherited-secondary, token: {env: RESOURCE_SECONDARY_SECRET}}
    cache: {use: example.com/cached.New}
  bind:
    implementations:
      example.com/consumer.New: {database: database.primary}
    instances:
      cache: {database: database.primary}
`

func TestResourceBaselineAndPublicDigest(t *testing.T) {
	options := resourceOptions(t)
	baseline, err := RuntimeBaseline(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Defaults) != 2 {
		t.Fatalf("provider defaults not inventoried: %d", len(baseline.Defaults))
	}
	for _, private := range []string{"PRIVATE_DEFAULT", "initial-primary", "initial-secondary", "RESOURCE_PRIMARY_SECRET", "inherited-primary"} {
		if bytes.Contains(baseline.Contract, []byte(private)) {
			t.Fatal("public contract contains private input")
		}
	}
	private := options.ResourceConfigurations[0]
	original, err := ResourceConfigurationDigest(private)
	if err != nil {
		t.Fatal(err)
	}
	private.YAML = []byte("value: PRIVATE_SENTINEL\ntoken: {env: PRIVATE_REFERENCE}\n")
	changed, err := ResourceConfigurationDigest(private)
	if err != nil || changed != original {
		t.Fatal("runtime-only values changed public digest", err)
	}
	private.YAML = []byte("value: PRIVATE_SENTINEL\ncount: 8\n")
	changed, err = ResourceConfigurationDigest(private)
	if err != nil || changed == original || !strings.HasPrefix(changed, "sha256:") {
		t.Fatal("build-visible digest did not change", err)
	}
	private.YAML = []byte("value: [PRIVATE_SENTINEL]\n")
	if _, err := ResourceConfigurationDigest(private); err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
		t.Fatal("invalid private input accepted or disclosed", err)
	}
	options.ResourceConfigurations[0].YAML = []byte("value: refreshed\ntoken: {env: PRIVATE_REFERENCE}\n")
	refreshed, err := RuntimeBaseline(options)
	if err != nil || refreshed.ContractID != baseline.ContractID {
		t.Fatal("runtime-only change invalidated baseline contract", err)
	}
	options.ResourceConfigurations[0].YAML = []byte("value: refreshed\ncount: 8\n")
	refreshed, err = RuntimeBaseline(options)
	if err != nil || refreshed.ContractID == baseline.ContractID {
		t.Fatal("build-visible change did not invalidate baseline", err)
	}
}

func TestResourceBaselineRejectsIncompletePlans(t *testing.T) {
	for name, change := range map[string]func(*Options){
		"missing Resource order":   func(o *Options) { o.ResourceOrder = nil },
		"wrong Resource order":     func(o *Options) { o.ResourceOrder[0] = "missing" },
		"duplicate Resource order": func(o *Options) { o.ResourceOrder[0] = o.ResourceOrder[1] },
		"missing config":           func(o *Options) { o.ResourceConfigurations = o.ResourceConfigurations[1:] },
		"unknown config":           func(o *Options) { o.ResourceConfigurations[0].Name = "unknown" },
		"duplicate config": func(o *Options) {
			o.ResourceConfigurations = append(o.ResourceConfigurations, o.ResourceConfigurations[0])
		},
		"wrong config provider": func(o *Options) { o.ResourceConfigurations[0].Provider = "example.com/alternate.New" },
		"missing provider":      func(o *Options) { o.ResourceInventory = nil },
		"duplicate provider":    func(o *Options) { o.ResourceInventory = append(o.ResourceInventory, o.ResourceInventory[0]) },
		"duplicate instance":    func(o *Options) { o.ResourceInstances = append(o.ResourceInstances, o.ResourceInstances[0]) },
		"invalid name":          func(o *Options) { o.ResourceInstances[0].Name = "PRIVATE_SENTINEL" },
		"missing compatibility": func(o *Options) {
			o.ApplicationModelCompatibility.document.Projection.ResourceInstances = []ResourceInstanceInput{}
			o.ApplicationModelCompatibility.canonicalJSON, _ = encodeApplicationModelCompatibility(o.ApplicationModelCompatibility.document)
			o.ApplicationModelCompatibility.digest = applicationModelCompatibilityDigest(o.ApplicationModelCompatibility.canonicalJSON)
		},
		"unknown target":    func(o *Options) { o.ResourceBindings[0].Target = "missing" },
		"unknown namespace": func(o *Options) { o.ResourceBindings[0].Namespace = "unknown" },
		"blank parameter":   func(o *Options) { o.ResourceBindings[0].ParameterName = "_" },
	} {
		t.Run(name, func(t *testing.T) {
			options := resourceOptions(t)
			change(&options)
			if _, err := RuntimeBaseline(options); !errors.Is(err, ErrInvalidOptions) || strings.Contains(err.Error(), "PRIVATE_SENTINEL") {
				t.Fatal("accepted invalid plan or leaked input", err)
			}
		})
	}
}

func TestResourceTargetsFollowRenderedOrder(t *testing.T) {
	options := resourceOptions(t)
	digest := options.ApplicationModelCompatibility.ApplicationModelDigest()
	var err error
	options.ConfigurationProvenance, err = transportprovenance.New(transportprovenance.Input{
		Mode: generation.ConfigurationModeDefault, RootPath: "plystra.yaml", RootDigest: digest, SelectedPath: "plystra.yaml", SelectedDigest: digest, DependencyCompositionDigest: digest, ApplicationModelDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	options.DefaultStartupTimeout = time.Minute
	before, err := Render(options)
	if err != nil {
		t.Fatal(err)
	}
	options.ResourceInstances[0], options.ResourceInstances[2] = options.ResourceInstances[2], options.ResourceInstances[0]
	options.ResourceConfigurations[0], options.ResourceConfigurations[1] = options.ResourceConfigurations[1], options.ResourceConfigurations[0]
	after, err := Render(options)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("metadata order changed ResourceConfig targets", err)
	}
	for _, private := range []string{"PRIVATE_DEFAULT", "initial-primary", "RESOURCE_PRIMARY_SECRET", "inherited-primary"} {
		if bytes.Contains(before, []byte(private)) {
			t.Fatal("rendered source disclosed private Resource input")
		}
	}
	for _, required := range []string{`resource: "database.primary"`, `target: &configuration.ResourceConfig1`, `resource: "database.secondary"`, `target: &configuration.ResourceConfig0`} {
		if !bytes.Contains(before, []byte(required)) {
			t.Fatalf("render omitted %s", required)
		}
	}
}

func TestResourceCompatibilityUsesResolvedNamedGraph(t *testing.T) {
	options := resourceOptions(t)
	base := options.ApplicationModelCompatibility
	instances := append([]ResourceInstanceInput(nil), options.ResourceInstances...)
	bindings := append([]ResourceBindingInput(nil), options.ResourceBindings...)
	instances[0], instances[1] = instances[1], instances[0]
	bindings[0], bindings[1] = bindings[1], bindings[0]
	equivalent, err := base.WithResources(instances, bindings)
	if err != nil || !bytes.Equal(base.CanonicalJSON(), equivalent.CanonicalJSON()) {
		t.Fatal("input order changed compatibility", err)
	}
	instances[0].Provider = "example.com/alternate.New"
	changed, err := base.WithResources(instances, bindings)
	if err != nil || changed.Digest() == base.Digest() {
		t.Fatal("provider change not frozen", err)
	}
	bindings[0].Target = "database.secondary"
	changed, err = base.WithResources(options.ResourceInstances, bindings)
	if err != nil || changed.Digest() == base.Digest() {
		t.Fatal("binding change not frozen", err)
	}
	if base.Digest() != options.ApplicationModelCompatibility.Digest() {
		t.Fatal("WithResources mutated the original")
	}
}

func TestGeneratedResourceRuntimeWithoutSourceTree(t *testing.T) {
	options := resourceOptions(t)
	baseline, err := RuntimeBaseline(options)
	if err != nil {
		t.Fatal(err)
	}
	// The helper test compiles only Config targets, so add discovery-equivalent
	// consumers without linking application constructors into this focused binary.
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(baseline.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	contract["constructor_inventory"], err = json.Marshal([]baselineConstructor{
		{Symbol: "example.com/consumer.New", Interfaces: []string{"records.read/v1"}, Dependencies: []baselineResourceDependency{{"database", "data.database/v1"}}},
		{Symbol: "example.com/dormant.New", Interfaces: []string{"records.dormant/v1"}, Dependencies: []baselineResourceDependency{{"database", "data.database/v1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseline.Contract, err = json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	baseline.ContractID = runtimebaseline.ContractID(baseline.Contract)
	encoded, err := runtimebaseline.Encode(baseline)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, err := renderRuntimeConfigurationSupport(nil, options.ExecutableInterfaceChoices, options.ExecutableConstructors)
	if err != nil {
		t.Fatal(err)
	}
	constructors, err := renderConstructorConfiguration(nil, nil, options.ResourceConfigurations, options.ResourceInstances, options.ResourceOrder)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(constructors, "configuration.ResourceConfig0") || !strings.Contains(constructors, "configuration.ResourceConfig1") || strings.Contains(constructors, "configuration.ResourceConfig2") || strings.Contains(constructors, "PRIVATE_DEFAULT") {
		t.Fatal("incorrect or private typed targets")
	}
	assembly := "package assembly\nimport \"github.com/plystra/kernel/configuration\"\n" + resourceConfigSource + "type ConstructorConfiguration struct { ResourceConfig0, ResourceConfig1 Config }\n"
	source := runtimeTestHeader +
		"const compiledRuntimeContract = " + strconv.Quote(baseline.ContractID) + "\n" +
		"const compiledApplicationModelDigest = " + strconv.Quote(options.ApplicationModelCompatibility.ApplicationModelDigest()) + "\n" +
		"const compiledApplicationModelCompatibilityDigest = " + strconv.Quote(options.ApplicationModelCompatibility.Digest()) + "\n" +
		"const initialBaseline = " + strconv.Quote(string(encoded)) + "\n" +
		"const compilerResourceParity = " + strconv.Quote(resourceParityCases(t, options)) + "\n" +
		runtimeSource + runtimeBaselineSupport + constructors
	runEmittedRuntime(t, assembly, source, resourceRuntimeTests)
}

func resourceParityCases(t *testing.T, options Options) string {
	t.Helper()
	type instance struct{ Name, Provider, Configuration string }
	type scenario struct {
		Lower, Upper string
		Accepted     bool
		Instances    []instance
	}
	providers := make(map[string]resourceproviderinventory.Provider)
	for _, provider := range options.ResourceInventory {
		providers[provider.Symbol().String()] = provider
	}
	lookup := func(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		if namespace != applicationmeta.ConfigurationNamespaceResource {
			return implementationinventory.Configuration{}, false
		}
		return providers[symbol.String()].Configuration()
	}
	base, err := applicationmeta.ParseSource("template.yaml", []byte(resourceTemplate))
	if err != nil {
		t.Fatal(err)
	}
	document := func(entry string) string { return "resources: {instances: {pending: " + entry + "}}\n" }
	cases := []scenario{}
	for _, lower := range []string{
		"{}", "{config: {PRIVATE_UNBOUND_KEY: PRIVATE_UNBOUND_VALUE}}", "{config: {$remove: true}}",
		"{use: example.com/provider.New, config: {value: lower, nested: {left: lower, right: lower}, pointer: {left: lower}}}",
		"{use: example.com/provider.New, config: {value: [PRIVATE_SENTINEL]}}",
		"{use: example.com/empty.New}",
	} {
		for _, upper := range []string{
			"{}", "{$remove: true}", "{config: {value: upper, nested: {right: upper}, pointer: {right: upper}}}",
			"{use: example.com/provider.New, config: {value: upper}}", "{use: example.com/alternate.New, config: {value: upper}}",
			"{use: example.com/empty.New}", "{config: {value: {$remove: true}}}",
		} {
			item := scenario{Lower: document(lower), Upper: document(upper)}
			root, err := applicationmeta.ParseSource("plystra.yaml", []byte(item.Lower))
			if err != nil {
				t.Fatal(err)
			}
			overlay, err := applicationmeta.ParseOverlaySource("plystra.prod.yaml", []byte(item.Upper))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
			if err == nil {
				composition, composeErr := applicationmeta.Compose([]applicationmeta.Dependency{{ModulePath: "example.com/near", Manifest: base}}, selected, lookup)
				err = composeErr
				if err == nil {
					err = composition.ValidateRequiredConfiguration(lookup, nil, "plystra.prod.yaml")
				}
				if err == nil {
					for _, resource := range composition.Manifest().ResourceInstances() {
						result := instance{Name: resource.Name(), Provider: resource.Provider().String()}
						if schema, exists := lookup(applicationmeta.ConfigurationNamespaceResource, resource.Provider()); exists {
							var parsed yaml.Node
							var node *yaml.Node
							if data := resource.ConfigurationYAML(); len(data) > 0 {
								if err := yaml.Unmarshal(data, &parsed); err != nil {
									t.Fatal(err)
								}
								node = parsed.Content[0]
							}
							normalized, normalizeErr := constructorconfig.Normalize(constructorconfig.Schema{Kind: "object", Fields: compileConstructorFields(schema.Fields())}, node)
							if normalizeErr != nil {
								t.Fatal(normalizeErr)
							}
							data, marshalErr := yaml.Marshal(normalized)
							if marshalErr != nil {
								t.Fatal(marshalErr)
							}
							result.Configuration = string(data)
						}
						item.Instances = append(item.Instances, result)
					}
				}
			}
			item.Accepted = err == nil
			cases = append(cases, item)
		}
	}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
