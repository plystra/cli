package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/invocationpolicy"
	"github.com/plystra/cli/internal/testkernel"
)

func TestPublicGenerationRejectsLegacyTimeoutAndRetryPolicies(t *testing.T) {
	root := writeCommandPolicyProject(t)
	writeCommandFile(t, filepath.Join(root, "business", "plugin.yaml"), "id: acme.business\nprovides: [audit.write/v1]\n")
	writeCommandFile(t, filepath.Join(root, "business", "capabilities", "audit.write", "v1", "capability.yaml"), "id: audit.write/v1\nrequest: {}\nresponse: {}\n")
	dependency := t.TempDir()
	writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module example.com/policy-export\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), "composition:\n  exports:\n    defaults:\n      interfaces:\n        policies: {audit.write/v1: {timeout: 5s, retry: {eligibility: replay_safe}}}\n")
	mod := string(readCommandFile(t, root, "go.mod"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/policy-export v1.0.0\nreplace example.com/policy-export => "+filepath.ToSlash(dependency)+"\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "composition:\n  adopt: [{module: example.com/policy-export, export: defaults}]\ncapabilities: {require: [audit.write/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {audit.write/v1: {timeout: 2s, retry: {eligibility: replay_safe, max_attempts: 3}}}}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "untouched\n")
	before, dependencyBefore := commandTree(t, root), commandTree(t, dependency)
	for _, mode := range []struct {
		selector []string
		source   string
	}{
		{source: "example.com/policy-export:plystra.yaml"},
		{selector: []string{"--env", "production"}, source: "example.com/acme/policy:plystra.production.yaml"},
	} {
		for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
			args := append(append([]string(nil), invocation...), mode.selector...)
			code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
			if code != 1 || stdout != "" || !commandContainsAll(stderr,
				diagnosticcode.PolicyNotEnforced,
				`interfaces.policies["audit.write/v1"].timeout`,
				"installed CLI ", ", Kernel ",
				"specified=yes parsed=yes generated=yes executed=no accepted=no",
				"Source: "+mode.source+":1:1 (configuration-declaration)",
				"Remove the reported policy", "plystra inspect capabilities --format json",
			) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Contains(stderr, root) {
				t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
				t.Fatal("policy failure changed a Project")
			}
			assertNoCommandTransactions(t, root)
		}
		args := append([]string{"inspect", "interfaces", "--format", "json"}, mode.selector...)
		if code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment()); code != 0 {
			t.Fatalf("read-only inspection = %d, %q, %q", code, stdout, stderr)
		}
		if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
			t.Fatal("read-only inspection changed a Project")
		}
	}
}

func TestPublicGenerationEnforcesActiveTimeoutPolicies(t *testing.T) {
	root := writeCommandPolicyProject(t)
	configuration := "interfaces:\n  require: [email.send/v1]\n  policies:\n    email.send/v1: {timeout: 5s}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), strings.Replace(configuration, "5s", "2s", 1))
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), strings.Replace(configuration, "5s", "7s", 1))
	for _, mode := range []struct {
		name        string
		selector    []string
		environment map[string]string
		timeout     time.Duration
	}{
		{name: "default", timeout: 5 * time.Second},
		{name: "environment", selector: []string{"--env", "production"}, timeout: 2 * time.Second},
		{name: "replacement", selector: []string{"--config", "deploy/customer.yaml"}, timeout: 7 * time.Second},
		{name: "ambient environment", environment: map[string]string{"PLYSTRA_ENV": "production"}, timeout: 2 * time.Second},
		{name: "ambient replacement", environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, timeout: 7 * time.Second},
		{name: "explicit override", selector: []string{"--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "missing", "PLYSTRA_CONFIG": "missing.yaml"}, timeout: 7 * time.Second},
	} {
		t.Run(mode.name, func(t *testing.T) {
			assertCommandTimeoutPolicy(t, root, mode.selector, commandGoEnvironmentWith(mode.environment), mode.timeout)
		})
	}
}

func TestPublicGenerationEnforcesAdoptedPolicyAndCurrentReplacement(t *testing.T) {
	root := writeCommandPolicyProject(t)
	dependency := t.TempDir()
	writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module example.com/policy-export\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), "composition:\n  exports:\n    defaults:\n      interfaces:\n        policies: {email.send/v1: {timeout: 5s}}\n")
	mod := string(readCommandFile(t, root, "go.mod"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/policy-export v1.0.0\nreplace example.com/policy-export => "+filepath.ToSlash(dependency)+"\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "composition:\n  adopt: [{module: example.com/policy-export, export: defaults}]\ninterfaces: {require: [email.send/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {email.send/v1: {timeout: 2s}}}\n")
	dependencyBefore := commandTree(t, dependency)
	assertCommandTimeoutPolicy(t, root, nil, commandGoEnvironment(), 5*time.Second)
	assertCommandTimeoutPolicy(t, root, []string{"--env", "production"}, commandGoEnvironment(), 2*time.Second)
	if !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
		t.Fatal("generation changed adopted source")
	}
}

func assertCommandTimeoutPolicy(t *testing.T, root string, selector, environment []string, timeout time.Duration) {
	t.Helper()
	want := invocationpolicy.Default()
	want.Timeout = timeout
	assertCommandInvocationPolicy(t, root, selector, environment, want)
}

func assertCommandInvocationPolicy(t *testing.T, root string, selector, environment []string, want invocationpolicy.Policy) {
	t.Helper()
	args := append([]string{"generate"}, selector...)
	if code, stdout, stderr := runCommand(t, args, filepath.Join(root, "smtp"), environment); code != 0 {
		t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
	}
	manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	bindings := manifest.InterfaceProvenance().Bindings()
	if len(bindings) != 1 || bindings[0].Policy().Compiled() != want {
		t.Fatalf("compiled invocation policy = %#v; want %#v", bindings, want)
	}
	before := commandTree(t, root)
	for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "interfaces", "--format", "json"}} {
		args := append(append([]string(nil), invocation...), selector...)
		if code, stdout, stderr := runCommand(t, args, root, environment); code != 0 {
			t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
		}
		if !reflect.DeepEqual(commandTree(t, root), before) {
			t.Fatalf("%v mutated the Project", args)
		}
		assertNoCommandTransactions(t, root)
	}
}

func writeCommandPolicyProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := testkernel.Root(t)
	goMod := fmt.Sprintf(`module example.com/acme/policy

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandGraphInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "smtp", "service.go"), `package smtp

import (
	"context"

	sendv1 "example.com/acme/policy/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
	return root
}
