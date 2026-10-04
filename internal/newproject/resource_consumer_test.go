package newproject_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/resolutionevidence"
	"go.yaml.in/yaml/v3"
)

func TestCreateRejectsReachableTemplateResourceConsumerWithoutInstallation(t *testing.T) {
	const module = "example.com/acme/resource-consumer-template"
	proxy := createKernelProxy(t)
	writeProxyModule(t, proxy, module, "v1.0.0", map[string][]byte{
		"plystra.yaml": []byte("interfaces: {require: [app.resource/v1]}\n"),
		"consumer.go": []byte(`package consumer
import "context"
//plystra:interface app.resource/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
//plystra:resource storage.database/v1
type Resource interface { Health() error }
type Service struct{}
//plystra:implements app.resource/v1
func New(primary Resource) (*Service, error) { panic("PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY") }
func (*Service) Run(context.Context, Request) (Response, error) { return Response{}, nil }
`),
	})
	before := snapshotTree(t, proxy)
	for _, format := range []string{"human", "json"} {
		t.Run(format, func(t *testing.T) {
			parent := t.TempDir()
			var stdout, stderr bytes.Buffer
			arguments := []string{"new", "my-app", "--module", "example.com/acme/my-app", "--template", module + "@v1.0.0", "--format", format}
			exit := command.RunIn(arguments, &stdout, &stderr, parent, isolatedGoEnvironment(t, proxy))
			if exit != 3 {
				t.Fatalf("create = %d: %s %s", exit, &stdout, &stderr)
			}
			if format == "json" {
				var result struct {
					Status      string `json:"status"`
					Diagnostics []struct {
						Code      string                  `json:"code"`
						Locations []diagnosticjson.Source `json:"locations"`
					} `json:"diagnostics"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || stderr.Len() != 0 || result.Status != "validation_failed" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != diagnosticcode.ResourceBindingMissing {
					t.Fatalf("structured Resource failure: %v: %s %s", err, &stdout, &stderr)
				}
				found := false
				for _, location := range result.Diagnostics[0].Locations {
					if location.Module == module && location.Path == "consumer.go" && location.Kind == "implementation-constructor" && location.Line == 11 && location.Column == 6 {
						found = true
					}
				}
				if !found {
					t.Fatal("creation lost template constructor source")
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), diagnosticcode.ResourceBindingMissing) || !strings.Contains(stderr.String(), "primary") {
				t.Fatalf("human Resource failure: %s %s", &stdout, &stderr)
			}
			for _, private := range []string{parent, proxy, "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY"} {
				if strings.Contains(stdout.String()+stderr.String(), private) {
					t.Fatal("creation disclosed private input")
				}
			}
			if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target exists after missing Resource binding: %v", err)
			}
			assertNoTransactionFiles(t, parent)
		})
	}
	if !reflect.DeepEqual(before, snapshotTree(t, proxy)) {
		t.Fatal("creation changed the template proxy")
	}
}

func TestPublicCreateQualifiesTemplateResourceLifecycle(t *testing.T) {
	const module = "example.com/acme/resource-lifecycle-template"
	proxy := createKernelProxy(t)
	writeProxyModule(t, proxy, module, "v1.0.0", map[string][]byte{
		"plystra.yaml": []byte(`resources:
  instances:
    database.primary:
      use: example.com/acme/resource-lifecycle-template.New
      config: {name: primary}
    database.replica:
      use: example.com/acme/resource-lifecycle-template.New
      config: {name: replica}
`),
		"resource.go": []byte(strings.ReplaceAll(`package database
import ("context"; "errors"; "os")
//plystra:resource storage.database/v1
type Resource interface { Name() string }
type Config struct { Name string @@yaml:"name" plystra:"required"@@ }
type value struct { name string }
//plystra:implements-resource storage.database/v1
func New(c Config) (*value,error) {return &value{name:c.Name},nil}
func (v *value) Name() string {return v.name}
func (v *value) record(event string) error {
 f,err:=os.OpenFile(os.Getenv("RESOURCE_CREATION_EVENTS"),os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil{return err};defer f.Close()
 _,err=f.WriteString(event+":"+v.name+"\n");return err
}
func (v *value) Start(context.Context) error {
 if err:=v.record("start");err!=nil{return err}
 if v.name=="replica" {
  switch os.Getenv("RESOURCE_CREATION_FAILURE") {
  case "error": return errors.New("PRIVATE_RESOURCE_START_FAILURE")
  case "panic": panic("PRIVATE_RESOURCE_START_FAILURE")
  }
 }
 return nil
}
func (v *value) Stop(context.Context) error {return v.record("stop")}
`, "@@", "`")),
	})
	before := snapshotTree(t, proxy)
	for _, failure := range []string{"", "error", "panic"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			marker := filepath.Join(t.TempDir(), "events")
			environment := isolatedGoEnvironment(t, proxy)
			environment = setEnvironmentValue(environment, "RESOURCE_CREATION_EVENTS", marker)
			environment = setEnvironmentValue(environment, "RESOURCE_CREATION_FAILURE", failure)
			var stdout, stderr bytes.Buffer
			exit := command.RunIn([]string{"new", "my-app", "--module", "example.com/acme/my-app", "--template", module + "@v1.0.0"}, &stdout, &stderr, parent, environment)
			root := filepath.Join(parent, "my-app")
			if failure == "" {
				if exit != 0 {
					t.Fatalf("Resource template creation = %d: %s %s", exit, &stdout, &stderr)
				}
				document, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
				if err != nil || !bytes.Contains(document, []byte("template: "+module)) || bytes.Contains(document, []byte("resources:")) {
					t.Fatalf("template relationship not retained as a delta: %s, %v", document, err)
				}
				stdout.Reset()
				stderr.Reset()
				if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, environment); code != 0 {
					t.Fatalf("created Resource Project is stale: %d: %s %s", code, &stdout, &stderr)
				}
			} else {
				if exit == 0 || !strings.Contains(stderr.String(), "lifecycle smoke failed") || strings.Contains(stdout.String()+stderr.String(), "PRIVATE_RESOURCE_START_FAILURE") {
					t.Fatalf("Resource smoke failure = %d: %s %s", exit, &stdout, &stderr)
				}
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Resource smoke failure left target: %v", err)
				}
			}
			events, err := os.ReadFile(marker)
			if err != nil || string(events) != "start:primary\nstart:replica\nstop:replica\nstop:primary\n" {
				t.Fatalf("selected unconsumed Resource lifecycle = %q, %v", events, err)
			}
			assertNoTransactionFiles(t, parent)
		})
	}
	if !reflect.DeepEqual(before, snapshotTree(t, proxy)) {
		t.Fatal("Resource template qualification changed source modules")
	}
}

