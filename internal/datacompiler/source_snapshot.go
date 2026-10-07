package datacompiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/interfaceinventory"
	"golang.org/x/mod/module"
)

const (
	// DeclarationImportPath is the only compiler package included in the
	// source snapshot. It is support source for type checking, never a root.
	DeclarationImportPath = ModulePath + "/declaration"

	// MaxSourceSnapshotPackages bounds the package inventory, including the
	// selected compiler declaration package.
	MaxSourceSnapshotPackages = MaxImports + 1
	MaxSourceSnapshotFiles    = MaxFrameBytes / 64
	MaxSourceSnapshotBytes    = MaxFrameBytes
	RootEligibilityEligible   = "eligible"
	RootEligibilitySupport    = "support"
	ResourceTypeName          = "Resource"
)

var (
	// ErrSourceSnapshot identifies failure to build a Data analyze source
	// snapshot.
	ErrSourceSnapshot = errors.New("build Data analyze source snapshot")
	// ErrInvalidSourceSnapshotInput identifies incomplete or unsafe builder
	// inputs.
	ErrInvalidSourceSnapshotInput = errors.New("invalid Data analyze source snapshot input")
	// ErrSourceSnapshotDrift identifies a source changed after its selected
	// identity was captured.
	ErrSourceSnapshotDrift = errors.New("Data analyze source snapshot changed during capture")
	// ErrSourceSnapshotBounds identifies a finite snapshot limit breach.
	ErrSourceSnapshotBounds = errors.New("Data analyze source snapshot exceeds its bounds")
)

// SnapshotModule supplies a CLI-only source root for one selected Go Module.
// Roots are used only while reading and never enter AnalyzeSourceSnapshot.
type SnapshotModule struct {
	ModulePath    string
	ModuleVersion string
	Root          string
}

// AnalyzeSourceSnapshotOptions identifies the already selected source and
// compiler inputs for one read-only snapshot operation.
type AnalyzeSourceSnapshotOptions struct {
	DataPackages []interfaceinventory.DataPackage
	// ResourcePackages identifies authored Resource contracts whose source and
	// non-standard import closure must be available for Data access typing.
	ResourcePackages []interfaceinventory.Resource
	Modules          []SnapshotModule
	// GoDirectory is the selected Project directory used for one package-graph
	// query. It is kept outside the serialized snapshot.
	GoDirectory string
	Compiler    Selection
	GoCommand   string
	Environment []string
	OutputLimit int
}

// AnalyzeSourceFile is one immutable module-relative source file. Content is
// encoded by encoding/json as the Data protocol's base64 JSON byte string.
type AnalyzeSourceFile struct {
	path    string
	content []byte
	digest  string
	bytes   int
}

// Path returns the slash-separated module-relative source path.
func (f AnalyzeSourceFile) Path() string { return f.path }

// Content returns a defensive copy of the source bytes.
func (f AnalyzeSourceFile) Content() []byte { return append([]byte(nil), f.content...) }

// Digest returns the SHA-256 digest of Content.
func (f AnalyzeSourceFile) Digest() string { return f.digest }

// Bytes returns the exact source byte count.
func (f AnalyzeSourceFile) Bytes() int { return f.bytes }

func (f AnalyzeSourceFile) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Path    string `json:"path"`
		Content []byte `json:"content"`
	}{Path: f.path, Content: f.content})
}

// AnalyzeSourcePackage is one finite source package with an explicit root
// eligibility marker. Support packages are available for type checking only.
type AnalyzeSourcePackage struct {
	importPath      string
	rootEligibility string
	files           []AnalyzeSourceFile
}

// AnalyzeResourceContract is the validated Resource identity supplied to the
// independent Data analyzer. The CLI does not expose the compiled Go type.
type AnalyzeResourceContract struct {
	ImportPath string `json:"import_path"`
	TypeName   string `json:"type_name"`
	ID         string `json:"id"`
}

// ImportPath returns the package's canonical Go import path.
func (p AnalyzeSourcePackage) ImportPath() string { return p.importPath }

