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
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/plugincreate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// KernelVersion is the exact Kernel release targeted by this CLI release.
const KernelVersion = "v0.0.0-20260718010024-34af10315d98"

var (
	// ErrCreate reports a project creation failure.
	ErrCreate = errors.New("create Plystra project")
	// ErrGitInitialization reports a failed requested Git repository setup.
	ErrGitInitialization = errors.New("initialize Git repository")
)

// Options contains the explicit inputs and process environment for creation.
type Options struct {
	Parent      string
	ModulePath  string
	Plugin      string
	Git         bool
	GitHubCI    bool
	Skills      bool
	GoCommand   string
	GitCommand  string
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
		if err := populate(stagingRoot, options.ModulePath, name, options.GitHubCI, options.Skills); err != nil {
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
		if err := verifyModule(stagingRoot, options.ModulePath); err != nil {
			return err
		}
		if options.Git {
			if err := initializeGit(ctx, stagingRoot, options.GitCommand, environment); err != nil {
				return err
			}
		}
		return verifyChoices(stagingRoot, options.ModulePath, options.Git, options.GitHubCI, options.Skills)
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{modulePath: options.ModulePath, path: target}, nil
}

func populate(root, modulePath, name string, githubCI, skills bool) error {
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		return fmt.Errorf("render Kernel compatibility source: %w", err)
	}
	managed := make([]generatedfiles.File, 0, 5)
	compatibilityFile, err := generatedfiles.NewFile("generated/go/assembly/compatibility_gen.go", compatibility)
	if err != nil {
		return fmt.Errorf("prepare Kernel compatibility source: %w", err)
	}
	managed = append(managed, compatibilityFile)
	invocations, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               modulePath,
		ApplicationBuildIdentity: "initial-scaffold",
		KernelModuleVersion:      KernelVersion,
		DefaultTimeout:           applicationmeta.DefaultInvocationTimeout,
	})
	if err != nil {
		return fmt.Errorf("render intrinsic-only canonical invocation source: %w", err)
	}
	invocationsFile, err := generatedfiles.NewFile(assemblygen.InvocationsPath, invocations)
	if err != nil {
		return fmt.Errorf("prepare intrinsic-only canonical invocation source: %w", err)
	}
	managed = append(managed, invocationsFile)
	bootstrap, err := bootstrapgen.Render(bootstrapgen.Options{
		ModulePath:            modulePath,
		DefaultStartupTimeout: applicationmeta.DefaultStartupTimeout,
	})
	if err != nil {
		return fmt.Errorf("render runtime bootstrap source: %w", err)
	}
	bootstrapFile, err := generatedfiles.NewFile(bootstrapgen.Path, bootstrap)
	if err != nil {
		return fmt.Errorf("prepare runtime bootstrap source: %w", err)
	}
	managed = append(managed, bootstrapFile)
	providers, err := assemblygen.RenderProviders(modulePath, nil)
	if err != nil {
		return fmt.Errorf("render empty selected-provider source: %w", err)
	}
	providersFile, err := generatedfiles.NewFile(assemblygen.ProvidersPath, providers)
	if err != nil {
		return fmt.Errorf("prepare empty selected-provider source: %w", err)
	}
	managed = append(managed, providersFile)
	aliasManifest, err := generatedfiles.NewFile("generated/manifest.json", []byte("{\"capability_aliases\":[]}\n"))
	if err != nil {
		return fmt.Errorf("prepare empty Alias manifest: %w", err)
	}
	managed = append(managed, aliasManifest)
	generated, err := generatedfiles.NewOutput(managed)
	if err != nil {
		return fmt.Errorf("prepare initial generated output: %w", err)
	}
	readme := fmt.Sprintf(readmeTemplate, name, modulePath)
	if githubCI {
		readme += githubCIReadmeTemplate
	}
	if skills {
		readme += skillsReadmeTemplate
	}
	type projectFile struct {
		path string
		data []byte
	}
	files := []projectFile{
		{path: "go.mod", data: fmt.Appendf(nil, goModuleTemplate, modulePath, KernelVersion)},
		{path: "README.md", data: []byte(readme)},
		{path: ".gitignore", data: []byte(gitignoreTemplate)},
		{path: ".gitattributes", data: []byte(gitattributesTemplate)},
	}
	if githubCI {
		files = append(files, projectFile{path: ".github/workflows/ci.yml", data: []byte(ciTemplate)})
	}
	if skills {
		files = append(files,
			projectFile{path: ".agents/skills/plystra/SKILL.md", data: fmt.Appendf(nil, skillTemplate, modulePath)},
			projectFile{path: ".agents/skills/plystra/agents/openai.yaml", data: []byte(skillAgentTemplate)},
		)
	}
	files = append(files, projectFile{path: "plystra.yaml", data: []byte(plystraTemplate)})
	for _, file := range generated.Files() {
		files = append(files, projectFile{path: file.Path(), data: file.Data()})
	}
	files = append(files, projectFile{path: generatedfiles.ManifestPath, data: generated.ManifestJSON()})
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

