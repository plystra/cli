package command

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestWriteCommandFailureAddsOnePrimaryRecoveryForCommonTypedFailures(t *testing.T) {
	t.Parallel()

	missing, ambiguous, invalidChoice, contractConflict := recoveryProviderFailures(t)
	tests := []struct {
		name    string
		err     error
		context recoveryContext
		want    string
	}{
		{
			name: "invalid template with nested Provider ambiguity",
			err: fmt.Errorf(
				"%w: template cannot qualify: %w; correction: publish a corrected template version",
				newproject.ErrInvalidTemplate,
				ambiguous,
			),
			want: "publish a corrected template version",
		},
		{
			name: "missing Provider",
			err:  missing,
			want: "Add an intended dependency with `plystra add <go-module-query>` whose Plugin provides email.send/v1.",
		},
		{
			name:    "ambiguous Provider",
			err:     ambiguous,
			context: commandRecoveryContext("", "production", nil),
			want:    "Select one compatible Provider explicitly by running `plystra use email.send/v1 <plugin-id> --env \"production\"`.",
		},
		{
			name:    "invalid Provider choice",
			err:     invalidChoice,
			context: commandRecoveryContext("deploy/customer.yaml", "", nil),
			want:    "Replace the invalid Provider choice with one visible compatible Plugin by running `plystra use email.send/v1 <plugin-id> --config \"deploy/customer.yaml\"`.",
		},
		{
			name: "Provider contract conflict",
			err:  contractConflict,
			want: "Make every Provider of email.send/v1 carry one identical provider-independent capability.yaml.",
		},
		{
			name:    "inherited configuration conflict",
			err:     fmt.Errorf("compose dependencies: %w", applicationmeta.ErrInheritedConflict),
			context: commandRecoveryContext("", "test", nil),
			want:    "Set or remove the conflicting field explicitly in plystra.test.yaml, then rerun the command.",
		},
		{
			name: "configuration selection",
			err:  fmt.Errorf("resolve: %w", applicationresolve.ErrConfigurationSelection),
			want: "Select exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.",
		},
		{
			name:    "application runtime dependency",
			err:     fmt.Errorf("%w: go.mod is stale; run plystra generate to repair module metadata transactionally", applicationgenerate.ErrRuntimeDependency),
			context: commandRecoveryContext("", "production", nil),
			want:    "Run `plystra generate --env \"production\"` to repair the required direct application runtime dependencies.",
		},
		{
			name: "invalid selected configuration",
			err: errors.Join(
				applicationresolve.ErrConfigurationSelection,
				applicationresolve.ErrManifest,
				applicationmeta.ErrInvalidManifest,
			),
			context: commandRecoveryContext("", "production", nil),
			want:    "Edit plystra.production.yaml so every value matches a selected Plugin's closed typed schema, then rerun the command.",
		},
		{
			name: "invalid dependency Project manifest",
			err:  errors.Join(applicationresolve.ErrManifest, applicationmeta.ErrInvalidManifest),
			want: "Correct the reported root or dependency Project plystra.yaml, then rerun the command.",
		},
		{
			name: "Plugin targeting",
			err:  fmt.Errorf("author Capability: %w", plugintarget.ErrAmbiguous),
			want: "Rerun with `--plugin <plugin-directory-or-id>` to select one exact local Plugin.",
		},
		{
			name: "generation helper failure",
			err:  fmt.Errorf("run extension: %w", generationexec.ErrCompile),
			want: "Fix the selected generation package reported above, then rerun the command.",
		},
		{
			name:    "generated ownership",
			err:     fmt.Errorf("install: %w", generatedfiles.ErrUnexpected),
			context: commandRecoveryContext("", "staging", nil),
			want:    "Move the reported unowned path outside generated/, then run `plystra generate --env \"staging\"`.",
		},
		{
			name: "Protobuf history",
			err:  fmt.Errorf("allocate fields: %w", protobufwiremap.ErrHistory),
			want: "Restore generated/proto/wire-map.json from its last known-good generated state, then regenerate.",
		},
		{
			name: "Capability confirmation",
			err:  fmt.Errorf("create Capability: %w", capabilitycreate.ErrConfirmationRequired),
			want: "Review the visible Capability versions, then rerun the create command with `--confirm`.",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			writeCommandFailure(&output, "command failed", test.err, test.context)
			got := output.String()
			if !strings.Contains(got, "command failed: ") || !strings.Contains(got, "\n\nRecovery:\n"+test.want+"\n") {
				t.Fatalf("writeCommandFailure() = %q, want recovery %q", got, test.want)
			}
			if count := strings.Count(got, "Recovery:"); count != 1 {
				t.Fatalf("Recovery count = %d in %q", count, got)
			}
			if strings.Contains(got, "correction:") {
				t.Fatalf("embedded recovery was not removed: %q", got)
			}
		})
	}
}

