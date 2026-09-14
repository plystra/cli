package commandschema_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

const testInvocationID = "123e4567-e89b-42d3-a456-426614174000"

func TestProjectCreatedResultIsCanonicalAndClosed(t *testing.T) {
	t.Parallel()

	payload := newProjectCreated(t)
	effect := newProjectWriteEffect(t)
	effects := newEffects(t, commandschema.EffectsInput{Observed: []commandschema.Effect{effect}})
	result, err := commandschema.NewResult(commandschema.ResultInput{
		Operation:    "new",
		InvocationID: testInvocationID,
		Status:       commandschema.StatusChanged,
		Effects:      effects,
		Payload:      payload,
	})
	if err != nil || !result.Valid() {
		t.Fatalf("NewResult = %#v, %v", result, err)
	}
	want := `{"schema":"plystra.result/v1","operation":"new","invocation_id":"123e4567-e89b-42d3-a456-426614174000","snapshot":null,"status":"changed","exit_class":0,"changes":[],"diagnostics":[],"recovery":[],"effects":{"observed":[{"schema":"plystra.effect/v1","id":"create-project","class":"project_write","phase":"commit","target":"my-app","owner":{"module":"example.com/acme/my-app","path":"."},"reason":"requested_project_creation","reversibility":"manual","verification":["plystra","check"]}],"planned":[],"skipped":[],"unverified":[]},"support":[],"payload":{"schema":"plystra.project-created/v1","module_path":"example.com/acme/my-app","directory":"my-app"},"continuation":null}`
	if string(result.CanonicalJSON()) != want {
		t.Fatalf("CanonicalJSON = %s\nwant = %s", result.CanonicalJSON(), want)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(result.CanonicalJSON(), &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	wantFields := []string{"changes", "continuation", "diagnostics", "effects", "exit_class", "invocation_id", "operation", "payload", "recovery", "schema", "snapshot", "status", "support"}
	gotFields := make([]string, 0, len(document))
	for field := range document {
		gotFields = append(gotFields, field)
	}
	sortStrings(gotFields)
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("top-level fields = %q, want %q", gotFields, wantFields)
	}
	if result.Operation() != "new" || result.InvocationID() != testInvocationID || result.Status() != commandschema.StatusChanged || result.ExitClass() != 0 {
		t.Fatalf("result identity = operation %q invocation %q status %q exit %d", result.Operation(), result.InvocationID(), result.Status(), result.ExitClass())
	}
}

func TestResultStatusesHaveCanonicalExitClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status commandschema.Status
		exit   int
	}{
		{commandschema.StatusSuccess, 0},
		{commandschema.StatusNoOp, 0},
		{commandschema.StatusChanged, 0},
		{commandschema.StatusInvalidInvocation, 2},
		{commandschema.StatusUnsupported, 2},
		{commandschema.StatusValidationFailed, 3},
		{commandschema.StatusDecisionRequired, 4},
		{commandschema.StatusAuthorityRequired, 4},
		{commandschema.StatusPrerequisiteMissing, 4},
		{commandschema.StatusCancelled, 5},
		{commandschema.StatusPartial, 6},
		{commandschema.StatusUncertainExternal, 7},
		{commandschema.StatusExecutionFailed, 8},
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.status), func(t *testing.T) {
			t.Parallel()
			exit, ok := commandschema.ExitClass(test.status)
			if !ok || exit != test.exit {
				t.Fatalf("ExitClass(%q) = %d, %t; want %d, true", test.status, exit, ok, test.exit)
			}

			input := commandschema.ResultInput{
				Operation:    "new",
				InvocationID: testInvocationID,
				Status:       test.status,
				Effects:      newEffects(t, commandschema.EffectsInput{}),
			}
			if test.exit != 0 {
				input.Diagnostics = []diagnosticjson.Diagnostic{{Code: diagnosticcode.ProjectCreateFailed, Severity: diagnosticjson.SeverityError, Message: "Project creation failed."}}
			}
			if test.status == commandschema.StatusChanged {
				input.Payload = newProjectCreated(t)
				input.Effects = newEffects(t, commandschema.EffectsInput{Observed: []commandschema.Effect{newProjectWriteEffect(t)}})
			}
			result, err := commandschema.NewResult(input)
			if err != nil || result.ExitClass() != test.exit {
				t.Fatalf("NewResult(%q) = %#v, %v", test.status, result, err)
			}
		})
	}
	if exit, ok := commandschema.ExitClass("future"); ok || exit != 0 {
		t.Fatalf("ExitClass(future) = %d, %t", exit, ok)
	}
}