// RootEligibility returns either "eligible" or "support".
func (p AnalyzeSourcePackage) RootEligibility() string { return p.rootEligibility }

// Files returns defensive copies of the selected source files.
func (p AnalyzeSourcePackage) Files() []AnalyzeSourceFile {
	files := make([]AnalyzeSourceFile, len(p.files))
	for index, file := range p.files {
		files[index] = AnalyzeSourceFile{
			path: file.path, content: append([]byte(nil), file.content...),
			digest: file.digest, bytes: file.bytes,
		}
	}
	return files
}

func (p AnalyzeSourcePackage) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ImportPath      string              `json:"import_path"`
		RootEligibility string              `json:"root_eligibility"`
		Files           []AnalyzeSourceFile `json:"files"`
	}{ImportPath: p.importPath, RootEligibility: p.rootEligibility, Files: p.files})
}

// AnalyzeSourceSnapshot is the finite source portion of a Data analyze
// request. It contains no filesystem roots, compiler executable paths, or
// absolute source paths.
type AnalyzeSourceSnapshot struct {
	packages  []AnalyzeSourcePackage
	resources []AnalyzeResourceContract
	jsonLimit int
}

// Packages returns the deterministic package inventory with defensive source
// copies.
func (s AnalyzeSourceSnapshot) Packages() []AnalyzeSourcePackage {
	packages := make([]AnalyzeSourcePackage, len(s.packages))
	for index, pkg := range s.packages {
		packages[index] = AnalyzeSourcePackage{
			importPath: pkg.importPath, rootEligibility: pkg.rootEligibility,
			files: pkg.Files(),
		}
	}
	return packages
}

// ResourceContracts returns the deterministic validated Resource identities.
func (s AnalyzeSourceSnapshot) ResourceContracts() []AnalyzeResourceContract {
	return append([]AnalyzeResourceContract(nil), s.resources...)
}

// JSON returns the bounded Data analyzer source snapshot object. The caller
// can place this object in the protocol's larger snapshot envelope.
func (s AnalyzeSourceSnapshot) JSON() ([]byte, error) {
	data, err := json.Marshal(struct {
		Packages  []AnalyzeSourcePackage    `json:"packages"`
		Resources []AnalyzeResourceContract `json:"resources"`
	}{Packages: s.packages, Resources: s.resources})
	if err != nil {
		return nil, fmt.Errorf("%w: marshal source snapshot: %v", ErrSourceSnapshot, err)
	}
	limit := s.jsonLimit
	if limit <= 0 || limit > MaxFrameBytes {
		limit = MaxFrameBytes
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%w: JSON is %d bytes", ErrSourceSnapshotBounds, len(data))
	}
	return data, nil
}

