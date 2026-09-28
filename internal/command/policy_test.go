package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/testkernel"
)

func TestPublicGenerationRejectsUnenforcedActivePolicies(t *testing.T) {
	root := writeCommandPolicyProject(t)
	configuration := "interfaces:\n  require: [email.send/v1]\n  policies:\n    email.send/v1: {timeout: 5s}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), strings.Replace(configuration, "5s", "2s", 1))
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), strings.Replace(configuration, "5s", "7s", 1))
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "untouched\n")
	before := commandTree(t, root)
	for _, mode := range []struct {
		name        string
		selector    []string
		environment map[string]string
		path        string
	}{
		{name: "default", path: "plystra.yaml"},
		{name: "environment", selector: []string{"--env", "production"}, path: "plystra.production.yaml"},
		{name: "replacement", selector: []string{"--config", "deploy/customer.yaml"}, path: "deploy/customer.yaml"},
		{name: "ambient environment", environment: map[string]string{"PLYSTRA_ENV": "production"}, path: "plystra.production.yaml"},
		{name: "ambient replacement", environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, path: "deploy/customer.yaml"},
		{name: "explicit override", selector: []string{"--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "missing", "PLYSTRA_CONFIG": "missing.yaml"}, path: "deploy/customer.yaml"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			environment := commandGoEnvironmentWith(mode.environment)
			for _, command := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
				args := append(append([]string(nil), command...), mode.selector...)
				code, stdout, stderr := runCommand(t, args, filepath.Join(root, "smtp"), environment)
				if code != 1 || stdout != "" || !commandContainsAll(stderr,
					diagnosticcode.PolicyNotEnforced,
					`interfaces.policies["email.send/v1"].timeout`,
					"installed CLI ", ", Kernel ",
					"specified=yes parsed=yes generated=yes executed=no accepted=no",
					"Source: example.com/acme/policy:"+mode.path+":1:1 (configuration-declaration)",
					"Remove the reported policy", "plystra inspect capabilities --format json",
				) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 ||
					strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
					t.Fatalf("%v = exit %d stdout %q stderr %q", args, code, stdout, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%v changed the rejected Project", args)
				}
				assertNoCommandTransactions(t, root)
			}
			args := append([]string{"inspect", "interfaces", "--format", "json"}, mode.selector...)
			code, stdout, stderr := runCommand(t, args, root, environment)
			if code != 0 || stderr != "Resolving selected application model...\n" || !strings.Contains(stdout, "email.send/v1") {
				t.Fatalf("read-only inspection = exit %d stdout %q stderr %q", code, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("read-only inspection changed the Project")
			}
		})
	}
}

func TestPublicGenerationReportsAdoptedPolicyAndCurrentReplacement(t *testing.T) {
	root := writeCommandPolicyProject(t)
	dependency := t.TempDir()
	writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module example.com/policy-export\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), "composition:\n  exports:\n    defaults:\n      interfaces:\n        policies: {email.send/v1: {timeout: 5s}}\n")
	mod := string(readCommandFile(t, root, "go.mod"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/policy-export v1.0.0\nreplace example.com/policy-export => "+filepath.ToSlash(dependency)+"\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "composition:\n  adopt: [{module: example.com/policy-export, export: defaults}]\ninterfaces: {require: [email.send/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {email.send/v1: {timeout: 2s}}}\n")
	before, dependencyBefore := commandTree(t, root), commandTree(t, dependency)
	for _, mode := range []struct {
		selector []string
		source   string
	}{
		{source: "example.com/policy-export:plystra.yaml"},
		{selector: []string{"--env", "production"}, source: "example.com/acme/policy:plystra.production.yaml"},
	} {
		for _, command := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
			args := append(append([]string(nil), command...), mode.selector...)
			code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
			if code != 1 || stdout != "" || !commandContainsAll(stderr, diagnosticcode.PolicyNotEnforced, "Source: "+mode.source+":1:1 (configuration-declaration)") || strings.Count(stderr, "Source: ") != 1 {
				t.Fatalf("adopted policy %v = %d, %q, %q", args, code, stdout, stderr)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
				t.Fatal("policy failure changed a Project")
			}
			assertNoCommandTransactions(t, root)
		}
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
	go.yaml.in/yaml/v3 v3.0.4
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