func TestResultRejectsUnsafeIdentityAndIncompleteFailure(t *testing.T) {
	t.Parallel()

	effects := newEffects(t, commandschema.EffectsInput{})
	tests := []struct {
		name  string
		input commandschema.ResultInput
	}{
		{name: "operation", input: commandschema.ResultInput{Operation: "New", InvocationID: testInvocationID, Status: commandschema.StatusSuccess, Effects: effects}},
		{name: "uppercase UUID", input: commandschema.ResultInput{Operation: "new", InvocationID: "123E4567-e89b-42d3-a456-426614174000", Status: commandschema.StatusSuccess, Effects: effects}},
		{name: "leading separator", input: commandschema.ResultInput{Operation: "new", InvocationID: "-invocation", Status: commandschema.StatusSuccess, Effects: effects}},
		{name: "unsafe invocation ID", input: commandschema.ResultInput{Operation: "new", InvocationID: "../../invocation", Status: commandschema.StatusSuccess, Effects: effects}},
		{name: "failure without diagnostic", input: commandschema.ResultInput{Operation: "new", InvocationID: testInvocationID, Status: commandschema.StatusExecutionFailed, Effects: effects}},
		{name: "changed without payload", input: commandschema.ResultInput{Operation: "new", InvocationID: testInvocationID, Status: commandschema.StatusChanged, Effects: effects}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := commandschema.NewResult(test.input)
			if !errors.Is(err, commandschema.ErrResult) || result.Valid() {
				t.Fatalf("NewResult = %#v, %v; want ErrResult", result, err)
			}
		})
	}
}

func TestResultAcceptsCanonicalNonUUIDInvocationIdentity(t *testing.T) {
	t.Parallel()

	result, err := commandschema.NewResult(commandschema.ResultInput{
		Operation:    "inspect",
		InvocationID: "example-inspect-1",
		Status:       commandschema.StatusSuccess,
		Effects:      newEffects(t, commandschema.EffectsInput{}),
	})
	if err != nil || !result.Valid() || result.InvocationID() != "example-inspect-1" {
		t.Fatalf("NewResult = %#v, %v", result, err)
	}
}

func TestProjectCreatedRejectsAbsoluteOrUnsafeDirectories(t *testing.T) {
	t.Parallel()

	for _, directory := range []string{"", ".", "../app", "/tmp/app", `C:\\tmp\\app`, "App"} {
		payload, err := commandschema.NewProjectCreated(commandschema.ProjectCreatedInput{ModulePath: "example.com/acme/app", Directory: directory})
		if !errors.Is(err, commandschema.ErrProjectCreated) || payload.Valid() {
			t.Fatalf("NewProjectCreated(%q) = %#v, %v; want ErrProjectCreated", directory, payload, err)
		}
	}
	if payload, err := commandschema.NewProjectCreated(commandschema.ProjectCreatedInput{ModulePath: "local module", Directory: "app"}); !errors.Is(err, commandschema.ErrProjectCreated) || payload.Valid() {
		t.Fatalf("invalid module payload = %#v, %v", payload, err)
	}
}

func TestProjectCreatedAcceptsInitialLocalModulePath(t *testing.T) {
	t.Parallel()

	payload, err := commandschema.NewProjectCreated(commandschema.ProjectCreatedInput{ModulePath: "my-app", Directory: "my-app"})
	if err != nil || !payload.Valid() || payload.ModulePath() != "my-app" || payload.Directory() != "my-app" {
		t.Fatalf("NewProjectCreated(local module) = %#v, %v", payload, err)
	}
}

func TestResultReturnsDefensiveJSONAndDiagnosticCopies(t *testing.T) {
	t.Parallel()

	diagnostics := []diagnosticjson.Diagnostic{{Code: diagnosticcode.ProjectCreateFailed, Severity: diagnosticjson.SeverityError, Message: "Project creation failed."}}
	result, err := commandschema.NewResult(commandschema.ResultInput{
		Operation:    "new",
		InvocationID: testInvocationID,
		Status:       commandschema.StatusExecutionFailed,
		Diagnostics:  diagnostics,
		Effects:      newEffects(t, commandschema.EffectsInput{}),
	})
	if err != nil {
		t.Fatalf("NewResult: %v", err)
	}
	diagnostics[0].Message = "changed"
	copyDiagnostics := result.Diagnostics()
	copyDiagnostics[0].Message = "changed again"
	canonical := result.CanonicalJSON()
	canonical[0] = '['
	if !result.Valid() || bytes.HasPrefix(result.CanonicalJSON(), []byte("[")) || result.Diagnostics()[0].Message != "Project creation failed." {
		t.Fatal("result exposed mutable construction or result state")
	}
}

func newProjectCreated(t testing.TB) commandschema.ProjectCreated {
	t.Helper()
	payload, err := commandschema.NewProjectCreated(commandschema.ProjectCreatedInput{ModulePath: "example.com/acme/my-app", Directory: "my-app"})
	if err != nil {
		t.Fatalf("NewProjectCreated: %v", err)
	}
	return payload
}

func newProjectWriteEffect(t testing.TB) commandschema.Effect {
	t.Helper()
	effect, err := commandschema.NewEffect(commandschema.EffectInput{
		ID:            "create-project",
		Class:         commandschema.EffectProjectWrite,
		Phase:         "commit",
		Target:        "my-app",
		Owner:         commandschema.Owner{Module: "example.com/acme/my-app", Path: "."},
		Reason:        "requested_project_creation",
		Reversibility: "manual",
		Verification:  []string{"plystra", "check"},
	})
	if err != nil {
		t.Fatalf("NewEffect: %v", err)
	}
	return effect
}

func newEffects(t testing.TB, input commandschema.EffectsInput) commandschema.Effects {
	t.Helper()
	effects, err := commandschema.NewEffects(input)
	if err != nil {
		t.Fatalf("NewEffects: %v", err)
	}
	return effects
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}
