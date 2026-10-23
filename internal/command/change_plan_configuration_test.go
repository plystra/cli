package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

type configurationChangePlanResult struct {
	Status  string
	Payload struct {
		RequestDigest    string `json:"request_digest"`
		PlanDigest       string `json:"plan_digest"`
		FinalModelDigest string `json:"final_model_digest"`
		Completeness     string
		WriteSet         []struct {
			Path         string
			BeforeDigest string `json:"before_digest"`
			AfterDigest  string `json:"after_digest"`
		} `json:"write_set"`
		Operations []struct {
			Kind, Action, Owner, Path string
			Derived                   bool
			Value                     json.RawMessage
		}
		ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
		Effects             struct{ Planned []json.RawMessage }
	}
	Effects struct{ Planned []json.RawMessage }
}

const configurationImplementationOwner = "example.com/acme/implementation-use/smtp.New"

func TestRunChangePlanConfigurationHelpDescribesInstalledBounds(t *testing.T) {
	for _, args := range [][]string{{"change", "--help"}, {"change", "plan", "--help"}} {
		exit, stdout, stderr := runCommand(t, args, t.TempDir(), nil)
		if exit != 0 || stderr != "" {
			t.Fatalf("change help: %d, %s", exit, stderr)
		}
		for _, phrase := range []string{"configuration set/remove", "at most 1024 requested operations", "owner-relative declared field path", "non-pointer fixed objects", "already-satisfied removals", "Public request identity omits submitted values", "even for equal values", "non_exact and cannot authorize apply", "Same-owner ancestor/descendant targets conflict", "batches of interface_root present/absent", "implementation_selection set/remove and configuration set/remove", "Interface batches and combined Resource/configuration previews require a valid complete final state", "Other mixed operation kinds remain unsupported", "derived operations separately"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("help omits %q", phrase)
			}
		}
	}
}

func TestRunChangePlanConfigurationUsesTypedOwnersAndSelectedLayer(t *testing.T) {
	for _, owner := range []string{configurationImplementationOwner, "database.primary"} {
		for _, selector := range []struct {
			name, path string
			args       []string
		}{
			{"default", "plystra.yaml", nil},
			{"environment", "plystra.production.yaml", []string{"--env", "production"}},
			{"replacement", "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}},
		} {
			for _, action := range []string{"set", "remove"} {
				t.Run(owner+"/"+selector.name+"/"+action, func(t *testing.T) {
					root, _ := writeConfigurationPlanProject(t)
					value := json.RawMessage(`"PRIVATE_NEW"`)
					if action == "remove" {
						value = nil
					}
					request := configurationPlanRequest(t, owner, "value", action, value)
					result := runConfigurationPlan(t, root, request, true, selector.args...)
					if len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != selector.path || len(result.Payload.Operations) != 1 {
						t.Fatalf("wrong write scope or operations: %#v", result.Payload)
					}
					operation := result.Payload.Operations[0]
					if operation.Kind != "configuration" || operation.Action != action || operation.Owner != owner || operation.Path != "value" || operation.Derived || operation.Value != nil {
						t.Fatalf("incorrect public operation: %#v", operation)
					}
					for _, name := range []string{"newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
						if string(result.Payload.ConfigurationImpact[name]) != "[]" {
							t.Fatalf("requested field edit became owner cleanup: %s", result.Payload.ConfigurationImpact[name])
						}
					}
				})
			}
		}
	}
}

func TestRunChangePlanConfigurationDoesNotExposePrivateRequestEquality(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	for _, test := range []struct {
		path   string
		values []json.RawMessage
	}{
		{"value", []json.RawMessage{json.RawMessage(`"PRIVATE_PRIMARY"`), json.RawMessage(`"PRIVATE_NEW"`), json.RawMessage(`"PRIVATE_ANOTHER"`)}},
		{"password", []json.RawMessage{json.RawMessage(`{"env":"PRIVATE_SECRET"}`), json.RawMessage(`{"env":"PRIVATE_ANOTHER_SECRET"}`)}},
		{"pointer", []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"label":"PRIVATE_POINTER"}`)}},
	} {
		var first configurationChangePlanResult
		for index, value := range test.values {
			request := configurationPlanRequest(t, "database.primary", test.path, "set", value)
			result := runConfigurationPlan(t, root, request, true)
			parsed, err := changerequest.Parse(request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Payload.RequestDigest == parsed.Digest() || len(result.Payload.WriteSet) != 1 || len(result.Payload.Effects.Planned) != 1 || len(result.Effects.Planned) != 1 {
				t.Fatal("private request digest or value equality exposed")
			}
			if index == 0 {
				first = result
			} else if !reflect.DeepEqual(first.Payload, result.Payload) {
				t.Fatalf("private-only request changed public payload for %s", test.path)
			}
		}
	}
	first := runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_A"`)), true)
	second := runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "public", "set", json.RawMessage(`"PUBLIC_B"`)), true)
	if first.Payload.PlanDigest == second.Payload.PlanDigest || first.Payload.RequestDigest != second.Payload.RequestDigest {
		t.Fatal("public-safe request projection or build-visible final identity is incorrect")
	}
	var numeric configurationChangePlanResult
	for index, value := range []string{"1", "1.0", "1e0"} {
		result := runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "number", "set", json.RawMessage(value)), true)
		if index == 0 {
			numeric = result
		} else if !reflect.DeepEqual(numeric.Payload, result.Payload) {
			t.Fatal("equivalent numbers changed public identity")
		}
	}
}

