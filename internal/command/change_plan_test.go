package command_test

import (
	"encoding/json"
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
