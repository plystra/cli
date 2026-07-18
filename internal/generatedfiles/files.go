package generatedfiles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/atomicfs"
)

const maximumManifestBytes = 16 << 20

var (
	// ErrCheck reports failure to compare desired output with the application.
	ErrCheck = errors.New("check managed generated files")
	// ErrInstall reports failure to transactionally install desired output.
	ErrInstall = errors.New("install managed generated files")
	// ErrManifest reports malformed or unsafe prior ownership data.
	ErrManifest = errors.New("invalid managed generated manifest")
	// ErrConflict reports a desired path occupied by an unowned file.
	ErrConflict = errors.New("managed generated path conflicts with unowned file")
	// ErrUnexpected reports unowned output that a strict mutating workflow
	// refuses to retain under generated/.
	ErrUnexpected = errors.New("unexpected generated output")
)

// ChangeKind classifies one Git-visible generated-output difference.
type ChangeKind string

const (
	ChangeChanged    ChangeKind = "changed"
	ChangeMissing    ChangeKind = "missing"
	ChangeUnexpected ChangeKind = "unexpected"
	ChangeObsolete   ChangeKind = "obsolete"
)

// Change is one immutable deterministic drift diagnostic.
type Change struct {
	kind ChangeKind
	path string
}

// Kind returns changed, missing, unexpected, or obsolete.
func (c Change) Kind() ChangeKind { return c.kind }

// Path returns the slash-separated application-relative path.
func (c Change) Path() string { return c.path }

// Report is a deterministic path-sorted set of drift diagnostics, grouped in
// changed, missing, unexpected, then obsolete order.
type Report struct {
	changes []Change
}

// Clean reports whether desired and Git-visible generated files agree.
func (r Report) Clean() bool { return len(r.changes) == 0 }

// Changes returns defensive ordered diagnostics.
func (r Report) Changes() []Change { return append([]Change(nil), r.changes...) }

// Changed returns paths whose bytes or file kind differ from desired or from
// the prior ownership snapshot when the path is also obsolete.
func (r Report) Changed() []string { return r.paths(ChangeChanged) }

// Missing returns desired paths absent from the application.
func (r Report) Missing() []string { return r.paths(ChangeMissing) }

// Unexpected returns Git-visible generated files owned by neither the desired
// nor prior manifest. Installation preserves these paths.
func (r Report) Unexpected() []string { return r.paths(ChangeUnexpected) }

// Obsolete returns prior managed paths no longer present in desired output.
func (r Report) Obsolete() []string { return r.paths(ChangeObsolete) }

func (r Report) paths(kind ChangeKind) []string {
	var paths []string
	for _, change := range r.changes {
		if change.kind == kind {
			paths = append(paths, change.path)
		}
	}
	return paths
}

// Check compares desired output with the application without changing the
// filesystem. Ignored JavaScript build output is excluded.
func Check(rootPath string, output Output) (Report, error) {
	state, err := inspect(rootPath, output)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrCheck, err)
	}
	return state.report, nil
}

// Install atomically writes desired output and its ownership manifest, removes
// unchanged obsolete managed files, validates the complete updated
// application, and rolls back on error or panic. Unowned and modified-obsolete
// files are preserved and remain visible as unexpected drift.
func Install(rootPath string, output Output, validate func(root string) error) (Report, error) {
	return install(rootPath, output, validate, false)
}

// InstallStrict behaves like Install but rejects every unexpected unowned or
// modified-obsolete path. The path is preserved while all managed writes and
// removals are rolled back.
func InstallStrict(rootPath string, output Output, validate func(root string) error) (Report, error) {
	return install(rootPath, output, validate, true)
}

