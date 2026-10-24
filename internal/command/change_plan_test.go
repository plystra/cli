package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuiltCLIChangePlanRemovesSelectionReadOnly(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [reports.read/v1]
  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}
config:
  example.com/acme/implementation-use/smtp.New: {endpoint: PRIVATE_ENDPOINT}
`)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "smtp"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"remove","interface":"email.send/v1"}]}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() != 0 || bytes.Contains(output, []byte("PRIVATE_")) {
		t.Fatalf("built CLI removal: %v\n%s\n%s", err, output, &stderr)
	}
	var result struct {
		Status  string `json:"status"`
		Payload struct {
			ReadSet []struct {
				BeforeExists *bool `json:"before_exists"`
				AfterExists  *bool `json:"after_exists"`
			} `json:"read_set"`
			WriteSet []struct {
				Path         string
				BeforeExists *bool `json:"before_exists"`
				AfterExists  *bool `json:"after_exists"`
			} `json:"write_set"`
			Operations   []struct{ Kind, Action string } `json:"operations"`
			Verification []string                        `json:"verification"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Status != "success" || len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != "plystra.production.yaml" || len(result.Payload.Operations) != 2 {
		t.Fatalf("built CLI removal result = %s, %v", output, err)
	}
	if !reflect.DeepEqual(result.Payload.Verification, []string{"plystra", "check", "--env", "production"}) {
		t.Fatalf("built CLI verification changed argument order: %q", result.Payload.Verification)
	}
	if len(result.Payload.ReadSet) != 2 {
		t.Fatalf("missing selected input membership: %s", output)
	}
	for _, input := range result.Payload.ReadSet {
		if input.BeforeExists == nil || !*input.BeforeExists || input.AfterExists != nil {
			t.Fatalf("invalid input existence: %s", output)
		}
	}
	write := result.Payload.WriteSet[0]
	if write.BeforeExists == nil || !*write.BeforeExists || write.AfterExists == nil || !*write.AfterExists {
		t.Fatalf("configuration update lost existence: %s", output)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("built CLI preview changed Project files")
	}
	assertNoCommandTransactions(t, root)
}

func TestRunChangePlanVerificationRetainsSelectedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		ambient   map[string]string
		selector  []string
		verify    bool
	}{
		{name: "default"},
		{name: "explicit environment", arguments: []string{"--env", "production"}, selector: []string{"--env", "production"}},
		{name: "ambient environment", ambient: map[string]string{"PLYSTRA_ENV": "production"}, selector: []string{"--env", "production"}},
		{name: "explicit replacement", arguments: []string{"--config", "deploy/production config.yaml"}, selector: []string{"--config", "deploy/production config.yaml"}},
		{name: "ambient replacement", ambient: map[string]string{"PLYSTRA_CONFIG": "deploy/production config.yaml"}, selector: []string{"--config", "deploy/production config.yaml"}},
		{name: "flag-like explicit replacement", arguments: []string{"--config", "./--production.yaml"}, selector: []string{"--config", "./--production.yaml"}, verify: true},
		{name: "flag-like ambient replacement", ambient: map[string]string{"PLYSTRA_CONFIG": "--production.yaml"}, selector: []string{"--config", "./--production.yaml"}, verify: true},
		{name: "flag-like ambient environment", ambient: map[string]string{"PLYSTRA_ENV": "--production"}, selector: []string{"--env", "--production"}, verify: true},
		{name: "literal replacement", arguments: []string{"--config", "deploy/{{blue}}.yaml"}, selector: []string{"--config", "deploy/{{blue}}.yaml"}, verify: true},
		{name: "literal ambient replacement", ambient: map[string]string{"PLYSTRA_CONFIG": "deploy/${blue}.yaml"}, selector: []string{"--config", "deploy/${blue}.yaml"}, verify: true},
		{name: "literal ambient environment", ambient: map[string]string{"PLYSTRA_ENV": "{{blue}}"}, selector: []string{"--env", "{{blue}}"}, verify: true},
		{name: "long replacement path", arguments: []string{"--config", strings.Repeat("nested/", 40) + "configuration.yaml"}, selector: []string{"--config", strings.Repeat("nested/", 40) + "configuration.yaml"}},
		{name: "explicit overrides ambient", arguments: []string{"--config", "deploy/production config.yaml"}, ambient: map[string]string{"PLYSTRA_ENV": "missing"}, selector: []string{"--config", "deploy/production config.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
			if len(test.selector) != 0 && test.selector[0] == "--env" {
				writeCommandFile(t, filepath.Join(root, "plystra."+test.selector[1]+".yaml"), "{}\n")
			}
			replacement := "deploy/production config.yaml"
			if len(test.selector) != 0 && test.selector[0] == "--config" {
				replacement = test.selector[1]
			}
			writeCommandFile(t, filepath.Join(root, filepath.FromSlash(replacement)), "interfaces:\n  require: [email.send/v1]\n")
			request := filepath.Join(root, "change.json")
			writeCommandFile(t, request, `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
			before := commandTree(t, root)
			arguments := append([]string{"change", "plan", "--request", request, "--format", "json"}, test.arguments...)
			exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "local"), implementationSelectionCommandEnvironment(test.ambient))
			if exitCode != 0 || stderr != "" {
				t.Fatalf("change plan failed: %d\n%s\n%s", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					Verification []string
					Requirements []struct{ Verification []string } `json:"effect_requirements"`
					Effects      struct {
						Planned []struct{ Verification []string }
					}
				}
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			want := append([]string{"plystra", "check"}, test.selector...)
			if !reflect.DeepEqual(result.Payload.Verification, want) || len(result.Payload.Effects.Planned) != 1 || !reflect.DeepEqual(result.Payload.Effects.Planned[0].Verification, want) ||
				len(result.Payload.Requirements) != 1 || !reflect.DeepEqual(result.Payload.Requirements[0].Verification, want) {
				t.Fatalf("verification lost selected configuration: %s", stdout)
			}
			if test.verify {
				checkExit, _, checkStderr := runCommand(t, result.Payload.Verification[1:], filepath.Join(root, "local"), implementationSelectionCommandEnvironment(nil))
				if checkExit != 1 || !strings.Contains(checkStderr, "PLYSTRA_RESOLVE_MULTIPLE_IMPLEMENTATIONS") {
					t.Fatalf("verification did not reach the selected Project: %d\n%s", checkExit, checkStderr)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("verification preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanImplementationSelectionIsStructuredAndReadOnly(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [email.send/v1]\n")
	request := `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`
	requestPath := filepath.Join(root, "change.json")
	writeCommandFile(t, requestPath, request)
	environment := implementationSelectionCommandEnvironment(nil)
	before := commandTree(t, root)

	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var first struct {
		Status  string          `json:"status"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &first); err != nil {
		t.Fatalf("decode change plan result: %v\n%s", err, stdout)
	}
	var payload struct {
		Schema              string                     `json:"schema"`
		RequestDigest       string                     `json:"request_digest"`
		PlanDigest          string                     `json:"plan_digest"`
		Completeness        string                     `json:"completeness"`
		FinalModelDigest    string                     `json:"final_model_digest"`
		Verification        []string                   `json:"verification"`
		ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
	}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatalf("decode change-plan payload: %v", err)
	}
	if first.Status != "success" || payload.Schema != "plystra.change-plan/v1" || !strings.HasPrefix(payload.RequestDigest, "sha256:") || !strings.HasPrefix(payload.PlanDigest, "sha256:") || !strings.HasPrefix(payload.FinalModelDigest, "sha256:") || payload.Completeness != "non_exact" {
		t.Fatalf("result identity = status %q payload %#v", first.Status, payload)
	}
	if !reflect.DeepEqual(payload.Verification, []string{"plystra", "check"}) {
		t.Fatalf("verification is not executable in its emitted order: %q", payload.Verification)
	}
	for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
		if string(payload.ConfigurationImpact[name]) == "null" {
			t.Fatalf("configuration_impact.%s is null", name)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("change plan mutated the Project:\nbefore: %#v\nafter: %#v", before, after)
	}

	exitCode, secondStdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repeat change plan = exit %d, stderr %q", exitCode, stderr)
	}
	var second struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(secondStdout), &second); err != nil || string(second.Payload) != string(first.Payload) {
		t.Fatalf("repeat payload = %s, want %s; err=%v", second.Payload, first.Payload, err)
	}
}

