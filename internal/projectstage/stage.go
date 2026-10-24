// Package projectstage captures owner-private working copies for final-state
// planning. It isolates filesystem writes, not the execution of trusted code.
package projectstage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/modfile"
)

const (
	MaximumRoots         = 1_024
	MaximumEntries       = 65_536
	MaximumFileBytes     = 64 << 20
	MaximumTotalBytes    = 512 << 20
	maximumMetadataBytes = 1 << 20
)

var (
	ErrStage            = errors.New("capture private Project stage")
	ErrConcurrentChange = errors.New("Project staging input changed")
)

// Event identifies an effect that occurred during Create without exposing paths
// or captured data. A later failure or cleanup does not undo the observation.
type Event uint8

const (
	GoEnvironmentStarted Event = iota + 1
	TemporaryTreeCreated
)

// Options preserves the selected Go workspace and ordinary local replacements.
// Alternate module files and source overlays need their own relocation contract
// and are rejected rather than silently reading live external inputs.
type Options struct {
	Start           string
	TemporaryParent string
	GoCommand       string
	Environment     []string
	// Observe is called synchronously, at most once per event. Go process
	// startup is reported after the invocation returns; tree creation is
	// reported before copying any captured input.
	Observe func(Event)
}

// Stage owns only its independently allocated temporary tree. Captured file
// identities and digests are private concurrency evidence, not public plan data.
type Stage struct {
	project       string
	inputs        Inputs
	projectImages map[string]relocationImage
	root          string
	temporary     string
	parent        *os.Root
	identity      os.FileInfo
	files         *os.Root
	environment   []string
	trees         []treeSnapshot
	metadata      []fileSnapshot
	aliases       []directorySnapshot
	mainRoots     map[string]string
	relocations   []replacementRelocation
	paths         []pathSnapshot
	stagedPaths   []pathSnapshot
	workspaceBase string
	goEnvironment *fileSnapshot
	closed        bool
}

func (Stage) String() string                   { return "<private-project-stage>" }
func (s Stage) GoString() string               { return s.String() }
func (s Stage) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, s.String()) }

func (s *Stage) Root() string          { return s.root }
func (s *Stage) Environment() []string { return append([]string(nil), s.environment...) }