func install(rootPath string, output Output, validate func(root string) error, rejectUnexpected bool) (Report, error) {
	if validate == nil {
		return Report{}, fmt.Errorf("%w: validation callback is nil", ErrInstall)
	}
	state, err := inspect(rootPath, output)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrInstall, err)
	}

	desired := make(map[string]File, len(output.files))
	for _, file := range output.files {
		desired[file.path] = file
	}
	writes := make([]atomicfs.Write, 0, len(output.files)+1)
	for _, file := range output.files {
		actual, exists := state.actual[file.path]
		switch {
		case !exists:
			writes = append(writes, atomicfs.Write{Path: file.path, Data: file.data, MustNotExist: true})
		case !actual.mode.IsRegular() || actual.mode&fs.ModeSymlink != 0:
			return state.report, fmt.Errorf("%w: %w: desired path %s is not a regular file", ErrInstall, ErrConflict, file.path)
		case bytes.Equal(actual.data, file.data):
			// Identical legacy or unowned output can be adopted without mutation.
		case state.previous[file.path] == "":
			return state.report, fmt.Errorf("%w: %w: desired path %s already contains different unowned bytes", ErrInstall, ErrConflict, file.path)
		default:
			writes = append(writes, atomicfs.Write{Path: file.path, Data: file.data, ExpectedData: actual.data})
		}
	}

	manifest, manifestExists := state.actual[ManifestPath]
	switch {
	case !manifestExists:
		writes = append(writes, atomicfs.Write{Path: ManifestPath, Data: output.manifestJSON, MustNotExist: true})
	case !manifest.mode.IsRegular() || manifest.mode&fs.ModeSymlink != 0:
		return state.report, fmt.Errorf("%w: %w: %s is not a regular file", ErrInstall, ErrManifest, ManifestPath)
	case !bytes.Equal(manifest.data, output.manifestJSON):
		writes = append(writes, atomicfs.Write{Path: ManifestPath, Data: output.manifestJSON, ExpectedData: manifest.data})
	}

	removes := make([]atomicfs.Remove, 0)
	for filePath, previousDigest := range state.previous {
		if _, retained := desired[filePath]; retained {
			continue
		}
		actual, exists := state.actual[filePath]
		if !exists || !actual.mode.IsRegular() || actual.mode&fs.ModeSymlink != 0 || digest(actual.data) != previousDigest {
			continue
		}
		removes = append(removes, atomicfs.Remove{Path: filePath, ExpectedData: actual.data})
	}
	sort.Slice(removes, func(left, right int) bool { return removes[left].Path < removes[right].Path })

	validateInstalled := func(root string) error {
		if err := validateInstalledOutput(root, output, rejectUnexpected); err != nil {
			return err
		}
		if err := validate(root); err != nil {
			return err
		}
		return validateInstalledOutput(root, output, rejectUnexpected)
	}
	if err := atomicfs.ApplyFiles(rootPath, writes, removes, validateInstalled); err != nil {
		return state.report, fmt.Errorf("%w: %w", ErrInstall, err)
	}
	final, err := inspect(rootPath, output)
	if err != nil {
		return Report{}, fmt.Errorf("%w: inspect committed output: %w", ErrInstall, err)
	}
	if err := invalidInstalledReport(final.report, rejectUnexpected); err != nil {
		return final.report, fmt.Errorf("%w: %w immediately after commit", ErrInstall, err)
	}
	return final.report, nil
}

func validateInstalledOutput(root string, output Output, rejectUnexpected bool) error {
	report, err := Check(root, output)
	if err != nil {
		return err
	}
	return invalidInstalledReport(report, rejectUnexpected)
}

func invalidInstalledReport(report Report, rejectUnexpected bool) error {
	concurrent := make([]string, 0)
	unexpected := make([]string, 0)
	for _, change := range report.changes {
		description := fmt.Sprintf("%s file %s", change.kind, change.path)
		if change.kind == ChangeUnexpected {
			if rejectUnexpected {
				unexpected = append(unexpected, description)
			}
			continue
		}
		concurrent = append(concurrent, description)
	}
	var result error
	if len(concurrent) != 0 {
		result = errors.Join(result, fmt.Errorf("%w: %s", atomicfs.ErrConcurrentChange, strings.Join(concurrent, ", ")))
	}
	if len(unexpected) != 0 {
		result = errors.Join(result, fmt.Errorf("%w: %s", ErrUnexpected, strings.Join(unexpected, ", ")))
	}
	return result
}

type actualFile struct {
	mode fs.FileMode
	size int64
	data []byte
}

type inspectedState struct {
	actual   map[string]actualFile
	previous map[string]string
	report   Report
}

func inspect(rootPath string, output Output) (inspectedState, error) {
	if !output.valid() {
		return inspectedState{}, ErrOutput
	}
	absoluteRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return inspectedState{}, fmt.Errorf("resolve application root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return inspectedState{}, fmt.Errorf("inspect application root: %w", err)
	}
	if !info.IsDir() {
		return inspectedState{}, errors.New("application root is not a directory")
	}
	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return inspectedState{}, fmt.Errorf("open application root: %w", err)
	}
	actual, err := scanGenerated(absoluteRoot)
	if err != nil {
		_ = root.Close()
		return inspectedState{}, err
	}
	if manifest, exists := actual[ManifestPath]; exists && manifest.mode.IsRegular() && manifest.mode&fs.ModeSymlink == 0 {
		manifest.data, err = readBoundedManifest(root, manifest)
		if err != nil {
			_ = root.Close()
			return inspectedState{}, err
		}
		actual[ManifestPath] = manifest
	}
	previous, err := decodePreviousManifest(actual)
	if err != nil {
		_ = root.Close()
		return inspectedState{}, err
	}

	load := make(map[string]struct{}, len(output.files)+len(previous))
	for _, file := range output.files {
		load[file.path] = struct{}{}
	}
	for filePath := range previous {
		load[filePath] = struct{}{}
	}
	for filePath := range load {
		file, exists := actual[filePath]
		if !exists || !file.mode.IsRegular() || file.mode&fs.ModeSymlink != 0 {
			continue
		}
		file.data, err = root.ReadFile(filepath.FromSlash(filePath))
		if err != nil {
			_ = root.Close()
			return inspectedState{}, fmt.Errorf("read generated file %s: %w", filePath, err)
		}
		actual[filePath] = file
	}
	if err := root.Close(); err != nil {
		return inspectedState{}, fmt.Errorf("close application root: %w", err)
	}
	state := inspectedState{actual: actual, previous: previous}
	state.report = classify(output, state)
	return state, nil
}

