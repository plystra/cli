package command_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type explainCommandEnvelope struct {
	Schema                 string `json:"schema"`
	SchemaVersion          int    `json:"schema_version"`
	ConfigurationMode      string `json:"configuration_mode"`
	ApplicationModelDigest string `json:"application_model_digest"`
	Result                 struct {
		Subject struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"subject"`
		Decision struct {
			Outcome string `json:"outcome"`
		} `json:"decision"`
		Reason struct {
			Code    string `json:"code"`
			Sources []struct {
				Module string `json:"module"`
				Path   string `json:"path"`
				Kind   string `json:"kind"`
				Line   int    `json:"line"`
				Column int    `json:"column"`
			} `json:"sources"`
		} `json:"reason"`
		Change struct {
			Kind    string `json:"kind"`
			Module  string `json:"module"`
			Path    string `json:"path"`
			Field   string `json:"field"`
			Command string `json:"command"`
		} `json:"change"`
		ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
	} `json:"result"`
}

func TestExplainCapabilityHumanOutputIsConciseCausalAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "email.send/v1"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		inspectProgress,
		"Capability: email.send/v1\n",
		"Decision: required; Provider acme.email.smtp is selected\n",
		"Reason: current-project-replacement\n",
		"Source: example.com/acme/provider-use:plystra.yaml:",
		"(provider-selection)\n",
		"Change: plystra use email.send/v1 acme.email.local\n",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("capability explanation omits %q:\n%s", fragment, stdout)
		}
	}
	for _, forbidden := range []string{"Resolution evidence:", "provider_candidates", "contract_digest", "resolved-secret-marker", root} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("concise capability explanation contains %q:\n%s", forbidden, stdout)
		}
	}
	if exitCode != 0 || stderr != "" {
		t.Fatalf("capability explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("capability explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainCapabilityJSONOwnsStdoutAndIsDeterministic(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress {
		t.Fatalf("JSON explanation = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if firstStdout != secondStdout || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
		t.Fatalf("JSON stdout is not one deterministic document:\nfirst:  %q\nsecond: %q", firstStdout, secondStdout)
	}
	document := decodeExplainCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.explain" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("explain envelope identity = %#v", document)
	}
	if document.Result.Subject.Kind != "capability" || document.Result.Subject.ID != "email.send/v1" || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "current-project-replacement" {
		t.Fatalf("capability decision = %#v", document.Result)
	}
	if len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Module != "example.com/acme/provider-use" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[0].Kind != "provider-selection" {
		t.Fatalf("capability reason sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "command" || document.Result.Change.Command != "plystra use email.send/v1 acme.email.local" || document.Result.Change.Module != "" || document.Result.Change.Path != "" || document.Result.Change.Field != "" || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("capability change/evidence = %#v, %s", document.Result.Change, document.Result.ResolutionEvidence)
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("capability JSON leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
}

func TestExplainCapabilitySelectorsUseOneSharedSelectedModel(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		path        string
		command     string
	}{
		{
			name:        "explicit environment",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "production"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "environment",
			path:        "plystra.production.yaml",
			command:     `plystra use email.send/v1 acme.email.smtp --env "production"`,
		},
		{
			name:        "ambient environment",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json"},
			environment: map[string]string{"PLYSTRA_ENV": "production"},
			mode:        "environment",
			path:        "plystra.production.yaml",
			command:     `plystra use email.send/v1 acme.email.smtp --env "production"`,
		},
		{
			name:        "explicit configuration",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json", "--config", "deploy/customer.yaml"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			command:     `plystra use email.send/v1 acme.email.smtp --config "deploy/customer.yaml"`,
		},
		{
			name:        "ambient configuration",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json"},
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			command:     `plystra use email.send/v1 acme.email.smtp --config "deploy/customer.yaml"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 0 || stderr != inspectProgress {
				t.Fatalf("explain = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeExplainCommandEnvelope(t, stdout)
			if document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "current-project-replacement" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Command != test.command {
				t.Fatalf("selected explanation = mode %q outcome %q reason %q sources %#v change %#v", document.ConfigurationMode, document.Result.Decision.Outcome, document.Result.Reason.Code, document.Result.Reason.Sources, document.Result.Change)
			}
			if strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
				t.Fatalf("selected explanation leaked private input: %s", stdout)
			}
		})
	}
}

