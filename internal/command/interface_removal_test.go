package command_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/invocationpolicy"
)

func TestPublicInterfaceTombstonesAcrossSelections(t *testing.T) {
	removals := "interfaces:\n  require: [email.send/v1]\n  use: {email.send/v1: {$remove: true}, kernel.info/v1: {$remove: true}}\n  policies: {email.send/v1: {$remove: true}}\nhttp: {expose: {email.send/v1: {$remove: true}}}\n"
	lower := "interfaces:\n  require: [email.send/v1]\n  use: {email.send/v1: example.com/acme/policy/smtp.New}\n  policies: {email.send/v1: {timeout: 5s}}\nhttp: {expose: {email.send/v1: {transport: connect}}}\n"
	for _, mode := range []string{"default", "environment", "replacement", "self-adoption"} {
		t.Run(mode, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			selectedPath := "plystra.yaml"
			rootData := removals
			var selector []string
			switch mode {
			case "environment":
				rootData, selectedPath = lower, "plystra.production.yaml"
				selector = []string{"--env", "production"}
			case "replacement":
				rootData, selectedPath = lower, "deploy/customer.yaml"
				selector = []string{"--config", "deploy/customer.yaml"}
			case "self-adoption":
				rootData = "composition:\n  exports:\n    defaults:\n      interfaces:\n        use: {email.send/v1: example.com/acme/policy/smtp.New}\n        policies: {email.send/v1: {timeout: 5s}}\n  adopt: [{module: example.com/acme/policy, export: defaults}]\n" + removals
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			if selectedPath != "plystra.yaml" {
				writeCommandFile(t, filepath.Join(root, selectedPath), removals)
			}
			selectedBefore := string(readCommandFile(t, root, selectedPath))
			assertCommandInvocationPolicy(t, root, selector, commandGoEnvironment(), invocationpolicy.Default())
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			bindings := manifest.InterfaceProvenance().Bindings()
			if len(bindings) != 1 || len(bindings[0].ExposureSources()) != 0 || bindings[0].Selection().Reason() == interfaceprovenance.SelectionExplicit {
				t.Fatalf("removed exposure or explicit selection remains effective: %#v", bindings)
			}
			if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, selectedPath)) != selectedBefore {
				t.Fatal("generation changed authored exclusion intent")
			}
		})
	}
}

func TestPublicInvalidInterfaceRemovalsNeverMutate(t *testing.T) {
	for _, field := range []string{
		"interfaces: {use: {email.send/v1: %s}}\n",
		"interfaces: {policies: {email.send/v1: %s}}\n",
		"http: {expose: {email.send/v1: %s}}\n",
	} {
		for _, value := range []string{"null", "{$remove: false}"} {
			for _, mode := range []string{"default", "environment", "replacement"} {
				t.Run(mode+"/"+fmt.Sprintf(field, value), func(t *testing.T) {
					root := t.TempDir()
					writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/application\n\ngo 1.26\n")
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
					selectedPath := "plystra.yaml"
					var selector []string
					switch mode {
					case "environment":
						selectedPath, selector = "plystra.production.yaml", []string{"--env", "production"}
					case "replacement":
						selectedPath, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
					}
					writeCommandFile(t, filepath.Join(root, selectedPath), fmt.Sprintf(field, value))
					before := commandTree(t, root)
					for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
						args := append(append([]string(nil), invocation...), selector...)
						code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
						if code != 1 || stdout != "" || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 || !strings.Contains(stderr, selectedPath) {
							t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
						}
						if !reflect.DeepEqual(commandTree(t, root), before) {
							t.Fatalf("%v mutated invalid configuration", args)
						}
						assertNoCommandTransactions(t, root)
					}
				})
			}
		}
	}
}
