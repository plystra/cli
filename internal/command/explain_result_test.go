package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/moduledependency"
)

const explainResultTestInvocationID = "123e4567-e89b-42d3-a456-426614174000"

type explainResultTestDocument struct {
	Schema       string          `json:"schema"`
	Operation    string          `json:"operation"`
	InvocationID string          `json:"invocation_id"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Status       string          `json:"status"`
	ExitClass    int             `json:"exit_class"`
	Diagnostics  []struct {
		Code string `json:"code"`
	} `json:"diagnostics"`
	Recovery []struct {
		Schema string `json:"schema"`
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"target"`
		Selector      json.RawMessage `json:"selector"`
		Preconditions []struct {
			Kind string `json:"kind"`
		} `json:"preconditions"`
		Verification struct {
			WorkingDirectory string   `json:"working_directory"`
			Argv             []string `json:"argv"`
		} `json:"verification"`
		Options []struct {
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
	Payload json.RawMessage `json:"payload"`
}

func TestRunExplainRejectsExplicitSelectorConflictBeforeProjectDiscovery(t *testing.T) {
	t.Parallel()

	called := false
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runExplainWithDependencies(
		[]string{"explain", "capability", "email.send/v1", "--format", "json", "--config", "deploy/customer.yaml", "--env", "production"},
		&stdout,
		&stderr,
		t.TempDir(),
		nil,
		explainDependencies{
			resolve: func(_ context.Context, _ applicationresolve.Options) (applicationresolve.Result, error) {
				called = true
				return applicationresolve.Result{}, nil
			},
			invocationID: func() (string, error) { return explainResultTestInvocationID, nil },
		},
	)
	if called {
		t.Fatal("selector conflict reached Project discovery")
	}
	if exitCode != 3 || stderr.Len() != 0 {
		t.Fatalf("runExplainWithDependencies = exit %d, stderr %q", exitCode, stderr.String())
	}
	document := decodeExplainResultTestDocument(t, stdout.Bytes())
	if document.Schema != commandschema.ResultSchemaV1 || document.Operation != "explain.capability" || document.InvocationID != explainResultTestInvocationID || document.Status != "validation_failed" || document.ExitClass != 3 || string(document.Snapshot) != "null" || string(document.Payload) != "null" {
		t.Fatalf("selector-conflict result identity = %#v, snapshot %s, payload %s", document, document.Snapshot, document.Payload)
	}
	if len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.ConfigurationSelectionInvalid {
		t.Fatalf("selector-conflict diagnostics = %#v", document.Diagnostics)
	}
	if len(document.Recovery) != 1 || document.Recovery[0].Schema != commandschema.RecoverySchemaV1 || document.Recovery[0].ID != "select-explain-configuration" || document.Recovery[0].Kind != "manual" || document.Recovery[0].Target.Kind != "configuration_selector" || document.Recovery[0].Target.ID != "explain.capability" || string(document.Recovery[0].Selector) != "null" || len(document.Recovery[0].Preconditions) != 1 || document.Recovery[0].Preconditions[0].Kind != "exactly_one_selector" {
		t.Fatalf("selector-conflict recovery = %#v", document.Recovery)
	}
	if document.Effects.Observed == nil || document.Effects.Planned == nil || document.Effects.Skipped == nil || document.Effects.Unverified == nil || len(document.Effects.Observed) != 0 || len(document.Effects.Planned) != 0 || len(document.Effects.Skipped) != 0 || len(document.Effects.Unverified) != 0 {
		t.Fatalf("selector-conflict effects = %#v", document.Effects)
	}
}

func TestExplainRejectsPlaceholderSubjectsBeforeResolution(t *testing.T) {
	t.Parallel()
	for _, subject := range []string{`config["<constructor>"]["host"]`, `config["${CONSTRUCTOR}"]["host"]`} {
		t.Run(subject, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := runExplainWithDependencies([]string{"explain", "config", subject, "--format", "json"}, &stdout, &stderr, t.TempDir(), nil, explainDependencies{
				invocationID: func() (string, error) { return explainResultTestInvocationID, nil },
				resolve: func(context.Context, applicationresolve.Options) (applicationresolve.Result, error) {
					t.Error("unresolved subject reached application resolution")
					return applicationresolve.Result{}, errors.New("must not resolve")
				},
			})
			document := decodeExplainResultTestDocument(t, stdout.Bytes())
			if exitCode != 2 || stderr.Len() != 0 || document.Status != "invalid_invocation" || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.ExplainSubjectInvalid {
				t.Fatalf("placeholder subject = exit %d stdout %s stderr %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestExplainInitializationFailureIsBoundedAndPrecedesResolution(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		id   invocationIDGenerator
	}{
		{name: "missing generator"},
		{name: "generator failure", id: func() (string, error) { return "", errors.New("private initialization detail") }},
		{name: "invalid identity", id: func() (string, error) { return "private invalid identity", nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := runExplainWithDependencies([]string{"explain", "capability", "kernel.health/v1", "--format", "json"}, &stdout, &stderr, t.TempDir(), nil, explainDependencies{
				invocationID: test.id,
				resolve: func(context.Context, applicationresolve.Options) (applicationresolve.Result, error) {
					t.Fatal("resolution ran before result initialization")
					return applicationresolve.Result{}, nil
				},
			})
			if exitCode != 8 || stdout.Len() != 0 || stderr.String() != "initialize plystra explain result: internal failure\n" {
				t.Fatalf("initialization = %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestExplainResolverFailuresKeepResultOwnershipAndRedactUnknownErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status string
		code   string
		exit   int
	}{
		{name: "internal", err: errors.New("private resolver detail C:/private/project secret-value"), status: "execution_failed", code: diagnosticcode.ExplainFailed, exit: 8},
		{name: "prerequisite", err: moduledependency.ErrModuleUnavailable, status: "prerequisite_missing", code: diagnosticcode.GoModuleUnavailable, exit: 4},
	} {
		for _, format := range []string{"human", "json"} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				exitCode := runExplainWithDependencies([]string{"explain", "capability", "kernel.health/v1", "--format", format}, &stdout, &stderr, t.TempDir(), nil, explainDependencies{
					invocationID: func() (string, error) { return explainResultTestInvocationID, nil },
					resolve: func(context.Context, applicationresolve.Options) (applicationresolve.Result, error) {
						return applicationresolve.Result{}, test.err
					},
				})
				if exitCode != test.exit || strings.Contains(stdout.String()+stderr.String(), "private resolver detail") || strings.Contains(stdout.String()+stderr.String(), "secret-value") {
					t.Fatalf("resolver failure = %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
				}
				if format == "human" {
					if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Diagnostic: "+test.code) {
						t.Fatalf("human failure = stdout %q stderr %q", stdout.String(), stderr.String())
					}
					return
				}
				document := decodeExplainResultTestDocument(t, stdout.Bytes())
				if stderr.Len() != 0 || document.Status != test.status || document.ExitClass != test.exit || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != test.code || len(document.Recovery) != 1 {
					t.Fatalf("JSON failure = %#v stderr %q", document, stderr.String())
				}
			})
		}
	}
}

func TestExplainResultBuildFailureAlwaysEmitsInitializedFallback(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"explain.capability", "invalid operation"} {
		t.Run(operation, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			encoder, ok := initializeExplainResultEncoder(&stderr, func() (string, error) { return explainResultTestInvocationID, nil })
			if !ok {
				t.Fatal("initialize encoder")
			}
			encoder.buildResult = func(commandschema.ResultInput) (commandschema.Result, error) {
				return commandschema.Result{}, errors.New("private builder failure")
			}
			output, err := newCommandOutput(commandFormatJSON, &stdout, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			invalidSnapshot := commandschema.SelectorSnapshot{}
			exitCode := emitExplainFailure(output, encoder, explainFailureInput{operation: operation, err: errExplainInvocation, snapshot: &invalidSnapshot}, func() error { t.Fatal("JSON invoked human renderer"); return nil })
			document := decodeExplainResultTestDocument(t, stdout.Bytes())
			wantOperation := operation
			if operation == "invalid operation" {
				wantOperation = "explain"
			}
			if exitCode != 8 || stderr.Len() != 0 || document.Operation != wantOperation || document.InvocationID != explainResultTestInvocationID || document.Status != "execution_failed" || document.ExitClass != 8 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != diagnosticcode.ExplainFailed || string(document.Snapshot) != "null" {
				t.Fatalf("fallback = %#v, exit %d stderr %q", document, exitCode, stderr.String())
			}
		})
	}
}

func TestExplainProviderRecoveryUsesFiniteNonExecutableOptions(t *testing.T) {
	t.Parallel()
	_, ambiguous, _, _, _ := recoveryProviderFailures(t)
	var human bytes.Buffer
	if err := writeHumanExplainFailure(&human, "explain", ambiguous, recoveryContext{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human.String(), "plystra use") || !strings.Contains(human.String(), `capabilities.use["email.send/v1"]`) || !strings.Contains(human.String(), "acme.email.local, acme.email.smtp") {
		t.Fatalf("human Provider recovery = %q", human.String())
	}
	var stderr bytes.Buffer
	encoder, ok := initializeExplainResultEncoder(&stderr, func() (string, error) { return explainResultTestInvocationID, nil })
	if !ok {
		t.Fatal("initialize encoder")
	}
	arguments := explainArguments{subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1"}
	for _, selector := range []string{"production", "<environment>"} {
		t.Run(selector, func(t *testing.T) {
			result, err := encoder.failure(explainFailureInput{operation: "explain.capability", err: ambiguous, arguments: &arguments, context: commandRecoveryContext("", selector, nil)})
			if err != nil {
				t.Fatal(err)
			}
			document := decodeExplainResultTestDocument(t, result.CanonicalJSON())
			if document.Status != "decision_required" || document.ExitClass != 4 || len(document.Recovery) != 1 {
				t.Fatalf("ambiguity = %#v", document)
			}
			action := document.Recovery[0]
			if selector == "<environment>" {
				if action.Kind != "manual" || len(action.Options) != 0 || string(action.Selector) != "null" {
					t.Fatalf("unsafe selector recovery = %#v", action)
				}
				return
			}
			if action.Kind != "choose" || action.ID != "choose-capability-provider" || action.Target.ID != `capabilities.use["email.send/v1"]` || len(action.Options) != 2 || action.Options[0].Value != "acme.email.local" || action.Options[1].Value != "acme.email.smtp" {
				t.Fatalf("Provider options = %#v", action)
			}
			for _, option := range action.Options {
				if option.WorkingDirectory != "" || len(option.Argv) != 0 {
					t.Fatalf("unsupported Provider command is executable: %#v", option)
				}
			}
			if !reflect.DeepEqual(action.Verification.Argv, []string{"plystra", "explain", "capability", "email.send/v1", "--format", "json", "--env", "production"}) {
				t.Fatalf("verification = %#v", action.Verification)
			}
		})
	}
}

func decodeExplainResultTestDocument(t testing.TB, value []byte) explainResultTestDocument {
	t.Helper()
	var document explainResultTestDocument
	if err := json.Unmarshal(value, &document); err != nil {
		t.Fatalf("decode explain result %q: %v", value, err)
	}
	return document
}
