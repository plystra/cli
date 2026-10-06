// Package newproject creates a validated Plystra Go Module in an atomic stage.
package newproject

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/agentguidance"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfacecompatibility"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/runtimebaseline"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/cli/internal/version"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const maximumGoEnvironmentValueBytes = 64 << 10

var (
	// ErrCreate reports a project creation failure.
	ErrCreate = errors.New("create Plystra project")
	// ErrInvalidProjectName reports a Project directory name that is not one
	// safe canonical child component.
	ErrInvalidProjectName = errors.New("invalid Plystra project name")
	// ErrInvalidModulePath reports a Go Module identity that cannot be used by
	// the created Project.
	ErrInvalidModulePath = errors.New("invalid Plystra project module path")
	// ErrInvalidTemplateQuery reports a malformed Go Module query supplied to
	// the Project creation command.
	ErrInvalidTemplateQuery = errors.New("invalid Plystra project template query")
	// ErrInvalidPluginName reports a malformed or reserved initial Plugin name.
	ErrInvalidPluginName = errors.New("invalid initial Plystra plugin name")
	// ErrInvalidPluginID reports inputs that cannot derive a canonical initial
	// Plugin identity.
	ErrInvalidPluginID = errors.New("invalid initial Plystra plugin ID")
	// ErrTargetExists reports a Project target that cannot be replaced.
	ErrTargetExists = errors.New("plystra project target already exists")
	// ErrGitInitialization reports a failed requested Git repository setup.
	ErrGitInitialization = errors.New("initialize Git repository")
	// ErrGitUnavailable reports that requested Git repository setup could not
	// start because the configured Git executable was unavailable.
	ErrGitUnavailable = errors.New("git executable unavailable")
	// ErrInvalidTemplate reports a resolved module that cannot serve as the
	// selected Plystra Project dependency.
	ErrInvalidTemplate = errors.New("invalid selected Plystra Project dependency")
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
	// NoAgentGuidance explicitly opts out of the default installed-release
	// guidance projection.
	NoAgentGuidance bool
	GoCommand       string
	NPMCommand      string
	GitCommand      string
	Environment     []string
}

// Result identifies a successfully committed project.
type Result struct {
	modulePath string
	directory  string
	path       string
}

// ModulePath returns the generated Go Module path.
func (r Result) ModulePath() string { return r.modulePath }

// Directory returns the relative child directory created below the requested
// parent.
func (r Result) Directory() string { return r.directory }

// Path returns the absolute committed project directory.
func (r Result) Path() string { return r.path }

