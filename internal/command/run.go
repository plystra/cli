// Package command owns the user-facing Plystra command dispatcher.
package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/version"
)

const usage = `Usage:
  plystra help
  plystra version
  plystra new <module-path>
  plystra plugin create <name>
`

// Run executes one Plystra command and returns its process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
	if stdout == nil || stderr == nil {
		return 2
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
		return 1
	}
	return RunIn(arguments, stdout, stderr, workingDirectory, os.Environ())
}

// RunIn executes a command in an explicit environment. It exists so command
// integration tests can isolate filesystem and Go Module state.
func RunIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if stdout == nil || stderr == nil {
		return 2
	}
	if len(arguments) == 0 {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		if len(arguments) != 1 {
			return rejectArguments(stderr, arguments[0])
		}
		_, _ = io.WriteString(stdout, usage)
		return 0
	case "version", "-version", "--version":
		if len(arguments) != 1 {
			return rejectArguments(stderr, arguments[0])
		}
		_, _ = fmt.Fprintf(stdout, "plystra %s\n", version.Current)
		return 0
	case "new":
		if len(arguments) != 2 {
			_, _ = io.WriteString(stderr, "usage: plystra new <module-path>\n")
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := newproject.Create(ctx, newproject.Options{
			Parent:      workingDirectory,
			ModulePath:  arguments[1],
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "create project: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "created %s in %s\n", result.ModulePath(), result.Path())
		return 0
	case "plugin":
		if len(arguments) != 3 || arguments[1] != "create" {
			_, _ = io.WriteString(stderr, "usage: plystra plugin create <name>\n")
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := plugincreate.Create(ctx, plugincreate.Options{
			Start:       workingDirectory,
			Name:        arguments[2],
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "create plugin: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "created plugin %s in %s\n", result.ID(), result.Path())
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", arguments[0], usage)
		return 2
	}
}

func rejectArguments(stderr io.Writer, command string) int {
	_, _ = fmt.Fprintf(stderr, "%s does not accept arguments\n", command)
	return 2
}
