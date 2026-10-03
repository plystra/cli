package command_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

const commandResourceSource = "package api\n//plystra:resource data.database/v1\ntype Resource interface{Read() Value}\ntype Value struct{Data string; private []byte}\n"

func TestBuiltCLIInspectsResourceContracts(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir = commandRepositoryRoot(t)
	build.Env = inspectCommandEnvironment(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, nested := createInspectModuleGraphProject(t)
	writeCommandFile(t, filepath.Join(root, "api", "resource.go"), commandResourceSource)
	writeCommandFile(t, filepath.Join(root, "provider", "provider.go"), commandResourceProviderSource("example.com/acme/inspect"))
	before := snapshotInspectProject(t, root)
	cmd := exec.CommandContext(t.Context(), binary, "inspect", "resources", "--format", "json")
	cmd.Dir = nested
	cmd.Env = inspectCommandEnvironment(nil)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil || stderr.String() != inspectProgress {
		t.Fatalf("built Resource inspection: %v\n%s\n%s", err, output, &stderr)
	}
	node := inspectGraphNodeByID(t, decodeInspectGraphCommandEnvelope(t, string(output)).Result.Nodes, "resource-contract:data.database/v1")
	if node.ResourceID != "data.database/v1" || len(node.ContractDigest) != 71 {
		t.Fatalf("built Resource record: %#v", node)
	}
	if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
		t.Fatal("built CLI modified Project")
	}
}

func TestInspectResourcesReportsDeterministicReadOnlyContracts(t *testing.T) {
	t.Parallel()
	root, nested := createInspectModuleGraphProject(t)
	local := filepath.Join(root, "api", "resource.go")
	writeCommandFile(t, local, commandResourceSource)
	writeCommandFile(t, filepath.Join(root, "library", "api", "resource.go"), strings.Replace(commandResourceSource, "data.database/v1", "data.remote/v1", 1))
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "replacement.yaml"), "{}\n")
	environment := inspectCommandEnvironment(nil)
	before := snapshotInspectProject(t, root)
	var first string
	var originalDigest string
	for _, selector := range [][]string{nil, {"--env", "production"}, {"--config", "deploy/replacement.yaml"}} {
		arguments := append([]string{"inspect", "resources", "--format", "json"}, selector...)
		code, stdout, stderr := runCommand(t, arguments, nested, environment)
		if code != 0 || stderr != inspectProgress {
			t.Fatalf("inspect resources = %d, %s, %s", code, stdout, stderr)
		}
		document := decodeInspectGraphCommandEnvelope(t, stdout)
		if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.Result.Type != "resources" || len(document.Result.Nodes) != 4 || len(document.Result.Edges) != 2 {
			t.Fatalf("Resource graph = %#v", document)
		}
		resource := inspectGraphNodeByID(t, document.Result.Nodes, "resource-contract:data.database/v1")
		if resource.ResourceID != "data.database/v1" || resource.Label != "example.com/acme/inspect/api" || len(resource.ContractDigest) != 71 || len(resource.Sources) != 1 || resource.Sources[0].Path != "api/resource.go" || resource.Sources[0].Kind != "resource-declaration" || resource.Sources[0].Line != 2 {
			t.Fatalf("Resource = %#v", resource)
		}
		if first == "" {
			first, originalDigest = stdout, resource.ContractDigest
		}
		if resource.ContractDigest != originalDigest {
			t.Fatal("selector changed Resource contract identity")
		}
		assertInspectGraphEdge(t, document.Result.Edges, "defines-resource", "module:example.com/acme/library", "resource-contract:data.remote/v1", "authored", "example.com/acme/library", "api/resource.go", "resource-declaration")
		for _, forbidden := range []string{root, "resolved-secret-marker", "private", "parameter_name", "selected-provider"} {
			if strings.Contains(stdout, forbidden) {
				t.Fatalf("Resource view contains %q", forbidden)
			}
		}
	}
	code, stdout, stderr := runCommand(t, []string{"inspect", "resources", "--verbose", "--format", "json"}, root, environment)
	if code != 0 || stdout != first || stderr != inspectProgress {
		t.Fatal("repeated Resource view changed")
	}
	code, stdout, stderr = runCommand(t, []string{"inspect", "resources", "--verbose"}, nested, environment)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Resource contracts: 2 visible") || !strings.Contains(stdout, originalDigest) || !strings.Contains(stdout, "Resource instance construction and binding are not supported.") || !strings.Contains(stdout, "Resolution evidence:") {
		t.Fatalf("human Resource view = %d, %s, %s", code, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("Resource inspection mutated files")
	}
	writeCommandFile(t, local, strings.Replace(commandResourceSource, "private []byte", "private int", 1))
	code, stdout, stderr = runCommand(t, []string{"inspect", "resources", "--format", "json"}, root, environment)
	if code != 0 || stdout != first {
		t.Fatalf("private change altered Resource output: %d, %s, %s", code, stdout, stderr)
	}
	writeCommandFile(t, local, strings.Replace(commandResourceSource, "Data string", "Data []byte", 1))
	code, stdout, stderr = runCommand(t, []string{"inspect", "resources", "--format", "json"}, root, environment)
	if code != 0 {
		t.Fatalf("inspect changed contract: %d, %s, %s", code, stdout, stderr)
	}
	changed := inspectGraphNodeByID(t, decodeInspectGraphCommandEnvelope(t, stdout).Result.Nodes, "resource-contract:data.database/v1")
	if changed.ContractDigest == originalDigest {
		t.Fatal("transitive public type edit did not change recorded digest")
	}
	longID := "data." + strings.Repeat("long", 300) + "/v1"
	writeCommandFile(t, local, strings.Replace(commandResourceSource, "data.database/v1", longID, 1))
	code, stdout, stderr = runCommand(t, []string{"inspect", "resources", "--format", "json"}, root, environment)
	if code != 0 {
		t.Fatalf("inspect long identity: %d, %s, %s", code, stdout, stderr)
	}
	found := false
	for _, node := range decodeInspectGraphCommandEnvelope(t, stdout).Result.Nodes {
		if node.ResourceID == longID {
			found = true
		}
	}
	if !found {
		t.Fatal("bounded graph ID lost the exact Resource identity")
	}
	code, stdout, stderr = runCommand(t, []string{"inspect", "resources"}, root, environment)
	if code != 0 || !strings.Contains(stdout, "Resource: "+longID+"\n") {
		t.Fatalf("human long identity: %d, %s, %s", code, stdout, stderr)
	}
}

