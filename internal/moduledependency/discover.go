// Package moduledependency discovers explicit application Go Module
// dependencies without traversing the transitive module graph.
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
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const maximumGoModSize = 1 << 20

const (
	defaultOutputLimit       = 16 << 20
	maximumArgumentListBytes = 16 << 10
)

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

// Module is one explicit application requirement resolved to its selected
// read-only source root.
type Module struct {
	path            string
	requiredVersion string
	selectedVersion string
	root            string
	indirect        bool
	workspace       bool
	replacement     Replacement
}

// Path returns the required module path.
func (m Module) Path() string { return m.path }

// RequiredVersion returns the version written in the application go.mod.
func (m Module) RequiredVersion() string { return m.requiredVersion }

// SelectedVersion returns the version selected by Go. It is empty for a
// module supplied directly by the active go.work workspace.
func (m Module) SelectedVersion() string { return m.selectedVersion }

// Root returns the canonical absolute source root. This CLI-only path is not
// generation-extension input or generated manifest data.
func (m Module) Root() string { return m.root }

// Indirect reports whether the explicit go.mod requirement carries the
// indirect marker.
func (m Module) Indirect() bool { return m.indirect }

// Workspace reports whether the selected source is an active go.work module.
func (m Module) Workspace() bool { return m.workspace }

// Replacement returns replacement provenance when Go selected one.
func (m Module) Replacement() (Replacement, bool) {
	return m.replacement, m.replacement.path != ""
}

// Index is an immutable deterministic collection of explicit dependency
// modules.
type Index struct {
	modules []Module
}

// Modules returns a defensive copy sorted by module path.
func (i Index) Modules() []Module {
	return append([]Module(nil), i.modules...)
}

// ByPath returns one exact required module.
func (i Index) ByPath(modulePath string) (Module, bool) {
	for _, dependency := range i.modules {
		if dependency.path == modulePath {
			return dependency, true
		}
	}
	return Module{}, false
}

// Discover resolves every module explicitly required by application go.mod.
// It never asks Go to enumerate the transitive module graph.
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
	if len(requirements) == 0 {
		return Index{}, nil
	}

	outputLimit := options.OutputLimit
	if outputLimit <= 0 {
		outputLimit = defaultOutputLimit
	}
	var output bytes.Buffer
	for _, batch := range requirementBatches(requirements) {
		remaining := outputLimit - output.Len()
		if remaining <= 0 {
			return Index{}, fmt.Errorf("%w: resolve selected modules: %w: limit %d bytes", ErrDiscover, gocommand.ErrOutputTooLarge, outputLimit)
		}
		arguments := append([]string{"list", "-m", "-json", "-mod=readonly"}, batch...)
		batchOutput, err := gocommand.Output(ctx, gocommand.Options{
			Command:     options.GoCommand,
			Directory:   application.Path(),
			Environment: options.Environment,
			OutputLimit: remaining,
		}, arguments...)
		if err != nil {
			return Index{}, fmt.Errorf("%w: resolve selected modules: %w", ErrDiscover, err)
		}
		_, _ = output.Write(batchOutput)
	}
	after, err := readGoMod(goModPath)
	if err != nil || !sameSnapshot(before, after) {
		if err == nil {
			err = ErrConcurrentChange
		}
		return Index{}, fmt.Errorf("%w: %w: application go.mod changed: %v", ErrDiscover, ErrConcurrentChange, err)
	}

	modules, err := decodeModules(output.Bytes(), requirements)
	if err != nil {
		return Index{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	return Index{modules: modules}, nil
}

func requirementBatches(requirements []requirement) [][]string {
	const baseBytes = len("list -m -json -mod=readonly ")
	batches := make([][]string, 0, 1)
	current := make([]string, 0, len(requirements))
	used := baseBytes
	for _, declared := range requirements {
		additional := len(declared.path) + 1
		if len(current) != 0 && used+additional > maximumArgumentListBytes {
			batches = append(batches, current)
			current = make([]string, 0, len(requirements)-len(current))
			used = baseBytes
		}
		current = append(current, declared.path)
		used += additional
	}
	if len(current) != 0 {
		batches = append(batches, current)
	}
	return batches
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

func decodeModules(data []byte, requirements []requirement) ([]Module, error) {
	expected := make(map[string]requirement, len(requirements))
	for _, declared := range requirements {
		expected[declared.path] = declared
	}
	resolved := make(map[string]Module, len(requirements))
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
		declared, intended := expected[listed.Path]
		if !intended {
			return nil, fmt.Errorf("%w: Go returned unrequested module %q", ErrInvalidOutput, listed.Path)
		}
		if _, duplicate := resolved[listed.Path]; duplicate {
			return nil, fmt.Errorf("%w: Go returned module %q more than once", ErrInvalidOutput, listed.Path)
		}
		if listed.Error != nil {
			return nil, fmt.Errorf("%w: Go reported an error for module %q", ErrInvalidOutput, listed.Path)
		}
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
			if semver.Compare(listed.Version, declared.version) < 0 {
				return nil, fmt.Errorf("%w: selected module %s@%s is older than required %s", ErrInvalidOutput, listed.Path, listed.Version, declared.version)
			}
		}

		directory := listed.Dir
		goModPath := listed.GoMod
		var replacement Replacement
		expectedSourcePath := listed.Path
		if listed.Replace != nil {
			if listed.Replace.Path == "" || strings.ContainsRune(listed.Replace.Path, 0) {
				return nil, fmt.Errorf("%w: module %q has invalid replacement path", ErrInvalidOutput, listed.Path)
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
		root, err := validateModuleRoot(listed.Path, directory, goModPath, expectedSourcePath)
		if err != nil {
			return nil, err
		}
		resolved[listed.Path] = Module{
			path:            listed.Path,
			requiredVersion: declared.version,
			selectedVersion: listed.Version,
			root:            root,
			indirect:        declared.indirect,
			workspace:       listed.Main,
			replacement:     replacement,
		}
	}

	modules := make([]Module, 0, len(requirements))
	for _, declared := range requirements {
		resolvedModule, ok := resolved[declared.path]
		if !ok {
			return nil, fmt.Errorf("%w: Go omitted requested module %q", ErrInvalidOutput, declared.path)
		}
		modules = append(modules, resolvedModule)
	}
	return modules, nil
}

func validateModuleRoot(modulePath, directory, goModPath, expectedSourcePath string) (string, error) {
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
	expectedGoMod := filepath.Join(canonicalRoot, "go.mod")
	expectedInfo, err := os.Lstat(expectedGoMod)
	if err != nil || !expectedInfo.Mode().IsRegular() || expectedInfo.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %w: module %q has no regular non-symbolic go.mod", ErrInvalidOutput, ErrModuleUnavailable, modulePath)
	}
	reportedInfo, err := os.Lstat(goModPath)
	if err != nil || !reportedInfo.Mode().IsRegular() || reportedInfo.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: module %q reported invalid go.mod provenance", ErrInvalidOutput, modulePath)
	}
	snapshot, err := readGoMod(expectedGoMod)
	if err != nil {
		return "", fmt.Errorf("%w: %w: inspect go.mod for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, modulePath, err)
	}
	parsed, err := modfile.Parse("go.mod", snapshot.data, nil)
	if err != nil || parsed.Module == nil {
		return "", fmt.Errorf("%w: module %q source has invalid go.mod", ErrInvalidOutput, modulePath)
	}
	if err := module.CheckPath(parsed.Module.Mod.Path); err != nil {
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
