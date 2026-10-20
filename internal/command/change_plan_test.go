package command_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunChangePlanImplementationSelectionIsStructuredAndReadOnly(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [email.send/v1]\n")
	request := `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`
	requestPath := filepath.Join(root, "change.json")
	writeCommandFile(t, requestPath, request)
	environment := implementationSelectionCommandEnvironment(nil)
	before := commandTree(t, root)

	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var first struct {
		Status  string          `json:"status"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &first); err != nil {
		t.Fatalf("decode change plan result: %v\n%s", err, stdout)
	}
	var payload struct {
		Schema              string                     `json:"schema"`
		RequestDigest       string                     `json:"request_digest"`
		PlanDigest          string                     `json:"plan_digest"`
		Completeness        string                     `json:"completeness"`
		FinalModelDigest    string                     `json:"final_model_digest"`
		ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
	}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatalf("decode change-plan payload: %v", err)
	}
	if first.Status != "success" || payload.Schema != "plystra.change-plan/v1" || !strings.HasPrefix(payload.RequestDigest, "sha256:") || !strings.HasPrefix(payload.PlanDigest, "sha256:") || !strings.HasPrefix(payload.FinalModelDigest, "sha256:") || payload.Completeness != "non_exact" {
		t.Fatalf("result identity = status %q payload %#v", first.Status, payload)
	}
	for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
		if string(payload.ConfigurationImpact[name]) == "null" {
			t.Fatalf("configuration_impact.%s is null", name)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("change plan mutated the Project:\nbefore: %#v\nafter: %#v", before, after)
	}

	exitCode, secondStdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repeat change plan = exit %d, stderr %q", exitCode, stderr)
	}
	var second struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(secondStdout), &second); err != nil || string(second.Payload) != string(first.Payload) {
		t.Fatalf("repeat payload = %s, want %s; err=%v", second.Payload, first.Payload, err)
	}
}

func TestRunChangePlanResourceSelectionReportsOwnershipImpact(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("Resource change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status  string `json:"status"`
		Payload struct {
			Schema              string                     `json:"schema"`
			Completeness        string                     `json:"completeness"`
			ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
			Operations          []struct {
				Kind     string `json:"kind"`
				Action   string `json:"action"`
				Instance string `json:"instance"`
				Provider string `json:"provider"`
				Owner    string `json:"owner"`
				Path     string `json:"path"`
				Derived  bool   `json:"derived"`
			} `json:"operations"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Status != "success" ||
		result.Payload.Schema != "plystra.change-plan/v1" || result.Payload.Completeness != "non_exact" {
		t.Fatalf("Resource change plan result = %#v, %v", result, err)
	}
	for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
		if len(result.Payload.ConfigurationImpact[name]) == 0 || string(result.Payload.ConfigurationImpact[name]) == "null" {
			t.Fatalf("configuration_impact.%s = %s", name, result.Payload.ConfigurationImpact[name])
		}
	}
	var retained []struct{ Owner, Path string }
	var unowned []struct{ Owner, Path string }
	var derived []struct{ Kind, Action, Owner, Path string }
	for _, field := range []struct {
		name string
		to   any
	}{{"retained_owners", &retained}, {"newly_unowned_paths", &unowned}, {"derived_operations", &derived}} {
		if err := json.Unmarshal(result.Payload.ConfigurationImpact[field.name], field.to); err != nil {
			t.Fatalf("decode configuration_impact.%s: %v", field.name, err)
		}
	}
	wantPath := `resources.instances["database.primary"].config`
	if len(unowned) != 1 || unowned[0].Owner != "database.primary" || unowned[0].Path != wantPath ||
		len(derived) != 1 || derived[0].Kind != "resource_configuration" || derived[0].Action != "remove" || derived[0].Path != wantPath ||
		len(retained) != 1 || retained[0].Owner != "database.retained" || len(result.Payload.Operations) != 2 {
		t.Fatalf("Resource change impact = retained %#v, unowned %#v, derived %#v, operations %#v", retained, unowned, derived, result.Payload.Operations)
	}
	requested, companion := false, false
	for _, operation := range result.Payload.Operations {
		requested = requested || !operation.Derived && operation.Kind == "resource_selection" && operation.Action == "set" && operation.Instance == "database.primary" && operation.Provider == "example.com/acme/implementation-use/replacement.New"
		companion = companion || operation.Derived && operation.Kind == "resource_configuration" && operation.Action == "remove" && operation.Owner == "database.primary" && operation.Path == wantPath
	}
	if !requested || !companion {
		t.Fatalf("Resource change operations = %#v", result.Payload.Operations)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("Resource change plan modified Project files")
	}
}