func TestPublicTemplateUpdatePreservesResourceConfigurationAndBindingDelta(t *testing.T) {
	const templatePath = "example.com/acme/resource-update-template"
	const projectPath = "example.com/acme/my-app"
	const provider = templatePath + "/provider.New"
	const consumer = templatePath + "/consumer.New"
	proxy := createKernelProxy(t)
	templateFiles := map[string][]byte{
		"plystra.yaml": []byte(`interfaces:
  require: [app.probe/v1]
  use: {app.probe/v1: ` + consumer + `}
resources:
  instances:
    database.primary:
      use: ` + provider + `
      config: {name: inherited-primary-v1, region: inherited-region-v1}
    database.replica:
      use: ` + provider + `
      config: {name: inherited-replica-v1, region: inherited-region-v1}
  bind:
    implementations:
      ` + consumer + `: {primary: database.replica, Replica: database.replica}
`),
		"database/resource.go": []byte(`package database
//plystra:resource storage.database/v1
type Resource interface { Name() string }
`),
		"provider/provider.go": []byte(strings.ReplaceAll(`package provider
type Config struct {
 Name string @@yaml:"name" plystra:"required"@@
 Region string @@yaml:"region" plystra:"required"@@
 Note string @@yaml:"note"@@
}
type value struct { config Config }
//plystra:implements-resource storage.database/v1
func New(c Config) (*value, error) { return &value{config: c}, nil }
func (v *value) Name() string { return v.config.Name }
`, "@@", "`")),
		"interfaces/app/probe/v1/interface.go": []byte(`package probev1
import "context"
//plystra:interface app.probe/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`),
		"consumer/consumer.go": []byte(`package consumer
import (
 "context"
 "` + templatePath + `/database"
 probev1 "` + templatePath + `/interfaces/app/probe/v1"
)
type Service struct { primary, replica database.Resource }
//plystra:implements app.probe/v1
func New(primary database.Resource, Replica database.Resource) (*Service, error) {
 return &Service{primary: primary, replica: Replica}, nil
}
func (*Service) Run(context.Context, probev1.Request) (probev1.Response, error) {
 return probev1.Response{}, nil
}
`),
	}
	writeProxyModule(t, proxy, templatePath, "v1.0.0", templateFiles)
	templateFiles["plystra.yaml"] = []byte(`interfaces:
  require: [app.probe/v1]
  use: {app.probe/v1: ` + consumer + `}
resources:
  instances:
    database.primary:
      use: ` + provider + `
      config: {name: inherited-primary-v2, region: inherited-region-v2, note: newly-inherited-note}
    database.replica:
      use: ` + provider + `
      config: {name: inherited-replica-v2, region: inherited-region-v2, note: newly-inherited-note}
  bind:
    implementations:
      ` + consumer + `: {primary: database.replica, Replica: database.replica}
`)
	writeProxyModule(t, proxy, templatePath, "v1.0.1", templateFiles)
	environment := slices.DeleteFunc(isolatedGoEnvironment(t, proxy), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(key, "PLYSTRA_ENV") || strings.EqualFold(key, "PLYSTRA_CONFIG")
	})
	sourcesBefore := map[string]map[string][]byte{proxy: snapshotTree(t, proxy)}
	for _, version := range []string{"v1.0.0", "v1.0.1"} {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: t.TempDir(), Environment: environment}, "mod", "download", templatePath+"@"+version); err != nil {
			t.Fatalf("pre-download template %s: %v", version, err)
		}
		root := moduleCacheRoot(t, environment, templatePath, version)
		sourcesBefore[root] = snapshotTree(t, root)
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "my-app")
	run := func(directory string, args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := command.RunIn(args, &stdout, &stderr, directory, environment); code != 0 || stderr.Len() != 0 {
			t.Fatalf("%v = %d: %s %s", args, code, &stdout, &stderr)
		}
	}
	run(parent, "new", "my-app", "--module", projectPath, "--template", templatePath+"@v1.0.0")
	configuration, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := applicationmeta.Parse(configuration)
	if err != nil || manifest.Template() != templatePath || len(manifest.ResourceInstances()) != 0 || len(manifest.ResourceBindings()) != 0 {
		t.Fatalf("creation materialized Resource declarations or lost template: %v", err)
	}
	consumerSymbol, err := constructorsymbol.Parse(consumer)
	if err != nil {
		t.Fatal(err)
	}
	assertModel := func(version, region string, local bool) {
		t.Helper()
		assertDirectRequirement(t, root, templatePath, version)
		resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
		if err != nil {
			t.Fatal(err)
		}
		wantNames := []string{"database.primary", "database.replica"}
		wantValues := []string{"inherited-primary-v1", "inherited-replica-v1"}
		wantTarget, wantOwner := "database.replica", templatePath
		wantReasons := []constructorgraph.SelectionReason{constructorgraph.SelectionExplicit, constructorgraph.SelectionExplicit}
		if local {
			wantNames, wantValues = []string{"database.primary"}, []string{"current-primary"}
			wantTarget, wantOwner = "database.primary", projectPath
			wantReasons[1] = constructorgraph.SelectionUnique
		}
		instances := resolved.Manifest().ResourceInstances()
		graph := resolved.InterfaceResolution().Graph()
		nodes := graph.ResourceConstructionOrder()
		if len(instances) != len(wantNames) || len(nodes) != len(wantNames) {
			t.Fatalf("selected Resource counts = %d, %d; want %d", len(instances), len(nodes), len(wantNames))
		}
		for i, name := range wantNames {
			if instances[i].Name() != name || instances[i].Provider().String() != provider || nodes[i].Name() != name || nodes[i].ResourceID().String() != "storage.database/v1" || nodes[i].Provider().Symbol().String() != provider {
				t.Fatalf("Resource %s lost its instance, contract, or provider identity", name)
			}
			var config map[string]string
			if err := yaml.Unmarshal(instances[i].ConfigurationYAML(), &config); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(config, map[string]string{"name": wantValues[i], "region": region}) {
				t.Fatalf("%s Config did not preserve the exact inherited/local values and field removal", name)
			}
		}
		bindings := resolved.Manifest().ResourceBindings()
		wantBindingCount := 2
		if local {
			wantBindingCount = 1
		}
		if len(bindings) != wantBindingCount {
			t.Fatalf("effective explicit bindings = %d, want %d", len(bindings), wantBindingCount)
		}
		for _, binding := range bindings {
			if binding.Namespace() != "implementations" || binding.Consumer() != consumer || binding.Target() != wantTarget || (local && binding.ParameterName() != "primary") {
				t.Fatalf("unexpected explicit Resource binding: %#v", binding)
			}
		}
		dependencies := graph.ResourceDependencies(consumerSymbol)
		if len(dependencies) != 2 || len(graph.ConstructionOrder()) != 1 || graph.ConstructionOrder()[0].Symbol() != consumerSymbol {
			t.Fatal("Resource update lost the consumer or collapsed its repeated dependencies")
		}
		for i, parameter := range []string{"primary", "Replica"} {
			dependency := dependencies[i]
			if dependency.Namespace() != constructorgraph.ResourceConsumerImplementation || dependency.Consumer() != consumer || dependency.ParameterName() != parameter || dependency.ParameterPosition() != i+1 || dependency.InstanceName() != wantTarget || dependency.Provider().String() != provider || dependency.Reason() != wantReasons[i] {
				t.Fatalf("resolved dependency %s lost its exact address, target, or reason", parameter)
			}
		}
		data, err := os.ReadFile(filepath.Join(root, "generated", "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		provenance, err := applicationgen.DecodeManifestProvenance(data)
		if err != nil {
			t.Fatal(err)
		}
		resources, edges := provenance.InterfaceProvenance().Resources(), provenance.InterfaceProvenance().ResourceBindings()
		if len(resources) != len(wantNames) || len(edges) != 2 {
			t.Fatal("generated Resource provenance lost selected instances or dependency edges")
		}
		for i, resource := range resources {
			if resource.Name != wantNames[i] || resource.ResourceID != "storage.database/v1" || resource.Provider != provider || resource.ModulePath != templatePath || resource.ModuleVersion != version || len(resource.SelectionSources) != 1 || resource.SelectionSources[0].Module != templatePath || resource.SelectionSources[0].Path != "plystra.yaml" {
				t.Fatalf("generated Resource identity/version/selection owner = %#v", resource)
			}
			owners := make(map[string]bool)
			for _, source := range resource.ConfigurationSources {
				if source.Path != "plystra.yaml" {
					t.Fatalf("unexpected Resource Config source: %#v", source)
				}
				owners[source.Module] = true
			}
			wantOwners := map[string]bool{templatePath: true}
			if local {
				wantOwners[projectPath] = true
			}
			if !reflect.DeepEqual(owners, wantOwners) {
				t.Fatalf("Resource Config owners = %v, want %v", owners, wantOwners)
			}
		}
		for _, edge := range edges {
			index := edge.ParameterPosition - 1
			if index < 0 || index > 1 || edge.ParameterName != []string{"primary", "Replica"}[index] || edge.ConsumerKind != "implementations" || edge.Consumer != consumer || edge.Constructor != consumer || edge.ResourceID != "storage.database/v1" || edge.InstanceName != wantTarget || edge.Provider != provider || edge.Reason != interfaceprovenance.SelectionReason(wantReasons[index]) {
				t.Fatalf("generated Resource edge = %#v", edge)
			}
			if edge.Reason == interfaceprovenance.SelectionExplicit {
				if len(edge.BindingSources) != 1 || edge.BindingSources[0].Module != wantOwner || edge.BindingSources[0].Path != "plystra.yaml" {
					t.Fatalf("explicit binding lost its owning source: %#v", edge)
				}
			} else if len(edge.BindingSources) != 0 {
				t.Fatalf("removed binding retained an explicit source: %#v", edge)
			}
		}
		if local {
			wantRemoved := map[string]bool{
				`resources.instances["database.primary"].config["note"]`:        false,
				`resources.instances["database.replica"]`:                       false,
				`resources.bind.implementations["` + consumer + `"]["Replica"]`: false,
			}
			for _, field := range resolved.ResolutionEvidence().ConfigurationFields() {
				if _, wanted := wantRemoved[field.Path()]; wanted {
					if !field.Effective() || !field.Removed() || field.Owner() != resolutionevidence.ConfigurationOwnerRoot {
						t.Fatalf("removal lost current-Project ownership: %s", field.Path())
					}
					wantRemoved[field.Path()] = true
				}
			}
			for path, found := range wantRemoved {
				if !found {
					t.Fatalf("missing removal evidence for %s", path)
				}
			}
		}
	}
	assertModel("v1.0.0", "inherited-region-v1", false)
	var document, delta yaml.Node
	if err := yaml.Unmarshal(configuration, &document); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(`# Preserve this authored Resource delta across template updates.
resources:
  instances:
    database.primary:
      config: {name: current-primary, note: {$remove: true}}
    database.replica: {$remove: true}
  bind:
    implementations:
      `+consumer+`:
        primary: database.primary
        Replica: {$remove: true}
`), &delta); err != nil {
		t.Fatal(err)
	}
	document.Content[0].Content = append(document.Content[0].Content, delta.Content[0].Content...)
	authored, err := yaml.Marshal(&document)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "plystra.yaml"), authored)
	writeTestFile(t, filepath.Join(root, "local.go"), []byte("package myapp\n\n// Authored source remains unchanged across dependency updates.\nconst Local = true\n"))
	authoredFiles := func() map[string][]byte {
		t.Helper()
		files := snapshotTree(t, root)
		for path := range files {
			if path == "go.mod" || path == "go.sum" || strings.HasPrefix(path, "generated/") || strings.HasPrefix(path, "dist/") {
				delete(files, path)
			}
		}
		return files
	}
	before := authoredFiles()
	for _, args := range [][]string{{"generate"}, {"update", templatePath + "@v1.0.1"}, {"generate", "--check"}, {"check"}} {
		readOnly := args[0] == "check" || (len(args) == 2 && args[1] == "--check")
		var beforeCheck map[string][]byte
		if readOnly {
			beforeCheck = snapshotTree(t, root)
		}
		run(root, args...)
		if !reflect.DeepEqual(before, authoredFiles()) {
			t.Fatalf("%v changed authored files or materialized template sources", args)
		}
		if readOnly && !reflect.DeepEqual(beforeCheck, snapshotTree(t, root)) {
			t.Fatalf("%v changed the Project", args)
		}
		version, region := "v1.0.1", "inherited-region-v2"
		if len(args) == 1 && args[0] == "generate" {
			version, region = "v1.0.0", "inherited-region-v1"
		}
		assertModel(version, region, true)
	}
	for source, before := range sourcesBefore {
		if !reflect.DeepEqual(before, snapshotTree(t, source)) {
			t.Fatalf("creation or update changed dependency source %s", source)
		}
	}
	for _, path := range []string{"database", "provider", "consumer", "interfaces", "go.work"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("template path %s was copied: %v", path, err)
		}
	}
	assertNoTransactionFiles(t, parent)
}
