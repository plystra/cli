package command_test

import (
	"encoding/json"
	"fmt"
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

func TestExplainAliasHumanOutputIsConciseCausalAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "alias", "mail.send/v1"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Alias: mail.send/v1\n",
		"Decision: maps directly to email.send/v1; inherits target exposure (Go, HTTP, JavaScript)\n",
		"Reason: application-alias\n",
		"Source: example.com/acme/provider-use:plystra.yaml:",
		"(alias-target)\n",
		`Change: edit plystra.yaml at capabilities.aliases["mail.send/v1"]`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("Alias explanation omits %q:\n%s", fragment, stdout)
		}
	}
	for _, forbidden := range []string{"Resolution evidence:", "target_contract_digest", "resolved-secret-marker", root} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("concise Alias explanation contains %q:\n%s", forbidden, stdout)
		}
	}
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Alias explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Alias explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "alias", "mail.send/v1", "--env", "production"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Decision: maps directly to email.send/v1; narrows target exposure to (Go)\n",
		"Source: example.com/acme/provider-use:plystra.production.yaml:",
		`Change: edit plystra.production.yaml at capabilities.aliases["mail.send/v1"]`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("narrowed Alias explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("narrowed Alias explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("selected Alias explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainAliasJSONIsDeterministicAndSelectorMatched(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		path        string
	}{
		{name: "default", arguments: []string{"explain", "alias", "mail.send/v1", "--format", "json"}, mode: "default", path: "plystra.yaml"},
		{name: "explicit environment", arguments: []string{"explain", "alias", "mail.send/v1", "--format", "json", "--env", "production"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "environment", path: "plystra.production.yaml"},
		{name: "ambient environment", arguments: []string{"explain", "alias", "mail.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production"}, mode: "environment", path: "plystra.production.yaml"},
		{name: "explicit configuration", arguments: []string{"explain", "alias", "mail.send/v1", "--format", "json", "--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml"},
		{name: "ambient configuration", arguments: []string{"explain", "alias", "mail.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstExit, firstStdout, firstStderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			secondExit, secondStdout, secondStderr := runCommand(t, append(append([]string(nil), test.arguments...), "--verbose"), root, inspectCommandEnvironment(test.environment))
			if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
				t.Fatalf("Alias JSON = first (%d, %q) second (%d, %q)\nfirst: %s\nsecond: %s", firstExit, firstStderr, secondExit, secondStderr, firstStdout, secondStdout)
			}
			document := decodeExplainCommandEnvelope(t, firstStdout)
			if document.ConfigurationMode != test.mode || document.Result.Subject.Kind != "alias" || document.Result.Subject.ID != "mail.send/v1" || document.Result.Decision.Outcome != "valid" || document.Result.Reason.Code != "application-alias" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Kind != "file" || document.Result.Change.Path != test.path || document.Result.Change.Field != `capabilities.aliases["mail.send/v1"]` {
				t.Fatalf("selected Alias explanation = mode %q result %#v", document.ConfigurationMode, document.Result)
			}
			if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
				t.Fatalf("Alias explanation leaked private input: %s", firstStdout)
			}
		})
	}
}

