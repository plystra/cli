package command_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticschema"
)

func TestInspectImplementationResourceConsumers(t *testing.T) {
	t.Parallel()
	root, nested := createInspectResourceConsumerProject(t)
	before := snapshotInspectProject(t, root)
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
			args := append([]string{"inspect", "implementations", "--format", "json"}, selector.args...)
			environment := inspectCommandEnvironment(selector.env)
			code, output, stderr := runCommand(t, args, nested, environment)
			if code != 0 || stderr != inspectProgress {
				t.Fatalf("inspect implementations = %d, %s, %s", code, output, stderr)
			}
			document := decodeInspectGraphCommandEnvelope(t, output)
			if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.Result.Type != "implementations" {
				t.Fatalf("unexpected graph identity: %#v", document)
			}
			for _, owner := range []struct{ module, id string }{{"inspect", "local"}, {"library", "direct"}, {"transitive", "transitive"}} {
				module := "example.com/acme/" + owner.module
				resourceID := "data." + owner.id + "/v1"
				node := inspectGraphNodeByID(t, document.Result.Nodes, "resource-contract:"+resourceID)
				if node.ResourceID != resourceID || node.Label != module+"/api" || !strings.HasPrefix(node.ContractDigest, "sha256:") || len(node.ContractDigest) != 71 || len(node.Sources) != 1 || node.Sources[0].Module != module || node.Sources[0].Path != "api/resource.go" || node.Sources[0].Kind != "resource-declaration" || node.Sources[0].Line != 2 {
					t.Fatalf("Resource contract lost identity or provenance: %#v", node)
				}
				names := make(map[string]int)
				ids := make(map[string]bool)
				for _, edge := range document.Result.Edges {
					if edge.To != node.ID || edge.Kind == "defines-resource" {
						continue
					}
					if edge.Kind != "declares-dependency" || edge.Reason != "resource" || edge.From != "constructor:"+module+"/consumer.New" || ids[edge.ID] || len(edge.Sources) != 1 || edge.Sources[0].Module != module || edge.Sources[0].Path != "consumer/service.go" || edge.Sources[0].Kind != "implementation-constructor" || edge.Sources[0].Line <= 0 {
						t.Fatalf("Resource dependency is not an exact authored declaration: %#v", edge)
					}
					names[edge.ParameterName] = edge.ParameterPosition
					ids[edge.ID] = true
				}
				if !reflect.DeepEqual(names, map[string]int{"database": 3, "Database": 4, "_database": 5, "\u03b4": 6}) || len(ids) != 4 {
					t.Fatalf("repeated Resource parameters collapsed: %#v", names)
				}
				assertInspectGraphEdge(t, document.Result.Edges, "defines-resource", "module:"+module, node.ID, "authored", module, "api/resource.go", "resource-declaration")
				assertInspectGraphEdge(t, document.Result.Edges, "declares-dependency", "constructor:"+module+"/consumer.New", "interface:app."+owner.id+"/v1", "required", module, "consumer/service.go", "implementation-constructor")
			}
			for _, edge := range document.Result.Edges {
				if edge.Kind == "assembles-constructor" || edge.Kind == "depends-on-interface" || edge.Kind == "requires-interface" || edge.Kind == "selects-constructor" {
					t.Fatalf("dormant Resource consumer acquired active graph state: %#v", edge)
				}
			}
			selectionPath := "plystra.yaml"
			if strings.HasPrefix(selector.name, "replacement") {
				selectionPath = "deploy/replacement.yaml"
			}
			assertInspectGraphEdge(t, document.Result.Edges, "dormant-selects-constructor", "interface:app.local/v1", "constructor:example.com/acme/inspect/consumer.New", "explicit", "example.com/acme/inspect", selectionPath, "implementation-selection")
			code, repeated, stderr := runCommand(t, append(args, "--verbose"), root, environment)
			if code != 0 || repeated != output || stderr != inspectProgress {
				t.Fatal("Resource consumer JSON is nondeterministic")
			}
			code, human, stderr := runCommand(t, append([]string{"inspect", "implementations", "--verbose"}, selector.args...), nested, environment)
			if code != 0 || stderr != "" {
				t.Fatalf("human inspection = %d, %s, %s", code, human, stderr)
			}
			for _, phrase := range []string{"Resource contracts: 3 visible", "Resource: data.local/v1", "-> data.local/v1 (resource)", "parameter 3 database", "parameter 4 Database", "parameter 5 _database", "parameter 6 \u03b4", "(dormant-explicit)", "(unselected-candidate)", "instance construction and binding are not supported", "Resolution evidence:"} {
				if !strings.Contains(human, phrase) {
					t.Fatalf("human inspection omits %q", phrase)
				}
			}
			for _, forbidden := range []string{root, filepath.ToSlash(root), "resolved-secret-marker", "PRIVATE_DEFAULT_MARKER", "constructor-entry-marker", "ordinary-marker"} {
				if strings.Contains(output+human, forbidden) {
					t.Fatalf("inspection disclosed %q", forbidden)
				}
			}
		})
	}
	for _, view := range []string{"resources", "interfaces"} {
		code, output, stderr := runCommand(t, []string{"inspect", view, "--format", "json"}, root, inspectCommandEnvironment(nil))
		if code != 0 || stderr != inspectProgress {
			t.Fatalf("%s = %d, %s, %s", view, code, output, stderr)
		}
		document := decodeInspectGraphCommandEnvelope(t, output)
		for _, node := range document.Result.Nodes {
			if node.Kind == "constructor" || (view == "resources" && node.Kind != "module" && node.Kind != "resource-contract") || (view == "interfaces" && node.Kind == "resource-contract") {
				t.Fatalf("%s view contains consumer state: %#v", view, node)
			}
		}
		for _, edge := range document.Result.Edges {
			if edge.ParameterName != "" || (view == "resources" && edge.Kind != "defines-resource") {
				t.Fatalf("%s contains consumer relationship: %#v", view, edge)
			}
		}
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("Resource consumer inspection mutated the Project or a dependency")
	}
}