func TestRunChangePlanRemoveDormantImplementationSelection(t *testing.T) {
	for _, test := range []struct {
		name, selectedPath string
		selector           []string
		invalidConfig      bool
		noSchema           bool
	}{
		{name: "default", selectedPath: "plystra.yaml"},
		{name: "environment", selectedPath: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selectedPath: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
		{name: "default invalid old Config", selectedPath: "plystra.yaml", invalidConfig: true},
		{name: "environment invalid old Config", selectedPath: "plystra.production.yaml", selector: []string{"--env", "production"}, invalidConfig: true},
		{name: "replacement invalid old Config", selectedPath: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}, invalidConfig: true},
		{name: "default schema-less old Config", selectedPath: "plystra.yaml", noSchema: true},
		{name: "replacement schema-less old Config", selectedPath: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}, noSchema: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			source := `package smtp
import (
  "context"
  "example.com/acme/implementation-use/database"
  contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)
type Config struct { Endpoint string ` + "`plystra:\"required\"`" + ` }
type Service struct{}
//plystra:implements email.send/v1
func New(cfg Config, db database.Resource) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) {
  return contract.Response{}, nil
}
`
			if test.noSchema {
				source = strings.ReplaceAll(source, "func New(cfg Config, db database.Resource)", "func New(db database.Resource)")
			}
			writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), source)
			document := `interfaces:
  require: [reports.read/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/smtp.New:
    endpoint: PRIVATE_ENDPOINT
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_PRIMARY}
    database.retained:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_RETAINED}
  bind:
    implementations:
      example.com/acme/implementation-use/smtp.New:
        db: database.primary
`
			if test.invalidConfig {
				document = strings.ReplaceAll(document, "endpoint: PRIVATE_ENDPOINT", "endpoint: [PRIVATE_INVALID]")
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
			writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), document)
			if test.invalidConfig && strings.HasPrefix(test.name, "environment") {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.ReplaceAll(document, "endpoint: [PRIVATE_INVALID]", "endpoint: PRIVATE_ENDPOINT"))
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "config:\n  example.com/acme/implementation-use/smtp.New: {endpoint: [PRIVATE_INVALID]}\n")
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"remove","interface":"email.send/v1"}]}`)
			before := commandTree(t, root)
			args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("remove preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Status  string `json:"status"`
				Payload struct {
					Completeness        string                     `json:"completeness"`
					PlanDigest          string                     `json:"plan_digest"`
					ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
					WriteSet            []struct{ Path string }    `json:"write_set"`
					Operations          []struct {
						Kind, Action, Interface, Path string
						Derived                       bool `json:"derived"`
					} `json:"operations"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != "success" || result.Payload.Completeness != "non_exact" {
				t.Fatalf("remove preview result = %#v", result)
			}
			for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
				if value := result.Payload.ConfigurationImpact[name]; len(value) == 0 || string(value) == "null" {
					t.Fatalf("missing impact %s: %#v", name, result.Payload.ConfigurationImpact)
				}
			}
			var retained []struct{ Owner, Path string }
			if err := json.Unmarshal(result.Payload.ConfigurationImpact["retained_owners"], &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained) != 2 || retained[0].Owner != "database.primary" || retained[1].Owner != "database.retained" {
				t.Fatalf("removal lost retained Resource configuration: %#v", retained)
			}
			bindingPath := `resources.bind.implementations["example.com/acme/implementation-use/smtp.New"]["db"]`
			requested, derived, binding := false, false, false
			for _, operation := range result.Payload.Operations {
				requested = requested || operation.Kind == "implementation_selection" && operation.Action == "remove" && operation.Interface == "email.send/v1" && !operation.Derived
				derived = derived || operation.Kind == "configuration" && operation.Action == "remove" && operation.Path == "config.example.com/acme/implementation-use/smtp.New" && operation.Derived
				binding = binding || operation.Kind == "resource_binding" && operation.Action == "remove" && operation.Path == bindingPath && operation.Derived
			}
			if len(result.Payload.Operations) != 3 || !requested || !derived || !binding {
				t.Fatalf("remove operations = %#v", result.Payload.Operations)
			}
			var unowned []struct{ Owner, Path, Source string }
			if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_unowned_paths"], &unowned); err != nil {
				t.Fatal(err)
			}
			if len(unowned) != 2 {
				t.Fatalf("unowned paths = %#v", unowned)
			}
			sourcePath := test.selectedPath
			if strings.HasPrefix(test.name, "environment") {
				sourcePath = "plystra.yaml"
			}
			for _, path := range unowned {
				wantSource := sourcePath
				if test.invalidConfig && strings.HasPrefix(test.name, "environment") && path.Path != bindingPath {
					wantSource = test.selectedPath
				}
				if path.Owner != "example.com/acme/implementation-use/smtp.New" || !strings.HasPrefix(path.Source, wantSource+" ") || path.Path != "config.example.com/acme/implementation-use/smtp.New" && path.Path != bindingPath {
					t.Fatalf("unowned path = %#v", path)
				}
			}
			if len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != test.selectedPath {
				t.Fatalf("remove write set = %#v", result.Payload.WriteSet)
			}
			secondExit, secondStdout, secondStderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
			var second struct {
				Payload struct {
					PlanDigest string `json:"plan_digest"`
				} `json:"payload"`
			}
			if secondExit != 0 || secondStderr != "" || json.Unmarshal([]byte(secondStdout), &second) != nil || result.Payload.PlanDigest == "" || second.Payload.PlanDigest != result.Payload.PlanDigest {
				t.Fatalf("repeat removal = exit %d, stdout %q, stderr %q", secondExit, secondStdout, secondStderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("remove preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanRemoveSatisfiedImplementationSelectionHasNoWrites(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector []string
		target   string
	}{
		{name: "default"},
		{name: "replacement", selector: []string{"--config", "deploy/customer.yaml"}},
		{name: "existing overlay tombstone", selector: []string{"--env", "production"}},
		{name: "unknown default", target: "deleted.read/v1"},
		{name: "unknown replacement", target: "deleted.read/v1", selector: []string{"--config", "deploy/customer.yaml"}},
		{name: "unknown overlay tombstone", target: "deleted.read/v1", selector: []string{"--env", "production"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			target := test.target
			if target == "" {
				target = "email.send/v1"
			}
			document := "interfaces:\n  require: [reports.read/v1]\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), fmt.Sprintf("interfaces:\n  use:\n    %s: {$remove: true}\n", target))
			writeCommandFile(t, filepath.Join(root, "change.json"), fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"remove","interface":%q}]}`, target))
			before := commandTree(t, root)
			args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" {
				t.Fatalf("satisfied removal = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					WriteSet json.RawMessage `json:"write_set"`
					Effects  struct {
						Planned json.RawMessage `json:"planned"`
					} `json:"effects"`
					Operations []json.RawMessage `json:"operations"`
				} `json:"payload"`
				Effects struct {
					Planned json.RawMessage `json:"planned"`
				} `json:"effects"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if string(result.Payload.WriteSet) != "[]" || string(result.Payload.Effects.Planned) != "[]" || string(result.Effects.Planned) != "[]" || len(result.Payload.Operations) != 1 {
				t.Fatalf("satisfied removal advertised writes: %s", stdout)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("satisfied removal preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanRemoveImplementationSelectionFallsBackOrRejectsAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name                     string
		unique, missing, invalid bool
	}{
		{name: "unique fallback", unique: true},
		{name: "ambiguous fallback"},
		{name: "missing fallback", missing: true},
		{name: "still-owned invalid Config", unique: true, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			if test.unique || test.missing {
				writeCommandFile(t, filepath.Join(root, "local", "implementation.go"), "package local\n")
			}
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			if test.missing {
				writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), "package smtp\n")
			}
			document := `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/smtp.New:
    endpoint: PRIVATE_ENDPOINT
`
			if test.invalid {
				document = strings.ReplaceAll(document, "endpoint: PRIVATE_ENDPOINT", "endpoint: [PRIVATE_INVALID]")
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"remove","interface":"email.send/v1"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("fallback preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Status  string          `json:"status"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if test.unique && !test.invalid {
				var payload struct {
					Impact struct {
						Retained []struct{ Owner, Reason string } `json:"retained_owners"`
						Unowned  []json.RawMessage                `json:"newly_unowned_paths"`
						Derived  []json.RawMessage                `json:"derived_operations"`
					} `json:"configuration_impact"`
				}
				if err := json.Unmarshal(result.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if exitCode != 0 || result.Status != "success" || len(payload.Impact.Retained) != 1 || payload.Impact.Retained[0].Owner != "example.com/acme/implementation-use/smtp.New" || payload.Impact.Retained[0].Reason != "owner-remains-effective" || len(payload.Impact.Unowned) != 0 || len(payload.Impact.Derived) != 0 {
					t.Fatalf("unique fallback = exit %d, %#v", exitCode, result)
				}
			} else if exitCode != 3 || result.Status != "validation_failed" || string(result.Payload) != "null" {
				t.Fatalf("ambiguous fallback = exit %d, %#v", exitCode, result)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("fallback preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanResourceSelectionReportsOwnershipImpact(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("Resource change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status  string `json:"status"`
		Payload struct {
			Schema              string                     `json:"schema"`
			Completeness        string                     `json:"completeness"`
			ConfigurationImpact map[string]json.RawMessage `json:"configuration_impact"`
			Operations          []struct {
				Kind     string `json:"kind"`
				Action   string `json:"action"`
				Instance string `json:"instance"`
				Provider string `json:"provider"`
				Owner    string `json:"owner"`
				Path     string `json:"path"`
				Derived  bool   `json:"derived"`
			} `json:"operations"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Status != "success" ||
		result.Payload.Schema != "plystra.change-plan/v1" || result.Payload.Completeness != "non_exact" {
		t.Fatalf("Resource change plan result = %#v, %v", result, err)
	}
	for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
		if len(result.Payload.ConfigurationImpact[name]) == 0 || string(result.Payload.ConfigurationImpact[name]) == "null" {
			t.Fatalf("configuration_impact.%s = %s", name, result.Payload.ConfigurationImpact[name])
		}
	}
	var retained []struct{ Owner, Path string }
	var unowned []struct{ Owner, Path string }
	var derived []struct{ Kind, Action, Owner, Path string }
	for _, field := range []struct {
		name string
		to   any
	}{{"retained_owners", &retained}, {"newly_unowned_paths", &unowned}, {"derived_operations", &derived}} {
		if err := json.Unmarshal(result.Payload.ConfigurationImpact[field.name], field.to); err != nil {
			t.Fatalf("decode configuration_impact.%s: %v", field.name, err)
		}
	}
	wantPath := `resources.instances["database.primary"].config`
	if len(unowned) != 1 || unowned[0].Owner != "database.primary" || unowned[0].Path != wantPath ||
		len(derived) != 1 || derived[0].Kind != "resource_configuration" || derived[0].Action != "remove" || derived[0].Path != wantPath ||
		len(retained) != 1 || retained[0].Owner != "database.retained" || len(result.Payload.Operations) != 2 {
		t.Fatalf("Resource change impact = retained %#v, unowned %#v, derived %#v, operations %#v", retained, unowned, derived, result.Payload.Operations)
	}
	requested, companion := false, false
	for _, operation := range result.Payload.Operations {
		requested = requested || !operation.Derived && operation.Kind == "resource_selection" && operation.Action == "set" && operation.Instance == "database.primary" && operation.Provider == "example.com/acme/implementation-use/replacement.New"
		companion = companion || operation.Derived && operation.Kind == "resource_configuration" && operation.Action == "remove" && operation.Owner == "database.primary" && operation.Path == wantPath
	}
	if !requested || !companion {
		t.Fatalf("Resource change operations = %#v", result.Payload.Operations)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("Resource change plan modified Project files")
	}
}

func TestRunChangePlanResourceSelectionRepairsInvalidOldConfiguration(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_INVALID") {
		t.Fatalf("repair preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				RetainedOwners    []struct{ Owner string }        `json:"retained_owners"`
				NewlyUnownedPaths []struct{ Owner string }        `json:"newly_unowned_paths"`
				DerivedOperations []struct{ Kind, Action string } `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if len(impact.RetainedOwners) != 1 || impact.RetainedOwners[0].Owner != "database.retained" ||
		len(impact.NewlyUnownedPaths) != 1 || impact.NewlyUnownedPaths[0].Owner != "database.primary" ||
		len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" || impact.DerivedOperations[0].Action != "remove" {
		t.Fatalf("repair impact = %#v", impact)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("repair preview modified Project files")
	}
}

func TestRunChangePlanResourceSelectionPreservesSameProviderConfiguration(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("same-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				RetainedOwners    []struct{ Owner string } `json:"retained_owners"`
				NewlyUnownedPaths []json.RawMessage        `json:"newly_unowned_paths"`
				DerivedOperations []json.RawMessage        `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if len(impact.RetainedOwners) != 2 || impact.RetainedOwners[0].Owner != "database.primary" || impact.RetainedOwners[1].Owner != "database.retained" || len(impact.NewlyUnownedPaths) != 0 || len(impact.DerivedOperations) != 0 {
		t.Fatalf("same-provider impact = %#v", impact)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("same-provider preview modified Project files")
	}
}

func TestRunChangePlanDoesNotPublishPrivateResourceValueEquality(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "original", "provider.go"), `package original
import "github.com/plystra/kernel/configuration"
type Config struct {
	Value string `+"`yaml:\"value\"`"+`
	Password configuration.Secret `+"`yaml:\"password\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "config: {value: PRIVATE_PRIMARY}", "config: {value: PRIVATE_PRIMARY, password: {env: PRIVATE_TARGET_A}}", 1))
	preview := func() json.RawMessage {
		t.Helper()
		before := commandTree(t, root)
		exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
		if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
			t.Fatalf("private-value preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatal("private-value preview modified Project files")
		}
		var result struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		return result.Payload
	}
	first := preview()
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "PRIVATE_PRIMARY", "PRIVATE_CHANGED", 1))
	second := preview()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("private-only edit changed public plan:\nfirst %s\nsecond %s", first, second)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "PRIVATE_TARGET_A", "PRIVATE_TARGET_B", 1))
	third := preview()
	if !reflect.DeepEqual(first, third) {
		t.Fatalf("Secret target edit changed public plan:\nfirst %s\nthird %s", first, third)
	}
}

func TestRunChangePlanResourceSelectionOverlayKeepsLowerLayerUnchanged(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/replacement.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--env", "production", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("overlay preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationScope struct {
				Path string `json:"path"`
			} `json:"configuration_scope"`
			ConfigurationImpact struct {
				NewlyUnownedPaths []struct{ Owner, Source string } `json:"newly_unowned_paths"`
				DerivedOperations []struct{ Kind string }          `json:"derived_operations"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	if result.Payload.ConfigurationScope.Path != "plystra.production.yaml" || len(impact.NewlyUnownedPaths) != 2 ||
		impact.NewlyUnownedPaths[0].Owner != "database.primary" || impact.NewlyUnownedPaths[1].Owner != "database.primary" ||
		impact.NewlyUnownedPaths[0].Source == impact.NewlyUnownedPaths[1].Source ||
		len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" {
		t.Fatalf("overlay impact = %#v", result.Payload)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("overlay preview modified Project files")
	}
}

func TestRunChangePlanResourceSelectionOverlayProviderReversion(t *testing.T) {
	for _, value := range []string{"PRIVATE_REPLACEMENT", "[PRIVATE_INVALID]"} {
		t.Run(value, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources:\n  instances:\n    database.primary:\n      use: example.com/acme/implementation-use/replacement.New\n      config: {value: "+value+"}\n")
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/original.New"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--env", "production", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, value) {
				t.Fatalf("provider reversion preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationImpact struct {
						RetainedOwners    []struct{ Owner string }         `json:"retained_owners"`
						NewlyUnownedPaths []struct{ Owner, Source string } `json:"newly_unowned_paths"`
						DerivedOperations []struct{ Kind, Source string }  `json:"derived_operations"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			impact := result.Payload.ConfigurationImpact
			if len(impact.NewlyUnownedPaths) != 1 || impact.NewlyUnownedPaths[0].Owner != "database.primary" ||
				!strings.Contains(impact.NewlyUnownedPaths[0].Source, "plystra.production.yaml") ||
				len(impact.DerivedOperations) != 1 || impact.DerivedOperations[0].Kind != "resource_configuration" ||
				!strings.Contains(impact.DerivedOperations[0].Source, "plystra.production.yaml") ||
				len(impact.RetainedOwners) != 1 || impact.RetainedOwners[0].Owner != "database.retained" {
				t.Fatalf("provider reversion impact = %#v", impact)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("provider reversion preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceSelectionReportsEveryMissingRequiredField(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("missing Resource Config preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			Completeness        string `json:"completeness"`
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Owner              string `json:"owner"`
					Path               string `json:"path"`
					SchemaSource       string `json:"schema_source"`
					RequirednessReason string `json:"requiredness_reason"`
					DefaultSatisfies   bool   `json:"default_satisfies"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target       string   `json:"target"`
					Reason       string   `json:"reason"`
					Alternatives []string `json:"alternatives"`
					Sources      []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	wantPaths := []string{`resources.instances["database.primary"].config["name"]`, `resources.instances["database.primary"].config["settings"]["endpoint"]`}
	if result.Payload.Completeness != "non_exact" || len(impact.NewlyRequiredFields) != len(wantPaths) || len(impact.UnresolvedDecisions) != len(wantPaths) {
		t.Fatalf("missing Resource fields = %#v", impact)
	}
	for index, path := range wantPaths {
		field, decision := impact.NewlyRequiredFields[index], impact.UnresolvedDecisions[index]
		if field.Owner != "database.primary" || field.Path != path || !strings.Contains(field.SchemaSource, "/required/provider.go:") ||
			field.RequirednessReason != "required-field-on-newly-selected-resource-provider" || field.DefaultSatisfies || decision.Target != path ||
			decision.Reason != "required-configuration-value-missing" || len(decision.Alternatives) != 2 || len(decision.Sources) != 2 ||
			decision.Sources[0] != "plystra.yaml" && decision.Sources[1] != "plystra.yaml" {
			t.Fatalf("missing Resource field %d = %#v, decision %#v", index, field, decision)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("missing Resource Config preview modified Project files")
	}
	exitCode, _, _ = runCommand(t, []string{"use", "database.primary", "example.com/acme/implementation-use/required.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted missing required Resource Config")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed Resource use modified Project files")
	}
}

func TestRunChangePlanResourceSelectionFirstProviderRequiresValues(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, string(data)+"    database.pending: {}\n")
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.pending","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("first-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				UnresolvedDecisions []struct {
					Alternatives []string `json:"alternatives"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
		t.Fatalf("first-provider decisions = %#v, %v", result, err)
	}
	for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
		if len(decision.Alternatives) != 1 || decision.Alternatives[0] != "provide-required-field" {
			t.Fatalf("first-provider alternatives = %#v", decision.Alternatives)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("first-provider preview modified Project files")
	}
}

func TestRunChangePlanResourceRequiredFieldsUseSelectedDocument(t *testing.T) {
	for _, test := range []struct {
		name, path string
		selector   []string
	}{
		{name: "environment", path: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", path: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeRequiredResourceCommandProvider(t, root)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
			before := commandTree(t, root)
			arguments := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("selected required-field preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationScope struct {
						Path string `json:"path"`
					} `json:"configuration_scope"`
					ConfigurationImpact struct {
						UnresolvedDecisions []struct {
							Sources []string `json:"sources"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if result.Payload.ConfigurationScope.Path != test.path || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
				t.Fatalf("selected required-field scope and decisions = %#v", result.Payload)
			}
			for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
				if len(decision.Sources) != 2 || decision.Sources[0] != test.path && decision.Sources[1] != test.path {
					t.Fatalf("selected required-field sources = %#v", decision.Sources)
				}
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("selected required-field preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRequiredFieldsDoNotHideOtherBlockers(t *testing.T) {
	for _, test := range []struct {
		name, provider string
		prepare        func(*testing.T, string)
	}{
		{name: "another incomplete instance", provider: "required", prepare: func(t *testing.T, root string) {
			writeRequiredResourceCommandProvider(t, root)
			path := filepath.Join(root, "plystra.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old := "database.retained:\n      use: example.com/acme/implementation-use/original.New\n      config: {value: PRIVATE_RETAINED}"
			newValue := "database.retained:\n      use: example.com/acme/implementation-use/required.New"
			writeCommandFile(t, path, strings.Replace(string(data), old, newValue, 1))
		}},
		{name: "implicit fixed-array child", provider: "arrayrequired", prepare: func(t *testing.T, root string) {
			writeCommandFile(t, filepath.Join(root, "arrayrequired", "provider.go"), `package arrayrequired
type Item struct { Code string `+"`yaml:\"code\" plystra:\"required\"`"+` }
type Config struct {
	Name string `+"`yaml:\"name\" plystra:\"required\"`"+`
	Items [1]Item `+"`yaml:\"items\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			test.prepare(t, root)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/`+test.provider+`.New"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 3 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("incomplete candidate preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Status  string          `json:"status"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Status != "validation_failed" || string(result.Payload) != "null" {
				t.Fatalf("incomplete candidate result = %#v, %v", result, err)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("incomplete candidate preview modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRequiredFieldsRejectInvalidPreviousProviderAlternative(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeRequiredResourceCommandProvider(t, root)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, strings.Replace(string(data), "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/required.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
		t.Fatalf("invalid-previous-provider preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				UnresolvedDecisions []struct {
					Alternatives []string `json:"alternatives"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 2 {
		t.Fatalf("invalid-previous-provider decisions = %#v, %v", result, err)
	}
	for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
		if len(decision.Alternatives) != 1 || decision.Alternatives[0] != "provide-required-field" {
			t.Fatalf("invalid-previous-provider alternatives = %#v", decision.Alternatives)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("invalid-previous-provider preview modified Project files")
	}
}

func TestRunChangePlanResourceSelectionReportsValidBindingChoices(t *testing.T) {
	for _, test := range []struct {
		name, selectedPath string
		selector           []string
	}{
		{name: "default", selectedPath: "plystra.yaml"},
		{name: "environment", selectedPath: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selectedPath: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			if test.name == "environment" {
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), `# Selected Resource layer.
resources:
  instances:
    database.primary: {config: {value: PRIVATE_OVERLAY}}
    database.retained: {use: example.com/acme/implementation-use/original.New}
`)
			}
			writeCommandFile(t, filepath.Join(root, "ambiguous", "provider.go"), `package ambiguous
import "example.com/acme/implementation-use/database"
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(replica database.Resource) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/ambiguous.New"}]}`)
			before := commandTree(t, root)
			arguments := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("binding preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					Completeness        string `json:"completeness"`
					PlanDigest          string `json:"plan_digest"`
					ConfigurationImpact struct {
						NewlyRequiredFields json.RawMessage `json:"newly_required_fields"`
						RetainedOwners      json.RawMessage `json:"retained_owners"`
						NewlyUnownedPaths   json.RawMessage `json:"newly_unowned_paths"`
						DerivedOperations   json.RawMessage `json:"derived_operations"`
						UnresolvedDecisions []struct {
							Target       string   `json:"target"`
							Reason       string   `json:"reason"`
							Alternatives []string `json:"alternatives"`
							Sources      []string `json:"sources"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			decisions := result.Payload.ConfigurationImpact.UnresolvedDecisions
			if result.Payload.Completeness != "non_exact" || result.Payload.PlanDigest == "" || len(decisions) != 1 ||
				decisions[0].Target != `resources.bind.instances["database.primary"]["replica"]` ||
				decisions[0].Reason != "ambiguous-resource-binding" ||
				!reflect.DeepEqual(decisions[0].Alternatives, []string{"database.retained"}) ||
				!slices.Contains(decisions[0].Sources, test.selectedPath) ||
				!slices.ContainsFunc(decisions[0].Sources, func(source string) bool {
					return strings.Contains(source, "example.com/acme/implementation-use@local/ambiguous/provider.go:")
				}) || test.name == "environment" && !slices.Contains(decisions[0].Sources, `plystra.production.yaml resources.instances["database.retained"].use`) {
				t.Fatalf("binding decisions = %#v", decisions)
			}
			for _, field := range []json.RawMessage{
				result.Payload.ConfigurationImpact.NewlyRequiredFields,
				result.Payload.ConfigurationImpact.RetainedOwners,
				result.Payload.ConfigurationImpact.NewlyUnownedPaths,
				result.Payload.ConfigurationImpact.DerivedOperations,
			} {
				if len(field) == 0 || string(field) == "null" {
					t.Fatalf("incomplete configuration impact = %#v", result.Payload.ConfigurationImpact)
				}
			}
			secondExit, secondStdout, secondStderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			var second struct {
				Payload struct {
					PlanDigest string `json:"plan_digest"`
				} `json:"payload"`
			}
			if secondExit != 0 || secondStderr != "" || json.Unmarshal([]byte(secondStdout), &second) != nil || second.Payload.PlanDigest != result.Payload.PlanDigest {
				t.Fatalf("repeat binding preview = exit %d, stdout %q, stderr %q", secondExit, secondStdout, secondStderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("binding preview modified Project files")
			}
			exitCode, _, _ = runCommand(t, append([]string{"use", "database.primary", "example.com/acme/implementation-use/ambiguous.New"}, test.selector...), root, implementationSelectionCommandEnvironment(nil))
			if exitCode == 0 {
				t.Fatal("mutating use accepted an unresolved Resource binding")
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("failed Resource use modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceSelectionCombinesBindingAndRequiredDecisions(t *testing.T) {
	for _, test := range []struct {
		name, selectedPath string
		selector           []string
	}{
		{name: "default", selectedPath: "plystra.yaml"},
		{name: "environment", selectedPath: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selectedPath: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "combined", "provider.go"), `package combined
import "example.com/acme/implementation-use/database"
type Settings struct { Endpoint string `+"`yaml:\"endpoint\" plystra:\"required\"`"+` }
type Config struct {
    Name string `+"`yaml:\"name\" plystra:\"required\"`"+`
    Settings Settings `+"`yaml:\"settings\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(c Config, alpha database.Resource, beta database.Resource) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
			for _, name := range []string{"plystra.yaml", "deploy/customer.yaml"} {
				path := filepath.Join(root, filepath.FromSlash(name))
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeCommandFile(t, path, string(data)+"    database.backup: {use: example.com/acme/implementation-use/original.New}\n")
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/combined.New"}]}`)
			before := commandTree(t, root)
			arguments := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exitCode, stdout, stderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("combined decision preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					Completeness        string `json:"completeness"`
					PlanDigest          string `json:"plan_digest"`
					ConfigurationImpact struct {
						NewlyRequiredFields []struct {
							Path string `json:"path"`
						} `json:"newly_required_fields"`
						RetainedOwners      json.RawMessage `json:"retained_owners"`
						NewlyUnownedPaths   json.RawMessage `json:"newly_unowned_paths"`
						DerivedOperations   json.RawMessage `json:"derived_operations"`
						UnresolvedDecisions []struct {
							Target, Reason string
							Alternatives   []string `json:"alternatives"`
							Sources        []string `json:"sources"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			decisions := make(map[string][]string)
			for _, decision := range result.Payload.ConfigurationImpact.UnresolvedDecisions {
				if len(decision.Sources) < 2 || !slices.Contains(decision.Sources, test.selectedPath) {
					t.Fatalf("decision has no provenance: %#v", decision)
				}
				decisions[decision.Target] = decision.Alternatives
			}
			wantBindings := []string{
				"alpha=database.backup,beta=database.backup",
				"alpha=database.backup,beta=database.retained",
				"alpha=database.retained,beta=database.backup",
				"alpha=database.retained,beta=database.retained",
			}
			wantRequired := []string{"provide-required-field", "retain-previous-provider"}
			if got := decisions[`resources.bind.instances["database.primary"]`]; !reflect.DeepEqual(got, wantBindings) {
				t.Fatalf("joint binding alternatives = %#v", got)
			}
			for _, field := range []string{`resources.instances["database.primary"].config["name"]`, `resources.instances["database.primary"].config["settings"]["endpoint"]`} {
				if got := decisions[field]; !reflect.DeepEqual(got, wantRequired) {
					t.Fatalf("required field %s alternatives = %#v", field, got)
				}
			}
			if result.Payload.Completeness != "non_exact" || result.Payload.PlanDigest == "" || len(result.Payload.ConfigurationImpact.NewlyRequiredFields) != 2 || len(decisions) != 3 {
				t.Fatalf("combined decisions = %#v", result.Payload.ConfigurationImpact)
			}
			for _, field := range []json.RawMessage{result.Payload.ConfigurationImpact.RetainedOwners, result.Payload.ConfigurationImpact.NewlyUnownedPaths, result.Payload.ConfigurationImpact.DerivedOperations} {
				if len(field) == 0 || string(field) == "null" {
					t.Fatalf("incomplete configuration impact = %#v", result.Payload.ConfigurationImpact)
				}
			}
			secondExit, secondStdout, secondStderr := runCommand(t, arguments, root, implementationSelectionCommandEnvironment(nil))
			var second struct {
				Payload struct {
					PlanDigest string `json:"plan_digest"`
				} `json:"payload"`
			}
			if secondExit != 0 || secondStderr != "" || json.Unmarshal([]byte(secondStdout), &second) != nil || second.Payload.PlanDigest != result.Payload.PlanDigest {
				t.Fatalf("repeat combined preview = exit %d, stdout %q, stderr %q", secondExit, secondStdout, secondStderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("combined preview modified Project files")
			}
			exitCode, _, _ = runCommand(t, append([]string{"use", "database.primary", "example.com/acme/implementation-use/combined.New"}, test.selector...), root, implementationSelectionCommandEnvironment(nil))
			if exitCode == 0 {
				t.Fatal("mutating use accepted unresolved binding and Config decisions")
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("failed Resource use modified Project files")
			}
		})
	}
}

func TestRunChangePlanResourceBindingAllowsExactCandidateLimit(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "ambiguous", "provider.go"), `package ambiguous
import "example.com/acme/implementation-use/database"
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(replica database.Resource) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
	path := filepath.Join(root, "plystra.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := range 62 {
		data = fmt.Appendf(data, "    database.extra%03d: {use: example.com/acme/implementation-use/original.New}\n", index)
	}
	writeCommandFile(t, path, string(data))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/ambiguous.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("exact-limit preview = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				UnresolvedDecisions []struct {
					Alternatives []string `json:"alternatives"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || len(result.Payload.ConfigurationImpact.UnresolvedDecisions) != 1 || len(result.Payload.ConfigurationImpact.UnresolvedDecisions[0].Alternatives) != 63 {
		t.Fatalf("exact-limit decisions = %#v, %v", result, err)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("exact-limit preview modified Project files")
	}
}

func TestRunChangePlanResourceBindingChoicesDoNotHideOtherFailures(t *testing.T) {
	for _, test := range []struct {
		name, parameters, extraDocument string
		arrayConfig                     bool
		invalidRetainedConfig           bool
		otherResource                   bool
	}{
		{name: "invalid sibling binding", parameters: "replica database.Resource", extraDocument: "  bind:\n    instances:\n      database.retained: {bogus: database.primary}\n"},
		{name: "invalid retained Config", parameters: "replica database.Resource", invalidRetainedConfig: true},
		{name: "non-enumerable required array child", parameters: "replica database.Resource", arrayConfig: true},
		{name: "no compatible instance", parameters: "replica other.Resource", otherResource: true},
		{name: "excessive candidates", parameters: "replica database.Resource"},
		{name: "excessive joint search", parameters: "replica database.Resource, backup database.Resource"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			config := ""
			parameters := test.parameters
			if test.arrayConfig {
				config = "type Item struct { Code string `yaml:\"code\" plystra:\"required\"` }\ntype Config struct { Items [1]Item `yaml:\"items\"` }\n"
				parameters = "c Config, " + parameters
			}
			dependencyImport := "example.com/acme/implementation-use/database"
			if test.otherResource {
				dependencyImport = "example.com/acme/implementation-use/other"
			}
			writeCommandFile(t, filepath.Join(root, "ambiguous", "provider.go"), `package ambiguous
import "`+dependencyImport+`"
`+config+`type Value struct{}
//plystra:implements-resource storage.database/v1
func New(`+parameters+`) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
			extraDocument := test.extraDocument
			if test.name == "excessive candidates" {
				for index := range 65 {
					extraDocument += fmt.Sprintf("    database.extra%03d: {use: example.com/acme/implementation-use/original.New}\n", index)
				}
			} else if test.name == "excessive joint search" {
				for index := range 7 {
					extraDocument += fmt.Sprintf("    database.extra%03d: {use: example.com/acme/implementation-use/original.New}\n", index)
				}
			}
			if extraDocument != "" || test.invalidRetainedConfig {
				path := filepath.Join(root, "plystra.yaml")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				document := string(data) + extraDocument
				if test.invalidRetainedConfig {
					document = strings.Replace(document, "value: PRIVATE_RETAINED", "value: [PRIVATE_INVALID]", 1)
				}
				writeCommandFile(t, path, document)
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/acme/implementation-use/ambiguous.New"}]}`)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 3 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("unresolved other failure = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Status  string          `json:"status"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Status != "validation_failed" || string(result.Payload) != "null" {
				t.Fatalf("incomplete binding result = %#v, %v", result, err)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("rejected binding preview modified Project files")
			}
		})
	}
}

func writeRequiredResourceCommandProvider(t *testing.T, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "required", "provider.go"), `package required
type Settings struct { Endpoint string `+"`yaml:\"endpoint\" plystra:\"required\"`"+` }
type Config struct {
	Name string `+"`yaml:\"name\" plystra:\"required\"`"+`
	Settings Settings `+"`yaml:\"settings\"`"+`
}
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
}

func TestRunChangePlanRejectsUninstalledOperationWithoutMutation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	requestPath := filepath.Join(root, "change.json")
	writeCommandFile(t, requestPath, `{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"example.com/other@v1.0.0"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 2 || stderr != "" {
		t.Fatalf("unsupported change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status      string `json:"status"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode unsupported result: %v\n%s", err, stdout)
	}
	if result.Status != "unsupported" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_CHANGE_PLAN_OPERATION_UNSUPPORTED" {
		t.Fatalf("unsupported result = %#v", result)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unsupported change plan mutated the Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestRunChangePlanReportsNewlyRequiredConstructorField(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/local.New:
    endpoint: PRIVATE_ENDPOINT
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Owner              string `json:"owner"`
					Path               string `json:"path"`
					SchemaSource       string `json:"schema_source"`
					RequirednessReason string `json:"requiredness_reason"`
					DefaultSatisfies   bool   `json:"default_satisfies"`
				} `json:"newly_required_fields"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode change plan: %v\n%s", err, stdout)
	}
	fields := result.Payload.ConfigurationImpact.NewlyRequiredFields
	if len(fields) != 1 || fields[0].Owner != "example.com/acme/implementation-use/local.New" || fields[0].Path != `config["example.com/acme/implementation-use/local.New"]["endpoint"]` || !strings.HasPrefix(fields[0].SchemaSource, "example.com/acme/implementation-use@local/local/implementation.go:") || fields[0].RequirednessReason != "required-field-on-newly-reachable-constructor" || fields[0].DefaultSatisfies {
		t.Fatalf("newly required fields = %#v", fields)
	}
	if strings.Contains(stdout, "PRIVATE_ENDPOINT") {
		t.Fatal("change plan disclosed a private configuration value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}
}

func TestRunChangePlanReportsMissingRequiredConstructorValueWithoutMutation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Status  string `json:"status"`
		Payload struct {
			Completeness        string `json:"completeness"`
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Path string `json:"path"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target       string   `json:"target"`
					Reason       string   `json:"reason"`
					Alternatives []string `json:"alternatives"`
					Sources      []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode change plan: %v\n%s", err, stdout)
	}
	impact := result.Payload.ConfigurationImpact
	wantPath := `config["example.com/acme/implementation-use/local.New"]["endpoint"]`
	if result.Status != "success" || result.Payload.Completeness != "non_exact" || len(impact.NewlyRequiredFields) != 1 || impact.NewlyRequiredFields[0].Path != wantPath || len(impact.UnresolvedDecisions) != 1 || impact.UnresolvedDecisions[0].Target != wantPath || impact.UnresolvedDecisions[0].Reason != "required-configuration-value-missing" || len(impact.UnresolvedDecisions[0].Alternatives) == 0 || len(impact.UnresolvedDecisions[0].Sources) == 0 {
		t.Fatalf("missing-value impact = %#v", result)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}

	exitCode, _, _ = runCommand(t, []string{"use", "email.send/v1", "example.com/acme/implementation-use/local.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted a missing required configuration value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed use changed Project files")
	}
}

func TestRunChangePlanReportsEveryMissingRequiredFixedField(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	implementationPath := filepath.Join(root, "local", "implementation.go")
	source, err := os.ReadFile(implementationPath)
	if err != nil {
		t.Fatal(err)
	}
	oldField := "Endpoint string `plystra:\"required\"`"
	newFields := "Endpoint string `plystra:\"required\"`; Database struct { Host string `plystra:\"required\"` }; Private string"
	if !strings.Contains(string(source), oldField) {
		t.Fatal("configurable fixture lacks the expected field")
	}
	writeCommandFile(t, implementationPath, strings.Replace(string(source), oldField, newFields, 1))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/acme/implementation-use/smtp.New
config:
  example.com/acme/implementation-use/local.New:
    private: PRIVATE_VALUE
`)
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode != 0 || stderr != "" {
		t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	var result struct {
		Payload struct {
			ConfigurationImpact struct {
				NewlyRequiredFields []struct {
					Path string `json:"path"`
				} `json:"newly_required_fields"`
				UnresolvedDecisions []struct {
					Target  string   `json:"target"`
					Sources []string `json:"sources"`
				} `json:"unresolved_decisions"`
			} `json:"configuration_impact"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	impact := result.Payload.ConfigurationImpact
	want := []string{
		`config["example.com/acme/implementation-use/local.New"]["database"]["host"]`,
		`config["example.com/acme/implementation-use/local.New"]["endpoint"]`,
	}
	var fields, decisions []string
	for _, field := range impact.NewlyRequiredFields {
		fields = append(fields, field.Path)
	}
	for _, decision := range impact.UnresolvedDecisions {
		decisions = append(decisions, decision.Target)
		if len(decision.Sources) == 0 {
			t.Fatalf("missing decision provenance: %#v", decision)
		}
	}
	if !reflect.DeepEqual(fields, want) || !reflect.DeepEqual(decisions, want) {
		t.Fatalf("incomplete required-field impact = %#v", impact)
	}
	if strings.Contains(stdout, "PRIVATE_VALUE") {
		t.Fatal("change plan disclosed a private value")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("change plan changed Project files")
	}
	exitCode, _, _ = runCommand(t, []string{"use", "email.send/v1", "example.com/acme/implementation-use/local.New"}, root, implementationSelectionCommandEnvironment(nil))
	if exitCode == 0 {
		t.Fatal("mutating use accepted missing required values")
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failed use changed Project files")
	}
}

func TestRunChangePlanReportsRequiredFixedObjectChild(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	implementationPath := filepath.Join(root, "local", "implementation.go")
	source, err := os.ReadFile(implementationPath)
	if err != nil {
		t.Fatal(err)
	}
	oldField := "Endpoint string `plystra:\"required\"`"
	newField := "Database struct { Host string `plystra:\"required\"` }"
	if !strings.Contains(string(source), oldField) {
		t.Fatal("configurable fixture lacks the expected field")
	}
	writeCommandFile(t, implementationPath, strings.Replace(string(source), oldField, newField, 1))
	writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/implementation-use/local.New"}]}`)
	wantPath := `config["example.com/acme/implementation-use/local.New"]["database"]["host"]`
	for _, test := range []struct {
		name, config string
		unresolved   bool
	}{
		{"provided", "config:\n  example.com/acme/implementation-use/local.New:\n    database: {host: PRIVATE_HOST}\n", false},
		{"missing", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [email.send/v1]\n  use:\n    email.send/v1: example.com/acme/implementation-use/smtp.New\n"+test.config)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			if exitCode != 0 || stderr != "" {
				t.Fatalf("change plan = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			var result struct {
				Payload struct {
					ConfigurationImpact struct {
						NewlyRequiredFields []struct {
							Path string `json:"path"`
						} `json:"newly_required_fields"`
						UnresolvedDecisions []struct {
							Target string `json:"target"`
						} `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				} `json:"payload"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			impact := result.Payload.ConfigurationImpact
			if len(impact.NewlyRequiredFields) != 1 || impact.NewlyRequiredFields[0].Path != wantPath || (len(impact.UnresolvedDecisions) == 1) != test.unresolved {
				t.Fatalf("fixed-object impact = %#v", impact)
			}
			if test.unresolved && impact.UnresolvedDecisions[0].Target != wantPath {
				t.Fatalf("unresolved target = %#v", impact.UnresolvedDecisions)
			}
			if strings.Contains(stdout, "PRIVATE_HOST") {
				t.Fatal("change plan disclosed a private value")
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("change plan changed Project files")
			}
		})
	}
}