func TestRunChangePlanResourceSelectionRepairsInvalidOldConfiguration(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_INVALID") {
		t.Fatalf("repair preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				RetainedOwners    []struct{ Owner string }        `json:"retained_owners"`
				NewlyUnownedPaths []struct{ Owner string }        `json:"newly_unowned_paths"`
				DerivedOperations []struct{ Kind, Action string } `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if len(impact.RetainedOwners) != 1 || impact.RetainedOwners[0].Owner != "database.retained" ||
		len(impact.NewlyUnownedPaths) != 1 || impact.NewlyUnownedPaths[0].Owner != "database.primary" ||
		len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" || impact.DerivedOperations[0].Action != "remove" {
		t.Fatalf("repair impact = %#v", impact)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("repair preview modified Project files")
	}
}

func TestRunChangePlanResourceSelectionPreservesSameProviderConfiguration(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("same-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				RetainedOwners    []struct{ Owner string } `json:"retained_owners"`
				NewlyUnownedPaths []json.RawMessage        `json:"newly_unowned_paths"`
				DerivedOperations []json.RawMessage        `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if len(impact.RetainedOwners) != 2 || impact.RetainedOwners[0].Owner != "database.primary" || impact.RetainedOwners[1].Owner != "database.retained" || len(impact.NewlyUnownedPaths) != 0 || len(impact.DerivedOperations) != 0 {
		t.Fatalf("same-provider impact = %#v", impact)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("same-provider preview modified Project files")
	}
}

func TestRunChangePlanDoesNotPublishPrivateResourceValueEquality(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "original", "provider.go"), `package original
import "github.com/plystra/kernel/configuration"
type Config struct {
	Value string `+"`yaml:\"value\"`"+`
	Password configuration.Secret `+"`yaml:\"password\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "config: {value: PRIVATE_PRIMARY}", "config: {value: PRIVATE_PRIMARY, password: {env: PRIVATE_TARGET_A}}", 1))
	preview := func() json.RawMessage {
		t.Helper()
		before := commandTree(t, root)
		exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
		if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
			t.Fatalf("private-value preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatal("private-value preview modified Project files")
		}
		var result struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		return result.Payload
	}
	first := preview()
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "PRIVATE_PRIMARY", "PRIVATE_CHANGED", 1))
	second := preview()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("private-only edit changed public plan:\nfirst %s\nsecond %s", first, second)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "PRIVATE_TARGET_A", "PRIVATE_TARGET_B", 1))
	third := preview()
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("Secret target edit changed public plan:\nfirst %s\nthird %s", first, third)
	}
}

