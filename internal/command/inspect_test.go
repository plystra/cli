package command_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const inspectProgress = "Resolving selected application model...\n"

type inspectCommandEnvelope struct {
	Schema                 string `json:"schema"`
	SchemaVersion          int    `json:"schema_version"`
	ConfigurationMode      string `json:"configuration_mode"`
	ApplicationModelDigest string `json:"application_model_digest"`
	Result                 struct {
		Project struct {
			Module string `json:"module"`
		} `json:"project"`
		Configuration struct {
			Mode        string `json:"mode"`
			Environment string `json:"environment"`
			Path        string `json:"path"`
		} `json:"configuration"`
		Summary struct {
			SelectedPluginCount      int      `json:"selected_plugin_count"`
			AvailableCapabilityCount int      `json:"available_capability_count"`
			RequiredCapabilityCount  int      `json:"required_capability_count"`
			ExposedCapabilityCount   int      `json:"exposed_capability_count"`
			CapabilityAliasCount     int      `json:"capability_alias_count"`
			AuthNActive              bool     `json:"authn_active"`
			AuthZActive              bool     `json:"authz_active"`
			Transports               []string `json:"transports"`
		} `json:"summary"`
		Readiness struct {
			State        string `json:"state"`
			ProblemCount int    `json:"problem_count"`
			NextAction   string `json:"next_action"`
		} `json:"readiness"`
		ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
	} `json:"result"`
}

func TestInspectHumanOutputIsConciseAndReadOnlyFromNestedDirectory(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect"}, nested, inspectCommandEnvironment(nil))
	want := inspectProgress +
		"Project: example.com/acme/inspect\n" +
		"Configuration: default (plystra.yaml)\n" +
		"Plugins: 0 selected\n" +
		"Capabilities: 2 available, 0 required, 0 exposed, 0 aliases\n" +
		"AuthN: inactive\n" +
		"AuthZ: inactive\n" +
		"Transports: connect\n" +
		"Readiness: ready (0 problems)\n" +
		"Next action: Run plystra check to validate the selected model.\n"
	if exitCode != 0 || stdout != want || stderr != "" {
		t.Fatalf("inspect = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout, stderr, want)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("inspect mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectJSONOwnsStdoutAndIsDeterministic(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"inspect", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"inspect", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress {
		t.Fatalf("JSON inspect = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if firstStdout != secondStdout || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
		t.Fatalf("JSON stdout is not one deterministic document:\nfirst:  %q\nsecond: %q", firstStdout, secondStdout)
	}
	document := decodeInspectCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.inspect" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("inspect envelope identity = %#v", document)
	}
	if document.Result.Project.Module != "example.com/acme/inspect" || document.Result.Configuration.Mode != "default" || document.Result.Configuration.Path != "plystra.yaml" {
		t.Fatalf("inspect result identity = %#v", document.Result)
	}
	if document.Result.Summary.SelectedPluginCount != 0 || document.Result.Summary.AvailableCapabilityCount != 2 || !reflect.DeepEqual(document.Result.Summary.Transports, []string{"connect"}) {
		t.Fatalf("inspect summary = %#v", document.Result.Summary)
	}
	if document.Result.Readiness.State != "ready" || document.Result.Readiness.ProblemCount != 0 || document.Result.Readiness.NextAction != "Run plystra check to validate the selected model." || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("inspect readiness/evidence = %#v, %s", document.Result.Readiness, document.Result.ResolutionEvidence)
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("inspect JSON leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
}

func TestInspectSelectorsUseOneSharedSelectedModel(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		selectedEnv string
		path        string
		transports  []string
		nextAction  string
	}{
		{
			name:        "explicit environment",
			arguments:   []string{"inspect", "--format", "json", "--env", "production"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "environment",
			selectedEnv: "production",
			path:        "plystra.production.yaml",
			transports:  []string{"rest"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "ambient environment",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_ENV": "production"},
			mode:        "environment",
			selectedEnv: "production",
			path:        "plystra.production.yaml",
			transports:  []string{"rest"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "explicit configuration",
			arguments:   []string{"inspect", "--format", "json", "--config", "deploy/customer.yaml"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect", "rest"},
			nextAction:  "Run plystra check --config \"deploy/customer.yaml\" to validate the selected model.",
		},
		{
			name:        "ambient configuration",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect", "rest"},
			nextAction:  "Run plystra check --config \"deploy/customer.yaml\" to validate the selected model.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 0 || stderr != inspectProgress {
				t.Fatalf("inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeInspectCommandEnvelope(t, stdout)
			if document.ConfigurationMode != test.mode || document.Result.Configuration.Mode != test.mode || document.Result.Configuration.Environment != test.selectedEnv || document.Result.Configuration.Path != test.path || !reflect.DeepEqual(document.Result.Summary.Transports, test.transports) || document.Result.Readiness.NextAction != test.nextAction {
				t.Fatalf("selected model = mode %q/%q env %q path %q transports %#v next %q", document.ConfigurationMode, document.Result.Configuration.Mode, document.Result.Configuration.Environment, document.Result.Configuration.Path, document.Result.Summary.Transports, document.Result.Readiness.NextAction)
			}
			if strings.Contains(stdout, root) {
				t.Fatalf("inspect JSON contains absolute Project path: %s", stdout)
			}
		})
	}
}

func TestInspectVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createInspectCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect", "--verbose", "--env", "production"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Configuration: environment \"production\" (plystra.production.yaml)\n",
		"Transports: rest\n",
		"Resolution evidence:\n  {\n",
		"    \"modules\": [",
		"    \"configuration_selection\": {",
		"    \"static_assembly\": {",
		"    \"http_transports\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose inspect omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestInspectFailuresKeepJSONStdoutEmptyAndDoNotMutate(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		want        string
	}{
		{name: "missing overlay", arguments: []string{"inspect", "--format", "json", "--env", "missing"}, want: "plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"inspect", "--format", "json", "--env", "../test"}, want: "safe filename component"},
		{name: "ambient conflict", arguments: []string{"inspect", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "deploy/customer.yaml"}, want: "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, inspectProgress) || !strings.Contains(stderr, test.want) {
				t.Fatalf("inspect failure = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed inspect mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func createInspectCommandProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/inspect\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  address: resolved-secret-marker\n  transports:\n    connect: true\n    rest: false\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "http:\n  transports:\n    connect: false\n    rest: true\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "http:\n  transports:\n    connect: true\n    rest: true\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func decodeInspectCommandEnvelope(t testing.TB, output string) inspectCommandEnvelope {
	t.Helper()
	var result inspectCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode inspect JSON: %v\n%s", err, output)
	}
	return result
}

func inspectCommandEnvironment(values map[string]string) []string {
	environment := commandGoEnvironment()
	filtered := environment[:0]
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PLYSTRA_ENV") || strings.EqualFold(key, "PLYSTRA_CONFIG") {
			continue
		}
		filtered = append(filtered, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filtered = append(filtered, key+"="+values[key])
	}
	return filtered
}

func snapshotInspectProject(t testing.TB, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[filepath.ToSlash(relative)+"/"] = nil
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot Project: %v", err)
	}
	return result
}
