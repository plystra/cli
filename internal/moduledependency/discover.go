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
)

// Options controls the read-only Go command used for module discovery.
type Options struct {
	GoCommand   string
	Environment []string
	OutputLimit int
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
	sourcePath      string
	direct          bool
	indirect        bool
	workspace       bool
	project         bool
	replacement     Replacement
}

// Path returns the required module path.
func (m Module) Path() string { return m.path }

// RequiredVersion returns the version written in the current Project's
// go.mod, or empty for a transitive or workspace-only dependency.
func (m Module) RequiredVersion() string { return m.requiredVersion }

// SelectedVersion returns the version selected by Go. It is empty for a
// module supplied directly by the active go.work workspace.
func (m Module) SelectedVersion() string { return m.selectedVersion }

// Root returns the canonical absolute source root. This CLI-only path is not
// generation-extension input or generated manifest data.
func (m Module) Root() string { return m.root }

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
func Discover(ctx context.Context, application modulelocate.Module, options Options) (Index, error) {
	if application.Path() == "" || application.ModulePath() == "" {
		return Index{}, fmt.Errorf("%w: application module is empty", ErrDiscover)
	}
	goModPath := filepath.Join(application.Path(), "go.mod")
	before, err := readGoMod(goModPath)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w: %v", ErrDiscover, ErrInvalidGoMod, err)
	}
	requirements, err := parseRequirements(before.data, application.ModulePath())
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}

	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = defaultOutputLimit
	}
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   application.Path(),
		Environment: options.Environment,
		OutputLimit: outputLimit,
	}, "list", "-m", "-json", "-mod=readonly", "all")
	if err != nil {
		return Index{}, fmt.Errorf("%w: resolve effective module graph: %w", ErrDiscover, err)
	}
	after, err := readGoMod(goModPath)
	if err != nil || !sameSnapshot(before, after) {
		if err == nil {
			err = ErrConcurrentChange
		}
		return Index{}, fmt.Errorf("%w: %w: application go.mod changed: %v", ErrDiscover, ErrConcurrentChange, err)
	}

	modules, err := decodeModules(output, application, requirements)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	if err := resolveMissingSources(ctx, application.Path(), before, modules, options, outputLimit); err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	for index := range modules {
		project, err := projectlocate.Recognize(modules[index].root)
		if err != nil {
			return Index{}, fmt.Errorf("%w: inspect dependency Project marker for %q: %w", ErrDiscover, modules[index].path, err)
		}
		modules[index].project = project
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
		return nil, fmt.Errorf("%w: module directive changed from %q", ErrInvalidGoMod, applicationPath)
	}
	seen := make(map[string]struct{}, len(parsed.Require))
	requirements := make([]requirement, 0, len(parsed.Require))
	for _, declared := range parsed.Require {
		if err := module.Check(declared.Mod.Path, declared.Mod.Version); err != nil {
			return nil, fmt.Errorf("%w: requirement %s@%s: %v", ErrInvalidGoMod, declared.Mod.Path, declared.Mod.Version, err)
		}
		if declared.Mod.Path == applicationPath {
			return nil, fmt.Errorf("%w: application module cannot require itself", ErrInvalidGoMod)
		}
		if _, duplicate := seen[declared.Mod.Path]; duplicate {
			return nil, fmt.Errorf("%w: duplicate requirement %q", ErrInvalidGoMod, declared.Mod.Path)
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
	Path    string             `json:"Path"`
	Version string             `json:"Version"`
	Main    bool               `json:"Main"`
	Dir     string             `json:"Dir"`
	GoMod   string             `json:"GoMod"`
	Replace *listedReplacement `json:"Replace"`
	Error   *listedError       `json:"Error"`
}

type listedReplacement struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	GoMod   string `json:"GoMod"`
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
		}
		root := ""
		if (directory == "" || goModPath == "") && !listed.Main && (listed.Replace == nil || listed.Replace.Version != "") {
			// go list may report only graph metadata when a selected source
			// archive has not been extracted yet. Resolve that exact selected
			// version later, outside the Project directory.
		} else {
			var err error
			root, err = validateModuleRoot(listed.Path, directory, goModPath, expectedSourcePath, listed.Main)
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
			sourcePath:      expectedSourcePath,
			direct:          isDirect,
			indirect:        isDirect && declared.indirect,
			workspace:       listed.Main,
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
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	GoMod   string `json:"GoMod"`
}

