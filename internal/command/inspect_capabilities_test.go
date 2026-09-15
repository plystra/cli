package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/installedcapabilities"
)

const inspectCapabilitiesTestInvocationID = "123e4567-e89b-42d3-a456-426614174000"

type inspectCapabilitiesResultDocument struct {
	Schema       string `json:"schema"`
	Operation    string `json:"operation"`
	InvocationID string `json:"invocation_id"`
	Status       string `json:"status"`
	ExitClass    int    `json:"exit_class"`
	Diagnostics  []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Recovery []struct {
		Schema string `json:"schema"`
		Kind   string `json:"kind"`
		Target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"target"`
		Verification struct {
			WorkingDirectory string   `json:"working_directory"`
			Argv             []string `json:"argv"`
		} `json:"verification"`
	} `json:"recovery"`
	Effects struct {
		Observed   []json.RawMessage `json:"observed"`
		Planned    []json.RawMessage `json:"planned"`
		Skipped    []json.RawMessage `json:"skipped"`
		Unverified []json.RawMessage `json:"unverified"`
	} `json:"effects"`
	Payload json.RawMessage `json:"payload"`
}

type inspectCapabilitiesErrorWriter struct {
	err error
}

func (w inspectCapabilitiesErrorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestParseInspectCapabilitiesArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		format    commandFormat
		ok        bool
	}{
		{arguments: []string{"inspect", "capabilities"}, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "capabilities", "--format", "human"}, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "capabilities", "--format", "json"}, format: commandFormatJSON, ok: true},
		{arguments: nil},
		{arguments: []string{"inspect"}},
		{arguments: []string{"inspect", "modules"}},
		{arguments: []string{"inspect", "capabilities", "--verbose"}},
		{arguments: []string{"inspect", "capabilities", "--env", "production"}},
		{arguments: []string{"inspect", "capabilities", "--config", "deploy.yaml"}},
		{arguments: []string{"inspect", "capabilities", "--format"}},
		{arguments: []string{"inspect", "capabilities", "--format", "yaml"}},
		{arguments: []string{"inspect", "capabilities", "--format", "json", "--format", "human"}},
	}
	for _, test := range tests {
		result, ok := parseInspectCapabilitiesArguments(test.arguments)
		if result.format != test.format || ok != test.ok {
			t.Errorf("parseInspectCapabilitiesArguments(%q) = %#v, %t; want format %q, ok %t", test.arguments, result, ok, test.format, test.ok)
		}
	}
}

func TestRunInspectCapabilitiesEmitsCanonicalJSONSuccess(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr := runInspectCapabilitiesForTest(t, []string{"inspect", "capabilities", "--format", "json"}, installedcapabilities.Current)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("runInspectCapabilities = exit %d, stderr %q", exitCode, stderr)
	}
	document := decodeInspectCapabilitiesResult(t, stdout)
	if document.Schema != commandschema.ResultSchemaV1 || document.Operation != "inspect.capabilities" || document.InvocationID != inspectCapabilitiesTestInvocationID || document.Status != "success" || document.ExitClass != 0 {
		t.Fatalf("result identity = %#v", document)
	}
	capabilities, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("installedcapabilities.Current: %v", err)
	}
	if !bytes.Equal(document.Payload, capabilities.CanonicalJSON()) {
		t.Fatalf("payload = %s\nwant = %s", document.Payload, capabilities.CanonicalJSON())
	}
	if len(document.Diagnostics) != 0 || len(document.Recovery) != 0 || len(document.Effects.Observed) != 0 || len(document.Effects.Planned) != 0 || len(document.Effects.Skipped) != 0 || len(document.Effects.Unverified) != 0 {
		t.Fatalf("read-only result contains failure or effect facts: %#v", document)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("stdout is not one compact canonical document: %q", stdout)
	}
}

func TestRunInspectCapabilitiesIgnoresProjectAndAmbientSelectors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plystra.yaml"), []byte("root: [invalid\n"), 0o644); err != nil {
		t.Fatalf("write invalid Project marker: %v", err)
	}
	environment := []string{"PLYSTRA_ENV=production", "PLYSTRA_CONFIG=deploy/customer.yaml"}
	var firstStdout bytes.Buffer
	var firstStderr bytes.Buffer
	firstExit := RunIn([]string{"inspect", "capabilities", "--format", "json"}, &firstStdout, &firstStderr, root, environment)
	var secondStdout bytes.Buffer
	var secondStderr bytes.Buffer
	secondExit := RunIn([]string{"inspect", "capabilities", "--format", "json"}, &secondStdout, &secondStderr, filepath.Join(root, "missing"), environment)
	if firstExit != 0 || secondExit != 0 || firstStderr.Len() != 0 || secondStderr.Len() != 0 {
		t.Fatalf("Project-independent inspection = exits %d/%d, stderr %q/%q", firstExit, secondExit, firstStderr.String(), secondStderr.String())
	}
	first := decodeInspectCapabilitiesResult(t, firstStdout.String())
	second := decodeInspectCapabilitiesResult(t, secondStdout.String())
	if !bytes.Equal(first.Payload, second.Payload) {
		t.Fatalf("installed payload changed with Project state:\nfirst  %s\nsecond %s", first.Payload, second.Payload)
	}
}

func TestRunInspectCapabilitiesHumanOutputIdentifiesOmissions(t *testing.T) {
	t.Parallel()

	exitCode, stdout, stderr := runInspectCapabilitiesForTest(t, []string{"inspect", "capabilities"}, installedcapabilities.Current)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("runInspectCapabilities = exit %d, stderr %q", exitCode, stderr)
	}
	for _, expected := range []string{
		"Installed Plystra capabilities\n",
		"Public schemas:\n  continuation: unavailable\n  diagnostic: unavailable\n  graph: plystra.graph/v1\n  inspection: plystra.inspect/v1\n  recovery: plystra.recovery/v1\n  result: plystra.result/v1\n",
		"Defaults: startup 2m, invocation 30s\n",
		"inspect.capabilities: specified=yes parsed=yes generated=not_applicable executed=yes accepted=yes\n",
		"Transport component details are omitted from human output; use --format json for the complete installed payload.\n",
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("human output %q does not contain %q", stdout, expected)
		}
	}
}

func TestRunInspectCapabilitiesPreservesJSONIntentForInvalidInvocation(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"inspect", "capabilities", "--verbose", "--format", "json"},
		{"inspect", "capabilities", "--env", "production", "--format", "json"},
		{"inspect", "capabilities", "--config", "deploy.yaml", "--format", "json"},
		{"inspect", "capabilities", "--unknown", "--format", "json"},
		{"inspect", "capabilities", "--format", "json", "--format", "human"},
	}
	for _, arguments := range tests {
		arguments := arguments
		t.Run(strings.Join(arguments[2:], "_"), func(t *testing.T) {
			t.Parallel()
			exitCode, stdout, stderr := runInspectCapabilitiesForTest(t, arguments, func() (commandschema.Capabilities, error) {
				t.Fatal("invalid invocation reached installed capability discovery")
				return commandschema.Capabilities{}, nil
			})
			if exitCode != 2 || stderr != "" {
				t.Fatalf("runInspectCapabilities = exit %d, stderr %q", exitCode, stderr)
			}
			document := decodeInspectCapabilitiesResult(t, stdout)
			if document.Status != "invalid_invocation" || document.ExitClass != 2 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.InspectCapabilitiesInvocationInvalid || string(document.Payload) != "null" {
				t.Fatalf("invalid result = %#v, payload %s", document, document.Payload)
			}
			if len(document.Recovery) != 1 || document.Recovery[0].Schema != commandschema.RecoverySchemaV1 || document.Recovery[0].Kind != "manual" || document.Recovery[0].Target.Kind != "command" || document.Recovery[0].Target.ID != "inspect.capabilities" {
				t.Fatalf("recovery = %#v", document.Recovery)
			}
		})
	}
}

func TestRunInspectCapabilitiesReportsInstalledProviderFailure(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected installed capability failure")
	exitCode, stdout, stderr := runInspectCapabilitiesForTest(t, []string{"inspect", "capabilities", "--format", "json"}, func() (commandschema.Capabilities, error) {
		return commandschema.Capabilities{}, injected
	})
	if exitCode != 8 || stderr != "" {
		t.Fatalf("runInspectCapabilities = exit %d, stderr %q", exitCode, stderr)
	}
	document := decodeInspectCapabilitiesResult(t, stdout)
	if document.Status != "execution_failed" || document.ExitClass != 8 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.InspectCapabilitiesFailed || string(document.Payload) != "null" {
		t.Fatalf("failure result = %#v, payload %s", document, document.Payload)
	}
	if len(document.Recovery) != 1 || document.Recovery[0].Target.Kind != "installation" || document.Recovery[0].Target.ID != "plystra" {
		t.Fatalf("failure recovery = %#v", document.Recovery)
	}
}

func TestRunInspectCapabilitiesReturnsInternalFailureOnResultWriteFailure(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected result write failure")
	for _, test := range []struct {
		name       string
		arguments  []string
		wantStderr string
	}{
		{
			name:       "human",
			arguments:  []string{"inspect", "capabilities"},
			wantStderr: "render installed capabilities: injected result write failure\n",
		},
		{
			name:      "json",
			arguments: []string{"inspect", "capabilities", "--format", "json"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			exitCode := runInspectCapabilities(test.arguments, inspectCapabilitiesErrorWriter{err: injected}, &stderr, inspectCapabilitiesDependencies{
				current: installedcapabilities.Current,
				invocationID: func() (string, error) {
					return inspectCapabilitiesTestInvocationID, nil
				},
			})
			if exitCode != 8 || stderr.String() != test.wantStderr {
				t.Fatalf("runInspectCapabilities = exit %d, stderr %q; want exit 8, stderr %q", exitCode, stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunInspectCapabilitiesBoundsInitializationFailure(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runInspectCapabilities(
		[]string{"inspect", "capabilities", "--format", "json"},
		&stdout,
		&stderr,
		inspectCapabilitiesDependencies{
			current: func() (commandschema.Capabilities, error) {
				t.Fatal("uninitialized command reached installed capability discovery")
				return commandschema.Capabilities{}, nil
			},
			invocationID: func() (string, error) { return "", errors.New(strings.Repeat("x", 10_000)) },
		},
	)
	if exitCode != 8 || stdout.Len() != 0 || stderr.String() != "initialize plystra inspect capabilities result: internal failure\n" || stderr.Len() > 4096 {
		t.Fatalf("runInspectCapabilities = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func runInspectCapabilitiesForTest(t *testing.T, arguments []string, current installedCapabilitiesProvider) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runInspectCapabilities(arguments, &stdout, &stderr, inspectCapabilitiesDependencies{
		current: current,
		invocationID: func() (string, error) {
			return inspectCapabilitiesTestInvocationID, nil
		},
	})
	return exitCode, stdout.String(), stderr.String()
}

func decodeInspectCapabilitiesResult(t testing.TB, value string) inspectCapabilitiesResultDocument {
	t.Helper()
	var document inspectCapabilitiesResultDocument
	if err := json.Unmarshal([]byte(value), &document); err != nil {
		t.Fatalf("decode inspect capabilities result %q: %v", value, err)
	}
	return document
}