func TestPublicCommandsRejectInvalidResourceContractsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source, code, kind string
		duplicate                bool
	}{
		{"identity", strings.Replace(commandResourceSource, "data.database/v1", "invalid-private-marker", 1), diagnosticcode.ResourceDeclarationInvalid, "resource-declaration", false},
		{"target", strings.Replace(commandResourceSource, "type Resource interface", "type Other interface", 1), diagnosticcode.ResourceDeclarationInvalid, "resource-declaration", false},
		{"mixed", strings.Replace(commandResourceSource, "type Resource", "//plystra:interface data.database/v1\ntype Resource", 1), diagnosticcode.ResourceDeclarationInvalid, "resource-declaration", false},
		{"lifecycle", strings.Replace(commandResourceSource, "Read() Value", "Close()", 1), diagnosticcode.ResourceContractInvalid, "resource-contract", false},
		{"depth", strings.Replace(commandResourceSource, "Read() Value", "Read("+strings.Repeat("*", 65)+"int)", 1), diagnosticcode.ResourceContractInvalid, "resource-contract", false},
		{"duplicate", commandResourceSource, diagnosticcode.ResourceIDDuplicate, "resource-declaration", true},
	} {
		for _, dependency := range []bool{false, true} {
			name := test.name + "/local"
			if dependency {
				name = test.name + "/dependency"
			}
			t.Run(name, func(t *testing.T) {
				root, _ := createInspectModuleGraphProject(t)
				owner, module := root, "example.com/acme/inspect"
				if dependency {
					owner, module = filepath.Join(root, "library"), "example.com/acme/library"
				}
				writeCommandFile(t, filepath.Join(owner, "api", "resource.go"), test.source)
				if test.duplicate {
					writeCommandFile(t, filepath.Join(root, "other", "resource.go"), test.source)
				}
				before := snapshotInspectProject(t, root)
				for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "resources"}} {
					code, _, stderr := runCommand(t, arguments, root, inspectCommandEnvironment(nil))
					if code != 1 || !strings.Contains(stderr, "Diagnostic: "+test.code) || !strings.Contains(stderr, "Source: "+module+":api/resource.go:2:1 ("+test.kind+")") {
						t.Fatalf("%v = %d, %s", arguments, code, stderr)
					}
					if test.duplicate && !strings.Contains(stderr, "Source: example.com/acme/inspect:other/resource.go:2:1 (resource-declaration)") {
						t.Fatalf("missing duplicate source: %s", stderr)
					}
					for _, forbidden := range []string{root, "resolved-secret-marker", "invalid-private-marker", "private []byte"} {
						if strings.Contains(stderr, forbidden) {
							t.Fatalf("failure disclosed %q: %s", forbidden, stderr)
						}
					}
					if after := snapshotInspectProject(t, root); !reflect.DeepEqual(before, after) {
						t.Fatalf("%v modified rejected Project", arguments)
					}
				}
			})
		}
	}
}
