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

	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/modfile"
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
	environment = isolateUnlistedWorkspace(options.Directory, environment)
	process := exec.CommandContext(ctx, command, arguments...)
	process.Dir = options.Directory
	process.Env = append([]string(nil), environment...)
	return process
}

// isolateUnlistedWorkspace prevents an automatically discovered parent
// workspace from redirecting commands for a nearest module it does not list.
// Explicit GOWORK selections and valid workspaces containing the module remain
// authoritative for local multi-module development.
func isolateUnlistedWorkspace(directory string, environment []string) []string {
	result := append([]string(nil), environment...)
	if directory == "" || hasEnvironmentKey(environment, "GOWORK") {
		return result
	}
	module, err := modulelocate.Find(directory)
	if err != nil {
		return result
	}
	workspacePath, exists := enclosingFile(directory, "go.work")
	if !exists {
		return result
	}
	data, err := os.ReadFile(workspacePath)
	if err != nil {
		return result
	}
	workspace, err := modfile.ParseWork(workspacePath, data, nil)
	if err != nil {
		return result
	}
	workspaceRoot := filepath.Dir(workspacePath)
	for _, use := range workspace.Use {
		usedPath, valid := workspaceUseDirectory(workspaceRoot, use.Path)
		if !valid {
			return result
		}
		if sameDirectory(module.Path(), usedPath) {
			return result
		}
	}
	return append(result, "GOWORK=off")
}

func workspaceUseDirectory(workspaceRoot, usedPath string) (string, bool) {
	if !filepath.IsAbs(usedPath) {
		usedPath = filepath.Join(workspaceRoot, usedPath)
	}
	absolute, err := filepath.Abs(usedPath)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", false
	}
	module, err := modulelocate.Find(canonical)
	if err != nil || !sameDirectory(module.Path(), canonical) {
		return "", false
	}
	return module.Path(), true
}

func hasEnvironmentKey(environment []string, wanted string) bool {
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, wanted) {
			return true
		}
	}
	return false
}

func enclosingFile(start, name string) (string, bool) {
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", false
	}
	for directory := canonical; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", false
		}
	}
}

func sameDirectory(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftPath, leftErr = filepath.EvalSymlinks(leftPath)
	rightPath, rightErr = filepath.EvalSymlinks(rightPath)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftInfo, leftErr := os.Stat(leftPath)
	rightInfo, rightErr := os.Stat(rightPath)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
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
