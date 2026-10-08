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
