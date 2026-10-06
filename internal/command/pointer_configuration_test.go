package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorsymbol"
	"go.yaml.in/yaml/v3"
)

func TestPublicPointerConfigurationAcrossSelections(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, test := range []struct {
		name   string
		active bool
	}{
		{name: "default dormant"},
		{name: "default active", active: true},
		{name: "environment dormant"},
		{name: "environment active", active: true},
		{name: "replacement dormant"},
		{name: "replacement active", active: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandPointerConfigurationImplementation(t, root)
			selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
			if test.active {
				selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
			}
			rootData := selection + "config: {" + constructor + ": {settings: {first: root.internal, second: root-only.internal}}}\n"
			selectedPath, selectedData := "plystra.yaml", selection+"config: {"+constructor+": {settings: {first: current.internal}}}\n"
			var selector []string
			options := applicationresolve.Options{Start: root, Environment: commandGoEnvironment()}
			switch {
			case strings.HasPrefix(test.name, "environment"):
				selectedPath, selector = "plystra.production.yaml", []string{"--env", "production"}
				selectedData = "config: {" + constructor + ": {settings: {first: current.internal}}}\n"
				options.EnvironmentName = "production"
			case strings.HasPrefix(test.name, "replacement"):
				selectedPath, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				selectedData = selection + "config: {" + constructor + ": {settings: {first: current.internal}}}\n"
				options.ConfigurationPath = selectedPath
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			writeCommandFile(t, filepath.Join(root, selectedPath), selectedData)

			resolved, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			symbol, err := constructorsymbol.Parse(constructor)
			if err != nil {
				t.Fatal(err)
			}
			configured, exists := resolved.Manifest().Configuration(symbol)
			if !exists {
				t.Fatal("resolved pointer configuration is absent")
			}
			var values struct{ Settings map[string]string }
			if err := yaml.Unmarshal(configured.YAML(), &values); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(values.Settings, map[string]string{"first": "current.internal"}) {
				t.Fatalf("pointer configuration = %#v", values.Settings)
			}
			if code, stdout, stderr := runCommand(t, append([]string{"generate"}, selector...), root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			configurations := manifest.DormantConstructorConfigurations()
			if test.active {
				if len(configurations) != 0 || len(manifest.InterfaceProvenance().Bindings()) != 1 {
					t.Fatal("active pointer configuration has inconsistent runtime membership")
				}
			} else if len(configurations) != 1 || len(manifest.InterfaceProvenance().Bindings()) != 0 {
				t.Fatal("pointer configuration did not remain dormant")
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
				args := append(append([]string(nil), invocation...), selector...)
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, ".internal") {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
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
