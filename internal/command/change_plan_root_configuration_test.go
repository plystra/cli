package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

func TestRunChangePlanRootConfigurationActivatesFinalOwners(t *testing.T) {
	for _, kind := range []string{"require", "expose"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				root, document := writeConfigurationPlanProject(t)
				if err := os.Remove(filepath.Join(root, "local/implementation.go")); err != nil {
					t.Fatal(err)
				}
				implementationPath := filepath.Join(root, "smtp/implementation.go")
				implementation, err := os.ReadFile(implementationPath)
				if err != nil {
					t.Fatal(err)
				}
				writeCommandFile(t, implementationPath, strings.Replace(string(implementation), "Value string;", "Value string `plystra:\"required\"`;", 1))
				document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
				document = strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {}", 1)
				document = document[:strings.Index(document, "\nconfig:")] + "\nconfig: {}\n"
				path, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, path), document)
					path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
				} else if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, path), document)
				activation := rootConfigurationRequest(t, kind, "present")
				value := configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_NEW"`))
				runConfigurationPlan(t, root, configurationBatchRequest(t, activation, value), false, args...)
				endpoint := configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`""`))
				runConfigurationPlan(t, root, configurationBatchRequest(t, activation, endpoint), false, args...)
				request := configurationBatchRequest(t, activation, value, endpoint)
				first := runConfigurationPlan(t, root, request, true, args...)
				second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
				if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != path || string(first.Payload.ConfigurationImpact["unresolved_decisions"]) != "[]" {
					t.Fatal("root/configuration preview lost final-state scope or order independence")
				}
			})
		}
	}
}

func TestRunChangePlanRootConfigurationCleansOnlyLostOwners(t *testing.T) {
	for _, kind := range []string{"require", "expose"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				root, document := writeAutomaticRootConfigurationProject(t)
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    implementations:\n      "+configurationImplementationOwner+": {Obsolete: database.primary}\n", 1)
				if kind == "expose" {
					document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1) + "http: {expose: {email.send/v1: {transport: connect}}}\n"
				}
				path, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, path), document)
					path, args = "plystra.production.yaml", []string{"--env", "production"}
					document = "# KEEP_OVERLAY\nconfig: {" + configurationImplementationOwner + ": {value: [PRIVATE_INVALID]}}\nresources: {bind: {implementations: {" + configurationImplementationOwner + ": {Local: database.retained, Preserve: {$remove: true}}}}}\n"
				} else {
					document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION", "value: [PRIVATE_INVALID]", 1)
					if mode == "replacement" {
						path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
					}
				}
				writeCommandFile(t, filepath.Join(root, path), document)
				request := configurationBatchRequest(t, rootConfigurationRequest(t, kind, "absent"), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
				result := runConfigurationPlan(t, root, request, true, args...)
				if !bytes.Contains(result.Payload.ConfigurationImpact["newly_unowned_paths"], []byte(configurationImplementationOwner)) || !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte("Obsolete")) {
					t.Fatal("last automatic owner's Config or exact outgoing cleanup is missing")
				}
				plan := previewRootConfiguration(t, root, request, args...)
				var updated map[string]any
				if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
					t.Fatal(err)
				}
				config := updated["config"].(map[string]any)
				if mode == "environment" {
					if !reflect.DeepEqual(config[configurationImplementationOwner], map[string]any{"$remove": true}) || !bytes.Contains(plan.Updated(), []byte("# KEEP_OVERLAY")) {
						t.Fatal("inherited owner cleanup lost its exact exclusion or comments")
					}
					bindings := updated["resources"].(map[string]any)["bind"].(map[string]any)["implementations"].(map[string]any)[configurationImplementationOwner]
					if !reflect.DeepEqual(bindings, map[string]any{"Obsolete": map[string]any{"$remove": true}, "Preserve": map[string]any{"$remove": true}}) {
						t.Fatal("binding cleanup copied lower intent, retained a local leaf or lost an exclusion")
					}
				} else if _, exists := config[configurationImplementationOwner]; exists {
					t.Fatal("lost owner's Config survived removal")
				}
				for _, action := range []string{"set", "remove"} {
					var value json.RawMessage
					if action == "set" {
						value = json.RawMessage(`"PRIVATE_DISAPPEARING"`)
					}
					runConfigurationPlan(t, root, configurationBatchRequest(t, request, configurationPlanRequest(t, configurationImplementationOwner, "value", action, value)), false, args...)
				}
				if mode == "environment" {
					lower, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
					if err != nil {
						t.Fatal(err)
					}
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(string(lower), "endpoint: PRIVATE_PRIMARY_REQUIRED", "endpoint: [PRIVATE_INVALID_LOWER]", 1))
					runConfigurationPlan(t, root, request, false, args...)
				}
			})
		}
	}
}