// Digest returns the deterministic identity of package paths, root markers,
// selected source paths, sizes, and source digests. It excludes source bytes.
func (s AnalyzeSourceSnapshot) Digest() string {
	type fileIdentity struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
		Bytes  int    `json:"bytes"`
	}
	type packageIdentity struct {
		ImportPath      string         `json:"import_path"`
		RootEligibility string         `json:"root_eligibility"`
		Files           []fileIdentity `json:"files"`
	}
	packages := make([]packageIdentity, len(s.packages))
	for index, pkg := range s.packages {
		packages[index] = packageIdentity{
			ImportPath: pkg.importPath, RootEligibility: pkg.rootEligibility,
			Files: make([]fileIdentity, len(pkg.files)),
		}
		for fileIndex, file := range pkg.files {
			packages[index].Files[fileIndex] = fileIdentity{Path: file.path, Digest: file.digest, Bytes: file.bytes}
		}
	}
	resources := append([]AnalyzeResourceContract(nil), s.resources...)
	sort.Slice(resources, func(left, right int) bool {
		if resources[left].ID != resources[right].ID {
			return resources[left].ID < resources[right].ID
		}
		if resources[left].ImportPath != resources[right].ImportPath {
			return resources[left].ImportPath < resources[right].ImportPath
		}
		return resources[left].TypeName < resources[right].TypeName
	})
	data, _ := json.Marshal(struct {
		Packages  []packageIdentity         `json:"packages"`
		Resources []AnalyzeResourceContract `json:"resources"`
	}{Packages: packages, Resources: resources})
	sum := sha256.Sum256(append([]byte("plystra.data-source-snapshot/v1\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// BuildAnalyzeSourceSnapshot captures the selected source bytes for active
// Data packages and the exact selected compiler declaration package. Go's
// package selection determines declaration files; the captured inventory
// identities determine application files. The operation writes no source.
func BuildAnalyzeSourceSnapshot(ctx context.Context, options AnalyzeSourceSnapshotOptions) (AnalyzeSourceSnapshot, error) {
	if ctx == nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: context is nil", ErrSourceSnapshot)
	}
	if err := validateSnapshotOptions(options); err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	compiler, err := verifyCompilerSelection(options.Compiler)
	if err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	modules, err := normalizeSnapshotModules(options.Modules)
	if err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	packages := append([]interfaceinventory.DataPackage(nil), options.DataPackages...)
	sort.Slice(packages, func(left, right int) bool {
		if packages[left].ModulePath() != packages[right].ModulePath() {
			return packages[left].ModulePath() < packages[right].ModulePath()
		}
		if packages[left].ModuleVersion() != packages[right].ModuleVersion() {
			return packages[left].ModuleVersion() < packages[right].ModuleVersion()
		}
		return packages[left].ImportPath() < packages[right].ImportPath()
	})
	result := AnalyzeSourceSnapshot{
		packages:  make([]AnalyzeSourcePackage, 0, len(packages)+1),
		jsonLimit: compiler.Manifest.Bounds.MaxFrameBytes,
	}
	resources, err := normalizeResourceContracts(options.ResourcePackages)
	if err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	result.resources = resources
	seenPackages := make(map[string]struct{}, len(packages)+1)
	captured := make([]capturedSourcePackage, 0)
	budget := sourceSnapshotBudget{maxBytes: MaxSourceSnapshotBytes}
	if compiler.Manifest.Bounds.MaxFrameBytes < budget.maxBytes {
		budget.maxBytes = compiler.Manifest.Bounds.MaxFrameBytes
	}
	for _, pkg := range packages {
		if err := ctx.Err(); err != nil {
			return AnalyzeSourceSnapshot{}, err
		}
		if err := budget.addPackage(pkg.ImportPath()); err != nil {
			return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
		}
		if err := validateDataPackage(pkg); err != nil {
			return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
		}
		key := pkg.ImportPath()
		if _, exists := seenPackages[key]; exists {
			return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: duplicate package %q", ErrInvalidSourceSnapshotInput, key)
		}
		seenPackages[key] = struct{}{}
		moduleRoot, exists := modules[moduleKey(pkg.ModulePath(), pkg.ModuleVersion())]
		if !exists {
			return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: source root for %s is unavailable", ErrInvalidSourceSnapshotInput, pkg.ModulePath())
		}
		files, err := captureInventoryFiles(ctx, moduleRoot, pkg.Files(), &budget)
		if err != nil {
			return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: package %q: %w", ErrSourceSnapshot, pkg.ImportPath(), err)
		}
		result.packages = append(result.packages, AnalyzeSourcePackage{
			importPath: pkg.ImportPath(), rootEligibility: RootEligibilityEligible, files: files,
		})
		captured = append(captured, capturedSourcePackage{importPath: pkg.ImportPath(), root: moduleRoot, files: files})
	}

	support, supportCaptured, err := captureSupportPackages(ctx, options, compiler, modules, packages, seenPackages, &budget)
	if err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	for _, pkg := range support {
		seenPackages[pkg.importPath] = struct{}{}
		result.packages = append(result.packages, AnalyzeSourcePackage{
			importPath: pkg.importPath, rootEligibility: RootEligibilitySupport, files: pkg.files,
		})
	}
	captured = append(captured, supportCaptured...)

	if err := budget.addPackage(DeclarationImportPath); err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	declarationFiles, err := captureDeclarationFiles(ctx, compiler, options, &budget)
	if err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	if _, exists := seenPackages[DeclarationImportPath]; exists {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: declaration package is also an eligible package", ErrInvalidSourceSnapshotInput)
	}
	result.packages = append(result.packages, AnalyzeSourcePackage{
		importPath: DeclarationImportPath, rootEligibility: RootEligibilitySupport, files: declarationFiles,
	})
	captured = append(captured, capturedSourcePackage{importPath: DeclarationImportPath, root: compiler.Root, files: declarationFiles})
	sort.Slice(result.packages, func(left, right int) bool {
		return result.packages[left].importPath < result.packages[right].importPath
	})
	if len(result.packages) > MaxSourceSnapshotPackages {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: package count %d", ErrSourceSnapshotBounds, len(result.packages))
	}
	if _, err := result.JSON(); err != nil {
		return AnalyzeSourceSnapshot{}, err
	}
	if err := recheckCapturedPackages(ctx, captured); err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	if err := recheckCompilerSelection(compiler); err != nil {
		return AnalyzeSourceSnapshot{}, fmt.Errorf("%w: %w", ErrSourceSnapshot, err)
	}
	return result, nil
}

type capturedSourcePackage struct {
	importPath string
	root       string
	files      []AnalyzeSourceFile
}

type supportSourcePackage struct {
	importPath    string
	modulePath    string
	moduleVersion string
	root          string
	directory     string
	files         []AnalyzeSourceFile
}

func normalizeResourceContracts(resources []interfaceinventory.Resource) ([]AnalyzeResourceContract, error) {
	result := make([]AnalyzeResourceContract, 0, len(resources))
	seenIDs := make(map[string]struct{}, len(resources))
	seenTypes := make(map[string]struct{}, len(resources))
	for _, resource := range resources {
		if err := module.CheckImportPath(resource.PackagePath()); err != nil || strings.TrimSpace(resource.ID()) == "" {
			return nil, fmt.Errorf("%w: Resource identity is invalid", ErrInvalidSourceSnapshotInput)
		}
		key := resource.PackagePath() + "\x00" + ResourceTypeName
		if _, exists := seenTypes[key]; exists {
			return nil, fmt.Errorf("%w: Resource contract %q is repeated", ErrInvalidSourceSnapshotInput, key)
		}
		if _, exists := seenIDs[resource.ID()]; exists {
			return nil, fmt.Errorf("%w: Resource ID %q is repeated", ErrInvalidSourceSnapshotInput, resource.ID())
		}
		seenTypes[key] = struct{}{}
		seenIDs[resource.ID()] = struct{}{}
		result = append(result, AnalyzeResourceContract{ImportPath: resource.PackagePath(), TypeName: ResourceTypeName, ID: resource.ID()})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].ID != result[right].ID {
			return result[left].ID < result[right].ID
		}
		return result[left].ImportPath < result[right].ImportPath
	})
	return result, nil
}

