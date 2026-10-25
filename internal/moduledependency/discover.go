// Package moduledependency discovers the complete effective Go Module graph
// and recognizes dependency Plystra Projects without mutating module state.
package moduledependency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const maximumGoModSize = 1 << 20

const defaultOutputLimit = 16 << 20

var (
	// ErrDiscover reports that dependency-module discovery failed.
	ErrDiscover = errors.New("discover dependency modules")
	// ErrInvalidGoMod reports invalid or inconsistent application dependency
	// requirements.
	ErrInvalidGoMod = errors.New("invalid dependency go.mod")
	// ErrInvalidOutput reports inconsistent or unsafe go list module output.
	ErrInvalidOutput = errors.New("invalid go list module output")
	// ErrModuleUnavailable reports an explicit module whose source is not
	// available for read-only inspection.
	ErrModuleUnavailable = errors.New("dependency module source unavailable")
	// ErrConcurrentChange reports dependency state that changed during
	// discovery.
	ErrConcurrentChange = errors.New("dependency module state changed during discovery")
	// ErrSourceIntegrity reports unavailable or inconsistent archive checksum evidence.
	ErrSourceIntegrity = errors.New("dependency source integrity unavailable")
)

// Options controls the read-only Go command used for module discovery.
type Options struct {
	GoCommand   string
	Environment []string
	OutputLimit int
	// Offline requires the selected module sources to be present locally and
	// prevents Go from contacting a module proxy or checksum service.
	Offline bool
	// RequireSourceChecksums obtains both archive and manifest checksums for
	// every versioned source, using Project go.sum or ordinary Go download
	// verification in an isolated module. It never hashes the extracted tree.
	// Workspace discovery always normalizes complete checksum fields under
	// workspace authority; this flag additionally requires recorded full sums
	// when offline.
	RequireSourceChecksums bool
	// RetainRootManifests captures existing checksummed root manifests before
	// graph resolution, and requested effective manifests before preparation
	// downloads or workspace selection. Discovery and exact preparation also
	// capture present manifests around each download attempt under original authority.
	// It does not initiate downloads or refresh previously retained evidence.
	// Callbacks must use the supplied context, including bounded post-download
	// retention after the operation context has been cancelled.
	RetainRootManifests func(context.Context, []RootManifest) error
	// RetainManifestFiles preserves present cache .mod files as private concurrency
	// evidence when all-version replacement proofs fix their physical source before
	// discovery or workspace preparation selection. It is independent of source-tree
	// availability and original checksums, and supplies no graph-reading authority.
	RetainManifestFiles func(context.Context, []string) error
	// RetainModuleInformation captures present cache .info files before and
	// after exact preparation downloads, including failed downloads, and
	// before workspace selection for inferable requested effective versions.
	// A completely inferred graph also retains selected effective information
	// before graph listing, independently of manifest Go-version shortcuts.
	// Incomplete discovery graphs retain fixed-root effective information when
	// all-version replacement proofs establish the same physical source.
	// Information retention does not require a source-tree callback or directory.
	// These files have no independent Go checksum authority.
	// Callbacks honor the supplied context, including post-cancellation retention.
	RetainModuleInformation func(context.Context, []string) error
	// RetainSourceTrees preserves existing extracted trees before their archive
	// checksum is independently available. It records private concurrency evidence
	// only; consumers must authenticate the same retained trees against complete
	// resolved checksums before accepting them. Partial extractions are excluded.
	// Complete checked selection proofs retain these trees before graph listing
	// or workspace preparation selection, without requiring archive authority.
	// When graph inference is incomplete, raw root paths whose wildcard and exact
	// replacements fix one versioned source retain pending trees before listing
	// without predicting the logical selected version.
	RetainSourceTrees func(context.Context, []string) error
	// RetainSelectedSources captures warm and newly acquired source bundles before
	// extracted-manifest validation. A completely inferred selected graph is
	// captured before listing; otherwise actual Go-selected sources are captured
	// after listing, before the CLI reads their extracted manifests.
	// Warm trees require an independent original archive checksum. Their cache
	// metadata may be incomplete; callbacks retain available authorized metadata
	// independently and must not require missing files before acquisition.
	// Missing sources are acquired under the original checksum authorities and
	// retained before CLI validation or the next acquisition. Their extracted
	// manifests have not been parsed or recognized as Projects.
	RetainSelectedSources func([]Module) error
}

