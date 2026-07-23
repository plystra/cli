// Package interfaceinventory discovers and validates authored Interface
// declarations and shares their eligible Go package-loading boundary with
// Implementation constructor discovery.
package interfaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/importer"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfacedigest"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/module"
)

const (
	defaultOutputLimit = 64 << 20
	maximumSourceSize  = 16 << 20
)

var (
	// ErrDiscover reports failure to construct the visible Interface inventory.
	ErrDiscover = errors.New("discover Interface packages")
	// ErrInvalidOutput reports inconsistent or unsafe go list package output.
	ErrInvalidOutput = errors.New("invalid go list package output")
	// ErrPackage reports an eligible Interface or Implementation package that Go
	// could not load.
	ErrPackage = errors.New("invalid Interface package")
)

// Options controls the bounded read-only Go package-loading command.
type Options struct {
	GoCommand   string
	Environment []string
	OutputLimit int
}

// Interface is one parsed and type-checked Interface declaration with stable
// public module, package, and source provenance.
type Interface struct {
	modulePath          string
	moduleVersion       string
	packagePath         string
	sourcePath          string
	local               bool
	declaration         interfacedecl.Declaration
	contract            interfacecontract.Contract
	contractDigest      string
	documentationDigest string
	exampleDigest       string
	metadata            interfacemeta.Document
	hasMetadata         bool
	constraints         []interfacemeta.ConstraintTarget
	examples            []interfacemeta.Example
	deprecation         interfacemeta.Deprecation
	hasDeprecation      bool
}

// ID returns the exact canonical Interface ID.
func (i Interface) ID() string { return i.declaration.ID().String() }

// ModulePath returns the Go Module path that owns the Interface package.
func (i Interface) ModulePath() string { return i.modulePath }

// ModuleVersion returns the selected module version. It is empty for the
// current Project and dependency Projects supplied by an active workspace.
func (i Interface) ModuleVersion() string { return i.moduleVersion }

// PackagePath returns the canonical Go import path of the Interface package.
func (i Interface) PackagePath() string { return i.packagePath }

// SourcePath returns the stable slash-separated module-relative source path.
func (i Interface) SourcePath() string { return i.sourcePath }

// Source returns stable module-qualified source provenance.
func (i Interface) Source() string {
	version := i.moduleVersion
	if version == "" {
		version = "local"
	}
	position := i.declaration.Position()
	return fmt.Sprintf("%s@%s/%s:%d:%d", i.modulePath, version, i.sourcePath, position.Line, position.Column)
}

// Local reports whether the declaration belongs to the selected current
// Project rather than a dependency Project.
func (i Interface) Local() bool { return i.local }

// Declaration returns the immutable parsed directive declaration.
func (i Interface) Declaration() interfacedecl.Declaration { return i.declaration }

// Contract returns the immutable normalized type-checked Go contract.
func (i Interface) Contract() interfacecontract.Contract { return i.contract }

// ContractDigest returns the versioned SHA-256 digest of the exact normalized
// Go contract and compatibility metadata.
func (i Interface) ContractDigest() string { return i.contractDigest }

// DocumentationDigest returns the versioned SHA-256 digest of normalized
// descriptions and deprecation presentation.
func (i Interface) DocumentationDigest() string { return i.documentationDigest }

// ExampleDigest returns the versioned SHA-256 digest of normalized validated
// request-and-outcome examples.
func (i Interface) ExampleDigest() string { return i.exampleDigest }

// Metadata returns the optional immutable colocated interface.yaml document.
func (i Interface) Metadata() (interfacemeta.Document, bool) {
	return i.metadata, i.hasMetadata
}

// Description returns the optional normalized public Interface description.
func (i Interface) Description() (string, bool) {
	if !i.hasMetadata {
		return "", false
	}
	return i.metadata.Description()
}

// Semantics returns the optional normalized operation semantics declared by
// the Interface package's colocated metadata document.
func (i Interface) Semantics() (interfacemeta.Semantics, bool) {
	if !i.hasMetadata {
		return interfacemeta.Semantics{}, false
	}
	return i.metadata.Semantics()
}

