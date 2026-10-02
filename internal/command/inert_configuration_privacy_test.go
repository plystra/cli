package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicInertUnvalidatedConfigurationDoesNotPublishValueHashes(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			var selector []string
			switch mode {
			case "environment":
				selector = []string{"--env", "production"}
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
			case "replacement":
				selector = []string{"--config", "deploy/customer.yaml"}
				writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), "{}\n")
			}
			document := func(marker string) string {
				return "composition:\n  exports:\n    unavailable:\n      config:\n        example.com/unavailable/service.New: {password: {env: " + marker + "}, " + marker + ": [1, 2]}\n    invalid:\n      config:\n        example.com/acme/implementation-use/smtp.New: {endpoint: [" + marker + "]}\n"
			}
			invoke := func(arguments ...string) string {
				t.Helper()
				arguments = append(arguments, selector...)
				code, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
					t.Fatalf("%v = %d, %q, %q", arguments, code, stdout, stderr)
				}
				return stdout
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document("PRIVATE_FIRST"))
			invoke("generate")
			before := commandTree(t, filepath.Join(root, "generated"))
			inspection := invoke("inspect", "configuration", "--format", "json")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document("PRIVATE_SECOND"))
			unchanged := commandTree(t, root)
			invoke("generate", "--check")
			invoke("check")
			if got := invoke("inspect", "configuration", "--format", "json"); got != inspection {
				t.Fatal("private unvalidated export values changed public inspection")
			}
			if !reflect.DeepEqual(commandTree(t, root), unchanged) {
				t.Fatal("read-only checks mutated the Project")
			}
			invoke("generate")
			if !reflect.DeepEqual(commandTree(t, filepath.Join(root, "generated")), before) {
				t.Fatal("private unvalidated export values changed public generated artifacts")
			}
			if got := string(readCommandFile(t, root, "plystra.yaml")); got != document("PRIVATE_SECOND") {
				t.Fatal("generation rewrote inert authored values")
			}
			for _, export := range []struct{ name, code string }{
				{"unavailable", "PLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID"},
				{"invalid", "PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID"},
			} {
				adopt := "adopt: [{module: example.com/acme/implementation-use, export: " + export.name + "}]\n"
				switch mode {
				case "default":
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document("PRIVATE_SECOND")+"  "+adopt)
				case "environment":
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "composition:\n  "+adopt)
				case "replacement":
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), "composition:\n  "+adopt)
				}
				before := commandTree(t, root)
				for _, args := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args = append(args, selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code == 0 || stdout != "" || !strings.Contains(stderr, export.code) || strings.Contains(stderr, "PRIVATE_") {
						t.Fatalf("adopt %s, %v = %d, %q, %q", export.name, args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatal("invalid export adoption mutated the Project")
					}
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
