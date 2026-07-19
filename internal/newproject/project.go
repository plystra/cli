// Package newproject creates a validated Plystra Go Module in an atomic stage.
package newproject

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/providerresolution"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// KernelVersion is the exact Kernel release targeted by this CLI release.
const KernelVersion = "v0.0.0-20260718010024-34af10315d98"

const maximumGoEnvironmentValueBytes = 64 << 10

var (
	// ErrCreate reports a project creation failure.
	ErrCreate = errors.New("create Plystra project")
	// ErrGitInitialization reports a failed requested Git repository setup.
	ErrGitInitialization = errors.New("initialize Git repository")
	// ErrInvalidTemplate reports a resolved module that cannot serve as a
	// Plystra Project template dependency.
	ErrInvalidTemplate = errors.New("invalid Plystra Project template")
)

// Options contains the explicit inputs and process environment for creation.
type Options struct {
	Parent      string
	ProjectName string
	ModulePath  string
	Template    string
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
	if !validProjectName(options.ProjectName) {
		return Result{}, fmt.Errorf("%w: project name %q must be one lower-case ASCII kebab-case child directory", ErrCreate, options.ProjectName)
	}
	modulePath := options.ModulePath
	if modulePath == "" {
		modulePath = options.ProjectName
		if err := modulepath.CheckProject(modulePath); err != nil {
			return Result{}, fmt.Errorf("%w: project name %q cannot be used as the initial Go Module path: %v", ErrCreate, options.ProjectName, err)
		}
	} else if err := module.CheckPath(modulePath); err != nil {
		return Result{}, fmt.Errorf("%w: invalid explicit Go Module path %q: %v", ErrCreate, modulePath, err)
	}
	templateQuery := ""
	templateModulePath := ""
	if options.Template != "" {
		var err error
		templateQuery, templateModulePath, err = moduleargument.ParseQuery(options.Template)
		if err != nil {
			return Result{}, fmt.Errorf("%w: template query: %w", ErrCreate, err)
		}
	}
	if options.Plugin != "" {
		if _, err := plugincreate.DeriveID(modulePath, options.Plugin); err != nil {
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
	target := filepath.Join(absoluteParent, options.ProjectName)
	goCommand := options.GoCommand
	if goCommand == "" {
		goCommand = "go"
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}

	err = atomicfs.CreateDirectory(target, func(stagingRoot string) error {
		if err := populate(ctx, stagingRoot, modulePath, options.ProjectName, options.GitHubCI, options.Skills); err != nil {
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
		if templateQuery != "" {
			if err := installTemplateDependency(ctx, stagingRoot, templateQuery, templateModulePath, goCommand, environment); err != nil {
				return err
			}
		}
		if err := verifyModule(stagingRoot, modulePath); err != nil {
			return err
		}
		if options.Git {
			if err := initializeGit(ctx, stagingRoot, options.GitCommand, environment); err != nil {
				return err
			}
		}
		return verifyChoices(stagingRoot, modulePath, options.Git, options.GitHubCI, options.Skills)
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{modulePath: modulePath, path: target}, nil
}

func installTemplateDependency(ctx context.Context, root, query, modulePath, goCommand string, environment []string) error {
	return modulemutation.Change(ctx, root, modulemutation.ChangeOptions{
		GoCommand:          goCommand,
		Environment:        environment,
		Arguments:          []string{"get", query},
		DirectRequirements: []string{modulePath},
	}, func(mutate applicationgenerate.ModuleMutation) error {
		project, err := projectlocate.Find(root)
		if err != nil {
			return fmt.Errorf("%w: locate staged Project: %w", ErrInvalidTemplate, err)
		}
		dependencies, err := moduledependency.Discover(ctx, project, moduledependency.Options{
			GoCommand:   goCommand,
			Environment: environment,
		})
		if err != nil {
			return fmt.Errorf("%w: inspect resolved template %q: %w", ErrInvalidTemplate, query, err)
		}
		template, exists := dependencies.ByPath(modulePath)
		if !exists {
			return fmt.Errorf("%w: query %q did not select module %q in the effective Go Module graph", ErrInvalidTemplate, query, modulePath)
		}
		if !template.Direct() || template.Indirect() {
			return fmt.Errorf("%w: resolved module %q was not recorded as an ordinary direct dependency", ErrInvalidTemplate, modulePath)
		}
		if !template.Project() {
			return fmt.Errorf("%w: resolved module %q has no regular root plystra.yaml", ErrInvalidTemplate, modulePath)
		}
		if err := rejectPrivateTemplateDependencies(ctx, root, query, goCommand, environment, dependencies); err != nil {
			return err
		}
		if err := rejectRelativeTemplateReplacements(query, dependencies); err != nil {
			return err
		}
		if _, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:            root,
			GoCommand:        goCommand,
			Environment:      environment,
			MutateModule:     mutate,
			RejectUnexpected: true,
		}); err != nil {
			if errors.Is(err, providerresolution.ErrAmbiguousProvider) {
				return fmt.Errorf(
					"%w: template %q cannot qualify because its default Provider model is ambiguous: %w; correction: the template publisher must add the listed capabilities.use choices to its root plystra.yaml and publish a corrected module version",
					ErrInvalidTemplate,
					query,
					err,
				)
			}
			return fmt.Errorf("generate Project from template dependency %q: %w", query, err)
		}
		checked, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:       root,
			Check:       true,
			GoCommand:   goCommand,
			Environment: environment,
		})
		if err != nil {
			return fmt.Errorf(
				"%w: template %q cannot qualify because generated stability checking failed immediately after installation: %w; correction: the template publisher must make generation deterministic, run plystra generate followed by plystra generate --check in a fresh Project directory, and publish a corrected module version",
				ErrInvalidTemplate,
				query,
				err,
			)
		}
		if checked.ConfigurationChanged() || !checked.Report().Clean() {
			return fmt.Errorf(
				"%w: template %q cannot qualify because generated output is not stable immediately after installation: %s; correction: the template publisher must make generation deterministic, run plystra generate followed by plystra generate --check in a fresh Project directory, and publish a corrected module version",
				ErrInvalidTemplate,
				query,
				strings.Join(templateGenerationDrift(checked), ", "),
			)
		}
		return nil
	})
}

func templateGenerationDrift(result applicationgenerate.Result) []string {
	details := make([]string, 0, len(result.Report().Changes())+1)
	if result.ConfigurationChanged() {
		details = append(details, fmt.Sprintf("changed %s (dependency composition)", result.ConfigurationMaintenancePath()))
	}
	for _, change := range result.Report().Changes() {
		details = append(details, fmt.Sprintf("%s %s", change.Kind(), change.Path()))
	}
	return details
}

func rejectPrivateTemplateDependencies(ctx context.Context, root, query, goCommand string, environment []string, dependencies moduledependency.Index) error {
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command:     goCommand,
		Directory:   root,
		Environment: environment,
		OutputLimit: maximumGoEnvironmentValueBytes,
	}, "env", "GOPRIVATE")
	if err != nil {
		return fmt.Errorf("%w: inspect Go privacy configuration while qualifying template %q: %w", ErrInvalidTemplate, query, err)
	}
	patterns := strings.TrimSpace(string(output))
	if patterns == "" {
		return nil
	}

	private := make([]string, 0)
	for _, dependency := range dependencies.Modules() {
		modulePrivate := module.MatchPrefixPatterns(patterns, dependency.Path())
		replacement, replaced := dependency.Replacement()
		replacementPrivate := replaced && !replacement.Local() && module.MatchPrefixPatterns(patterns, replacement.Path())
		if !modulePrivate && !replacementPrivate {
			continue
		}
		reference := selectedModuleReference(dependency)
		if replacementPrivate {
			reference += " => " + replacement.Path() + "@" + replacement.Version()
		}
		private = append(private, reference)
	}
	if len(private) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: template %q cannot qualify because its effective Go Module graph requires private modules matched by GOPRIVATE: %s; correction: qualified templates must use only public modules; publish or replace the listed dependencies, or correct an overbroad GOPRIVATE setting, then retry",
		ErrInvalidTemplate,
		query,
		strings.Join(private, ", "),
	)
}

