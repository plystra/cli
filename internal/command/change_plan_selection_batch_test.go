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
	"go.yaml.in/yaml/v3"
)

func TestRunChangePlanSelectionBatchUsesJointFinalOwners(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
			path, args := "plystra.yaml", []string(nil)
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
			} else if mode == "replacement" {
				path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			request := configurationBatchRequest(t,
				rootConfigurationRequest(t, "require", "present"),
				implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
				resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider),
				resourceConfigurationSelectionRequest(t, "database.retained", "set", compoundResourceProvider),
				configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_IMPLEMENTATION"`)),
				configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_PRIMARY"`)),
				configurationPlanRequest(t, "database.retained", "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
			first := runConfigurationPlan(t, root, request, true, args...)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
			if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != path || string(first.Payload.ConfigurationImpact["unresolved_decisions"]) != "[]" {
				t.Fatal("mixed batch lost final-state ownership, order, or selected scope")
			}
		})
	}
}

func TestRunChangePlanSelectionBatchRemovesOrReplacesResourceConsumers(t *testing.T) {
	for _, scenario := range []string{"remove-both", "replace-consumer", "surviving-consumer"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(scenario+"/"+mode, func(t *testing.T) {
				root, document := writeConfigurationPlanProject(t)
				path := filepath.Join(root, "original/provider.go")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				source := strings.Replace(string(data), "import ", "import database \"example.com/acme/implementation-use/database\"\nimport ", 1)
				writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(c Config, Obsolete database.Resource)", 1))
				document = strings.Replace(document, "resources:\n", "resources:\n  bind: {instances: {database.primary: {Obsolete: database.retained}, database.retained: {Obsolete: database.primary}}}\n", 1)
				selected, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, selected), document)
					selected, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
				} else if mode == "replacement" {
					selected, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, selected), document)
				requests := [][]byte{resourceConfigurationSelectionRequest(t, "database.retained", "remove", "")}
				switch scenario {
				case "remove-both":
					requests = append(requests, resourceConfigurationSelectionRequest(t, "database.primary", "remove", ""))
				case "replace-consumer":
					requests = append(requests, resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
				case "surviving-consumer":
					requests = append(requests, rootConfigurationRequest(t, "require", "present"))
				}
				request := configurationBatchRequest(t, requests...)
				result := runConfigurationPlan(t, root, request, scenario != "surviving-consumer", args...)
				if scenario == "surviving-consumer" {
					return
				}
				plan := previewResourceConfiguration(t, root, request, args...)
				var edited map[string]any
				if err := yaml.Unmarshal(plan.Updated(), &edited); err != nil {
					t.Fatal(err)
				}
				bindings := edited["resources"].(map[string]any)["bind"].(map[string]any)["instances"].(map[string]any)
				for _, name := range []string{"database.primary", "database.retained"} {
					parameters := bindings[name].(map[string]any)
					_, exists := parameters["Obsolete"]
					if mode == "environment" {
						if !reflect.DeepEqual(parameters["Obsolete"], map[string]any{"$remove": true}) {
							t.Fatal("inherited outgoing binding lost its exact exclusion")
						}
					} else if exists {
						t.Fatal("outgoing binding was not removed")
					}
				}
				count := 0
				for _, operation := range result.Payload.Operations {
					if operation.Derived && operation.Kind == "resource_binding" {
						count++
					}
				}
				if count != 2 {
					t.Fatalf("joint cleanup repeated or omitted binding leaves: %d", count)
				}
			})
		}
	}
}

func TestRunChangePlanSelectionBatchResourceRemovalUsesFinalImplementationOwners(t *testing.T) {
	for _, retain := range []bool{false, true} {
		t.Run(fmt.Sprint(retain), func(t *testing.T) {
			root, document := writeSharedInterfaceBatchProject(t)
			path := filepath.Join(root, "smtp/implementation.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(string(data), "import (", "import (\n database \"example.com/acme/implementation-use/database\"", 1)
			writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(c Config, Primary database.Resource)", 1))
			document = strings.Replace(document, "resources:\n", "resources:\n  bind: {implementations: {"+configurationImplementationOwner+": {Primary: database.primary}}}\n", 1)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			requests := [][]byte{rootConfigurationRequest(t, "require", "absent"), implementationConfigurationSelectionRequest(t, "remove", ""), resourceConfigurationSelectionRequest(t, "database.primary", "remove", "")}
			if retain {
				requests = append(requests, bytes.ReplaceAll(implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner), []byte("email.send/v1"), []byte("email.copy/v1")))
			}
			request := configurationBatchRequest(t, requests...)
			result := runConfigurationPlan(t, root, request, !retain)
			if !retain {
				if !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte("Primary")) || !bytes.Contains(result.Payload.ConfigurationImpact["newly_unowned_paths"], []byte(configurationImplementationOwner)) {
					t.Fatal("joint final-owner loss omitted constructor Config or binding cleanup")
				}
			}
		})
	}
}

