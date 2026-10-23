package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const bindingRepairProvider = "example.com/acme/implementation-use/repair.New"

func TestRunChangePlanResourceBindingRepairsInvalidPreviousState(t *testing.T) {
	for _, previous := range []string{"invalid-config", "invalid-binding", "unknown-provider", "valid"} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			for _, required := range []bool{false, true} {
				name := previous + "/" + mode + "/binding-only"
				if required {
					name = previous + "/" + mode + "/required-config"
				}
				t.Run(name, func(t *testing.T) {
					root, selected, selector := writeBindingRepairProject(t, previous, mode, required)
					request := resourceConfigurationSelectionRequest(t, "database.primary", "set", bindingRepairProvider)
					result := runConfigurationPlan(t, root, request, true, selector...)
					var decisions []struct {
						Target, Reason string
						Alternatives   []string
						Sources        []string
					}
					if err := json.Unmarshal(result.Payload.ConfigurationImpact["unresolved_decisions"], &decisions); err != nil {
						t.Fatal(err)
					}
					wantCount := 1
					if required {
						wantCount = 3
					}
					if len(decisions) != wantCount || len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != selected {
						t.Fatalf("repair decisions or write scope: %#v", result.Payload)
					}
					for _, decision := range decisions {
						want := []string{"database.retained"}
						if decision.Reason == "required-configuration-value-missing" {
							want = []string{"provide-required-field"}
							if previous == "valid" {
								want = append(want, "retain-previous-provider")
							}
						} else if decision.Reason != "ambiguous-resource-binding" || decision.Target != `resources.bind.instances["database.primary"]["replica"]` {
							t.Fatalf("unexpected repair decision: %#v", decision)
						}
						if !reflect.DeepEqual(decision.Alternatives, want) || !slices.Contains(decision.Sources, selected) ||
							!slices.ContainsFunc(decision.Sources, func(source string) bool { return strings.Contains(source, "/repair/provider.go:") }) {
							t.Fatalf("unverified alternative or missing provenance: %#v", decision)
						}
					}
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
					wantFields := 0
					if required {
						wantFields = 2
					}
					if len(fields) != wantFields {
						t.Fatalf("required repair fields: %#v", fields)
					}
					for _, field := range fields {
						if field.Owner != "database.primary" || field.DefaultSatisfies || field.RequirednessReason != "required-field-on-newly-selected-resource-provider" || !strings.Contains(field.SchemaSource, "/repair/provider.go:") ||
							field.Path != `resources.instances["database.primary"].config["name"]` && field.Path != `resources.instances["database.primary"].config["settings"]["endpoint"]` {
							t.Fatalf("incorrect required-field evidence: %#v", field)
						}
					}
					if previous == "invalid-binding" && !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte(`obsolete`)) {
						t.Fatal("repair omitted proven obsolete binding cleanup")
					}
					second := runConfigurationPlan(t, root, request, true, selector...)
					if !reflect.DeepEqual(result.Payload, second.Payload) {
						t.Fatal("repeated repair preview changed public identity")
					}
					before := commandTree(t, root)
					exit, _, _ := runCommand(t, append([]string{"use", "database.primary", bindingRepairProvider}, selector...), root, implementationSelectionCommandEnvironment(nil))
					if exit == 0 || !reflect.DeepEqual(before, commandTree(t, root)) {
						t.Fatal("mutating use installed an unresolved repair")
					}
				})
			}
		}
	}
}

func TestRunChangePlanResourceBindingRepairRejectsInvalidFinalState(t *testing.T) {
	for _, failure := range []string{"retained-config", "retained-binding", "unknown-old-binding", "lower-config", "cycle"} {
		t.Run(failure, func(t *testing.T) {
			mode := "default"
			if failure == "lower-config" {
				mode = "environment"
			}
			root, selected, selector := writeBindingRepairProject(t, "invalid-config", mode, false)
			path := filepath.Join(root, filepath.FromSlash(selected))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			document := string(data)
			switch failure {
			case "retained-config":
				document = strings.Replace(document, "value: PRIVATE_RETAINED", "value: [PRIVATE_INVALID]", 1)
			case "retained-binding":
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.retained: {bogus: database.primary}\n", 1)
			case "unknown-old-binding":
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {bogus: database.retained}\n", 1)
			case "lower-config":
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			case "cycle":
				writeCommandFile(t, filepath.Join(root, "cyclic/provider.go"), "package cyclic\nimport \"example.com/acme/implementation-use/database\"\ntype Config struct { Value string }\ntype Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New(c Config, primary database.Resource)(*Value,error){panic(\"must not execute\")}\nfunc (*Value) Read(){}\n")
				document = strings.Replace(document, "database.retained:\n      use: example.com/acme/implementation-use/original.New", "database.retained:\n      use: example.com/acme/implementation-use/cyclic.New", 1)
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.retained: {primary: database.primary}\n", 1)
			}
			writeCommandFile(t, path, document)
			runConfigurationPlan(t, root, resourceConfigurationSelectionRequest(t, "database.primary", "set", bindingRepairProvider), false, selector...)
		})
	}
}

