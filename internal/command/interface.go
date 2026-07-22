package command

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/plystra/cli/internal/interfacecreate"
)

const (
	interfaceUsage = `Usage:
  plystra interface create <interface-name>
`
	interfaceCreateUsage = `Usage:
  plystra interface create <interface-name>

Creates the initial v1 canonical Go package for one unversioned Interface name.
The name must contain two or more lower-case dot-separated segments. The command
does not create optional metadata or edit application configuration.
`
)

func runInterface(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if len(arguments) == 2 && isHelp(arguments[1]) {
		_, _ = io.WriteString(stdout, interfaceUsage)
		return 0
	}
	if len(arguments) == 3 && arguments[1] == "create" && isHelp(arguments[2]) {
		_, _ = io.WriteString(stdout, interfaceCreateUsage)
		return 0
	}
	if len(arguments) < 2 || arguments[1] != "create" {
		_, _ = io.WriteString(stderr, interfaceUsage)
		return 2
	}
	if len(arguments) != 3 || strings.TrimSpace(arguments[2]) == "" || strings.HasPrefix(arguments[2], "--") {
		_, _ = io.WriteString(stderr, interfaceCreateUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := interfacecreate.Create(ctx, interfacecreate.Options{
		Start:       workingDirectory,
		Name:        arguments[2],
		Environment: environment,
	})
	if err != nil {
		writeCommandFailure(stderr, "create Interface", err, commandRecoveryContext("", "", environment))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "created Interface %s at %s\n", result.ID(), result.SourcePath())
	return 0
}