func TestRunChangePlanSelectionBatchFirstAssignmentCleansLostAutomaticOwner(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	if err := os.Remove(filepath.Join(root, "local/implementation.go")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "smtp/implementation.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "import (", "import (\n database \"example.com/acme/implementation-use/database\"", 1)
	writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(c Config, Primary database.Resource)", 1))
	document = strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {}", 1)
	document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
	document = strings.Replace(document, "resources:\n", "resources:\n  bind: {implementations: {"+configurationImplementationOwner+": {Primary: database.primary}}}\n", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider), rootConfigurationRequest(t, "require", "absent"))
	result := runConfigurationPlan(t, root, request, true)
	if !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte("Primary")) || !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte(configurationImplementationOwner)) {
		t.Fatal("unbound Resource hid proven automatic constructor ownership")
	}
}

func TestRunChangePlanSelectionBatchFirstAssignmentsRetainOnlySelectedConfig(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			document = strings.ReplaceAll(document, "      use: "+originalCompoundResourceProvider+"\n", "")
			path, args := "plystra.yaml", []string(nil)
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "resources: {instances: {database.primary: {config: {endpoint: PRIVATE_SELECTED}}}}\n"
			} else if mode == "replacement" {
				path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			selections := configurationBatchRequest(t,
				resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
				resourceConfigurationSelectionRequest(t, "database.retained", "set", originalCompoundResourceProvider))
			result := runConfigurationPlan(t, root, selections, mode != "environment", args...)
			if mode == "environment" {
				request := configurationBatchRequest(t, selections, configurationPlanRequest(t, "database.retained", "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
				plan := previewResourceConfiguration(t, root, request, args...)
				if bytes.Contains(plan.Updated(), []byte("PRIVATE_PRIMARY_REQUIRED")) || bytes.Contains(plan.Updated(), []byte("PRIVATE_RETAINED_VALUE")) || !bytes.Contains(plan.Updated(), []byte("PRIVATE_SELECTED")) {
					t.Fatal("first assignment copied lower unbound Config or discarded selected-local Config")
				}
				result = runConfigurationPlan(t, root, request, true, args...)
			}
			var fields []struct{ Owner string }
			if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_required_fields"], &fields); err != nil || len(fields) != 2 || fields[0].Owner != "database.primary" || fields[1].Owner != "database.retained" {
				t.Fatalf("joint first assignments omitted or repeated required provider fields: %v", err)
			}
		})
	}
}

func TestRunChangePlanSelectionBatchReversionAndConfigExclusion(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	overlay := "# KEEP_OVERLAY\nresources: {instances: {database.primary: {use: " + compoundResourceProvider + ", config: {value: PRIVATE_OBSOLETE}}, database.retained: {config: {$remove: true}}}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlay)
	request := configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
		resourceConfigurationSelectionRequest(t, "database.retained", "set", compoundResourceProvider),
		configurationPlanRequest(t, "database.retained", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	plan := previewResourceConfiguration(t, root, request, "--env", "production")
	var edited map[string]any
	if err := yaml.Unmarshal(plan.Updated(), &edited); err != nil {
		t.Fatal(err)
	}
	instances := edited["resources"].(map[string]any)["instances"].(map[string]any)
	if !reflect.DeepEqual(instances["database.primary"], map[string]any{"use": originalCompoundResourceProvider}) || !reflect.DeepEqual(instances["database.retained"], map[string]any{"use": compoundResourceProvider, "config": map[string]any{"value": "PRIVATE_NEW"}}) {
		t.Fatal("joint replacement migrated Config, retained an exclusion, or copied root Config")
	}
	runConfigurationPlan(t, root, request, true, "--env", "production")
}

func TestRunChangePlanSelectionBatchNoOpsAndInvalidFinalStates(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	noOps := configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
		resourceConfigurationSelectionRequest(t, "database.retained", "set", originalCompoundResourceProvider))
	result := runConfigurationPlan(t, root, noOps, true)
	if len(result.Payload.WriteSet) != 0 || len(result.Payload.Effects.Planned) != 0 {
		t.Fatal("satisfied selections invented write intent")
	}
	for _, invalid := range []string{
		strings.Replace(document, "endpoint: PRIVATE_RETAINED,", "", 1),
		strings.Replace(document, "value: PRIVATE_PRIMARY,", "value: [PRIVATE_INVALID],", 1),
		strings.Replace(document, "resources:\n", "resources:\n  bind: {instances: {database.absent: {Orphan: database.primary}}}\n", 1),
	} {
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), invalid)
		runConfigurationPlan(t, root, noOps, false)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	removal := resourceConfigurationSelectionRequest(t, "database.primary", "remove", "")
	for _, action := range []string{"set", "remove"} {
		var value json.RawMessage
		if action == "set" {
			value = json.RawMessage(`"PRIVATE_NEW"`)
		}
		runConfigurationPlan(t, root, configurationBatchRequest(t, removal, rootConfigurationRequest(t, "require", "present"), configurationPlanRequest(t, "database.primary", "value", action, value)), false)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
	request := configurationBatchRequest(t, noOps, rootConfigurationRequest(t, "require", "present"))
	runConfigurationPlan(t, root, request, false, "--env", "production")
}