func captureSupportPackages(ctx context.Context, options AnalyzeSourceSnapshotOptions, compiler Selection, modules map[string]string, dataPackages []interfaceinventory.DataPackage, eligible map[string]struct{}, budget *sourceSnapshotBudget) ([]supportSourcePackage, []capturedSourcePackage, error) {
	roots := make([]string, 0, len(dataPackages)+len(options.ResourcePackages))
	seenRoots := make(map[string]struct{}, cap(roots))
	for _, pkg := range dataPackages {
		if _, exists := seenRoots[pkg.ImportPath()]; !exists {
			seenRoots[pkg.ImportPath()] = struct{}{}
			roots = append(roots, pkg.ImportPath())
		}
	}
	for _, resource := range options.ResourcePackages {
		if _, exists := seenRoots[resource.PackagePath()]; !exists {
			seenRoots[resource.PackagePath()] = struct{}{}
			roots = append(roots, resource.PackagePath())
		}
	}
	if len(roots) == 0 {
		return nil, nil, nil
	}
	sort.Strings(roots)
	directory := options.GoDirectory
	if directory == "" {
		for _, pkg := range dataPackages {
			if root, exists := modules[moduleKey(pkg.ModulePath(), pkg.ModuleVersion())]; exists {
				directory = root
				break
			}
		}
	}
	if directory == "" {
		directory = compiler.Root
	}
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command: options.GoCommand, Directory: directory,
		Environment: snapshotEnvironment(options.Environment), OutputLimit: options.OutputLimit,
	}, append([]string{"list", "-mod=readonly", "-json", "-deps"}, roots...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("select Data source import closure: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	listed := make([]selectedSourcePackage, 0)
	for {
		var packageInfo selectedSourcePackage
		err := decoder.Decode(&packageInfo)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("decode Data source import closure: %w", err)
		}
		listed = append(listed, packageInfo)
	}
	sort.Slice(listed, func(left, right int) bool { return listed[left].ImportPath < listed[right].ImportPath })
	result := make([]supportSourcePackage, 0, len(listed))
	captured := make([]capturedSourcePackage, 0, len(listed))
	for _, packageInfo := range listed {
		if packageInfo.ImportPath == "" || packageInfo.ImportPath == DeclarationImportPath || !strings.Contains(packageInfo.ImportPath, ".") {
			continue
		}
		if _, exists := eligible[packageInfo.ImportPath]; exists {
			continue
		}
		if packageInfo.Error != nil || packageInfo.Incomplete || len(packageInfo.DepsErrors) != 0 || packageInfo.Module == nil || packageInfo.Module.Path == "" {
			return nil, nil, fmt.Errorf("source package %q is incomplete", packageInfo.ImportPath)
		}
		root, exists := modules[moduleKey(packageInfo.Module.Path, packageInfo.Module.Version)]
		if !exists {
			return nil, nil, fmt.Errorf("source root for support package %q is unavailable", packageInfo.ImportPath)
		}
		canonical, err := canonicalSourceRoot(root)
		if err != nil || !sameDirectory(canonical, packageInfo.Module.Dir) || !sourceDirectoryWithin(canonical, packageInfo.Dir) {
			return nil, nil, fmt.Errorf("support package %q is outside its selected module", packageInfo.ImportPath)
		}
		if err := budget.addPackage(packageInfo.ImportPath); err != nil {
			return nil, nil, err
		}
		files, err := captureSelectedPackageFiles(ctx, canonical, packageInfo.Dir, packageInfo.GoFiles, packageInfo.CgoFiles, budget)
		if err != nil {
			return nil, nil, fmt.Errorf("capture support package %q: %w", packageInfo.ImportPath, err)
		}
		result = append(result, supportSourcePackage{importPath: packageInfo.ImportPath, modulePath: packageInfo.Module.Path, moduleVersion: packageInfo.Module.Version, root: canonical, directory: packageInfo.Dir, files: files})
		captured = append(captured, capturedSourcePackage{importPath: packageInfo.ImportPath, root: canonical, files: files})
	}
	return result, captured, nil
}