func rejectRelativeTemplateReplacements(query string, dependencies moduledependency.Index) error {
	findings := make([]string, 0)
	for _, dependency := range dependencies.Projects() {
		parsed, err := modfile.Parse("go.mod", dependency.ProjectGoMod(), nil)
		if err != nil {
			return fmt.Errorf("%w: inspect dependency Project %s go.mod while qualifying template %q: %v", ErrInvalidTemplate, selectedModuleReference(dependency), query, err)
		}
		for _, replacement := range parsed.Replace {
			if replacement.New.Version != "" || !relativeReplacementPath(replacement.New.Path) {
				continue
			}
			old := replacement.Old.Path
			if replacement.Old.Version != "" {
				old += "@" + replacement.Old.Version
			}
			findings = append(findings, fmt.Sprintf(
				"%s/go.mod: replace %s => %s",
				selectedModuleReference(dependency),
				old,
				replacement.New.Path,
			))
		}
	}
	if len(findings) == 0 {
		return nil
	}
	sort.Strings(findings)
	return fmt.Errorf(
		"%w: template %q cannot qualify because dependency Plystra Projects declare relative Go Module replacements: %s; correction: publish every required module version and remove each relative replace from the listed go.mod before publishing a corrected template version",
		ErrInvalidTemplate,
		query,
		strings.Join(findings, "; "),
	)
}

func selectedModuleReference(dependency moduledependency.Module) string {
	version := dependency.SelectedVersion()
	if version == "" {
		version = "workspace"
	}
	return dependency.Path() + "@" + version
}

