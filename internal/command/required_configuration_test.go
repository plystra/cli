package command_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicMissingRequiredConstructorConfigurationDoesNotMutate(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			selected := "plystra.yaml"
			var selector []string
			switch mode {
			case "environment":
				selected, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement":
				selected, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			for _, active := range []bool{false, true} {
				selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
				if active {
					selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
				}
				for _, entry := range []string{"{}", "{endpoint: {$remove: true}}", "{$remove: true}", ""} {
					if !active && (entry == "" || entry == "{$remove: true}") {
						continue
					}
					configuration := ""
					if entry != "" {
						configuration = "config: {" + constructor + ": " + entry + "}\n"
					}
					writeCommandFile(t, filepath.Join(root, selected), selection+configuration)
					before := commandTree(t, root)
					for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
						args := append(append([]string(nil), invocation...), selector...)
						code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
						if code == 0 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID") || !strings.Contains(stderr, "required constructor configuration field is missing") || !strings.Contains(stderr, selected+":1:1") {
							t.Fatalf("active=%t, entry=%s, %v = %d, %q, %q", active, entry, args, code, stdout, stderr)
						}
						if !strings.Contains(stderr, "Supply the missing required field in "+selected+" or an adopted export") {
							t.Fatalf("%v omitted selected-document requiredness recovery: %s", args, stderr)
						}
						if !reflect.DeepEqual(commandTree(t, root), before) {
							t.Fatalf("%v mutated incomplete Project", args)
						}
					}
					assertNoCommandTransactions(t, root)
				}
			}
		})
	}
}

func TestPublicRequiredConstructorConfigurationComposesPartialExports(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			implementationPath := filepath.Join(root, "smtp", "implementation.go")
			implementation, err := os.ReadFile(implementationPath)
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, implementationPath, strings.Replace(string(implementation), "type Config struct {", "type Config struct {\n\tSettings struct { Region string `plystra:\"required\"` }", 1))
			exports := "composition:\n  exports:\n    endpoint:\n      config: {" + constructor + ": {endpoint: PRIVATE_ENDPOINT}}\n    settings:\n      config: {" + constructor + ": {settings: {region: PRIVATE_REGION}}}\n"
			selected, selector := "plystra.yaml", []string(nil)
			switch mode {
			case "environment":
				selected, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement":
				selected, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), exports)
			for _, active := range []bool{false, true} {
				selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
				if active {
					selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
				}
				adoption := "  adopt: [{module: example.com/acme/implementation-use, export: endpoint}, {module: example.com/acme/implementation-use, export: settings}]\n"
				prefix := "composition:\n" + adoption
				if mode == "default" {
					prefix = exports + adoption
				}
				for _, entry := range []string{"{}", "{endpoint: {$remove: true}}", `{endpoint: ""}`} {
					source := prefix + selection + "config: {" + constructor + ": " + entry + "}\n"
					writeCommandFile(t, filepath.Join(root, selected), source)
					before := commandTree(t, root)
					args := append([]string{"generate"}, selector...)
					code, _, stderr := runCommand(t, args, root, commandGoEnvironment())
					if strings.Contains(entry, "$remove") {
						if code == 0 || !strings.Contains(stderr, "required constructor configuration field is missing") || !strings.Contains(stderr, selected+":1:1") || strings.Contains(stderr, "PRIVATE_") {
							t.Fatalf("active=%t, entry=%s: %d, %s", active, entry, code, stderr)
						}
						if !reflect.DeepEqual(before, commandTree(t, root)) {
							t.Fatal("required field removal mutated Project")
						}
						continue
					}
					if code != 0 {
						t.Fatalf("partial exports, active=%t, entry=%s: %d, %s", active, entry, code, stderr)
					}
					if data, err := os.ReadFile(filepath.Join(root, selected)); err != nil || string(data) != source {
						t.Fatal("generation rewrote partial authored configuration")
					}
					args = append([]string{"generate", "--check"}, selector...)
					if code, _, stderr := runCommand(t, args, root, commandGoEnvironment()); code != 0 {
						t.Fatalf("generated fixed point: %d, %s", code, stderr)
					}
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
