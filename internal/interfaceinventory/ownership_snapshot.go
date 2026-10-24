package interfaceinventory

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/projectlocate"
	"golang.org/x/mod/modfile"
)

// ErrOwnershipSnapshot means previously captured authored declaration inputs
// changed. Neither configured values nor private source bytes enter the error.
var ErrOwnershipSnapshot = errors.New("captured declaration ownership sources changed")

// OwnershipSnapshot retains actual Go-selected declaration/type inputs and
// eligible declaration membership. It is independent of subsequent module
// selection, and never reinterprets old evidence through new dependencies.
type OwnershipSnapshot struct {
	application  string
	options      Options
	dependencies moduledependency.Index
	sources      []moduleSource
	active       map[string]bool
	packages     map[string]listedPackage
	files        map[string]ownershipFile
	directories  map[string]fs.FileInfo
	modules      map[string][]byte
}

func (OwnershipSnapshot) String() string                   { return "<private-ownership-snapshot>" }
func (s OwnershipSnapshot) GoString() string               { return s.String() }
func (s OwnershipSnapshot) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(s.String())) }

type ownershipFile struct {
	info   fs.FileInfo
	digest [sha256.Size]byte
}

// CaptureOwnershipSources uses the package graph that produced this Discovery.
// Only active declaration packages and their compiled type dependencies supply
// file evidence. Membership probes share ordinary reserved/nested exclusions.
func (d Discovery) CaptureOwnershipSources(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, options Options) (OwnershipSnapshot, error) {
	options.Environment = append([]string(nil), options.Environment...)
	s := OwnershipSnapshot{application: application.ModulePath(), options: options, dependencies: dependencies,
		active: make(map[string]bool), packages: make(map[string]listedPackage), files: make(map[string]ownershipFile), directories: make(map[string]fs.FileInfo), modules: make(map[string][]byte)}
	s.sources = []moduleSource{{path: application.ModulePath(), root: application.Path(), local: true}}
	for _, dependency := range dependencies.Projects() {
		s.sources = append(s.sources, moduleSource{path: dependency.Path(), root: dependency.Root(), version: dependency.SelectedVersion()})
	}
	for _, source := range s.sources {
		data, err := readRegularFile(filepath.Join(source.root, "go.mod"), 1<<20)
		if err != nil {
			return OwnershipSnapshot{}, ErrOwnershipSnapshot
		}
		s.modules[source.root] = data
	}
	for _, value := range d.Interfaces().Interfaces() {
		s.active[value.PackagePath()] = true
	}
	for _, value := range d.Resources().Resources() {
		s.active[value.PackagePath()] = true
	}
	for _, value := range d.Implementations().Implementations() {
		s.active[value.PackagePath()] = true
	}
	for _, value := range d.ResourceProviders().Providers() {
		s.active[value.PackagePath()] = true
	}
	var visit func(string) error
	visit = func(name string) error {
		if _, seen := s.packages[name]; seen {
			return nil
		}
		loaded, exists := d.sourcePackages[name]
		if !exists || loaded.Module == nil {
			return nil
		} // Standard library is toolchain-owned.
		s.packages[name] = loaded
		root := loaded.Module.Dir
		if loaded.Module.Replace != nil {
			root = loaded.Module.Replace.Dir
		}
		if root == "" {
			return ErrOwnershipSnapshot
		}
		data, err := readRegularFile(filepath.Join(root, "go.mod"), 1<<20)
		if errors.Is(err, fs.ErrNotExist) {
			data, err = nil, nil
		}
		if err != nil {
			return ErrOwnershipSnapshot
		}
		s.modules[root] = data
		for directory := loaded.Dir; ; directory = filepath.Dir(directory) {
			info, err := os.Lstat(directory)
			if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
				return ErrOwnershipSnapshot
			}
			s.directories[directory] = info
			if directory == root {
				break
			}
			if filepath.Dir(directory) == directory {
				return ErrOwnershipSnapshot
			}
		}
		for _, file := range append(append([]string(nil), loaded.GoFiles...), loaded.CgoFiles...) {
			if filepath.Base(file) != file {
				return ErrOwnershipSnapshot
			}
			name := filepath.Join(loaded.Dir, file)
			value, err := captureOwnershipFile(ctx, name)
			if err != nil {
				return err
			}
			s.files[name] = value
		}
		for _, imported := range loaded.Imports {
			if mapped, exists := loaded.ImportMap[imported]; exists {
				imported = mapped
			}
			if err := visit(imported); err != nil {
				return err
			}
		}
		return nil
	}
	for name := range s.active {
		if err := visit(name); err != nil {
			return OwnershipSnapshot{}, err
		}
	}
	for _, candidate := range d.sourceCandidates {
		if !s.active[candidate.importPath] {
			continue
		}
		name := filepath.Join(candidate.directory, "interface.yaml")
		value, err := captureOwnershipFile(ctx, name)
		if err != nil {
			return OwnershipSnapshot{}, err
		}
		s.files[name] = value
	}
	if err := s.ValidateSnapshot(ctx); err != nil {
		return OwnershipSnapshot{}, err
	}
	return s, nil
}