// Replacement records the selected replacement provenance reported by Go.
type Replacement struct {
	path    string
	version string
}

// Path returns the replacement module path or local filesystem path exactly as
// reported by Go.
func (r Replacement) Path() string { return r.path }

// Version returns the replacement module version. It is empty for a local
// filesystem replacement.
func (r Replacement) Version() string { return r.version }

// Local reports whether the replacement is a local filesystem source.
func (r Replacement) Local() bool { return r.path != "" && r.version == "" }

// Module is one dependency in the effective graph resolved to its selected
// read-only source root.
type Module struct {
	path            string
	requiredVersion string
	selectedVersion string
	root            string
	sourceDirectory string
	sourcePath      string
	checksum        string
	goModChecksum   string
	goModFile       string
	direct          bool
	indirect        bool
	workspace       bool
	project         bool
	goMod           []byte
	replacement     Replacement

	incompleteSourceMetadata bool
}

// Path returns the required module path.
func (m Module) Path() string { return m.path }

// RequiredVersion returns the version written in the current Project's
// go.mod, or empty for a transitive or workspace-only dependency.
func (m Module) RequiredVersion() string { return m.requiredVersion }

// SelectedVersion returns the version selected by Go. It is empty for a
// module supplied directly by the active go.work workspace.
func (m Module) SelectedVersion() string { return m.selectedVersion }

// Checksum returns Go's reported archive checksum for the effective source,
// including a versioned replacement. RequireSourceChecksums additionally binds
// it to Project go.sum or an isolated Go download. It may otherwise be absent;
// workspace and local-replacement sources have no archive checksum.
func (m Module) Checksum() string { return m.checksum }

// GoModChecksum returns Go's checksum for the effective source's go.mod file.
// RequireSourceChecksums obtains missing evidence through an isolated download.
// Otherwise it is empty when Go does not report one for the source.
func (m Module) GoModChecksum() string { return m.goModChecksum }

// GoModFile returns the exact module manifest path reported by Go or inferred
// from an exact effective identity for an interim source-retention callback.
// Versioned modules use a cache manifest independently of their source tree.
func (m Module) GoModFile() string { return m.goModFile }

// IncompleteSourceMetadata reports an interim source-retention callback whose
// archive-authorized tree lacks complete original cache-manifest authority or
// metadata files. Retain available authorized metadata independently. Completed
// acquisition callbacks and final snapshots require complete source evidence.
func (m Module) IncompleteSourceMetadata() bool { return m.incompleteSourceMetadata }

// Root returns the canonical absolute source root. This CLI-only path is not
// generation-extension input or generated manifest data.
func (m Module) Root() string { return m.root }

// SourceDirectory returns the source directory spelling selected by Go before
// canonicalization. It preserves symbolic path provenance for private snapshots.
func (m Module) SourceDirectory() string { return m.sourceDirectory }

// Direct reports whether the current Project's go.mod explicitly requires the
// module. It does not grant resolution or composition precedence.
func (m Module) Direct() bool { return m.direct }

// Indirect reports whether a direct go.mod requirement carries the indirect
// marker. It is false for transitive and workspace-only dependencies.
func (m Module) Indirect() bool { return m.indirect }

// Workspace reports whether the selected source is an active go.work module.
func (m Module) Workspace() bool { return m.workspace }

// Project reports whether the selected module root contains a regular
// non-symbolic plystra.yaml and is therefore a dependency Plystra Project.
func (m Module) Project() bool { return m.project }

// ProjectGoMod returns the exact bounded go.mod snapshot validated for a
// dependency Plystra Project. It returns nil for an ordinary Go dependency,
// and the returned bytes are defensive.
func (m Module) ProjectGoMod() []byte { return append([]byte(nil), m.goMod...) }

