// Package newproject creates a validated Plystra Go Module in an atomic stage.
package newproject

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/atomicfs"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// KernelVersion is the exact Kernel release targeted by this CLI release.
const KernelVersion = "v0.1.0"

// ErrCreate reports a project creation failure.
var ErrCreate = errors.New("create Plystra project")

var commandOutputURL = regexp.MustCompile(`(?i)\b(?:https?|file)://[^\s]+`)

// Options contains the explicit inputs and process environment for creation.
type Options struct {
	Parent      string
	ModulePath  string
	GoCommand   string
	Environment []string
}

// Result identifies a successfully committed project.
type Result struct {
	modulePath string
	path       string
}

// ModulePath returns the generated Go Module path.
func (r Result) ModulePath() string { return r.modulePath }

// Path returns the absolute committed project directory.
func (r Result) Path() string { return r.path }

// Create stages, validates, and atomically commits a new runnable project.
func Create(ctx context.Context, options Options) (Result, error) {
	if err := module.CheckPath(options.ModulePath); err != nil {
		return Result{}, fmt.Errorf("%w: invalid Go Module path %q: %v", ErrCreate, options.ModulePath, err)
	}
	prefix, _, ok := module.SplitPathVersion(options.ModulePath)
	if !ok {
		return Result{}, fmt.Errorf("%w: invalid semantic import version in %q", ErrCreate, options.ModulePath)
	}
	name := path.Base(prefix)
	if !validProjectName(name) {
		return Result{}, fmt.Errorf("%w: module base %q must be lower-case ASCII kebab-case", ErrCreate, name)
	}
	parent := options.Parent
	if strings.TrimSpace(parent) == "" {
		return Result{}, fmt.Errorf("%w: parent directory is empty", ErrCreate)
	}
	absoluteParent, err := filepath.Abs(parent)
	if err != nil {
		return Result{}, fmt.Errorf("%w: resolve parent directory: %v", ErrCreate, err)
	}
	target := filepath.Join(absoluteParent, name)
	goCommand := options.GoCommand
	if goCommand == "" {
		goCommand = "go"
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}

	err = atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		if err := populate(stagingRoot, options.ModulePath, name); err != nil {
			return err
		}
		for _, arguments := range [][]string{{"mod", "download"}, {"mod", "tidy"}, {"test", "./..."}} {
			if err := runGo(ctx, goCommand, environment, stagingRoot, arguments...); err != nil {
				return err
			}
		}
		return verifyModule(stagingRoot, options.ModulePath)
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{modulePath: options.ModulePath, path: target}, nil
}

func populate(root, modulePath, name string) error {
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		return fmt.Errorf("render Kernel compatibility source: %w", err)
	}
	files := []struct {
		path string
		data []byte
	}{
		{path: "go.mod", data: []byte(fmt.Sprintf(goModuleTemplate, modulePath, KernelVersion))},
		{path: "plystra.yaml", data: []byte(fmt.Sprintf(plystraTemplate, name))},
		{path: "README.md", data: []byte(fmt.Sprintf(readmeTemplate, name, modulePath))},
		{path: ".gitignore", data: []byte(gitignoreTemplate)},
		{path: ".gitattributes", data: []byte(gitattributesTemplate)},
		{path: ".github/workflows/ci.yml", data: []byte(ciTemplate)},
		{path: "generated/assembly/compatibility_gen.go", data: compatibility},
	}
	for _, file := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", file.path, err)
		}
		if err := os.WriteFile(fullPath, file.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file.path, err)
		}
	}
	return nil
}

func runGo(ctx context.Context, command string, environment []string, directory string, arguments ...string) error {
	process := exec.CommandContext(ctx, command, arguments...)
	process.Dir = directory
	process.Env = append([]string(nil), environment...)
	output, err := process.CombinedOutput()
	if err == nil {
		return nil
	}
	operation := "go " + strings.Join(arguments, " ")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	message := sanitizeCommandOutput(string(output), directory)
	if message == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if len(message) > 4096 {
		message = message[:4096] + "..."
	}
	return fmt.Errorf("%s: %w: %s", operation, err, message)
}

func sanitizeCommandOutput(output, stagingRoot string) string {
	message := strings.TrimSpace(output)
	message = strings.ReplaceAll(message, stagingRoot, ".")
	message = strings.ReplaceAll(message, filepath.ToSlash(stagingRoot), ".")
	return commandOutputURL.ReplaceAllString(message, "<redacted-url>")
}

func verifyModule(root, modulePath string) error {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read generated go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("parse generated go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != modulePath {
		return errors.New("generated go.mod lost its module path")
	}
	foundKernel := false
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == "github.com/plystra/kernel" && requirement.Mod.Version == KernelVersion && !requirement.Indirect {
			foundKernel = true
			break
		}
	}
	if !foundKernel {
		return fmt.Errorf("generated go.mod does not require github.com/plystra/kernel %s", KernelVersion)
	}
	info, err := os.Stat(filepath.Join(root, "go.sum"))
	if err != nil {
		return fmt.Errorf("inspect generated go.sum: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("generated go.sum is empty or not a regular file")
	}
	return nil
}

func validProjectName(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}
