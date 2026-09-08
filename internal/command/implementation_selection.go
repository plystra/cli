package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/implementationselect"
)

const (
	useSynopsis = "plystra use <interface-id> <constructor-symbol> [--env <environment>|--config <yaml-path>]"
	useUsage    = `Usage:
  ` + useSynopsis + `

Options:
  --env <environment>    Write the Implementation choice to plystra.<environment>.yaml.
  --config <yaml-path>   Write the Implementation choice to one complete replacement configuration.

An exact compatible choice may be recorded before its Interface is required. It
remains dormant without creating a root, binding, constructor, or generated
Interface runtime until that Interface becomes reachable; invalid choices are
rejected immediately.

An effective choice for an intrinsic kernel.* Interface emits
PLYSTRA_RESOLVE_INTRINSIC_INTERFACE_SELECTION with every contributing
implementation-selection Source. Set that interfaces.use entry to null in the
selected current-Project document to remove either a local or inherited choice.

PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit
selector is present; setting both is an error. Explicit --env or --config
overrides both variables, and the two flags cannot be combined. Relative
configuration paths are resolved from the detected Plystra Project root.
Invalid or conflicting selections emit the stable
PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
`
)

type useArguments struct {
	interfaceID string
	constructor string
	config      string
	environment string
}

func runUse(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if len(arguments) == 2 && arguments[0] == "use" && isHelp(arguments[1]) {
		_, _ = io.WriteString(stdout, useUsage)
		return 0
	}
	parsed, ok := parseUseArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, useUsage)
		return 2
	}
	if rejectConflictingConfigurationSelectors(stderr, parsed.config, parsed.environment) {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	result, err := implementationselect.Select(ctx, implementationselect.Options{
		Start:             workingDirectory,
		InterfaceID:       parsed.interfaceID,
		Constructor:       parsed.constructor,
		ConfigurationPath: parsed.config,
		EnvironmentName:   parsed.environment,
		Environment:       environment,
	})
	if err != nil {
		writeCommandFailure(stderr, "", err, commandRecoveryContext(parsed.config, parsed.environment, environment))
		return 1
	}
	if result.Changed() {
		_, _ = fmt.Fprintf(stdout, "selected Implementation %s for %s in %s\n", result.Constructor(), result.InterfaceID(), result.ManifestPath())
	} else {
		_, _ = fmt.Fprintf(stdout, "Implementation %s is already selected for %s in %s\n", result.Constructor(), result.InterfaceID(), result.ManifestPath())
	}
	return 0
}

func parseUseArguments(arguments []string) (useArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "use" || arguments[1] == "" || arguments[2] == "" || strings.HasPrefix(arguments[1], "--") || strings.HasPrefix(arguments[2], "--") {
		return useArguments{}, false
	}
	result := useArguments{interfaceID: arguments[1], constructor: arguments[2]}
	configurationSet := false
	environmentSet := false
	for index := 3; index < len(arguments); index++ {
		switch arguments[index] {
		case "--config":
			if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return useArguments{}, false
			}
			configurationSet = true
			index++
			result.config = arguments[index]
		case "--env":
			if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return useArguments{}, false
			}
			environmentSet = true
			index++
			result.environment = arguments[index]
		default:
			return useArguments{}, false
		}
	}
	return result, true
}