// Create copies the authored working tree, assets, and generated history, not a
// Git revision. Only VCS administration entries are excluded. It never invokes
// application code or installs files in a source module.
func Create(ctx context.Context, options Options) (result *Stage, resultErr error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrStage
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return nil, ErrStage
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	environment = slices.Clone(environment)
	output, started, err := gocommand.OutputObserved(ctx, gocommand.Options{Command: options.GoCommand, Directory: project.Path(), Environment: environment, OutputLimit: maximumMetadataBytes}, "env", "-json", "GOWORK", "GOFLAGS", "GOENV")
	if started && options.Observe != nil {
		options.Observe(GoEnvironmentStarted)
	}
	if err != nil {
		return nil, ErrStage
	}
	var settings goEnvironment
	if json.Unmarshal(output, &settings) != nil || unsafeGoFlags(settings.GOFLAGS) {
		return nil, ErrStage
	}
	s := &Stage{project: project.Path(), environment: environment, projectImages: map[string]relocationImage{}}
	defer func() {
		if resultErr != nil {
			s.inputs = Inputs{}
			if cleanupErr := s.Close(); cleanupErr != nil {
				result, resultErr = s, errors.Join(resultErr, cleanupErr)
			}
		}
	}()
	modules, workspace, err := s.captureModuleInputs(ctx, project.Path(), settings.GOWORK)
	if err != nil {
		return nil, ErrStage
	}
	if err := s.captureGoEnvironment(ctx, options.GoCommand, settings); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStage, err)
	}
	var vendorSource string
	if workspace != nil {
		vendor := filepath.Join(s.metadata[0].parent, "vendor")
		info, err := os.Lstat(vendor)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, ErrStage
		}
		s.paths = append(s.paths, pathSnapshot{path: vendor, info: info})
		if info != nil {
			if !info.IsDir() {
				return nil, ErrStage
			}
			vendorSource = vendor
		}
	}
	parent := options.TemporaryParent
	if parent == "" {
		parent = os.TempDir()
	}
	parent, err = canonicalDirectory(parent)
	if err != nil {
		return nil, ErrStage
	}
	for _, root := range modules {
		if within(root, parent) {
			return nil, ErrStage
		}
	}
	if vendorSource != "" && within(vendorSource, parent) {
		return nil, ErrStage
	}
	s.parent, err = os.OpenRoot(parent)
	if err != nil {
		return nil, ErrStage
	}
	temporaryName, err := privatefile.MkdirTempRoot(s.parent, "plystra-stage-")
	if err != nil {
		return nil, ErrStage
	}
	s.temporary = filepath.Join(parent, temporaryName)
	if options.Observe != nil {
		options.Observe(TemporaryTreeCreated)
	}
	s.identity, err = s.parent.Lstat(filepath.Base(s.temporary))
	if err != nil || !s.identity.IsDir() {
		return nil, ErrStage
	}
	s.files, err = s.parent.OpenRoot(filepath.Base(s.temporary))
	if err != nil {
		return nil, ErrStage
	}
	var used budget
	for index, root := range outermostRoots(modules) {
		destination := fmt.Sprintf("module-%d", index)
		snapshot, err := captureTree(ctx, root, destination, s.files, &used)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrStage, err)
		}
		s.trees = append(s.trees, snapshot)
	}
	if vendorSource != "" {
		snapshot, err := captureTree(ctx, vendorSource, "vendor", s.files, &used)
		if err != nil {
			return nil, ErrStage
		}
		s.trees = append(s.trees, snapshot)
	}
	s.root, err = s.relocated(project.Path())
	if err != nil {
		return nil, ErrStage
	}
	for _, root := range modules {
		staged, err := s.relocated(root)
		if err != nil {
			return nil, ErrStage
		}
		relative, err := filepath.Rel(s.temporary, staged)
		if err != nil {
			return nil, ErrStage
		}
		info, err := s.files.Lstat(relative)
		if err != nil || !info.IsDir() {
			return nil, ErrStage
		}
		s.stagedPaths = append(s.stagedPaths, pathSnapshot{path: relative, info: info})
	}
	for _, root := range modules {
		if err := s.relocateModule(root); err != nil {
			return nil, ErrStage
		}
	}
	selectedWorkspace := "off"
	if workspace != nil {
		selectedWorkspace, err = s.relocateWorkspace(workspace)
		if err != nil {
			return nil, ErrStage
		}
	}
	s.environment = replaceEnvironment(s.environment, "GOWORK", selectedWorkspace)
	if err := s.stageGoEnvironment(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStage, err)
	}
	s.inputs = s.captureInputs()
	if err := s.Recheck(ctx); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStage, err)
	}
	return s, nil
}

// Recheck rejects changed bytes, membership, modes, and path identities. It does
// not grant a mutation lock or replace installation-time preimage checks.
func (s *Stage) Recheck(ctx context.Context) error {
	if ctx == nil || s == nil || s.closed {
		return ErrConcurrentChange
	}
	if s.files == nil {
		return ErrConcurrentChange
	}
	temporary, err := os.Lstat(s.temporary)
	if err != nil || !temporary.IsDir() || !os.SameFile(temporary, s.identity) {
		return ErrConcurrentChange
	}
	for _, staged := range s.stagedPaths {
		current, err := s.files.Lstat(staged.path)
		if err != nil || !sameFile(staged.info, current) {
			return ErrConcurrentChange
		}
		for path := staged.path; path != "."; path = filepath.Dir(path) {
			info, err := s.files.Lstat(path)
			if err != nil || !info.IsDir() {
				return ErrConcurrentChange
			}
		}
	}
	if s.workspaceBase != "" {
		info, err := s.files.Lstat("go.work")
		if err != nil || !info.Mode().IsRegular() {
			return ErrConcurrentChange
		}
	}
	if s.goEnvironment != nil && s.goEnvironment.recheck() != nil {
		return ErrConcurrentChange
	}
	return s.inputs.Recheck(ctx)
}