// Replacement returns replacement provenance when Go selected one.
func (m Module) Replacement() (Replacement, bool) {
	return m.replacement, m.replacement.path != ""
}

// Index is an immutable deterministic collection of effective dependency
// modules.
type Index struct {
	modules []Module
}

// Modules returns a defensive copy sorted by module path.
func (i Index) Modules() []Module {
	return append([]Module(nil), i.modules...)
}

// Projects returns every direct and transitive dependency Plystra Project in
// module-path order. Ordinary graph modules remain available through Modules
// and ByPath but are not scanned as Plystra sources.
func (i Index) Projects() []Module {
	projects := make([]Module, 0, len(i.modules))
	for _, dependency := range i.modules {
		if dependency.project {
			projects = append(projects, dependency)
		}
	}
	return projects
}

// ByPath returns one exact dependency module from the effective graph.
func (i Index) ByPath(modulePath string) (Module, bool) {
	for _, dependency := range i.modules {
		if dependency.path == modulePath {
			return dependency, true
		}
	}
	return Module{}, false
}

// Discover asks standard Go tooling for the complete effective module graph,
// validates every selected source root, and recognizes dependency Projects by
// root plystra.yaml. It writes neither Project files nor dependency sources.
func Discover(ctx context.Context, application modulelocate.Module, options Options) (result Index, resultErr error) {
	if application.Path() == "" || application.ModulePath() == "" {
		return Index{}, fmt.Errorf("%w: application module is empty", ErrDiscover)
	}
	goModPath := filepath.Join(application.Path(), "go.mod")
	before, err := readGoMod(goModPath)
	if err != nil {
		cause := fmt.Errorf("%w: %w", ErrInvalidGoMod, err)
		if errors.Is(err, ErrConcurrentChange) {
			cause = goModSourceError(application.ModulePath(), 0, 0, cause)
		}
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, cause)
	}
	requirements, err := parseRequirements(before.data, application.ModulePath())
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}

	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = defaultOutputLimit
	}
	environment := options.Environment
	if options.Offline {
		environment = environmentWith(environment, "GOPROXY", "off")
		environment = environmentWith(environment, "GONOPROXY", "none")
		environment = environmentWith(environment, "GOSUMDB", "off")
		environment = environmentWith(environment, "GOTOOLCHAIN", "local")
	}
	inputs := preparationInputs{goModPath: before}
	options.Environment = environment
	workspace, err := capturePreparationWorkspace(ctx, application.Path(), options, inputs)
	if err != nil {
		return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
	}
	var sourceChecksums preparationChecksums
	if options.RetainSelectedSources != nil || options.RetainSourceTrees != nil {
		sourceChecksums, err = originalSourceChecksums(application.Path(), workspace, inputs)
		if err != nil {
			return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
		}
		if options.RetainSelectedSources != nil {
			options.RetainSelectedSources = retainSelectedSourcesOnce(options.RetainSelectedSources)
		}
	}
	if options.RetainModuleInformation != nil {
		options.RetainModuleInformation = retainModuleFilesOnce(inputs, options.RetainModuleInformation)
	}
	if options.RetainManifestFiles != nil {
		options.RetainManifestFiles = retainModuleFilesOnce(inputs, options.RetainManifestFiles)
	}
	if options.RetainRootManifests != nil || options.RetainManifestFiles != nil || options.RetainModuleInformation != nil || options.RetainSelectedSources != nil || options.RetainSourceTrees != nil {
		defer func() {
			if err := inputs.recheck(); err != nil {
				result, resultErr = Index{}, errors.Join(resultErr, ErrDiscover, ErrSourceIntegrity, err)
			}
		}()
		if err := retainRootManifests(ctx, application.Path(), before, options, inputs, workspace); err != nil {
			return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
		}
	}
	if workspace != nil {
		root, err := os.MkdirTemp("", "plystra-workspace-discovery-")
		if err != nil {
			return Index{}, errors.Join(ErrDiscover, err)
		}
		defer func() {
			if err := errors.Join(inputs.recheck(), os.RemoveAll(root)); err != nil {
				result, resultErr = Index{}, errors.Join(resultErr, ErrDiscover, err)
			}
		}()
		work, err := workspace.writeDiscovery(root)
		if err != nil {
			return Index{}, errors.Join(ErrDiscover, err)
		}
		environment = environmentWith(environment, "GOWORK", work)
	}
	options.Environment = environment
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   application.Path(),
		Environment: environment,
		OutputLimit: outputLimit,
	}, "list", "-m", "-json", "-mod=readonly", "all")
	if err != nil {
		return Index{}, fmt.Errorf("%w: resolve effective module graph: %w", ErrDiscover, errors.Join(err, ctx.Err(), inputs.recheck()))
	}
	after, err := readGoMod(goModPath)
	if err != nil || !sameSnapshot(before, after) {
		if err == nil {
			err = ErrConcurrentChange
		}
		return Index{}, fmt.Errorf(
			"%w: %w",
			ErrDiscover,
			goModSourceError(
				application.ModulePath(),
				0,
				0,
				fmt.Errorf("%w: application go.mod changed: %v", ErrConcurrentChange, err),
			),
		)
	}
	if err := errors.Join(ctx.Err(), inputs.recheck()); err != nil {
		return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
	}

	modules, err := decodeModules(output, application, requirements)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	if options.RetainSelectedSources != nil || options.RetainSourceTrees != nil {
		if err := retainListedSources(ctx, application.Path(), modules, sourceChecksums, options, inputs, options.RetainSelectedSources); err != nil {
			return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
		}
	}
	for index := range modules {
		dependency := &modules[index]
		if dependency.root != "" || dependency.sourceDirectory == "" || dependency.goModFile == "" {
			continue
		}
		dependency.root, dependency.goMod, err = validateModuleRoot(dependency.path, dependency.sourceDirectory, dependency.goModFile, dependency.sourcePath, false)
		if err != nil {
			return Index{}, errors.Join(ErrDiscover, err)
		}
	}
	if err := resolveMissingSources(ctx, application.Path(), application.ModulePath(), before, modules, options, outputLimit, inputs, workspace); err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	if options.RequireSourceChecksums || workspace != nil {
		options.Environment = environment
		if err := resolveSourceChecksums(ctx, application.Path(), before, modules, options, outputLimit, workspace); err != nil {
			return Index{}, errors.Join(ErrDiscover, ErrSourceIntegrity, err)
		}
	}
	for index := range modules {
		project, err := projectlocate.Recognize(modules[index].root, modules[index].path)
		if err != nil {
			return Index{}, fmt.Errorf("%w: inspect dependency Project marker for %q: %w", ErrDiscover, modules[index].path, err)
		}
		modules[index].project = project
		if !project {
			modules[index].goMod = nil
		}
	}
	return Index{modules: modules}, nil
}