// Create stages, validates, and atomically commits a new Plystra Go Module.
func Create(ctx context.Context, options Options) (Result, error) {
	if !validProjectName(options.ProjectName) {
		return Result{}, fmt.Errorf("%w: %w: project name %q must be one lower-case ASCII kebab-case child directory", ErrCreate, ErrInvalidProjectName, options.ProjectName)
	}
	modulePath := options.ModulePath
	if modulePath == "" {
		modulePath = options.ProjectName
		if err := modulepath.CheckProject(modulePath); err != nil {
			return Result{}, fmt.Errorf("%w: %w: project name %q cannot be used as the initial Go Module path: %v", ErrCreate, ErrInvalidModulePath, options.ProjectName, err)
		}
	} else if err := module.CheckPath(modulePath); err != nil {
		return Result{}, fmt.Errorf("%w: %w: invalid explicit Go Module path %q: %v", ErrCreate, ErrInvalidModulePath, modulePath, err)
	}
	templateQuery := ""
	templateModulePath := ""
	if options.Template != "" {
		var err error
		templateQuery, templateModulePath, err = moduleargument.ParseQuery(options.Template)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w: %v", ErrCreate, ErrInvalidTemplateQuery, err)
		}
	}
	if options.Plugin != "" {
		if _, err := plugincreate.DeriveID(modulePath, options.Plugin); err != nil {
			switch {
			case errors.Is(err, plugincreate.ErrInvalidName):
				return Result{}, fmt.Errorf("%w: %w: %w", ErrCreate, ErrInvalidPluginName, err)
			case errors.Is(err, plugincreate.ErrDeriveID):
				return Result{}, fmt.Errorf("%w: %w: %w", ErrCreate, ErrInvalidPluginID, err)
			default:
				return Result{}, fmt.Errorf("%w: initial plugin: %w", ErrCreate, err)
			}
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
		if err := populate(ctx, stagingRoot, modulePath, options.ProjectName, options.GitHubCI, !options.NoAgentGuidance); err != nil {
			return err
		}
		for _, arguments := range [][]string{{"mod", "download"}, {"mod", "tidy"}} {
			if err := gocommand.Run(ctx, gocommand.Options{Command: goCommand, Directory: stagingRoot, Environment: environment}, arguments...); err != nil {
				return err
			}
		}
		if options.Plugin == "" && templateQuery == "" {
			if _, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
				Start:       stagingRoot,
				GoCommand:   goCommand,
				Environment: environment,
			}); err != nil {
				return fmt.Errorf("generate resolved initial Project: %w", err)
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
		} else if templateQuery == "" {
			if err := gocommand.Run(ctx, gocommand.Options{Command: goCommand, Directory: stagingRoot, Environment: environment}, "test", "./..."); err != nil {
				return err
			}
		}
		if templateQuery != "" {
			if err := installTemplateDependency(ctx, stagingRoot, templateQuery, templateModulePath, goCommand, environment); err != nil {
				return err
			}
			if err := gocommand.Run(ctx, gocommand.Options{Command: goCommand, Directory: stagingRoot, Environment: environment}, "test", "./..."); err != nil {
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
		return verifyScaffoldOptions(stagingRoot, modulePath, options.Git, options.GitHubCI, !options.NoAgentGuidance)
	})
	if err != nil {
		if errors.Is(err, atomicfs.ErrTargetExists) {
			return Result{}, fmt.Errorf("%w: %w: %w", ErrCreate, ErrTargetExists, err)
		}
		return Result{}, fmt.Errorf("%w: %w", ErrCreate, err)
	}
	return Result{modulePath: modulePath, directory: options.ProjectName, path: target}, nil
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
			return fmt.Errorf("%w: inspect resolved dependency %q: %w", ErrInvalidTemplate, query, err)
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
		if _, err := applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start:            root,
			GoCommand:        goCommand,
			Environment:      environment,
			MutateModule:     mutate,
			RejectUnexpected: true,
		}); err != nil {
			return fmt.Errorf("generate Project after dependency %q: %w", query, err)
		}
		return nil
	})
}

