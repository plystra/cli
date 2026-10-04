package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestBuiltCLIInspectsAndRejectsResourceConsumers(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, _ := writeCommandResourceConsumer(t, true)
	run := func(arguments ...string) (int, string, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, arguments...)
		cmd.Dir, cmd.Env = root, inspectCommandEnvironment(nil)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil {
			if failure, ok := err.(*exec.ExitError); ok {
				return failure.ExitCode(), stdout.String(), stderr.String()
			}
			t.Fatal(err)
		}
		return 0, stdout.String(), stderr.String()
	}
	before := snapshotInspectProject(t, root)
	exit, stdout, stderr := run("inspect", "implementations", "--format", "json")
	if exit != 0 || stderr != inspectProgress {
		t.Fatalf("built dormant inspection = %d: %s %s", exit, stdout, stderr)
	}
	graph := decodeInspectGraphCommandEnvelope(t, stdout)
	found := false
	for _, edge := range graph.Result.Edges {
		if edge.Kind == "declares-dependency" && edge.Reason == "resource" && edge.To == "resource-contract:storage.database/v1" && edge.ParameterName == "primary" && edge.ParameterPosition == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("compiled inspection lost exact Resource dependency")
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("compiled inspection changed Project files")
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.resource/v1]}\n")
	before = snapshotInspectProject(t, root)
	for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "implementations", "--format", "json"}} {
		exit, stdout, stderr := run(arguments...)
		if exit != 1 || !strings.Contains(stderr, diagnosticcode.ResourceBindingMissing) || !strings.Contains(stderr, "primary") {
			t.Fatalf("built activation %v = %d: %s %s", arguments, exit, stdout, stderr)
		}
		if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
			t.Fatal("compiled rejection changed Project files")
		}
	}
}

func TestPublicCommandsRejectReachableResourceConsumersWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, dependency := range []bool{false, true} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			name := mode + "/local"
			if dependency {
				name = mode + "/dependency"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root, owner := writeCommandResourceConsumer(t, dependency)
				path := "plystra.yaml"
				var selector []string
				switch mode {
				case "environment":
					path, selector = "plystra.production.yaml", []string{"--env", "production"}
				case "replacement":
					path, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(path)), "interfaces: {require: [app.resource/v1]}\n")
				before := snapshotInspectProject(t, root)
				commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "implementations", "--format", "json"}, {"explain", "capability", "kernel.health/v1"}, {"explain", "capability", "kernel.health/v1", "--format", "json"}}
				for _, arguments := range commands {
					arguments = append(append([]string(nil), arguments...), selector...)
					exit, stdout, stderr := runCommand(t, arguments, root, inspectCommandEnvironment(nil))
					wantExit := 1
					if arguments[0] == "explain" {
						wantExit = 3
					}
					if exit != wantExit {
						t.Fatalf("%v exit %d, want %d: %s %s", arguments, exit, wantExit, stdout, stderr)
					}
					if arguments[0] == "explain" && len(arguments) > 4 && arguments[3] == "--format" {
						var result struct {
							Status      string `json:"status"`
							Diagnostics []struct {
								Code      string                  `json:"code"`
								Locations []diagnosticjson.Source `json:"locations"`
							} `json:"diagnostics"`
						}
						if err := json.Unmarshal([]byte(stdout), &result); err != nil || stderr != "" || result.Status != "validation_failed" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != diagnosticcode.ResourceBindingMissing {
							t.Fatalf("structured Resource failure: %v: %s %s", err, stdout, stderr)
						}
						found := false
						for _, source := range result.Diagnostics[0].Locations {
							if source.Module == owner && source.Path == "consumer/service.go" && source.Kind == "implementation-constructor" && source.Line == 9 && source.Column == 6 {
								found = true
							}
						}
						if !found {
							t.Fatal("structured Resource failure lost constructor source")
						}
					} else if !commandContainsAll(stderr,
						"Diagnostic: "+diagnosticcode.ResourceBindingMissing,
						"Source: "+owner+":consumer/service.go:9:6 (implementation-constructor)",
						"Source: example.com/acme/inspect:"+path+":1:1 (declaration)",
						"storage.database/v1", "primary", "parameter 1", "Select a compatible named instance",
					) || strings.Count(stderr, "Diagnostic:") != 1 || strings.Count(stderr, "Recovery:") != 1 {
						t.Fatalf("%v Resource failure lost typed recovery: %s %s", arguments, stdout, stderr)
					}
					for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY"} {
						if strings.Contains(stdout+stderr, private) {
							t.Fatalf("%v leaked private input", arguments)
						}
					}
					if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
						t.Fatalf("%v changed Project or dependency files", arguments)
					}
					assertNoCommandTransactions(t, root)
				}
			})
		}
	}
}

func TestPublicCommandsLocateInvalidResourceParameters(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{"_ database.Resource", "primary *database.Resource"} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			root, owner := writeCommandResourceConsumer(t, true)
			writeCommandFile(t, filepath.Join(root, "library", "consumer", "service.go"), strings.Replace(commandResourceConsumerSource(owner), "primary database.Resource", declaration, 1))
			before := snapshotInspectProject(t, root)
			exit, stdout, stderr := runCommand(t, []string{"inspect", "implementations", "--format", "json"}, root, inspectCommandEnvironment(nil))
			if exit != 1 || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.ImplementationResourceInvalid) || !strings.Contains(stderr, "Source: "+owner+":consumer/service.go:9:6 (implementation-constructor)") {
				t.Fatalf("invalid Resource dependency: %d %s %s", exit, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, root) || strings.Contains(stdout+stderr, "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY") || !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
				t.Fatal("invalid Resource dependency leaked input or changed files")
			}
		})
	}
}

func writeCommandResourceConsumer(t testing.TB, dependency bool) (string, string) {
	t.Helper()
	root, _ := createInspectModuleGraphProject(t)
	owner, module := root, "example.com/acme/inspect"
	if dependency {
		owner, module = filepath.Join(root, "library"), "example.com/acme/library"
	}
	writeCommandFile(t, filepath.Join(owner, "database", "resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Health() error }\n")
	writeCommandGraphInterface(t, owner, "app/resource/v1", "resourcev1", "app.resource/v1", "Run")
	writeCommandFile(t, filepath.Join(owner, "consumer", "service.go"), commandResourceConsumerSource(module))
	return root, module
}

func commandResourceConsumerSource(module string) string {
	return `package consumer
import (
 "context"
 api "` + module + `/interfaces/app/resource/v1"
 database "` + module + `/database"
)
type Service struct{}
//plystra:implements app.resource/v1
func New(primary database.Resource) (*Service,error) { panic("PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY") }
func (*Service) Run(context.Context, api.Request) (api.Response,error) { return api.Response{},nil }
`
}