// SemanticErrors returns the immutable code-ordered domain errors declared by
// the Interface package's colocated metadata document.
func (i Interface) SemanticErrors() []interfacemeta.SemanticError {
	if !i.hasMetadata {
		return nil
	}
	return i.metadata.Errors()
}

// ConstraintTargets returns a defensive path-ordered view of metadata
// constraint declarations resolved to canonical Go fields.
func (i Interface) ConstraintTargets() []interfacemeta.ConstraintTarget {
	return append([]interfacemeta.ConstraintTarget(nil), i.constraints...)
}

// Examples returns a defensive name-ordered view of request and response or
// semantic-error examples validated against the canonical Go contract.
func (i Interface) Examples() []interfacemeta.Example {
	return append([]interfacemeta.Example(nil), i.examples...)
}

// Deprecation returns optional lifecycle documentation whose replacement, if
// present, has been validated against the complete visible Interface inventory.
func (i Interface) Deprecation() (interfacemeta.Deprecation, bool) {
	return i.deprecation, i.hasDeprecation
}

// Conformance returns the optional normalized owner-supplied Behavioral
// Conformance Suite configuration without loading or executing suite code.
func (i Interface) Conformance() (interfacemeta.Conformance, bool) {
	if !i.hasMetadata {
		return interfacemeta.Conformance{}, false
	}
	return i.metadata.Conformance()
}

// MetadataSource returns stable module-qualified metadata provenance, or an
// empty string when the Interface package has no optional metadata document.
func (i Interface) MetadataSource() string {
	if !i.hasMetadata {
		return ""
	}
	version := i.moduleVersion
	if version == "" {
		version = "local"
	}
	return fmt.Sprintf("%s@%s/%s", i.modulePath, version, i.metadata.Path())
}

// Index is an immutable deterministic visible Interface inventory. Duplicate
// IDs remain represented independently so the separate duplicate-identity
// validation boundary can report every defining package and source.
type Index struct {
	interfaces []Interface
}

// Interfaces returns a defensive copy ordered by Interface ID and provenance.
func (i Index) Interfaces() []Interface {
	return append([]Interface(nil), i.interfaces...)
}

// Discovery contains the Interface and Implementation declarations obtained
// from one shared eligible-package scan and the same Go-selected source files.
type Discovery struct {
	interfaces      Index
	implementations implementationinventory.Index
}

// Interfaces returns the immutable visible Interface inventory.
func (d Discovery) Interfaces() Index { return d.interfaces }

// Implementations returns the immutable visible Implementation constructor
// inventory obtained from the same package-loading operation.
func (d Discovery) Implementations() implementationinventory.Index { return d.implementations }

// Discover finds candidate authored packages without following links or
// entering reserved paths, asks ordinary Go tooling which source files are
// active, and returns the validated Interface view. The shared scan also
// validates active Implementation directive syntax. It writes neither Project
// files nor dependency sources.
func Discover(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, options Options) (Index, error) {
	discovery, err := DiscoverApplication(ctx, application, dependencies, options)
	if err != nil {
		return Index{}, err
	}
	return discovery.Interfaces(), nil
}