func TestRunChangePlanRootConfigurationActivatesTransitiveOwners(t *testing.T) {
	root, document := writeAutomaticRootConfigurationProject(t)
	writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
 "context"
 email "example.com/acme/implementation-use/interfaces/email/send/v1"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Config struct { Endpoint string `+"`plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements reports.read/v1
func New(cfg Config,sender email.Interface)(*Service,error){panic("must not execute")}
func (*Service) Read(context.Context,contract.Request)(contract.Response,error){return contract.Response{},nil}
`)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	document = document[:strings.Index(document, "\nconfig:")] + "\nconfig: {}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	activation := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("reports.read/v1"))
	owner := "example.com/acme/implementation-use/reports.New"
	request := configurationBatchRequest(t, activation, configurationPlanRequest(t, owner, "endpoint", "set", json.RawMessage(`"PRIVATE_REPORTS"`)))
	runConfigurationPlan(t, root, request, false)
	request = configurationBatchRequest(t, request, configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_EMAIL"`)))
	result := runConfigurationPlan(t, root, request, true)
	for _, requiredOwner := range []string{owner, configurationImplementationOwner} {
		if !bytes.Contains(result.Payload.ConfigurationImpact["newly_required_fields"], []byte(requiredOwner)) {
			t.Fatal("transitive newly required owner was omitted")
		}
	}
}

func TestRunChangePlanRootConfigurationRejectsSurvivingInvalidObligations(t *testing.T) {
	for _, failure := range []string{"ambiguous activation", "remaining required value", "dangling binding", "invalid retained Config"} {
		t.Run(failure, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			switch failure {
			case "ambiguous activation":
				document = strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {}", 1)
			case "remaining required value":
				document = strings.Replace(document, "endpoint: PRIVATE_RETAINED, ", "", 1)
			case "dangling binding":
				document = strings.Replace(document, "resources:\n", "resources:\n  bind: {instances: {database.primary: {Unknown: database.missing}}}\n", 1)
			case "invalid retained Config":
				document = strings.Replace(document, "value: PRIVATE_RETAINED_VALUE", "value: [PRIVATE_INVALID]", 1)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			request := configurationBatchRequest(t, rootConfigurationRequest(t, "expose", "present"), configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
			runConfigurationPlan(t, root, request, false)
		})
	}
}

func TestRunChangePlanRootConfigurationRetainsOtherRootOrDormantOwner(t *testing.T) {
	for _, reason := range []string{"exposure", "dormant", "dependency"} {
		root, document := writeAutomaticRootConfigurationProject(t)
		switch reason {
		case "exposure":
			document += "http: {expose: {email.send/v1: {transport: connect}}}\n"
		case "dormant":
			document = strings.Replace(document, "use: {}", "use: {email.send/v1: "+configurationImplementationOwner+"}", 1)
		case "dependency":
			writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
 "context"
 email "example.com/acme/implementation-use/interfaces/email/send/v1"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Service struct{}
//plystra:implements reports.read/v1
func New(sender email.Interface)(*Service,error){return &Service{},nil}
func (*Service) Read(context.Context,contract.Request)(contract.Response,error){return contract.Response{},nil}
`)
			document = strings.Replace(document, "require: [email.send/v1]", "require: [email.send/v1, reports.read/v1]", 1)
		}
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
		request := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "absent"), configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
		result := runConfigurationPlan(t, root, request, true)
		if string(result.Payload.ConfigurationImpact["newly_unowned_paths"]) != "[]" || !bytes.Contains(result.Payload.ConfigurationImpact["retained_owners"], []byte(configurationImplementationOwner)) {
			t.Fatalf("%s failed to retain final ownership", reason)
		}
	}
}

func TestRunChangePlanRootConfigurationPreservesPrivateIdentityAndSatisfiedRoots(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	activation := rootConfigurationRequest(t, "require", "present")
	for _, field := range []string{"value", "password"} {
		var first configurationChangePlanResult
		values := []string{"PRIVATE_PRIMARY", "PRIVATE_DIFFERENT"}
		if field == "password" {
			values[0] = "PRIVATE_SECRET"
		}
		for index, private := range values {
			value := json.RawMessage(fmt.Sprintf("%q", private))
			if field == "password" {
				value = json.RawMessage(fmt.Sprintf(`{"env":%q}`, private))
			}
			request := configurationBatchRequest(t, activation, configurationPlanRequest(t, "database.primary", field, "set", value))
			result := runConfigurationPlan(t, root, request, true, "--env", "production")
			plan := previewRootConfiguration(t, root, request, "--env", "production")
			if bytes.Contains(plan.Updated(), []byte("interfaces:")) || !plan.Changed() {
				t.Fatal("inherited present root materialized or set lost private-safe write intent")
			}
			if index == 0 {
				first = result
			} else if !reflect.DeepEqual(first.Payload, result.Payload) {
				t.Fatal("private value or Secret-reference equality changed public identity")
			}
		}
	}
	first := runConfigurationPlan(t, root, configurationBatchRequest(t, activation, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_A"`))), true)
	second := runConfigurationPlan(t, root, configurationBatchRequest(t, activation, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_B"`))), true)
	if first.Payload.PlanDigest == second.Payload.PlanDigest || first.Payload.RequestDigest != second.Payload.RequestDigest {
		t.Fatal("build-visible final identity is incorrect")
	}
	request := configurationBatchRequest(t, activation, configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_PRIMARY"`)))
	plan := previewRootConfiguration(t, root, request)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(plan.Updated()))
	plan = previewRootConfiguration(t, root, request)
	if !bytes.Equal(plan.Original(), plan.Updated()) || !plan.Changed() {
		t.Fatal("equal-byte configuration set disclosed equality")
	}
	request = configurationBatchRequest(t, activation, configurationPlanRequest(t, "database.primary", "unused", "remove", nil))
	plan = previewRootConfiguration(t, root, request, "--env", "production")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), string(plan.Updated()))
	result := runConfigurationPlan(t, root, request, true, "--env", "production")
	if len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
		t.Fatal("satisfied compound removal advertised a write")
	}
	data, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	delete(document["resources"].(map[string]any)["instances"].(map[string]any)["database.primary"].(map[string]any)["config"].(map[string]any), "endpoint")
	data, err = yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(data))
	runConfigurationPlan(t, root, request, false, "--env", "production")
}

