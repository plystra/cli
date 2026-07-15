// Package gocommand runs bounded Go tool commands for transactional CLI work.
package gocommand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const maximumErrorOutput = 4096

var commandOutputURL = regexp.MustCompile(`(?i)\b(?:https?|file)://[^\s]+`)

// ErrRun reports an unsuccessful Go tool invocation.
var ErrRun = errors.New("go command failed")

// Options controls one Go tool invocation.
type Options struct {
	Command     string
	Directory   string
	Environment []string
}

// Run invokes the Go tool with an isolated working directory and sanitized
// diagnostics suitable for returning across the CLI boundary.
func Run(ctx context.Context, options Options, arguments ...string) error {
	command := options.Command
	if command == "" {
		command = "go"
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}

	process := exec.CommandContext(ctx, command, arguments...)
	process.Dir = options.Directory
	process.Env = append([]string(nil), environment...)
	output, err := process.CombinedOutput()
	if err == nil {
		return nil
	}
	operation := "go " + strings.Join(arguments, " ")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	message := SanitizeOutput(string(output), options.Directory)
	if message == "" {
		return fmt.Errorf("%w: %s", ErrRun, operation)
	}
	if len(message) > maximumErrorOutput {
		message = message[:maximumErrorOutput] + "..."
	}
	return fmt.Errorf("%w: %s: %s", ErrRun, operation, message)
}

// SanitizeOutput removes private local paths and credential-bearing URLs from
// bounded command diagnostics.
func SanitizeOutput(output string, privatePaths ...string) string {
	message := strings.TrimSpace(output)
	for _, privatePath := range privatePaths {
		if privatePath == "" {
			continue
		}
		message = strings.ReplaceAll(message, privatePath, ".")
		message = strings.ReplaceAll(message, filepath.ToSlash(privatePath), ".")
	}
	return commandOutputURL.ReplaceAllString(message, "<redacted-url>")
}