func scanGenerated(absoluteRoot string) (map[string]actualFile, error) {
	actual := make(map[string]actualFile)
	err := fs.WalkDir(os.DirFS(absoluteRoot), "generated", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(filepath.Join(absoluteRoot, filepath.FromSlash(filePath)))
		if err != nil {
			return err
		}
		mode := info.Mode()
		if filePath == "generated" {
			if !mode.IsDir() || mode&fs.ModeSymlink != 0 {
				return fmt.Errorf("generated root is not a regular directory")
			}
			return nil
		}
		if mode.IsDir() && mode&fs.ModeSymlink == 0 && ignoredGeneratedPath(filePath) {
			return fs.SkipDir
		}
		actual[filePath] = actualFile{mode: mode, size: info.Size()}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return actual, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan generated files: %w", err)
	}
	return actual, nil
}

func readBoundedManifest(root *os.Root, file actualFile) ([]byte, error) {
	if file.mode&fs.ModeSymlink != 0 || !file.mode.IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrManifest, ManifestPath)
	}
	if file.size > maximumManifestBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrManifest, ManifestPath, maximumManifestBytes)
	}
	data, err := root.ReadFile(filepath.FromSlash(ManifestPath))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrManifest, ManifestPath, err)
	}
	if len(data) > maximumManifestBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrManifest, ManifestPath, maximumManifestBytes)
	}
	return data, nil
}

func decodePreviousManifest(actual map[string]actualFile) (map[string]string, error) {
	file, exists := actual[ManifestPath]
	if !exists {
		return make(map[string]string), nil
	}
	if !file.mode.IsRegular() || file.mode&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrManifest, ManifestPath)
	}
	decoder := json.NewDecoder(bytes.NewReader(file.data))
	decoder.DisallowUnknownFields()
	var document manifestDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode %s: %v", ErrManifest, ManifestPath, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: %s contains trailing JSON", ErrManifest, ManifestPath)
	}
	if document.Version != manifestVersion || document.Files == nil {
		return nil, fmt.Errorf("%w: %s must use version %d with a files array", ErrManifest, ManifestPath, manifestVersion)
	}
	previous := make(map[string]string, len(document.Files))
	for index, record := range document.Files {
		if !validManagedPath(record.Path) {
			return nil, fmt.Errorf("%w: files[%d] has invalid path %q", ErrManifest, index, record.Path)
		}
		if !validDigest(record.SHA256) {
			return nil, fmt.Errorf("%w: files[%d] has invalid sha256 for %s", ErrManifest, index, record.Path)
		}
		if _, duplicate := previous[record.Path]; duplicate {
			return nil, fmt.Errorf("%w: files[%d] duplicates %s", ErrManifest, index, record.Path)
		}
		previous[record.Path] = record.SHA256
	}
	return previous, nil
}

func classify(output Output, state inspectedState) Report {
	changes := make(map[ChangeKind]map[string]struct{}, 4)
	add := func(kind ChangeKind, filePath string) {
		paths := changes[kind]
		if paths == nil {
			paths = make(map[string]struct{})
			changes[kind] = paths
		}
		paths[filePath] = struct{}{}
	}
	desired := make(map[string]File, len(output.files))
	for _, file := range output.files {
		desired[file.path] = file
		actual, exists := state.actual[file.path]
		switch {
		case !exists:
			add(ChangeMissing, file.path)
		case !actual.mode.IsRegular() || actual.mode&fs.ModeSymlink != 0 || !bytes.Equal(actual.data, file.data):
			add(ChangeChanged, file.path)
		}
	}
	manifest, exists := state.actual[ManifestPath]
	switch {
	case !exists:
		add(ChangeMissing, ManifestPath)
	case !manifest.mode.IsRegular() || manifest.mode&fs.ModeSymlink != 0 || !bytes.Equal(manifest.data, output.manifestJSON):
		add(ChangeChanged, ManifestPath)
	}
	for filePath, previousDigest := range state.previous {
		if _, retained := desired[filePath]; retained {
			continue
		}
		add(ChangeObsolete, filePath)
		if actual, exists := state.actual[filePath]; exists && (!actual.mode.IsRegular() || actual.mode&fs.ModeSymlink != 0 || digest(actual.data) != previousDigest) {
			add(ChangeChanged, filePath)
		}
	}
	for filePath, actual := range state.actual {
		if filePath == ManifestPath || actual.mode.IsDir() {
			continue
		}
		if _, expected := desired[filePath]; expected {
			continue
		}
		if _, formerlyManaged := state.previous[filePath]; formerlyManaged {
			continue
		}
		add(ChangeUnexpected, filePath)
	}

	order := [...]ChangeKind{ChangeChanged, ChangeMissing, ChangeUnexpected, ChangeObsolete}
	var result []Change
	for _, kind := range order {
		paths := make([]string, 0, len(changes[kind]))
		for filePath := range changes[kind] {
			paths = append(paths, filePath)
		}
		sort.Strings(paths)
		for _, filePath := range paths {
			result = append(result, Change{kind: kind, path: filePath})
		}
	}
	return Report{changes: result}
}
