package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulemutation"
)

type scaffoldPlanDocument struct {
	Status  string
	Effects struct{ Observed, Planned, Skipped, Unverified []struct{ ID, Class string } }
	Payload struct {
		PlanDigest       string `json:"plan_digest"`
		FinalModelDigest string `json:"final_model_digest"`
		Completeness     string
		Operations       []struct{ Kind, Name, Semantics, Contract, Package string }
		WriteSet         []struct {
			Path, Kind   string
			BeforeExists bool   `json:"before_exists"`
			AfterExists  bool   `json:"after_exists"`
			BeforeDigest string `json:"before_digest"`
			AfterDigest  string `json:"after_digest"`
		} `json:"write_set"`
		Verification        []string
		ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
	}
}

func TestRunChangePlanScaffoldsCompleteFinalCandidateReadOnly(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement", "absolute replacement", "ambient environment", "ambient absolute replacement", "explicit precedence"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			var selector []string
			var expectedSelector []string
			environment := implementationSelectionCommandEnvironment(nil)
			switch mode {
			case "environment", "ambient environment", "explicit precedence":
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
				expectedSelector = []string{"--env", "production"}
				selector = expectedSelector
				if mode == "ambient environment" {
					selector = nil
					environment = implementationSelectionCommandEnvironment(map[string]string{"PLYSTRA_ENV": "production"})
				} else if mode == "explicit precedence" {
					environment = implementationSelectionCommandEnvironment(map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "missing.yaml"})
				}
			case "replacement", "absolute replacement", "ambient absolute replacement":
				writeCommandFile(t, filepath.Join(root, "deploy", "${blue}.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
				expectedSelector = []string{"--config", "deploy/${blue}.yaml"}
				selector = expectedSelector
				if mode == "absolute replacement" {
					selector = []string{"--config", filepath.Join(root, "deploy", "${blue}.yaml")}
				} else if mode == "ambient absolute replacement" {
					selector = nil
					environment = implementationSelectionCommandEnvironment(map[string]string{"PLYSTRA_CONFIG": filepath.Join(root, "deploy", "${blue}.yaml")})
				}
			}
			// This authored package cannot load until both requested sources exist.
			writeCommandFile(t, filepath.Join(root, "pending", "pending.go"), `package pending
import (
  contract "example.com/acme/implementation-use/interfaces/todo/add/v1"
  implementation "example.com/acme/implementation-use/todo/add"
)
var _ contract.Interface = (*implementation.Service)(nil)
`)
			operations := scaffoldCommandOperations()
			var firstDigest string
			for _, reverse := range []bool{false, true} {
				if reverse && strings.Contains(mode, " ") {
					break
				}
				if reverse {
					slices.Reverse(operations)
				}
				data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
				if err != nil {
					t.Fatal(err)
				}
				writeCommandFile(t, filepath.Join(root, "change.json"), string(data))
				before := commandTree(t, root)
				args := append([]string{"change", "plan", "--request", "../change.json", "--format", "json"}, selector...)
				code, stdout, stderr := runCommand(t, args, filepath.Join(root, "reports"), environment)
				if code != 0 || stderr != "" {
					t.Fatalf("complete staged preview: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
				var result scaffoldPlanDocument
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != "success" || result.Payload.Completeness != "non_exact" || !strings.HasPrefix(result.Payload.FinalModelDigest, "sha256:") || len(result.Payload.Operations) != 4 {
					t.Fatalf("incomplete scaffold plan: %s", stdout)
				}
				assertChangePlanImpactArrays(t, result.Payload.ConfigurationImpact)
				wantVerification := append([]string{"plystra", "check"}, expectedSelector...)
				if !reflect.DeepEqual(result.Payload.Verification, wantVerification) {
					t.Fatalf("lost selected verification: %s", stdout)
				}
				wanted := map[string]bool{"interfaces/todo/add/v1/interface.go": false, "interfaces/todo/add/v1/interface.yaml": false, "todo/add/implementation.go": false, "dist/runtime-baseline.json": false, "dist/.gitignore": false, "generated/.plystra-manifest.json": false, "generated/manifest.json": false}
				for _, write := range result.Payload.WriteSet {
					if _, ok := wanted[write.Path]; ok {
						wanted[write.Path] = true
					}
					if write.Path == "dist/runtime-baseline.json" && (write.BeforeDigest != "" || write.AfterDigest != "") {
						t.Fatal("private baseline digest disclosed")
					}
				}
				for path, found := range wanted {
					if !found {
						t.Fatalf("missing planned write %s: %s", path, stdout)
					}
				}
				if len(result.Effects.Observed) < 3 || len(result.Effects.Unverified) < 2 || len(result.Effects.Planned) != len(result.Payload.WriteSet) {
					t.Fatalf("missing candidate effects: %s", stdout)
				}
				if !reverse {
					firstDigest = result.Payload.PlanDigest
				} else if result.Payload.PlanDigest != firstDigest {
					t.Fatalf("request order or private stage path changed plan identity: %s != %s", firstDigest, result.Payload.PlanDigest)
				}
				if !reflect.DeepEqual(before, commandTree(t, root)) {
					t.Fatal("scaffold preview changed authored Project")
				}
				if bytes.Contains([]byte(stdout), []byte(root)) {
					t.Fatal("private Project root disclosed")
				}
				assertNoCommandTransactions(t, root)
			}
		})
	}
}

