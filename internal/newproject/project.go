// Package newproject creates a validated Plystra Go Module in an atomic stage.
package newproject

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/plugincreate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// KernelVersion is the exact Kernel release targeted by this CLI release.
const KernelVersion = "v0.1.0"

// ErrCreate reports a project creation failure.
var ErrCreate = errors.New("create Plystra project")

// Options contains the explicit inputs and process environment for creation.
type Options struct {
	Parent      string
	ModulePath  string
	Library     bool
	Plugin      string
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

// Create stages, validates, and atomically commits a new Plystra Go Module.
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
	if options.Plugin != "" {
		if _, err := plugincreate.DeriveID(options.ModulePath, options.Plugin); err != nil {
			return Result{}, fmt.Errorf("%w: initial plugin: %w", ErrCreate, err)
		}
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
		if err := populate(stagingRoot, options.ModulePath, name, options.Library); err != nil {
			return err
		}
		for _, arguments := range [][]string{{"mod", "download"}, {"mod", "tidy"}} {
			if err := gocommand.Run(ctx, gocommand.Options{Command: goCommand, Directory: stagingRoot, Environment: environment}, arguments...); err != nil {
				return err
			}
		}
		if options.Plugin != "" {
			if _, err := plugincreate.Create(ctx, plugincreate.Options{
				Start:       stagingRoot,
				Name:        options.Plugin,
				GoCommand:   goCommand,
				Environment: environment,
			}); err != nil {
				return err
			}
		} else if err := gocommand.Run(ctx, gocommand.Options{Command: goCommand, Directory: stagingRoot, Environment: environment}, "test", "./..."); err != nil {
			return err
		}
		return verifyModule(stagingRoot, options.ModulePath, options.Library)
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{modulePath: options.ModulePath, path: target}, nil
}

func populate(root, modulePath, name string, library bool) error {
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		return fmt.Errorf("render Kernel compatibility source: %w", err)
	}
	readme := readmeTemplate
	if library {
		readme = libraryReadmeTemplate
	}
	files := []struct {
		path string
		data []byte
	}{
		{path: "go.mod", data: []byte(fmt.Sprintf(goModuleTemplate, modulePath, KernelVersion))},
		{path: "README.md", data: []byte(fmt.Sprintf(readme, name, modulePath))},
		{path: ".gitignore", data: []byte(gitignoreTemplate)},
		{path: ".gitattributes", data: []byte(gitattributesTemplate)},
		{path: ".github/workflows/ci.yml", data: []byte(ciTemplate)},
		{path: "generated/assembly/compatibility_gen.go", data: compatibility},
	}
	if !library {
		files = append(files, struct {
			path string
			data []byte
		}{path: "plystra.yaml", data: []byte(fmt.Sprintf(plystraTemplate, name))})
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

func verifyModule(root, modulePath string, library bool) error {
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
	configuration, err := os.Lstat(filepath.Join(root, "plystra.yaml"))
	if library {
		if err == nil {
			return errors.New("library module contains plystra.yaml")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect library plystra.yaml: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect generated plystra.yaml: %w", err)
	}
	if !configuration.Mode().IsRegular() {
		return errors.New("generated plystra.yaml is not a regular file")
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
