package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

func TestRunChangePlanConfigurationBatchValidatesOnlyCompleteFinalState(t *testing.T) {
	for _, selector := range []struct {
		name, path string
		args       []string
	}{
		{"default", "plystra.yaml", nil},
		{"environment", "plystra.production.yaml", []string{"--env", "production"}},
		{"replacement", "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}},
	} {
		t.Run(selector.name, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			incomplete := strings.Replace(document, "endpoint: PRIVATE_PRIMARY_REQUIRED, ", "", 1)
			incomplete = strings.Replace(incomplete, "endpoint: PRIVATE_IMPLEMENTATION_REQUIRED, ", "", 1)
			path := "plystra.yaml"
			if selector.name == "replacement" {
				path = selector.path
			}
			writeCommandFile(t, filepath.Join(root, path), incomplete)
			resource := configurationPlanRequest(t, "database.primary", "endpoint", "set", json.RawMessage(`""`))
			implementation := configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`""`))
			runConfigurationPlan(t, root, resource, false, selector.args...)
			runConfigurationPlan(t, root, implementation, false, selector.args...)
			request := configurationBatchRequest(t, resource, implementation)
			first := runConfigurationPlan(t, root, request, true, selector.args...)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, selector.args...)
			if len(first.Payload.Operations) != 2 || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != selector.path || !reflect.DeepEqual(first.Payload, second.Payload) {
				t.Fatal("batch lost operations, scope, or order-independent identity")
			}
			for _, operation := range first.Payload.Operations {
				if operation.Kind != "configuration" || operation.Derived || operation.Value != nil {
					t.Fatal("incorrect public requested operation")
				}
			}
			if selector.name == "environment" {
				plan := previewConfigurationBatch(t, root, request, "production")
				var updated map[string]any
				if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{
					"config":    map[string]any{configurationImplementationOwner: map[string]any{"endpoint": ""}},
					"resources": map[string]any{"instances": map[string]any{"database.primary": map[string]any{"config": map[string]any{"endpoint": ""}}}},
				}
				if !reflect.DeepEqual(updated, want) {
					t.Fatal("batch copied root state into the sparse overlay")
				}
			}
		})
	}
}

