package command_test

import (
	"bytes"
	"testing"

	"github.com/plystra/cli/internal/command"
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
			const want = "Usage:\n  plystra help\n  plystra version\n"
			if stdout.String() != want {
				t.Fatalf("Run(%q) stdout = %q, want %q", arguments, stdout.String(), want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", arguments, stderr.String())
			}
		})
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

func TestRunRejectsUnknownCommandAndExtraArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantError string
	}{
		{name: "unknown", arguments: []string{"unknown"}, wantError: "unknown command \"unknown\"\n\nUsage:\n  plystra help\n  plystra version\n"},
		{name: "help arguments", arguments: []string{"help", "extra"}, wantError: "help does not accept arguments\n"},
		{name: "version arguments", arguments: []string{"version", "extra"}, wantError: "version does not accept arguments\n"},
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
