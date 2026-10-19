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
	command := exec.CommandContext(t.Context(), binary, "inspect", "resources", "--configured", "--format", "json")
	command.Dir, command.Env = nested, inspectCommandEnvironment(nil)
	configured, err := command.Output()
	if err != nil || !bytes.Contains(configured, []byte(`"schema":"plystra.resource-selections/v1"`)) || !bytes.Contains(configured, []byte(`"scope":"configured_only"`)) {
		t.Fatalf("built configured inspection: %v\n%s", err, configured)
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("built inspection changed authored files")
	}
}

func TestInspectResourcesNeedsSelectedProviderToExist(t *testing.T) {
	root, nested := createInspectSelectedResourceProject(t)
	path := filepath.Join(root, "plystra.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	selected := strings.Replace(string(content), "    view.main:\n", "    access.generated:\n      use: example.com/acme/inspect/dataaccess.New\n    view.main:\n", 1)
	if selected == string(content) {
		t.Fatal("resource fixture did not contain the expected instance")
	}
	writeCommandFile(t, path, selected)
	writeCommandFile(t, filepath.Join(root, "deploy/replacement.yaml"), selected)
	before := snapshotInspectProject(t, root)
	code, output, stderr := runCommand(t, []string{"inspect", "resources", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	if code != 1 || output != "" || !strings.Contains(stderr, "provider is not visible") {
		t.Fatalf("complete Resource inspection = %d, stdout = %q, stderr = %q", code, output, stderr)
	}
	for _, selector := range []struct {
		args       []string
		sourcePath string
	}{
		{sourcePath: "plystra.yaml"},
		{args: []string{"--env", "production"}, sourcePath: "plystra.yaml"},
		{args: []string{"--config", "deploy/replacement.yaml"}, sourcePath: "deploy/replacement.yaml"},
	} {
		args := append([]string{"inspect", "resources", "--configured", "--format", "json"}, selector.args...)
		code, output, stderr = runCommand(t, args, nested, inspectCommandEnvironment(nil))
		if code != 0 || stderr != "Reading configured Resource selections...\n" {
			t.Fatalf("configured Resource inspection = %d, stdout = %q, stderr = %q", code, output, stderr)
		}
		var result struct {
			Schema    string `json:"schema"`
			Operation string `json:"operation"`
			Status    string `json:"status"`
			ExitClass int    `json:"exit_class"`
			Payload   struct {
				Schema     string `json:"schema"`
				ModulePath string `json:"module_path"`
				Scope      string `json:"scope"`
				Instances  []struct {
					Name            string `json:"name"`
					Provider        string `json:"provider"`
					SourcePath      string `json:"source_path"`
					ProviderVisible bool   `json:"provider_visible"`
					ResourceID      string `json:"resource_id"`
				} `json:"instances"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		if result.Schema != "plystra.result/v1" || result.Operation != "inspect.resources" || result.Status != "success" || result.ExitClass != 0 || result.Payload.Schema != "plystra.resource-selections/v1" || result.Payload.Scope != "configured_only" || result.Payload.ModulePath != "example.com/acme/inspect" || len(result.Payload.Instances) != 4 {
			t.Fatalf("unexpected configured selection result: %#v", result)
		}
		missing := result.Payload.Instances[0]
		if missing.Name != "access.generated" || missing.Provider != "example.com/acme/inspect/dataaccess.New" || missing.SourcePath != selector.sourcePath || missing.ProviderVisible || missing.ResourceID != "" {
			t.Fatalf("missing provider incorrectly resolved: %#v", missing)
		}
		if visible := result.Payload.Instances[1]; visible.Name != "database.primary" || !visible.ProviderVisible || visible.ResourceID != "storage.database/v1" {
			t.Fatalf("visible provider omitted: %#v", visible)
		}
		for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_CONFIG", "PRIVATE_DEFAULT", "PRIVATE_CONSTRUCTOR"} {
			if strings.Contains(output, private) {
				t.Fatalf("configured inspection exposed %q", private)
			}
		}
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("failed inspection changed Project files")
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
		if resource.ResourceID != "storage."+contract+"/v1" || resource.PackagePath != "example.com/acme/inspect/"+contract || resource.Provider != "example.com/acme/inspect/"+provider+".New" || resource.ContractSource.Path != contract+"/resource.go" || resource.ContractSource.Module != resource.ModulePath || resource.DeclarationSource.Path != provider+"/new.go" {
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
	writeCommandFile(t, filepath.Join(root, "database/resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface{Read()}\n")
	writeCommandFile(t, filepath.Join(root, "view/resource.go"), "package view\n//plystra:resource storage.view/v1\ntype Resource interface{View()}\n")
	writeCommandGraphInterface(t, root, "run/v1", "runv1", "app.run/v1", "Run")
	writeCommandFile(t, filepath.Join(root, "provider/new.go"), "package provider\ntype Config struct { First string `yaml:\"first\" plystra-default:\"PRIVATE_DEFAULT\"`; Second string `yaml:\"second\"`; Third string `yaml:\"third\"` }\ntype Value struct{}\nfunc (*Value) Read(){}\n//plystra:implements-resource storage.database/v1\nfunc New(cfg Config)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n")
	writeCommandFile(t, filepath.Join(root, "viewprovider/new.go"), "package viewprovider\nimport \"example.com/acme/inspect/database\"\ntype Value struct{}\nfunc (*Value) View(){}\n//plystra:implements-resource storage.view/v1\nfunc New(upstream database.Resource)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n")
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