func TestExplainExposureHumanOutputCoversPublicAndInternalCapabilities(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "exposure", "email.send/v1"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Exposure: email.send/v1\n",
		"Decision: public canonical Capability through HTTP and JavaScript\n",
		"Reason: http-expose\n",
		"Source: example.com/acme/provider-use:plystra.yaml:",
		"(exposure)\n",
		`Change: edit plystra.yaml at http.expose["email.send/v1"]`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("public exposure explanation omits %q:\n%s", fragment, stdout)
		}
	}
	for _, forbidden := range []string{"Resolution evidence:", "contract_digest", "resolved-secret-marker", root} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("concise exposure explanation contains %q:\n%s", forbidden, stdout)
		}
	}
	if exitCode != 0 || stderr != "" {
		t.Fatalf("public exposure explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "exposure", "reports.read/v1"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Exposure: reports.read/v1\n",
		"Decision: internal canonical Capability; no HTTP or JavaScript exposure\n",
		"Reason: not-publicly-exposed\n",
		"Source: example.com/acme/provider-use:plystra.yaml (configuration-selection)\n",
		`Change: edit plystra.yaml at http.expose["reports.read/v1"]`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("internal exposure explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("internal exposure explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("exposure explanations mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainExposureJSONIsDeterministicAndSelectorMatched(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		outcome     string
		reason      string
		path        string
	}{
		{name: "default", arguments: []string{"explain", "exposure", "mail.send/v1", "--format", "json"}, mode: "default", outcome: "public", reason: "application-alias", path: "plystra.yaml"},
		{name: "explicit environment", arguments: []string{"explain", "exposure", "mail.send/v1", "--format", "json", "--env", "production"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "environment", outcome: "internal", reason: "alias-exposure-narrowing", path: "plystra.production.yaml"},
		{name: "ambient environment", arguments: []string{"explain", "exposure", "mail.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production"}, mode: "environment", outcome: "internal", reason: "alias-exposure-narrowing", path: "plystra.production.yaml"},
		{name: "explicit configuration", arguments: []string{"explain", "exposure", "mail.send/v1", "--format", "json", "--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "explicit-config", outcome: "public", reason: "application-alias", path: "deploy/customer.yaml"},
		{name: "ambient configuration", arguments: []string{"explain", "exposure", "mail.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, mode: "explicit-config", outcome: "public", reason: "application-alias", path: "deploy/customer.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstExit, firstStdout, firstStderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			secondExit, secondStdout, secondStderr := runCommand(t, append(append([]string(nil), test.arguments...), "--verbose"), root, inspectCommandEnvironment(test.environment))
			if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
				t.Fatalf("exposure JSON = first (%d, %q) second (%d, %q)\nfirst: %s\nsecond: %s", firstExit, firstStderr, secondExit, secondStderr, firstStdout, secondStdout)
			}
			document := decodeExplainCommandEnvelope(t, firstStdout)
			if document.ConfigurationMode != test.mode || document.Result.Subject.Kind != "exposure" || document.Result.Subject.ID != "mail.send/v1" || document.Result.Decision.Outcome != test.outcome || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Kind != "file" || document.Result.Change.Path != test.path || document.Result.Change.Field != `capabilities.aliases["mail.send/v1"]` {
				t.Fatalf("selected exposure explanation = mode %q result %#v", document.ConfigurationMode, document.Result)
			}
			if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
				t.Fatalf("exposure explanation leaked private input: %s", firstStdout)
			}
		})
	}
}

func TestExplainPluginCurrentProjectOutputIsConciseCausalAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "plugin", "acme.email.smtp"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Plugin: acme.email.smtp\n",
		"Decision: selected from the current Project\n",
		"Reason: current-project\n",
		"Source: example.com/acme/provider-use:smtp/plugin.yaml:1:1 (plugin-declaration)\n",
		"Change: edit smtp/plugin.yaml at id\n",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("Plugin explanation omits %q:\n%s", fragment, stdout)
		}
	}
	for _, forbidden := range []string{"Resolution evidence:", "provider_candidates", "contract_digest", "resolved-secret-marker", root} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("concise Plugin explanation contains %q:\n%s", forbidden, stdout)
		}
	}
	if exitCode != 0 || stderr != "" {
		t.Fatalf("Plugin explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Plugin explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainPluginCoversProviderAndVisibleUnselectedDecisions(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"explain", "plugin", "example.shared", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"explain", "plugin", "example.shared", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
		t.Fatalf("selected Plugin JSON = first (%d, %q) second (%d, %q)\nfirst: %s\nsecond: %s", firstExit, firstStderr, secondExit, secondStderr, firstStdout, secondStdout)
	}
	document := decodeExplainCommandEnvelope(t, firstStdout)
	if document.Result.Subject.Kind != "plugin" || document.Result.Subject.ID != "example.shared" || document.Result.Decision.Outcome != "selected" || document.Result.Reason.Code != "provider" {
		t.Fatalf("selected Plugin decision = %#v", document.Result)
	}
	if len(document.Result.Reason.Sources) != 2 || document.Result.Reason.Sources[0].Module != "example.com/app" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[1].Module != "example.com/platform" || document.Result.Reason.Sources[1].Path != "shared/capabilities/reports.read/v1/capability.yaml" {
		t.Fatalf("selected Plugin sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "command" || document.Result.Change.Command != "plystra use email.send/v1 example.alternative" || strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("selected Plugin change/output = %#v\n%s", document.Result.Change, firstStdout)
	}

	exitCode, stdout, stderr := runCommand(t, []string{"explain", "plugin", "example.alternative", "--format", "json"}, nested, environment)
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "another-provider-selected" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != "alternative/capabilities/email.send/v1/capability.yaml" || document.Result.Change.Command != "plystra use email.send/v1 example.alternative" {
		t.Fatalf("unselected alternative Plugin = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "plugin", "example.optional", "--format", "json"}, nested, environment)
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "capability-not-required" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != "optional/capabilities/audit.record/v1/capability.yaml" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.require["audit.record/v1"]` {
		t.Fatalf("unrequired Provider Plugin = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
}

func TestExplainPluginSelectorsUseOneSharedSelectedModel(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		path        string
		command     string
	}{
		{name: "explicit environment", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json", "--env", "production"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "environment", path: "plystra.production.yaml", command: `plystra use email.send/v1 example.shared --env "production"`},
		{name: "ambient environment", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production"}, mode: "environment", path: "plystra.production.yaml", command: `plystra use email.send/v1 example.shared --env "production"`},
		{name: "explicit configuration", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json", "--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml", command: `plystra use email.send/v1 example.shared --config "deploy/customer.yaml"`},
		{name: "ambient configuration", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json"}, environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml", command: `plystra use email.send/v1 example.shared --config "deploy/customer.yaml"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "selected" || document.Result.Reason.Code != "provider" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Command != test.command {
				t.Fatalf("selected Plugin explanation = exit %d, stderr %q, mode %q, result %#v", exitCode, stderr, document.ConfigurationMode, document.Result)
			}
			if strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
				t.Fatalf("selected Plugin explanation leaked private input: %s", stdout)
			}
		})
	}
}

