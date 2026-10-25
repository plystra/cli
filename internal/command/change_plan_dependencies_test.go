package command_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulemutation"
)

const previewDependencyModule = "example.com/preview/remote"

func TestRunChangePlanRejectsInvalidAcquisitionManifest(t *testing.T) {
	const dependency = "example.com/batch/dependency"
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{{
		path: dependency, version: "v1.0.0", manifest: "{}\n",
		goMod: "module example.com/wrong\ngo 1.26\n",
	}})
	var acquired atomic.Bool
	files := http.FileServer(http.Dir(proxy))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+dependency+"/@v/v1.0.0.zip" {
			acquired.Store(true)
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/batch/app\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "app.go"), "package app\n")
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"`+dependency+`@v1.0.0"}]}`)
	before := commandTree(t, root)
	environment := append(commandDependencyEnvironment(t, proxy), "GOMODCACHE="+t.TempDir(), "GOPROXY="+server.URL)
	code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if code == 0 || stderr != "" || !strings.Contains(stdout, "PLYSTRA_CHANGE_PLAN_FAILED") {
		t.Fatalf("invalid acquired module accepted: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("rejected acquisition changed the original Project")
	}
	// This fixture has no Kernel dependency or transitive graph. Acquiring its
	// complete ZIP exercises settled metadata capture before manifest rejection.
	if !acquired.Load() {
		t.Fatal("public preview did not reach exact source acquisition")
	}
	assertNoCommandTransactions(t, root)
}

func TestRunChangePlanDependenciesResolveCompleteCandidate(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false)})
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	writeCommandFile(t, filepath.Join(root, "preview_test.go"), `package preview
import "testing"
func TestMustNotRunDuringPreview(t *testing.T) { t.Fatal("PREVIEW_MUST_NOT_RUN_TESTS") }
`)
	// The old authored tree cannot load these same-request sources or imports.
	writeCommandFile(t, filepath.Join(root, "pending", "pending.go"), `package pending