func TestConfigurationPreviewSetDeclaresWriteForIdenticalBytes(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	requestBytes := configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_PRIMARY"`))
	request, err := changerequest.Parse(requestBytes)
	if err != nil {
		t.Fatal(err)
	}
	change, _ := request.Operations()[0].Configuration()
	options := implementationselect.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}
	plan, err := implementationselect.PreviewConfiguration(t.Context(), options, change)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(plan.Updated()))
	plan, err = implementationselect.PreviewConfiguration(t.Context(), options, change)
	if err != nil || !bytes.Equal(plan.Original(), plan.Updated()) || !plan.Changed() {
		t.Fatalf("identical-byte set disclosed private equality: %v", err)
	}
	same := runConfigurationPlan(t, root, requestBytes, true)
	different := runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_DIFFERENT"`)), true)
	if len(same.Payload.WriteSet) != 1 || !reflect.DeepEqual(same.Payload, different.Payload) {
		t.Fatal("identical-byte set changed public effects or identity")
	}
}

func TestRunChangePlanConfigurationRejectsInvalidFinalStates(t *testing.T) {
	for _, mode := range []string{"unknown owner", "unowned constructor", "unknown field", "map traversal", "pointer traversal", "list traversal", "invalid value", "remove required", "reserved marker", "missing sibling required", "invalid sibling config", "invalid binding", "invalid lower layer", "ancestor exclusion"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			owner, path, action, value := "database.primary", "value", "set", json.RawMessage(`"PRIVATE_NEW"`)
			var selector []string
			switch mode {
			case "unknown owner":
				owner = "database.absent"
			case "unowned constructor":
				owner = "example.com/acme/implementation-use/local.New"
			case "unknown field":
				path = "private_unknown"
			case "map traversal":
				path = "mapping.key"
			case "pointer traversal":
				path = "pointer.label"
			case "list traversal":
				path = "items.label"
			case "invalid value":
				value = json.RawMessage(`["PRIVATE_INVALID"]`)
			case "remove required":
				path, action, value = "endpoint", "remove", nil
			case "reserved marker":
				value = json.RawMessage(`{"$remove":true}`)
			case "missing sibling required":
				document = strings.Replace(document, "endpoint: PRIVATE_RETAINED, ", "", 1)
			case "invalid sibling config":
				document = strings.Replace(document, "endpoint: PRIVATE_RETAINED", "endpoint: [PRIVATE_INVALID]", 1)
			case "invalid binding":
				document = strings.Replace(document, "resources:\n", "resources:\n  bind:\n    instances:\n      database.primary: {db: database.missing}\n", 1)
			case "invalid lower layer":
				document = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
				selector = []string{"--env", "production"}
			case "ancestor exclusion":
				owner = configurationImplementationOwner
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "config: {"+owner+": {$remove: true}}\n")
				selector = []string{"--env", "production"}
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			runConfigurationPlan(t, root, configurationPlanRequest(t, owner, path, action, value), false, selector...)
		})
	}
}

