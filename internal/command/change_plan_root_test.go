package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type rootChangePlanResult struct {
	Status  string `json:"status"`
	Payload struct {
		Completeness string                  `json:"completeness"`
		PlanDigest   string                  `json:"plan_digest"`
		WriteSet     []struct{ Path string } `json:"write_set"`
		Operations   []struct {
			Kind, Action, Interface, Root, Path string
			Derived                             bool
		} `json:"operations"`
		ConfigurationImpact map[string]json.RawMessage          `json:"configuration_impact"`
		Effects             struct{ Planned []json.RawMessage } `json:"effects"`
	} `json:"payload"`
	Effects struct{ Planned []json.RawMessage } `json:"effects"`
}

func TestRunChangePlanInterfaceRootReportsOwnershipAndScope(t *testing.T) {
	for _, selector := range []struct {
		name, path string
		args       []string
	}{
		{"default", "plystra.yaml", nil},
		{"environment", "plystra.production.yaml", []string{"--env", "production"}},
		{"replacement", "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}},
	} {
		for _, rootKind := range []string{"require", "expose"} {
			for _, action := range []string{"present", "absent"} {
				t.Run(selector.name+"/"+rootKind+"/"+action, func(t *testing.T) {
					root := writeResourceSelectionCommandProject(t)
					writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
					writeCommandFile(t, filepath.Join(root, "smtp/implementation.go"), `package smtp
import (
  "context"
  "example.com/acme/implementation-use/database"
  contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)
type Config struct { Endpoint string `+"`plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements email.send/v1
func New(cfg Config, db database.Resource) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
					document := `resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_PRIMARY}
    database.retained:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_RETAINED}
  bind:
    implementations:
      example.com/acme/implementation-use/smtp.New: {db: database.primary}
config:
  example.com/acme/implementation-use/smtp.New: {endpoint: PRIVATE_ENDPOINT}
`
					// A dormant explicit choice owns Config before activation. Removal
					// instead starts with an automatic owner that loses its last root.
					if action == "present" {
						document += "interfaces:\n  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}\n"
					} else if rootKind == "require" {
						document += "interfaces:\n  require: [email.send/v1]\n"
					} else {
						document += "http:\n  expose: {email.send/v1: {transport: connect}}\n"
					}
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), document)
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
					result := runRootChangePlan(t, root, "email.send/v1", rootKind, action, selector.args...)
					if len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != selector.path {
						t.Fatalf("wrong write scope: %#v", result.Payload.WriteSet)
					}
					requested, cleanup := 0, 0
					for _, operation := range result.Payload.Operations {
						if !operation.Derived {
							if operation.Kind != "interface_root" || operation.Action != action || operation.Root != rootKind || operation.Interface != "email.send/v1" {
								t.Fatalf("wrong requested root: %#v", operation)
							}
							requested++
						} else {
							if operation.Action != "remove" || operation.Path != "config.example.com/acme/implementation-use/smtp.New" && operation.Path != `resources.bind.implementations["example.com/acme/implementation-use/smtp.New"]["db"]` {
								t.Fatalf("unexpected companion edit: %#v", operation)
							}
							cleanup++
						}
					}
					if requested != 1 || action == "absent" && cleanup != 2 || action == "present" && cleanup != 0 {
						t.Fatalf("root operations = %#v", result.Payload.Operations)
					}
					var retained []struct{ Owner string }
					if err := json.Unmarshal(result.Payload.ConfigurationImpact["retained_owners"], &retained); err != nil {
						t.Fatal(err)
					}
					for _, name := range []string{"database.primary", "database.retained"} {
						if !slices.ContainsFunc(retained, func(value struct{ Owner string }) bool { return value.Owner == name }) {
							t.Fatalf("root edit lost Resource instance %s: %#v", name, retained)
						}
					}
					var unowned []struct{ Owner, Path, Source string }
					if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_unowned_paths"], &unowned); err != nil {
						t.Fatal(err)
					}
					if len(unowned) != cleanup {
						t.Fatalf("unowned paths = %#v", unowned)
					}
					for _, path := range unowned {
						source := selector.path
						if selector.name == "environment" {
							source = "plystra.yaml"
						}
						if path.Owner != "example.com/acme/implementation-use/smtp.New" || !strings.HasPrefix(path.Source, source+" ") {
							t.Fatalf("wrong cleanup provenance: %#v", path)
						}
					}
				})
			}
		}
	}
}