func relativeReplacementPath(value string) bool {
	normalized := strings.ReplaceAll(value, `\`, "/")
	return normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, "../")
}

func populate(ctx context.Context, root, modulePath, name string, githubCI, skills bool) error {
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
	currentManifest, err := applicationmeta.Parse([]byte(plystraTemplate))
	if err != nil {
		return fmt.Errorf("parse initial Project configuration: %w", err)
	}
	composition, err := applicationmeta.Compose(nil, currentManifest, func(string) (kernelmanifest.Config, bool) {
		return kernelmanifest.Config{}, false
	})
	if err != nil {
		return fmt.Errorf("compose initial Project configuration: %w", err)
	}
	input, err := applicationinput.Build(currentManifest, plugininventory.Index{}, generationexec.BuildOptions{})
	if err != nil {
		return fmt.Errorf("build initial application model: %w", err)
	}
	resolution, err := generationresolution.ResolveExtensions(ctx, input)
	if err != nil {
		return fmt.Errorf("resolve initial application model: %w", err)
	}
	modelDigest, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:          modulePath,
		KernelModuleVersion: KernelVersion,
		Resolution:          resolution,
	})
	if err != nil {
		return fmt.Errorf("digest initial application model: %w", err)
	}
	invocations, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               modulePath,
		ApplicationBuildIdentity: resolution.Context().Digest(),
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
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootData:               []byte(plystraTemplate),
		SelectedPath:           "plystra.yaml",
		SelectedData:           []byte(plystraTemplate),
		Composition:            composition,
		ApplicationModelDigest: modelDigest,
	})
	if err != nil {
		return fmt.Errorf("construct initial application manifest provenance: %w", err)
	}
	manifestData, err := applicationgen.RenderManifest(resolution.AliasResolution().CanonicalJSON(), provenance)
	if err != nil {
		return fmt.Errorf("render initial application manifest: %w", err)
	}
	aliasManifest, err := generatedfiles.NewFile("generated/manifest.json", manifestData)
	if err != nil {
		return fmt.Errorf("prepare initial application manifest: %w", err)
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
		"plystra new app",
		"plystra new app --module github.com/acme/app",
		"plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3",
		"Template-declared operational values and Secret-reference placeholders",
		"does not read PLATFORM_SMTP_PASSWORD",
		"invent values for required fields omitted by the template",
		"Template creation requires an unambiguous default Provider model",
		"Template dependencies must not match the effective GOPRIVATE setting",
		"Template dependency Projects must not declare relative replace directives",
		"plystra plugin create records",
		"plystra capability create records.read --plugin records --expose",
		"plystra capability implement email.send/v1 --plugin mailer",
		"capabilities/records.read/v1/capability.yaml",
		"There is no handwritten provider registration",
		"dependencies.Dependencies",
		"generated/go/dependencies/",
		"plystra add github.com/acme/email@v1.4.2",
		"plystra remove github.com/acme/email",
		"plystra update github.com/acme/email@v1.5.0",
		"bootstrap.New",
		"npm run typecheck",
		"plystra generate --check",
	}
	for _, phrase := range required {
		if !strings.Contains(text, phrase) {
			return fmt.Errorf("generated Plystra skill omits required guidance %q", phrase)
		}
	}
	return validateSkillProcessGuidance(text, modulePath)
}

func validateSkillProcessGuidance(text, modulePath string) error {
	forbiddenWords := map[string]struct{}{
		"branch": {}, "branches": {}, "checkout": {}, "checkouts": {},
		"commit": {}, "commits": {}, "committed": {}, "committing": {},
		"git": {}, "github": {}, "pull": {}, "pulled": {}, "pulling": {}, "pulls": {},
		"push": {}, "pushed": {}, "pushes": {}, "pushing": {},
		"repositories": {}, "repository": {},
	}
	processGuidance := strings.ReplaceAll(text, modulePath, "module-path")
	processGuidance = redactGoModuleReferences(processGuidance)
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

func redactGoModuleReferences(text string) string {
	redacted := []byte(text)
	for start := 0; start < len(text); {
		if !isModuleReferenceStart(text[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(text) && isModuleReferenceByte(text[end]) {
			end++
		}
		candidate := text[start:end]
		path := candidate
		if separator := strings.LastIndexByte(candidate, '@'); separator >= 0 {
			path = candidate[:separator]
			if separator == len(candidate)-1 {
				start = end
				continue
			}
		}
		if strings.Contains(path, "/") && module.CheckPath(path) == nil {
			for index := start; index < end; index++ {
				redacted[index] = ' '
			}
		}
		start = end
	}
	return string(redacted)
}

func isModuleReferenceStart(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}

func isModuleReferenceByte(character byte) bool {
	return isModuleReferenceStart(character) || strings.ContainsRune("-._~+/@", rune(character))
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
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' || module.CheckImportPath(value) != nil {
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
