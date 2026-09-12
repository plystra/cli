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

type inspectGraphCommandEnvelope struct {
	Schema                 string `json:"schema"`
	SchemaVersion          int    `json:"schema_version"`
	ConfigurationMode      string `json:"configuration_mode"`
	ApplicationModelDigest string `json:"application_model_digest"`
	Result                 struct {
		Type  string `json:"type"`
		Nodes []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Label   string `json:"label"`
			Sources []struct {
				Module string `json:"module"`
				Path   string `json:"path"`
				Kind   string `json:"kind"`
			} `json:"sources"`
		} `json:"nodes"`
		Edges []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			From   string `json:"from"`
			To     string `json:"to"`
			Reason string `json:"reason"`
		} `json:"edges"`
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
		"Transports: none\n" +
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
	if document.Result.Summary.SelectedPluginCount != 0 || document.Result.Summary.AvailableCapabilityCount != 2 || !reflect.DeepEqual(document.Result.Summary.Transports, []string{}) {
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
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "ambient environment",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_ENV": "production"},
			mode:        "environment",
			selectedEnv: "production",
			path:        "plystra.production.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "explicit configuration",
			arguments:   []string{"inspect", "--format", "json", "--config", "deploy/customer.yaml"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --config \"deploy/customer.yaml\" to validate the selected model.",
		},
		{
			name:        "ambient configuration",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect"},
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
		"Transports: connect\n",
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

func TestInspectModulesHumanAndJSONAreDeterministicAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	humanExit, humanStdout, humanStderr := runCommand(t, []string{"inspect", "modules"}, nested, inspectCommandEnvironment(nil))
	wantHuman := inspectProgress +
		"Module graph: 1 modules, 0 dependencies\n" +
		"Module: example.com/acme/inspect (current)\n" +
		"  Source: example.com/acme/inspect:plystra.yaml:1:1 (project-marker)\n"
	if humanExit != 0 || humanStdout != wantHuman || humanStderr != "" {
		t.Fatalf("human module graph = exit %d, stdout %q, stderr %q; want stdout %q", humanExit, humanStdout, humanStderr, wantHuman)
	}

	firstExit, firstStdout, firstStderr := runCommand(t, []string{"inspect", "modules", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"inspect", "modules", "--verbose", "--format", "json"}, root, inspectCommandEnvironment(nil))
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
		t.Fatalf("JSON module graph = first (%d, %q, %q) second (%d, %q, %q)", firstExit, firstStdout, firstStderr, secondExit, secondStdout, secondStderr)
	}
	if strings.Count(firstStdout, "\n") != 1 || !strings.HasSuffix(firstStdout, "\n") {
		t.Fatalf("module graph JSON is not one document: %q", firstStdout)
	}
	document := decodeInspectGraphCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("module graph envelope = %#v", document)
	}
	if document.Result.Type != "modules" || len(document.Result.Nodes) != 1 || len(document.Result.Edges) != 0 || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("module graph result = %#v", document.Result)
	}
	if document.Result.Nodes[0].ID != "module:example.com/acme/inspect" || document.Result.Nodes[0].Kind != "module" || document.Result.Nodes[0].Label != "example.com/acme/inspect" || len(document.Result.Nodes[0].Sources) != 1 || document.Result.Nodes[0].Sources[0].Path != "plystra.yaml" {
		t.Fatalf("module graph node = %#v", document.Result.Nodes[0])
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("module graph leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("module graph mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectModulesIncludesDeterministicDependencyEdges(t *testing.T) {
	t.Parallel()

	root, nested := createInspectModuleGraphProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect", "modules", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	if exitCode != 0 || stderr != inspectProgress {
		t.Fatalf("module graph dependency inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	document := decodeInspectGraphCommandEnvelope(t, stdout)
	if document.ConfigurationMode != "default" || len(document.Result.Nodes) != 2 || len(document.Result.Edges) != 1 {
		t.Fatalf("module graph dependency shape = mode %q nodes %d edges %d", document.ConfigurationMode, len(document.Result.Nodes), len(document.Result.Edges))
	}
	if document.Result.Nodes[0].ID != "module:example.com/acme/inspect" || document.Result.Nodes[1].ID != "module:example.com/acme/library" {
		t.Fatalf("module graph node order = %#v", document.Result.Nodes)
	}
	edge := document.Result.Edges[0]
	if edge.ID != "requires:example.com/acme/inspect->example.com/acme/library" || edge.Kind != "requires" || edge.From != "module:example.com/acme/inspect" || edge.To != "module:example.com/acme/library" || edge.Reason != "replacement" {
		t.Fatalf("module graph edge = %#v", edge)
	}
	if strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("module graph dependency output leaked a path or secret: %s", stdout)
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
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  address: resolved-secret-marker\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {expose: {kernel.health/v1: {transport: connect}}}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "http: {expose: {kernel.info/v1: {transport: connect}}}\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func createInspectModuleGraphProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/inspect\n\ngo 1.26\n\nrequire example.com/acme/library v0.0.0\n\nreplace example.com/acme/library => ./library\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  address: resolved-secret-marker\n")
	writeCommandFile(t, filepath.Join(root, "library", "go.mod"), "module example.com/acme/library\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "library", "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "library", "library.go"), "package library\n")
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

func decodeInspectGraphCommandEnvelope(t testing.TB, output string) inspectGraphCommandEnvelope {
	t.Helper()
	var result inspectGraphCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode inspect graph JSON: %v\n%s", err, output)
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
