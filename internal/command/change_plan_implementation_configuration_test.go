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
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

const compoundImplementationOwner = "example.com/acme/implementation-use/local.New"

func TestRunChangePlanImplementationConfigurationUsesOnlyFinalDocumentBounds(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	padding := applicationmeta.MaximumSize - len(document) + len("PRIVATE_IMPLEMENTATION") - 32
	document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION,", "value: "+strings.Repeat("x", padding)+",", 1)
	if len(document) != applicationmeta.MaximumSize-32 {
		t.Fatal("fixture does not approach the captured document bound")
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
		configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	runConfigurationPlan(t, root, request, true)
}

func TestRunChangePlanImplementationConfigurationPreservesAncestorExclusions(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	overlay := "resources: {instances: {database.primary: {config: {nested: {$remove: true}}}}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlay)
	request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
		configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)),
		configurationPlanRequest(t, "database.primary", "nested.label", "remove", nil))
	plan := previewImplementationConfiguration(t, root, request, "--env", "production")
	if bytes.Contains(plan.Updated(), []byte("label:")) || !bytes.Contains(plan.Updated(), []byte("$remove: true")) {
		t.Fatal("descendant removal widened an ancestor exclusion")
	}
	runConfigurationPlan(t, root, request, true, "--env", "production")
	invalid := configurationBatchRequest(t, request, configurationPlanRequest(t, "database.primary", "endpoint", "remove", nil))
	runConfigurationPlan(t, root, invalid, false, "--env", "production")
}

func TestRunChangePlanImplementationConfigurationRepairsJointRequiredFields(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	path := filepath.Join(root, "local/implementation.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "Value string;", "Value string `plystra:\"required\"`;", 1))
	selection := implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner)
	endpoint := configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`""`))
	value := configurationPlanRequest(t, compoundImplementationOwner, "value", "set", json.RawMessage(`""`))
	runConfigurationPlan(t, root, configurationBatchRequest(t, selection, endpoint), false)
	runConfigurationPlan(t, root, configurationBatchRequest(t, selection, value), false)
	request := configurationBatchRequest(t, selection, endpoint, value)
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) {
		t.Fatal("joint required-field repair depends on operation order")
	}
	// A satisfied selection and removal must not hide an invalid required field.
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(string(previewImplementationConfiguration(t, root, request).Updated()), "endpoint: \"\"", "endpoint: [PRIVATE_INVALID]", 1))
	runConfigurationPlan(t, root, configurationBatchRequest(t, selection, configurationPlanRequest(t, compoundImplementationOwner, "unused", "remove", nil)), false)
}

func TestRunChangePlanImplementationConfigurationUsesFinalOwnership(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    implementations:\n      "+configurationImplementationOwner+": {Obsolete: database.primary}\n", 1)
			path, args := "plystra.yaml", []string(nil)
			switch mode {
			case "environment":
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args = "plystra.production.yaml", []string{"--env", "production"}
				document = "# KEEP_OVERLAY\nconfig: {" + configurationImplementationOwner + ": {value: [PRIVATE_INVALID]}}\nresources: {bind: {implementations: {" + configurationImplementationOwner + ": {Local: database.retained, Preserve: {$remove: true}}}}}\n"
			case "replacement":
				path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			if mode != "environment" {
				document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION", "value: [PRIVATE_INVALID]", 1)
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
				configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`""`)),
				configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
			first := runConfigurationPlan(t, root, request, true, args...)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
			if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != path || string(first.Payload.ConfigurationImpact["unresolved_decisions"]) != "[]" {
				t.Fatal("compound preview lost final-state identity or scope")
			}
			if !bytes.Contains(first.Payload.ConfigurationImpact["newly_unowned_paths"], []byte(configurationImplementationOwner)) || !bytes.Contains(first.Payload.ConfigurationImpact["derived_operations"], []byte("Obsolete")) {
				t.Fatal("proven owner or exact binding cleanup is missing")
			}
			plan := previewImplementationConfiguration(t, root, request, args...)
			var updated map[string]any
			if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
				t.Fatal(err)
			}
			configs := updated["config"].(map[string]any)
			if !reflect.DeepEqual(configs[compoundImplementationOwner], map[string]any{"endpoint": ""}) {
				t.Fatal("new owner Config was not prepared against its final schema")
			}
			if mode == "environment" {
				want := map[string]any{
					"config":     map[string]any{configurationImplementationOwner: map[string]any{"$remove": true}, compoundImplementationOwner: map[string]any{"endpoint": ""}},
					"interfaces": map[string]any{"use": map[string]any{"email.send/v1": compoundImplementationOwner}},
					"resources": map[string]any{
						"instances": map[string]any{"database.primary": map[string]any{"config": map[string]any{"value": "PRIVATE_NEW"}}},
						"bind":      map[string]any{"implementations": map[string]any{configurationImplementationOwner: map[string]any{"Obsolete": map[string]any{"$remove": true}, "Preserve": map[string]any{"$remove": true}}}},
					},
				}
				if !reflect.DeepEqual(updated, want) || !bytes.Contains(plan.Updated(), []byte("# KEEP_OVERLAY")) {
					t.Fatal("compound edit widened cleanup or materialized lower-layer values")
				}
			} else if _, exists := configs[configurationImplementationOwner]; exists {
				t.Fatal("obsolete selected Config survived")
			}
		})
	}
}