func TestExplainCapabilityCoversAvailableSoleProviderAndIntrinsicDecisions(t *testing.T) {
	t.Parallel()

	t.Run("available but not required", func(t *testing.T) {
		_, nested := createExplainCommandProject(t)
		exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "reports.read/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
		document := decodeExplainCommandEnvelope(t, stdout)
		if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "capability-not-required" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Kind != "configuration-selection" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.require["reports.read/v1"]` {
			t.Fatalf("available explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
		}
	})

	t.Run("sole ordinary Provider", func(t *testing.T) {
		root, nested := createExplainCommandProject(t)
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1, reports.read/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
		exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "reports.read/v1"}, nested, inspectCommandEnvironment(nil))
		for _, fragment := range []string{
			"Decision: required; Provider acme.other is selected\n",
			"Reason: sole-provider\n",
			"(provider-declaration)\n",
			`Change: edit plystra.yaml at capabilities.use["reports.read/v1"]`,
		} {
			if !strings.Contains(stdout, fragment) {
				t.Fatalf("sole-Provider explanation omits %q:\n%s", fragment, stdout)
			}
		}
		if exitCode != 0 || stderr != "" {
			t.Fatalf("sole-Provider explanation = exit %d, stderr %q", exitCode, stderr)
		}
	})

	t.Run("required Kernel intrinsic", func(t *testing.T) {
		root, nested := createExplainCommandProject(t)
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1, kernel.health/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
		exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "kernel.health/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
		document := decodeExplainCommandEnvelope(t, stdout)
		if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "intrinsic-kernel" || len(document.Result.Reason.Sources) != 1 || document.Result.Change.Kind != "file" || document.Result.Change.Field != `capabilities.require["kernel.health/v1"]` {
			t.Fatalf("intrinsic explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
		}
	})
}

func TestExplainCapabilityReportsEveryCompatibleInheritedSelectionSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	aRoot := filepath.Join(root, "a")
	bRoot := filepath.Join(root, "b")
	writeCommandFile(t, filepath.Join(aRoot, "go.mod"), "module example.com/a\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(bRoot, "go.mod"), "module example.com/b\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(aRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	writeCommandFile(t, filepath.Join(bRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	writeCommandFile(t, filepath.Join(aRoot, "smtp", "plugin.yaml"), "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(aRoot, "smtp", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.2.0
)

replace example.com/a => ../a
replace example.com/b => ../b
`)
	writeCommandFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1]}\n")
	nested := filepath.Join(appRoot, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}

	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document := decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "inherited-selection" || len(document.Result.Reason.Sources) != 2 {
		t.Fatalf("inherited explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Reason.Sources[0].Module != "example.com/a" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[1].Module != "example.com/b" || document.Result.Reason.Sources[1].Path != "plystra.yaml" {
		t.Fatalf("inherited explanation sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Module != "example.com/app" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || strings.Contains(stdout, root) {
		t.Fatalf("inherited explanation change = %#v", document.Result.Change)
	}
}

func TestExplainCapabilityFailuresKeepJSONStdoutEmptyAndDoNotMutate(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		want        string
	}{
		{name: "unknown canonical Capability", arguments: []string{"explain", "capability", "missing.operation/v1", "--format", "json"}, want: "not visible in the selected application model"},
		{name: "invalid Capability identity", arguments: []string{"explain", "capability", "email.send", "--format", "json"}, want: "invalid capability ID"},
		{name: "missing overlay", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "missing"}, want: "plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "../test"}, want: "safe filename component"},
		{name: "ambient conflict", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "deploy/customer.yaml"}, want: "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, inspectProgress) || !strings.Contains(stderr, test.want) {
				t.Fatalf("explain failure = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestExplainCapabilityVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createExplainCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--verbose", "--env", "production"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Decision: required; Provider acme.email.local is selected\n",
		`Change: plystra use email.send/v1 acme.email.smtp --env "production"`,
		"Resolution evidence:\n  {\n",
		"    \"requirements\": [",
		"    \"provider_candidates\": [",
		"    \"selected_providers\": [",
		"    \"configuration_selection\": {",
		"    \"static_assembly\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func createExplainCommandProject(t *testing.T) (string, string) {
	t.Helper()
	root := writeProviderCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\nhttp:\n  address: resolved-secret-marker\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.local}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.ignored.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.smtp}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.local}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "ignored.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
	nested := filepath.Join(root, "smtp", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func decodeExplainCommandEnvelope(t testing.TB, output string) explainCommandEnvelope {
	t.Helper()
	var result explainCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode explain JSON: %v\n%s", err, output)
	}
	return result
}