func TestRunChangePlanConfigurationRepairsSelectedValuesAndChecksNoOps(t *testing.T) {
	for _, mode := range []string{"repair default", "repair environment", "repair replacement", "absent default", "absent replacement", "new overlay exclusion", "existing overlay exclusion", "invalid no-op"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			path, action, value := "unused", "remove", json.RawMessage(nil)
			var selector []string
			if strings.HasPrefix(mode, "repair") {
				path, action, value = "value", "set", json.RawMessage(`"PRIVATE_REPAIRED"`)
				invalid := strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
				switch mode {
				case "repair default":
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), invalid)
				case "repair environment":
					selector = []string{"--env", "production"}
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}}}\n")
				case "repair replacement":
					selector = []string{"--config", "deploy/customer.yaml"}
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), invalid)
				}
			} else if mode == "absent replacement" {
				selector = []string{"--config", "deploy/customer.yaml"}
			} else if mode == "new overlay exclusion" || mode == "existing overlay exclusion" {
				selector = []string{"--env", "production"}
				if mode == "existing overlay exclusion" {
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {config: {unused: {$remove: true}}}}}\n")
				}
			} else if mode == "invalid no-op" {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(document, "endpoint: PRIVATE_RETAINED, ", "", 1))
			}
			result := runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", path, action, value), mode != "invalid no-op", selector...)
			if mode != "invalid no-op" {
				want := 0
				if strings.HasPrefix(mode, "repair") || mode == "new overlay exclusion" {
					want = 1
				}
				if len(result.Payload.WriteSet) != want || len(result.Payload.Effects.Planned) != want || len(result.Effects.Planned) != want {
					t.Fatal("wrong no-op or exclusion effects")
				}
			}
		})
	}
}

func TestConfigurationPreviewRetainsSparseFinalValues(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	request, err := changerequest.Parse(configurationPlanRequest(t, "database.primary", "nested.label", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	if err != nil {
		t.Fatal(err)
	}
	change, _ := request.Operations()[0].Configuration()
	plan, err := implementationselect.PreviewConfiguration(t.Context(), implementationselect.Options{Start: root, EnvironmentName: "production", Environment: implementationSelectionCommandEnvironment(nil)}, change)
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]any
	if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"resources": map[string]any{"instances": map[string]any{"database.primary": map[string]any{"config": map[string]any{"nested": map[string]any{"label": "PRIVATE_NEW"}}}}}}
	if !reflect.DeepEqual(updated, want) {
		t.Fatal("overlay copied lower state or changed another field")
	}
	candidate, err := plan.Inputs().ComposeCandidate(plan.Updated())
	if err != nil || plan.Inputs().ValidateCandidate(candidate) != nil {
		t.Fatalf("invalid final configuration: %v", err)
	}
}

func TestRunChangePlanConfigurationRetainsDormantOwnerAndRequiredPresence(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	result := runConfigurationPlan(t, root, configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`""`)), true)
	if len(result.Payload.WriteSet) != 1 || string(result.Payload.ConfigurationImpact["derived_operations"]) != "[]" {
		t.Fatal("dormant field edit lost ownership or rejected a present empty required value")
	}
	runConfigurationPlan(t, root, configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "remove", nil), false)
}