func TestRunChangePlanConfigurationBatchRepairsMultipleSelectedValues(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
	document = strings.Replace(document, "public: PUBLIC_OLD", "public: [PRIVATE_INVALID]", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	value := configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_REPAIRED"`))
	public := configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_REPAIRED"`))
	runConfigurationPlan(t, root, value, false)
	runConfigurationPlan(t, root, public, false)
	runConfigurationPlan(t, root, configurationBatchRequest(t, value, public), true)
}

func TestRunChangePlanConfigurationBatchSuppliesNestedRequiredFieldsTogether(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	providerPath := filepath.Join(root, "original/provider.go")
	provider, err := os.ReadFile(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, providerPath, strings.Replace(string(provider), "Nested struct { Label string; Count uint64 }", "Nested struct { Label string `plystra:\"required\"`; Count uint64 `plystra:\"required\"` }", 1))
	document = strings.Replace(document, "nested: {label: PRIVATE_NESTED, count: 7}", "nested: {}", 1)
	document = strings.Replace(document, "endpoint: PRIVATE_RETAINED,", "endpoint: PRIVATE_RETAINED, nested: {label: PRIVATE_KEEP, count: 0},", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	label := configurationPlanRequest(t, "database.primary", "nested.label", "set", json.RawMessage(`""`))
	count := configurationPlanRequest(t, "database.primary", "nested.count", "set", json.RawMessage(`0`))
	runConfigurationPlan(t, root, label, false)
	runConfigurationPlan(t, root, count, false)
	runConfigurationPlan(t, root, configurationBatchRequest(t, label, count), true)
}

func TestRunChangePlanConfigurationBatchPreservesPrivateIdentityAndWriteIntent(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	request := configurationBatchRequest(t,
		configurationPlanRequest(t, "database.primary", "password", "set", json.RawMessage(`{"env":"PRIVATE_SECRET"}`)),
		configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_PRIMARY"`)),
		configurationPlanRequest(t, configurationImplementationOwner, "unused", "remove", nil),
	)
	plan := previewConfigurationBatch(t, root, request, "")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(plan.Updated()))
	plan = previewConfigurationBatch(t, root, request, "")
	if !plan.Changed() || !bytes.Equal(plan.Original(), plan.Updated()) {
		t.Fatal("identical-byte batch failed to declare write intent")
	}
	first := runConfigurationPlan(t, root, request, true)
	private := bytes.ReplaceAll(request, []byte("PRIVATE_SECRET"), []byte("PRIVATE_ANOTHER_SECRET"))
	private = bytes.ReplaceAll(private, []byte("PRIVATE_PRIMARY"), []byte("PRIVATE_DIFFERENT"))
	second := runConfigurationPlan(t, root, private, true)
	if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || len(first.Payload.Effects.Planned) != 1 || len(first.Effects.Planned) != 1 {
		t.Fatal("batch exposed private request equality or lost accumulated write intent")
	}
	changedTarget := bytes.ReplaceAll(request, []byte(`"unused"`), []byte(`"mapping"`))
	third := runConfigurationPlan(t, root, changedTarget, true)
	if first.Payload.RequestDigest == third.Payload.RequestDigest {
		t.Fatal("public request identity omitted a later no-op operation")
	}
	for _, public := range []string{"PUBLIC_A", "PUBLIC_B"} {
		request := configurationBatchRequest(t,
			configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_PRIMARY"`)),
			configurationPlanRequest(t, configurationImplementationOwner, "public", "set", json.RawMessage(fmt.Sprintf("%q", public))),
		)
		result := runConfigurationPlan(t, root, request, true)
		if public == "PUBLIC_A" {
			first = result
		} else if first.Payload.PlanDigest == result.Payload.PlanDigest || first.Payload.RequestDigest != result.Payload.RequestDigest {
			t.Fatal("build-visible batch identity is not bound to the candidate")
		}
	}
}

func TestRunChangePlanConfigurationBatchChecksNoOpsAndRejectsInvalidFinalStates(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	request := configurationBatchRequest(t,
		configurationPlanRequest(t, "database.primary", "unused", "remove", nil),
		configurationPlanRequest(t, configurationImplementationOwner, "unused", "remove", nil),
	)
	for _, selector := range [][]string{nil, {"--config", "deploy/customer.yaml"}} {
		result := runConfigurationPlan(t, root, request, true, selector...)
		if len(result.Payload.WriteSet) != 0 || len(result.Payload.Effects.Planned) != 0 || len(result.Effects.Planned) != 0 {
			t.Fatal("satisfied removals advertised a write")
		}
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {config: {unused: {$remove: true}}}}}\nconfig: {"+configurationImplementationOwner+": {unused: {$remove: true}}}\n")
	result := runConfigurationPlan(t, root, request, true, "--env", "production")
	if len(result.Payload.WriteSet) != 0 {
		t.Fatal("existing exact exclusions were rewritten")
	}
	for _, invalid := range []struct{ owner, path, action, value string }{
		{"database.absent", "value", "set", `"PRIVATE_NEW"`},
		{"database.primary", "private_unknown", "set", `"PRIVATE_NEW"`},
		{"database.primary", "endpoint", "remove", ""},
		{"database.primary", "value", "set", `["PRIVATE_INVALID"]`},
		{"database.primary", "pointer.label", "set", `"PRIVATE_NEW"`},
	} {
		var value json.RawMessage
		if invalid.value != "" {
			value = json.RawMessage(invalid.value)
		}
		runConfigurationPlan(t, root, configurationBatchRequest(t,
			configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_NEW"`)),
			configurationPlanRequest(t, invalid.owner, invalid.path, invalid.action, value),
		), false)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(document, "endpoint: PRIVATE_RETAINED, ", "", 1))
	runConfigurationPlan(t, root, request, false)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	runConfigurationPlan(t, root, configurationBatchRequest(t,
		configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_REPAIRED"`)),
		configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_NEW"`)),
	), false, "--env", "production")
}