func TestRunChangePlanImplementationConfigurationRetainsSharedAndDormantOwners(t *testing.T) {
	for _, dormant := range []bool{false, true} {
		root, document := writeConfigurationPlanProject(t)
		writeCommandInterface(t, root, "email/copy/v1", "copy", "email.copy/v1", "Copy")
		path := filepath.Join(root, "smtp/implementation.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		smtp := strings.Replace(string(data), "//plystra:implements email.send/v1", "//plystra:implements email.send/v1\n//plystra:implements email.copy/v1", 1)
		smtp = strings.Replace(smtp, `contract "example.com/acme/implementation-use/interfaces/email/send/v1"`, "copyapi \"example.com/acme/implementation-use/interfaces/email/copy/v1\"\n contract \"example.com/acme/implementation-use/interfaces/email/send/v1\"", 1)
		writeCommandFile(t, path, smtp+"\nfunc (*Service) Copy(context.Context,copyapi.Request)(copyapi.Response,error){return copyapi.Response{},nil}\n")
		document = strings.Replace(document, "use: {email.send/v1: "+configurationImplementationOwner+"}", "use: {email.send/v1: "+configurationImplementationOwner+", email.copy/v1: "+configurationImplementationOwner+"}", 1)
		if dormant {
			document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
		} else {
			document = strings.Replace(document, "require: [email.send/v1]", "require: [email.send/v1, email.copy/v1]", 1)
		}
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
		request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
			configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)),
			configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
		previewImplementationConfiguration(t, root, request)
		result := runConfigurationPlan(t, root, request, true)
		if !bytes.Contains(result.Payload.ConfigurationImpact["retained_owners"], []byte(configurationImplementationOwner)) || string(result.Payload.ConfigurationImpact["newly_unowned_paths"]) != "[]" {
			t.Fatal("another explicit choice did not retain the old owner")
		}
	}
}

func TestRunChangePlanImplementationConfigurationRejectsEmptyRequest(t *testing.T) {
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[]}`)
	exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, nil)
	var result struct {
		Status  string
		Payload json.RawMessage
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || exit != 2 || result.Status != "unsupported" || stderr != "" || string(result.Payload) != "null" {
		t.Fatalf("empty request did not fail before discovery: %d\n%s\n%s", exit, stdout, stderr)
	}
}

func TestRunChangePlanImplementationConfigurationRemovalAndInvalidFinalStates(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "remove", ""), configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	// The remaining unique compatible constructor is an ordinary fallback owner.
	path := filepath.Join(root, "smtp/implementation.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "//plystra:implements email.send/v1\n", "", 1))
	runConfigurationPlan(t, root, request, true)
	for _, change := range [][]byte{
		configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_ORPHAN"`)),
		configurationPlanRequest(t, configurationImplementationOwner, "value", "remove", nil),
		configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "remove", nil),
		configurationPlanRequest(t, compoundImplementationOwner, "pointer.label", "set", json.RawMessage(`"PRIVATE_INVALID"`)),
	} {
		runConfigurationPlan(t, root, configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "remove", ""), change), false)
	}
	// An invalid root field cannot be repaired only by a selected overlay.
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	runConfigurationPlan(t, root, configurationBatchRequest(t, request, configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_REPAIR"`))), false, "--env", "production")
}

func TestRunChangePlanImplementationConfigurationPrivateIdentityAndNoOps(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner),
		configurationPlanRequest(t, configurationImplementationOwner, "password", "set", json.RawMessage(`{"env":"PRIVATE_SECRET"}`)),
		configurationPlanRequest(t, configurationImplementationOwner, "unused", "remove", nil))
	plan := previewImplementationConfiguration(t, root, request)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(plan.Updated()))
	plan = previewImplementationConfiguration(t, root, request)
	if !plan.Changed() || !bytes.Equal(plan.Original(), plan.Updated()) {
		t.Fatal("equal-byte set lost write intent")
	}
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, bytes.ReplaceAll(request, []byte("PRIVATE_SECRET"), []byte("PRIVATE_OTHER")), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 {
		t.Fatal("compound preview exposed private equality")
	}
	third := runConfigurationPlan(t, root, bytes.ReplaceAll(request, []byte(`"unused"`), []byte(`"mapping"`)), true)
	if first.Payload.RequestDigest == third.Payload.RequestDigest {
		t.Fatal("satisfied later removal was not digest-bound")
	}
	noOp := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner), configurationPlanRequest(t, configurationImplementationOwner, "unused", "remove", nil))
	result := runConfigurationPlan(t, root, noOp, true)
	if len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
		t.Fatal("satisfied compound request declared a write")
	}
	for _, value := range []string{"PUBLIC_A", "PUBLIC_B"} {
		result := runConfigurationPlan(t, root, configurationBatchRequest(t, noOp, configurationPlanRequest(t, configurationImplementationOwner, "public", "set", json.RawMessage(fmt.Sprintf("%q", value)))), true)
		if value == "PUBLIC_A" {
			first = result
		} else if first.Payload.PlanDigest == result.Payload.PlanDigest || first.Payload.RequestDigest != result.Payload.RequestDigest {
			t.Fatal("build-visible final identity is incorrect")
		}
	}
}

