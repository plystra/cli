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

const compoundResourceProvider = "example.com/acme/implementation-use/replacement.New"
const originalCompoundResourceProvider = "example.com/acme/implementation-use/original.New"

func TestRunChangePlanResourceConfigurationUsesFinalProviderAndExactCleanup(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			path := filepath.Join(root, "original/provider.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(string(data), "import ", "import database \"example.com/acme/implementation-use/database\"\nimport ", 1)
			writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(c Config, Obsolete database.Resource)", 1))
			writeCommandFile(t, filepath.Join(root, "replacement/provider.go"), "package replacement\ntype Config struct { Endpoint string `plystra:\"required\"`; Value string `plystra:\"required\"` }\ntype Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New(Config)(*Value,error){panic(\"must not execute\")}\nfunc (*Value) Read(){}\n")
			document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {Obsolete: database.retained}\n      database.retained: {Obsolete: database.primary}\n", 1)
			selected, args := "plystra.yaml", []string(nil)
			switch mode {
			case "environment":
				writeCommandFile(t, filepath.Join(root, selected), document)
				selected, args = "plystra.production.yaml", []string{"--env", "production"}
				document = "# KEEP_OVERLAY\nresources: {instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}}, bind: {instances: {database.primary: {Obsolete: database.retained, Preserve: {$remove: true}}}}}\n"
			case "replacement":
				selected, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			if mode != "environment" {
				document = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
			}
			writeCommandFile(t, filepath.Join(root, selected), document)
			selection := resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider)
			endpoint := configurationPlanRequest(t, "database.primary", "endpoint", "set", json.RawMessage(`""`))
			value := configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`))
			runConfigurationPlan(t, root, configurationBatchRequest(t, selection, endpoint), false, args...)
			request := configurationBatchRequest(t, selection, endpoint, value, configurationPlanRequest(t, configurationImplementationOwner, "value", "remove", nil))
			first := runConfigurationPlan(t, root, request, true, args...)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
			if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != selected || string(first.Payload.ConfigurationImpact["unresolved_decisions"]) != "[]" {
				t.Fatal("compound Resource preview lost final-state identity or scope")
			}
			if !bytes.Contains(first.Payload.ConfigurationImpact["derived_operations"], []byte("Obsolete")) || !bytes.Contains(first.Payload.ConfigurationImpact["newly_unowned_paths"], []byte("database.primary")) || !bytes.Contains(first.Payload.ConfigurationImpact["retained_owners"], []byte("database.retained")) {
				t.Fatal("exact binding cleanup or retained instance evidence missing")
			}
			plan := previewResourceConfiguration(t, root, request, args...)
			var updated map[string]any
			if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
				t.Fatal(err)
			}
			resources := updated["resources"].(map[string]any)
			instances := resources["instances"].(map[string]any)
			if !reflect.DeepEqual(instances["database.primary"], map[string]any{"use": compoundResourceProvider, "config": map[string]any{"endpoint": "", "value": "PRIVATE_NEW"}}) {
				t.Fatal("new Config inherited obsolete provider fields")
			}
			if mode == "environment" {
				want := map[string]any{
					"instances": map[string]any{"database.primary": instances["database.primary"]},
					"bind":      map[string]any{"instances": map[string]any{"database.primary": map[string]any{"Obsolete": map[string]any{"$remove": true}, "Preserve": map[string]any{"$remove": true}}}},
				}
				if !reflect.DeepEqual(resources, want) || !bytes.Contains(plan.Updated(), []byte("# KEEP_OVERLAY")) {
					t.Fatal("overlay cleanup widened intent or copied root values")
				}
			} else if bindings, exists := resources["bind"].(map[string]any)["instances"].(map[string]any)["database.primary"]; exists {
				if _, exists := bindings.(map[string]any)["Obsolete"]; exists {
					t.Fatal("obsolete outgoing binding survived complete-document cleanup")
				}
			}
		})
	}
}

func TestRunChangePlanResourceConfigurationFirstAssignmentAndReversion(t *testing.T) {
	for _, scenario := range []string{"complete-unbound", "overlay-local-unbound", "overlay-root-unbound", "root-reversion", "config-tombstone"} {
		t.Run(scenario, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			args := []string{"--env", "production"}
			provider := originalCompoundResourceProvider
			overlay := "# KEEP\nresources: {instances: {database.primary: {config: {value: PRIVATE_LOCAL}}}}\n"
			switch scenario {
			case "complete-unbound", "overlay-local-unbound", "overlay-root-unbound":
				document = strings.Replace(document, "      use: "+provider+"\n", "", 1)
				if scenario == "complete-unbound" {
					args = nil
				} else if scenario == "overlay-root-unbound" {
					overlay = "{}\n"
				} else {
					overlay = "# KEEP\nresources: {instances: {database.primary: {config: {endpoint: PRIVATE_LOCAL_REQUIRED, value: PRIVATE_LOCAL}}}}\n"
				}
			case "root-reversion":
				overlay = "# KEEP\nresources: {instances: {database.primary: {use: " + compoundResourceProvider + ", config: {value: [PRIVATE_INVALID]}}}}\n"
			case "config-tombstone":
				provider = compoundResourceProvider
				overlay = "# KEEP\nresources: {instances: {database.primary: {config: {$remove: true}}}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlay)
			request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", provider), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
			if scenario == "overlay-root-unbound" {
				// Unbound lower-layer Config cannot cross the new provider boundary.
				runConfigurationPlan(t, root, request, false, args...)
				request = configurationBatchRequest(t, request, configurationPlanRequest(t, "database.primary", "endpoint", "set", json.RawMessage(`"PRIVATE_NEW_REQUIRED"`)))
			}
			plan := previewResourceConfiguration(t, root, request, args...)
			candidate, err := plan.Inputs().ComposeCandidate(plan.Updated())
			if err != nil {
				t.Fatal(err)
			}
			for _, instance := range candidate.Manifest().ResourceInstances() {
				if instance.Name() == "database.primary" {
					if instance.Provider().String() != provider {
						t.Fatal("wrong final provider")
					}
				}
			}
			if scenario == "root-reversion" && bytes.Contains(plan.Updated(), []byte("endpoint:")) {
				t.Fatal("reversion materialized same-provider root Config")
			}
			runConfigurationPlan(t, root, request, true, args...)
		})
	}
}

func TestRunChangePlanResourceConfigurationRemovalAndOrphanProtection(t *testing.T) {
	for _, scenario := range []string{"existing", "unbound", "absent", "excluded", "incoming"} {
		t.Run(scenario, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			args := []string(nil)
			switch scenario {
			case "unbound":
				document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
			case "absent":
				start, end := strings.Index(document, "    database.primary:"), strings.Index(document, "    database.retained:")
				document = document[:start] + document[end:]
			case "excluded":
				args = []string{"--env", "production"}
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
			case "incoming":
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    implementations:\n      "+configurationImplementationOwner+": {Primary: database.primary}\n", 1)
				path := filepath.Join(root, "smtp/implementation.go")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				source := strings.Replace(string(data), "import (", "import (\n database \"example.com/acme/implementation-use/database\"", 1)
				writeCommandFile(t, path, strings.Replace(source, "New(Config)", "New(c Config, Primary database.Resource)", 1))
			}
			if scenario != "incoming" {
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {Orphan: database.retained}\n", 1)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			selection := resourceConfigurationSelectionRequest(t, "database.primary", "remove", "")
			request := configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.retained", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
			if scenario == "incoming" {
				runConfigurationPlan(t, root, configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider), configurationPlanRequest(t, "database.retained", "value", "set", json.RawMessage(`"PRIVATE_NEW"`))), true)
			}
			runConfigurationPlan(t, root, request, scenario == "existing" || scenario == "unbound", args...)
			for _, action := range []string{"set", "remove"} {
				var value json.RawMessage
				if action == "set" {
					value = json.RawMessage(`"PRIVATE_ORPHAN"`)
				}
				runConfigurationPlan(t, root, configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "value", action, value)), false, args...)
			}
		})
	}
}

func TestRunChangePlanResourceConfigurationRejectsInvalidFinalStates(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	selection := resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider)
	for _, change := range [][]byte{
		configurationPlanRequest(t, "database.primary", "endpoint", "remove", nil),
		configurationPlanRequest(t, "database.primary", "pointer.label", "set", json.RawMessage(`"PRIVATE_INVALID"`)),
		configurationPlanRequest(t, "database.primary", "number", "set", json.RawMessage(`-1`)),
	} {
		runConfigurationPlan(t, root, configurationBatchRequest(t, selection, change), false)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	runConfigurationPlan(t, root, configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "unused", "remove", nil)), false)
	runConfigurationPlan(t, root, configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_REPAIR"`))), false, "--env", "production")
	for _, instance := range []string{"database.absent", "database.primary"} {
		writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
		runConfigurationPlan(t, root, configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, instance, "set", compoundResourceProvider), configurationPlanRequest(t, "database.retained", "value", "remove", nil)), false, "--env", "production")
	}
}