// ValidateSnapshot rechecks old source bytes and identities, active Go-file
// membership, and declaration membership in the original module roots. Source
// selection uses captured module metadata only in private alternate files;
// it neither compiles application tests nor changes the authored module graph.
func (s OwnershipSnapshot) ValidateSnapshot(ctx context.Context) error {
	if ctx == nil {
		return ErrOwnershipSnapshot
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.recheckDirectories(); err != nil {
		return err
	}
	if err := s.recheckFiles(ctx); err != nil {
		return err
	}
	groups := make(map[string]map[string]string)
	for _, loaded := range s.packages {
		root := loaded.Module.Dir
		if loaded.Module.Replace != nil {
			root = loaded.Module.Replace.Dir
		}
		if groups[root] == nil {
			groups[root] = make(map[string]string)
		}
		groups[root][loaded.Dir] = loaded.ImportPath
	}
	var candidates []packageCandidate
	for _, source := range s.sources {
		if !source.local {
			project, err := projectlocate.Recognize(source.root, source.path)
			if err != nil || !project {
				return ErrOwnershipSnapshot
			}
		}
		found, err := probeModule(source, s.application, false)
		if err != nil {
			return ErrOwnershipSnapshot
		}
		candidates = append(candidates, found...)
		for _, candidate := range found {
			if groups[source.root] == nil {
				groups[source.root] = make(map[string]string)
			}
			groups[source.root][candidate.directory] = candidate.importPath
		}
	}
	selected := make(map[string]listedPackage)
	for root, directories := range groups {
		packages, err := s.selectSources(ctx, root, directories)
		if err != nil {
			return errors.Join(ErrOwnershipSnapshot, ctx.Err())
		}
		for name, value := range packages {
			selected[name] = value
		}
	}
	for name, before := range s.packages {
		after, exists := selected[name]
		if !exists || before.Dir != after.Dir || !reflect.DeepEqual(before.GoFiles, after.GoFiles) || !reflect.DeepEqual(before.CgoFiles, after.CgoFiles) {
			return ErrOwnershipSnapshot
		}
	}
	active := make(map[string]bool)
	for _, candidate := range candidates {
		loaded, exists := selected[candidate.importPath]
		if !exists {
			return ErrOwnershipSnapshot
		}
		declarations, err := parseDeclarations(candidate, loaded)
		if err != nil {
			return ErrOwnershipSnapshot
		}
		if len(declarations.interfaces)+len(declarations.resources)+len(declarations.implementations)+len(declarations.resourceProviders) > 0 {
			active[candidate.importPath] = true
		}
	}
	if !reflect.DeepEqual(s.active, active) {
		return ErrOwnershipSnapshot
	}
	if err := s.recheckDirectories(); err != nil {
		return err
	}
	return s.recheckFiles(ctx)
}

func (s OwnershipSnapshot) recheckDirectories() error {
	for name, before := range s.directories {
		after, err := os.Lstat(name)
		if err != nil || !after.IsDir() || before.Mode() != after.Mode() || !os.SameFile(before, after) {
			return ErrOwnershipSnapshot
		}
	}
	return nil
}

func (s OwnershipSnapshot) recheckFiles(ctx context.Context) error {
	for name, before := range s.files {
		after, err := captureOwnershipFile(ctx, name)
		if err != nil {
			return ErrOwnershipSnapshot
		}
		if before.info == nil || after.info == nil {
			if before.info != nil || after.info != nil {
				return ErrOwnershipSnapshot
			}
			continue
		}
		if before.info.Mode() != after.info.Mode() || !os.SameFile(before.info, after.info) || before.digest != after.digest {
			return ErrOwnershipSnapshot
		}
	}
	return nil
}

func captureOwnershipFile(ctx context.Context, name string) (ownershipFile, error) {
	_, err := os.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return ownershipFile{}, nil
	}
	if err != nil {
		return ownershipFile{}, ErrOwnershipSnapshot
	}
	file, err := openRegularFile(name)
	if err != nil {
		return ownershipFile{}, ErrOwnershipSnapshot
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ownershipFile{}, ErrOwnershipSnapshot
	}
	// Supporting packages are compiler inputs, not authored declaration files.
	// Stream their bytes without adding a source-size admission limit.
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return ownershipFile{}, err
		}
		n, err := file.Read(buffer)
		_, _ = hash.Write(buffer[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ownershipFile{}, ErrOwnershipSnapshot
		}
	}
	after, err := os.Lstat(name)
	if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return ownershipFile{}, ErrOwnershipSnapshot
	}
	result := ownershipFile{info: info}
	copy(result.digest[:], hash.Sum(nil))
	return result, nil
}

