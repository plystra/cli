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
	"github.com/plystra/cli/internal/dependencyadd"
	"github.com/plystra/cli/internal/dependencyremove"
	"github.com/plystra/cli/internal/dependencyupdate"
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
  plystra new <project-name> [options]
  plystra add <go-module-query>
  plystra remove <go-module-path>
  plystra update <go-module-query>
  plystra use <capability-name>/vN <plugin-id> [--env <environment>|--config <yaml-path>]
  plystra plugin create <name>
  plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]
  plystra capability implement <capability-name>/vN [--plugin <plugin>]
  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]
  plystra generate [--check] [--env <environment>|--config <yaml-path>]
`
	addUsage = `Usage:
  plystra add <go-module-query>

Adds one ordinary Go Module dependency, recomposes root plystra.yaml, regenerates,
tidies, and validates the complete Project in one rollback boundary.
`
	removeUsage = `Usage:
  plystra remove <go-module-path>

Removes one ordinary Go Module dependency, recomposes root plystra.yaml,
regenerates, tidies, and validates the complete Project in one rollback boundary.
`
	updateUsage = `Usage:
  plystra update <go-module-query>

Updates one selected ordinary Go Module dependency, recomposes root plystra.yaml,
regenerates, tidies, and validates the complete Project in one rollback boundary.
`
	newUsage = `Usage:
  plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [--plugin <name>] [--git|--no-git] [--github-ci|--no-github-ci] [--skills|--no-skills]

Options:
  --module <go-module-path> Set the Go Module path; defaults to the project name.
  --template <module-query> Create from one public Plystra Project dependency.
  --plugin <name>           Create an initial root-level plugin.
  --git, --no-git           Initialize or omit a Git repository.
  --github-ci, --no-github-ci
                            Include or omit GitHub Actions CI.
  --skills, --no-skills     Include or omit Plystra agent skills.

Interactive creation asks for each unspecified choice. Non-interactive creation
must specify one flag from every choice pair.
`
	generateUsage = `Usage:
  plystra generate [--check] [--env <environment>|--config <yaml-path>]

Options:
  --check                Report drift without modifying configuration or generated files.
  --env <environment>    Overlay root plystra.yaml with plystra.<environment>.yaml.
  --config <yaml-path>   Use one complete current-project configuration instead of root plystra.yaml.

PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit
selector is present; setting both is an error. Explicit --env or --config
overrides both variables, and the two flags cannot be combined. Relative
configuration paths are resolved from the detected Plystra Project root. Root
plystra.yaml remains mandatory and is not merged beneath --config.
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
			ProjectName: options.projectName,
			ModulePath:  options.modulePath,
			Template:    options.template,
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
		if options.template != "" {
			_, _ = fmt.Fprintf(stdout, "created %s from %s in %s\n", result.ModulePath(), options.template, result.Path())
		} else {
			_, _ = fmt.Fprintf(stdout, "created %s in %s\n", result.ModulePath(), result.Path())
		}
		return 0
	case "add":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, addUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, addUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyadd.Add(ctx, dependencyadd.Options{
			Start:       workingDirectory,
			Query:       arguments[1],
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "added dependency %s to %s in %s\n", result.Query(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "remove":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, removeUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, removeUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyremove.Remove(ctx, dependencyremove.Options{
			Start:       workingDirectory,
			ModulePath:  arguments[1],
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "removed dependency %s from %s in %s\n", result.ModulePath(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "update":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, updateUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, updateUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyupdate.Update(ctx, dependencyupdate.Options{
			Start:       workingDirectory,
			Query:       arguments[1],
			Environment: environment,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "updated dependency %s in %s at %s\n", result.Query(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "use":
		return runUse(arguments, stdout, stderr, workingDirectory, environment)
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
		if len(arguments) == 2 && (arguments[1] == "help" || arguments[1] == "-h" || arguments[1] == "--help") {
			_, _ = io.WriteString(stdout, generateUsage)
			return 0
		}
		generate, ok := parseGenerateArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, generateUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:             workingDirectory,
			Check:             generate.check,
			ConfigurationPath: generate.configurationPath,
			EnvironmentName:   generate.environmentName,
			Environment:       environment,
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
			writeGenerationReport(stderr, heading, configurationDrift, result.ConfigurationMaintenancePath(), result.Report())
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

type generateArguments struct {
	check             bool
	configurationPath string
	environmentName   string
}

func parseGenerateArguments(arguments []string) (generateArguments, bool) {
	if len(arguments) == 0 || arguments[0] != "generate" {
		return generateArguments{}, false
	}
	var result generateArguments
	configurationSet := false
	environmentSet := false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--check":
			if result.check {
				return generateArguments{}, false
			}
			result.check = true
		case "--config":
			if configurationSet || environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return generateArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return generateArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return generateArguments{}, false
		}
	}
	return result, true
}

func writeGenerationReport(writer io.Writer, heading string, configurationDrift bool, configurationPath string, report generatedfiles.Report) {
	_, _ = fmt.Fprintf(writer, "%s:\n", heading)
	if configurationDrift {
		_, _ = fmt.Fprintf(writer, "  changed %s (dependency composition)\n", configurationPath)
	}
	for _, change := range report.Changes() {
		_, _ = fmt.Fprintf(writer, "  %s %s\n", change.Kind(), change.Path())
	}
}

type newArguments struct {
	projectName string
	modulePath  string
	template    string
	plugin      string
	git         booleanChoice
	githubCI    booleanChoice
	skills      booleanChoice
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
	result := newArguments{projectName: arguments[1]}
	moduleSet := false
	templateSet := false
	pluginSet := false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--module":
			if moduleSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			moduleSet = true
			index++
			result.modulePath = arguments[index]
		case "--template":
			if templateSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			templateSet = true
			index++
			result.template = arguments[index]
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
