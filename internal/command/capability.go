package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/plugintarget"
)

const (
	capabilityCreateSynopsis    = "plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]"
	capabilityImplementSynopsis = "plystra capability implement <capability-name>/vN [--plugin <plugin>]"
	capabilityExposeSynopsis    = "plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]"
	capabilityUsage             = `Usage:
  ` + capabilityCreateSynopsis + `
  ` + capabilityImplementSynopsis + `
  ` + capabilityExposeSynopsis + `
`
	capabilityCreateUsage    = "usage: " + capabilityCreateSynopsis + "\n"
	capabilityImplementUsage = "usage: " + capabilityImplementSynopsis + "\n"
	capabilityExposeUsage    = "usage: " + capabilityExposeSynopsis + "\n"
	capabilityExposeHelp     = `Usage:
  ` + capabilityExposeSynopsis + `

Options:
  --env <environment>    Write exposure to plystra.<environment>.yaml.
  --config <yaml-path>   Write exposure to one complete replacement configuration.

The current Connect unary boundary accepts a canonical Capability whose
explicit semantics.kind is query or command. Exposing an event or stream fails
the transaction; remove it from http.expose until that operation kind is
supported rather than relabeling the contract.
PLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED reports the effective http.expose
document at 1:1 as an exposure source before selector-aware recovery.

PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit
selector is present; setting both is an error. Explicit --env or --config
overrides both variables, and the two flags cannot be combined. Relative
configuration paths are resolved from the detected Plystra Project root.
Invalid or conflicting selections emit the stable
PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
Malformed exact Capability IDs emit the stable
PLYSTRA_CAPABILITY_EXPOSE_REFERENCE_INVALID diagnostic before Project discovery
or mutation.
A well-formed exact Capability absent from the selected visible catalog emits
PLYSTRA_CAPABILITY_EXPOSE_NOT_VISIBLE before write planning or mutation.
An exposure with no enabled HTTP transport emits
PLYSTRA_HTTP_TRANSPORT_SELECTION_INVALID with effective exposure sources and
rolls back the attempted write.
`
	capabilityCreateHelp = `Usage:
  ` + capabilityCreateSynopsis + `

Intent profiles:
  --query   Create a read-only, safely retryable query contract for a new Capability identity.

A new Capability identity requires one explicit intent profile. A later version
copies the complete semantics of its highest visible source contract; omit the
profile flag in that case. Names never imply semantics.

Malformed Capability names or optional exact IDs emit the stable
PLYSTRA_CAPABILITY_CREATE_REFERENCE_INVALID diagnostic before Project discovery
or mutation.
An already-visible exact version emits
PLYSTRA_CAPABILITY_CREATE_ALREADY_VISIBLE and directs the developer to the
implementation command without changing the Project.
An explicit older or skipped new version emits
PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED and requires the same create
command to be repeated with --confirm before mutation.
An omitted version whose highest visible major is already the maximum emits
PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED and requires a new Capability
identity before mutation.
A missing new-identity profile emits
PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED; a profile supplied while
copying a later version emits PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED.
Both failures occur before Project mutation.
If visible Providers disagree on the source exact contract, the command emits
PLYSTRA_CAPABILITY_SCHEMA_CONFLICT with every module-relative capability.yaml
declaration before mutation.
If --expose leaves no enabled HTTP transport, the command emits
PLYSTRA_HTTP_TRANSPORT_SELECTION_INVALID with effective exposure sources and
rolls back the attempted creation.
`
	capabilityImplementHelp = `Usage:
  ` + capabilityImplementSynopsis + `

Malformed exact Capability IDs emit the stable
PLYSTRA_CAPABILITY_IMPLEMENT_REFERENCE_INVALID diagnostic before Project
discovery or mutation.
A valid exact version that is not visible emits
PLYSTRA_CAPABILITY_IMPLEMENT_NOT_VISIBLE and directs the developer to the
creation command without changing the Project.
If visible Providers disagree on that exact contract, the command emits
PLYSTRA_CAPABILITY_SCHEMA_CONFLICT with every module-relative capability.yaml
declaration before mutation.
`
)

type capabilityArguments struct {
	action      string
	reference   string
	plugin      string
	confirm     bool
	expose      bool
	query       bool
	config      string
	environment string
}

