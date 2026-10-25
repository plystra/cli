package command_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChangePlanEnforcesCandidatePolicySupport(t *testing.T) {
	testChangePlanCandidatePolicySupport(t, runCommand)
}

func TestBuiltCLIChangePlanEnforcesCandidatePolicySupport(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	testChangePlanCandidatePolicySupport(t, func(t testing.TB, arguments []string, root string, environment []string) (int, string, string) {
		t.Helper()
		command := exec.CommandContext(t.Context(), binary, arguments...)
		command.Dir, command.Env = root, environment
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		exit := 0
		if err := command.Run(); err != nil {
			var failure *exec.ExitError
			if !errors.As(err, &failure) {
				t.Fatal(err)
			}
			exit = failure.ExitCode()
		}
		return exit, stdout.String(), stderr.String()
	})
}

func testChangePlanCandidatePolicySupport(t *testing.T, run func(testing.TB, []string, string, []string) (int, string, string)) {
	t.Helper()
	for _, test := range []struct {
		name, operation, runtime string
		active, exposed          bool
		selector                 []string
		exit                     int
	}{
		{name: "root present", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, active: true, exit: 3},
		{name: "root activation", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, exit: 3},
		{name: "exposure activation", operation: `{"kind":"interface_root","action":"present","root":"expose","interface":"email.send/v1"}`, exit: 3},
		{name: "selection", operation: `{"kind":"implementation_selection","action":"set","interface":"email.send/v1","constructor":"example.com/acme/policy/smtp.New"}`, active: true, exit: 3},
		{name: "selection removal", operation: `{"kind":"implementation_selection","action":"remove","interface":"email.send/v1"}`, active: true, exit: 3},
		{name: "configuration", operation: `{"kind":"configuration","action":"set","owner":"example.com/acme/policy/smtp.New","path":"endpoint","value":"PRIVATE_NEW"}`, active: true, exit: 3},
		{name: "environment", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, active: true, selector: []string{"--env", "production"}, exit: 3},
		{name: "replacement", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, active: true, selector: []string{"--config", "deploy/customer.yaml"}, exit: 3},
		{name: "remaining exposure", operation: `{"kind":"interface_root","action":"absent","root":"require","interface":"email.send/v1"}`, active: true, exposed: true, exit: 3},
		{name: "root deactivation", operation: `{"kind":"interface_root","action":"absent","root":"require","interface":"email.send/v1"}`, active: true},
		{name: "batch deactivation", operation: `{"kind":"interface_root","action":"absent","root":"require","interface":"email.send/v1"},{"kind":"interface_root","action":"absent","root":"expose","interface":"email.send/v1"}`, active: true, exposed: true},
		{name: "batch root move", operation: `{"kind":"interface_root","action":"absent","root":"require","interface":"email.send/v1"},{"kind":"interface_root","action":"present","root":"expose","interface":"email.send/v1"}`, active: true, exit: 3},
		{name: "batch activation and configuration", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"},{"kind":"configuration","action":"set","owner":"example.com/acme/policy/smtp.New","path":"endpoint","value":"PRIVATE_NEW"}`, exit: 3},
		{name: "dormant configuration", operation: `{"kind":"configuration","action":"set","owner":"example.com/acme/policy/smtp.New","path":"endpoint","value":"PRIVATE_NEW"}`},
		{name: "compiler failure", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, active: true, runtime: "invalid", exit: 8},
		{name: "compatible", operation: `{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}`, active: true, runtime: "compatible"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			writeCommandFile(t, filepath.Join(root, "smtp/service.go"), `package smtp
import (
 "context"
 sendv1 "example.com/acme/policy/interfaces/email/send/v1"
)
type Config struct { Endpoint string }
type Service struct{}
//plystra:implements email.send/v1
func New(Config) (*Service, error) { panic("PRIVATE_CONSTRUCTOR_MUST_NOT_RUN") }
func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) { panic("PRIVATE_BUSINESS_MUST_NOT_RUN") }
`)
			source := previewPolicyKernel
			if test.runtime != "compatible" {
				source = strings.Replace(source, "PolicySchemaVersion = 1", "PolicySchemaVersion = 2", 1)
			}
			if test.runtime == "invalid" {
				source = "package invocation\nvar _ = PRIVATE_COMPILER_FAILURE\n"
			}
			kernel := writePreviewPolicyKernel(t, root, source)
			require := "[]"
			if test.active {
				require = "[email.send/v1]"
			}
			configuration := "interfaces:\n  require: " + require + "\n  use: {email.send/v1: example.com/acme/policy/smtp.New}\n  policies: {email.send/v1: {timeout: 5s}}\nconfig:\n  example.com/acme/policy/smtp.New: {endpoint: PRIVATE_OLD}\n"
			if test.exposed {
				configuration += "http: {expose: {email.send/v1: {transport: connect}}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
			selected := "plystra.yaml"
			if len(test.selector) > 0 {
				selected = "plystra.production.yaml"
				data := "interfaces: {policies: {email.send/v1: {timeout: 2s}}}\n"
				if test.selector[0] == "--config" {
					selected, data = "deploy/customer.yaml", configuration
				}
				writeCommandFile(t, filepath.Join(root, selected), data)
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[`+test.operation+`]}`)
			before, kernelBefore := commandTree(t, root), commandTree(t, kernel)
			arguments := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, test.selector...)
			exit, stdout, stderr := run(t, arguments, root, commandGoEnvironment())
			if exit != test.exit || stderr != "" {
				t.Fatalf("preview exit = %d; want %d\n%s\n%s", exit, test.exit, stdout, stderr)
			}
			var result struct {
				Status      string
				Diagnostics []struct {
					Code, Message string
					Locations     []struct{ Path string }
				}
				Recovery []json.RawMessage
				Effects  struct {
					Observed []struct{ ID, Class string }
				}
				Payload struct {
					PlanDigest         string `json:"plan_digest"`
					Effects            json.RawMessage
					EffectRequirements []struct{ ID, Class string } `json:"effect_requirements"`
				}
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stdout, root) || strings.Contains(stdout, kernel) || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("preview exposed private inputs: %s", stdout)
			}
			if test.exit == 3 && (result.Status != "validation_failed" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_POLICY_NOT_ENFORCED" || len(result.Diagnostics[0].Locations) == 0 || result.Diagnostics[0].Locations[0].Path != selected || len(result.Recovery) != 1) {
				t.Fatalf("policy failure lost identity or recovery: %s", stdout)
			}
			if test.exit == 8 && result.Status != "execution_failed" {
				t.Fatalf("compiler failure became a compatibility verdict: %s", stdout)
			}
			started := false
			for _, effect := range result.Effects.Observed {
				started = started || effect.ID == "policy-support-go" && effect.Class == "trusted_code_execution"
			}
			wantStarted := test.exit != 0 || test.runtime == "compatible"
			if started != wantStarted {
				t.Fatalf("support tooling observed = %v; want %v: %s", started, wantStarted, stdout)
			}
			if test.exit == 0 {
				var document map[string]json.RawMessage
				if err := json.Unmarshal([]byte(stdout), &document); err != nil {
					t.Fatal(err)
				}
				var outerEffects, payloadEffects any
				if err := json.Unmarshal(document["effects"], &outerEffects); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(result.Payload.Effects, &payloadEffects); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(outerEffects, payloadEffects) {
					t.Fatalf("result and payload effects differ: %s", stdout)
				}
				for _, effect := range result.Payload.EffectRequirements {
					if effect.ID == "policy-support-go" || effect.ID == "policy-support-module" {
						t.Fatal("observed query entered stable effect requirements")
					}
				}
				_, repeated, _ := run(t, arguments, root, commandGoEnvironment())
				var second struct {
					Payload struct {
						PlanDigest string `json:"plan_digest"`
					}
				}
				if err := json.Unmarshal([]byte(repeated), &second); err != nil || result.Payload.PlanDigest == "" || result.Payload.PlanDigest != second.Payload.PlanDigest {
					t.Fatalf("repeated preview changed plan identity: %v\n%s", err, repeated)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) || !reflect.DeepEqual(kernelBefore, commandTree(t, kernel)) {
				t.Fatal("preview changed Project or selected Kernel files")
			}
		})
	}
}

func TestChangePlanResourceDecisionsCheckPolicySupport(t *testing.T) {
	for _, test := range []struct {
		name                                 string
		compatible, broken, dormant, missing bool
		exit                                 int
	}{
		{name: "unsupported complete assignments", exit: 3},
		{name: "unverified complete assignments", broken: true, exit: 8},
		{name: "supported assignments", compatible: true},
		{name: "dormant assignments", dormant: true},
		{name: "unsupported previous provider", missing: true},
		{name: "unverified previous provider", missing: true, broken: true, exit: 8},
		{name: "supported previous provider", missing: true, compatible: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			source := previewPolicyKernel
			if !test.compatible {
				source = strings.Replace(source, "PolicySchemaVersion = 1", "PolicySchemaVersion = 2", 1)
			}
			if test.broken {
				source = "package invocation\nvar _ = PRIVATE_COMPILER_FAILURE\n"
			}
			kernel := writePreviewPolicyKernel(t, root, source)
			document := string(readCommandFile(t, root, "plystra.yaml"))
			require := "[email.send/v1]"
			if test.dormant {
				require = "[]"
			}
			document += "interfaces:\n  require: " + require + "\n  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}\n  policies: {email.send/v1: {timeout: 5s}}\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			provider := "example.com/acme/implementation-use/ambiguous.New"
			writeCommandFile(t, filepath.Join(root, "ambiguous", "provider.go"), `package ambiguous
import "example.com/acme/implementation-use/database"
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(replica database.Resource) (*Value, error) { panic("PRIVATE_CONSTRUCTOR_MUST_NOT_RUN") }
func (*Value) Read() {}
`)
			if test.missing {
				writeRequiredResourceCommandProvider(t, root)
				provider = "example.com/acme/implementation-use/required.New"
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"`+provider+`"}]}`)
			before, kernelBefore := commandTree(t, root), commandTree(t, kernel)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, commandGoEnvironment())
			if exit != test.exit || stderr != "" || strings.Contains(stdout, "PRIVATE_") || strings.Contains(stdout, root) || strings.Contains(stdout, kernel) {
				t.Fatalf("Resource policy preview = %d; want %d\n%s\n%s", exit, test.exit, stdout, stderr)
			}
			var result struct {
				Diagnostics []struct{ Code string }
				Payload     struct {
					Impact struct {
						Decisions []struct{ Alternatives []string } `json:"unresolved_decisions"`
					} `json:"configuration_impact"`
				}
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if exit == 3 && (len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_POLICY_NOT_ENFORCED") {
				t.Fatalf("assignment failure lost policy diagnostic: %s", stdout)
			}
			if test.missing && exit == 0 {
				if len(result.Payload.Impact.Decisions) == 0 || strings.Contains(stdout, "retain-previous-provider") != test.compatible {
					t.Fatalf("missing Config decision offered an unsupported previous provider: %s", stdout)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) || !reflect.DeepEqual(kernelBefore, commandTree(t, kernel)) {
				t.Fatal("Resource policy preview changed inputs")
			}
		})
	}
}

func writePreviewPolicyKernel(t *testing.T, root, source string) string {
	t.Helper()
	kernel := t.TempDir()
	writeCommandFile(t, filepath.Join(kernel, "go.mod"), "module github.com/plystra/kernel\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(kernel, "invocation/policy.go"), source)
	module := string(readCommandFile(t, root, "go.mod"))
	start := strings.Index(module, "replace github.com/plystra/kernel => ")
	if start < 0 {
		t.Fatal("fixture has no Kernel replacement")
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), module[:start]+"replace github.com/plystra/kernel => "+filepath.ToSlash(kernel)+"\n")
	return kernel
}

func TestChangePlanSourceSelectionRetainsEarlyPolicyEffects(t *testing.T) {
	for _, broken := range []bool{false, true} {
		name, wantExit := "unsupported", 3
		source := strings.Replace(previewPolicyKernel, "PolicySchemaVersion = 1", "PolicySchemaVersion = 2", 1)
		if broken {
			name, wantExit = "unverified", 8
			source = "package invocation\nvar _ = PRIVATE_COMPILER_FAILURE\n"
		}
		t.Run(name, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			kernel := writePreviewPolicyKernel(t, root, source)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  use: {email.send/v1: example.com/acme/policy/smtp.New}\n  policies: {email.send/v1: {timeout: 5s}}\n")
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"reports.read","semantics":"query"},{"kind":"interface_root","action":"present","root":"require","interface":"email.send/v1"}]}`)
			before, kernelBefore := commandTree(t, root), commandTree(t, kernel)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, commandGoEnvironment())
			if exit != wantExit || stderr != "" || strings.Contains(stdout, "PRIVATE_") || strings.Contains(stdout, root) || strings.Contains(stdout, kernel) {
				t.Fatalf("staged selection policy = %d; want %d\n%s\n%s", exit, wantExit, stdout, stderr)
			}
			var result struct {
				Diagnostics []struct{ Code string }
				Effects     struct{ Observed []struct{ ID string } }
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if !broken && (len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "PLYSTRA_POLICY_NOT_ENFORCED") {
				t.Fatalf("staged selection lost policy diagnostic: %s", stdout)
			}
			counts := make(map[string]int)
			for _, effect := range result.Effects.Observed {
				counts[effect.ID]++
			}
			if counts["candidate-policy-go"] != 1 || counts["candidate-policy-module"] != 1 || counts["candidate-build"] != 0 {
				t.Fatalf("early policy failure lost effects or reached build: %s", stdout)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) || !reflect.DeepEqual(kernelBefore, commandTree(t, kernel)) {
				t.Fatal("staged selection policy failure changed inputs")
			}
		})
	}
}

const previewPolicyKernel = `package invocation
import "time"
type Policy struct {
 SchemaVersion, CompilerVersion, DefaultsVersion int
 Timeout time.Duration
 ConcurrencyLimit, QueueLimit int
 Retry RetryPolicy
 Circuit CircuitPolicy
}
type RetryPolicy struct { Eligibility string; MaxAttempts int; Backoff time.Duration }
type CircuitPolicy struct { FailureThreshold int; OpenFor time.Duration; ProbeLimit int }
const (
 PolicySchemaVersion = 1
 PolicyCompilerVersion = 1
 PolicyDefaultsVersion = 1
 DefaultConcurrencyLimit = 64
 MaximumConcurrencyLimit = 65536
 MaximumPolicyDuration = 9223372036854775807
 MaximumRetryAttempts = 16
 RetryReplaySafe = "replay_safe"
)
func init() { panic("PRIVATE_KERNEL_INIT_MUST_NOT_RUN") }
`
