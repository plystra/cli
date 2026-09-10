package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicGenerationCommandsReportMissingActivationRequirementSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeMissingGenerationActivationProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "records"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (declaration)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (exposure)\n\n" +
				"Recovery:\nAdd the missing generation.activations entry to the intended Plugin's plugin.yaml.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationActivationMissing + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "extensions.audit") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the missing-activation Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeMissingGenerationActivationProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [records.get/v1, records.list/v1]
http:
  expose: [records.get/v1, records.list/v1]
`)
	writeCommandFile(t, filepath.Join(root, "records", "plugin.yaml"), "id: acme.library.records\nprovides: [records.get/v1, records.list/v1]\n")
	writeCommandFile(t, filepath.Join(root, "records", "capabilities", "records.get", "v1", "capability.yaml"), `id: records.get/v1
request: {}
response: {}
errors: []
extensions:
  audit: {event: records.read}
`)
	writeCommandFile(t, filepath.Join(root, "records", "capabilities", "records.list", "v1", "capability.yaml"), `id: records.list/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
  audit: {event: records.listed}
`)
	return root
}