func runCapability(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string, selectPlugin plugintarget.Selector) int {
	if help, ok := capabilityHelp(arguments); ok {
		_, _ = io.WriteString(stdout, help)
		return 0
	}
	parsed, ok := parseCapabilityArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, capabilityArgumentUsage(arguments))
		return 2
	}
	if rejectConflictingConfigurationSelectors(stderr, parsed.config, parsed.environment) {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	if parsed.action == "expose" {
		result, err := capabilityexpose.Expose(ctx, capabilityexpose.Options{
			Start:             workingDirectory,
			Reference:         parsed.reference,
			ConfigurationPath: parsed.config,
			EnvironmentName:   parsed.environment,
			Environment:       environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext(parsed.config, parsed.environment, environment))
			return 1
		}
		if result.Changed() {
			_, _ = fmt.Fprintf(stdout, "exposed capability %s over HTTP in %s\n", result.Capability(), result.ManifestPath())
		} else {
			_, _ = fmt.Fprintf(stdout, "capability %s is already exposed over HTTP in %s\n", result.Capability(), result.ManifestPath())
		}
		return 0
	}
	options := capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       workingDirectory,
			Reference:   parsed.reference,
			Plugin:      parsed.plugin,
			Intent:      capabilityIntent(parsed),
			Select:      selectPlugin,
			Environment: environment,
		},
		Confirm: parsed.confirm,
		Expose:  parsed.expose,
	}
	var result capabilitycreate.Result
	var err error
	switch parsed.action {
	case "create":
		result, err = capabilitycreate.Create(ctx, options)
	case "implement":
		result, err = capabilitycreate.Implement(ctx, options)
	default:
		panic("validated capability action is unsupported")
	}
	if err != nil {
		writeCommandFailure(stderr, "", err, commandRecoveryContext("", "", environment))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s capability %s in %s at %s\n", pastTense(parsed.action), result.Capability(), result.PluginID(), result.CapabilityPath())
	if parsed.expose {
		_, _ = fmt.Fprintf(stdout, "exposed capability %s over HTTP in %s\n", result.Capability(), result.ApplicationManifestPath())
	}
	writeCapabilityRecommendations(stderr, result)
	return 0
}

func writeCapabilityRecommendations(writer io.Writer, result capabilitycreate.Result) {
	recommendations := result.Recommendations()
	if len(recommendations) == 0 {
		return
	}
	values := make([]string, len(recommendations))
	for index, recommendation := range recommendations {
		values[index] = recommendation.String()
	}
	label := "Capability"
	if len(values) != 1 {
		label = "Capabilities"
	}
	_, _ = fmt.Fprintf(writer, "recommendation: review visible %s %s before keeping custom %s\n", label, strings.Join(values, ", "), result.Capability())
}

func parseCapabilityArguments(arguments []string) (capabilityArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "capability" || (arguments[1] != "create" && arguments[1] != "implement" && arguments[1] != "expose") || arguments[2] == "" || strings.HasPrefix(arguments[2], "--") {
		return capabilityArguments{}, false
	}
	result := capabilityArguments{action: arguments[1], reference: arguments[2]}
	if result.action == "expose" {
		configurationSet := false
		environmentSet := false
		for index := 3; index < len(arguments); index++ {
			switch arguments[index] {
			case "--config":
				if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
					return capabilityArguments{}, false
				}
				configurationSet = true
				index++
				result.config = arguments[index]
			case "--env":
				if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
					return capabilityArguments{}, false
				}
				environmentSet = true
				index++
				result.environment = arguments[index]
			default:
				return capabilityArguments{}, false
			}
		}
		return result, true
	}
	pluginSet := false
	profileSet := false
	for index := 3; index < len(arguments); index++ {
		switch arguments[index] {
		case "--plugin":
			if pluginSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return capabilityArguments{}, false
			}
			pluginSet = true
			index++
			result.plugin = arguments[index]
		case "--confirm":
			if result.action != "create" || result.confirm {
				return capabilityArguments{}, false
			}
			result.confirm = true
		case "--query":
			if result.action != "create" || profileSet {
				return capabilityArguments{}, false
			}
			profileSet = true
			result.query = true
		case "--expose":
			if result.action != "create" || result.expose {
				return capabilityArguments{}, false
			}
			result.expose = true
		default:
			return capabilityArguments{}, false
		}
	}
	return result, true
}

func capabilityHelp(arguments []string) (string, bool) {
	switch {
	case len(arguments) == 2 && arguments[0] == "capability" && isHelp(arguments[1]):
		return capabilityUsage, true
	case len(arguments) == 3 && arguments[0] == "capability" && arguments[1] == "create" && isHelp(arguments[2]):
		return capabilityCreateHelp, true
	case len(arguments) == 3 && arguments[0] == "capability" && arguments[1] == "implement" && isHelp(arguments[2]):
		return capabilityImplementHelp, true
	case len(arguments) == 3 && arguments[0] == "capability" && arguments[1] == "expose" && isHelp(arguments[2]):
		return capabilityExposeHelp, true
	default:
		return "", false
	}
}

func capabilityIntent(arguments capabilityArguments) capabilitycreate.IntentProfile {
	if arguments.query {
		return capabilitycreate.IntentProfileQuery
	}
	return ""
}

func capabilityArgumentUsage(arguments []string) string {
	if len(arguments) >= 2 {
		switch arguments[1] {
		case "create":
			return capabilityCreateUsage
		case "implement":
			return capabilityImplementUsage
		case "expose":
			return capabilityExposeUsage
		}
	}
	return strings.ToLower(capabilityUsage[:1]) + capabilityUsage[1:]
}

func isHelp(value string) bool { return value == "help" || value == "-h" || value == "--help" }

func pastTense(action string) string {
	if action == "create" {
		return "created"
	}
	return "implemented"
}
