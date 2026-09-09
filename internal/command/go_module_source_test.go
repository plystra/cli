package command_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicCommandsReportInvalidGoModuleDependencySourceWithoutMutation(t *testing.T) {
	t.Parallel()

	failures := []struct {
		name        string
		goMod       string
		wantProblem string
		wantSource  string
	}{
		{
			name:        "self-requirement",
			goMod:       "module example.com/acme/application\n\ngo 1.26\n\nrequire example.com/acme/application v1.0.0\n",
			wantProblem: "application module cannot require itself",
			wantSource:  "Source: example.com/acme/application:go.mod:5:1 (module-dependency)",
		},
		{
			name: "duplicate-requirement",
			goMod: "module example.com/acme/application\n\ngo 1.26\n\nrequire (\n" +
				"example.com/dependency v1.0.0\n" +
				"example.com/dependency v1.1.0\n" +
				")\n",
			wantProblem: `duplicate requirement "example.com/dependency"`,
			wantSource:  "Source: example.com/acme/application:go.mod:7:1 (module-dependency)",
		},
	}
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
		{name: "use", arguments: []string{"use", "kernel.health/v1", "example.com/acme/application/records.New"}},
		{name: "capability-expose", arguments: []string{"capability", "expose", "kernel.health/v1"}},
	}
	for _, failure := range failures {
		failure := failure
		for _, command := range commands {
			command := command
			t.Run(failure.name+"/"+command.name, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				if err := os.MkdirAll(filepath.Join(root, "records"), 0o755); err != nil {
					t.Fatalf("MkdirAll(records): %v", err)
				}
				writeCommandFile(t, filepath.Join(root, "go.mod"), failure.goMod)
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
				before := commandTree(t, root)

				exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "records"), commandGoEnvironment())
				wantSuffix := "\n\n" + failure.wantSource + "\n\n" +
					"Recovery:\nCorrect the reported go.mod entry with standard Go Module syntax, then rerun the command.\n\n" +
					"Diagnostic: " + diagnosticcode.GoModuleInvalid + "\n"
				if exitCode != 1 || stdout != command.wantStdout || !strings.Contains(stderr, failure.wantProblem) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
					t.Fatalf("%s %s = exit %d, stdout %q, stderr %q", failure.name, command.name, exitCode, stdout, stderr)
				}
				if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
					t.Fatalf("%s %s exposed the current Project path: %q", failure.name, command.name, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s %s mutated the invalid Project:\nbefore: %#v\nafter:  %#v", failure.name, command.name, before, after)
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}