func TestRunChangePlanRootConfigurationUsesFinalDocumentBounds(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	padding := applicationmeta.MaximumSize - len(document) + len("PRIVATE_IMPLEMENTATION") - 8
	document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION,", "value: "+strings.Repeat("x", padding)+",", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	request := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "present"), configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_SMALL"`)))
	runConfigurationPlan(t, root, request, true)
}

func TestRunChangePlanRootConfigurationAcceptsMaximumRequestWithCleanup(t *testing.T) {
	root, document := writeAutomaticRootConfigurationProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	path := filepath.Join(root, "original/provider.go")
	provider, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields strings.Builder
	requests := [][]byte{rootConfigurationRequest(t, "require", "absent")}
	for index := range changerequest.MaximumOperations - 1 {
		name := fmt.Sprintf("root_%04d", index)
		fmt.Fprintf(&fields, "Root%04d string `yaml:%q`; ", index, name)
		requests = append(requests, configurationPlanRequest(t, "database.primary", name, "remove", nil))
	}
	writeCommandFile(t, path, strings.Replace(string(provider), "type Config struct {", "type Config struct { "+fields.String(), 1))
	result := runConfigurationPlan(t, root, configurationBatchRequest(t, requests...), true)
	requested, derived := 0, 0
	for _, operation := range result.Payload.Operations {
		if operation.Derived {
			derived++
		} else {
			requested++
		}
	}
	if requested != changerequest.MaximumOperations || derived == 0 {
		t.Fatal("root cleanup consumed the independent request bound")
	}
}

func TestBuiltCLIChangePlanRootConfigurationFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	root, _ := writeConfigurationPlanProject(t)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(configurationBatchRequest(t, rootConfigurationRequest(t, "expose", "present"), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`))))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || bytes.Contains(output, []byte("PRIVATE_")) {
		t.Fatalf("compiled root preview: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled root preview changed Project")
	}
}

func writeAutomaticRootConfigurationProject(t *testing.T) (string, string) {
	t.Helper()
	root, document := writeConfigurationPlanProject(t)
	if err := os.Remove(filepath.Join(root, "local/implementation.go")); err != nil {
		t.Fatal(err)
	}
	return root, strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {}", 1)
}

func previewRootConfiguration(t *testing.T, root string, data []byte, selector ...string) implementationselect.PlanResult {
	t.Helper()
	request, err := changerequest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	options := implementationselect.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}
	if len(selector) == 2 {
		if selector[0] == "--env" {
			options.EnvironmentName = selector[1]
		} else {
			options.ConfigurationPath = selector[1]
		}
	}
	plan, err := implementationselect.PreviewRootConfigurations(t.Context(), options, request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func rootConfigurationRequest(t testing.TB, kind, action string) []byte {
	t.Helper()
	return []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":"` + action + `","root":"` + kind + `","interface":"email.send/v1"}]}`)
}
