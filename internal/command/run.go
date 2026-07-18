// Package command owns the user-facing Plystra command dispatcher.
package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/version"
)

const (
	generationCommandTimeout = 15 * time.Minute
	usage                    = `Usage:
  plystra help
  plystra version
  plystra new <module-path> [--library] [--plugin <name>]
  plystra plugin create <name>
  plystra capability create <capability-name> [--plugin <plugin>] [--confirm]
  plystra capability implement <capability-name>/vN [--plugin <plugin>]
  plystra generate [--check]
`
)

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
	return runIn(arguments, stdout, stderr, workingDirectory, os.Environ(), terminalPluginSelector(os.Stdin, stderr))
}

// RunIn executes a command in an explicit environment. It exists so command
// integration tests can isolate filesystem and Go Module state. It remains
// non-interactive so automation never consumes an implicit input stream.
func RunIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	return runIn(arguments, stdout, stderr, workingDirectory, environment, nil)
}

func runIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string, selectPlugin plugintarget.Selector) int {
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
		options, ok := parseNewArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, "usage: plystra new <module-path> [--library] [--plugin <name>]\n")
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := newproject.Create(ctx, newproject.Options{
			Parent:      workingDirectory,
			ModulePath:  options.modulePath,
			Library:     options.library,
			Plugin:      options.plugin,
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
	case "capability":
		return runCapability(arguments, stdout, stderr, workingDirectory, environment, selectPlugin)
	case "generate":
		check, ok := parseGenerateArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, "usage: plystra generate [--check]\n")
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:       workingDirectory,
			Check:       check,
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		if !result.Report().Clean() {
			heading := "generated output remains inconsistent after installation"
			if result.Checked() {
				heading = "generated output is not current"
			}
			writeGenerationReport(stderr, heading, result.Report())
			return 1
		}
		if result.Checked() {
			_, _ = fmt.Fprintf(stdout, "generated output is current for %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		} else {
			_, _ = fmt.Fprintf(stdout, "generated %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", arguments[0], usage)
		return 2
	}
}

func terminalPluginSelector(input *os.File, output io.Writer) plugintarget.Selector {
	outputFile, ok := output.(*os.File)
	if !ok || !terminalFile(input) || !terminalFile(outputFile) {
		return nil
	}
	return plugintarget.Prompt(input, output)
}

func terminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func parseGenerateArguments(arguments []string) (bool, bool) {
	switch {
	case len(arguments) == 1:
		return false, true
	case len(arguments) == 2 && arguments[1] == "--check":
		return true, true
	default:
		return false, false
	}
}

func writeGenerationReport(writer io.Writer, heading string, report generatedfiles.Report) {
	_, _ = fmt.Fprintf(writer, "%s:\n", heading)
	for _, change := range report.Changes() {
		_, _ = fmt.Fprintf(writer, "  %s %s\n", change.Kind(), change.Path())
	}
}

type newArguments struct {
	modulePath string
	library    bool
	plugin     string
}

func parseNewArguments(arguments []string) (newArguments, bool) {
	if len(arguments) < 2 || arguments[1] == "" || strings.HasPrefix(arguments[1], "--") {
		return newArguments{}, false
	}
	result := newArguments{modulePath: arguments[1]}
	pluginSet := false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--library":
			if result.library {
				return newArguments{}, false
			}
			result.library = true
		case "--plugin":
			if pluginSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			pluginSet = true
			index++
			result.plugin = arguments[index]
		default:
			return newArguments{}, false
		}
	}
	return result, true
}

func rejectArguments(stderr io.Writer, command string) int {
	_, _ = fmt.Fprintf(stderr, "%s does not accept arguments\n", command)
	return 2
}
