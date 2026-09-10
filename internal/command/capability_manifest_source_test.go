package command_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicCommandsReportInvalidCapabilityManifestSourcesWithoutMutation(t *testing.T) {
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
		{name: "capability-create", arguments: []string{"capability", "create", "records.list", "--query", "--plugin", "records"}},
		{name: "capability-implement", arguments: []string{"capability", "implement", "email.send/v1", "--plugin", "records"}},
		{name: "capability-expose", arguments: []string{"capability", "expose", "kernel.health/v1"}},
	}
	provenances := []struct {
		name       string
		dependency bool
		wantSource string
	}{
		{name: "current Project", wantSource: "Source: example.com/acme/library:records/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)"},
		{name: "dependency Project", dependency: true, wantSource: "Source: example.com/acme/providers:smtp/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)"},
	}
	for _, provenance := range provenances {
		provenance := provenance
		for _, command := range commands {
			command := command
			t.Run(provenance.name+"/"+command.name, func(t *testing.T) {
				t.Parallel()

				root := writeCapabilityManifestDiagnosticProject(t, provenance.dependency)
				before := commandTree(t, root)
				exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "records"), commandGoEnvironment())
				wantSuffix := "\n\n" + provenance.wantSource + "\n\n" +
					"Recovery:\nCorrect the reported authored capability.yaml, then rerun the command.\n\n" +
					"Diagnostic: " + diagnosticcode.CapabilityManifestInvalid + "\n"
				if exitCode != 1 || stdout != command.wantStdout || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
					t.Fatalf("%s %s = exit %d, stdout %q, stderr %q", provenance.name, command.name, exitCode, stdout, stderr)
				}
				if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
					t.Fatalf("%s %s exposed the Project path: %q", provenance.name, command.name, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s %s mutated the invalid Project:\nbefore: %#v\nafter:  %#v", provenance.name, command.name, before, after)
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func writeCapabilityManifestDiagnosticProject(t *testing.T, dependency bool) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	providerRoot := root
	plugin := "records"
	if dependency {
		providerRoot = filepath.Join(root, "providers-dependency")
		plugin = "smtp"
		writeCommandFile(t, filepath.Join(providerRoot, "go.mod"), "module example.com/acme/providers\n\ngo 1.26\n")
		writeCommandFile(t, filepath.Join(providerRoot, "plystra.yaml"), "{}\n")
		goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			t.Fatalf("read application go.mod: %v", err)
		}
		writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/providers v0.0.0\n\nreplace example.com/acme/providers => ./providers-dependency\n")
	}
	writeCommandFile(t, filepath.Join(providerRoot, plugin, "plugin.yaml"), "id: acme.invalid-provider\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(providerRoot, plugin, "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nunknown: true\n")
	return root
}
