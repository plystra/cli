package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
)

func TestPublicConstructorEntryTombstones(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			inventory, _ := writeCommandTemplate(t, root, "constructor", "config: {"+constructor+": {endpoint: inherited.internal}}\n")
			selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
			removal := "config: {" + constructor + ": {$remove: true}}\n"
			rootData := inventory + selection + removal
			selectedPath, selectedData := "plystra.yaml", rootData
			var selector []string
			switch mode {
			case "environment":
				rootData = inventory + selection + "config: {" + constructor + ": {endpoint: root.internal}}\n"
				selectedPath, selectedData = "plystra.production.yaml", removal
				selector = []string{"--env", "production"}
			case "replacement":
				rootData = inventory + selection
				selectedPath, selectedData = "deploy/customer.yaml", selection+removal
				selector = []string{"--config", selectedPath}
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			writeCommandFile(t, filepath.Join(root, selectedPath), selectedData)
			if code, stdout, stderr := runCommand(t, append([]string{"generate"}, selector...), root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bindings := manifest.InterfaceProvenance().Bindings(); len(bindings) != 0 {
				t.Fatalf("dormant removal activated bindings: %#v", bindings)
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}, {"explain", "config", `config["` + constructor + `"]`, "--format", "json"}} {
				args := append(append([]string(nil), invocation...), selector...)
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				if invocation[0] == "explain" {
					document := decodeExplainCommandEnvelope(t, stdout)
					if document.Result.Decision.Outcome != "removed" || document.Result.Change.Path != selectedPath {
						t.Fatalf("removal explanation = %#v", document.Result)
					}
				}
				if !reflect.DeepEqual(commandTree(t, root), before) {
					t.Fatalf("%v mutated the Project", args)
				}
			}
			if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, selectedPath)) != selectedData {
				t.Fatal("generation changed authored configuration")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicInvalidConstructorEntryRemovalDoesNotMutate(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, value := range []string{"null", "{$remove: false}", "{$remove: true, private_key: private_value}"} {
			t.Run(mode+"/"+value, func(t *testing.T) {
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
				writeCommandFile(t, filepath.Join(root, selected), "config: {"+constructor+": "+value+"}\n")
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code == 0 || stdout != "" || !strings.Contains(stderr, "must be a mapping or {$remove: true}") || strings.Contains(stderr, "private_key") || strings.Contains(stderr, "private_value") {
						t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v mutated rejected Project", args)
					}
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}