func captureSelectedPackageFiles(ctx context.Context, root, directory string, goFiles, cgoFiles []string, budget *sourceSnapshotBudget) ([]AnalyzeSourceFile, error) {
	fileNames := append(append([]string(nil), goFiles...), cgoFiles...)
	sort.Strings(fileNames)
	if len(fileNames) == 0 {
		return nil, fmt.Errorf("package has no selected Go source")
	}
	files := make([]AnalyzeSourceFile, 0, len(fileNames))
	for index, fileName := range fileNames {
		if index > 0 && fileNames[index-1] == fileName || fileName == "" || filepath.Base(fileName) != fileName || !strings.HasSuffix(fileName, ".go") {
			return nil, fmt.Errorf("package selects unsafe or duplicate source")
		}
		absolute := filepath.Join(directory, fileName)
		relative, err := filepath.Rel(root, absolute)
		if err != nil || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("package source path is invalid")
		}
		relative = filepath.ToSlash(relative)
		if err := validateSourcePath(relative); err != nil {
			return nil, err
		}
		info, err := os.Lstat(absolute)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("package source is unavailable")
		}
		if err := budget.addFile(int(info.Size())); err != nil {
			return nil, err
		}
		file, err := readSnapshotFile(root, relative, "", -1, budget.maxBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func recheckCapturedPackages(ctx context.Context, packages []capturedSourcePackage) error {
	for _, pkg := range packages {
		for _, file := range pkg.files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := readSnapshotFile(pkg.root, file.Path(), file.Digest(), file.Bytes(), MaxSourceSnapshotBytes); err != nil {
				return err
			}
		}
	}
	return nil
}

