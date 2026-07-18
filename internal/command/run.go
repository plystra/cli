// Package command owns the user-facing Plystra command dispatcher.
package command

import (
	"bufio"
	"context"
	"errors"
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
  plystra new <module-path> [options]
  plystra plugin create <name>
  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]
  plystra capability implement <capability-name>/vN [--plugin <plugin>]
  plystra capability expose <capability-name>/vN
  plystra generate [--check]
`
	newUsage = `Usage:
  plystra new <module-path> [--plugin <name>] [--git|--no-git] [--github-ci|--no-github-ci] [--skills|--no-skills]

Options:
  --plugin <name>           Create an initial root-level plugin.
  --git, --no-git           Initialize or omit a Git repository.
  --github-ci, --no-github-ci
                            Include or omit GitHub Actions CI.
  --skills, --no-skills     Include or omit Plystra agent skills.

Interactive creation asks for each unspecified choice. Non-interactive creation
must specify one flag from every choice pair.
`
)

var (
	errNewChoiceRequired = errors.New("new project choice is required")
	errNewChoicePrompt   = errors.New("prompt for new project choice")
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
	return runIn(arguments, stdout, stderr, workingDirectory, os.Environ(), terminalPluginSelector(os.Stdin, stderr), terminalNewProjectPrompter(os.Stdin, stderr))
}

// RunIn executes a command in an explicit environment. It exists so command
// integration tests can isolate filesystem and Go Module state. It remains
// non-interactive so automation never consumes an implicit input stream.
func RunIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	return runIn(arguments, stdout, stderr, workingDirectory, environment, nil, nil)
}

func runIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string, selectPlugin plugintarget.Selector, promptNew newProjectPrompter) int {
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
		if len(arguments) == 2 && (arguments[1] == "help" || arguments[1] == "-h" || arguments[1] == "--help") {
			_, _ = io.WriteString(stdout, newUsage)
			return 0
		}
		options, ok := parseNewArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, newUsage)
			return 2
		}
		choices, err := resolveNewChoices(options, promptNew)
		if err != nil {
			if errors.Is(err, errNewChoiceRequired) {
				_, _ = fmt.Fprintf(stderr, "%v\n\n%s", err, newUsage)
				return 2
			}
			_, _ = fmt.Fprintf(stderr, "choose new project options: %v\n", err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := newproject.Create(ctx, newproject.Options{
			Parent:      workingDirectory,
			ModulePath:  options.modulePath,
			Plugin:      options.plugin,
			Git:         choices.git,
			GitHubCI:    choices.githubCI,
			Skills:      choices.skills,
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
		configurationDrift := result.Checked() && result.ConfigurationChanged()
		if configurationDrift || !result.Report().Clean() {
			heading := "generated output remains inconsistent after installation"
			if result.Checked() {
				heading = "generated output is not current"
				if configurationDrift {
					heading = "Project configuration or generated output is not current"
				}
			}
			writeGenerationReport(stderr, heading, configurationDrift, result.Report())
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

func terminalNewProjectPrompter(input *os.File, output io.Writer) newProjectPrompter {
	outputFile, ok := output.(*os.File)
	if !ok || !terminalFile(input) || !terminalFile(outputFile) {
		return nil
	}
	return promptNewProject(input, output)
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

func writeGenerationReport(writer io.Writer, heading string, configurationDrift bool, report generatedfiles.Report) {
	_, _ = fmt.Fprintf(writer, "%s:\n", heading)
	if configurationDrift {
		_, _ = io.WriteString(writer, "  changed plystra.yaml (dependency composition)\n")
	}
	for _, change := range report.Changes() {
		_, _ = fmt.Fprintf(writer, "  %s %s\n", change.Kind(), change.Path())
	}
}

type newArguments struct {
	modulePath string
	plugin     string
	git        booleanChoice
	githubCI   booleanChoice
	skills     booleanChoice
}

type booleanChoice uint8

const (
	choiceUnspecified booleanChoice = iota
	choiceYes
	choiceNo
)

type resolvedNewChoices struct {
	git      bool
	githubCI bool
	skills   bool
}

type newProjectPrompter func(question string, defaultValue bool) (bool, error)

func parseNewArguments(arguments []string) (newArguments, bool) {
	if len(arguments) < 2 || arguments[1] == "" || strings.HasPrefix(arguments[1], "--") {
		return newArguments{}, false
	}
	result := newArguments{modulePath: arguments[1]}
	pluginSet := false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--plugin":
			if pluginSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			pluginSet = true
			index++
			result.plugin = arguments[index]
		case "--git":
			if result.git != choiceUnspecified {
				return newArguments{}, false
			}
			result.git = choiceYes
		case "--no-git":
			if result.git != choiceUnspecified {
				return newArguments{}, false
			}
			result.git = choiceNo
		case "--github-ci":
			if result.githubCI != choiceUnspecified {
				return newArguments{}, false
			}
			result.githubCI = choiceYes
		case "--no-github-ci":
			if result.githubCI != choiceUnspecified {
				return newArguments{}, false
			}
			result.githubCI = choiceNo
		case "--skills":
			if result.skills != choiceUnspecified {
				return newArguments{}, false
			}
			result.skills = choiceYes
		case "--no-skills":
			if result.skills != choiceUnspecified {
				return newArguments{}, false
			}
			result.skills = choiceNo
		default:
			return newArguments{}, false
		}
	}
	return result, true
}

func resolveNewChoices(arguments newArguments, prompt newProjectPrompter) (resolvedNewChoices, error) {
	choices := []struct {
		value    booleanChoice
		question string
		flags    string
		set      func(bool)
	}{
		{value: arguments.git, question: "Initialize a Git repository?", flags: "--git or --no-git"},
		{value: arguments.githubCI, question: "Include GitHub Actions CI?", flags: "--github-ci or --no-github-ci"},
		{value: arguments.skills, question: "Include Plystra development skills?", flags: "--skills or --no-skills"},
	}
	var result resolvedNewChoices
	choices[0].set = func(value bool) { result.git = value }
	choices[1].set = func(value bool) { result.githubCI = value }
	choices[2].set = func(value bool) { result.skills = value }
	missing := make([]string, 0, len(choices))
	for _, choice := range choices {
		switch choice.value {
		case choiceYes:
			choice.set(true)
		case choiceNo:
			choice.set(false)
		case choiceUnspecified:
			if prompt == nil {
				missing = append(missing, choice.flags)
				continue
			}
			value, err := prompt(choice.question, true)
			if err != nil {
				return resolvedNewChoices{}, fmt.Errorf("%w: %s: %v", errNewChoicePrompt, choice.question, err)
			}
			choice.set(value)
		default:
			return resolvedNewChoices{}, fmt.Errorf("%w: invalid parsed choice", errNewChoicePrompt)
		}
	}
	if len(missing) != 0 {
		return resolvedNewChoices{}, fmt.Errorf("%w in non-interactive mode; specify %s", errNewChoiceRequired, strings.Join(missing, ", "))
	}
	return result, nil
}

func promptNewProject(input io.Reader, output io.Writer) newProjectPrompter {
	scanner := bufio.NewScanner(input)
	return func(question string, defaultValue bool) (bool, error) {
		if input == nil || output == nil {
			return false, errors.New("input and output are required")
		}
		suffix := " [y/N]: "
		if defaultValue {
			suffix = " [Y/n]: "
		}
		for {
			if _, err := io.WriteString(output, question+suffix); err != nil {
				return false, fmt.Errorf("write prompt: %v", err)
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return false, fmt.Errorf("read choice: %v", err)
				}
				return false, errors.New("input ended before a choice")
			}
			switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
			case "":
				return defaultValue, nil
			case "y", "yes":
				return true, nil
			case "n", "no":
				return false, nil
			default:
				if _, err := io.WriteString(output, "Please enter yes or no.\n"); err != nil {
					return false, fmt.Errorf("write retry guidance: %v", err)
				}
			}
		}
	}
}

func rejectArguments(stderr io.Writer, command string) int {
	_, _ = fmt.Fprintf(stderr, "%s does not accept arguments\n", command)
	return 2
}