func TestRunChangePlanScaffoldFailuresPreserveInputsAndEffects(t *testing.T) {
	for _, failure := range []string{"collision", "compile", "missing binding", "dependency", "stage capture"} {
		t.Run(failure, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			operations := scaffoldCommandOperations()
			switch failure {
			case "collision":
				writeCommandFile(t, filepath.Join(root, "todo", "add", "keep.txt"), "USER_FILE\n")
			case "compile":
				writeCommandFile(t, filepath.Join(root, "invalid", "invalid.go"), "package invalid\nvar _ = missingSymbol\n")
			case "missing binding":
				operations = append(operations, map[string]any{"kind": "interface_create", "name": "todo.other", "semantics": "query"}, map[string]any{"kind": "interface_root", "action": "present", "interface": "todo.other/v1", "root": "require"})
			case "dependency":
				operations = append(operations, map[string]any{"kind": "dependency", "action": "remove", "module": "example.com/other"})
			case "stage capture":
				asset := filepath.Join(t.TempDir(), "asset.txt")
				writeCommandFile(t, asset, "USER_ASSET\n")
				if err := os.Symlink(asset, filepath.Join(root, "linked-asset.txt")); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			writeCommandFile(t, filepath.Join(root, "change.json"), string(data))
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			var result scaffoldPlanDocument
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if code == 0 || stderr != "" || result.Status == "success" || result.Payload.PlanDigest != "" {
				t.Fatalf("invalid final candidate accepted: exit=%d %s %s", code, stdout, stderr)
			}
			if len(result.Effects.Observed) < 2 {
				t.Fatalf("known staging effects lost on failure: %s", stdout)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("failed preview changed Project")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func scaffoldCommandOperations() []map[string]any {
	return []map[string]any{
		{"kind": "implementation_create", "contract": "todo.add/v1", "package": "./todo/add"},
		{"kind": "interface_root", "action": "present", "interface": "todo.add/v1", "root": "expose"},
		{"kind": "interface_create", "name": "todo.add", "semantics": "command"},
		{"kind": "implementation_selection", "action": "set", "interface": "todo.add/v1", "constructor": "example.com/acme/implementation-use/todo/add.New"},
	}
}

func TestRunChangePlanScaffoldOnlyCandidates(t *testing.T) {
	for _, kind := range []string{"interface", "implementation", "resource provider"} {
		t.Run(kind, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			operation := map[string]any{"kind": "interface_create", "name": "todo.add", "semantics": "query"}
			want := "interfaces/todo/add/v1/interface.go"
			if kind != "interface" {
				contract := "email.send/v1"
				if kind == "resource provider" {
					contract = "storage.database/v1"
					writeCommandFile(t, filepath.Join(root, "database", "resource.go"), "package database\nimport \"context\"\n//plystra:resource storage.database/v1\ntype Resource interface { Health(context.Context) error }\n")
				}
				operation = map[string]any{"kind": "implementation_create", "contract": contract, "package": "./candidate"}
				want = "candidate/implementation.go"
			}
			result, _, _ := runScaffoldPlan(t, root, []map[string]any{operation})
			found := false
			for _, path := range result.Payload.WriteSet {
				found = found || path.Path == want && !path.BeforeExists && path.AfterExists
				if path.Path == "plystra.yaml" {
					t.Fatal("inactive scaffold unexpectedly edits application intent")
				}
			}
			if !found {
				t.Fatalf("missing scaffold creation %s", want)
			}
		})
	}
}

func TestRunChangePlanScaffoldsReportGeneratedDeletions(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  expose:\n    reports.read/v1: {transport: connect}\n")
	seedScaffoldGeneratedProject(t, root)
	operations := append(scaffoldCommandOperations(), map[string]any{"kind": "interface_root", "action": "absent", "interface": "reports.read/v1", "root": "expose"})
	result, _, _ := runScaffoldPlan(t, root, operations)
	found := false
	for _, path := range result.Payload.WriteSet {
		found = found || strings.Contains(path.Path, "/reports/read/v1/") && path.BeforeExists && !path.AfterExists
	}
	if !found {
		t.Fatal("removed exposure did not report removal of its generated surface")
	}
}

func TestRunChangePlanScaffoldsHidePrivateValueEquality(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "reports", "implementation.go"), `package reports
import (
 "context"
 "github.com/plystra/kernel/configuration"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Config struct {
 Endpoint string `+"`plystra:\"required\"`"+`
 Password configuration.Secret
}
type Service struct{}
//plystra:implements reports.read/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Read(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
	configuration := "interfaces:\n  require: []\n  use: {reports.read/v1: example.com/acme/implementation-use/reports.New}\nconfig:\n  example.com/acme/implementation-use/reports.New: {endpoint: %s, password: {env: %s}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), fmt.Sprintf(configuration, "PRIVATE_BEFORE", "PRIVATE_TARGET_A"))
	seedScaffoldGeneratedProject(t, root)
	operations := []map[string]any{
		{"kind": "interface_create", "name": "todo.add", "semantics": "query"},
		{"kind": "interface_root", "action": "present", "interface": "reports.read/v1", "root": "require"},
		{"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "endpoint", "value": "PRIVATE_AFTER"},
		{"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "password", "value": map[string]any{"env": "PRIVATE_TARGET_B"}},
	}
	var first json.RawMessage
	for _, pair := range [][2]string{{"PRIVATE_BEFORE", "PRIVATE_TARGET_A"}, {"PRIVATE_AFTER", "PRIVATE_TARGET_B"}} {
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), fmt.Sprintf(configuration, pair[0], pair[1]))
		_, payload, stdout := runScaffoldPlan(t, root, operations)
		if bytes.Contains([]byte(stdout), []byte("PRIVATE_")) {
			t.Fatal("private configuration leaked into scaffold plan")
		}
		if first == nil {
			first = payload
		} else if !bytes.Equal(first, payload) {
			t.Fatal("private value equality changed the public scaffold payload")
		}
	}
}

func TestBuiltCLIChangePlanScaffoldsFromStdinReadOnly(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	before := commandTree(t, root)
	request, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": scaffoldCommandOperations()})
	settings := filepath.Join(t.TempDir(), "env")
	writeCommandFile(t, settings, "GOPRIVATE=PRIVATE_GOENV_VALUE\n")
	for _, source := range []string{"off", settings} {
		command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
		command.Dir, command.Env = filepath.Join(root, "reports"), implementationSelectionCommandEnvironment(map[string]string{"PLYSTRA_ENV": "production", "GOENV": source})
		command.Stdin = bytes.NewReader(request)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		var result scaffoldPlanDocument
		if err != nil || stderr.Len() != 0 || json.Unmarshal(output, &result) != nil || result.Status != "success" || !reflect.DeepEqual(result.Payload.Verification, []string{"plystra", "check", "--env", "production"}) {
			t.Fatalf("compiled stdin preview: %v\n%s\n%s", err, output, &stderr)
		}
		if bytes.Contains(output, []byte(settings)) || bytes.Contains(output, []byte("PRIVATE_")) {
			t.Fatal("compiled stdin preview exposed external GOENV input")
		}
		assertChangePlanImpactArrays(t, result.Payload.ConfigurationImpact)
		if !reflect.DeepEqual(before, commandTree(t, root)) {
			t.Fatal("compiled stdin preview modified the Project")
		}
	}
	assertNoCommandTransactions(t, root)
}

func runScaffoldPlan(t *testing.T, root string, operations []map[string]any) (scaffoldPlanDocument, json.RawMessage, string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, filepath.Join(root, "change.json"), string(data))
	before := commandTree(t, root)
	code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	var result scaffoldPlanDocument
	if code != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &result) != nil || result.Status != "success" {
		t.Fatalf("scaffold preview: exit=%d\n%s\n%s", code, stdout, stderr)
	}
	assertChangePlanImpactArrays(t, result.Payload.ConfigurationImpact)
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("scaffold preview modified the Project")
	}
	assertNoCommandTransactions(t, root)
	var document struct{ Payload json.RawMessage }
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	return result, document.Payload, stdout
}

func seedScaffoldGeneratedProject(t *testing.T, root string) {
	t.Helper()
	environment := implementationSelectionCommandEnvironment(nil)
	err := modulemutation.Tidy(t.Context(), root, "", environment, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start: root, Environment: environment, MutateModule: mutate,
			Validate: func(ctx context.Context, directory string) error {
				return gocommand.Run(ctx, gocommand.Options{Directory: directory, Environment: environment}, "build", "-mod=readonly", "./...")
			},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
