package command

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/plystra/cli/internal/implementationcreate"
)

const implementUsage = `Usage:
  plystra implement <interface-id> --package <project-relative-package>

Creates a new ordinary Go package that implements one visible canonical
Interface. The package path must begin with ./ and its target directory must not
already exist. The scaffold imports the canonical Interface package and creates
no copied contract, generated substitute, configuration, or registration code.
`

func runImplement(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if len(arguments) == 2 && isHelp(arguments[1]) {
		_, _ = io.WriteString(stdout, implementUsage)
		return 0
	}
	if len(arguments) != 4 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") || arguments[2] != "--package" || strings.TrimSpace(arguments[3]) == "" || strings.HasPrefix(arguments[3], "--") {
		_, _ = io.WriteString(stderr, implementUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := implementationcreate.Create(ctx, implementationcreate.Options{
		Start:       workingDirectory,
		InterfaceID: arguments[1],
		Package:     arguments[3],
		Environment: environment,
	})
	if err != nil {
		writeCommandFailure(stderr, "create Implementation", err, commandRecoveryContext("", "", environment))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "created Implementation %s for %s at %s\n", result.Constructor(), result.InterfaceID(), result.SourcePath())
	return 0
}