type requirement struct {
	path     string
	version  string
	indirect bool
}

func parseRequirements(data []byte, applicationPath string) ([]requirement, error) {
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidGoMod, err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != applicationPath {
		cause := fmt.Errorf("%w: module directive changed from %q", ErrInvalidGoMod, applicationPath)
		if parsed.Module == nil {
			return nil, cause
		}
		return nil, goModSourceError(
			applicationPath,
			parsed.Module.Syntax.Start.Line,
			parsed.Module.Syntax.Start.LineRune,
			cause,
		)
	}
	seen := make(map[string]struct{}, len(parsed.Require))
	requirements := make([]requirement, 0, len(parsed.Require))
	for _, declared := range parsed.Require {
		if err := module.Check(declared.Mod.Path, declared.Mod.Version); err != nil {
			return nil, goModSourceError(
				applicationPath,
				declared.Syntax.Start.Line,
				declared.Syntax.Start.LineRune,
				fmt.Errorf("%w: requirement %s@%s: %v", ErrInvalidGoMod, declared.Mod.Path, declared.Mod.Version, err),
			)
		}
		if declared.Mod.Path == applicationPath {
			return nil, goModSourceError(
				applicationPath,
				declared.Syntax.Start.Line,
				declared.Syntax.Start.LineRune,
				fmt.Errorf("%w: application module cannot require itself", ErrInvalidGoMod),
			)
		}
		if _, duplicate := seen[declared.Mod.Path]; duplicate {
			return nil, goModSourceError(
				applicationPath,
				declared.Syntax.Start.Line,
				declared.Syntax.Start.LineRune,
				fmt.Errorf("%w: duplicate requirement %q", ErrInvalidGoMod, declared.Mod.Path),
			)
		}
		seen[declared.Mod.Path] = struct{}{}
		requirements = append(requirements, requirement{
			path:     declared.Mod.Path,
			version:  declared.Mod.Version,
			indirect: declared.Indirect,
		})
	}
	sort.Slice(requirements, func(left, right int) bool {
		return requirements[left].path < requirements[right].path
	})
	return requirements, nil
}

