package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunClassifiesConflictingConfigurationSelectorsWithoutMutation(t *testing.T) {
	commands := []struct {
		name      string
		arguments []string
		selectors []string
	}{
		{name: "use", arguments: []string{"use", "email.send/v1", "example.com/acme/email/smtp.New"}, selectors: []string{"--env", "production", "--config", "deploy/customer.yaml"}},
		{name: "capability expose", arguments: []string{"capability", "expose", "email.send/v1"}, selectors: []string{"--config", "deploy/customer.yaml", "--env", "production"}},
		{name: "inspect", arguments: []string{"inspect", "--format", "json"}, selectors: []string{"--env", "production", "--config", "deploy/customer.yaml"}},
		{name: "explain", arguments: []string{"explain", "capability", "email.send/v1", "--format", "json"}, selectors: []string{"--config", "deploy/customer.yaml", "--env", "production"}},
		{name: "check", arguments: []string{"check"}, selectors: []string{"--env", "production", "--config", "deploy/customer.yaml"}},
		{name: "generate", arguments: []string{"generate"}, selectors: []string{"--env", "production", "--config", "deploy/customer.yaml"}},
		{name: "generate check", arguments: []string{"generate", "--check"}, selectors: []string{"--config", "deploy/customer.yaml", "--env", "production"}},
	}
	conflicts := []struct {
		name        string
		explicit    bool
		environment map[string]string
		problem     string
	}{
		{name: "explicit", explicit: true, problem: "--config and --env cannot be used together"},
		{name: "ambient", environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml", "PLYSTRA_ENV": "production"}, problem: "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together"},
	}
	for _, conflict := range conflicts {
		conflict := conflict
		for _, command := range commands {
			command := command
			t.Run(conflict.name+"/"+command.name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				writeCommandFile(t, filepath.Join(root, "keep.txt"), "keep\n")
				if !conflict.explicit {
					writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/selector\n\ngo 1.26\n")
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
				}
				start := filepath.Join(root, "nested")
				writeCommandFile(t, filepath.Join(start, "keep.txt"), "keep\n")
				before := commandTree(t, root)
				arguments := append([]string(nil), command.arguments...)
				if conflict.explicit {
					arguments = append(arguments, command.selectors...)
				}
				exitCode, stdout, stderr := runCommand(t, arguments, start, commandGoEnvironmentWith(conflict.environment))
				if exitCode != 1 || stdout != "" || !commandContainsAll(
					stderr,
					conflict.problem,
					"Recovery:\nSelect exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.\n",
					"Diagnostic: "+diagnosticcode.ConfigurationSelectionInvalid,
				) {
					t.Fatalf("%s selector conflict = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
				}
				if strings.Count(stderr, "Source: ") != 0 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 || strings.Contains(strings.ToLower(stderr), "usage:") {
					t.Fatalf("%s selector conflict emitted unstable diagnostic framing: %q", command.name, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s selector conflict changed the Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicCommandsReportMissingSelectedConfigurationSourcesWithoutMutation(t *testing.T) {
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
		name        string
		path        string
		selectors   []string
		environment map[string]string
	}{
		{name: "explicit-environment", path: "plystra.production.yaml", selectors: []string{"--env", "production"}},
		{name: "ambient-environment", path: "plystra.production.yaml", environment: map[string]string{"PLYSTRA_ENV": "production"}},
		{name: "explicit-configuration", path: "deploy/customer.yaml", selectors: []string{"--config", "deploy/customer.yaml"}},
		{name: "ambient-configuration", path: "deploy/customer.yaml", environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}},
	}
	for _, selection := range selections {
		selection := selection
		for _, command := range commands {
			command := command
			t.Run(selection.name+"/"+command.name, func(t *testing.T) {
				t.Parallel()
				root := writeCapabilityCommandModule(t)
				before := commandTree(t, root)
				arguments := append(append([]string(nil), command.arguments...), selection.selectors...)
				exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "records"), commandGoEnvironmentWith(selection.environment))
				if exitCode != 1 || stdout != command.wantStdout || !commandContainsAll(
					stderr,
					selection.path,
					"Source: example.com/acme/library:"+selection.path+" (configuration-selection)",
					"Recovery:\nSelect exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.\n",
					"Diagnostic: "+diagnosticcode.ConfigurationSelectionInvalid,
				) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
					t.Fatalf("%s %s = exit %d stdout %q stderr %q", selection.name, command.name, exitCode, stdout, stderr)
				}
				if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
					t.Fatalf("%s %s exposed private Project path: %q", selection.name, command.name, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s %s mutated missing selected configuration:\nbefore: %#v\nafter:  %#v", selection.name, command.name, before, after)
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}