func (s OwnershipSnapshot) selectSources(ctx context.Context, root string, directories map[string]string) (result map[string]listedPackage, resultErr error) {
	temporary, err := os.MkdirTemp("", "plystra-ownership-")
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(temporary)) }()
	moduleFile := filepath.Join(temporary, "go.mod")
	data := s.modules[root]
	directory, mode := root, "readonly"
	paths := make([]string, 0, len(directories))
	for packageDirectory := range directories {
		relative, err := filepath.Rel(root, packageDirectory)
		if err != nil || !filepath.IsLocal(relative) {
			return nil, ErrOwnershipSnapshot
		}
		paths = append(paths, "./"+filepath.ToSlash(relative))
	}
	if len(data) == 0 {
		// Legacy dependency archives have no root go.mod. Select them through
		// the captured application graph, where Go uses cached synthetic module
		// metadata. All checksum/requirement normalization stays private.
		directory, mode = s.sources[0].root, "mod"
		file, err := modfile.Parse("go.mod", s.modules[directory], nil)
		if err != nil {
			return nil, err
		}
		paths = paths[:0]
		for _, name := range directories {
			loaded := s.packages[name]
			if loaded.Module == nil || loaded.Module.Version == "" {
				return nil, ErrOwnershipSnapshot
			}
			if err := file.AddRequire(loaded.Module.Path, loaded.Module.Version); err != nil {
				return nil, err
			}
			paths = append(paths, name)
		}
		// A workspace may override the application's replacement directives.
		// Reproduce only its captured effective sources in this private graph.
		for _, dependency := range s.dependencies.Modules() {
			var target, targetVersion string
			if dependency.Workspace() {
				target = dependency.Root()
			} else if replacement, exists := dependency.Replacement(); exists {
				target, targetVersion = replacement.Path(), replacement.Version()
				if replacement.Local() {
					target = dependency.Root()
				}
			} else {
				continue
			}
			for _, replacement := range append([]*modfile.Replace(nil), file.Replace...) {
				if replacement.Old.Path == dependency.Path() {
					if err := file.DropReplace(replacement.Old.Path, replacement.Old.Version); err != nil {
						return nil, err
					}
				}
			}
			if err := file.AddReplace(dependency.Path(), "", target, targetVersion); err != nil {
				return nil, err
			}
		}
		data, err = file.Format()
		if err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(moduleFile, data, 0o600); err != nil {
		return nil, err
	}
	arguments := []string{"list", "-e", "-json", "-export=false", "-mod=" + mode, "-modfile=" + moduleFile}
	sort.Strings(paths)
	arguments = append(arguments, paths...)
	options := withOfflineGoEnvironment(s.options)
	options.Environment = moduledependencyEnvironment(options.Environment, "GOWORK", "off")
	limit := options.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: directory, Environment: options.Environment, OutputLimit: limit}, arguments...)
	if err != nil {
		return nil, err
	}
	listed, _, err := decodePackages(output)
	if err != nil {
		return nil, err
	}
	result = make(map[string]listedPackage, len(listed))
	for _, loaded := range listed {
		// A versioned fork has its own module directive when listed as a main
		// module. The captured source directory binds its original import identity.
		if name, exists := directories[loaded.Dir]; exists {
			result[name] = loaded
		}
	}
	return result, nil
}

func moduledependencyEnvironment(environment []string, key, value string) []string {
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
