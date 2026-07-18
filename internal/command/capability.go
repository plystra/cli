package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/plugintarget"
)

const (
	capabilityCreateSynopsis    = "plystra capability create <capability-name> [--plugin <plugin>] [--confirm]"
	capabilityImplementSynopsis = "plystra capability implement <capability-name>/vN [--plugin <plugin>]"
	capabilityUsage             = `Usage:
  ` + capabilityCreateSynopsis + `
  ` + capabilityImplementSynopsis + `
`
	capabilityCreateUsage    = "usage: " + capabilityCreateSynopsis + "\n"
	capabilityImplementUsage = "usage: " + capabilityImplementSynopsis + "\n"
)

type capabilityArguments struct {
	action    string
	reference string
	plugin    string
	confirm   bool
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
	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	options := capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       workingDirectory,
			Reference:   parsed.reference,
			Plugin:      parsed.plugin,
			Select:      selectPlugin,
			Environment: environment,
		},
		Confirm: parsed.confirm,
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
		if errors.Is(err, capabilitycreate.ErrConfirmationRequired) {
			_, _ = fmt.Fprintf(stderr, "%v; rerun with --confirm after reviewing visible Capability versions\n", err)
		} else {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "%s capability %s in %s at %s\n", pastTense(parsed.action), result.Capability(), result.PluginID(), result.CapabilityPath())
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
	if len(arguments) < 3 || arguments[0] != "capability" || (arguments[1] != "create" && arguments[1] != "implement") || arguments[2] == "" || strings.HasPrefix(arguments[2], "--") {
		return capabilityArguments{}, false
	}
	result := capabilityArguments{action: arguments[1], reference: arguments[2]}
	pluginSet := false
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
		return "Usage:\n  " + capabilityCreateSynopsis + "\n", true
	case len(arguments) == 3 && arguments[0] == "capability" && arguments[1] == "implement" && isHelp(arguments[2]):
		return "Usage:\n  " + capabilityImplementSynopsis + "\n", true
	default:
		return "", false
	}
}

func capabilityArgumentUsage(arguments []string) string {
	if len(arguments) >= 2 {
		switch arguments[1] {
		case "create":
			return capabilityCreateUsage
		case "implement":
			return capabilityImplementUsage
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