type listedModule struct {
	Path     string             `json:"Path"`
	Version  string             `json:"Version"`
	Main     bool               `json:"Main"`
	Dir      string             `json:"Dir"`
	GoMod    string             `json:"GoMod"`
	Checksum string             `json:"Sum"`
	GoModSum string             `json:"GoModSum"`
	Replace  *listedReplacement `json:"Replace"`
	Error    *listedError       `json:"Error"`
}

type listedReplacement struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Dir      string `json:"Dir"`
	GoMod    string `json:"GoMod"`
	Checksum string `json:"Sum"`
	GoModSum string `json:"GoModSum"`
}

type listedError struct {
	Err string `json:"Err"`
}

func decodeModules(data []byte, application modulelocate.Module, requirements []requirement) ([]Module, error) {
	direct := make(map[string]requirement, len(requirements))
	for _, declared := range requirements {
		direct[declared.path] = declared
	}
	resolved := make(map[string]Module)
	seen := make(map[string]struct{})
	applicationFound := false
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var listed listedModule
		err := decoder.Decode(&listed)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: decode JSON: %v", ErrInvalidOutput, err)
		}
		var pathErr error
		if listed.Main {
			pathErr = modulepath.CheckProject(listed.Path)
		} else {
			pathErr = module.CheckPath(listed.Path)
		}
		if pathErr != nil {
			return nil, fmt.Errorf("%w: Go returned invalid module path %q: %v", ErrInvalidOutput, listed.Path, pathErr)
		}
		if _, duplicate := seen[listed.Path]; duplicate {
			return nil, fmt.Errorf("%w: Go returned module %q more than once", ErrInvalidOutput, listed.Path)
		}
		seen[listed.Path] = struct{}{}
		if listed.Error != nil {
			return nil, fmt.Errorf("%w: Go reported an error for module %q", ErrInvalidOutput, listed.Path)
		}

		declared, isDirect := direct[listed.Path]
		if listed.Main {
			if listed.Version != "" || listed.Replace != nil {
				return nil, fmt.Errorf("%w: workspace module %q has version or replacement provenance", ErrInvalidOutput, listed.Path)
			}
		} else {
			if listed.Version == "" {
				return nil, fmt.Errorf("%w: selected module %q has no version", ErrInvalidOutput, listed.Path)
			}
			if err := module.Check(listed.Path, listed.Version); err != nil {
				return nil, fmt.Errorf("%w: selected module %s@%s: %v", ErrInvalidOutput, listed.Path, listed.Version, err)
			}
			if isDirect && semver.Compare(listed.Version, declared.version) < 0 {
				return nil, fmt.Errorf("%w: selected module %s@%s is older than required %s", ErrInvalidOutput, listed.Path, listed.Version, declared.version)
			}
		}

		directory := listed.Dir
		goModPath := listed.GoMod
		checksum, goModChecksum := listed.Checksum, listed.GoModSum
		var replacement Replacement
		expectedSourcePath := listed.Path
		if listed.Replace != nil {
			if listed.Main || listed.Replace.Path == "" || strings.ContainsRune(listed.Replace.Path, 0) {
				return nil, fmt.Errorf("%w: module %q has invalid replacement provenance", ErrInvalidOutput, listed.Path)
			}
			if listed.Replace.Version != "" {
				if err := module.Check(listed.Replace.Path, listed.Replace.Version); err != nil {
					return nil, fmt.Errorf("%w: replacement %s@%s for %q: %v", ErrInvalidOutput, listed.Replace.Path, listed.Replace.Version, listed.Path, err)
				}
				expectedSourcePath = listed.Replace.Path
			} else {
				expectedSourcePath = ""
			}
			if directory == "" {
				directory = listed.Replace.Dir
			}
			if goModPath == "" {
				goModPath = listed.Replace.GoMod
			}
			replacement = Replacement{path: listed.Replace.Path, version: listed.Replace.Version}
			checksum, goModChecksum = listed.Replace.Checksum, listed.Replace.GoModSum
		}
		root := ""
		var goModData []byte
		if !listed.Main && (listed.Replace == nil || listed.Replace.Version != "") {
			// Decode the complete selection before retaining or reading any
			// versioned tree. Missing sources are acquired separately afterward.
			if directory != "" && !filepath.IsAbs(directory) || goModPath != "" && !filepath.IsAbs(goModPath) {
				return nil, fmt.Errorf("%w: module %q returned non-absolute source provenance", ErrInvalidOutput, listed.Path)
			}
		} else {
			var err error
			root, goModData, err = validateModuleRoot(listed.Path, directory, goModPath, expectedSourcePath, listed.Main)
			if err != nil {
				return nil, err
			}
		}
		if listed.Path == application.ModulePath() {
			if !listed.Main || isDirect || !sameDirectory(root, application.Path()) {
				return nil, fmt.Errorf("%w: current module %q has inconsistent main-module provenance", ErrInvalidOutput, listed.Path)
			}
			applicationFound = true
			continue
		}
		resolved[listed.Path] = Module{
			path:            listed.Path,
			requiredVersion: declared.version,
			selectedVersion: listed.Version,
			root:            root,
			sourceDirectory: directory,
			sourcePath:      expectedSourcePath,
			checksum:        checksum,
			goModChecksum:   goModChecksum,
			goModFile:       goModPath,
			direct:          isDirect,
			indirect:        isDirect && declared.indirect,
			workspace:       listed.Main,
			goMod:           goModData,
			replacement:     replacement,
		}
	}
	if !applicationFound {
		return nil, fmt.Errorf("%w: Go omitted current module %q", ErrInvalidOutput, application.ModulePath())
	}
	for _, declared := range requirements {
		if _, ok := resolved[declared.path]; !ok {
			return nil, fmt.Errorf("%w: Go omitted directly required module %q", ErrInvalidOutput, declared.path)
		}
	}
	modules := make([]Module, 0, len(resolved))
	for _, dependency := range resolved {
		modules = append(modules, dependency)
	}
	sort.Slice(modules, func(left, right int) bool { return modules[left].path < modules[right].path })
	return modules, nil
}