func populate(ctx context.Context, root, modulePath, name string, githubCI, agentGuidance bool) error {
	currentManifest, err := applicationmeta.Parse([]byte(plystraTemplate))
	if err != nil {
		return fmt.Errorf("parse initial Project configuration: %w", err)
	}
	schemaLookup := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	composition, err := applicationmeta.Compose(nil, currentManifest, schemaLookup)
	if err != nil {
		return fmt.Errorf("compose initial Project configuration: %w", err)
	}
	configurationDigest, err := applicationmeta.ConfigurationLayerDigest(currentManifest, schemaLookup)
	if err != nil {
		return fmt.Errorf("digest initial Project configuration: %w", err)
	}
	input, err := applicationinput.Build(currentManifest, plugininventory.Index{}, applicationinput.SourceContext{CurrentModulePath: modulePath}, &generation.ConfigurationProvenanceInput{
		Mode:           generation.ConfigurationModeDefault,
		RootPath:       "plystra.yaml",
		RootDigest:     configurationDigest,
		SelectedPath:   "plystra.yaml",
		SelectedDigest: configurationDigest,
	}, generationexec.BuildOptions{})
	if err != nil {
		return fmt.Errorf("build initial application model: %w", err)
	}
	resolution, err := generationresolution.ResolveExtensions(ctx, input)
	if err != nil {
		return fmt.Errorf("resolve initial application model: %w", err)
	}
	protobufProjection, err := applicationgen.ProtobufProjection(currentManifest.HTTPTransports(), resolution)
	if err != nil {
		return fmt.Errorf("build initial Protobuf projection: %w", err)
	}
	interfaceProtobufModel, err := protobufmodel.BuildInterfaces(currentManifest.HTTPTransports().Connect, nil)
	if err != nil {
		return fmt.Errorf("build initial Interface Protobuf projection: %w", err)
	}
	wireMap, err := protobufwiremap.Build(protobufProjection, interfaceProtobufModel, nil, false, "")
	if err != nil {
		return fmt.Errorf("build initial Protobuf wire map: %w", err)
	}
	var httpCORS *applicationmeta.HTTPCORS
	if selected, exists := currentManifest.HTTPCORS(); exists {
		httpCORS = &selected
	}
	modelDigest, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:             modulePath,
		KernelModuleVersion:    version.KernelVersion,
		HTTPTransports:         currentManifest.HTTPTransports(),
		HTTPCORS:               httpCORS,
		Resolution:             resolution,
		ProtobufWireMap:        wireMap,
		InterfaceProtobufModel: interfaceProtobufModel,
	})
	if err != nil {
		return fmt.Errorf("digest initial application model: %w", err)
	}
	interfaceBaseline, err := interfacecompatibility.New(nil)
	if err != nil {
		return fmt.Errorf("construct initial Interface compatibility baseline: %w", err)
	}
	metadataBaseline, err := interfacecompatibility.NewMetadata(nil)
	if err != nil {
		return fmt.Errorf("construct initial Interface metadata compatibility baseline: %w", err)
	}
	descriptorEvidence, err := protobufdescriptor.BuildWithInterfaces(protobufProjection, wireMap, interfaceProtobufModel)
	if err != nil {
		return fmt.Errorf("build initial Protobuf descriptor evidence: %w", err)
	}
	transportBaseline, err := interfacecompatibility.BuildTransport(wireMap, descriptorEvidence)
	if err != nil {
		return fmt.Errorf("construct initial Interface transport compatibility baseline: %w", err)
	}
	javaScriptModel, err := applicationgen.JavaScriptModel(resolution)
	if err != nil {
		return fmt.Errorf("construct initial JavaScript SDK model: %w", err)
	}
	javaScriptAPI, err := javascriptgen.BuildPublicAPI("", javaScriptModel, interfaceProtobufModel)
	if err != nil {
		return fmt.Errorf("construct initial JavaScript public API: %w", err)
	}
	javaScriptBaseline, err := interfacecompatibility.NewJavaScript(javaScriptAPI)
	if err != nil {
		return fmt.Errorf("construct initial Interface JavaScript compatibility baseline: %w", err)
	}
	interfaceProvenance, err := interfaceprovenance.New(interfaceprovenance.Input{})
	if err != nil {
		return fmt.Errorf("construct initial Interface provenance: %w", err)
	}
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		return fmt.Errorf("identify embedded transport toolchain: %w", err)
	}
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   applicationgen.ConfigurationModeDefault,
		RootPath:               "plystra.yaml",
		RootDigest:             configurationDigest,
		SelectedPath:           "plystra.yaml",
		SelectedDigest:         configurationDigest,
		Composition:            composition,
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: modelDigest,
		InterfaceProvenance:    interfaceProvenance,
		TransportToolchain:     toolchain,
	})
	if err != nil {
		return fmt.Errorf("construct initial application manifest provenance: %w", err)
	}
	generated, err := applicationgen.Render(applicationgen.Options{
		ModulePath:             modulePath,
		KernelModuleVersion:    version.KernelVersion,
		HTTPTransports:         currentManifest.HTTPTransports(),
		HTTPCORS:               httpCORS,
		Composition:            composition,
		ManifestProvenance:     provenance,
		InterfaceCompatibility: interfaceBaseline,
		InterfaceMetadata:      metadataBaseline,
		InterfaceTransport:     transportBaseline,
		InterfaceJavaScript:    javaScriptBaseline,
		ProtobufWireMap:        wireMap,
		InterfaceProtobufModel: interfaceProtobufModel,
	}, resolution)
	if err != nil {
		return fmt.Errorf("render initial generated output: %w", err)
	}
	readme := fmt.Sprintf(readmeTemplate, name, modulePath)
	if githubCI {
		readme += githubCIReadmeTemplate
	}
	if agentGuidance {
		readme += agentGuidanceReadmeTemplate
	}
	type projectFile struct {
		path string
		data []byte
	}
	files := []projectFile{
		{path: "go.mod", data: fmt.Appendf(nil, goModuleTemplate, modulePath, version.KernelVersion, bootstrapgen.YAMLModuleVersion, modulepath.RuntimeModuleVersion, runtimebaseline.PermissionsVersion)},
		{path: "README.md", data: []byte(readme)},
		{path: ".gitignore", data: []byte(gitignoreTemplate)},
		{path: ".gitattributes", data: []byte(gitattributesTemplate)},
	}
	if githubCI {
		files = append(files, projectFile{path: ".github/workflows/ci.yml", data: []byte(ciTemplate)})
	}
	if agentGuidance {
		guidance, err := agentguidance.Render(modulePath)
		if err != nil {
			return err
		}
		for _, file := range guidance.Files() {
			files = append(files, projectFile{path: file.Path(), data: file.Data()})
		}
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
	writes, err := runtimebaseline.Writes(root, generated.RuntimeBaseline())
	if err != nil {
		return err
	}
	return atomicfs.WriteFiles(root, writes, func(string) error { return nil })
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
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return gitUnavailableError{cause: err}
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

type gitUnavailableError struct {
	cause error
}

func (gitUnavailableError) Error() string {
	return ErrGitInitialization.Error() + ": git init failed"
}

func (e gitUnavailableError) Unwrap() []error {
	return []error{ErrGitInitialization, ErrGitUnavailable, e.cause}
}

func verifyScaffoldOptions(root, modulePath string, git, githubCI, agentGuidance bool) error {
	if err := verifyChoicePath(root, ".git", git, true); err != nil {
		return err
	}
	if err := verifyChoicePath(root, ".github/workflows/ci.yml", githubCI, false); err != nil {
		return err
	}
	return verifyAgentGuidance(root, modulePath, agentGuidance)
}

func verifyAgentGuidance(root, modulePath string, expected bool) error {
	if !expected {
		return verifyChoicePath(root, agentguidance.Root, false, true)
	}
	projection, err := agentguidance.Render(modulePath)
	if err != nil {
		return err
	}
	for _, file := range projection.Files() {
		fullPath := filepath.Join(root, filepath.FromSlash(file.Path()))
		info, err := os.Lstat(fullPath)
		if err != nil {
			return fmt.Errorf("inspect generated Agent guidance path %s: %w", file.Path(), err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated Agent guidance path %s is not a regular file", file.Path())
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("read generated Agent guidance path %s: %w", file.Path(), err)
		}
		if !bytes.Equal(data, file.Data()) {
			return fmt.Errorf("generated Agent guidance path %s differs from the installed catalog", file.Path())
		}
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
	foundYAML := false
	foundModulePaths := false
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == "github.com/plystra/kernel" && requirement.Mod.Version == version.KernelVersion && !requirement.Indirect {
			foundKernel = true
		}
		if requirement.Mod.Path == bootstrapgen.YAMLModulePath && requirement.Mod.Version == bootstrapgen.YAMLModuleVersion && !requirement.Indirect {
			foundYAML = true
		}
		if requirement.Mod.Path == modulepath.RuntimeModulePath && semver.Compare(requirement.Mod.Version, modulepath.RuntimeModuleVersion) >= 0 && !requirement.Indirect {
			foundModulePaths = true
		}
	}
	if !foundKernel {
		return fmt.Errorf("generated go.mod does not require github.com/plystra/kernel %s", version.KernelVersion)
	}
	if !foundYAML {
		return fmt.Errorf("generated go.mod does not require %s %s", bootstrapgen.YAMLModulePath, bootstrapgen.YAMLModuleVersion)
	}
	if !foundModulePaths {
		return fmt.Errorf("generated go.mod does not directly require %s at %s or newer", modulepath.RuntimeModulePath, modulepath.RuntimeModuleVersion)
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