func TestConfigurationPreviewRemovingLastFieldDoesNotRemoveOwner(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	for _, file := range []string{"smtp/implementation.go", "original/provider.go"} {
		path := filepath.Join(root, file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeCommandFile(t, path, strings.Replace(string(data), "Endpoint string `plystra:\"required\"`", "Endpoint string", 1))
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}
config:
  example.com/acme/implementation-use/smtp.New: {value: PRIVATE_LAST}
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_LAST}
`)
	for _, owner := range []string{configurationImplementationOwner, "database.primary"} {
		request, err := changerequest.Parse(configurationPlanRequest(t, owner, "value", "remove", nil))
		if err != nil {
			t.Fatal(err)
		}
		change, _ := request.Operations()[0].Configuration()
		plan, err := implementationselect.PreviewConfiguration(t.Context(), implementationselect.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}, change)
		if err != nil {
			t.Fatal(err)
		}
		var updated map[string]any
		if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
			t.Fatal(err)
		}
		config := updated["config"].(map[string]any)[owner]
		if owner == "database.primary" {
			config = updated["resources"].(map[string]any)["instances"].(map[string]any)[owner].(map[string]any)["config"]
		}
		if !reflect.DeepEqual(config, map[string]any{}) || len(plan.ConfigurationImpact().NewlyUnownedPaths()) != 0 || len(plan.ConfigurationImpact().DerivedOperations()) != 0 {
			t.Fatal("last field removal was treated as owner removal")
		}
	}
}

func TestRunChangePlanConfigurationRevalidatesNestedRequiredFieldsAndPreservesDefaultOmission(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	path := filepath.Join(root, "original/provider.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	provider := strings.Replace(string(data), "Count uint64 }", "Count uint64 `plystra:\"required\"` }", 1)
	provider = strings.Replace(provider, "Number uint64 `plystra:\"build-visible\"`", "Number uint64 `plystra:\"build-visible\" plystra-default:\"7\"`", 1)
	writeCommandFile(t, path, provider)
	document = strings.Replace(document, "endpoint: PRIVATE_RETAINED,", "endpoint: PRIVATE_RETAINED, nested: {count: 0},", 1)
	document = strings.Replace(document, "value: PRIVATE_PRIMARY,", "value: PRIVATE_PRIMARY, number: 9,", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "nested.count", "set", json.RawMessage(`0`)), true)
	runConfigurationPlan(t, root, configurationPlanRequest(t, "database.primary", "nested.count", "remove", nil), false)
	requestBytes := configurationPlanRequest(t, "database.primary", "number", "remove", nil)
	runConfigurationPlan(t, root, requestBytes, true)
	request, err := changerequest.Parse(requestBytes)
	if err != nil {
		t.Fatal(err)
	}
	change, _ := request.Operations()[0].Configuration()
	plan, err := implementationselect.PreviewConfiguration(t.Context(), implementationselect.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}, change)
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]any
	if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
		t.Fatal(err)
	}
	config := updated["resources"].(map[string]any)["instances"].(map[string]any)["database.primary"].(map[string]any)["config"].(map[string]any)
	if _, present := config["number"]; present {
		t.Fatal("removal copied a declared default into authored configuration")
	}
	candidate, err := plan.Inputs().ComposeCandidate(plan.Updated())
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range candidate.Manifest().ResourceInstances() {
		if instance.Name() != "database.primary" {
			continue
		}
		provider, _ := plan.Inputs().Declarations().ResourceProviders().BySymbol(instance.Provider())
		schema, _ := provider.Configuration()
		for _, field := range schema.Fields() {
			if field.Name() == "number" && string(field.DefaultJSON()) == "7" {
				return
			}
		}
	}
	t.Fatal("field removal lost its declared default")
}

func TestBuiltCLIChangePlanConfigurationFromStdin(t *testing.T) {
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
	command.Stdin = bytes.NewReader(configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_NEW"`)))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() != 0 || bytes.Contains(output, []byte("PRIVATE_")) {
		t.Fatalf("compiled preview: %v\n%s\n%s", err, output, &stderr)
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("compiled preview changed Project bytes")
	}
}