func TestRunChangePlanSelectionBatchPreservesPrivateIdentity(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	selections := configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider),
		resourceConfigurationSelectionRequest(t, "database.retained", "set", originalCompoundResourceProvider),
		rootConfigurationRequest(t, "require", "present"))
	for _, field := range []struct {
		path   string
		values []json.RawMessage
	}{
		{"value", []json.RawMessage{json.RawMessage(`"PRIVATE_PRIMARY"`), json.RawMessage(`"PRIVATE_NEW"`)}},
		{"password", []json.RawMessage{json.RawMessage(`{"env":"PRIVATE_SECRET"}`), json.RawMessage(`{"env":"PRIVATE_NEW_SECRET"}`)}},
	} {
		first := runConfigurationPlan(t, root, configurationBatchRequest(t, selections, configurationPlanRequest(t, "database.primary", field.path, "set", field.values[0])), true)
		second := runConfigurationPlan(t, root, configurationBatchRequest(t, selections, configurationPlanRequest(t, "database.primary", field.path, "set", field.values[1])), true)
		if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 {
			t.Fatal("mixed batch disclosed private value or Secret-reference equality")
		}
	}
	first := runConfigurationPlan(t, root, configurationBatchRequest(t, selections, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_A"`))), true)
	second := runConfigurationPlan(t, root, configurationBatchRequest(t, selections, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_B"`))), true)
	if first.Payload.PlanDigest == second.Payload.PlanDigest || first.Payload.RequestDigest != second.Payload.RequestDigest {
		t.Fatal("mixed batch lost build-visible final identity")
	}
}

func TestRunChangePlanSelectionBatchCannotInventBindingOwnership(t *testing.T) {
	for _, scenario := range []string{"absent", "excluded", "unknown-provider", "first-assignment"} {
		t.Run(scenario, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			document = strings.Replace(document, "resources:\n", "resources:\n  bind: {instances: {database.primary: {Orphan: database.retained}}}\n", 1)
			args := []string(nil)
			if scenario == "absent" {
				start, end := strings.Index(document, "    database.primary:"), strings.Index(document, "    database.retained:")
				document = document[:start] + document[end:]
			} else if scenario == "excluded" {
				args = []string{"--env", "production"}
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
			} else if scenario == "unknown-provider" {
				document = strings.Replace(document, "use: "+originalCompoundResourceProvider, "use: example.com/acme/implementation-use/missing.New", 1)
			} else {
				document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			action, provider := "set", compoundResourceProvider
			if scenario == "first-assignment" {
				provider = originalCompoundResourceProvider
			}
			if scenario == "absent" || scenario == "excluded" {
				action, provider = "remove", ""
			}
			request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", action, provider), resourceConfigurationSelectionRequest(t, "database.retained", "remove", ""))
			runConfigurationPlan(t, root, request, false, args...)
			if scenario == "unknown-provider" || scenario == "first-assignment" {
				document = strings.Replace(document, "  bind: {instances: {database.primary: {Orphan: database.retained}}}\n", "", 1)
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
				runConfigurationPlan(t, root, request, true, args...)
			}
		})
	}
}

func TestRunChangePlanSelectionBatchFinalBoundsAndMaximumRequests(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	padding := applicationmeta.MaximumSize - len(document) + len("PRIVATE_PRIMARY") - 16
	document = strings.Replace(document, "value: PRIVATE_PRIMARY,", "value: "+strings.Repeat("x", padding)+",", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	requests := [][]byte{
		resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider),
		resourceConfigurationSelectionRequest(t, "database.retained", "set", compoundResourceProvider),
		rootConfigurationRequest(t, "expose", "present"),
		configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)),
	}
	for index := range changerequest.MaximumOperations - len(requests) {
		requests = append(requests, resourceConfigurationSelectionRequest(t, fmt.Sprintf("unused.n%04d", index), "remove", ""))
	}
	request := configurationBatchRequest(t, requests...)
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true)
	requested, derived := 0, 0
	for _, operation := range first.Payload.Operations {
		if operation.Derived {
			derived++
		} else {
			requested++
		}
	}
	if !reflect.DeepEqual(first.Payload, second.Payload) || requested != changerequest.MaximumOperations || derived != 2 || len(first.Payload.WriteSet) != 1 {
		t.Fatal("mixed final-bound preview lost operations, unique cleanup, or request-order independence")
	}
}

func TestBuiltCLIChangePlanSelectionBatchFromStdin(t *testing.T) {
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
	command.Stdin = bytes.NewReader(configurationBatchRequest(t,
		resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider),
		resourceConfigurationSelectionRequest(t, "database.retained", "remove", ""),
		rootConfigurationRequest(t, "expose", "present"),
		implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
		configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`))))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || bytes.Contains(output, []byte("PRIVATE_")) || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatalf("compiled mixed selection preview: %v\n%s\n%s", err, output, &stderr)
	}
}