func TestRunChangePlanResourceSelectionOverlayKeepsLowerLayerUnchanged(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--env", "production", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("overlay preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationScope struct {
				Path string `json:"path"`
			} `json:"configuration_scope"`
			ConfigurationImpact struct {
				NewlyUnownedPaths []struct{ Owner, Source string } `json:"newly_unowned_paths"`
				DerivedOperations []struct{ Kind string }          `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if result.Payload.ConfigurationScope.Path != "plystra.production.yaml" || len(impact.NewlyUnownedPaths) != 2 ||
		impact.NewlyUnownedPaths[0].Owner != "database.primary" || impact.NewlyUnownedPaths[1].Owner != "database.primary" ||
		impact.NewlyUnownedPaths[0].Source == impact.NewlyUnownedPaths[1].Source ||
		len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" {
		t.Fatalf("overlay impact = %#v", result.Payload)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("overlay preview modified Project files")
	}
}

func TestRunChangePlanResourceSelectionOverlayProviderReversion(t *testing.T) {
	for _, value := range []string{"PRIVATE_REPLACEMENT", "[PRIVATE_INVALID]"} {
		t.Run(value, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources:\n  instances:\n    database.primary:\n      use: example.com/acme/implementation-use/replacement.New\n      config: {value: "+value+"}\n")
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--env", "production", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, value) {
				t.Fatalf("provider reversion preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationImpact struct {
						RetainedOwners    []struct{ Owner string }         `json:"retained_owners"`
						NewlyUnownedPaths []struct{ Owner, Source string } `json:"newly_unowned_paths"`
						DerivedOperations []struct{ Kind, Source string }  `json:"derived_operations"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			impact := result.Payload.ConfigurationImpact
			if len(impact.NewlyUnownedPaths) != 1 || impact.NewlyUnownedPaths[0].Owner != "database.primary" ||
				!strings.Contains(impact.NewlyUnownedPaths[0].Source, "plystra.production.yaml") ||
				len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" ||
				!strings.Contains(impact.DerivedOperations[0].Source, "plystra.production.yaml") ||
				len(impact.RetainedOwners) != 1 || impact.RetainedOwners[0].Owner != "database.retained" {
				t.Fatalf("provider reversion impact = %#v", impact)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("provider reversion preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceSelectionReportsEveryMissingRequiredField(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("missing Resource Config preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			Completeness        string `json:"completeness"`
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Owner              string `json:"owner"`
					Path               string `json:"path"`
					SchemaSource       string `json:"schema_source"`
					RequirednessReason string `json:"requiredness_reason"`
					DefaultSatisfies   bool   `json:"default_satisfies"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target       string   `json:"target"`
					Reason       string   `json:"reason"`
					Alternatives []string `json:"alternatives"`
					Sources      []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	wantPaths := []string{`resources.instances["database.primary"].config["name"]`, `resources.instances["database.primary"].config["settings"]["endpoint"]`}
	if result.Payload.Completeness != "non_exact" || len(impact.NewlyRequiredFields) != len(wantPaths) || len(impact.UnresolvedDecisions) != len(wantPaths) {
		t.Fatalf("missing Resource fields = %#v", impact)
	}
	for index, path := range wantPaths {
		field, decision := impact.NewlyRequiredFields[index], impact.UnresolvedDecisions[index]
		if field.Owner != "database.primary" || field.Path != path || !strings.Contains(field.SchemaSource, "/required/provider.go:") ||
			field.RequirednessReason != "required-field-on-newly-selected-resource-provider" || field.DefaultSatisfies || decision.Target != path ||
			decision.Reason != "required-configuration-value-missing" || len(decision.Alternatives) != 2 || len(decision.Sources) != 2 ||
			decision.Sources[0] != "plystra.yaml" && decision.Sources[1] != "plystra.yaml" {
			t.Fatalf("missing Resource field %d = %#v, decision %#v", index, field, decision)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("missing Resource Config preview modified Project files")
	}
	exitCode, _, _ = runCommand(t, []string{"use", "database.primary", "example.com/acme/implementation-use/required.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted missing required Resource Config")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed Resource use modified Project files")
	}
}

func TestRunChangePlanResourceSelectionFirstProviderRequiresValues(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, string(data)+"    database.pending: {}\n")
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.pending","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("first-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				UnresolvedDecisions []struct {
					Alternatives []string `json:"alternatives"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
		t.Fatalf("first-provider decisions = %#v, %v", result, err)
	}
	for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
		if len(decision.Alternatives) != 1 || decision.Alternatives[0] != "provide-required-field" {
			t.Fatalf("first-provider alternatives = %#v", decision.Alternatives)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("first-provider preview modified Project files")
	}
}

func TestRunChangePlanResourceRequiredFieldsUseSelectedDocument(t *testing.T) {
	for _, test := range []struct {
		name, path string
		selector   []string
	}{
		{name: "environment", path: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", path: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeRequiredResourceCommandProvider(t, root)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
			before := commandTree(t, root)
			arguments := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("selected required-field preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationScope struct {
						Path string `json:"path"`
					} `json:"configuration_scope"`
					ConfigurationImpact struct {
						UnresolvedDecisions []struct {
							Sources []string `json:"sources"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if result.Payload.ConfigurationScope.Path != test.path || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
				t.Fatalf("selected required-field scope and decisions = %#v", result.Payload)
			}
			for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
				if len(decision.Sources) != 2 || decision.Sources[0] != test.path && decision.Sources[1] != test.path {
					t.Fatalf("selected required-field sources = %#v", decision.Sources)
				}
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("selected required-field preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRequiredFieldsDoNotHideOtherBlockers(t *testing.T) {
	for _, test := range []struct {
		name, provider string
		prepare        func(*testing.T, string)
	}{
		{name: "another incomplete instance", provider: "required", prepare: func(t *testing.T, root string) {
			writeRequiredResourceCommandProvider(t, root)
			path := filepath.Join(root, "plystra.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old := "database.retained:\n      use: example.com/acme/implementation-use/original.New\n      config: {value: PRIVATE_RETAINED}"
			newValue := "database.retained:\n      use: example.com/acme/implementation-use/required.New"
			writeCommandFile(t, path, strings.Replace(string(data), old, newValue, 1))
		}},
		{name: "implicit fixed-array child", provider: "arrayrequired", prepare: func(t *testing.T, root string) {
			writeCommandFile(t, filepath.Join(root, "arrayrequired", "provider.go"), `package arrayrequired
type Item struct { Code string `+"`yaml:\"code\" plystra:\"required\"`"+` }
type Config struct {
	Name string `+"`yaml:\"name\" plystra:\"required\"`"+`
	Items [1]Item `+"`yaml:\"items\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			test.prepare(t, root)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/`+test.provider+`.New"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 3 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("incomplete candidate preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Status  string          `json:"status"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Status != "validation_failed" || string(result.Payload) != "null" {
				t.Fatalf("incomplete candidate result = %#v, %v", result, err)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("incomplete candidate preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRequiredFieldsRejectInvalidPreviousProviderAlternative(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("invalid-previous-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				UnresolvedDecisions []struct {
					Alternatives []string `json:"alternatives"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
		t.Fatalf("invalid-previous-provider decisions = %#v, %v", result, err)
	}
	for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
		if len(decision.Alternatives) != 1 || decision.Alternatives[0] != "provide-required-field" {
			t.Fatalf("invalid-previous-provider alternatives = %#v", decision.Alternatives)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("invalid-previous-provider preview modified Project files")
	}
}

func writeRequiredResourceCommandProvider(t *testing.T, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "required", "provider.go"), `package required
type Settings struct { Endpoint string `+"`yaml:\"endpoint\" plystra:\"required\"`"+` }
type Config struct {
	Name string `+"`yaml:\"name\" plystra:\"required\"`"+`
	Settings Settings `+"`yaml:\"settings\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
}

func TestRunChangePlanRejectsUninstalledOperationWithoutMutation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	requestPath := filepath.Join(root, "change.json")
	writeCommandFile(t, requestPath, `{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"example.com/other@v1.0.0"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 2 || stderr != "" {
		t.Fatalf("unsupported change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status      string `json:"status"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode unsupported result: %v\n%s", err, stdout)
	}
	if result.Status != "unsupported" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_CHANGE_PLAN_OPERATION_UNSUPPORTED" {
		t.Fatalf("unsupported result = %#v", result)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unsupported change plan mutated the Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestRunChangePlanReportsNewlyRequiredConstructorField(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/local.New:
    endpoint: PRIVATE_ENDPOINT
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Owner              string `json:"owner"`
					Path               string `json:"path"`
					SchemaSource       string `json:"schema_source"`
					RequirednessReason string `json:"requiredness_reason"`
					DefaultSatisfies   bool   `json:"default_satisfies"`
				} `json:"newly_required_fields"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode change plan: %v\n%s", err, stdout)
	}
	fields := result.Payload.ConfigurationImpact.NewlyRequiredFields
	if len(fields) != 1 || fields[0].Owner != "example.com/acme/implementation-use/local.New" || fields[0].Path != `config["example.com/acme/implementation-use/local.New"]["endpoint"]` || !strings.HasPrefix(fields[0].SchemaSource, "example.com/acme/implementation-use@local/local/implementation.go:") || fields[0].RequirednessReason != "required-field-on-newly-reachable-constructor" || fields[0].DefaultSatisfies {
		t.Fatalf("newly required fields = %#v", fields)
	}
	if strings.Contains(stdout, "PRIVATE_ENDPOINT") {
		t.Fatal("change plan disclosed a private configuration value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}
}

func TestRunChangePlanReportsMissingRequiredConstructorValueWithoutMutation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status  string `json:"status"`
		Payload struct {
			Completeness        string `json:"completeness"`
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Path string `json:"path"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target       string   `json:"target"`
					Reason       string   `json:"reason"`
					Alternatives []string `json:"alternatives"`
					Sources      []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode change plan: %v\n%s", err, stdout)
	}
	impact := result.Payload.ConfigurationImpact
	wantPath := `config["example.com/acme/implementation-use/local.New"]["endpoint"]`
	if result.Status != "success" || result.Payload.Completeness != "non_exact" || len(impact.NewlyRequiredFields) != 1 || impact.NewlyRequiredFields[0].Path != wantPath || len(impact.UnresolvedDecisions) != 1 || impact.UnresolvedDecisions[0].Target != wantPath || impact.UnresolvedDecisions[0].Reason != "required-configuration-value-missing" || len(impact.UnresolvedDecisions[0].Alternatives) == 0 || len(impact.UnresolvedDecisions[0].Sources) == 0 {
		t.Fatalf("missing-value impact = %#v", result)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}

	exitCode, _, _ = runCommand(t, []string{"use", "email.send/v1", "example.com/acme/implementation-use/local.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted a missing required configuration value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed use changed Project files")
	}
}

func TestRunChangePlanReportsEveryMissingRequiredFixedField(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	implementationPath := filepath.Join(root, "local", "implementation.go")
	source, err := os.ReadFile(implementationPath)
	if err != nil {
		t.Fatal(err)
	}
	oldField := "Endpoint string `plystra:\"required\"`"
	newFields := "Endpoint string `plystra:\"required\"`; Database struct { Host string `plystra:\"required\"` }; Private string"
	if !strings.Contains(string(source), oldField) {
		t.Fatal("configurable fixture lacks the expected field")
	}
	writeCommandFile(t, implementationPath, strings.Replace(string(source), oldField, newFields, 1))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/local.New:
    private: PRIVATE_VALUE
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Path string `json:"path"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target  string   `json:"target"`
					Sources []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	want := []string{
		`config["example.com/acme/implementation-use/local.New"]["database"]["host"]`,
		`config["example.com/acme/implementation-use/local.New"]["endpoint"]`,
	}
	var fields, decisions []string
	for _, field := range impact.NewlyRequiredFields {
		fields = append(fields, field.Path)
	}
	for _, decision := range impact.UnresolvedDecisions {
		decisions = append(decisions, decision.Target)
		if len(decision.Sources) == 0 {
			t.Fatalf("missing decision provenance: %#v", decision)
		}
	}
	if !reflect.DeepEqual(fields, want) || !reflect.DeepEqual(decisions, want) {
		t.Fatalf("incomplete required-field impact = %#v", impact)
	}
	if strings.Contains(stdout, "PRIVATE_VALUE") {
		t.Fatal("change plan disclosed a private value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}
	exitCode, _, _ = runCommand(t, []string{"use", "email.send/v1", "example.com/acme/implementation-use/local.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted missing required values")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed use changed Project files")
	}
}

func TestRunChangePlanReportsRequiredFixedObjectChild(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	implementationPath := filepath.Join(root, "local", "implementation.go")
	source, err := os.ReadFile(implementationPath)
	if err != nil {
		t.Fatal(err)
	}
	oldField := "Endpoint string `plystra:\"required\"`"
	newField := "Database struct { Host string `plystra:\"required\"` }"
	if !strings.Contains(string(source), oldField) {
		t.Fatal("configurable fixture lacks the expected field")
	}
	writeCommandFile(t, implementationPath, strings.Replace(string(source), oldField, newField, 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	wantPath := `config["example.com/acme/implementation-use/local.New"]["database"]["host"]`
	for _, test := range []struct {
		name, config string
		unresolved   bool
	}{
		{"provided", "config:\n  example.com/acme/implementation-use/local.New:\n    database: {host: PRIVATE_HOST}\n", false},
		{"missing", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [email.send/v1]\n  use:\n    email.send/v1: example.com/acme/implementation-use/smtp.New\n"+test.config)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" {
				t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationImpact struct {
						NewlyRequiredFields []struct {
							Path string `json:"path"`
						} `json:"newly_required_fields"`
						UnresolvedDecisions []struct {
							Target string `json:"target"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			impact := result.Payload.ConfigurationImpact
			if len(impact.NewlyRequiredFields) != 1 || impact.NewlyRequiredFields[0].Path != wantPath || (len(impact.UnresolvedDecisions) == 1) != test.unresolved {
				t.Fatalf("fixed-object impact = %#v", impact)
			}
			if test.unresolved && impact.UnresolvedDecisions[0].Target != wantPath {
				t.Fatalf("unresolved target = %#v", impact.UnresolvedDecisions)
			}
			if strings.Contains(stdout, "PRIVATE_HOST") {
				t.Fatal("change plan disclosed a private value")
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("change plan changed Project files")
			}
		})
	}
}