import (
  contract "example.com/preview/remote/echo/v1"
  candidate "example.com/acme/implementation-use/candidate"
)
var _ contract.Interface = (*candidate.Service)(nil)
`)
	operations := []map[string]any{
		{"kind": "dependency", "action": "add", "module": previewDependencyModule + "@v1.0.0"},
		{"kind": "implementation_create", "contract": "remote.echo/v1", "package": "./candidate"},
		{"kind": "interface_root", "action": "present", "interface": "remote.echo/v1", "root": "require"},
		{"kind": "implementation_selection", "action": "set", "interface": "remote.echo/v1", "constructor": previewDependencyModule + "/remote.New"},
		{"kind": "configuration", "action": "set", "owner": previewDependencyModule + "/remote.New", "path": "endpoint", "value": "PRIVATE_ENDPOINT"},
	}
	environment := previewDependencyEnvironment(t, proxy)
	var first json.RawMessage
	for _, reverse := range []bool{false, true} {
		if reverse {
			slices.Reverse(operations)
		}
		data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
		if err != nil {
			t.Fatal(err)
		}
		writeCommandFile(t, filepath.Join(root, "change.json"), string(data))
		before := commandTree(t, root)
		code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
		if code != 0 || stderr != "" {
			t.Fatalf("complete dependency preview: exit=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		var result scaffoldPlanDocument
		var envelope struct{ Payload json.RawMessage }
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		if result.Status != "success" || result.Payload.Completeness != "non_exact" || !strings.HasPrefix(result.Payload.FinalModelDigest, "sha256:") {
			t.Fatalf("incomplete dependency preview: %s", stdout)
		}
		assertChangePlanImpactArrays(t, result.Payload.ConfigurationImpact)
		for _, name := range []string{"go.mod", "go.sum", "candidate/implementation.go", "plystra.yaml", "dist/runtime-baseline.json"} {
			found := false
			for _, path := range result.Payload.WriteSet {
				found = found || path.Path == name && path.AfterExists
			}
			if !found {
				t.Fatalf("missing planned write %s: %s", name, stdout)
			}
		}
		if strings.Contains(stdout, "PRIVATE_ENDPOINT") || strings.Contains(stdout, root) {
			t.Fatalf("private candidate data disclosed: %s", stdout)
		}
		if reverse && !reflect.DeepEqual(first, envelope.Payload) {
			t.Fatal("request order or stage location changed dependency payload")
		}
		first = envelope.Payload
		if !reflect.DeepEqual(before, commandTree(t, root)) {
			t.Fatal("dependency preview changed original Project")
		}
		assertNoCommandTransactions(t, root)
	}
}

func TestRunChangePlanDependencyOnlyUsesSelectedConfiguration(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", true),
	})
	baseEnvironment := previewDependencyEnvironment(t, proxy)
	for _, mode := range []string{"default", "environment", "replacement", "ambient absolute replacement"} {
		for _, action := range []string{"add", "remove", "update"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
				environment := append([]string(nil), baseEnvironment...)
				var selector []string
				wantSelector := []string{}
				switch mode {
				case "environment":
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
					selector = []string{"--env", "production"}
					wantSelector = selector
				case "replacement", "ambient absolute replacement":
					writeCommandFile(t, filepath.Join(root, "deploy", "selected.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
					selector = []string{"--config", "deploy/selected.yaml"}
					wantSelector = selector
					if mode == "ambient absolute replacement" {
						environment = append(environment, "PLYSTRA_CONFIG="+filepath.Join(root, "deploy", "selected.yaml"))
						selector = nil
					}
				}
				query := previewDependencyModule + "@v1.0.0"
				if action != "add" {
					seedPreviewDependency(t, root, environment, "v1.0.0")
					if action == "update" {
						query = previewDependencyModule + "@v1.1.0"
					} else {
						query = previewDependencyModule
					}
				}
				request, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": []map[string]any{{"kind": "dependency", "action": action, "module": query}}})
				writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
				before := commandTree(t, root)
				args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, selector...)
				code, stdout, stderr := runCommand(t, args, root, environment)
				if code != 0 || stderr != "" {
					t.Fatalf("dependency-only plan: %d %s %s", code, stdout, stderr)
				}
				var result struct {
					Status  string
					Payload struct {
						Operations          []struct{ Kind, Action, Module string }
						Verification        []string
						ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
					}
				}
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != "success" || len(result.Payload.Operations) != 1 || result.Payload.Operations[0].Module != query || result.Payload.Operations[0].Action != action {
					t.Fatalf("dependency identity not bound: %s", stdout)
				}
				assertChangePlanImpactArrays(t, result.Payload.ConfigurationImpact)
				if !reflect.DeepEqual(result.Payload.Verification, append([]string{"plystra", "check"}, wantSelector...)) {
					t.Fatalf("lost selected verification: %s", stdout)
				}
				if !reflect.DeepEqual(before, commandTree(t, root)) {
					t.Fatal("dependency-only plan changed original Project")
				}
			})
		}
	}
}

func TestRunChangePlanDependencyFailurePreservesInputsAndActualEffects(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false)})
	environment := previewDependencyEnvironment(t, proxy)
	for _, failure := range []string{"prepare", "get", "missing required configuration", "unselected removal"} {
		t.Run(failure, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			operations := []map[string]any{{"kind": "dependency", "action": "add", "module": previewDependencyModule + "@v1.0.0"}}
			switch failure {
			case "prepare":
				operations[0]["module"] = previewDependencyModule + "@v1.9.0"
			case "get":
				operations[0]["module"] = previewDependencyModule + "@missing-revision"
			case "missing required configuration":
				operations = append(operations, map[string]any{"kind": "interface_root", "action": "present", "interface": "remote.echo/v1", "root": "require"})
			case "unselected removal":
				operations[0]["action"], operations[0]["module"] = "remove", previewDependencyModule
			}
			request, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
			var result scaffoldPlanDocument
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if code != 3 || stderr != "" || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || len(result.Effects.Planned) != 0 {
				t.Fatalf("invalid dependency candidate accepted: %d %s %s", code, stdout, stderr)
			}
			effects := result.Effects.Observed
			if failure == "prepare" || failure == "unselected removal" {
				effects = result.Effects.Skipped
			}
			found := false
			for _, effect := range effects {
				found = found || effect.ID == "candidate-dependencies"
			}
			if !found {
				t.Fatalf("actual get effect not accounted: %s", stdout)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("failed dependency preview changed original inputs")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunChangePlanDependencyUpdateReportsSameSymbolRequiredField(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{
		previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", true),
	})
	environment := previewDependencyEnvironment(t, proxy)
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			owner := previewDependencyModule + "/remote.New"
			document := "interfaces:\n  require: [reports.read/v1, remote.echo/v1]\nconfig:\n  " + owner + ": {endpoint: PRIVATE_ENDPOINT}\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			var selector []string
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
				selector = []string{"--env", "production"}
			} else if mode == "replacement" {
				writeCommandFile(t, filepath.Join(root, "deploy", "selected.yaml"), document)
				selector = []string{"--config", "deploy/selected.yaml"}
			}
			seedPreviewDependency(t, root, environment, "v1.0.0")
			operations := []map[string]any{
				{"kind": "dependency", "action": "update", "module": previewDependencyModule + "@v1.1.0"},
				{"kind": "configuration", "action": "set", "owner": owner, "path": "token", "value": "PRIVATE_TOKEN"},
			}
			var first json.RawMessage
			for _, reverse := range []bool{false, true} {
				if reverse {
					slices.Reverse(operations)
				}
				request, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
				writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
				before := commandTree(t, root)
				args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, selector...)
				code, stdout, stderr := runCommand(t, args, root, environment)
				if code != 0 || stderr != "" {
					t.Fatalf("same-symbol schema update: %d %s %s", code, stdout, stderr)
				}
				var result configurationChangePlanResult
				var envelope struct{ Payload json.RawMessage }
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
					t.Fatal(err)
				}
				var fields []struct{ Owner, Path string }
				if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_required_fields"], &fields); err != nil {
					t.Fatal(err)
				}
				if len(fields) != 1 || fields[0].Owner != owner || fields[0].Path != "config[\""+owner+"\"][\"token\"]" {
					t.Fatalf("lost same-symbol field impact: %s", stdout)
				}
				if strings.Contains(stdout, "PRIVATE_") || reverse && !reflect.DeepEqual(first, envelope.Payload) {
					t.Fatalf("private equality or request order changed plan: %s", stdout)
				}
				first = envelope.Payload
				if !reflect.DeepEqual(before, commandTree(t, root)) {
					t.Fatal("schema update preview changed original inputs")
				}
			}
		})
	}
}

func TestRunChangePlanDependencyUpdatePreservesOldPrivateIdentity(t *testing.T) {
	old := previewDependency(t, "v1.0.0", false)
	next := previewDependency(t, "v1.1.0", false)
	next.sources["remote/implementation.go"] = strings.Replace(next.sources["remote/implementation.go"], `plystra:"required"`, `plystra:"required,build-visible"`, 1)
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{old, next})
	environment := previewDependencyEnvironment(t, proxy)
	for _, unknownOld := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured-old-schema", true: "unknown-old-schema"}[unknownOld], func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			owner := previewDependencyModule + "/remote.New"
			seedPreviewDependency(t, root, environment, "v1.0.0")
			operations := []map[string]any{
				{"kind": "dependency", "action": "update", "module": previewDependencyModule + "@v1.1.0"},
				{"kind": "configuration", "action": "set", "owner": owner, "path": "endpoint", "value": "public-final"},
			}
			if unknownOld {
				writeCommandFile(t, filepath.Join(root, "pending", "pending.go"), `package pending
