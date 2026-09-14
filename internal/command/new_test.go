package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/newproject"
)

const newCommandTestInvocationID = "123e4567-e89b-42d3-a456-426614174000"

type stubNewProjectResult struct {
	modulePath string
	directory  string
	path       string
}

func (r stubNewProjectResult) ModulePath() string { return r.modulePath }

func (r stubNewProjectResult) Directory() string { return r.directory }

func (r stubNewProjectResult) Path() string { return r.path }

type newResultDocument struct {
	Schema       string `json:"schema"`
	Operation    string `json:"operation"`
	InvocationID string `json:"invocation_id"`
	Status       string `json:"status"`
	ExitClass    int    `json:"exit_class"`
	Diagnostics  []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"diagnostics"`
	Recovery []struct {
		Schema           string   `json:"schema"`
		Kind             string   `json:"kind"`
		WorkingDirectory string   `json:"working_directory"`
		Argv             []string `json:"argv"`
		Target           struct {
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
	Payload *struct {
		Schema     string `json:"schema"`
		ModulePath string `json:"module_path"`
		Directory  string `json:"directory"`
	} `json:"payload"`
}

func TestRunNewCommandEmitsCanonicalJSONSuccess(t *testing.T) {
	t.Parallel()

	var received newproject.Options
	creator := func(_ context.Context, options newproject.Options) (newProjectResult, error) {
		received = options
		return stubNewProjectResult{
			modulePath: "example.com/acme/app",
			directory:  "app",
			path:       `C:\work\app`,
		}, nil
	}
	result, stdout, stderr := runNewCommandForTest(t, []string{"new", "app", "--module", "example.com/acme/app", "--format", "json"}, creator, nil)
	if result != 0 || stderr != "" {
		t.Fatalf("runNewCommand = exit %d, stderr %q", result, stderr)
	}
	if received.Parent != `C:\work` || received.ProjectName != "app" || received.ModulePath != "example.com/acme/app" || received.NoAgentGuidance {
		t.Fatalf("creator options = %#v", received)
	}
	document := decodeNewResult(t, stdout)
	if document.Schema != commandschema.ResultSchemaV1 || document.Operation != "new" || document.InvocationID != newCommandTestInvocationID || document.Status != "changed" || document.ExitClass != 0 {
		t.Fatalf("result identity = %#v", document)
	}
	if document.Payload == nil || document.Payload.Schema != commandschema.ProjectCreatedSchemaV1 || document.Payload.ModulePath != "example.com/acme/app" || document.Payload.Directory != "app" {
		t.Fatalf("payload = %#v", document.Payload)
	}
	if len(document.Effects.Observed) != 1 || len(document.Effects.Planned) != 0 || len(document.Effects.Skipped) != 0 || len(document.Effects.Unverified) != 0 {
		t.Fatalf("effects = %#v", document.Effects)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(strings.TrimSpace(stdout), "\n") != 0 {
		t.Fatalf("stdout is not one compact canonical document: %q", stdout)
	}
}

func TestRunNewCommandHonorsJSONIntentAfterMalformedArgument(t *testing.T) {
	t.Parallel()

	creator := func(context.Context, newproject.Options) (newProjectResult, error) {
		t.Fatal("invalid invocation reached creator")
		return nil, nil
	}
	exitCode, stdout, stderr := runNewCommandForTest(t, []string{"new", "app", "--unknown", "--format", "json"}, creator, nil)
	if exitCode != 2 || stderr != "" {
		t.Fatalf("runNewCommand = exit %d, stderr %q", exitCode, stderr)
	}
	document := decodeNewResult(t, stdout)
	assertNewFailure(t, document, "invalid_invocation", 2, diagnosticcode.ProjectCreateInvocationInvalid)
	if len(document.Recovery) != 1 || document.Recovery[0].Schema != commandschema.RecoverySchemaV1 || document.Recovery[0].Kind != "manual" || len(document.Recovery[0].Argv) != 0 {
		t.Fatalf("recovery = %#v", document.Recovery)
	}
}

func TestRunNewCommandClassifiesClosedFailureOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		status     string
		exit       int
		code       string
		targetKind string
		targetID   string
	}{
		{name: "validation", err: fmtError(newproject.ErrInvalidProjectName), status: "validation_failed", exit: 3, code: diagnosticcode.ProjectCreateNameInvalid, targetKind: "argument", targetID: "project-name"},
		{name: "git unavailable", err: fmt.Errorf("%w: %w", newproject.ErrGitInitialization, newproject.ErrGitUnavailable), status: "prerequisite_missing", exit: 4, code: diagnosticcode.ProjectCreateGitUnavailable, targetKind: "tool", targetID: "git"},
		{name: "cancelled", err: context.Canceled, status: "cancelled", exit: 5, code: diagnosticcode.ProjectCreateCancelled, targetKind: "command", targetID: "new"},
		{name: "git failed", err: fmtError(newproject.ErrGitInitialization), status: "execution_failed", exit: 8, code: diagnosticcode.ProjectCreateGitInitializationFailed, targetKind: "tool", targetID: "git"},
		{name: "execution", err: fmtError(newproject.ErrCreate), status: "execution_failed", exit: 8, code: diagnosticcode.ProjectCreateFailed, targetKind: "operation", targetID: "new"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			creator := func(context.Context, newproject.Options) (newProjectResult, error) {
				return nil, test.err
			}
			exitCode, stdout, stderr := runNewCommandForTest(t, []string{"new", "app", "--format", "json"}, creator, nil)
			if exitCode != test.exit || stderr != "" {
				t.Fatalf("runNewCommand = exit %d, stderr %q", exitCode, stderr)
			}
			document := decodeNewResult(t, stdout)
			assertNewFailure(t, document, test.status, test.exit, test.code)
			if len(document.Recovery) != 1 || document.Recovery[0].Target.Kind != test.targetKind || document.Recovery[0].Target.ID != test.targetID {
				t.Fatalf("recovery target = %#v; want %s/%s", document.Recovery, test.targetKind, test.targetID)
			}
			if len(document.Diagnostics[0].Message) == 0 || len(document.Diagnostics[0].Message) > 4096 {
				t.Fatalf("diagnostic message length = %d", len(document.Diagnostics[0].Message))
			}
		})
	}
}

func TestRunNewCommandClassifiesPromptCancellationBeforeCreation(t *testing.T) {
	t.Parallel()

	creator := func(context.Context, newproject.Options) (newProjectResult, error) {
		t.Fatal("cancelled prompt reached creator")
		return nil, nil
	}
	prompt := func(string, bool) (bool, error) { return false, errors.New("input ended") }
	exitCode, stdout, stderr := runNewCommandForTest(t, []string{"new", "app", "--interactive", "--format", "json"}, creator, prompt)
	if exitCode != 5 || stderr != "" {
		t.Fatalf("runNewCommand = exit %d, stderr %q", exitCode, stderr)
	}
	document := decodeNewResult(t, stdout)
	assertNewFailure(t, document, "cancelled", 5, diagnosticcode.ProjectCreateCancelled)
	if len(document.Recovery) != 1 || document.Recovery[0].Kind != "manual" || document.Recovery[0].Target.Kind != "command" || document.Recovery[0].Target.ID != "new" || document.Recovery[0].WorkingDirectory != "" || len(document.Recovery[0].Argv) != 0 || document.Recovery[0].Verification.WorkingDirectory != "." || !equalStrings(document.Recovery[0].Verification.Argv, []string{"plystra", "new", "--help"}) {
		t.Fatalf("cancellation recovery = %#v", document.Recovery)
	}
}

func TestRunNewCommandBoundsPreInitializationFailure(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runNewCommand(
		[]string{"new", "app", "--format", "json"},
		&stdout,
		&stderr,
		`C:\work`,
		nil,
		nil,
		newCommandDependencies{
			create: func(context.Context, newproject.Options) (newProjectResult, error) {
				t.Fatal("uninitialized command reached creator")
				return nil, nil
			},
			invocationID: func() (string, error) { return "", errors.New(strings.Repeat("x", 10_000)) },
		},
	)
	if exitCode != 8 || stdout.Len() != 0 || stderr.String() != "initialize plystra new result: internal failure\n" || stderr.Len() > 4096 {
		t.Fatalf("runNewCommand = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
}

func runNewCommandForTest(t *testing.T, arguments []string, creator newProjectCreator, prompt newProjectPrompter) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runNewCommand(arguments, &stdout, &stderr, `C:\work`, []string{"TEST=1"}, prompt, newCommandDependencies{
		create:       creator,
		invocationID: func() (string, error) { return newCommandTestInvocationID, nil },
	})
	return exitCode, stdout.String(), stderr.String()
}

func decodeNewResult(t *testing.T, value string) newResultDocument {
	t.Helper()
	var document newResultDocument
	if err := json.Unmarshal([]byte(value), &document); err != nil {
		t.Fatalf("decode result %q: %v", value, err)
	}
	return document
}

func assertNewFailure(t *testing.T, document newResultDocument, status string, exit int, code string) {
	t.Helper()
	if document.Schema != commandschema.ResultSchemaV1 || document.Operation != "new" || document.InvocationID != newCommandTestInvocationID || document.Status != status || document.ExitClass != exit {
		t.Fatalf("result identity = %#v", document)
	}
	if len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != code || document.Payload != nil {
		t.Fatalf("failure facts = diagnostics %#v payload %#v", document.Diagnostics, document.Payload)
	}
	if len(document.Effects.Observed) != 0 || len(document.Effects.Planned) != 0 || len(document.Effects.Skipped) != 0 || len(document.Effects.Unverified) != 0 {
		t.Fatalf("failure effects = %#v", document.Effects)
	}
}

func fmtError(target error) error {
	return fmt.Errorf("wrapped failure: %w", target)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