type sourceSnapshotBudget struct {
	packages int
	files    int
	bytes    int
	maxBytes int
}

func (b *sourceSnapshotBudget) addPackage(importPath string) error {
	if importPath == "" {
		return fmt.Errorf("%w: package path is empty", ErrInvalidSourceSnapshotInput)
	}
	b.packages++
	if b.packages > MaxSourceSnapshotPackages {
		return fmt.Errorf("%w: package count %d", ErrSourceSnapshotBounds, b.packages)
	}
	return nil
}

func (b *sourceSnapshotBudget) addFile(size int) error {
	if size < 0 || size > b.maxBytes || b.files == MaxSourceSnapshotFiles {
		return ErrSourceSnapshotBounds
	}
	if b.bytes > b.maxBytes-size {
		return ErrSourceSnapshotBounds
	}
	b.files++
	b.bytes += size
	return nil
}

func validateSnapshotOptions(options AnalyzeSourceSnapshotOptions) error {
	if options.Compiler.ModulePath != ModulePath || options.Compiler.ModuleVersion == "" || options.Compiler.Root == "" || options.Compiler.ModuleChecksum == "" || options.Compiler.ManifestDigest == "" {
		return ErrInvalidSourceSnapshotInput
	}
	if options.Modules == nil {
		return fmt.Errorf("%w: module roots are absent", ErrInvalidSourceSnapshotInput)
	}
	return nil
}

func verifyCompilerSelection(selection Selection) (Selection, error) {
	verified, err := Resolve(Source{
		ModulePath: selection.ModulePath, ModuleVersion: selection.ModuleVersion,
		ModuleChecksum: selection.ModuleChecksum, Root: selection.Root,
	})
	if err != nil {
		return Selection{}, fmt.Errorf("%w: selected compiler source is unavailable or changed", ErrSourceSnapshotDrift)
	}
	if verified.ModulePath != selection.ModulePath || verified.ModuleVersion != selection.ModuleVersion || verified.ModuleChecksum != selection.ModuleChecksum || verified.ManifestDigest != selection.ManifestDigest || verified.Root != selection.Root {
		return Selection{}, fmt.Errorf("%w: selected compiler identity changed", ErrSourceSnapshotDrift)
	}
	return verified, nil
}

func recheckCompilerSelection(selection Selection) error {
	_, err := verifyCompilerSelection(selection)
	return err
}

func normalizeSnapshotModules(sources []SnapshotModule) (map[string]string, error) {
	result := make(map[string]string, len(sources))
	for _, source := range sources {
		if source.ModulePath == "" || source.Root == "" || !filepath.IsAbs(source.Root) {
			return nil, fmt.Errorf("%w: module root is incomplete", ErrInvalidSourceSnapshotInput)
		}
		if source.ModuleVersion != "" {
			if err := module.Check(source.ModulePath, source.ModuleVersion); err != nil {
				return nil, fmt.Errorf("%w: invalid module version", ErrInvalidSourceSnapshotInput)
			}
		} else if err := module.CheckPath(source.ModulePath); err != nil {
			return nil, fmt.Errorf("%w: invalid module path", ErrInvalidSourceSnapshotInput)
		}
		root, err := canonicalSourceRoot(source.Root)
		if err != nil {
			return nil, fmt.Errorf("%w: module source is unavailable", ErrInvalidSourceSnapshotInput)
		}
		key := moduleKey(source.ModulePath, source.ModuleVersion)
		if previous, exists := result[key]; exists && previous != root {
			return nil, fmt.Errorf("%w: module source is selected more than once", ErrInvalidSourceSnapshotInput)
		}
		result[key] = root
	}
	return result, nil
}

func moduleKey(modulePath, moduleVersion string) string { return modulePath + "\x00" + moduleVersion }

