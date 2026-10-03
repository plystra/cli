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
				return "config: {example.com/unavailable/service.New: {password: {env: " + marker + "}, " + marker + ": [1, 2]}}\n"
			}
			relationship, dependency := writeCommandTemplate(t, root, "unselected", document("PRIVATE_FIRST"))
			invoke := func(arguments ...string) string {
				t.Helper()
				arguments = append(arguments, selector...)
				code, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
					t.Fatalf("%v = %d, %q, %q", arguments, code, stdout, stderr)
				}
				return stdout
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			invoke("generate")
			before := commandTree(t, filepath.Join(root, "generated"))
			inspection := invoke("inspect", "configuration", "--format", "json")
			writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), document("PRIVATE_SECOND"))
			unchanged := commandTree(t, root)
			invoke("generate", "--check")
			invoke("check")
			if got := invoke("inspect", "configuration", "--format", "json"); got != inspection {
				t.Fatal("private unvalidated dependency values changed public inspection")
			}
			if !reflect.DeepEqual(commandTree(t, root), unchanged) {
				t.Fatal("read-only checks mutated the Project")
			}
			invoke("generate")
			if !reflect.DeepEqual(commandTree(t, filepath.Join(root, "generated")), before) {
				t.Fatal("private unvalidated dependency values changed public generated artifacts")
			}
			if got := string(readCommandFile(t, dependency, "plystra.yaml")); got != document("PRIVATE_SECOND") {
				t.Fatal("generation rewrote inert authored values")
			}
			for _, test := range []struct{ name, data, code string }{
				{"unavailable", document("PRIVATE_SECOND"), "PLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID"},
				{"invalid", "interfaces: {use: {email.send/v1: example.com/acme/implementation-use/smtp.New}}\nconfig: {example.com/acme/implementation-use/smtp.New: {endpoint: [PRIVATE_SECOND]}}\n", "PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID"},
			} {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship)
				writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), test.data)
				before := commandTree(t, root)
				for _, args := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args = append(args, selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code == 0 || stdout != "" || !strings.Contains(stderr, test.code) || strings.Contains(stderr, "PRIVATE_") {
						t.Fatalf("template %s, %v = %d, %q, %q", test.name, args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatal("invalid template selection mutated the Project")
					}
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