func TestImplementationConfigurationPreviewRejectsInvalidShapeBeforeDiscovery(t *testing.T) {
	configuration := configurationPlanRequest(t, compoundImplementationOwner, "value", "remove", nil)
	selection := implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner)
	valid, err := changerequest.Parse(configurationBatchRequest(t, configuration, selection))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{configuration, selection, configurationBatchRequest(t, configuration, selection, []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"remove","instance":"database.primary"}]}`))} {
		request, _ := changerequest.Parse(input)
		plan, err := implementationselect.PreviewImplementationConfigurations(t.Context(), implementationselect.Options{}, request)
		if !errors.Is(err, implementationselect.ErrSelect) || !reflect.DeepEqual(plan, implementationselect.PlanResult{}) {
			t.Fatal("invalid shape returned a plan")
		}
	}
	for _, test := range []struct {
		ctx     context.Context
		options implementationselect.Options
	}{
		{nil, implementationselect.Options{}}, {t.Context(), implementationselect.Options{Constructor: compoundImplementationOwner}}, {t.Context(), implementationselect.Options{Target: "email.send/v1"}},
	} {
		if _, err := implementationselect.PreviewImplementationConfigurations(test.ctx, test.options, valid); !errors.Is(err, implementationselect.ErrSelect) {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestRunChangePlanImplementationConfigurationAcceptsMaximumRequestWithCleanup(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	path := filepath.Join(root, "local/implementation.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requests := [][]byte{implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`))}
	var fields strings.Builder
	for index := range changerequest.MaximumOperations - 2 {
		name := fmt.Sprintf("batch_%04d", index)
		fmt.Fprintf(&fields, "Batch%04d string `yaml:%q`; ", index, name)
		requests = append(requests, configurationPlanRequest(t, compoundImplementationOwner, name, "set", json.RawMessage(`"PRIVATE_BATCH"`)))
	}
	writeCommandFile(t, path, strings.Replace(string(data), "type Config struct {", "type Config struct { "+fields.String(), 1))
	result := runConfigurationPlan(t, root, configurationBatchRequest(t, requests...), true)
	requested, derived := 0, 0
	for _, operation := range result.Payload.Operations {
		if operation.Derived {
			derived++
		} else {
			requested++
		}
	}
	if requested != changerequest.MaximumOperations || derived == 0 || len(result.Payload.WriteSet) != 1 {
		t.Fatal("maximum request lost requested or proven derived operations")
	}
}

func TestBuiltCLIChangePlanImplementationConfigurationFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	root, _ := writeConfigurationPlanProject(t)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`))))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || bytes.Contains(output, []byte("PRIVATE_")) {
		t.Fatalf("compiled preview: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled preview changed Project")
	}
}

func implementationConfigurationSelectionRequest(t testing.TB, action, constructor string) []byte {
	t.Helper()
	operation := map[string]any{"kind": "implementation_selection", "action": action, "interface": "email.send/v1"}
	if constructor != "" {
		operation["constructor"] = constructor
	}
	data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": []any{operation}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func previewImplementationConfiguration(t *testing.T, root string, data []byte, selector ...string) implementationselect.PlanResult {
	t.Helper()
	request, err := changerequest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	options := implementationselect.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}
	if len(selector) == 2 {
		if selector[0] == "--env" {
			options.EnvironmentName = selector[1]
		} else {
			options.ConfigurationPath = selector[1]
		}
	}
	plan, err := implementationselect.PreviewImplementationConfigurations(t.Context(), options, request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