type downloadedModule struct {
	Path     string `json:"Path"`
	Version  string `json:"Version"`
	Dir      string `json:"Dir"`
	GoMod    string `json:"GoMod"`
	Checksum string `json:"Sum"`
	GoModSum string `json:"GoModSum"`
	Error    string `json:"Error"`
}

func resolveMissingSources(ctx context.Context, applicationRoot, applicationModulePath string, expectedGoMod fileSnapshot, modules []Module, options Options, outputLimit int, inputs preparationInputs, workspace *preparationWorkspace) (resultErr error) {
	indices := map[module.Version][]int{}
	var versions []module.Version
	for index, dependency := range modules {
		if dependency.root != "" {
			continue
		}
		version := sourceVersion(dependency)
		if version.Path == "" || version.Version == "" {
			return fmt.Errorf("%w: local source for module %q is unavailable", ErrModuleUnavailable, dependency.path)
		}
		if _, exists := indices[version]; !exists {
			versions = append(versions, version)
		}
		indices[version] = append(indices[version], index)
	}
	if len(versions) == 0 {
		return nil
	}
	if options.Offline {
		version := versions[0]
		return fmt.Errorf("%w: selected source %s for module %q is not available locally", ErrModuleUnavailable, version.String(), modules[indices[version][0]].path)
	}

	downloadRoot, err := os.MkdirTemp("", "plystra-module-download-")
	if err != nil {
		return fmt.Errorf("%w: create isolated download directory: %v", ErrModuleUnavailable, err)
	}
	temporaryRoot := downloadRoot
	defer func() { resultErr = errors.Join(resultErr, inputs.recheck(), os.RemoveAll(temporaryRoot)) }()

	environment := preparationEnvironment(options)
	if workspace == nil {
		sums, err := inputs.read(filepath.Join(applicationRoot, "go.sum"), true)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(downloadRoot, "go.mod"), []byte("module plystra-module-download\n"), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(downloadRoot, "go.sum"), sums.data, 0o600); err != nil {
			return err
		}
	} else {
		work, err := workspace.write(filepath.Join(downloadRoot, "checksums"), nil, false)
		if err != nil {
			return err
		}
		downloadRoot = work.members[0].root
		environment = environmentWith(environment, "GOWORK", work.path)
	}
	options.OutputLimit = outputLimit
	if callback := options.RetainSourceTrees; callback != nil {
		options.RetainSourceTrees = func(retainCtx context.Context, directories []string) error {
			return errors.Join(callback(retainCtx, directories), retainCtx.Err(), inputs.recheck())
		}
	}
	if callback := options.RetainModuleInformation; callback != nil {
		options.RetainModuleInformation = func(retainCtx context.Context, paths []string) error {
			return errors.Join(callback(retainCtx, paths), retainCtx.Err(), inputs.recheck())
		}
	}
	retain := func(sources []Module) error {
		var err error
		if callback := options.RetainSelectedSources; callback != nil {
			err = callback(sources)
		}
		return errors.Join(err, ctx.Err(), inputs.recheck())
	}
	var manifestChecksums map[module.Version]string
	if options.RetainRootManifests != nil {
		checksums, err := originalSourceChecksums(applicationRoot, workspace, inputs)
		if err != nil {
			return err
		}
		manifestChecksums = checksums.manifests
	}
	for _, version := range versions {
		group := make([]Module, len(indices[version]))
		for index, original := range indices[version] {
			group[index] = modules[original]
		}
		retainMetadata, err := preparationManifestRetainer(ctx, downloadRoot, []module.Version{version}, manifestChecksums, options, environment, inputs)
		if err != nil {
			return err
		}
		if err := downloadPreparedSources(ctx, downloadRoot, group, nil, options, environment, retain, retainMetadata); err != nil {
			return err
		}
		for index, original := range indices[version] {
			modules[original] = group[index]
		}
	}

	// Downloads run outside the Project directory. Recheck the Project module
	// file anyway so a concurrent dependency edit cannot be mistaken for the
	// graph whose sources were just inspected.
	currentGoMod, err := readGoMod(filepath.Join(applicationRoot, "go.mod"))
	if err != nil || !sameSnapshot(expectedGoMod, currentGoMod) {
		if err == nil {
			err = ErrConcurrentChange
		}
		return goModSourceError(
			applicationModulePath,
			0,
			0,
			fmt.Errorf("%w: application go.mod changed while resolving sources: %v", ErrConcurrentChange, err),
		)
	}
	return nil
}