import candidate "example.com/acme/implementation-use/candidate"
var _ = candidate.New
`)
				operations = append(operations, map[string]any{"kind": "implementation_create", "contract": "remote.echo/v1", "package": "./candidate"})
			}
			request, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
			var first json.RawMessage
			for _, private := range []string{"PRIVATE_FIRST", "PRIVATE_SECOND"} {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1, remote.echo/v1]\n  use:\n    remote.echo/v1: "+owner+"\nconfig:\n  "+owner+": {endpoint: "+private+"}\n")
				before := commandTree(t, root)
				code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
				if code != 0 || stderr != "" {
					t.Fatalf("private old-schema preview: %d %s %s", code, stdout, stderr)
				}
				var result struct{ Payload json.RawMessage }
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(stdout, "PRIVATE_") || first != nil && !bytes.Equal(first, result.Payload) {
					t.Fatal("the new build-visible schema disclosed old private value equality")
				}
				first = result.Payload
				if !reflect.DeepEqual(before, commandTree(t, root)) {
					t.Fatal("old-schema preview changed original inputs")
				}
			}
		})
	}
}

func TestRunChangePlanDependencyUpdateCleansOnlyProvenOldOwnership(t *testing.T) {
	for _, change := range []string{"automatic constructor", "Resource parameter", "unknown parameter"} {
		t.Run(change, func(t *testing.T) {
			old := previewDependency(t, "v1.0.0", false)
			next := previewDependency(t, "v1.1.0", false)
			owner := previewDependencyModule + "/remote.New"
			document := "interfaces:\n  require: [reports.read/v1, remote.echo/v1]\nconfig:\n  " + owner + ": {endpoint: PRIVATE_ENDPOINT}\n"
			operations := []map[string]any{{"kind": "dependency", "action": "update", "module": previewDependencyModule + "@v1.1.0"}}
			if change == "automatic constructor" {
				next.sources["remote/implementation.go"] = strings.ReplaceAll(next.sources["remote/implementation.go"], "func New(", "func Replacement(")
				operations = append(operations, map[string]any{"kind": "configuration", "action": "set", "owner": previewDependencyModule + "/remote.Replacement", "path": "endpoint", "value": "PRIVATE_NEW"})
			} else {
				for _, candidate := range []*commandProxyModule{&old, &next} {
					candidate.sources["database/resource.go"] = `package database