func validateDataPackage(pkg interfaceinventory.DataPackage) error {
	if err := module.CheckImportPath(pkg.ImportPath()); err != nil {
		return fmt.Errorf("%w: invalid package path", ErrInvalidSourceSnapshotInput)
	}
	files := pkg.Files()
	if len(files) == 0 {
		return fmt.Errorf("%w: package %q has no source files", ErrInvalidSourceSnapshotInput, pkg.ImportPath())
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := validateSourcePath(file.Path()); err != nil || !validDigest(file.Digest()) || file.Bytes() < 0 || file.Bytes() > MaxSourceSnapshotBytes {
			return fmt.Errorf("%w: package %q has invalid source identity", ErrInvalidSourceSnapshotInput, pkg.ImportPath())
		}
		if _, exists := seen[file.Path()]; exists {
			return fmt.Errorf("%w: package %q lists source %q more than once", ErrInvalidSourceSnapshotInput, pkg.ImportPath(), file.Path())
		}
		seen[file.Path()] = struct{}{}
	}
	return nil
}

func captureInventoryFiles(ctx context.Context, root string, identities []interfaceinventory.DataSourceFile, budget *sourceSnapshotBudget) ([]AnalyzeSourceFile, error) {
	identities = append([]interfaceinventory.DataSourceFile(nil), identities...)
	sort.Slice(identities, func(left, right int) bool { return identities[left].Path() < identities[right].Path() })
	files := make([]AnalyzeSourceFile, 0, len(identities))
	for _, identity := range identities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := budget.addFile(identity.Bytes()); err != nil {
			return nil, err
		}
		file, err := readSnapshotFile(root, identity.Path(), identity.Digest(), identity.Bytes(), budget.maxBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func captureDeclarationFiles(ctx context.Context, selection Selection, options AnalyzeSourceSnapshotOptions, budget *sourceSnapshotBudget) ([]AnalyzeSourceFile, error) {
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command: options.GoCommand, Directory: selection.Root,
		Environment: snapshotEnvironment(options.Environment), OutputLimit: options.OutputLimit,
	}, "list", "-mod=readonly", "-json", DeclarationImportPath)
	if err != nil {
		return nil, fmt.Errorf("select declaration package files: %w", err)
	}
	var listed selectedSourcePackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&listed); err != nil {
		return nil, fmt.Errorf("decode declaration package selection: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("declaration package selection has trailing output")
	}
	if listed.ImportPath != DeclarationImportPath || listed.Dir == "" || listed.Module == nil || listed.Module.Path != ModulePath || listed.Error != nil || listed.Incomplete || len(listed.DepsErrors) != 0 {
		return nil, fmt.Errorf("declaration package selection is incomplete")
	}
	root, err := canonicalSourceRoot(selection.Root)
	if err != nil || !sameDirectory(root, listed.Module.Dir) || !sourceDirectoryWithin(root, listed.Dir) {
		return nil, fmt.Errorf("declaration package source is outside the selected compiler module")
	}
	fileNames := append(append([]string(nil), listed.GoFiles...), listed.CgoFiles...)
	sort.Strings(fileNames)
	if len(fileNames) == 0 {
		return nil, fmt.Errorf("declaration package has no selected Go source")
	}
	files := make([]AnalyzeSourceFile, 0, len(fileNames))
	for index, fileName := range fileNames {
		if index > 0 && fileNames[index-1] == fileName {
			return nil, fmt.Errorf("declaration package selects source %q more than once", fileName)
		}
		if fileName == "" || filepath.Base(fileName) != fileName || !strings.HasSuffix(fileName, ".go") {
			return nil, fmt.Errorf("declaration package selects unsafe source")
		}
		absolute := filepath.Join(listed.Dir, fileName)
		relative, err := filepath.Rel(root, absolute)
		if err != nil || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("declaration package source path is invalid")
		}
		relative = filepath.ToSlash(relative)
		if err := validateSourcePath(relative); err != nil {
			return nil, err
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return nil, fmt.Errorf("declaration package source is unavailable")
		}
		if err := budget.addFile(int(info.Size())); err != nil {
			return nil, err
		}
		file, err := readSnapshotFile(root, relative, "", -1, budget.maxBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

type selectedSourcePackage struct {
	Dir        string                `json:"Dir"`
	ImportPath string                `json:"ImportPath"`
	GoFiles    []string              `json:"GoFiles"`
	CgoFiles   []string              `json:"CgoFiles"`
	Incomplete bool                  `json:"Incomplete"`
	Error      *selectedPackageErr   `json:"Error"`
	DepsErrors []*selectedPackageErr `json:"DepsErrors"`
	Module     *selectedSourceModule `json:"Module"`
}

type selectedPackageErr struct {
	Err string `json:"Err"`
}

type selectedSourceModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
}

func readSnapshotFile(root, relative, expectedDigest string, expectedBytes, maximumBytes int) (AnalyzeSourceFile, error) {
	absolute, err := safeSourcePath(root, relative)
	if err != nil {
		return AnalyzeSourceFile{}, err
	}
	before, err := os.Lstat(absolute)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s is unavailable", ErrSourceSnapshotDrift, relative)
	}
	if before.Size() > int64(maximumBytes) || (expectedBytes >= 0 && before.Size() != int64(expectedBytes)) {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s identity changed", ErrSourceSnapshotDrift, relative)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s is unavailable", ErrSourceSnapshotDrift, relative)
	}
	opened, err := file.Stat()
	if err != nil || !sameFileInfo(before, opened) {
		_ = file.Close()
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s changed while opening", ErrSourceSnapshotDrift, relative)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(maximumBytes)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s is unreadable", ErrSourceSnapshotDrift, relative)
	}
	after, err := os.Lstat(absolute)
	if err != nil || !sameFileInfo(opened, after) {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s changed during capture", ErrSourceSnapshotDrift, relative)
	}
	if len(data) > maximumBytes {
		return AnalyzeSourceFile{}, ErrSourceSnapshotBounds
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if expectedDigest != "" && (digest != expectedDigest || len(data) != expectedBytes) {
		return AnalyzeSourceFile{}, fmt.Errorf("%w: source %s identity changed", ErrSourceSnapshotDrift, relative)
	}
	return AnalyzeSourceFile{path: relative, content: append([]byte(nil), data...), digest: digest, bytes: len(data)}, nil
}

