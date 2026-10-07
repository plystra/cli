package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestRunDoctorJSONReportsProjectWithoutDataMember(t *testing.T) {
	root := writeDoctorProject(t, "{}\n")
	var stdout, stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"doctor", "--format", "json", "--offline"}, &stdout, &stderr, root, doctorEnvironment()); exitCode != 0 {
		t.Fatalf("doctor exit code = %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor stderr = %q, want empty", stderr.String())
	}
	var result doctorResultDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor result: %v", err)
	}
	if result.Schema != "plystra.result/v1" || result.Operation != "doctor" || result.Status != "success" || result.ExitClass != 0 {
		t.Fatalf("doctor result envelope = %#v", result)
	}
	var payload doctorPayloadDocument
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("decode doctor payload: %v", err)
	}
	if payload.Schema != "plystra.doctor/v1" || !payload.Offline || string(payload.DataCompiler) != "null" {
		t.Fatalf("doctor payload = %#v", payload)
	}
	if len(payload.Checks) != 4 || payload.Checks[0].Status != "not_applicable" || payload.Checks[1].Status != "not_applicable" {
		t.Fatalf("doctor checks = %#v", payload.Checks)
	}
}

func TestRunDoctorJSONReportsMissingDataCompilerPrerequisite(t *testing.T) {
	root := writeDoctorProject(t, "data:\n  members:\n    example.records/v1:\n      resource: database.primary\n")
	var stdout, stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"doctor", "--format", "json", "--offline"}, &stdout, &stderr, root, doctorEnvironment()); exitCode != 4 {
		t.Fatalf("doctor exit code = %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor stderr = %q, want empty", stderr.String())
	}
	var result doctorResultDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode doctor result: %v", err)
	}
	if result.Status != "prerequisite_missing" || result.ExitClass != 4 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_DOCTOR_PREREQUISITE_MISSING" {
		t.Fatalf("doctor missing prerequisite result = %#v", result)
	}
	var payload doctorPayloadDocument
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatalf("decode doctor payload: %v", err)
	}
	if payload.Schema != "plystra.doctor/v1" || len(payload.Checks) != 3 || payload.Checks[0].Status != "no" {
		t.Fatalf("doctor missing prerequisite payload = %#v", payload)
	}
}

func TestRunDoctorRejectsConflictingConfigurationSelectors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := command.Run([]string{"doctor", "--env", "test", "--config", "other.yaml", "--format", "json"}, &stdout, &stderr); exitCode != 3 {
		t.Fatalf("doctor conflicting selectors exit code = %d", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor conflicting selectors stderr = %q, want empty", stderr.String())
	}
	var result doctorResultDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode conflicting selector result: %v", err)
	}
	if result.Status != "validation_failed" || result.ExitClass != 3 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_DOCTOR_FAILED" {
		t.Fatalf("doctor conflicting selector result = %#v", result)
	}
}

func writeDoctorProject(t *testing.T, configuration string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"go.mod":       "module example.com/doctor\n\ngo 1.26\n",
		"main.go":      "package main\n\nfunc main() {}\n",
		"plystra.yaml": configuration,
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return root
}

func doctorEnvironment() []string {
	result := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "GOWORK=") || strings.HasPrefix(value, "GOPROXY=") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "GOWORK=off", "GOPROXY=off")
}

type doctorResultDocument struct {
	Schema      string `json:"schema"`
	Operation   string `json:"operation"`
	Status      string `json:"status"`
	ExitClass   int    `json:"exit_class"`
	Diagnostics []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Payload json.RawMessage `json:"payload"`
}

type doctorPayloadDocument struct {
	Schema  string `json:"schema"`
	Offline bool   `json:"offline"`
	Checks  []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"checks"`
	DataCompiler json.RawMessage `json:"data_compiler"`
}
