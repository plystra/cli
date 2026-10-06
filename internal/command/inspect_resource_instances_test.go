package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceprovenance"
)

func TestInspectSelectedResourcesAcrossSelectors(t *testing.T) {
	t.Parallel()
	for _, selector := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{name: "default"},
		{name: "environment", args: []string{"--env", "production"}},
		{name: "replacement", args: []string{"--config", "deploy/replacement.yaml"}},
		{name: "environment-variable", env: map[string]string{"PLYSTRA_ENV": "production"}},
		{name: "replacement-variable", env: map[string]string{"PLYSTRA_CONFIG": "deploy/replacement.yaml"}},
	} {
		t.Run(selector.name, func(t *testing.T) {
			root, nested := createInspectSelectedResourceProject(t)
			before := snapshotInspectProject(t, root)
			for _, view := range []string{"resources", "implementations"} {
				args := append([]string{"inspect", view, "--format", "json"}, selector.args...)
				environment := inspectCommandEnvironment(selector.env)
				code, output, stderr := runCommand(t, args, nested, environment)
				if code != 0 || stderr != inspectProgress {
					t.Fatalf("%s: %d\n%s\n%s", view, code, output, stderr)
				}
				assertSelectedResourceGraph(t, output, view)
				code, repeated, stderr := runCommand(t, append(args, "--verbose"), root, environment)
				if code != 0 || repeated != output || stderr != inspectProgress {
					t.Fatal("selected Resource graph is nondeterministic")
				}
				code, human, stderr := runCommand(t, append([]string{"inspect", view, "--verbose"}, selector.args...), nested, environment)
				if code != 0 || stderr != "" {
					t.Fatalf("human %s: %d\n%s\n%s", view, code, human, stderr)
				}
				for _, phrase := range []string{"Resource instances: 3 selected", "Instance: database.unconsumed", "Provider: example.com/acme/inspect/provider.New", "parameter 1 primary -> database.primary (explicit)", "parameter 2 Replica -> database.primary (explicit)", "parameter 3 view -> view.main (unique-compatible)", "instances view.main parameter 1 upstream -> database.primary (explicit)", "Resolution evidence:"} {
					if !strings.Contains(human, phrase) {
						t.Fatalf("human %s omits %q", view, phrase)
					}
				}
				for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_CONFIG", "PRIVATE_DEFAULT", "PRIVATE_CONSTRUCTOR"} {
					if strings.Contains(output+human, private) {
						t.Fatalf("inspection exposed %q", private)
					}
				}
			}
			code, output, stderr := runCommand(t, append([]string{"inspect", "interfaces", "--format", "json"}, selector.args...), nested, inspectCommandEnvironment(selector.env))
			if code != 0 || stderr != inspectProgress {
				t.Fatalf("interfaces: %d\n%s\n%s", code, output, stderr)
			}
			for _, node := range decodeInspectGraphCommandEnvelope(t, output).Result.Nodes {
				if strings.HasPrefix(node.Kind, "resource-") {
					t.Fatal("Resource leaked into Interface graph")
				}
			}
			if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
				t.Fatal("inspection changed authored files")
			}
		})
	}
}

func TestBuiltCLIInspectsSelectedResourceInstances(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, nested := createInspectSelectedResourceProject(t)
	before := snapshotInspectProject(t, root)
	for _, view := range []string{"resources", "implementations"} {
		for _, format := range []string{"human", "json"} {
			command := exec.CommandContext(t.Context(), binary, "inspect", view, "--format", format)
			command.Dir, command.Env = nested, inspectCommandEnvironment(nil)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			if err != nil {
				t.Fatalf("built CLI: %v\n%s\n%s", err, output, &stderr)
			}
			if format == "json" {
				if stderr.String() != inspectProgress {
					t.Fatalf("unexpected stderr: %s", &stderr)
				}
				assertSelectedResourceGraph(t, string(output), view)
			} else if stderr.Len() != 0 || !bytes.Contains(output, []byte("Instance: database.unconsumed")) {
				t.Fatalf("human graph missing unconsumed instance: %s\n%s", output, &stderr)
			}
		}
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("built inspection changed authored files")
	}
}

