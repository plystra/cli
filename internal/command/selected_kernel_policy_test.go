package command_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/testkernel"
)

func TestBuiltCLIRejectsApplicationSelectedKernelPolicy(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"missing", "environment", "replacement", "compile failure"} {
		t.Run(mode, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			kernel := t.TempDir()
			writeCommandFile(t, filepath.Join(kernel, "go.mod"), "module github.com/plystra/kernel\ngo 1.26\n")
			if mode != "missing" {
				source := `package invocation
const (
PolicySchemaVersion = 2
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
				if mode == "compile failure" {
					source = "package invocation\nvar _ = PRIVATE_COMPILE_FAILURE\n"
				}
				writeCommandFile(t, filepath.Join(kernel, "invocation", "policy.go"), source)
			}
			module := strings.Replace(string(readCommandFile(t, root, "go.mod")), filepath.ToSlash(testkernel.Root(t)), filepath.ToSlash(kernel), 1)
			writeCommandFile(t, filepath.Join(root, "go.mod"), module)
			configuration := "interfaces: {require: [email.send/v1], policies: {email.send/v1: {timeout: 5s}}}\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
			var selector []string
			selectedPath := "plystra.yaml"
			switch mode {
			case "environment":
				selectedPath, selector = "plystra.production.yaml", []string{"--env", "production"}
				writeCommandFile(t, filepath.Join(root, selectedPath), "interfaces: {policies: {email.send/v1: {timeout: 2s}}}\n")
			case "replacement":
				selectedPath, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				writeCommandFile(t, filepath.Join(root, selectedPath), configuration)
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), `{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"unused.read","semantics":"query"}]}`)
			before, kernelBefore := commandTree(t, root), commandTree(t, kernel)
			for _, invocation := range [][]string{{"generate", "--check", "--format", "json"}, {"generate", "--format", "json"}, {"change", "plan", "--request", "change.json", "--format", "json"}, {"generate", "--check"}, {"check"}} {
				arguments := append(append([]string(nil), invocation...), selector...)
				process := exec.CommandContext(t.Context(), binary, arguments...)
				process.Dir, process.Env = root, commandGoEnvironment()
				var stderr bytes.Buffer
				process.Stderr = &stderr
				stdout, err := process.Output()
				wantExit := 3
				if mode == "compile failure" {
					wantExit = 8
				}
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != wantExit {
					t.Fatalf("%v = %v\n%s\n%s", arguments, err, stdout, &stderr)
				}
				combined := string(stdout) + stderr.String()
				if strings.Contains(combined, kernel) || strings.Contains(combined, root) || strings.Contains(combined, "PRIVATE_") {
					t.Fatalf("policy failure exposed private input: %s", combined)
				}
				if invocation[0] != "check" && len(invocation) > 2 {
					var result struct {
						Status      string
						Diagnostics []struct {
							Code, Message string
							Locations     []struct{ Path string }
						}
						Recovery []json.RawMessage
						Effects  struct{ Observed []struct{ ID, Class string } }
					}
					if stderr.Len() != 0 || json.Unmarshal(stdout, &result) != nil || len(result.Diagnostics) != 1 {
						t.Fatalf("structured policy failure: %s\n%s", stdout, &stderr)
					}
					if mode == "compile failure" {
						if result.Status != "execution_failed" || !strings.Contains(result.Diagnostics[0].Message, "could not be verified") {
							t.Fatalf("operational support failure: %s", stdout)
						}
					} else if result.Status != "validation_failed" || result.Diagnostics[0].Code != "PLYSTRA_POLICY_NOT_ENFORCED" || !strings.Contains(result.Diagnostics[0].Message, "selected Kernel v0.0.0 (local replacement)") || !strings.Contains(result.Diagnostics[0].Message, "executed=no") || len(result.Recovery) != 1 || len(result.Diagnostics[0].Locations) == 0 || result.Diagnostics[0].Locations[0].Path != selectedPath {
						t.Fatalf("policy diagnostic: %s", stdout)
					}
					if mode != "missing" {
						wantID := "policy-support-go"
						if invocation[0] == "change" {
							wantID = "candidate-policy-go"
						}
						found := false
						for _, effect := range result.Effects.Observed {
							found = found || effect.ID == wantID && effect.Class == "trusted_code_execution"
						}
						if !found {
							t.Fatalf("compiler execution was omitted: %s", stdout)
						}
					}
				} else if mode != "compile failure" && (!strings.Contains(combined, "PLYSTRA_POLICY_NOT_ENFORCED") || !strings.Contains(combined, "Recovery:")) {
					t.Fatalf("human policy failure: %s", combined)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, kernel), kernelBefore) {
					t.Fatal("rejected selected Kernel changed Project or replacement files")
				}
			}
		})
	}
}