// Close removes only this Stage's allocated directory and is idempotent.
func (s *Stage) Close() error {
	if s == nil || s.closed {
		return nil
	}
	if s.files != nil {
		if err := s.files.Close(); err != nil {
			return ErrStage
		}
		s.files = nil
	}
	if s.temporary != "" {
		if s.parent == nil || s.identity == nil {
			return ErrStage
		}
		current, err := s.parent.Lstat(filepath.Base(s.temporary))
		if errors.Is(err, os.ErrNotExist) {
			// Removal by the owner is already clean; never follow a replacement.
		} else if err != nil || !os.SameFile(s.identity, current) {
			return ErrConcurrentChange
		} else if err := s.parent.RemoveAll(filepath.Base(s.temporary)); err != nil {
			return ErrStage
		}
	}
	if s.parent != nil {
		if err := s.parent.Close(); err != nil {
			return ErrStage
		}
	}
	s.closed = true
	return nil
}

type fileSnapshot struct {
	parent string
	name   string
	info   os.FileInfo
	digest [sha256.Size]byte
	data   []byte
}

type directorySnapshot struct {
	path   string
	target string
	info   os.FileInfo
}

type pathSnapshot struct {
	path string
	info os.FileInfo
}

func (s *Stage) captureDirectory(base, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	target, err := canonicalDirectory(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", ErrStage
	}
	for _, alias := range s.aliases {
		if alias.path == path {
			if alias.target != target || !sameFile(alias.info, info) {
				return "", ErrConcurrentChange
			}
			return target, nil
		}
	}
	if len(s.aliases) == MaximumRoots {
		return "", ErrStage
	}
	s.aliases = append(s.aliases, directorySnapshot{path: path, target: target, info: info})
	return target, nil
}

func readMetadata(parent, name string, optional bool) (fileSnapshot, error) {
	r, err := os.OpenRoot(parent)
	if err != nil {
		return fileSnapshot{}, ErrStage
	}
	defer r.Close()
	info, err := r.Lstat(name)
	if optional && errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{parent: parent, name: name}, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return fileSnapshot{}, ErrStage
	}
	f, err := r.Open(name)
	if err != nil {
		return fileSnapshot{}, ErrStage
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return fileSnapshot{}, ErrConcurrentChange
	}
	data, err := io.ReadAll(io.LimitReader(f, maximumMetadataBytes+1))
	after, statErr := r.Lstat(name)
	if err != nil || statErr != nil || len(data) > maximumMetadataBytes || !sameFile(info, after) {
		return fileSnapshot{}, ErrConcurrentChange
	}
	return fileSnapshot{parent: parent, name: name, info: info, digest: sha256.Sum256(data), data: data}, nil
}

func (f fileSnapshot) recheck() error {
	after, err := readMetadata(f.parent, f.name, f.info == nil)
	if err != nil || !sameFile(f.info, after.info) || f.digest != after.digest {
		return ErrConcurrentChange
	}
	return nil
}