func resolveMissingSources(ctx context.Context, applicationRoot string, expectedGoMod fileSnapshot, modules []Module, options Options, outputLimit int) error {
	missing := false
	for _, dependency := range modules {
		if dependency.root == "" {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}

	downloadRoot, err := os.MkdirTemp("", "plystra-module-download-")
	if err != nil {
		return fmt.Errorf("%w: create isolated download directory: %v", ErrModuleUnavailable, err)
	}
	defer os.RemoveAll(downloadRoot)

	environment := environmentWith(options.Environment, "GOWORK", "off")
	for index := range modules {
		if modules[index].root != "" {
			continue
		}
		queryPath := modules[index].sourcePath
		queryVersion := modules[index].selectedVersion
		if replacement, exists := modules[index].Replacement(); exists {
			if replacement.Local() {
				return fmt.Errorf("%w: local replacement source for module %q is unavailable", ErrModuleUnavailable, modules[index].path)
			}
			queryPath = replacement.Path()
			queryVersion = replacement.Version()
		}
		query := queryPath + "@" + queryVersion
		output, err := gocommand.Output(ctx, gocommand.Options{
			Command:     options.GoCommand,
			Directory:   downloadRoot,
			Environment: environment,
			OutputLimit: outputLimit,
		}, "mod", "download", "-json", query)
		if err != nil {
			return fmt.Errorf("%w: download selected source for module %q: %w", ErrModuleUnavailable, modules[index].path, err)
		}
		var downloaded downloadedModule
		decoder := json.NewDecoder(bytes.NewReader(output))
		if err := decoder.Decode(&downloaded); err != nil {
			return fmt.Errorf("%w: decode downloaded source for module %q: %v", ErrInvalidOutput, modules[index].path, err)
		}
		if err := ensureJSONEnd(decoder); err != nil {
			return fmt.Errorf("%w: downloaded source for module %q: %v", ErrInvalidOutput, modules[index].path, err)
		}
		if downloaded.Path != queryPath || downloaded.Version != queryVersion {
			return fmt.Errorf("%w: downloaded source for module %q returned %s@%s, expected %s", ErrInvalidOutput, modules[index].path, downloaded.Path, downloaded.Version, query)
		}
		root, err := validateModuleRoot(modules[index].path, downloaded.Dir, downloaded.GoMod, queryPath, false)
		if err != nil {
			return err
		}
		modules[index].root = root
	}

	// Downloads run outside the Project directory. Recheck the Project module
	// file anyway so a concurrent dependency edit cannot be mistaken for the
	// graph whose sources were just inspected.
	currentGoMod, err := readGoMod(filepath.Join(applicationRoot, "go.mod"))
	if err != nil || !sameSnapshot(expectedGoMod, currentGoMod) {
		if err == nil {
			err = ErrConcurrentChange
		}
		return fmt.Errorf("%w: application go.mod changed while resolving sources: %v", ErrConcurrentChange, err)
	}
	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("go returned more than one JSON document")
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

func validateModuleRoot(modulePath, directory, goModPath, expectedSourcePath string, project bool) (string, error) {
	if directory == "" || goModPath == "" {
		return "", fmt.Errorf("%w: %w: module %q must be downloaded before generation", ErrInvalidOutput, ErrModuleUnavailable, modulePath)
	}
	if !filepath.IsAbs(directory) || !filepath.IsAbs(goModPath) {
		return "", fmt.Errorf("%w: module %q returned non-absolute source provenance", ErrInvalidOutput, modulePath)
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(directory))
	if err != nil {
		return "", fmt.Errorf("%w: %w: resolve source for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, modulePath, err)
	}
	rootInfo, err := os.Lstat(canonicalRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %w: source for module %q is not a directory", ErrInvalidOutput, ErrModuleUnavailable, modulePath)
	}
	reportedInfo, err := os.Lstat(goModPath)
	if err != nil || !reportedInfo.Mode().IsRegular() || reportedInfo.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: module %q reported invalid go.mod provenance", ErrInvalidOutput, modulePath)
	}
	manifestPath := goModPath
	rootGoMod := filepath.Join(canonicalRoot, "go.mod")
	rootInfo, rootErr := os.Lstat(rootGoMod)
	switch {
	case rootErr == nil && rootInfo.Mode().IsRegular() && rootInfo.Mode()&fs.ModeSymlink == 0:
		manifestPath = rootGoMod
	case rootErr == nil:
		return "", fmt.Errorf("%w: module %q source has unsafe go.mod", ErrInvalidOutput, modulePath)
	case !errors.Is(rootErr, fs.ErrNotExist):
		return "", fmt.Errorf("%w: inspect source go.mod for module %q: %v", ErrInvalidOutput, modulePath, rootErr)
	}
	snapshot, err := readGoMod(manifestPath)
	if err != nil {
		return "", fmt.Errorf("%w: %w: inspect go.mod for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, modulePath, err)
	}
	parsed, err := modfile.Parse("go.mod", snapshot.data, nil)
	if err != nil || parsed.Module == nil {
		return "", fmt.Errorf("%w: module %q source has invalid go.mod", ErrInvalidOutput, modulePath)
	}
	var pathErr error
	if project {
		pathErr = modulepath.CheckProject(parsed.Module.Mod.Path)
	} else {
		pathErr = module.CheckPath(parsed.Module.Mod.Path)
	}
	if pathErr != nil {
		return "", fmt.Errorf("%w: module %q source declares invalid path %q", ErrInvalidOutput, modulePath, parsed.Module.Mod.Path)
	}
	if expectedSourcePath != "" && parsed.Module.Mod.Path != expectedSourcePath {
		return "", fmt.Errorf("%w: module %q source declares %q, expected %q", ErrInvalidOutput, modulePath, parsed.Module.Mod.Path, expectedSourcePath)
	}
	return canonicalRoot, nil
}

type fileSnapshot struct {
	info fs.FileInfo
	data []byte
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