func environmentWith(environment []string, key, value string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, key) {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func sameDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func validateModuleRoot(modulePath, directory, goModPath, expectedSourcePath string, project bool) (string, []byte, error) {
	if directory == "" || goModPath == "" {
		return "", nil, fmt.Errorf("%w: %w: module %q must be downloaded before generation", ErrInvalidOutput, ErrModuleUnavailable, modulePath)
	}
	if !filepath.IsAbs(directory) || !filepath.IsAbs(goModPath) {
		return "", nil, fmt.Errorf("%w: module %q returned non-absolute source provenance", ErrInvalidOutput, modulePath)
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(directory))
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w: resolve source for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, modulePath, err)
	}
	rootInfo, err := os.Lstat(canonicalRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("%w: %w: source for module %q is not a directory", ErrInvalidOutput, ErrModuleUnavailable, modulePath)
	}
	reportedInfo, err := os.Lstat(goModPath)
	if err != nil || !reportedInfo.Mode().IsRegular() || reportedInfo.Mode()&fs.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("%w: module %q reported invalid go.mod provenance", ErrInvalidOutput, modulePath)
	}
	manifestPath := goModPath
	rootGoMod := filepath.Join(canonicalRoot, "go.mod")
	rootInfo, rootErr := os.Lstat(rootGoMod)
	switch {
	case rootErr == nil && rootInfo.Mode().IsRegular() && rootInfo.Mode()&fs.ModeSymlink == 0:
		manifestPath = rootGoMod
	case rootErr == nil:
		return "", nil, fmt.Errorf("%w: module %q source has unsafe go.mod", ErrInvalidOutput, modulePath)
	case !errors.Is(rootErr, fs.ErrNotExist):
		return "", nil, fmt.Errorf("%w: inspect source go.mod for module %q: %v", ErrInvalidOutput, modulePath, rootErr)
	}
	snapshot, err := readGoMod(manifestPath)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %w: inspect go.mod for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, modulePath, err)
	}
	parsed, err := modfile.Parse("go.mod", snapshot.data, nil)
	if err != nil || parsed.Module == nil {
		return "", nil, fmt.Errorf("%w: module %q source has invalid go.mod", ErrInvalidOutput, modulePath)
	}
	var pathErr error
	if project {
		pathErr = modulepath.CheckProject(parsed.Module.Mod.Path)
	} else {
		pathErr = module.CheckPath(parsed.Module.Mod.Path)
	}
	if pathErr != nil {
		return "", nil, fmt.Errorf("%w: module %q source declares invalid path %q", ErrInvalidOutput, modulePath, parsed.Module.Mod.Path)
	}
	if expectedSourcePath != "" && parsed.Module.Mod.Path != expectedSourcePath {
		return "", nil, fmt.Errorf("%w: module %q source declares %q, expected %q", ErrInvalidOutput, modulePath, parsed.Module.Mod.Path, expectedSourcePath)
	}
	return canonicalRoot, append([]byte(nil), snapshot.data...), nil
}

