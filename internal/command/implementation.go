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
  plystra implement <contract> --package <project-relative-package>

Creates an unfinished ordinary Go scaffold for one visible canonical Interface
or Resource ID including /vN. The kind is inferred without a flag. The package
path must begin with ./ and its target directory must not already exist.
The scaffold imports the canonical contract and adds a conformance assertion.
Resource constructors return an error and methods panic until implemented.
No configuration, dependencies, lifecycle hooks, or activation are invented;
the command creates no selected Resource instance or generated output.
Missing, ambiguous, or inaccessible contracts fail before mutation.
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
		ContractID:  arguments[1],
		Package:     arguments[3],
		Environment: environment,
	})
	if err != nil {
		writeCommandFailure(stderr, "scaffold contract implementation", err, commandRecoveryContext("", "", environment))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "created unfinished %s scaffold %s for %s at %s; not activated\n", result.Kind(), result.Constructor(), result.ContractID(), result.SourcePath())
	return 0
}
