package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicCommandsReportMalformedSelectedConfigurationSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name       string
		arguments  []string
		wantStdout string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
		{name: "inspect", arguments: []string{"inspect"}, wantStdout: inspectProgress},
		{name: "explain", arguments: []string{"explain", "capability", "kernel.health/v1"}, wantStdout: inspectProgress},
		{name: "use", arguments: []string{"use", "kernel.health/v1", "example.com/acme/library/records.New"}},
		{name: "capability-expose", arguments: []string{"capability", "expose", "kernel.health/v1"}},
	}
	selections := []struct {
		name      string
		path      string
		selectors []string
	}{
		{name: "environment", path: "plystra.production.yaml", selectors: []string{"--env", "production"}},
		{name: "explicit", path: "deploy/customer.yaml", selectors: []string{"--config", "deploy/customer.yaml"}},
	}
	for _, selection := range selections {
		selection := selection
		for _, command := range commands {
			command := command
			t.Run(selection.name+"/"+command.name, func(t *testing.T) {
				t.Parallel()
				root := writeCapabilityCommandModule(t)
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(selection.path)), "unknown: true\n")
				before := commandTree(t, root)
				arguments := append(append([]string(nil), command.arguments...), selection.selectors...)
				exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "records"), commandGoEnvironment())
				if exitCode != 1 || stdout != command.wantStdout || !commandContainsAll(
					stderr,
					`unknown key "unknown"`,
					"Source: example.com/acme/library:"+selection.path+":1:1 (configuration-declaration)",
					"Recovery:\nEdit "+selection.path+" so every value matches a selected Plugin's closed typed schema, then rerun the command.\n",
					"Diagnostic: "+diagnosticcode.ConfigurationInvalid,
				) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
					t.Fatalf("%s %s = exit %d stdout %q stderr %q", selection.name, command.name, exitCode, stdout, stderr)
				}
				if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
					t.Fatalf("%s %s exposed private Project path: %q", selection.name, command.name, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s %s mutated malformed selected configuration:\nbefore: %#v\nafter:  %#v", selection.name, command.name, before, after)
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}