// DiscoverApplication obtains active Interface and Implementation declarations
// through one shared eligible-package scan. Go tooling selects source files and
// supplies compiled type information before either declaration kind is exposed.
func DiscoverApplication(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, options Options) (Discovery, error) {
	if ctx == nil {
		return Discovery{}, fmt.Errorf("%w: context is nil", ErrDiscover)
	}
	if application.Path() == "" || application.ModulePath() == "" {
		return Discovery{}, fmt.Errorf("%w: application module is empty", ErrDiscover)
	}

	sources := []moduleSource{{
		path:  application.ModulePath(),
		root:  application.Path(),
		local: true,
	}}
	for _, dependency := range dependencies.Projects() {
		sources = append(sources, moduleSource{
			path:    dependency.Path(),
			version: dependency.SelectedVersion(),
			root:    dependency.Root(),
		})
	}
	sort.Slice(sources, func(left, right int) bool {
		if sources[left].path != sources[right].path {
			return sources[left].path < sources[right].path
		}
		return sources[left].root < sources[right].root
	})

	interfaces := make([]Interface, 0)
	implementationInputs := make([]implementationinventory.Input, 0)
	for _, source := range sources {
		candidates, err := probeModule(source, application.ModulePath())
		if err != nil {
			return Discovery{}, fmt.Errorf("%w: inspect %s: %w", ErrDiscover, source.label(), err)
		}
		found, err := loadCandidates(ctx, candidates, options)
		if err != nil {
			return Discovery{}, fmt.Errorf("%w: inspect %s: %w", ErrDiscover, source.label(), err)
		}
		interfaces = append(interfaces, found.interfaces...)
		implementationInputs = append(implementationInputs, found.implementations...)
	}

	sort.Slice(interfaces, func(left, right int) bool {
		if interfaces[left].ID() != interfaces[right].ID() {
			return interfaces[left].ID() < interfaces[right].ID()
		}
		if interfaces[left].PackagePath() != interfaces[right].PackagePath() {
			return interfaces[left].PackagePath() < interfaces[right].PackagePath()
		}
		if interfaces[left].SourcePath() != interfaces[right].SourcePath() {
			return interfaces[left].SourcePath() < interfaces[right].SourcePath()
		}
		leftPosition := interfaces[left].declaration.Position()
		rightPosition := interfaces[right].declaration.Position()
		if leftPosition.Line != rightPosition.Line {
			return leftPosition.Line < rightPosition.Line
		}
		return leftPosition.Column < rightPosition.Column
	})
	visibleIDs := make(map[string]struct{}, len(interfaces))
	for index := range interfaces {
		visibleIDs[interfaces[index].ID()] = struct{}{}
	}
	for index := range interfaces {
		deprecation, present, err := interfacemeta.ResolveDeprecation(interfaces[index].metadata, interfaces[index].contract, visibleIDs)
		if err != nil {
			version := interfaces[index].moduleVersion
			if version == "" {
				version = "local"
			}
			return Discovery{}, fmt.Errorf("%w: inspect %s@%s: package %s: %w", ErrDiscover, interfaces[index].modulePath, version, interfaces[index].packagePath, err)
		}
		interfaces[index].deprecation = deprecation
		interfaces[index].hasDeprecation = present
	}
	canonicalInterfaces := make([]implementationinventory.InterfaceInput, len(interfaces))
	for index, discovered := range interfaces {
		canonicalInterfaces[index] = implementationinventory.InterfaceInput{
			ID:          discovered.Declaration().ID(),
			PackagePath: discovered.PackagePath(),
		}
	}
	implementations, err := implementationinventory.Build(implementationInputs, canonicalInterfaces)
	if err != nil {
		return Discovery{}, fmt.Errorf("%w: %w", ErrDiscover, err)
	}
	return Discovery{
		interfaces:      Index{interfaces: interfaces},
		implementations: implementations,
	}, nil
}

type loadedInventory struct {
	interfaces      []Interface
	implementations []implementationinventory.Input
}