func configurationPlanRequest(t testing.TB, owner, path, action string, value json.RawMessage) []byte {
	t.Helper()
	operation := map[string]any{"kind": "configuration", "action": action, "owner": owner, "path": path}
	if value != nil {
		operation["value"] = value
	}
	request, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": []any{operation}})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func runConfigurationPlan(t *testing.T, root string, request []byte, success bool, selector ...string) configurationChangePlanResult {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "change.json"), string(request))
	before := commandTree(t, root)
	exit, stdout, stderr := runCommand(t, append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, selector...), root, implementationSelectionCommandEnvironment(nil))
	var result configurationChangePlanResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("unsafe result: %s\n%s", stdout, stderr)
	}
	if success {
		if exit != 0 || result.Status != "success" || result.Payload.Completeness != "non_exact" || !strings.HasPrefix(result.Payload.PlanDigest, "sha256:") {
			t.Fatalf("configuration preview: %d\n%s", exit, stdout)
		}
		for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
			if value := result.Payload.ConfigurationImpact[name]; len(value) == 0 || string(value) == "null" {
				t.Fatalf("missing impact array %s", name)
			}
		}
	} else if exit == 0 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" {
		t.Fatalf("invalid configuration accepted: %s", stdout)
	}
	if !success {
		var failure struct{ Payload json.RawMessage }
		if err := json.Unmarshal([]byte(stdout), &failure); err != nil || len(failure.Payload) != 0 && string(failure.Payload) != "null" || len(result.Effects.Planned) != 0 {
			t.Fatalf("failed preview published a partial plan: %s", stdout)
		}
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("preview changed Project bytes")
	}
	assertNoCommandTransactions(t, root)
	return result
}

func writeConfigurationPlanProject(t *testing.T) (string, string) {
	t.Helper()
	root := writeResourceSelectionCommandProject(t)
	fields := "Endpoint string `plystra:\"required\"`; Value string; Unused string; Public string `plystra:\"build-visible\"`; Number uint64 `plystra:\"build-visible\"`; Password configuration.Secret; Nested struct { Label string; Count uint64 }; Pointer *struct { Label string }; Mapping map[string]string; Items []string"
	writeCommandFile(t, filepath.Join(root, "original/provider.go"), "package original\nimport \"github.com/plystra/kernel/configuration\"\ntype Config struct { "+fields+" }\ntype Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New(Config)(*Value,error){panic(\"must not execute\")}\nfunc (*Value) Read(){}\n")
	for _, name := range []string{"smtp", "local"} {
		writeCommandFile(t, filepath.Join(root, name, "implementation.go"), fmt.Sprintf(`package %s
import (
 "context"
 "github.com/plystra/kernel/configuration"
 contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)
type Config struct { %s }
type Service struct{}
//plystra:implements email.send/v1
func New(Config)(*Service,error){panic("must not execute")}
func (*Service) Send(context.Context,contract.Request)(contract.Response,error){return contract.Response{},nil}
`, name, fields))
	}
	document := `# Keep selected configuration.
interfaces:
  require: [email.send/v1]
  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/original.New
      config: {endpoint: PRIVATE_PRIMARY_REQUIRED, value: PRIVATE_PRIMARY, nested: {label: PRIVATE_NESTED, count: 7}, public: PUBLIC_OLD, password: {env: PRIVATE_SECRET}, pointer: null}
    database.retained:
      use: example.com/acme/implementation-use/original.New
      config: {endpoint: PRIVATE_RETAINED, value: PRIVATE_RETAINED_VALUE}
config:
  example.com/acme/implementation-use/smtp.New: {endpoint: PRIVATE_IMPLEMENTATION_REQUIRED, value: PRIVATE_IMPLEMENTATION, nested: {label: PRIVATE_NESTED, count: 7}}
`
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), document)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	return root, document
}
