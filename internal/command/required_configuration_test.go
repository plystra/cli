package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicMissingRequiredConstructorConfigurationDoesNotMutate(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []struct {
		name     string
		selected string
		selector []string
	}{
		{name: "default", selected: "plystra.yaml"},
		{name: "environment", selected: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selected: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			writeCommandFile(t, filepath.Join(root, mode.selected), "interfaces: {use: {email.send/v1: "+constructor+"}}\n")
			for _, active := range []bool{false, true} {
				selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
				if active {
					selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
				}
				for _, entry := range []string{"", "{}"} {
					configuration := selection
					if entry != "" {
						configuration += "config: {" + constructor + ": " + entry + "}\n"
					}
					writeCommandFile(t, filepath.Join(root, mode.selected), configuration)
					before := commandTree(t, root)
					for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
						args := append(append([]string(nil), invocation...), mode.selector...)
						code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
						if code == 0 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID") || !strings.Contains(stderr, "required constructor configuration field is missing") || !strings.Contains(stderr, mode.selected+":1:1") {
							t.Fatalf("active=%t, entry=%s, %v = %d, %q, %q", active, entry, args, code, stdout, stderr)
						}
						if !strings.Contains(stderr, "Supply the missing required field in "+mode.selected) || strings.Contains(stderr, "template") {
							t.Fatalf("%v omitted current-project requiredness recovery: %s", args, stderr)
						}
						if !reflect.DeepEqual(commandTree(t, root), before) {
							t.Fatalf("%v mutated incomplete Project", args)
						}
					}
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