func TestRunChangePlanResourceConfigurationPrivateIdentityAndNoOps(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	selection := resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider)
	request := configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "password", "set", json.RawMessage(`{"env":"PRIVATE_SECRET"}`)))
	plan := previewResourceConfiguration(t, root, request)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(plan.Updated()))
	plan = previewResourceConfiguration(t, root, request)
	if !plan.Changed() || !bytes.Equal(plan.Original(), plan.Updated()) {
		t.Fatal("equal-byte set lost write intent")
	}
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, bytes.ReplaceAll(request, []byte("PRIVATE_SECRET"), []byte("PRIVATE_OTHER")), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 {
		t.Fatal("compound Resource preview exposed Secret target equality")
	}
	private := configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_A"`)))
	first = runConfigurationPlan(t, root, private, true)
	second = runConfigurationPlan(t, root, bytes.ReplaceAll(private, []byte("PRIVATE_A"), []byte("PRIVATE_B")), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) {
		t.Fatal("compound Resource preview exposed private value equality")
	}
	noOp := configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "unused", "remove", nil))
	result := runConfigurationPlan(t, root, noOp, true)
	if len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
		t.Fatal("satisfied compound request declared a write")
	}
	other := runConfigurationPlan(t, root, bytes.ReplaceAll(noOp, []byte(`"unused"`), []byte(`"mapping"`)), true)
	if result.Payload.RequestDigest == other.Payload.RequestDigest {
		t.Fatal("satisfied field removal not bound into request identity")
	}
	for _, value := range []string{"PUBLIC_A", "PUBLIC_B"} {
		result := runConfigurationPlan(t, root, configurationBatchRequest(t, selection, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(fmt.Sprintf("%q", value)))), true)
		if value == "PUBLIC_A" {
			first = result
		} else if first.Payload.PlanDigest == result.Payload.PlanDigest || first.Payload.RequestDigest != result.Payload.RequestDigest {
			t.Fatal("build-visible final identity is incorrect")
		}
	}
}

