package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
)

func TestPublicConstructorConfigurationAcrossSelections(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, test := range []struct {
		name         string
		rootData     string
		selected     string
		selectedData string
		selector     []string
	}{
		{
			name:         "default",
			rootData:     "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {endpoint: root.internal}}\n",
			selected:     "plystra.yaml",
			selectedData: "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {endpoint: root.internal}}\n",
		},
		{
			name:         "environment overlay",
			rootData:     "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {endpoint: root.internal}}\n",
			selected:     "plystra.production.yaml",
			selectedData: "config: {" + constructor + ": {endpoint: overlay.internal}}\n",
			selector:     []string{"--env", "production"},
		},
		{
			name:         "complete replacement",
			rootData:     "{}\n",
			selected:     "deploy/customer.yaml",
			selectedData: "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {endpoint: replacement.internal}}\n",
			selector:     []string{"--config", "deploy/customer.yaml"},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), test.rootData)
			writeCommandFile(t, filepath.Join(root, test.selected), test.selectedData)
			args := append([]string{"generate"}, test.selector...)
			if code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bindings := manifest.InterfaceProvenance().Bindings(); len(bindings) != 0 {
				t.Fatalf("dormant configuration activated a binding: %#v", bindings)
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
				args := append(append([]string(nil), invocation...), test.selector...)
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, ".internal") {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) {
					t.Fatalf("%v mutated the Project", args)
				}
			}
			if string(readCommandFile(t, root, "plystra.yaml")) != test.rootData || string(readCommandFile(t, root, test.selected)) != test.selectedData {
				t.Fatal("generation changed authored configuration")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicConstructorEntryTombstoneInEnvironmentOverlay(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	rootData := "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {endpoint: root.internal}}\n"
	selectedData := "config: {" + constructor + ": {$remove: true}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), selectedData)
	if code, stdout, stderr := runCommand(t, []string{"generate", "--env", "production"}, root, commandGoEnvironment()); code != 0 {
		t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
	}
	manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bindings := manifest.InterfaceProvenance().Bindings(); len(bindings) != 0 {
		t.Fatalf("overlay tombstone activated a binding: %#v", bindings)
	}
	before := commandTree(t, root)
	for _, invocation := range [][]string{{"generate", "--check", "--env", "production"}, {"check", "--env", "production"}, {"inspect", "configuration", "--env", "production", "--format", "json"}} {
		code, stdout, stderr := runCommand(t, invocation, root, commandGoEnvironment())
		if code != 0 || strings.Contains(stdout+stderr, ".internal") {
			t.Fatalf("%v = %d, %q, %q", invocation, code, stdout, stderr)
		}
		if !reflect.DeepEqual(commandTree(t, root), before) {
			t.Fatalf("%v mutated the Project", invocation)
		}
	}
	assertNoCommandTransactions(t, root)
}

func TestPublicInvalidConstructorEntryRemovalDoesNotMutate(t *testing.T) {
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
		for _, value := range []string{"null", "{$remove: false}", "{$remove: true, private_key: private_value}"} {
			t.Run(mode.name+"/"+value, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
				writeCommandFile(t, filepath.Join(root, mode.selected), "config: {"+constructor+": "+value+"}\n")
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), mode.selector...)
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
