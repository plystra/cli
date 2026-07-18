package command_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

const (
	wantUsage    = "Usage:\n  plystra help\n  plystra version\n  plystra new <module-path> [options]\n  plystra plugin create <name>\n  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN\n  plystra generate [--check]\n"
	wantNewUsage = "Usage:\n  plystra new <module-path> [--library] [--plugin <name>] [--git|--no-git] [--github-ci|--no-github-ci] [--skills|--no-skills]\n\nOptions:\n  --library                 Create a non-runnable plugin Go Module.\n  --plugin <name>           Create an initial root-level plugin.\n  --git, --no-git           Initialize or omit a Git repository.\n  --github-ci, --no-github-ci\n                            Include or omit GitHub Actions CI.\n  --skills, --no-skills     Include or omit Plystra agent skills.\n\nInteractive creation asks for each unspecified choice. Non-interactive creation\nmust specify one flag from every choice pair.\n"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		arguments := arguments
		t.Run(commandName(arguments), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", arguments, exitCode)
			}
			if stdout.String() != wantUsage {
				t.Fatalf("Run(%q) stdout = %q, want %q", arguments, stdout.String(), wantUsage)
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", arguments, stderr.String())
			}
		})
	}
}

func TestRunNewHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"new", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantNewUsage || stderr.Len() != 0 {
			t.Fatalf("Run(new %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"version", "-version", "--version"} {
		argument := argument
		t.Run(argument, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run([]string{argument}, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", argument, exitCode)
			}
			if stdout.String() != "plystra 0.1.0\n" {
				t.Fatalf("Run(%q) stdout = %q", argument, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", argument, stderr.String())
			}
		})
	}
}

func TestRunCapabilityHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"capability", "help"}, want: "Usage:\n  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN\n"},
		{arguments: []string{"capability", "create", "--help"}, want: "Usage:\n  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n"},
		{arguments: []string{"capability", "implement", "-h"}, want: "Usage:\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{arguments: []string{"capability", "expose", "help"}, want: "Usage:\n  plystra capability expose <capability-name>/vN\n"},
	}
	for _, test := range tests {
		test := test
		t.Run(strings.Join(test.arguments, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRejectsUnknownCommandAndExtraArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantError string
	}{
		{name: "unknown", arguments: []string{"unknown"}, wantError: "unknown command \"unknown\"\n\n" + wantUsage},
		{name: "help arguments", arguments: []string{"help", "extra"}, wantError: "help does not accept arguments\n"},
		{name: "version arguments", arguments: []string{"version", "extra"}, wantError: "version does not accept arguments\n"},
		{name: "new missing module", arguments: []string{"new"}, wantError: wantNewUsage},
		{name: "new unknown option", arguments: []string{"new", "example.com/app", "--unknown"}, wantError: wantNewUsage},
		{name: "new missing plugin name", arguments: []string{"new", "example.com/app", "--plugin"}, wantError: wantNewUsage},
		{name: "new extra argument", arguments: []string{"new", "example.com/app", "--library", "extra"}, wantError: wantNewUsage},
		{name: "new conflicting choice", arguments: []string{"new", "example.com/app", "--git", "--no-git"}, wantError: wantNewUsage},
		{name: "plugin missing subcommand", arguments: []string{"plugin"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin unknown subcommand", arguments: []string{"plugin", "remove", "account"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin missing name", arguments: []string{"plugin", "create"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin extra argument", arguments: []string{"plugin", "create", "account", "extra"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "capability missing subcommand", arguments: []string{"capability"}, wantError: "usage:\n  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN\n"},
		{name: "capability unknown subcommand", arguments: []string{"capability", "remove", "records.create/v1"}, wantError: "usage:\n  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN\n"},
		{name: "capability create missing reference", arguments: []string{"capability", "create"}, wantError: "usage: plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n"},
		{name: "capability implement missing reference", arguments: []string{"capability", "implement"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability expose missing reference", arguments: []string{"capability", "expose"}, wantError: "usage: plystra capability expose <capability-name>/vN\n"},
		{name: "capability implement confirm", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability implement expose", arguments: []string{"capability", "implement", "records.create/v1", "--expose"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability expose extra option", arguments: []string{"capability", "expose", "records.create/v1", "--confirm"}, wantError: "usage: plystra capability expose <capability-name>/vN\n"},
		{name: "capability create missing plugin", arguments: []string{"capability", "create", "records.create", "--plugin"}, wantError: "usage: plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]\n"},
		{name: "generate unknown option", arguments: []string{"generate", "--write"}, wantError: "usage: plystra generate [--check]\n"},
		{name: "generate duplicate check", arguments: []string{"generate", "--check", "--check"}, wantError: "usage: plystra generate [--check]\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 2 {
				t.Fatalf("Run(%q) exit code = %d, want 2", test.arguments, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%q) stdout = %q, want empty", test.arguments, stdout.String())
			}
			if stderr.String() != test.wantError {
				t.Fatalf("Run(%q) stderr = %q, want %q", test.arguments, stderr.String(), test.wantError)
			}
		})
	}
}

func TestRunRejectsMissingWriters(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if exitCode := command.Run(nil, nil, &output); exitCode != 2 {
		t.Fatalf("Run with nil stdout exit code = %d, want 2", exitCode)
	}
	if exitCode := command.Run(nil, &output, nil); exitCode != 2 {
		t.Fatalf("Run with nil stderr exit code = %d, want 2", exitCode)
	}
}

func commandName(arguments []string) string {
	if len(arguments) == 0 {
		return "empty"
	}
	return arguments[0]
}