//plystra:resource storage.database/v1
type Resource interface { Value() string }
`
					candidate.sources["databaseprovider/implementation.go"] = `package databaseprovider
type Pool struct{}
//plystra:implements-resource storage.database/v1
func New() (*Pool,error) { panic("PREVIEW_MUST_NOT_CONSTRUCT") }
func (*Pool) Value() string { return "" }
`
				}
				old.sources["remote/implementation.go"] = strings.Replace(old.sources["remote/implementation.go"], `"context"`, `"context"
 db "example.com/preview/remote/database"`, 1)
				old.sources["remote/implementation.go"] = strings.Replace(old.sources["remote/implementation.go"], "func New(Config)", "func New(config Config, database db.Resource)", 1)
				parameter := "database"
				if change == "unknown parameter" {
					parameter = "unowned"
				}
				document += "resources:\n  instances:\n    primary:\n      use: " + previewDependencyModule + "/databaseprovider.New\n  bind:\n    implementations:\n      " + owner + ":\n        " + parameter + ": primary\n"
			}
			proxy := writeCommandDependencyProxy(t, []commandProxyModule{old, next})
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			environment := previewDependencyEnvironment(t, proxy)
			seedPreviewDependency(t, root, environment, "v1.0.0")
			request, _ := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
			if change == "unknown parameter" {
				if code != 3 || !strings.Contains(stdout, `"status":"validation_failed"`) {
					t.Fatalf("unproven ownership was silently cleaned: %d %s %s", code, stdout, stderr)
				}
			} else {
				if code != 0 || stderr != "" {
					t.Fatalf("old ownership cleanup: %d %s %s", code, stdout, stderr)
				}
				var result configurationChangePlanResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if string(result.Payload.ConfigurationImpact["derived_operations"]) == "[]" || string(result.Payload.ConfigurationImpact["newly_unowned_paths"]) == "[]" {
					t.Fatalf("missing proven cleanup impact: %s", stdout)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("ownership preview modified original Project")
			}
		})
	}
}

func TestBuiltCLIChangePlanDependenciesFromStdin(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false)})
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
	command.Dir, command.Env = filepath.Join(root, "reports"), previewDependencyEnvironment(t, proxy)
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"example.com/preview/remote@v1.0.0"}]}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if output, err := command.Output(); err != nil || stderr.Len() != 0 {
		t.Fatalf("built stdin dependency preview: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("built stdin preview changed original Project")
	}
}

func TestRunChangePlanDependencyRemovalReplacesStaleGeneratedImports(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false)})
	environment := previewDependencyEnvironment(t, proxy)
	root := writeImplementationSelectionCommandProject(t)
	owner := previewDependencyModule + "/remote.New"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1, remote.echo/v1]\nconfig:\n  "+owner+": {endpoint: PRIVATE_ENDPOINT}\n")
	seedPreviewDependency(t, root, environment, "v1.0.0")
	if err := modulemutation.Tidy(t.Context(), root, "", environment, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, MutateModule: mutate, RejectUnexpected: true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	request := `{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"remove","module":"example.com/preview/remote"},{"kind":"interface_root","action":"absent","interface":"remote.echo/v1","root":"require"}]}`
	writeCommandFile(t, filepath.Join(root, "change.json"), request)
	before := commandTree(t, root)
	code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if code != 0 || stderr != "" {
		t.Fatalf("dependency removal with generated old imports: %d %s %s", code, stdout, stderr)
	}
	var result scaffoldPlanDocument
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	deleted := false
	for _, path := range result.Payload.WriteSet {
		deleted = deleted || strings.HasPrefix(path.Path, "generated/") && strings.Contains(path.Path, "/remote/echo/v1/") && path.BeforeExists && !path.AfterExists
	}
	if !deleted {
		t.Fatalf("removed dependency retained generated Interface sources: %s", stdout)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("preview removed original generated files or dependencies")
	}
}

func seedPreviewDependency(t *testing.T, root string, environment []string, version string) {
	t.Helper()
	options := gocommand.Options{Directory: root, Environment: environment}
	if err := gocommand.Run(t.Context(), options, "mod", "edit", "-require="+previewDependencyModule+"@"+version); err != nil {
		t.Fatal(err)
	}
	if err := gocommand.Run(t.Context(), options, "mod", "download", previewDependencyModule+"@"+version); err != nil {
		t.Fatal(err)
	}
}

func previewDependencyEnvironment(t *testing.T, proxy string) []string {
	t.Helper()
	cache, err := gocommand.Output(t.Context(), gocommand.Options{Environment: commandGoEnvironment()}, "env", "GOMODCACHE")
	if err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.ToSlash(filepath.Join(strings.TrimSpace(string(cache)), "cache", "download"))
	if runtime.GOOS == "windows" {
		cachePath = "/" + cachePath
	}
	// Each fixture may publish different bytes at the same test-only version.
	// Keep its selection isolated while using verified cached runtime archives.
	var environment []string
	for _, entry := range commandDependencyEnvironment(t, proxy) {
		name, value, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "GOMODCACHE":
			continue
		case "GOPROXY":
			entry = name + "=" + value + "," + (&url.URL{Scheme: "file", Path: cachePath}).String()
		}
		environment = append(environment, entry)
	}
	return append(environment, "GOMODCACHE="+t.TempDir())
}

func previewDependency(t *testing.T, version string, changed bool) commandProxyModule {
	t.Helper()
	fields := "Endpoint string `plystra:\"required\"`"
	if changed {
		fields += "\nToken string `plystra:\"required\"`"
	}
	return commandProxyModule{path: previewDependencyModule, version: version, manifest: "{}\n", sources: map[string]string{
		"echo/v1/interface.go": `package echov1
import "context"
//plystra:interface remote.echo/v1
type Interface interface { Echo(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`,
		"remote/implementation.go": `package remote
import (
 "context"
 contract "example.com/preview/remote/echo/v1"
)
type Config struct { ` + fields + ` }
type Service struct{}
//plystra:implements remote.echo/v1
func New(Config) (*Service, error) { panic("PREVIEW_MUST_NOT_CONSTRUCT") }
func (*Service) Echo(context.Context, contract.Request) (contract.Response, error) { panic("PREVIEW_MUST_NOT_CALL") }
`,
	}}
}