func (s *Stage) captureModuleInputs(ctx context.Context, project, work string) ([]string, *modfile.WorkFile, error) {
	if _, err := s.captureDirectory(project, "."); err != nil {
		return nil, nil, err
	}
	roots := []string{project}
	mainRoots := []string{project}
	s.mainRoots = map[string]string{project: project}
	seen := map[string]bool{project: true}
	addRoot := func(root string) error {
		if !seen[root] {
			if len(roots) == MaximumRoots {
				return ErrStage
			}
			seen[root] = true
			roots = append(roots, root)
		}
		return ctx.Err()
	}
	var workspace *modfile.WorkFile
	if work != "" && work != "off" {
		parent, err := s.captureDirectory(project, filepath.Dir(work))
		if err != nil {
			return nil, nil, err
		}
		s.workspaceBase = filepath.Dir(work)
		file, err := readMetadata(parent, filepath.Base(work), false)
		if err != nil {
			return nil, nil, err
		}
		s.metadata = append(s.metadata, file)
		sum, err := readMetadata(parent, filepath.Base(work)+".sum", true)
		if err != nil {
			return nil, nil, err
		}
		s.metadata = append(s.metadata, sum)
		workspace, err = modfile.ParseWork("go.work", file.data, nil)
		if err != nil {
			return nil, nil, ErrStage
		}
		for _, use := range workspace.Use {
			logical := use.Path
			if !filepath.IsAbs(logical) {
				logical = filepath.Join(s.workspaceBase, logical)
			}
			root, err := s.captureDirectory(s.workspaceBase, logical)
			if err != nil {
				return nil, nil, err
			}
			if err := addRoot(root); err != nil {
				return nil, nil, err
			}
			mainRoots = append(mainRoots, root)
			s.mainRoots[root] = logical
		}
		for _, replacement := range workspace.Replace {
			if replacement.New.Version == "" {
				root, err := s.captureDirectory(s.workspaceBase, replacement.New.Path)
				if err != nil {
					return nil, nil, err
				}
				if err := addRoot(root); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	// Only main modules' replacements affect the selected module graph. Local
	// replacement modules' own directives must not activate extra dependencies.
	sort.Strings(mainRoots)
	mainRoots = compactStrings(mainRoots)
	metadataBytes := 0
	mainFiles := make(map[string]*modfile.File, len(mainRoots))
	workspaceModules := map[string]bool{}
	for _, root := range mainRoots {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		file, err := readMetadata(root, "go.mod", false)
		if err != nil {
			return nil, nil, err
		}
		s.metadata = append(s.metadata, file)
		metadataBytes += len(file.data)
		if metadataBytes > MaximumTotalBytes {
			return nil, nil, ErrStage
		}
		module, err := modfile.Parse("go.mod", file.data, nil)
		if err != nil || module.Module == nil {
			return nil, nil, ErrStage
		}
		mainFiles[root] = module
		if workspace != nil {
			workspaceModules[module.Module.Mod.Path] = true
		}
	}
	for _, root := range mainRoots {
		module := mainFiles[root]
		for _, replacement := range module.Replace {
			if replacement.New.Version == "" && !workspaceOverrides(workspace, replacement) && !workspaceModules[replacement.Old.Path] {
				dependency, err := s.captureDirectory(s.mainRoots[root], replacement.New.Path)
				if err != nil {
					return nil, nil, err
				}
				if err := addRoot(dependency); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	sort.Strings(roots)
	roots = compactStrings(roots)
	if len(roots) > MaximumRoots {
		return nil, nil, ErrStage
	}
	return roots, workspace, nil
}

func workspaceOverrides(workspace *modfile.WorkFile, replacement *modfile.Replace) bool {
	if workspace != nil {
		for _, selected := range workspace.Replace {
			if selected.Old.Path == replacement.Old.Path && (selected.Old.Version == "" || selected.Old.Version == replacement.Old.Version) {
				return true
			}
		}
	}
	return false
}

func (s *Stage) relocateModule(root string) error {
	logical := s.mainRoots[root]
	if logical == "" {
		return nil
	}
	staged, err := s.relocated(root)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(s.temporary, filepath.Join(staged, "go.mod"))
	if err != nil {
		return ErrStage
	}
	data, err := s.files.ReadFile(relative)
	if err != nil {
		return err
	}
	originalData := append([]byte(nil), data...)
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	changed := false
	var relocations []replacementRelocation
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		original, err := resolveDirectory(logical, replacement.New.Path)
		if err != nil {
			continue
		}
		mapped, err := s.relocated(original)
		if err != nil {
			// A shadowed main-module replacement need not be a captured input.
			continue
		}
		token, err := originalReplacementToken(originalData, replacement)
		if err != nil {
			return err
		}
		relocations = append(relocations, replacementRelocation{owner: root, module: replacement.Old.Path, version: replacement.Old.Version, original: replacement.New.Path, originalToken: token.text, workspaceOriginal: s.workspaceVendorPath(logical, replacement.New.Path), mapped: mapped})
		if err := relocateReplacement(replacement, mapped); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		data, err = module.Format()
		if err != nil {
			return err
		}
		if err := s.files.WriteFile(relative, data, 0o600); err != nil {
			return err
		}
	}
	if root == s.project {
		s.projectImages["go.mod"] = relocationImage{original: originalData, baseline: append([]byte(nil), data...)}
	}
	s.relocations = append(s.relocations, relocations...)
	return s.relocateVendor(filepath.Join(filepath.Dir(relative), "vendor", "modules.txt"), relocations, false)
}

func (s *Stage) relocateWorkspace(workspace *modfile.WorkFile) (string, error) {
	base := s.workspaceBase
	for _, use := range workspace.Use {
		original, err := resolveDirectory(base, use.Path)
		if err != nil {
			return "", err
		}
		mapped, err := s.relocated(original)
		if err != nil {
			return "", err
		}
		use.Path = mapped
		use.Syntax.Token[len(use.Syntax.Token)-1] = modfile.AutoQuote(use.Path)
	}
	for _, replacement := range workspace.Replace {
		if replacement.New.Version != "" {
			continue
		}
		original, err := resolveDirectory(base, replacement.New.Path)
		if err != nil {
			return "", err
		}
		mapped, err := s.relocated(original)
		if err != nil {
			return "", err
		}
		s.relocations = append(s.relocations, replacementRelocation{module: replacement.Old.Path, version: replacement.Old.Version, original: replacement.New.Path, workspaceOriginal: replacement.New.Path, mapped: mapped, workspace: true})
		if err := relocateReplacement(replacement, mapped); err != nil {
			return "", err
		}
	}
	workspace.Cleanup()
	data := modfile.Format(workspace.Syntax)
	path := filepath.Join(s.temporary, "go.work")
	if err := writePrivate(s.files, "go.work", data); err != nil {
		return "", err
	}
	if s.metadata[1].info != nil {
		if err := writePrivate(s.files, "go.work.sum", s.metadata[1].data); err != nil {
			return "", err
		}
	}
	if err := s.relocateVendor(filepath.Join("vendor", "modules.txt"), s.relocations, true); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Stage) relocated(original string) (string, error) {
	for _, tree := range s.trees {
		if within(tree.source, original) {
			relative, err := filepath.Rel(tree.source, original)
			if err != nil {
				return "", ErrStage
			}
			return filepath.Join(tree.destination, relative), nil
		}
	}
	return "", ErrStage
}

func writePrivate(root *os.Root, path string, data []byte) error {
	file, err := privatefile.CreateRoot(root, path)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func relocateReplacement(replacement *modfile.Replace, mapped string) error {
	index := slices.Index(replacement.Syntax.Token, "=>")
	if index < 0 || index+1 >= len(replacement.Syntax.Token) {
		return ErrStage
	}
	replacement.New.Path = mapped
	replacement.Syntax.Token[index+1] = modfile.AutoQuote(replacement.New.Path)
	return nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", ErrStage
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", ErrStage
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.IsDir() {
		return "", ErrStage
	}
	return canonical, nil
}

func resolveDirectory(base, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return canonicalDirectory(path)
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && filepath.IsLocal(relative)
}

func outermostRoots(roots []string) []string {
	var result []string
	for _, root := range roots {
		nested := false
		for _, other := range roots {
			if other != root && within(other, root) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, root)
		}
	}
	return result
}

func compactStrings(values []string) []string {
	var result []string
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func replaceEnvironment(environment []string, name, value string) []string {
	var result []string
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, name) {
			result = append(result, entry)
		}
	}
	return append(result, name+"="+value)
}

func unsafeGoFlags(flags string) bool {
	return strings.Contains(flags, "-modfile") || strings.Contains(flags, "-overlay")
}