func TestRunChangePlanInterfaceRootRetainsSurvivingOwners(t *testing.T) {
	for _, reason := range []string{"exposure", "requirement", "dependency", "dormant selection"} {
		t.Run(reason, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			document := "config:\n  example.com/acme/implementation-use/smtp.New: {endpoint: PRIVATE_ENDPOINT}\n"
			rootKind := "require"
			switch reason {
			case "exposure":
				document += "interfaces: {require: [email.send/v1]}\nhttp: {expose: {email.send/v1: {transport: connect}}}\n"
			case "requirement":
				rootKind = "expose"
				document += "interfaces: {require: [email.send/v1]}\nhttp: {expose: {email.send/v1: {transport: connect}}}\n"
			case "dormant selection":
				document += "interfaces: {require: [email.send/v1], use: {email.send/v1: example.com/acme/implementation-use/smtp.New}}\n"
			case "dependency":
				document += "interfaces: {require: [email.send/v1, reports.read/v1]}\n"
				writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
  "context"
  email "example.com/acme/implementation-use/interfaces/email/send/v1"
  contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Service struct{}
//plystra:implements reports.read/v1
func New(sender email.Interface) (*Service, error) { return &Service{}, nil }
func (*Service) Read(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			result := runRootChangePlan(t, root, "email.send/v1", rootKind, "absent")
			if len(result.Payload.Operations) != 1 || string(result.Payload.ConfigurationImpact["newly_unowned_paths"]) != "[]" || !bytes.Contains(result.Payload.ConfigurationImpact["retained_owners"], []byte("example.com/acme/implementation-use/smtp.New")) {
				t.Fatalf("surviving owner cleaned up: %#v", result.Payload)
			}
		})
	}
}

func TestRunChangePlanInterfaceRootRequiredDecisionsAndFailures(t *testing.T) {
	for _, rootKind := range []string{"require", "expose"} {
		for _, failure := range []string{"missing value", "ambiguous", "missing implementation", "unknown interface", "invalid still-owned config"} {
			t.Run(rootKind+"/"+failure, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
				if failure != "ambiguous" {
					writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
				}
				document := "interfaces: {require: [reports.read/v1]}\n"
				target, action := "email.send/v1", "present"
				switch failure {
				case "missing implementation":
					writeCommandFile(t, filepath.Join(root, "smtp/implementation.go"), "package smtp\n")
				case "unknown interface":
					target = "unknown.run/v1"
				case "invalid still-owned config":
					action = "absent"
					document = "interfaces: {require: [email.send/v1], use: {email.send/v1: example.com/acme/implementation-use/smtp.New}}\nhttp: {expose: {email.send/v1: {transport: connect}}}\nconfig: {example.com/acme/implementation-use/smtp.New: {endpoint: [PRIVATE_INVALID]}}\n"
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
				writeRootChangeRequest(t, root, target, rootKind, action)
				before := commandTree(t, root)
				exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
				if stderr != "" || strings.Contains(stdout, "PRIVATE_") {
					t.Fatalf("unsafe result: %s\n%s", stdout, stderr)
				}
				var result rootChangePlanResult
				if err := json.Unmarshal([]byte(stdout), &result); err != nil {
					t.Fatal(err)
				}
				if failure == "missing value" {
					if exit != 0 || result.Status != "success" || result.Payload.Completeness != "non_exact" {
						t.Fatalf("missing field rejected: %s", stdout)
					}
					var decisions []struct {
						Target, Reason        string
						Alternatives, Sources []string
					}
					if err := json.Unmarshal(result.Payload.ConfigurationImpact["unresolved_decisions"], &decisions); err != nil {
						t.Fatal(err)
					}
					if len(decisions) != 1 || decisions[0].Target != `config["example.com/acme/implementation-use/smtp.New"]["endpoint"]` || decisions[0].Reason != "required-configuration-value-missing" || !slices.Contains(decisions[0].Alternatives, "retain-previous-root-state") || len(decisions[0].Sources) == 0 {
						t.Fatalf("wrong required decision: %#v", decisions)
					}
				} else if exit == 0 || result.Status != "validation_failed" {
					t.Fatalf("invalid candidate accepted: %s", stdout)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) {
					t.Fatal("root preview changed Project files")
				}
			})
		}
	}
}

func TestRunChangePlanInterfaceRootSatisfiedHasNoWrites(t *testing.T) {
	for _, selector := range [][]string{nil, {"--env", "production"}, {"--config", "deploy/customer.yaml"}} {
		for _, rootKind := range []string{"require", "expose"} {
			for _, action := range []string{"present", "absent"} {
				t.Run(fmt.Sprint(selector)+rootKind+action, func(t *testing.T) {
					root := writeImplementationSelectionCommandProject(t)
					document := "interfaces: {require: [reports.read/v1]}\nhttp: {expose: {reports.read/v1: {transport: connect}}}\n"
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), document)
					overlay := "{}\n"
					target := "reports.read/v1"
					if action == "absent" {
						target = "email.send/v1"
						overlay = "interfaces: {require: {remove: [email.send/v1]}}\nhttp: {expose: {email.send/v1: {$remove: true}}}\n"
					}
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlay)
					result := runRootChangePlan(t, root, target, rootKind, action, selector...)
					if len(result.Payload.WriteSet) != 0 || len(result.Payload.Effects.Planned) != 0 || len(result.Effects.Planned) != 0 || len(result.Payload.Operations) != 1 {
						t.Fatalf("satisfied root advertised writes: %#v", result)
					}
				})
			}
		}
	}
}