func TestRunChangePlanResourceConfigurationDoesNotInventBindingOwnership(t *testing.T) {
	for _, scenario := range []string{"unknown-old", "unknown-old-binding", "first-binding", "same-binding", "changed-binding", "incompatible"} {
		t.Run(scenario, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			provider := compoundResourceProvider
			switch scenario {
			case "unknown-old", "unknown-old-binding":
				document = strings.Replace(document, originalCompoundResourceProvider, "example.com/acme/implementation-use/absent.New", 1)
			case "first-binding":
				provider = originalCompoundResourceProvider
				document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
			case "same-binding":
				provider = originalCompoundResourceProvider
			case "incompatible":
				provider = "example.com/acme/implementation-use/different.New"
			}
			if strings.HasSuffix(scenario, "binding") {
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {Undeclared: database.retained}\n", 1)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", provider), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
			runConfigurationPlan(t, root, request, scenario == "unknown-old")
		})
	}
}

func TestRunChangePlanResourceConfigurationRemovalHasNoSatisfiedWrite(t *testing.T) {
	for _, environment := range []bool{false, true} {
		root, _ := writeConfigurationPlanProject(t)
		path, args := "plystra.yaml", []string(nil)
		if environment {
			path, args = "plystra.production.yaml", []string{"--env", "production"}
		}
		request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "remove", ""), configurationPlanRequest(t, "database.retained", "unused", "remove", nil))
		plan := previewResourceConfiguration(t, root, request, args...)
		writeCommandFile(t, filepath.Join(root, path), string(plan.Updated()))
		result := runConfigurationPlan(t, root, request, true, args...)
		if len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
			t.Fatal("satisfied compound removal declared a write")
		}
	}
}