func validateSourcePath(value string) error {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("%w: invalid module-relative source path", ErrInvalidSourceSnapshotInput)
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case ".git", "vendor", "testdata", "dist", "generated":
			return fmt.Errorf("%w: reserved source path", ErrInvalidSourceSnapshotInput)
		}
	}
	return nil
}

func canonicalSourceRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", ErrInvalidSourceSnapshotInput
	}
	clean := filepath.Clean(root)
	before, err := os.Lstat(clean)
	if err != nil || before.Mode()&fs.ModeSymlink != 0 {
		return "", ErrInvalidSourceSnapshotInput
	}
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return "", ErrInvalidSourceSnapshotInput
	}
	return canonical, nil
}

func safeSourcePath(root, relative string) (string, error) {
	if err := validateSourcePath(relative); err != nil {
		return "", err
	}
	canonicalRoot, err := canonicalSourceRoot(root)
	if err != nil {
		return "", fmt.Errorf("%w: source root is unavailable", ErrSourceSnapshotDrift)
	}
	current := canonicalRoot
	for _, component := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("%w: source %s is unavailable", ErrSourceSnapshotDrift, relative)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: source %s is symbolic", ErrSourceSnapshotDrift, relative)
		}
	}
	return current, nil
}

func sourceDirectoryWithin(root, directory string) bool {
	if directory == "" || !filepath.IsAbs(directory) {
		return false
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, canonical)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func sameDirectory(left, right string) bool {
	left, leftErr := filepath.EvalSymlinks(left)
	right, rightErr := filepath.EvalSymlinks(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func sameFileInfo(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func snapshotEnvironment(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	result := append([]string(nil), environment...)
	for index, entry := range result {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "GOWORK") {
			result[index] = "GOWORK=off"
			return result
		}
	}
	return append(result, "GOWORK=off")
}