func TestBuiltCLIInspectsLongResourceConsumerIdentities(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir = commandRepositoryRoot(t)
	// Preserve explicit build flags while retaining normal VCS checks by default.
	build.Env = commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, nested := createInspectResourceConsumerProject(t)
	longID := "data." + strings.Repeat("long", 300) + "/v1"
	writeCommandFile(t, filepath.Join(root, "api", "resource.go"), strings.Replace(commandResourceSource, "data.database/v1", longID, 1))
	consumerPath := filepath.Join(root, "consumer", "service.go")
	consumerSource, err := os.ReadFile(consumerPath)
	if err != nil {
		t.Fatal(err)
	}
	longParameter := strings.Repeat("database", 150)
	writeCommandFile(t, consumerPath, strings.Replace(string(consumerSource), "database, Database", longParameter+", Database", 1))
	before := snapshotInspectProject(t, root)
	var digest string
	for _, format := range []string{"json", "human"} {
		cmd := exec.CommandContext(t.Context(), binary, "inspect", "implementations", "--format", format)
		cmd.Dir, cmd.Env = nested, inspectCommandEnvironment(nil)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil || (format == "json" && stderr.String() != inspectProgress) || (format == "human" && stderr.Len() != 0) {
			t.Fatalf("built CLI: %v\n%s\n%s", err, output, &stderr)
		}
		if format == "json" {
			document := decodeInspectGraphCommandEnvelope(t, string(output))
			nodeID := diagnosticschema.GraphNodeID("resource-contract", longID)
			node := inspectGraphNodeByID(t, document.Result.Nodes, nodeID)
			digest = node.ContractDigest
			if node.ResourceID != longID || !strings.HasPrefix(nodeID, "resource-contract:sha256:") {
				t.Fatalf("bounded node lost full identity: %#v", node)
			}
			assertInspectGraphEdge(t, document.Result.Edges, "declares-dependency", "constructor:example.com/acme/inspect/consumer.New", nodeID, "resource", "example.com/acme/inspect", "consumer/service.go", "implementation-constructor")
			found := false
			for _, edge := range document.Result.Edges {
				if edge.ParameterName == longParameter {
					found = edge.ParameterPosition == 3 && edge.To == nodeID && strings.HasPrefix(edge.ID, "declares-dependency:sha256:")
				}
			}
			if !found {
				t.Fatal("bounded dependency ID lost exact parameter identity")
			}
		} else if !bytes.Contains(output, []byte("Resource: "+longID+"\n")) || !bytes.Contains(output, []byte("-> "+longID+" (resource)")) || !bytes.Contains(output, []byte(digest)) || !bytes.Contains(output, []byte("parameter 3 "+longParameter)) {
			t.Fatal("human graph lost full Resource identity or digest")
		}
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("built CLI modified the Project")
	}
}

func createInspectResourceConsumerProject(t testing.TB) (string, string) {
	t.Helper()
	root, nested := createInspectModuleGraphProject(t)
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/inspect\n\ngo 1.26\nrequire (\nexample.com/acme/library v0.0.0\nexample.com/acme/ordinary v0.0.0\n)\nreplace example.com/acme/library => ./library\nreplace example.com/acme/transitive => ./transitive\nreplace example.com/acme/ordinary => ./ordinary\n")
	writeCommandFile(t, filepath.Join(root, "library", "go.mod"), "module example.com/acme/library\n\ngo 1.26\nrequire example.com/acme/transitive v0.0.0\n")
	writeCommandFile(t, filepath.Join(root, "transitive", "go.mod"), "module example.com/acme/transitive\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "transitive", "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "ordinary", "go.mod"), "module example.com/acme/ordinary\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "ordinary", "invalid.go"), "package ordinary\n//plystra:resource ordinary-marker\ntype Resource interface{Close()}\n")
	manifest := "http: {address: resolved-secret-marker}\ninterfaces:\n  use:\n    app.local/v1: example.com/acme/inspect/consumer.New\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), manifest)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "replacement.yaml"), manifest)
	for _, owner := range []struct{ dir, module, id string }{{"", "inspect", "local"}, {"library", "library", "direct"}, {"transitive", "transitive", "transitive"}} {
		directory := filepath.Join(root, owner.dir)
		module := "example.com/acme/" + owner.module
		writeCommandFile(t, filepath.Join(directory, "api", "resource.go"), strings.Replace(commandResourceSource, "data.database/v1", "data."+owner.id+"/v1", 1))
		writeCommandGraphInterface(t, directory, "run/v1", "runv1", "app."+owner.id+"/v1", "Run")
		writeCommandFile(t, filepath.Join(directory, "consumer", "service.go"), fmt.Sprintf(`package consumer
import (
    "context"
    api %q
    runv1 %q
)
type Config struct { Label string `+"`plystra-default:\"PRIVATE_DEFAULT_MARKER\"`"+` }
type Service struct{}
//plystra:implements app.%s/v1
func New(cfg Config, required runv1.Interface, database, Database api.Resource, _database, %s api.Resource) (*Service, error) { panic("constructor-entry-marker") }
func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) { return runv1.Response{}, nil }
`, module+"/api", module+"/interfaces/run/v1", owner.id, "\u03b4"))
	}
	return root, nested
}