func TestExplainConfigurationReportsTypedOwnershipReplacementAndRemoval(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	beforeProject := snapshotInspectProject(t, root)
	dependencyRoot := filepath.Join(filepath.Dir(root), "platform")
	beforeDependency := snapshotInspectProject(t, dependencyRoot)
	tests := []struct {
		name         string
		arguments    []string
		mode         string
		outcome      string
		reason       string
		sourceModule string
		sourcePath   string
		changePath   string
		changeField  string
	}{
		{
			name:         "dependency Secret reference",
			arguments:    []string{"explain", "config", `config["example.com/platform/shared.New"]["password"]`, "--format", "json"},
			mode:         "default",
			outcome:      "effective",
			reason:       "dependency-project",
			sourceModule: "example.com/platform",
			sourcePath:   "plystra.yaml",
			changePath:   "plystra.yaml",
			changeField:  `config["example.com/platform/shared.New"]["password"]`,
		},
		{
			name:         "root replacement",
			arguments:    []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json"},
			mode:         "default",
			outcome:      "effective",
			reason:       "current-project-root",
			sourceModule: "example.com/app",
			sourcePath:   "plystra.yaml",
			changePath:   "plystra.yaml",
			changeField:  `config["example.com/platform/shared.New"]["host"]`,
		},
		{
			name:         "root process field",
			arguments:    []string{"explain", "config", "http.address", "--format", "json"},
			mode:         "default",
			outcome:      "effective",
			reason:       "current-project-root",
			sourceModule: "example.com/app",
			sourcePath:   "plystra.yaml",
			changePath:   "plystra.yaml",
			changeField:  "http.address",
		},
		{
			name:         "environment replacement",
			arguments:    []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json", "--env", "production"},
			mode:         "environment",
			outcome:      "effective",
			reason:       "current-project-environment",
			sourceModule: "example.com/app",
			sourcePath:   "plystra.production.yaml",
			changePath:   "plystra.production.yaml",
			changeField:  `config["example.com/platform/shared.New"]["host"]`,
		},
		{
			name:         "environment removal",
			arguments:    []string{"explain", "config", `config["example.com/platform/shared.New"]["password"]`, "--format", "json", "--env", "production"},
			mode:         "environment",
			outcome:      "removed",
			reason:       "current-project-environment",
			sourceModule: "example.com/app",
			sourcePath:   "plystra.production.yaml",
			changePath:   "plystra.production.yaml",
			changeField:  `config["example.com/platform/shared.New"]["password"]`,
		},
		{
			name:         "full replacement",
			arguments:    []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json", "--config", "deploy/customer.yaml"},
			mode:         "explicit-config",
			outcome:      "effective",
			reason:       "current-project-config",
			sourceModule: "example.com/app",
			sourcePath:   "deploy/customer.yaml",
			changePath:   "deploy/customer.yaml",
			changeField:  `config["example.com/platform/shared.New"]["host"]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(nil))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != test.mode {
				t.Fatalf("configuration explanation = exit %d, stderr %q, mode %q", exitCode, stderr, document.ConfigurationMode)
			}
			if document.Result.Subject.Kind != "configuration" || document.Result.Subject.ID != test.changeField || document.Result.Decision.Outcome != test.outcome || document.Result.Reason.Code != test.reason {
				t.Fatalf("configuration decision = %#v", document.Result)
			}
			if len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Module != test.sourceModule || document.Result.Reason.Sources[0].Path != test.sourcePath {
				t.Fatalf("configuration reason sources = %#v", document.Result.Reason.Sources)
			}
			if document.Result.Change.Kind != "file" || document.Result.Change.Module != "example.com/app" || document.Result.Change.Path != test.changePath || document.Result.Change.Field != test.changeField || document.Result.Change.Command != "" {
				t.Fatalf("configuration change = %#v", document.Result.Change)
			}
			assertConfigurationExplanationRedacted(t, stdout, root)
		})
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, beforeProject) {
		t.Fatalf("configuration explanations mutated the Project:\nbefore: %#v\nafter:  %#v", beforeProject, after)
	}
	if after := snapshotInspectProject(t, dependencyRoot); !reflect.DeepEqual(after, beforeDependency) {
		t.Fatalf("configuration explanations mutated the dependency:\nbefore: %#v\nafter:  %#v", beforeDependency, after)
	}
}

func TestExplainConfigurationCanonicalPathProducesDeterministicJSON(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress {
		t.Fatalf("configuration JSON = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if firstStdout != secondStdout || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
		t.Fatalf("configuration JSON is not one deterministic canonical document:\nfirst:  %q\nsecond: %q", firstStdout, secondStdout)
	}
	document := decodeExplainCommandEnvelope(t, firstStdout)
	if document.Result.Subject.Kind != "configuration" || document.Result.Subject.ID != `config["example.com/platform/shared.New"]["host"]` || document.Result.Decision.Outcome != "effective" || document.Result.Reason.Code != "current-project-root" {
		t.Fatalf("configuration JSON decision = %#v", document.Result)
	}
	assertConfigurationExplanationRedacted(t, firstStdout, root)
}

func TestExplainConfigurationReportsAncestorSuppression(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "config", `config["example.com/platform/shared.New"]["settings"]["nested"]`, "--format", "json", "--env", "suppressed"}, nested, inspectCommandEnvironment(nil))
	document := decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != "environment" || document.Result.Decision.Outcome != "suppressed" || document.Result.Reason.Code != "ancestor-removal" {
		t.Fatalf("suppressed configuration explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Subject.ID != `config["example.com/platform/shared.New"]["settings"]["nested"]` || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Module != "example.com/app" || document.Result.Reason.Sources[0].Path != "plystra.suppressed.yaml" || document.Result.Reason.Sources[0].Kind != "configuration-removal" {
		t.Fatalf("suppressed configuration source = %#v", document.Result)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.suppressed.yaml" || document.Result.Change.Field != `config["example.com/platform/shared.New"]` {
		t.Fatalf("suppressed configuration change = %#v", document.Result.Change)
	}
	assertConfigurationExplanationRedacted(t, stdout, root)
}

func TestExplainConfigurationSelectorsUseOneSharedSelectedModel(t *testing.T) {
	t.Parallel()

	root, nested := createExplainDependencyPluginProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		reason      string
		path        string
	}{
		{name: "explicit environment", arguments: []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json", "--env", "production"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "environment", reason: "current-project-environment", path: "plystra.production.yaml"},
		{name: "ambient environment", arguments: []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production"}, mode: "environment", reason: "current-project-environment", path: "plystra.production.yaml"},
		{name: "explicit configuration", arguments: []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json", "--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "explicit-config", reason: "current-project-config", path: "deploy/customer.yaml"},
		{name: "ambient configuration", arguments: []string{"explain", "config", `config["example.com/platform/shared.New"]["host"]`, "--format", "json"}, environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, mode: "explicit-config", reason: "current-project-config", path: "deploy/customer.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "effective" || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Path != test.path {
				t.Fatalf("selected configuration explanation = exit %d, stderr %q, mode %q, result %#v", exitCode, stderr, document.ConfigurationMode, document.Result)
			}
			assertConfigurationExplanationRedacted(t, stdout, root)
		})
	}
}

func TestExplainConfigurationHumanOutputNamesPluginAndVerboseEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createExplainDependencyPluginProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "config", `config["example.com/platform/shared.New"]["password"]`, "--verbose"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		`Configuration: config["example.com/platform/shared.New"]["password"]`,
		"Decision: effective redacted from dependency-project\n",
		"Reason: dependency-project\n",
		"Source: example.com/platform:plystra.yaml:1:1 (configuration-value)\n",
		`Change: edit plystra.yaml at config["example.com/platform/shared.New"]["password"]`,
		"Resolution evidence:\n  {\n",
		`    "configuration_fields": [`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("configuration explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" {
		t.Fatalf("configuration explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	assertConfigurationExplanationRedacted(t, stdout, root)
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
	writeCommandFile(t, filepath.Join(aRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}, aliases: {mail.send/v1: email.send/v1}}\nhttp: {expose: [email.send/v1]}\n")
	writeCommandFile(t, filepath.Join(bRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}, aliases: {mail.send/v1: email.send/v1}}\nhttp: {expose: [email.send/v1]}\n")
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
	writeCommandFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1]}\nhttp: {expose: [email.send/v1]}\n")
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

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "alias", "mail.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Subject.Kind != "alias" || document.Result.Decision.Outcome != "valid" || document.Result.Reason.Code != "compatible-alias-sources" || len(document.Result.Reason.Sources) != 2 {
		t.Fatalf("inherited Alias explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Reason.Sources[0].Module != "example.com/a" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[1].Module != "example.com/b" || document.Result.Reason.Sources[1].Path != "plystra.yaml" {
		t.Fatalf("inherited Alias explanation sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Module != "example.com/app" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.aliases["mail.send/v1"]` || strings.Contains(stdout, root) {
		t.Fatalf("inherited Alias explanation change = %#v", document.Result.Change)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "exposure", "email.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Subject.Kind != "exposure" || document.Result.Decision.Outcome != "public" || document.Result.Reason.Code != "http-expose" || len(document.Result.Reason.Sources) != 1 {
		t.Fatalf("current-Project exposure explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Reason.Sources[0].Module != "example.com/app" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `http.expose["email.send/v1"]` {
		t.Fatalf("current-Project exposure explanation sources or change = sources %#v, change %#v", document.Result.Reason.Sources, document.Result.Change)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "exposure", "mail.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.Result.Decision.Outcome != "public" || document.Result.Reason.Code != "compatible-alias-sources" || len(document.Result.Reason.Sources) != 2 || document.Result.Change.Field != `capabilities.aliases["mail.send/v1"]` {
		t.Fatalf("inherited Alias exposure explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
}

func TestExplainAliasCoversGeneratedOnlyAndMixedSources(t *testing.T) {
	root, nested := createExplainGenerationAliasProject(t)
	before := snapshotInspectProject(t, root)
	tests := []struct {
		name        string
		alias       string
		reason      string
		sourceKinds []string
	}{
		{name: "generated only", alias: "orders.start/v1", reason: "generation-extension-alias", sourceKinds: []string{"generation-alias-contribution"}},
		{name: "application and generation", alias: "orders.submit/v1", reason: "compatible-alias-sources", sourceKinds: []string{"generation-alias-contribution", "alias-target"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, []string{"explain", "alias", test.alias, "--format", "json"}, nested, inspectCommandEnvironment(nil))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != inspectProgress || document.Result.Subject.Kind != "alias" || document.Result.Subject.ID != test.alias || document.Result.Decision.Outcome != "valid" || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != len(test.sourceKinds) {
				t.Fatalf("generated Alias explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
			}
			for index, kind := range test.sourceKinds {
				if document.Result.Reason.Sources[index].Kind != kind {
					t.Fatalf("generated Alias sources = %#v", document.Result.Reason.Sources)
				}
			}
			if document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["authn.session.verify/v1"]` {
				t.Fatalf("generated Alias change = %#v", document.Result.Change)
			}
			if strings.Contains(stdout, root) || strings.Contains(stdout, "generation-private-marker") {
				t.Fatalf("generated Alias explanation leaked private input: %s", stdout)
			}
		})
	}
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "alias", "orders.start/v1", "--format", "json", "--env", "production"}, nested, inspectCommandEnvironment(map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}))
	document := decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != "environment" || document.Result.Reason.Code != "generation-extension-alias" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.production.yaml" || document.Result.Change.Field != `capabilities.use["authn.session.verify/v1"]` {
		t.Fatalf("selected generated Alias explanation = exit %d, stderr %q, mode %q, result %#v", exitCode, stderr, document.ConfigurationMode, document.Result)
	}
	for _, test := range []struct {
		name        string
		alias       string
		arguments   []string
		mode        string
		outcome     string
		reason      string
		sourceCount int
		changeField string
	}{
		{name: "generated public", alias: "orders.start/v1", arguments: []string{"explain", "exposure", "orders.start/v1", "--format", "json"}, mode: "default", outcome: "public", reason: "generation-extension-alias", sourceCount: 1, changeField: `capabilities.use["authn.session.verify/v1"]`},
		{name: "mixed public", alias: "orders.submit/v1", arguments: []string{"explain", "exposure", "orders.submit/v1", "--format", "json"}, mode: "default", outcome: "public", reason: "compatible-alias-sources", sourceCount: 2, changeField: `capabilities.use["authn.session.verify/v1"]`},
		{name: "target internal in environment", alias: "orders.start/v1", arguments: []string{"explain", "exposure", "orders.start/v1", "--format", "json", "--env", "production"}, mode: "environment", outcome: "internal", reason: "target-not-publicly-exposed", sourceCount: 1, changeField: `http.expose["order.create/v1"]`},
	} {
		t.Run("exposure "+test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(nil))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != inspectProgress || document.ConfigurationMode != test.mode || document.Result.Subject.ID != test.alias || document.Result.Decision.Outcome != test.outcome || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != test.sourceCount || document.Result.Change.Field != test.changeField {
				t.Fatalf("generated exposure explanation = exit %d, stderr %q, mode %q, result %#v", exitCode, stderr, document.ConfigurationMode, document.Result)
			}
			if strings.Contains(stdout, root) || strings.Contains(stdout, "generation-private-marker") {
				t.Fatalf("generated exposure explanation leaked private input: %s", stdout)
			}
		})
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("generated Alias explanations mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainFailuresKeepJSONStdoutEmptyAndDoNotMutate(t *testing.T) {
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
		{name: "unknown canonical Plugin", arguments: []string{"explain", "plugin", "missing.plugin", "--format", "json"}, want: "not visible in the selected application model"},
		{name: "invalid Plugin identity", arguments: []string{"explain", "plugin", "missing", "--format", "json"}, want: "invalid plugin ID"},
		{name: "unknown configuration field", arguments: []string{"explain", "config", "http.missing", "--format", "json"}, want: "is not present in the selected application model"},
		{name: "invalid configuration path", arguments: []string{"explain", "config", "config..host", "--format", "json"}, want: "is not present in the selected application model"},
		{name: "unknown canonical Alias", arguments: []string{"explain", "alias", "missing.operation/v1", "--format", "json"}, want: "is not present in the selected application model"},
		{name: "invalid Alias identity", arguments: []string{"explain", "alias", "mail.send", "--format", "json"}, want: "invalid capability ID"},
		{name: "unknown exposure identity", arguments: []string{"explain", "exposure", "missing.operation/v1", "--format", "json"}, want: "is not visible in the selected application model"},
		{name: "invalid exposure identity", arguments: []string{"explain", "exposure", "mail.send", "--format", "json"}, want: "invalid capability ID"},
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

func TestExplainPluginVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createExplainDependencyPluginProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "plugin", "example.alternative", "--verbose", "--env", "production"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Plugin: example.alternative\n",
		"Decision: selected as a Provider for email.send/v1\n",
		"Reason: provider\n",
		`Change: plystra use email.send/v1 example.shared --env "production"`,
		"Resolution evidence:\n  {\n",
		"    \"plugin_candidates\": [",
		"    \"selected_plugins\": [",
		"    \"provider_candidates\": [",
		"    \"configuration_selection\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose Plugin explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose Plugin explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestExplainAliasVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createExplainCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "alias", "mail.send/v1", "--verbose", "--env", "production"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Alias: mail.send/v1\n",
		"Decision: maps directly to email.send/v1; narrows target exposure to (Go)\n",
		"Reason: application-alias\n",
		`Change: edit plystra.production.yaml at capabilities.aliases["mail.send/v1"]`,
		"Resolution evidence:\n  {\n",
		"    \"capability_aliases\": [",
		"        \"target_contract_digest\": \"sha256:",
		"        \"validation_outcome\": \"valid\"",
		"    \"configuration_selection\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose Alias explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose Alias explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestExplainExposureVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createExplainCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "exposure", "mail.send/v1", "--verbose"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Exposure: mail.send/v1\n",
		"Decision: public Alias to email.send/v1 through HTTP and JavaScript\n",
		"Reason: application-alias\n",
		`Change: edit plystra.yaml at capabilities.aliases["mail.send/v1"]`,
		"Resolution evidence:\n  {\n",
		"    \"public_exposures\": [",
		"        \"contract_digest\": \"sha256:",
		"        \"canonical_target\": \"email.send/v1\"",
		"    \"configuration_selection\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose exposure explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose exposure explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func createExplainCommandProject(t *testing.T) (string, string) {
	t.Helper()
	root := writeProviderCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\n  aliases: {mail.send/v1: email.send/v1}\nhttp:\n  address: resolved-secret-marker\n  expose: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.local}\n  aliases:\n    mail.send/v1: {target: email.send/v1, expose: {go: true, http: false, javascript: false}}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.ignored.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.smtp}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.local}\n  aliases: {mail.send/v1: email.send/v1}\nhttp:\n  expose: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "ignored.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
	nested := filepath.Join(root, "smtp", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func createExplainGenerationAliasProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI repository root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/alias-explain

go 1.26

require (
	github.com/plystra/cli v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/cli => %q

replace github.com/plystra/kernel => %q
`, filepath.ToSlash(cliRoot), filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [order.create/v1]
  aliases:
    orders.submit/v1: order.create/v1
http:
  address: generation-private-marker
  expose: [order.create/v1]
`)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {expose: {remove: [order.create/v1]}}\n")
	writeCommandFile(t, filepath.Join(root, "business", "plugin.yaml"), "id: example.business\nprovides: [order.create/v1]\n")
	writeCommandFile(t, filepath.Join(root, "business", "capabilities", "order.create", "v1", "capability.yaml"), `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCommandFile(t, filepath.Join(root, "authn", "plugin.yaml"), `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writeCommandFile(t, filepath.Join(root, "authn", "capabilities", "authn.session.verify", "v1", "capability.yaml"), "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "generate.go"), explainAliasExtensionSource)
	nested := filepath.Join(root, "business", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func createExplainDependencyPluginProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	platformRoot := filepath.Join(root, "platform")
	kernelRoot := filepath.Join(root, "kernel")
	writeCommandFile(t, filepath.Join(kernelRoot, "go.mod"), "module github.com/plystra/kernel\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(kernelRoot, "configuration", "secret.go"), "package configuration\n\ntype Secret struct{}\n")
	writeCommandFile(t, filepath.Join(platformRoot, "go.mod"), fmt.Sprintf(`module example.com/platform

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	writeCommandFile(t, filepath.Join(platformRoot, "plystra.yaml"), `interfaces:
  use: {email.send/v1: example.com/platform/shared.New}
config:
  example.com/platform/shared.New:
    host: dependency-private.example
    password: {env: EXPLAIN_PRIVATE_PASSWORD}
    settings:
      nested: dependency-private
`)
	writeCommandFile(t, filepath.Join(platformRoot, "interfaces", "email", "send", "v1", "interface.go"), `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`)
	writeCommandFile(t, filepath.Join(platformRoot, "shared", "configuration.go"), `package shared

import (
	"context"

	sendv1 "example.com/platform/interfaces/email/send/v1"
	"github.com/plystra/kernel/configuration"
)

type Config struct {
	Host string
	Password configuration.Secret
	Settings struct {
		Nested string
	}
}

type Service struct{}

//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)

	writeCommandFile(t, filepath.Join(platformRoot, "shared", "plugin.yaml"), `id: example.shared
provides: [email.send/v1, reports.read/v1]
config:
  host: {type: string}
  password: {type: secret}
  settings: {type: object}
`)
	writeCommandFile(t, filepath.Join(platformRoot, "shared", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(platformRoot, "shared", "capabilities", "reports.read", "v1", "capability.yaml"), "id: reports.read/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(platformRoot, "alternative", "plugin.yaml"), "id: example.alternative\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(platformRoot, "alternative", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(platformRoot, "optional", "plugin.yaml"), "id: example.optional\nprovides: [audit.record/v1]\n")
	writeCommandFile(t, filepath.Join(platformRoot, "optional", "capabilities", "audit.record", "v1", "capability.yaml"), "id: audit.record/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeCommandFile(t, filepath.Join(appRoot, "go.mod"), fmt.Sprintf(`module example.com/app

go 1.26

require (
	example.com/platform v1.0.0
	github.com/plystra/kernel v0.0.0
)

replace example.com/platform => %s
replace github.com/plystra/kernel => %s
`, filepath.ToSlash(platformRoot), filepath.ToSlash(kernelRoot)))
	writeCommandFile(t, filepath.Join(appRoot, "plystra.yaml"), `capabilities:
  require: [email.send/v1, reports.read/v1]
  use: {email.send/v1: example.shared}
http:
  address: resolved-secret-marker
config:
  example.com/platform/shared.New:
    host: root-private.example
`)
	writeCommandFile(t, filepath.Join(appRoot, "plystra.production.yaml"), `capabilities:
  use: {email.send/v1: example.alternative}
config:
  example.com/platform/shared.New:
    host: production-private.example
    password: null
`)
	writeCommandFile(t, filepath.Join(appRoot, "plystra.suppressed.yaml"), `config:
  example.com/platform/shared.New: null
`)
	writeCommandFile(t, filepath.Join(appRoot, "deploy", "customer.yaml"), `capabilities:
  require: [email.send/v1, reports.read/v1]
  use: {email.send/v1: example.alternative}
config:
  example.com/platform/shared.New:
    host: customer-private.example
    password: {env: CUSTOMER_PRIVATE_PASSWORD}
`)
	nested := filepath.Join(appRoot, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return appRoot, nested
}

func decodeExplainCommandEnvelope(t testing.TB, output string) explainCommandEnvelope {
	t.Helper()
	var result explainCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode explain JSON: %v\n%s", err, output)
	}
	return result
}

func assertConfigurationExplanationRedacted(t testing.TB, output, root string) {
	t.Helper()
	for _, forbidden := range []string{
		root,
		"dependency-private",
		"root-private",
		"production-private",
		"customer-private",
		"EXPLAIN_PRIVATE_PASSWORD",
		"CUSTOMER_PRIVATE_PASSWORD",
		"resolved-secret-marker",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("configuration explanation exposed %q:\n%s", forbidden, output)
		}
	}
}

const explainAliasExtensionSource = `package extension

import generation "github.com/plystra/cli/generation/v1"

func Generate(context generation.GenerationContext) (generation.Output, error) {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	start, _ := generation.ParseCapabilityID("orders.start/v1")
	submit, _ := generation.ParseCapabilityID("orders.submit/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{AliasContributions: []generation.CapabilityAliasContribution{
		{ID: "authn.order-start", Namespace: "authn", Source: order, Alias: start, Target: order},
		{ID: "authn.order-submit", Namespace: "authn", Source: order, Alias: submit, Target: order},
	}}, nil
}
`
