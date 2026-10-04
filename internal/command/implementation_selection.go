package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/implementationselect"
)

const (
	useSynopsis = "plystra use <target> <constructor-symbol> [--env <environment>|--config <yaml-path>]"
	useUsage    = `Usage:
  ` + useSynopsis + `

Options:
  --env <environment>    Write the choice to plystra.<environment>.yaml.
  --config <yaml-path>   Write the choice to one complete replacement configuration.

The target is a canonical Interface ID with /vN or an existing named Resource
instance. No kind flag is needed. Resource selection replaces a compatible
provider; it never creates an instance. Unknown targets and incompatible
providers fail without mutation.

Malformed targets or constructor symbols report PLYSTRA_USE_TARGET_INVALID or
PLYSTRA_USE_CONSTRUCTOR_INVALID. Missing targets report PLYSTRA_USE_TARGET_NOT_FOUND;
incompatible Resource providers report PLYSTRA_USE_PROVIDER_INCOMPATIBLE.

An exact compatible choice may be recorded before its Interface is required. It
remains dormant without creating a root, binding, constructor, or generated
Interface runtime until that Interface becomes reachable; invalid choices are
rejected immediately.

Changing a Resource provider discards that instance's old Config, not another
instance's values. Selection removes only provably obsolete configuration and
binding parameters, preserving still-owned configuration. Surviving dependencies
must remain valid; the command never guesses a replacement binding or missing
required value. It regenerates and validates the selected final state, with
rollback on failure. Compound change plans and --dry-run are not installed.

An effective choice for an intrinsic kernel.* Interface emits
PLYSTRA_RESOLVE_INTRINSIC_INTERFACE_SELECTION with every contributing
implementation-selection Source. Set that interfaces.use entry to {$remove: true} in the
selected current-Project document to remove either a local or template choice.

PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit
selector is present; setting both is an error. Explicit --env or --config
overrides both variables, and the two flags cannot be combined. Relative
configuration paths are resolved from the detected Plystra Project root.
Invalid or conflicting selections emit the stable
PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
A normalized Project-contained selected document that cannot be loaded reports
one span-less configuration-selection source; conflicting or unsafe selectors
report none.
`
)

type useArguments struct {
	target      string
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
		Target:            parsed.target,
		Constructor:       parsed.constructor,
		ConfigurationPath: parsed.config,
		EnvironmentName:   parsed.environment,
		Environment:       environment,
	})
	if err != nil {
		writeCommandFailure(stderr, "", err, commandRecoveryContext(parsed.config, parsed.environment, environment))
		return 1
	}
	kind := "Implementation"
	if result.Kind() == "resource" {
		kind = "Resource provider"
	}
	if result.Changed() {
		_, _ = fmt.Fprintf(stdout, "selected %s %s for %s in %s\n", kind, result.Constructor(), result.Target(), result.ManifestPath())
	} else {
		_, _ = fmt.Fprintf(stdout, "%s %s is already selected for %s in %s\n", kind, result.Constructor(), result.Target(), result.ManifestPath())
	}
	return 0
}

func parseUseArguments(arguments []string) (useArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "use" || arguments[1] == "" || arguments[2] == "" || strings.HasPrefix(arguments[1], "--") || strings.HasPrefix(arguments[2], "--") {
		return useArguments{}, false
	}
	result := useArguments{target: arguments[1], constructor: arguments[2]}
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