func TestRunChangePlanResourceBindingRepairKeepsDiscardedValuesPrivate(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, selected, selector := writeBindingRepairProject(t, "invalid-config", mode, true)
			request := resourceConfigurationSelectionRequest(t, "database.primary", "set", bindingRepairProvider)
			first := runConfigurationPlan(t, root, request, true, selector...)
			path := filepath.Join(root, filepath.FromSlash(selected))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, path, strings.Replace(string(data), "[PRIVATE_INVALID]", "{PRIVATE_DIFFERENT: PRIVATE_OTHER}", 1))
			second := runConfigurationPlan(t, root, request, true, selector...)
			if !reflect.DeepEqual(first.Payload, second.Payload) {
				t.Fatal("discarded private old Config changed the public repair payload")
			}
		})
	}
}

func TestBuiltCLIChangePlanResourceBindingRepair(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, _, selector := writeBindingRepairProject(t, "invalid-config", "environment", true)
	before := commandTree(t, root)
	args := append([]string{"change", "plan", "--request", "-", "--format", "json"}, selector...)
	command := exec.CommandContext(t.Context(), binary, args...)
	command.Dir, command.Env = filepath.Join(root, "repair"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(resourceConfigurationSelectionRequest(t, "database.primary", "set", bindingRepairProvider))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || result.Payload.Completeness != "non_exact" || bytes.Contains(output, []byte("PRIVATE_")) || bytes.Contains(output, []byte("retain-previous-provider")) || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatalf("compiled binding repair preview: %v\n%s\n%s", err, output, &stderr)
	}
	assertNoCommandTransactions(t, root)
}

func writeBindingRepairProject(t *testing.T, previous, mode string, required bool) (string, string, []string) {
	t.Helper()
	root := writeResourceSelectionCommandProject(t)
	parameters, configuration := "replica database.Resource", ""
	if required {
		configuration = "type Settings struct { Endpoint string `yaml:\"endpoint\" plystra:\"required\"` }\ntype Config struct { Name string `yaml:\"name\" plystra:\"required\"`; Settings Settings `yaml:\"settings\"` }\n"
		parameters = "c Config, " + parameters
	}
	writeCommandFile(t, filepath.Join(root, "repair/provider.go"), "package repair\nimport \"example.com/acme/implementation-use/database\"\n"+configuration+"type Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New("+parameters+") (*Value,error){panic(\"must not execute\")}\nfunc (*Value) Read(){}\n")
	data, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	switch previous {
	case "invalid-config":
		document = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
	case "invalid-binding":
		writeCommandFile(t, filepath.Join(root, "broken/provider.go"), "package broken\nimport \"example.com/acme/implementation-use/database\"\ntype Config struct { Value string }\ntype Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New(c Config, obsolete database.Resource)(*Value,error){panic(\"must not execute\")}\nfunc (*Value) Read(){}\n")
		document = strings.Replace(document, "use: example.com/acme/implementation-use/original.New", "use: example.com/acme/implementation-use/broken.New", 1)
		document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {obsolete: database.missing}\n", 1)
	case "unknown-provider":
		document = strings.Replace(document, "use: example.com/acme/implementation-use/original.New", "use: example.com/acme/implementation-use/missing.New", 1)
	case "valid":
	default:
		t.Fatalf("unknown previous state %q", previous)
	}
	selected, selector := "plystra.yaml", []string(nil)
	if mode == "environment" {
		selected, selector = "plystra.production.yaml", []string{"--env", "production"}
	} else if mode == "replacement" {
		selected, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
	}
	writeCommandFile(t, filepath.Join(root, filepath.FromSlash(selected)), document)
	return root, selected, selector
}