type fileSnapshot struct {
	info       fs.FileInfo
	data       []byte
	parent     string
	parentInfo fs.FileInfo
}

func readGoMod(name string) (fileSnapshot, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return fileSnapshot{}, err
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return fileSnapshot{}, errors.New("go.mod is not a regular non-symbolic file")
	}
	if before.Size() > maximumGoModSize {
		return fileSnapshot{}, fmt.Errorf("go.mod exceeds %d bytes", maximumGoModSize)
	}
	file, err := os.Open(name)
	if err != nil {
		return fileSnapshot{}, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fileSnapshot{}, err
	}
	if !sameFile(before, opened) {
		_ = file.Close()
		return fileSnapshot{}, ErrConcurrentChange
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumGoModSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return fileSnapshot{}, readErr
	}
	if closeErr != nil {
		return fileSnapshot{}, closeErr
	}
	after, err := os.Lstat(name)
	if err != nil || !sameFile(opened, after) {
		return fileSnapshot{}, ErrConcurrentChange
	}
	if len(data) > maximumGoModSize {
		return fileSnapshot{}, fmt.Errorf("go.mod exceeds %d bytes", maximumGoModSize)
	}
	return fileSnapshot{info: after, data: append([]byte(nil), data...)}, nil
}

func sameSnapshot(left, right fileSnapshot) bool {
	return sameFile(left.info, right.info) && bytes.Equal(left.data, right.data)
}

func sameFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
