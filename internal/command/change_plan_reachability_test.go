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
)

func TestRunChangePlanRequiredFieldsIgnoreOldResourceGraph(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			name := "dormant/"
			if active {
				name = "active/"
			}
			t.Run(name+mode, func(t *testing.T) {
				root, document := writeConfigurationPlanProject(t)
				path := filepath.Join(root, "smtp/implementation.go")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				source := strings.Replace(string(data), "Count uint64 }", "Count uint64 `plystra:\"required\"` }", 1)
				source = strings.Replace(source, "Unused string;", "Unused string `plystra-default:\"PRIVATE_DEFAULT\"`;", 1)
				writeCommandFile(t, path, source)
				if !active {
					document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
				}
				document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
				path, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, path), document)
					path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
				} else if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, path), document)
				selections := configurationBatchRequest(t,
					resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
					configurationPlanRequest(t, "database.primary", "endpoint", "set", json.RawMessage(`"PRIVATE_DATABASE"`)),
					rootConfigurationRequest(t, "require", "present"))
				request := configurationBatchRequest(t, selections, configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
				first := runConfigurationPlan(t, root, request, true, args...)
				fields := implementationRequiredFields(t, first, configurationImplementationOwner)
				want := map[string]bool{}
				if !active {
					want = map[string]bool{
						`config["` + configurationImplementationOwner + `"]["endpoint"]`:        false,
						`config["` + configurationImplementationOwner + `"]["nested"]["count"]`: false,
					}
				}
				if !reflect.DeepEqual(fields, want) {
					t.Fatalf("required fields after Resource repair = %v, want %v", fields, want)
				}
				second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
				third := runConfigurationPlan(t, root, configurationBatchRequest(t, selections, configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_OTHER"`))), true, args...)
				if !reflect.DeepEqual(first.Payload, second.Payload) || !reflect.DeepEqual(first.Payload, third.Payload) {
					t.Fatal("required-field reporting depends on operation order or private values")
				}
			})
		}
	}
}

func TestRunChangePlanRequiredFieldsIncludeTransitiveRepairOwners(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	writeCommandInterface(t, root, "reports/read/v1", "reports", "reports.read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
 "context"
 email "example.com/acme/implementation-use/interfaces/email/send/v1"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Config struct { Endpoint string `+"`plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements reports.read/v1
func New(cfg Config, sender email.Interface)(*Service,error){panic("must not execute")}
func (*Service) Read(context.Context,contract.Request)(contract.Response,error){return contract.Response{},nil}
`)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	owner := "example.com/acme/implementation-use/reports.New"
	activation := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("reports.read/v1"))
	request := configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
		activation, configurationPlanRequest(t, owner, "endpoint", "set", json.RawMessage(`"PRIVATE_REPORTS"`)))
	result := runConfigurationPlan(t, root, request, true)
	for _, requiredOwner := range []string{owner, configurationImplementationOwner} {
		if fields := implementationRequiredFields(t, result, requiredOwner); !reflect.DeepEqual(fields, map[string]bool{`config["` + requiredOwner + `"]["endpoint"]`: false}) {
			t.Fatalf("missing newly reachable transitive required fields for %s: %v", requiredOwner, fields)
		}
	}
	for _, invalid := range []string{
		strings.Replace(document, "endpoint: PRIVATE_IMPLEMENTATION_REQUIRED", "endpoint: [PRIVATE_INVALID]", 1),
		strings.Replace(document, "resources:\n", "resources:\n  bind: {instances: {database.primary: {Orphan: missing.instance}}}\n", 1),
	} {
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), invalid)
		runConfigurationPlan(t, root, request, false)
	}
}

func TestRunChangePlanRequiredFieldsKeepIncompleteInterfaceBoundary(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {email.send/v1: example.com/acme/implementation-use/missing.New}", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	request := configurationBatchRequest(t,
		implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner),
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider))
	result := runConfigurationPlan(t, root, request, true)
	if fields := implementationRequiredFields(t, result, configurationImplementationOwner); len(fields) != 0 {
		t.Fatalf("incomplete old Interface closure invented newly reachable fields: %v", fields)
	}
}

func TestRunChangePlanRequiredFieldsValidatePreviousConstructorAlternative(t *testing.T) {
	for _, previous := range []string{"invalid-binding", "invalid-config", "valid"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(previous+"/"+mode, func(t *testing.T) {
				root, document := writeConfigurationPlanProject(t)
				if previous == "invalid-binding" {
					path := filepath.Join(root, "smtp/implementation.go")
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					source := strings.Replace(string(data), "import (", "import (\n database \"example.com/acme/implementation-use/database\"", 1)
					writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(cfg Config, Primary database.Resource)", 1))
				} else if previous == "invalid-config" {
					document = strings.Replace(document, "endpoint: PRIVATE_IMPLEMENTATION_REQUIRED, ", "", 1)
				}
				path, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, path), document)
					path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "{}\n"
				} else if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, path), document)
				result := runConfigurationPlan(t, root, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), true, args...)
				decisions := result.Payload.ConfigurationImpact["unresolved_decisions"]
				if string(decisions) == "[]" || bytes.Contains(decisions, []byte("retain-previous-constructor")) != (previous == "valid") {
					t.Fatalf("missing-value decision offered an unverified previous state: %s", decisions)
				}
			})
		}
	}
}

func TestRunChangePlanRequiredFieldsDeduplicateSharedConstructor(t *testing.T) {
	root, document := writeSharedInterfaceBatchProject(t)
	document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	activation := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("email.copy/v1"))
	request := configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider), activation)
	result := runConfigurationPlan(t, root, request, true)
	if fields := implementationRequiredFields(t, result, configurationImplementationOwner); len(fields) != 0 {
		t.Fatalf("a second Interface made an already-reachable constructor new: %v", fields)
	}
}

func TestBuiltCLIChangePlanRequiredFieldsAfterResourceRepair(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
		rootConfigurationRequest(t, "require", "present")))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || bytes.Contains(output, []byte("PRIVATE_")) || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatalf("compiled Resource repair preview: %v\n%s\n%s", err, output, &stderr)
	}
	if fields := implementationRequiredFields(t, result, configurationImplementationOwner); !reflect.DeepEqual(fields, map[string]bool{`config["` + configurationImplementationOwner + `"]["endpoint"]`: false}) {
		t.Fatalf("compiled preview omitted newly reachable required fields: %v", fields)
	}
	assertNoCommandTransactions(t, root)
}

func implementationRequiredFields(t *testing.T, result configurationChangePlanResult, owner string) map[string]bool {
	t.Helper()
	var fields []struct {
		Owner              string
		Path               string
		SchemaSource       string `json:"schema_source"`
		RequirednessReason string `json:"requiredness_reason"`
		DefaultSatisfies   bool   `json:"default_satisfies"`
	}
	if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_required_fields"], &fields); err != nil {
		t.Fatal(err)
	}
	resultFields := map[string]bool{}
	for _, field := range fields {
		if field.Owner != owner {
			continue
		}
		if field.SchemaSource == "" || field.RequirednessReason != "required-field-on-newly-reachable-constructor" {
			t.Fatalf("required field lacks exact schema provenance: %#v", field)
		}
		if _, duplicate := resultFields[field.Path]; duplicate {
			t.Fatal("required field reported more than once")
		}
		resultFields[field.Path] = field.DefaultSatisfies
	}
	return resultFields
}