func loadCandidates(ctx context.Context, candidates []packageCandidate, options Options) (loadedInventory, error) {
	if len(candidates) == 0 {
		return loadedInventory{}, nil
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].importPath < candidates[right].importPath
	})
	for index := 1; index < len(candidates); index++ {
		if candidates[index-1].importPath == candidates[index].importPath {
			return loadedInventory{}, fmt.Errorf("duplicate candidate package %q", candidates[index].importPath)
		}
	}
	limit := options.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	arguments := []string{"list", "-deps", "-export", "-json", "-e", "-mod=readonly"}
	for _, candidate := range candidates {
		arguments = append(arguments, candidate.pattern)
	}
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   candidates[0].source.root,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: limit,
	}, arguments...)
	if err != nil {
		return loadedInventory{}, fmt.Errorf("load eligible Go packages: %w", err)
	}

	listed, exports, err := decodePackages(output)
	if err != nil {
		return loadedInventory{}, err
	}
	checkedImporter := importer.ForCompiler(token.NewFileSet(), runtime.Compiler, exportLookup(exports))
	result := loadedInventory{}
	for _, candidate := range candidates {
		loaded, exists := listed[candidate.importPath]
		if !exists {
			return loadedInventory{}, fmt.Errorf("%w: Go omitted candidate package %q", ErrInvalidOutput, candidate.importPath)
		}
		declarations, err := parseDeclarations(candidate, loaded)
		if err != nil {
			return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
		}
		if len(declarations.interfaces) == 0 && len(declarations.implementations) == 0 {
			continue
		}
		if err := validateLoadedPackage(candidate, loaded); err != nil {
			return loadedInventory{}, err
		}
		checkedPackage, err := checkedImporter.Import(candidate.importPath)
		if err != nil {
			return loadedInventory{}, fmt.Errorf("%w: import compiled package %s: %s", ErrPackage, candidate.importPath, sanitizePackageError(err.Error(), candidate))
		}
		if checkedPackage.Path() != candidate.importPath || checkedPackage.Name() != loaded.Name {
			return loadedInventory{}, fmt.Errorf("%w: compiled package identity for %q is %s %q", ErrInvalidOutput, candidate.importPath, checkedPackage.Path(), checkedPackage.Name())
		}
		for _, declaration := range declarations.implementations {
			if declaration.PackageName() != loaded.Name {
				return loadedInventory{}, fmt.Errorf("%w: %s declares package %q, Go selected %q", ErrInvalidOutput, declaration.Position().Path, declaration.PackageName(), loaded.Name)
			}
			result.implementations = append(result.implementations, implementationinventory.Input{
				ModulePath:    candidate.source.path,
				ModuleVersion: candidate.source.version,
				PackagePath:   candidate.importPath,
				Local:         candidate.source.local,
				Declaration:   declaration,
				Types:         checkedPackage,
			})
		}
		if len(declarations.interfaces) == 0 {
			continue
		}
		metadata, hasMetadata, err := loadOptionalMetadata(candidate)
		if err != nil {
			return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
		}
		if err := validateConformancePackage(candidate, metadata); err != nil {
			return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
		}
		for _, declaration := range declarations.interfaces {
			if declaration.PackageName() != loaded.Name {
				return loadedInventory{}, fmt.Errorf("%w: %s declares package %q, Go selected %q", ErrInvalidOutput, declaration.Position().Path, declaration.PackageName(), loaded.Name)
			}
			contract, err := interfacecontract.Validate(declaration, checkedPackage)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
			}
			constraints, err := interfacemeta.ResolveConstraintTargets(metadata, contract)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
			}
			examples, err := interfacemeta.ResolveExamples(metadata, contract)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: %w", candidate.importPath, err)
			}
			contractDigest, err := interfacedigest.CalculateContract(contract, metadata, constraints)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: calculate Interface contract digest: %w", candidate.importPath, err)
			}
			documentationDigest, err := interfacedigest.CalculateDocumentation(contract, metadata)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: calculate Interface documentation digest: %w", candidate.importPath, err)
			}
			exampleDigest, err := interfacedigest.CalculateExamples(contract, examples)
			if err != nil {
				return loadedInventory{}, fmt.Errorf("package %s: calculate Interface example digest: %w", candidate.importPath, err)
			}
			result.interfaces = append(result.interfaces, Interface{
				modulePath:          candidate.source.path,
				moduleVersion:       candidate.source.version,
				packagePath:         candidate.importPath,
				sourcePath:          declaration.Position().Path,
				local:               candidate.source.local,
				declaration:         declaration,
				contract:            contract,
				contractDigest:      contractDigest,
				documentationDigest: documentationDigest,
				exampleDigest:       exampleDigest,
				metadata:            metadata,
				hasMetadata:         hasMetadata,
				constraints:         constraints,
				examples:            examples,
			})
		}
	}
	return result, nil
}

