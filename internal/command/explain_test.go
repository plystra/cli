package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/testkernel"
)

type explainCommandSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type explainCommandResult struct {
	Subject struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"subject"`
	Decision struct {
		Outcome string `json:"outcome"`
	} `json:"decision"`
	Reason struct {
		Code    string                 `json:"code"`
		Sources []explainCommandSource `json:"sources"`
	} `json:"reason"`
	Change struct {
		Kind    string   `json:"kind"`
		Module  string   `json:"module"`
		Path    string   `json:"path"`
		Field   string   `json:"field"`
		Command string   `json:"command"`
		Argv    []string `json:"argv"`
	} `json:"change"`
	ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
}

type explainCommandPayload struct {
	Schema                 string                 `json:"schema"`
	ConfigurationMode      string                 `json:"configuration_mode"`
	ApplicationModelDigest string                 `json:"application_model_digest"`
	Sources                []explainCommandSource `json:"sources"`
	Result                 explainCommandResult   `json:"result"`
}

type explainCommandEnvelope struct {
	Schema       string `json:"schema"`
	Operation    string `json:"operation"`
	InvocationID string `json:"invocation_id"`
	Snapshot     *struct {
		Selector struct {
			Mode string `json:"mode"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"selector"`
	} `json:"snapshot"`
	Status      string            `json:"status"`
	ExitClass   int               `json:"exit_class"`
	Changes     []json.RawMessage `json:"changes"`
	Diagnostics []struct {
		Code      string                 `json:"code"`
		Severity  string                 `json:"severity"`
		Message   string                 `json:"message"`
		Locations []explainCommandSource `json:"locations"`
	} `json:"diagnostics"`
	Recovery []struct {
		Schema string `json:"schema"`
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"target"`
		Owner *struct {
			Module string `json:"module"`
			Path   string `json:"path"`
		} `json:"owner"`
		Provenance []explainCommandSource `json:"provenance"`
		Selector   *struct {
			Mode string `json:"mode"`
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"selector"`
		Preconditions []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
			Count int    `json:"count"`
		} `json:"preconditions"`
		Effects []struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
			Count int    `json:"count"`
		} `json:"effects"`
		Verification struct {
			WorkingDirectory string   `json:"working_directory"`
			Argv             []string `json:"argv"`
		} `json:"verification"`
		WorkingDirectory string   `json:"working_directory"`
		Argv             []string `json:"argv"`
		Options          []struct {
			Value            string   `json:"value"`
			WorkingDirectory string   `json:"working_directory"`
			Argv             []string `json:"argv"`
		} `json:"options"`
	} `json:"recovery"`
	Effects struct {
		Observed   []json.RawMessage `json:"observed"`
		Planned    []json.RawMessage `json:"planned"`
		Skipped    []json.RawMessage `json:"skipped"`
		Unverified []json.RawMessage `json:"unverified"`
	} `json:"effects"`
	Support      []json.RawMessage      `json:"support"`
	Payload      *explainCommandPayload `json:"payload"`
	Continuation json.RawMessage        `json:"continuation"`

	ConfigurationMode      string               `json:"-"`
	ApplicationModelDigest string               `json:"-"`
	Result                 explainCommandResult `json:"-"`
}

func TestExplainCapabilityHumanOutputIsConciseCausalAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "email.send/v1"}, nested, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Capability: email.send/v1\n",
		"Decision: required; Provider acme.email.smtp is selected\n",
		"Reason: current-project-replacement\n",
		"Source: example.com/acme/provider-use:plystra.yaml:",
		"(provider-selection)\n",
		`Change: edit plystra.yaml at capabilities.use["email.send/v1"]`,
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
	if exitCode != 0 || stderr != inspectProgress {
		t.Fatalf("capability explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("capability explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestExplainCapabilityJSONOwnsStdoutAndIsDeterministic(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != "" || secondStderr != "" {
		t.Fatalf("JSON explanation = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if !bytes.Equal(canonicalExplainCommandResult(t, firstStdout), canonicalExplainCommandResult(t, secondStdout)) || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
		t.Fatalf("JSON stdout is not one deterministic document:\nfirst:  %q\nsecond: %q", firstStdout, secondStdout)
	}
	document := decodeExplainCommandEnvelope(t, firstStdout)
	if document.Schema != commandschema.ResultSchemaV1 || document.Operation != "explain.capability" || document.InvocationID == "" || document.Status != "success" || document.ExitClass != 0 || document.Snapshot.Selector.Mode != "default" || document.Payload == nil || document.Payload.Schema != "plystra.explain/v1" || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("explain envelope identity = %#v", document)
	}
	if document.Changes == nil || len(document.Changes) != 0 || document.Diagnostics == nil || len(document.Diagnostics) != 0 || document.Support == nil || len(document.Support) != 0 || string(document.Continuation) != "null" {
		t.Fatalf("explain result collections = changes %#v diagnostics %#v support %#v continuation %s", document.Changes, document.Diagnostics, document.Support, document.Continuation)
	}
	assertExplainCommandEmptyEffects(t, document)
	if document.Result.Subject.Kind != "capability" || document.Result.Subject.ID != "email.send/v1" || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "current-project-replacement" {
		t.Fatalf("capability decision = %#v", document.Result)
	}
	if len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Module != "example.com/acme/provider-use" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[0].Kind != "provider-selection" {
		t.Fatalf("capability reason sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Command != "" || len(document.Result.Change.Argv) != 0 || document.Result.Change.Module != "example.com/acme/provider-use" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("capability change/evidence = %#v, %s", document.Result.Change, document.Result.ResolutionEvidence)
	}
	if len(document.Recovery) != 1 || document.Recovery[0].Schema != commandschema.RecoverySchemaV1 || document.Recovery[0].ID != "change-explained-decision" || document.Recovery[0].Kind != "edit_source" || document.Recovery[0].Target.Kind != "configuration_field" || document.Recovery[0].Target.ID != `capabilities.use["email.send/v1"]` || document.Recovery[0].Owner == nil || document.Recovery[0].Owner.Module != "example.com/acme/provider-use" || document.Recovery[0].Owner.Path != "plystra.yaml" || document.Recovery[0].Selector == nil || document.Recovery[0].Selector.Mode != "default" || document.Recovery[0].WorkingDirectory != "" || len(document.Recovery[0].Argv) != 0 || document.Recovery[0].Verification.WorkingDirectory != "." || !reflect.DeepEqual(document.Recovery[0].Verification.Argv, []string{"plystra", "explain", "capability", "email.send/v1", "--format", "json"}) {
		t.Fatalf("capability recovery = %#v", document.Recovery)
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("capability JSON leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("capability JSON explanation mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
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
	if exitCode != 0 || stderr != inspectProgress {
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
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
			if firstExit != 0 || secondExit != 0 || firstStderr != "" || secondStderr != "" || !bytes.Equal(canonicalExplainCommandResult(t, firstStdout), canonicalExplainCommandResult(t, secondStdout)) {
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
	if exitCode != 0 || stderr != inspectProgress {
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
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
			if firstExit != 0 || secondExit != 0 || firstStderr != "" || secondStderr != "" || !bytes.Equal(canonicalExplainCommandResult(t, firstStdout), canonicalExplainCommandResult(t, secondStdout)) {
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
	if exitCode != 0 || stderr != inspectProgress {
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
	if firstExit != 0 || secondExit != 0 || firstStderr != "" || secondStderr != "" || !bytes.Equal(canonicalExplainCommandResult(t, firstStdout), canonicalExplainCommandResult(t, secondStdout)) {
		t.Fatalf("selected Plugin JSON = first (%d, %q) second (%d, %q)\nfirst: %s\nsecond: %s", firstExit, firstStderr, secondExit, secondStderr, firstStdout, secondStdout)
	}
	document := decodeExplainCommandEnvelope(t, firstStdout)
	if document.Result.Subject.Kind != "plugin" || document.Result.Subject.ID != "example.shared" || document.Result.Decision.Outcome != "selected" || document.Result.Reason.Code != "provider" {
		t.Fatalf("selected Plugin decision = %#v", document.Result)
	}
	if len(document.Result.Reason.Sources) != 2 || document.Result.Reason.Sources[0].Module != "example.com/app" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Reason.Sources[1].Module != "example.com/platform" || document.Result.Reason.Sources[1].Path != "shared/capabilities/reports.read/v1/capability.yaml" {
		t.Fatalf("selected Plugin sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("selected Plugin change/output = %#v\n%s", document.Result.Change, firstStdout)
	}

	exitCode, stdout, stderr := runCommand(t, []string{"explain", "plugin", "example.alternative", "--format", "json"}, nested, environment)
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != "" || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "another-provider-selected" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != "alternative/capabilities/email.send/v1/capability.yaml" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["email.send/v1"]` {
		t.Fatalf("unselected alternative Plugin = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "plugin", "example.optional", "--format", "json"}, nested, environment)
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != "" || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "capability-not-required" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != "optional/capabilities/audit.record/v1/capability.yaml" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.require["audit.record/v1"]` {
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
	}{
		{name: "explicit environment", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json", "--env", "production"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "environment", path: "plystra.production.yaml"},
		{name: "ambient environment", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production"}, mode: "environment", path: "plystra.production.yaml"},
		{name: "explicit configuration", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json", "--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml"},
		{name: "ambient configuration", arguments: []string{"explain", "plugin", "example.alternative", "--format", "json"}, environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, mode: "explicit-config", path: "deploy/customer.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			document := decodeExplainCommandEnvelope(t, stdout)
			if exitCode != 0 || stderr != "" || document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "selected" || document.Result.Reason.Code != "provider" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Kind != "file" || document.Result.Change.Path != test.path || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || document.Result.Change.Command != "" || len(document.Result.Change.Argv) != 0 {
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
			reason:       "adopted-export",
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
			if exitCode != 0 || stderr != "" || document.ConfigurationMode != test.mode {
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
	if firstExit != 0 || secondExit != 0 || firstStderr != "" || secondStderr != "" {
		t.Fatalf("configuration JSON = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if !bytes.Equal(canonicalExplainCommandResult(t, firstStdout), canonicalExplainCommandResult(t, secondStdout)) || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
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
	if exitCode != 0 || stderr != "" || document.ConfigurationMode != "environment" || document.Result.Decision.Outcome != "suppressed" || document.Result.Reason.Code != "ancestor-removal" {
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
			if exitCode != 0 || stderr != "" || document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "effective" || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Path != test.path {
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
		"Decision: effective redacted from adopted-export\n",
		"Reason: adopted-export\n",
		"Source: example.com/platform:plystra.yaml:1:1 (configuration-value)\n",
		`Change: edit plystra.yaml at config["example.com/platform/shared.New"]["password"]`,
		"Resolution evidence:\n  {\n",
		`    "configuration_fields": [`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("configuration explanation omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != inspectProgress {
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
	}{
		{
			name:        "explicit environment",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "production"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "environment",
			path:        "plystra.production.yaml",
		},
		{
			name:        "ambient environment",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json"},
			environment: map[string]string{"PLYSTRA_ENV": "production"},
			mode:        "environment",
			path:        "plystra.production.yaml",
		},
		{
			name:        "explicit configuration",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json", "--config", "deploy/customer.yaml"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
		},
		{
			name:        "ambient configuration",
			arguments:   []string{"explain", "capability", "email.send/v1", "--format", "json"},
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 0 || stderr != "" {
				t.Fatalf("explain = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeExplainCommandEnvelope(t, stdout)
			if document.ConfigurationMode != test.mode || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "current-project-replacement" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Path != test.path || document.Result.Change.Kind != "file" || document.Result.Change.Path != test.path || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || document.Result.Change.Command != "" || len(document.Result.Change.Argv) != 0 {
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
		if exitCode != 0 || stderr != "" || document.Result.Decision.Outcome != "available" || document.Result.Reason.Code != "capability-not-required" || len(document.Result.Reason.Sources) != 1 || document.Result.Reason.Sources[0].Kind != "configuration-selection" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.require["reports.read/v1"]` {
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
		if exitCode != 0 || stderr != inspectProgress {
			t.Fatalf("sole-Provider explanation = exit %d, stderr %q", exitCode, stderr)
		}
	})

	t.Run("required Kernel intrinsic", func(t *testing.T) {
		root, nested := createExplainCommandProject(t)
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1, kernel.health/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
		exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "kernel.health/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
		document := decodeExplainCommandEnvelope(t, stdout)
		if exitCode != 0 || stderr != "" || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "intrinsic-kernel" || len(document.Result.Reason.Sources) != 1 || document.Result.Change.Kind != "file" || document.Result.Change.Field != `capabilities.require["kernel.health/v1"]` {
			t.Fatalf("intrinsic explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
		}
	})
}

func TestExplainCapabilityIgnoresDependencyTopLevelApplicationConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	aRoot := filepath.Join(root, "a")
	bRoot := filepath.Join(root, "b")
	writeCommandFile(t, filepath.Join(aRoot, "go.mod"), "module example.com/a\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(bRoot, "go.mod"), "module example.com/b\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(aRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}, aliases: {mail.send/v1: email.send/v1}}\nhttp: {expose: {email.send/v1: {transport: connect}}}\n")
	writeCommandFile(t, filepath.Join(bRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}, aliases: {mail.send/v1: email.send/v1}}\nhttp: {expose: {email.send/v1: {transport: connect}}}\n")
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

	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "email.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document := decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != "" || document.Result.Decision.Outcome != "required" || document.Result.Reason.Code != "sole-provider" || len(document.Result.Reason.Sources) != 1 {
		t.Fatalf("dependency-inert explanation = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Reason.Sources[0].Module != "example.com/a" || document.Result.Reason.Sources[0].Path != "smtp/capabilities/email.send/v1/capability.yaml" || document.Result.Reason.Sources[0].Kind != "provider-declaration" {
		t.Fatalf("dependency-inert explanation sources = %#v", document.Result.Reason.Sources)
	}
	if document.Result.Change.Kind != "file" || document.Result.Change.Module != "example.com/app" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `capabilities.use["email.send/v1"]` || strings.Contains(stdout, root) {
		t.Fatalf("dependency-inert explanation change = %#v", document.Result.Change)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "alias", "mail.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 3 || stderr != "" || document.Status != "validation_failed" || document.ExitClass != 3 || document.Payload != nil || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.ExplainTargetNotFound || len(document.Recovery) != 1 || document.Recovery[0].ID != "select-visible-explain-target" || document.Recovery[0].Target.Kind != "alias" || document.Recovery[0].Target.ID != "mail.send/v1" {
		t.Fatalf("dependency Alias was not inert = exit %d, stderr %q, result %#v", exitCode, stderr, document)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"explain", "exposure", "email.send/v1", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	document = decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 0 || stderr != "" || document.Result.Subject.Kind != "exposure" || document.Result.Decision.Outcome != "internal" || document.Result.Reason.Code != "not-publicly-exposed" || len(document.Result.Reason.Sources) != 1 {
		t.Fatalf("dependency exposure was not inert = exit %d, stderr %q, result %#v", exitCode, stderr, document.Result)
	}
	if document.Result.Reason.Sources[0].Module != "example.com/app" || document.Result.Reason.Sources[0].Path != "plystra.yaml" || document.Result.Change.Path != "plystra.yaml" || document.Result.Change.Field != `http.expose["email.send/v1"]` {
		t.Fatalf("dependency-inert exposure sources or change = sources %#v, change %#v", document.Result.Reason.Sources, document.Result.Change)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("dependency-inert explanations mutated the fixture:\nbefore: %#v\nafter:  %#v", before, after)
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
			if exitCode != 0 || stderr != "" || document.Result.Subject.Kind != "alias" || document.Result.Subject.ID != test.alias || document.Result.Decision.Outcome != "valid" || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != len(test.sourceKinds) {
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
	if exitCode != 0 || stderr != "" || document.ConfigurationMode != "environment" || document.Result.Reason.Code != "generation-extension-alias" || document.Result.Change.Kind != "file" || document.Result.Change.Path != "plystra.production.yaml" || document.Result.Change.Field != `capabilities.use["authn.session.verify/v1"]` {
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
			if exitCode != 0 || stderr != "" || document.ConfigurationMode != test.mode || document.Result.Subject.ID != test.alias || document.Result.Decision.Outcome != test.outcome || document.Result.Reason.Code != test.reason || len(document.Result.Reason.Sources) != test.sourceCount || document.Result.Change.Field != test.changeField {
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

func TestExplainFailuresReturnCanonicalResultsAndDoNotMutate(t *testing.T) {
	t.Parallel()

	root, nested := createExplainCommandProject(t)
	before := snapshotInspectProject(t, root)
	tests := []struct {
		name             string
		arguments        []string
		environment      map[string]string
		operation        string
		exitClass        int
		status           string
		code             string
		snapshot         bool
		recoveryID       string
		targetKind       string
		targetID         string
		selectorMode     string
		locationPath     string
		verificationArgv []string
	}{
		{name: "unknown canonical Capability", arguments: []string{"explain", "capability", "missing.operation/v1", "--format", "json"}, operation: "explain.capability", exitClass: 3, status: "validation_failed", code: diagnosticcode.ExplainTargetNotFound, snapshot: true, recoveryID: "select-visible-explain-target", targetKind: "capability", targetID: "missing.operation/v1", selectorMode: "default", verificationArgv: []string{"plystra", "explain", "capability", "missing.operation/v1", "--format", "json"}},
		{name: "invalid Capability identity", arguments: []string{"explain", "capability", "email.send", "--format", "json"}, operation: "explain.capability", exitClass: 2, status: "invalid_invocation", code: diagnosticcode.ExplainSubjectInvalid, recoveryID: "correct-explain-subject", targetKind: "subject", targetID: "explain.capability", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "unknown canonical Plugin", arguments: []string{"explain", "plugin", "missing.plugin", "--format", "json"}, operation: "explain.plugin", exitClass: 3, status: "validation_failed", code: diagnosticcode.ExplainTargetNotFound, snapshot: true, recoveryID: "select-visible-explain-target", targetKind: "plugin", targetID: "missing.plugin", selectorMode: "default", verificationArgv: []string{"plystra", "explain", "plugin", "missing.plugin", "--format", "json"}},
		{name: "invalid Plugin identity", arguments: []string{"explain", "plugin", "missing", "--format", "json"}, operation: "explain.plugin", exitClass: 2, status: "invalid_invocation", code: diagnosticcode.ExplainSubjectInvalid, recoveryID: "correct-explain-subject", targetKind: "subject", targetID: "explain.plugin", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "unknown configuration field", arguments: []string{"explain", "config", "http.missing", "--format", "json"}, operation: "explain.config", exitClass: 3, status: "validation_failed", code: diagnosticcode.ExplainTargetNotFound, snapshot: true, recoveryID: "select-visible-explain-target", targetKind: "configuration", targetID: "http.missing", selectorMode: "default", verificationArgv: []string{"plystra", "explain", "config", "http.missing", "--format", "json"}},
		{name: "invalid configuration path", arguments: []string{"explain", "config", "config..host", "--format", "json"}, operation: "explain.config", exitClass: 2, status: "invalid_invocation", code: diagnosticcode.ExplainSubjectInvalid, recoveryID: "correct-explain-subject", targetKind: "subject", targetID: "explain.config", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "unknown canonical Alias", arguments: []string{"explain", "alias", "missing.operation/v1", "--format", "json"}, operation: "explain.alias", exitClass: 3, status: "validation_failed", code: diagnosticcode.ExplainTargetNotFound, snapshot: true, recoveryID: "select-visible-explain-target", targetKind: "alias", targetID: "missing.operation/v1", selectorMode: "default", verificationArgv: []string{"plystra", "explain", "alias", "missing.operation/v1", "--format", "json"}},
		{name: "invalid Alias identity", arguments: []string{"explain", "alias", "mail.send", "--format", "json"}, operation: "explain.alias", exitClass: 2, status: "invalid_invocation", code: diagnosticcode.ExplainSubjectInvalid, recoveryID: "correct-explain-subject", targetKind: "subject", targetID: "explain.alias", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "unknown exposure identity", arguments: []string{"explain", "exposure", "missing.operation/v1", "--format", "json"}, operation: "explain.exposure", exitClass: 3, status: "validation_failed", code: diagnosticcode.ExplainTargetNotFound, snapshot: true, recoveryID: "select-visible-explain-target", targetKind: "exposure", targetID: "missing.operation/v1", selectorMode: "default", verificationArgv: []string{"plystra", "explain", "exposure", "missing.operation/v1", "--format", "json"}},
		{name: "invalid exposure identity", arguments: []string{"explain", "exposure", "mail.send", "--format", "json"}, operation: "explain.exposure", exitClass: 2, status: "invalid_invocation", code: diagnosticcode.ExplainSubjectInvalid, recoveryID: "correct-explain-subject", targetKind: "subject", targetID: "explain.exposure", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "missing overlay", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "missing"}, operation: "explain.capability", exitClass: 3, status: "validation_failed", code: diagnosticcode.ConfigurationSelectionInvalid, recoveryID: "select-explain-configuration", targetKind: "configuration_selector", targetID: "explain.capability", selectorMode: "environment", locationPath: "plystra.missing.yaml", verificationArgv: []string{"plystra", "explain", "capability", "email.send/v1", "--format", "json", "--env", "missing"}},
		{name: "unsafe environment", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--env", "../test"}, operation: "explain.capability", exitClass: 3, status: "validation_failed", code: diagnosticcode.ConfigurationSelectionInvalid, recoveryID: "select-explain-configuration", targetKind: "configuration_selector", targetID: "explain.capability", verificationArgv: []string{"plystra", "explain", "--help"}},
		{name: "ambient conflict", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "deploy/customer.yaml"}, operation: "explain.capability", exitClass: 3, status: "validation_failed", code: diagnosticcode.ConfigurationSelectionInvalid, recoveryID: "select-explain-configuration", targetKind: "configuration_selector", targetID: "explain.capability", verificationArgv: []string{"plystra", "explain", "--help"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != test.exitClass || stderr != "" || !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
				t.Fatalf("explain failure = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeExplainCommandEnvelope(t, stdout)
			if document.Schema != commandschema.ResultSchemaV1 || document.Operation != test.operation || document.InvocationID == "" || document.Status != test.status || document.ExitClass != test.exitClass || document.Payload != nil || (document.Snapshot != nil) != test.snapshot {
				t.Fatalf("explain failure identity = %#v", document)
			}
			if document.Changes == nil || len(document.Changes) != 0 || document.Support == nil || len(document.Support) != 0 || string(document.Continuation) != "null" {
				t.Fatalf("explain failure collections = changes %#v support %#v continuation %s", document.Changes, document.Support, document.Continuation)
			}
			if len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != test.code || document.Diagnostics[0].Severity != "error" || document.Diagnostics[0].Message == "" {
				t.Fatalf("explain failure diagnostics = %#v", document.Diagnostics)
			}
			if test.locationPath == "" {
				if len(document.Diagnostics[0].Locations) != 0 {
					t.Fatalf("explain failure locations = %#v, want empty", document.Diagnostics[0].Locations)
				}
			} else if len(document.Diagnostics[0].Locations) != 1 || document.Diagnostics[0].Locations[0].Path != test.locationPath {
				t.Fatalf("explain failure locations = %#v, want path %q", document.Diagnostics[0].Locations, test.locationPath)
			}
			if len(document.Recovery) != 1 || document.Recovery[0].Schema != commandschema.RecoverySchemaV1 || document.Recovery[0].ID != test.recoveryID || document.Recovery[0].Kind != "manual" || document.Recovery[0].Target.Kind != test.targetKind || document.Recovery[0].Target.ID != test.targetID || !reflect.DeepEqual(document.Recovery[0].Verification.Argv, test.verificationArgv) {
				t.Fatalf("explain failure recovery = %#v", document.Recovery)
			}
			if test.selectorMode == "" {
				if document.Recovery[0].Selector != nil {
					t.Fatalf("explain failure selector = %#v, want null", document.Recovery[0].Selector)
				}
			} else if document.Recovery[0].Selector == nil || document.Recovery[0].Selector.Mode != test.selectorMode {
				t.Fatalf("explain failure selector = %#v, want mode %q", document.Recovery[0].Selector, test.selectorMode)
			}
			assertExplainCommandEmptyEffects(t, document)
			assertConfigurationExplanationRedacted(t, stdout, root)
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
		`Change: edit plystra.production.yaml at capabilities.use["email.send/v1"]`,
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
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
		`Change: edit plystra.production.yaml at capabilities.use["email.send/v1"]`,
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
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
	if exitCode != 0 || stderr != inspectProgress || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose exposure explanation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestExplainImplementationRecoveryCanBeAppliedThroughPublicCommands(t *testing.T) {
	t.Parallel()
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [email.send/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	environment := implementationSelectionCommandEnvironment(map[string]string{"PLYSTRA_ENV": "production"})
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"explain", "capability", "kernel.health/v1", "--format", "json"}, filepath.Join(root, "smtp"), environment)
	document := decodeExplainCommandEnvelope(t, stdout)
	if exitCode != 4 || stderr != "" || document.Status != "decision_required" || document.ExitClass != 4 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.ResolveMultipleImplementations || len(document.Recovery) != 1 {
		t.Fatalf("Implementation ambiguity = exit %d stdout %s stderr %q", exitCode, stdout, stderr)
	}
	action := document.Recovery[0]
	if action.Kind != "choose" || action.ID != "choose-interface-implementation" || action.Target.ID != "email.send/v1" || action.Selector == nil || action.Selector.Name != "production" || len(action.Options) != 2 {
		t.Fatalf("Implementation recovery = %#v", action)
	}
	for index, name := range []string{"local", "smtp"} {
		value := "example.com/acme/implementation-use/" + name + ".New"
		option := action.Options[index]
		if option.Value != value || option.WorkingDirectory != "." || !reflect.DeepEqual(option.Argv, []string{"plystra", "use", "email.send/v1", value, "--env", "production"}) {
			t.Fatalf("Implementation option = %#v", option)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed explanation mutated Project")
	}
	exitCode, stdout, stderr = runCommand(t, action.Options[0].Argv[1:], root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("apply recovery = %d %s %s", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runCommand(t, action.Verification.Argv[1:], root, environment)
	if exitCode != 0 || stderr != "" || decodeExplainCommandEnvelope(t, stdout).Status != "success" {
		t.Fatalf("verify recovery = %d %s %s", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("independent generated check = %d %s %s", exitCode, stdout, stderr)
	}
}

func createExplainCommandProject(t *testing.T) (string, string) {
	t.Helper()
	root := writeProviderCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\n  aliases: {mail.send/v1: email.send/v1}\nhttp:\n  address: resolved-secret-marker\n  expose: {email.send/v1: {transport: connect}}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.local}\n  aliases:\n    mail.send/v1: {target: email.send/v1, expose: {go: true, http: false, javascript: false}}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.ignored.yaml"), "capabilities:\n  use: {email.send/v1: acme.email.smtp}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.local}\n  aliases: {mail.send/v1: email.send/v1}\nhttp:\n  expose: {email.send/v1: {transport: connect}}\n")
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
	kernelRoot := testkernel.Root(t)
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/alias-explain

go 1.26

require (
	github.com/plystra/cli v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.5 // indirect
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
  expose: {order.create/v1: {transport: connect}}
`)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {expose: {order.create/v1: null}}\n")
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
	writeCommandFile(t, filepath.Join(platformRoot, "plystra.yaml"), `composition:
  exports:
    defaults:
      interfaces:
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
	writeCommandFile(t, filepath.Join(appRoot, "plystra.yaml"), `composition:
  adopt:
    - module: example.com/platform
      export: defaults
capabilities:
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
	writeCommandFile(t, filepath.Join(appRoot, "deploy", "customer.yaml"), `composition:
  adopt:
    - module: example.com/platform
      export: defaults
capabilities:
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
	if result.Payload != nil {
		result.ConfigurationMode = result.Payload.ConfigurationMode
		result.ApplicationModelDigest = result.Payload.ApplicationModelDigest
		result.Result = result.Payload.Result
	}
	return result
}

func canonicalExplainCommandResult(t testing.TB, output string) []byte {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode explain command result: %v\n%s", err, output)
	}
	if _, exists := document["invocation_id"]; !exists {
		t.Fatalf("explain command result omits invocation_id: %s", output)
	}
	delete(document, "invocation_id")
	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("normalize explain command result: %v", err)
	}
	return canonical
}

func assertExplainCommandEmptyEffects(t testing.TB, document explainCommandEnvelope) {
	t.Helper()
	if document.Effects.Observed == nil || document.Effects.Planned == nil || document.Effects.Skipped == nil || document.Effects.Unverified == nil || len(document.Effects.Observed) != 0 || len(document.Effects.Planned) != 0 || len(document.Effects.Skipped) != 0 || len(document.Effects.Unverified) != 0 {
		t.Fatalf("explain command effects = %#v, want four empty dispositions", document.Effects)
	}
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