func TestRunChangePlanConfigurationBatchRejectsMixedKindsWithoutDiscovery(t *testing.T) {
	configuration := configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`))
	for _, other := range []string{
		`{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"},{"kind":"dependency","action":"remove","module":"example.com/unused"}`,
		`{"kind":"resource_selection","action":"remove","instance":"database.primary"},{"kind":"dependency","action":"remove","module":"example.com/unused"}`,
		`{"kind":"implementation_selection","action":"remove","interface":"email.send/v1"},{"kind":"dependency","action":"remove","module":"example.com/unused"}`,
	} {
		request := configurationBatchRequest(t, configuration, []byte(`{"schema":"plystra.change/v1","operations":[`+other+`]}`))
		for _, request := range [][]byte{request, reverseConfigurationBatchRequest(t, request)} {
			root := t.TempDir()
			writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
			before := commandTree(t, root)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			var result struct {
				Status  string
				Payload json.RawMessage
				Effects struct{ Planned []json.RawMessage }
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || exit != 2 || result.Status != "unsupported" || stderr != "" || strings.Contains(stdout, "PRIVATE_") || len(result.Payload) != 0 && string(result.Payload) != "null" || len(result.Effects.Planned) != 0 {
				t.Fatalf("mixed request did not fail closed: %d\n%s\n%s", exit, stdout, stderr)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("unsupported mixed request changed files")
			}
		}
	}
}

func TestRunChangePlanConfigurationBatchAcceptsMaximumOperationCount(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	providerPath := filepath.Join(root, "original/provider.go")
	provider, err := os.ReadFile(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields strings.Builder
	var requests [][]byte
	for index := range changerequest.MaximumOperations {
		name := fmt.Sprintf("batch_%04d", index)
		fmt.Fprintf(&fields, "Batch%04d string `yaml:%q`; ", index, name)
		requests = append(requests, configurationPlanRequest(t, "database.primary", name, "set", json.RawMessage(`"PRIVATE_BATCH"`)))
	}
	writeCommandFile(t, providerPath, strings.Replace(string(provider), "type Config struct {", "type Config struct { "+fields.String(), 1))
	result := runConfigurationPlan(t, root, configurationBatchRequest(t, requests...), true)
	if len(result.Payload.Operations) != changerequest.MaximumOperations || len(result.Payload.WriteSet) != 1 {
		t.Fatal("maximum-sized batch lost operations or wrote more than one document")
	}
}

func TestConfigurationBatchPreviewRejectsInvalidRequestsBeforeDiscovery(t *testing.T) {
	valid, err := changerequest.Parse(configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[]}`))
	mixed, _ := changerequest.Parse(configurationBatchRequest(t, valid.CanonicalJSON(), []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}]}`)))
	for _, test := range []struct {
		ctx     context.Context
		request changerequest.Request
		options implementationselect.Options
	}{
		{t.Context(), changerequest.Request{}, implementationselect.Options{}},
		{t.Context(), empty, implementationselect.Options{}},
		{t.Context(), mixed, implementationselect.Options{}},
		{nil, valid, implementationselect.Options{}},
		{t.Context(), valid, implementationselect.Options{Constructor: "example.com/acme.New"}},
	} {
		plan, err := implementationselect.PreviewConfigurations(test.ctx, test.options, test.request)
		if !errors.Is(err, implementationselect.ErrSelect) || !reflect.DeepEqual(plan, implementationselect.PlanResult{}) {
			t.Fatalf("invalid batch returned a partial plan: %v", err)
		}
	}
}

func TestBuiltCLIChangePlanConfigurationBatchFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, _ := writeConfigurationPlanProject(t)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(configurationBatchRequest(t,
		configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)),
		configurationPlanRequest(t, configurationImplementationOwner, "value", "remove", nil),
	))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || bytes.Contains(output, []byte("PRIVATE_")) || result.Status != "success" || len(result.Payload.Operations) != 2 {
		t.Fatalf("compiled batch: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled batch modified Project files")
	}
}

func configurationBatchRequest(t testing.TB, requests ...[]byte) []byte {
	t.Helper()
	var operations []json.RawMessage
	for _, request := range requests {
		var document struct{ Operations []json.RawMessage }
		if err := json.Unmarshal(request, &document); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, document.Operations...)
	}
	data, err := json.Marshal(struct {
		Schema     string            `json:"schema"`
		Operations []json.RawMessage `json:"operations"`
	}{"plystra.change/v1", operations})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func reverseConfigurationBatchRequest(t testing.TB, request []byte) []byte {
	t.Helper()
	var document struct {
		Schema     string            `json:"schema"`
		Operations []json.RawMessage `json:"operations"`
	}
	if err := json.Unmarshal(request, &document); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(document.Operations)
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func previewConfigurationBatch(t *testing.T, root string, requestBytes []byte, environment string) implementationselect.PlanResult {
	t.Helper()
	request, err := changerequest.Parse(requestBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := implementationselect.PreviewConfigurations(t.Context(), implementationselect.Options{Start: root, EnvironmentName: environment, Environment: implementationSelectionCommandEnvironment(nil)}, request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