func loadOptionalMetadata(candidate packageCandidate) (interfacemeta.Document, bool, error) {
	relativeDirectory, err := filepath.Rel(candidate.source.root, candidate.directory)
	if err != nil || filepath.IsAbs(relativeDirectory) || relativeDirectory == ".." || strings.HasPrefix(relativeDirectory, ".."+string(filepath.Separator)) {
		return interfacemeta.Document{}, false, fmt.Errorf("%w: metadata package directory escapes its module root", interfacemeta.ErrInvalid)
	}
	relativePath := interfacemeta.Name
	if relativeDirectory != "." {
		relativePath = path.Join(filepath.ToSlash(relativeDirectory), interfacemeta.Name)
	}
	absolutePath := filepath.Join(candidate.directory, interfacemeta.Name)
	info, err := os.Lstat(absolutePath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return interfacemeta.Document{}, false, nil
	case err != nil:
		message := gocommand.SanitizeOutput(err.Error(), candidate.source.root, candidate.directory, absolutePath)
		return interfacemeta.Document{}, false, fmt.Errorf("%w: %s: inspect metadata: %s", interfacemeta.ErrInvalid, relativePath, message)
	case !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0:
		return interfacemeta.Document{}, false, fmt.Errorf("%w: %s must be a regular non-symbolic file", interfacemeta.ErrInvalid, relativePath)
	case info.Size() > interfacemeta.MaximumSize:
		return interfacemeta.Document{}, false, fmt.Errorf("%w: %s exceeds %d bytes", interfacemeta.ErrInvalid, relativePath, interfacemeta.MaximumSize)
	}
	data, err := readRegularFile(absolutePath, interfacemeta.MaximumSize)
	if err != nil {
		message := gocommand.SanitizeOutput(err.Error(), candidate.source.root, candidate.directory, absolutePath)
		return interfacemeta.Document{}, false, fmt.Errorf("%w: %s: read metadata: %s", interfacemeta.ErrInvalid, relativePath, message)
	}
	document, err := interfacemeta.ParseFile(relativePath, data)
	if err != nil {
		return interfacemeta.Document{}, false, err
	}
	return document, true, nil
}

func validateConformancePackage(candidate packageCandidate, metadata interfacemeta.Document) error {
	conformance, present := metadata.Conformance()
	if !present {
		return nil
	}
	packageDirectory := filepath.Join(candidate.directory, strings.TrimPrefix(conformance.Package(), "./"))
	relativeDirectory, err := filepath.Rel(candidate.source.root, packageDirectory)
	if err != nil || filepath.IsAbs(relativeDirectory) || relativeDirectory == ".." || strings.HasPrefix(relativeDirectory, ".."+string(filepath.Separator)) {
		return invalidConformancePackage(metadata, "package path escapes its owning Go Module")
	}
	info, err := os.Lstat(packageDirectory)
	if err != nil {
		message := gocommand.SanitizeOutput(err.Error(), candidate.source.root, candidate.directory, packageDirectory)
		return invalidConformancePackage(metadata, "inspect %s: %s", path.Join(path.Dir(metadata.Path()), "conformance"), message)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return invalidConformancePackage(metadata, "%s must be a non-symbolic directory", path.Join(path.Dir(metadata.Path()), "conformance"))
	}
	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		message := gocommand.SanitizeOutput(err.Error(), candidate.source.root, candidate.directory, packageDirectory)
		return invalidConformancePackage(metadata, "inspect %s: %s", path.Join(path.Dir(metadata.Path()), "conformance"), message)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			message := gocommand.SanitizeOutput(err.Error(), candidate.source.root, candidate.directory, filepath.Join(packageDirectory, entry.Name()))
			return invalidConformancePackage(metadata, "inspect conformance source %s: %s", entry.Name(), message)
		}
		if entryInfo.Mode().IsRegular() && entryInfo.Mode()&fs.ModeSymlink == 0 {
			return nil
		}
	}
	return invalidConformancePackage(metadata, "%s must contain at least one regular non-symbolic .go source file", path.Join(path.Dir(metadata.Path()), "conformance"))
}

func invalidConformancePackage(metadata interfacemeta.Document, format string, arguments ...any) error {
	return fmt.Errorf("%w: %w: %s: %s", interfacemeta.ErrInvalid, interfacemeta.ErrInvalidConformance, metadata.Path(), fmt.Sprintf(format, arguments...))
}