func initializeGit(ctx context.Context, root, command string, environment []string) error {
	if command == "" {
		command = "git"
	}
	process := exec.CommandContext(ctx, command, "init", "--quiet", "--initial-branch=main", "--template=")
	process.Dir = root
	process.Env = append([]string(nil), environment...)
	output, err := process.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %v", ErrGitInitialization, ctxErr)
	}
	message := gocommand.SanitizeOutput(string(output), root)
	if len(message) > 4096 {
		message = message[:4096] + "..."
	}
	if message == "" {
		return fmt.Errorf("%w: git init failed", ErrGitInitialization)
	}
	return fmt.Errorf("%w: git init failed: %s", ErrGitInitialization, message)
}

func verifyChoices(root, modulePath string, git, githubCI, skills bool) error {
	if err := verifyChoicePath(root, ".git", git, true); err != nil {
		return err
	}
	if err := verifyChoicePath(root, ".github/workflows/ci.yml", githubCI, false); err != nil {
		return err
	}
	if err := verifyChoicePath(root, ".agents/skills/plystra/SKILL.md", skills, false); err != nil {
		return err
	}
	if err := verifyChoicePath(root, ".agents/skills/plystra/agents/openai.yaml", skills, false); err != nil {
		return err
	}
	if skills {
		data, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "plystra", "SKILL.md"))
		if err != nil {
			return fmt.Errorf("read generated Plystra skill: %w", err)
		}
		if err := validateGeneratedSkill(data, modulePath); err != nil {
			return err
		}
	}
	return nil
}

func validateGeneratedSkill(data []byte, modulePath string) error {
	if len(data) == 0 || len(data) > 64<<10 {
		return errors.New("generated Plystra skill has an invalid size")
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\nname: plystra\n") || strings.Contains(text, "TODO") {
		return errors.New("generated Plystra skill is incomplete")
	}
	required := []string{
		"description: Develop, structure, configure, debug, and validate Plystra Go Modules",
		"The current Go Module path is " + modulePath,
		"## Module and file ownership",
		"plystra plugin create records",
		"plystra capability create records.read --plugin records --expose",
		"plystra capability implement email.send/v1 --plugin mailer",
		"capabilities/records.read/v1/capability.yaml",
		"There is no handwritten provider registration",
		"dependencies.Dependencies",
		"generated/go/dependencies/",
		"bootstrap.New",
		"npm run typecheck",
		"plystra generate --check",
	}
	for _, phrase := range required {
		if !strings.Contains(text, phrase) {
			return fmt.Errorf("generated Plystra skill omits required guidance %q", phrase)
		}
	}
	forbiddenWords := map[string]struct{}{
		"branch": {}, "branches": {}, "checkout": {}, "checkouts": {},
		"commit": {}, "commits": {}, "committed": {}, "committing": {},
		"git": {}, "github": {}, "pull": {}, "pulled": {}, "pulling": {}, "pulls": {},
		"push": {}, "pushed": {}, "pushes": {}, "pushing": {},
		"repositories": {}, "repository": {},
	}
	processGuidance := strings.ReplaceAll(text, modulePath, "module-path")
	words := strings.FieldsFunc(strings.ToLower(processGuidance), func(character rune) bool {
		return character < 'a' || character > 'z'
	})
	for _, word := range words {
		if _, forbidden := forbiddenWords[word]; forbidden {
			return fmt.Errorf("generated Plystra skill contains unrelated development-process guidance %q", word)
		}
	}
	if strings.Contains(strings.ToLower(processGuidance), "version control") {
		return errors.New("generated Plystra skill contains unrelated development-process guidance")
	}
	return nil
}

func verifyChoicePath(root, relativePath string, expected, directory bool) error {
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relativePath)))
	if !expected {
		if err == nil {
			return fmt.Errorf("unrequested scaffold path %s exists", relativePath)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect unrequested scaffold path %s: %w", relativePath, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect requested scaffold path %s: %w", relativePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || directory != info.IsDir() {
		return fmt.Errorf("requested scaffold path %s has an invalid type", relativePath)
	}
	return nil
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
	configuration, err := os.Lstat(filepath.Join(root, "plystra.yaml"))
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