func assertSelectedResourceGraph(t testing.TB, output, view string) {
	t.Helper()
	document := decodeInspectGraphCommandEnvelope(t, output)
	if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.Result.Type != view {
		t.Fatalf("invalid graph envelope: %#v", document)
	}
	var resources []interfaceprovenance.ResourceInput
	var bindings []interfaceprovenance.ResourceBindingInput
	for _, node := range document.Result.Nodes {
		if node.ResourceInstance != nil {
			resources = append(resources, *node.ResourceInstance)
		}
	}
	for _, edge := range document.Result.Edges {
		if edge.ResourceBinding != nil {
			if edge.Kind != "depends-on-resource" || edge.ParameterName != edge.ResourceBinding.ParameterName || edge.ParameterPosition != edge.ResourceBinding.ParameterPosition {
				t.Fatalf("binding edge mismatch: %#v", edge)
			}
			bindings = append(bindings, *edge.ResourceBinding)
		}
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ConstructionOrder < resources[j].ConstructionOrder })
	if len(resources) != 3 || len(bindings) != 4 {
		t.Fatalf("selected graph: %d instances, %d bindings", len(resources), len(bindings))
	}
	for i, name := range []string{"database.primary", "database.unconsumed", "view.main"} {
		resource := resources[i]
		if resource.Name != name || resource.ConstructionOrder != i+1 || resource.ModulePath != "example.com/acme/inspect" || resource.ModuleVersion != "local" || resource.ContractSource.Kind != "resource-declaration" || resource.ContractSource.Line <= 0 || resource.DeclarationSource.Line <= 0 {
			t.Fatalf("incomplete instance: %#v", resource)
		}
		contract, provider := "database", "provider"
		if name == "view.main" {
			contract, provider = "view", "viewprovider"
		}
		if resource.ResourceID != "data."+contract+"/v1" || resource.PackagePath != "example.com/acme/inspect/"+contract || resource.Provider != "example.com/acme/inspect/"+provider+".New" || resource.ContractSource.Path != contract+"/resource.go" || resource.ContractSource.Module != resource.ModulePath || resource.DeclarationSource.Path != provider+"/new.go" {
			t.Fatalf("instance lost exact contract/provider identity: %#v", resource)
		}
	}
	parameters := make(map[string]interfaceprovenance.ResourceBindingInput)
	for _, binding := range bindings {
		parameters[binding.ConsumerKind+":"+binding.ParameterName] = binding
	}
	for _, parameter := range []struct {
		kind, name, target string
		position           int
		reason             interfaceprovenance.SelectionReason
	}{
		{"implementations", "primary", "database.primary", 1, interfaceprovenance.SelectionExplicit},
		{"implementations", "Replica", "database.primary", 2, interfaceprovenance.SelectionExplicit},
		{"implementations", "view", "view.main", 3, interfaceprovenance.SelectionUniqueCompatible},
		{"instances", "upstream", "database.primary", 1, interfaceprovenance.SelectionExplicit},
	} {
		binding := parameters[parameter.kind+":"+parameter.name]
		if binding.InstanceName != parameter.target || binding.ParameterPosition != parameter.position || binding.Reason != parameter.reason || binding.Provider == "" || len(binding.SelectionSources) == 0 || (binding.Reason == interfaceprovenance.SelectionExplicit && len(binding.BindingSources) == 0) {
			t.Fatalf("incomplete binding: %#v", binding)
		}
		consumer, constructor := "example.com/acme/inspect/service.New", "example.com/acme/inspect/service.New"
		if parameter.kind == "instances" {
			consumer, constructor = "view.main", "example.com/acme/inspect/viewprovider.New"
		}
		if binding.Consumer != consumer || binding.Constructor != constructor || binding.DeclarationSource.Module != "example.com/acme/inspect" {
			t.Fatalf("binding lost exact consumer identity: %#v", binding)
		}
	}
	var evidence struct {
		Resources []interfaceprovenance.ResourceInput        `json:"resources"`
		Bindings  []interfaceprovenance.ResourceBindingInput `json:"resource_bindings"`
	}
	if err := json.Unmarshal(document.Result.ResolutionEvidence, &evidence); err != nil {
		t.Fatal(err)
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].ConsumerKind != bindings[j].ConsumerKind {
			return bindings[i].ConsumerKind < bindings[j].ConsumerKind
		}
		if bindings[i].Consumer != bindings[j].Consumer {
			return bindings[i].Consumer < bindings[j].Consumer
		}
		return bindings[i].ParameterPosition < bindings[j].ParameterPosition
	})
	if !reflect.DeepEqual(resources, evidence.Resources) || !reflect.DeepEqual(bindings, evidence.Bindings) {
		t.Fatal("graph differs from live resolution evidence")
	}
}

func createInspectSelectedResourceProject(t testing.TB) (string, string) {
	t.Helper()
	root, nested := createInspectCommandProject(t)
	manifest := `interfaces: {require: [app.run/v1]}
resources:
  instances:
    database.primary:
      use: example.com/acme/inspect/provider.New
      config: {first: PRIVATE_CONFIG, second: PRIVATE_CONFIG, third: PRIVATE_CONFIG}
    database.unconsumed:
      use: example.com/acme/inspect/provider.New
    view.main:
      use: example.com/acme/inspect/viewprovider.New
  bind:
    implementations:
      example.com/acme/inspect/service.New: {primary: database.primary, Replica: database.primary}
    instances:
      view.main: {upstream: database.primary}
`
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), manifest)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {config: {third: PRIVATE_CONFIG_OVERLAY}}}}\n")
	writeCommandFile(t, filepath.Join(root, "deploy/replacement.yaml"), manifest)
	writeCommandFile(t, filepath.Join(root, "database/resource.go"), "package database\n//plystra:resource data.database/v1\ntype Resource interface{Read()}\n")
	writeCommandFile(t, filepath.Join(root, "view/resource.go"), "package view\n//plystra:resource data.view/v1\ntype Resource interface{View()}\n")
	writeCommandGraphInterface(t, root, "run/v1", "runv1", "app.run/v1", "Run")
	writeCommandFile(t, filepath.Join(root, "provider/new.go"), "package provider\ntype Config struct { First string `yaml:\"first\" plystra-default:\"PRIVATE_DEFAULT\"`; Second string `yaml:\"second\"`; Third string `yaml:\"third\"` }\ntype Value struct{}\nfunc (*Value) Read(){}\n//plystra:implements-resource data.database/v1\nfunc New(cfg Config)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n")
	writeCommandFile(t, filepath.Join(root, "viewprovider/new.go"), "package viewprovider\nimport \"example.com/acme/inspect/database\"\ntype Value struct{}\nfunc (*Value) View(){}\n//plystra:implements-resource data.view/v1\nfunc New(upstream database.Resource)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n")
	writeCommandFile(t, filepath.Join(root, "service/new.go"), `package service
import (
 "context"
 "example.com/acme/inspect/database"
 "example.com/acme/inspect/view"
 runv1 "example.com/acme/inspect/interfaces/run/v1"
)
type Service struct{}
//plystra:implements app.run/v1
func New(primary database.Resource, Replica database.Resource, view view.Resource)(*Service,error){panic("PRIVATE_CONSTRUCTOR")}
func (*Service) Run(context.Context,runv1.Request)(runv1.Response,error){return runv1.Response{},nil}
`)
	return root, nested
}
