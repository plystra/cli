// Package gocommand runs bounded Go tool commands for transactional CLI work.
package gocommand

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maximumErrorOutput = 4096
	defaultOutputLimit = 16 << 20
)

var commandOutputURL = regexp.MustCompile(`(?i)\b(?:https?|file)://[^\s]+`)

// ErrRun reports an unsuccessful Go tool invocation.
var (
	ErrRun            = errors.New("go command failed")
	ErrOutputTooLarge = errors.New("go command output exceeds limit")
)

// Options controls one Go tool invocation.
type Options struct {
	Command     string
	Directory   string
	Environment []string
	OutputLimit int
}

// Run invokes the Go tool with an isolated working directory and sanitized
// diagnostics suitable for returning across the CLI boundary.
func Run(ctx context.Context, options Options, arguments ...string) error {
	process := commandContext(ctx, options, arguments...)
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

// Output invokes the Go tool and returns bounded stdout. Failure diagnostics
// combine bounded stdout and stderr after removing private paths and URLs.
func Output(ctx context.Context, options Options, arguments ...string) ([]byte, error) {
	limit := options.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	stdout := newLimitedBuffer(limit)
	stderr := newLimitedBuffer(maximumErrorOutput)
	process := commandContext(ctx, options, arguments...)
	process.Stdout = stdout
	process.Stderr = stderr
	err := process.Run()
	operation := "go " + strings.Join(arguments, " ")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("%s: %w", operation, ctxErr)
	}
	if stdout.exceeded {
		return nil, fmt.Errorf("%w: %s: limit %d bytes", ErrOutputTooLarge, operation, limit)
	}
	if err != nil {
		message := SanitizeOutput(string(bytes.Join([][]byte{stdout.bytes(), stderr.bytes()}, []byte("\n"))), options.Directory)
		if message == "" {
			return nil, fmt.Errorf("%w: %s", ErrRun, operation)
		}
		if stderr.exceeded || len(message) > maximumErrorOutput {
			message = message[:min(len(message), maximumErrorOutput)] + "..."
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrRun, operation, message)
	}
	return stdout.bytes(), nil
}

func commandContext(ctx context.Context, options Options, arguments ...string) *exec.Cmd {
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
	return process
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining < len(data) {
		b.exceeded = true
		if remaining < 0 {
			remaining = 0
		}
		data = data[:remaining]
	}
	_, _ = b.buffer.Write(data)
	return original, nil
}

func (b *limitedBuffer) bytes() []byte {
	return append([]byte(nil), b.buffer.Bytes()...)
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