type moduleSource struct {
	path    string
	version string
	root    string
	local   bool
}

func (s moduleSource) label() string {
	version := s.version
	if version == "" {
		version = "local"
	}
	return s.path + "@" + version
}

type packageCandidate struct {
	source     moduleSource
	importPath string
	pattern    string
	directory  string
}

func probeModule(source moduleSource, applicationPath string) ([]packageCandidate, error) {
	if source.path == "" || source.root == "" {
		return nil, errors.New("module source is empty")
	}
	if err := modulepath.CheckProject(source.path); err != nil {
		return nil, fmt.Errorf("invalid module path %q: %v", source.path, err)
	}
	root, err := filepath.Abs(source.root)
	if err != nil {
		return nil, fmt.Errorf("resolve module root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve module root links: %w", err)
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("module root is not a regular non-symbolic directory")
	}

	packageDirectories := make(map[string]struct{})
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if current == root {
				return nil
			}
			if reservedDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			nested, err := nestedModuleRoot(current)
			if err != nil {
				return err
			}
			if nested {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		data, err := readRegularFile(current, maximumSourceSize)
		if err != nil {
			return err
		}
		if hasDeclarationDirectiveComment(current, data) {
			packageDirectories[filepath.Dir(current)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	directories := make([]string, 0, len(packageDirectories))
	for directory := range packageDirectories {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	candidates := make([]packageCandidate, 0, len(directories))
	for _, directory := range directories {
		relative, err := filepath.Rel(root, directory)
		if err != nil {
			return nil, fmt.Errorf("locate candidate package: %w", err)
		}
		relative = filepath.ToSlash(relative)
		importPath := source.path
		pattern := "."
		if relative != "." {
			importPath = path.Join(source.path, relative)
			pattern = "./" + relative
		}
		if err := module.CheckImportPath(importPath); err != nil {
			return nil, fmt.Errorf("candidate package %q has invalid import path: %v", relative, err)
		}
		if !importableFrom(applicationPath, importPath) {
			continue
		}
		candidates = append(candidates, packageCandidate{
			source:     source,
			importPath: importPath,
			pattern:    pattern,
			directory:  directory,
		})
	}
	return candidates, nil
}

func reservedDirectory(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch strings.ToLower(name) {
	case "generated", "vendor", "testdata", "fixture", "fixtures", "dist":
		return true
	default:
		return false
	}
}

func nestedModuleRoot(directory string) (bool, error) {
	info, err := os.Lstat(filepath.Join(directory, "go.mod"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect nested go.mod: %w", err)
	case info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0:
		return true, nil
	default:
		return true, nil
	}
}

func importableFrom(importerPath, importedPath string) bool {
	index := strings.LastIndex(importedPath, "/internal/")
	if index < 0 && strings.HasSuffix(importedPath, "/internal") {
		index = len(importedPath) - len("/internal")
	}
	if index < 0 {
		return true
	}
	parent := importedPath[:index]
	return importerPath == parent || strings.HasPrefix(importerPath, parent+"/")
}

func hasInterfaceDirectiveComment(filename string, source []byte) bool {
	return hasDirectiveComment(filename, source, "//plystra:interface", "/*plystra:interface")
}

func hasImplementationDirectiveComment(filename string, source []byte) bool {
	return hasDirectiveComment(filename, source, "//plystra:implements", "/*plystra:implements")
}

func hasDeclarationDirectiveComment(filename string, source []byte) bool {
	return hasDirectiveComment(filename, source,
		"//plystra:interface", "/*plystra:interface",
		"//plystra:implements", "/*plystra:implements",
	)
}

func hasDirectiveComment(filename string, source []byte, prefixes ...string) bool {
	files := token.NewFileSet()
	file := files.AddFile(filename, -1, len(source))
	var lexical scanner.Scanner
	lexical.Init(file, source, nil, scanner.ScanComments)
	for {
		_, tokenKind, literal := lexical.Scan()
		if tokenKind == token.EOF {
			return false
		}
		if tokenKind != token.COMMENT {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(literal, prefix) {
				return true
			}
		}
	}
}

type listedPackage struct {
	Dir        string                `json:"Dir"`
	ImportPath string                `json:"ImportPath"`
	Name       string                `json:"Name"`
	Export     string                `json:"Export"`
	GoFiles    []string              `json:"GoFiles"`
	CgoFiles   []string              `json:"CgoFiles"`
	Incomplete bool                  `json:"Incomplete"`
	Error      *listedPackageError   `json:"Error"`
	DepsErrors []*listedPackageError `json:"DepsErrors"`
	Module     *listedModule         `json:"Module"`
}

type listedPackageError struct {
	ImportStack []string `json:"ImportStack"`
	Pos         string   `json:"Pos"`
	Err         string   `json:"Err"`
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace"`
}

func decodePackages(data []byte) (map[string]listedPackage, map[string]string, error) {
	packages := make(map[string]listedPackage)
	exports := make(map[string]string)
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var loaded listedPackage
		err := decoder.Decode(&loaded)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%w: decode JSON: %v", ErrInvalidOutput, err)
		}
		if loaded.ImportPath == "" || strings.IndexByte(loaded.ImportPath, 0) >= 0 {
			return nil, nil, fmt.Errorf("%w: Go returned an empty or unsafe import path", ErrInvalidOutput)
		}
		if _, duplicate := packages[loaded.ImportPath]; duplicate {
			return nil, nil, fmt.Errorf("%w: Go returned package %q more than once", ErrInvalidOutput, loaded.ImportPath)
		}
		packages[loaded.ImportPath] = loaded
		if loaded.Export != "" {
			if !filepath.IsAbs(loaded.Export) || strings.IndexByte(loaded.Export, 0) >= 0 {
				return nil, nil, fmt.Errorf("%w: package %q has unsafe export data provenance", ErrInvalidOutput, loaded.ImportPath)
			}
			exports[loaded.ImportPath] = loaded.Export
		}
	}
	return packages, exports, nil
}

type packageDeclarations struct {
	interfaces      []interfacedecl.Declaration
	implementations []implementationdecl.Declaration
}

func parseDeclarations(candidate packageCandidate, loaded listedPackage) (packageDeclarations, error) {
	if !sameDirectory(candidate.directory, loaded.Dir) {
		return packageDeclarations{}, fmt.Errorf("%w: package %q directory does not match its candidate source", ErrInvalidOutput, candidate.importPath)
	}
	fileNames := append(append([]string(nil), loaded.GoFiles...), loaded.CgoFiles...)
	sort.Strings(fileNames)
	declarations := packageDeclarations{}
	for index, fileName := range fileNames {
		if index > 0 && fileNames[index-1] == fileName {
			return packageDeclarations{}, fmt.Errorf("%w: package %q lists source %q more than once", ErrInvalidOutput, candidate.importPath, fileName)
		}
		if fileName == "" || filepath.Base(fileName) != fileName || !strings.HasSuffix(fileName, ".go") {
			return packageDeclarations{}, fmt.Errorf("%w: package %q lists unsafe Go source %q", ErrInvalidOutput, candidate.importPath, fileName)
		}
		absolutePath := filepath.Join(candidate.directory, fileName)
		data, err := readRegularFile(absolutePath, maximumSourceSize)
		if err != nil {
			return packageDeclarations{}, fmt.Errorf("read selected Go source %s: %w", fileName, err)
		}
		hasInterface := hasInterfaceDirectiveComment(fileName, data)
		hasImplementation := hasImplementationDirectiveComment(fileName, data)
		if !hasInterface && !hasImplementation {
			continue
		}
		relativePath, err := filepath.Rel(candidate.source.root, absolutePath)
		if err != nil || relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return packageDeclarations{}, fmt.Errorf("%w: package %q source %q escapes its module root", ErrInvalidOutput, candidate.importPath, fileName)
		}
		sourcePath := filepath.ToSlash(relativePath)
		if hasInterface {
			parsed, err := interfacedecl.ParseFile(sourcePath, data)
			if err != nil {
				return packageDeclarations{}, err
			}
			declarations.interfaces = append(declarations.interfaces, parsed...)
		}
		if hasImplementation {
			parsed, err := implementationdecl.ParseFile(sourcePath, data)
			if err != nil {
				return packageDeclarations{}, err
			}
			declarations.implementations = append(declarations.implementations, parsed...)
		}
	}
	sort.Slice(declarations.interfaces, func(left, right int) bool {
		leftPosition := declarations.interfaces[left].Position()
		rightPosition := declarations.interfaces[right].Position()
		if leftPosition.Path != rightPosition.Path {
			return leftPosition.Path < rightPosition.Path
		}
		if leftPosition.Line != rightPosition.Line {
			return leftPosition.Line < rightPosition.Line
		}
		return leftPosition.Column < rightPosition.Column
	})
	sort.Slice(declarations.implementations, func(left, right int) bool {
		leftPosition := declarations.implementations[left].Position()
		rightPosition := declarations.implementations[right].Position()
		if leftPosition.Path != rightPosition.Path {
			return leftPosition.Path < rightPosition.Path
		}
		if leftPosition.Line != rightPosition.Line {
			return leftPosition.Line < rightPosition.Line
		}
		return leftPosition.Column < rightPosition.Column
	})
	return declarations, nil
}

func validateLoadedPackage(candidate packageCandidate, loaded listedPackage) error {
	if loaded.Name == "" {
		return fmt.Errorf("%w: package %s has no Go package name", ErrInvalidOutput, candidate.importPath)
	}
	if loaded.Name == "main" {
		return fmt.Errorf("%w: package %s is a program and cannot define an importable Interface or Implementation", ErrPackage, candidate.importPath)
	}
	if loaded.Module == nil || loaded.Module.Path != candidate.source.path {
		return fmt.Errorf("%w: package %q has inconsistent module provenance", ErrInvalidOutput, candidate.importPath)
	}
	if loaded.Error != nil || loaded.Incomplete || len(loaded.DepsErrors) != 0 {
		message := "Go could not load the package"
		if loaded.Error != nil && loaded.Error.Err != "" {
			message = loaded.Error.Err
		} else {
			for _, dependencyError := range loaded.DepsErrors {
				if dependencyError != nil && dependencyError.Err != "" {
					message = dependencyError.Err
					break
				}
			}
		}
		return packageError(candidate, loaded, message)
	}
	if loaded.Export == "" {
		return packageError(candidate, loaded, "Go produced no compiled export data")
	}
	return nil
}

func packageError(candidate packageCandidate, loaded listedPackage, message string) error {
	return fmt.Errorf("%w: package %s: %s", ErrPackage, candidate.importPath, sanitizePackageError(message, candidate, loaded.Dir))
}

func sanitizePackageError(message string, candidate packageCandidate, extraPaths ...string) string {
	privatePaths := []string{candidate.source.root, candidate.directory}
	privatePaths = append(privatePaths, extraPaths...)
	message = gocommand.SanitizeOutput(message, privatePaths...)
	if message == "" {
		return "Go could not load the package"
	}
	return message
}

func exportLookup(exports map[string]string) importer.Lookup {
	return func(importPath string) (io.ReadCloser, error) {
		exportPath, exists := exports[importPath]
		if !exists {
			return nil, fmt.Errorf("compiled export data for %q is unavailable", importPath)
		}
		return openRegularFile(exportPath)
	}
}

func readRegularFile(name string, limit int64) ([]byte, error) {
	file, err := openRegularFile(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("regular file exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("regular file exceeds %d bytes", limit)
	}
	after, err := os.Lstat(name)
	if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("regular file changed while it was read")
	}
	return data, nil
}

func openRegularFile(name string) (*os.File, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, errors.New("source is not a regular non-symbolic file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || before.Mode() != opened.Mode() || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) {
		_ = file.Close()
		return nil, errors.New("regular file changed before it was opened")
	}
	return file, nil
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" || !filepath.IsAbs(right) {
		return false
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && leftInfo.IsDir() && rightInfo.IsDir() && os.SameFile(leftInfo, rightInfo)
}