func TestRunChangePlanResourceConfigurationUsesOnlyFinalDocumentBounds(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "      use: "+originalCompoundResourceProvider+"\n", "", 1)
	padding := applicationmeta.MaximumSize - len(document) + len("PRIVATE_PRIMARY") - 16
	document = strings.Replace(document, "value: PRIVATE_PRIMARY,", "value: "+strings.Repeat("x", padding)+",", 1)
	if len(document) != applicationmeta.MaximumSize-16 {
		t.Fatal("fixture does not approach captured document bound")
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	request := configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	runConfigurationPlan(t, root, request, true)
}

func TestRunChangePlanResourceConfigurationAcceptsMaximumRequestWithCleanup(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	path := filepath.Join(root, "replacement/provider.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	requests := [][]byte{resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider)}
	var fields strings.Builder
	for index := range changerequest.MaximumOperations - 1 {
		name := fmt.Sprintf("batch_%04d", index)
		fmt.Fprintf(&fields, "Batch%04d string `yaml:%q`; ", index, name)
		requests = append(requests, configurationPlanRequest(t, "database.primary", name, "set", json.RawMessage(`"PRIVATE_BATCH"`)))
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
		t.Fatal("maximum request lost requested or derived operations")
	}
}

func TestResourceConfigurationPreviewRejectsInvalidShapeBeforeDiscovery(t *testing.T) {
	configuration := configurationPlanRequest(t, "database.primary", "value", "remove", nil)
	selection := resourceConfigurationSelectionRequest(t, "database.primary", "set", originalCompoundResourceProvider)
	valid, err := changerequest.Parse(configurationBatchRequest(t, configuration, selection))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{configuration, selection, configurationBatchRequest(t, configuration, selection, resourceConfigurationSelectionRequest(t, "database.retained", "remove", ""))} {
		request, _ := changerequest.Parse(data)
		plan, err := implementationselect.PreviewResourceConfigurations(t.Context(), implementationselect.Options{}, request)
		if !errors.Is(err, implementationselect.ErrSelect) || !reflect.DeepEqual(plan, implementationselect.PlanResult{}) {
			t.Fatal("invalid shape returned a partial plan")
		}
	}
	for _, test := range []struct {
		ctx     context.Context
		options implementationselect.Options
	}{{nil, implementationselect.Options{}}, {t.Context(), implementationselect.Options{Target: "database.primary"}}, {t.Context(), implementationselect.Options{Constructor: originalCompoundResourceProvider}}} {
		if _, err := implementationselect.PreviewResourceConfigurations(test.ctx, test.options, valid); !errors.Is(err, implementationselect.ErrSelect) {
			t.Fatal("invalid options accepted")
		}
	}
}

func TestBuiltCLIChangePlanResourceConfigurationFromStdin(t *testing.T) {
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
	command.Stdin = bytes.NewReader(configurationBatchRequest(t, resourceConfigurationSelectionRequest(t, "database.primary", "set", compoundResourceProvider), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`))))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || bytes.Contains(output, []byte("PRIVATE_")) || result.Status != "success" {
		t.Fatalf("compiled compound preview: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled preview changed Project bytes")
	}
}

func resourceConfigurationSelectionRequest(t testing.TB, instance, action, provider string) []byte {
	t.Helper()
	operation := map[string]any{"kind": "resource_selection", "instance": instance, "action": action}
	if provider != "" {
		operation["provider"] = provider
	}
	data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": []any{operation}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func previewResourceConfiguration(t *testing.T, root string, data []byte, selector ...string) implementationselect.PlanResult {
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
	plan, err := implementationselect.PreviewResourceConfigurations(t.Context(), options, request)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