func TestRecoverySelectorPreservesModeWithoutEchoingUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		context    recoveryContext
		wantSuffix string
		reject     string
	}{
		{name: "default", context: commandRecoveryContext("", "", nil), wantSuffix: "`plystra generate`"},
		{name: "explicit environment", context: commandRecoveryContext("", "production", []string{"PLYSTRA_CONFIG=ignored.yaml"}), wantSuffix: "`plystra generate --env \"production\"`"},
		{name: "ambient environment", context: commandRecoveryContext("", "", []string{"PLYSTRA_ENV=test"}), wantSuffix: "`plystra generate --env \"test\"`"},
		{name: "explicit configuration", context: commandRecoveryContext("deploy/customer.yaml", "", []string{"PLYSTRA_ENV=ignored"}), wantSuffix: "`plystra generate --config \"deploy/customer.yaml\"`"},
		{name: "ambient configuration", context: commandRecoveryContext("", "", []string{"PLYSTRA_CONFIG=deploy/ambient.yaml"}), wantSuffix: "`plystra generate --config \"deploy/ambient.yaml\"`"},
		{name: "absolute configuration", context: commandRecoveryContext(filepath.Join(t.TempDir(), "private-token.yaml"), "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "shell-sensitive configuration", context: commandRecoveryContext("deploy/`private-token`.yaml", "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "unsafe environment", context: commandRecoveryContext("", "production\nprivate-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
		{name: "shell-sensitive environment", context: commandRecoveryContext("", "$env:private-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			action, ok := primaryRecoveryAction(generatedfiles.ErrConflict, test.context)
			if !ok || !strings.Contains(action, test.wantSuffix) {
				t.Fatalf("primaryRecoveryAction() = %q, %t; want %q", action, ok, test.wantSuffix)
			}
			if test.reject != "" && strings.Contains(action, test.reject) {
				t.Fatalf("primaryRecoveryAction() leaked %q in %q", test.reject, action)
			}
		})
	}
}

func FuzzRecoverySelectorDoesNotInjectDiagnosticLines(f *testing.F) {
	for _, seed := range []string{"production", "deploy/customer.yaml", "../outside.yaml", "value\nRecovery:\nunsafe", "$env:SECRET", "`command`", strings.Repeat("x", 600)} {
		f.Add(true, seed)
		f.Add(false, seed)
	}
	f.Fuzz(func(t *testing.T, environmentMode bool, value string) {
		context := commandRecoveryContext(value, "", nil)
		if environmentMode {
			context = commandRecoveryContext("", value, nil)
		}
		action, ok := primaryRecoveryAction(generatedfiles.ErrConflict, context)
		if !ok {
			t.Fatal("generated ownership conflict was not actionable")
		}
		if strings.ContainsAny(action, "\r\n\x00") {
			t.Fatalf("recovery action contains an injected line or NUL: %q", action)
		}
		if len(action) > 1000 {
			t.Fatalf("recovery action is unbounded: %d bytes", len(action))
		}
	})
}

func TestWriteCommandFailureChoosesOneJoinedProblemAndLeavesUnknownErrorsUnchanged(t *testing.T) {
	t.Parallel()

	_, ambiguous, invalidChoice, _ := recoveryProviderFailures(t)
	joined := errors.Join(ambiguous, invalidChoice)
	var output strings.Builder
	writeCommandFailure(&output, "", joined, recoveryContext{})
	got := output.String()
	for _, want := range []string{"selected Plugin ID is not visible", "\n\nRecovery:\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("joined output = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "ambiguous canonical Capability provider") {
		t.Fatalf("joined output included a non-primary problem: %q", got)
	}
	if strings.Contains(got, "correction:") || strings.Count(got, "Recovery:") != 1 {
		t.Fatalf("joined output contains duplicate recovery advice: %q", got)
	}

	output.Reset()
	unknown := errors.New("internal renderer failed")
	writeCommandFailure(&output, "inspect", unknown, recoveryContext{})
	if got := output.String(); got != "inspect: internal renderer failed\n" {
		t.Fatalf("unknown output = %q", got)
	}
}

func recoveryProviderFailures(t *testing.T) (missing, ambiguous, invalidChoice, contractConflict error) {
	t.Helper()
	contract := recoveryContract("string")
	requirement := providerresolution.Requirement{
		Contract: contract,
		Source: providerresolution.RequirementSource{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  "plystra.yaml capabilities.require[email.send/v1]",
			ModulePath: "example.com/project",
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		},
	}
	_, missing = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}})
	candidates := []providerresolution.Candidate{
		{PluginID: "acme.email.local", Contract: contract, Source: "local/capability.yaml"},
		{PluginID: "acme.email.smtp", Contract: contract, Source: "smtp/capability.yaml"},
	}
	_, ambiguous = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}, Candidates: candidates})
	_, invalidChoice = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates:   candidates,
		Choices: []providerresolution.Choice{{
			Capability: "email.send/v1",
			PluginID:   "missing.email",
			Sources: []providerresolution.ChoiceSource{{
				Kind:       providerresolution.ChoiceSourceCurrentProject,
				Reference:  "plystra.yaml capabilities.use[email.send/v1]",
				ModulePath: "example.com/project",
				Path:       "plystra.yaml",
				Line:       2,
				Column:     3,
			}},
		}},
	})
	_, contractConflict = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates: []providerresolution.Candidate{{
			PluginID: "acme.email.local",
			Contract: recoveryContract("boolean"),
			Source:   "local/capability.yaml",
		}},
	})
	for name, err := range map[string]error{
		"missing": missing, "ambiguous": ambiguous, "invalid choice": invalidChoice, "contract conflict": contractConflict,
	} {
		if err == nil {
			t.Fatalf("%s provider input unexpectedly resolved", name)
		}
	}
	return missing, ambiguous, invalidChoice, contractConflict
}

func recoveryContract(fieldType string) []byte {
	return []byte("id: email.send/v1\nrequest: {value: {type: " + fieldType + "}}\n" + `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`)
}