func TestRunChangePlanInterfaceRootDoesNotHideOtherRequiredFailures(t *testing.T) {
	for _, failure := range []string{"new array owner", "retained owner", "dormant owner", "retained Resource"} {
		t.Run(failure, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
			writeCommandFile(t, filepath.Join(root, "smtp/implementation.go"), "package smtp\n")
			writeCommandConfigurableImplementation(t, root, "a", "email.send/v1", "email/send/v1", "Send")
			document := "{}\n"
			target := "email.send/v1"
			switch failure {
			case "new array owner":
				target = "reports.read/v1"
				writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), "package reports\n")
				writeCommandFile(t, filepath.Join(root, "z/implementation.go"), `package z
import (
  "context"
  email "example.com/acme/implementation-use/interfaces/email/send/v1"
  contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Item struct { Value string `+"`plystra:\"required\"`"+` }
type Config struct { Items [1]Item }
type Service struct{}
//plystra:implements reports.read/v1
func New(cfg Config, sender email.Interface) (*Service, error) { return &Service{}, nil }
func (*Service) Read(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
			case "retained owner":
				writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
				document = "interfaces: {require: [reports.read/v1]}\n"
			case "dormant owner":
				writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
				document = "interfaces: {use: {reports.read/v1: example.com/acme/implementation-use/reports.New}}\nconfig: {example.com/acme/implementation-use/reports.New: {}}\n"
			case "retained Resource":
				writeCommandFile(t, filepath.Join(root, "original/provider.go"), `package original
type Config struct { Value string `+"`plystra:\"required\"`"+` }
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
				document = "resources: {instances: {database.primary: {use: example.com/acme/implementation-use/original.New}}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeRootChangeRequest(t, root, target, "require", "present")
			before := commandTree(t, root)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			var result rootChangePlanResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || exit == 0 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || stderr != "" {
				t.Fatalf("hidden requiredness failure accepted: %d\n%s\n%s\n%v", exit, stdout, stderr, err)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) {
				t.Fatal("failed preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanInterfaceRootReportsEveryNewOwnerMissingField(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
	writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
  "context"
  email "example.com/acme/implementation-use/interfaces/email/send/v1"
  contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Config struct { Endpoint string `+"`plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements reports.read/v1
func New(cfg Config, sender email.Interface) (*Service, error) { return &Service{}, nil }
func (*Service) Read(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	result := runRootChangePlan(t, root, "reports.read/v1", "require", "present")
	var decisions []struct{ Target string }
	if err := json.Unmarshal(result.Payload.ConfigurationImpact["unresolved_decisions"], &decisions); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Target != `config["example.com/acme/implementation-use/reports.New"]["endpoint"]` || decisions[1].Target != `config["example.com/acme/implementation-use/smtp.New"]["endpoint"]` {
		t.Fatalf("lost new owner missing field: %#v", decisions)
	}
}

func TestRunChangePlanInterfaceRootOmitsInvalidPreviousStateAlternative(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [reports.read/v1]
  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}
config: {example.com/acme/implementation-use/smtp.New: {}}
`)
	result := runRootChangePlan(t, root, "email.send/v1", "require", "present")
	var decisions []struct{ Alternatives []string }
	if err := json.Unmarshal(result.Payload.ConfigurationImpact["unresolved_decisions"], &decisions); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !slices.Equal(decisions[0].Alternatives, []string{"provide-required-field"}) {
		t.Fatalf("invalid previous state offered: %#v", decisions)
	}
}

func TestRunChangePlanInterfaceRootRemovesInvalidLastOwnerConfiguration(t *testing.T) {
	for _, rootKind := range []string{"require", "expose"} {
		t.Run(rootKind, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "local/implementation.go"), "package local\n")
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			document := "config: {example.com/acme/implementation-use/smtp.New: {endpoint: [PRIVATE_INVALID]}}\n"
			if rootKind == "require" {
				document += "interfaces: {require: [email.send/v1]}\n"
			} else {
				document += "http: {expose: {email.send/v1: {transport: connect}}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			result := runRootChangePlan(t, root, "email.send/v1", rootKind, "absent")
			if len(result.Payload.Operations) != 2 || len(result.Payload.WriteSet) != 1 {
				t.Fatalf("invalid obsolete Config blocked cleanup: %#v", result.Payload)
			}
		})
	}
}

func TestBuiltCLIChangePlanInterfaceRootFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [reports.read/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "reports"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":"absent","interface":"reports.read/v1","root":"require"}]}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("compiled root preview: %v\n%s\n%s", err, output, &stderr)
	}
	var result rootChangePlanResult
	if err := json.Unmarshal(output, &result); err != nil || result.Status != "success" || len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != "plystra.production.yaml" {
		t.Fatalf("compiled result: %s, %v", output, err)
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("compiled preview changed Project files")
	}
	assertNoCommandTransactions(t, root)
}

func writeRootChangeRequest(t testing.TB, root, target, rootKind, action string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "change.json"), fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":%q,"interface":%q,"root":%q}]}`, action, target, rootKind))
}

func runRootChangePlan(t *testing.T, root, target, rootKind, action string, selector ...string) rootChangePlanResult {
	t.Helper()
	writeRootChangeRequest(t, root, target, rootKind, action)
	before := commandTree(t, root)
	args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, selector...)
	var first rootChangePlanResult
	for attempt := 0; attempt < 2; attempt++ {
		exit, stdout, stderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
		if exit != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
			t.Fatalf("root preview = %d\n%s\n%s", exit, stdout, stderr)
		}
		var result rootChangePlanResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "success" || result.Payload.Completeness != "non_exact" || !strings.HasPrefix(result.Payload.PlanDigest, "sha256:") {
			t.Fatalf("root result: %s", stdout)
		}
		for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
			if value := result.Payload.ConfigurationImpact[name]; len(value) == 0 || string(value) == "null" {
				t.Fatalf("missing impact %s: %s", name, stdout)
			}
		}
		if attempt == 0 {
			first = result
		} else if first.Payload.PlanDigest != result.Payload.PlanDigest {
			t.Fatal("root plan digest is unstable")
		}
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("root preview changed Project files")
	}
	assertNoCommandTransactions(t, root)
	return first
}
