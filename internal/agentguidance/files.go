package agentguidance

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/atomicfs"
)

var (
	// ErrCheck reports failure to compare installed Agent guidance with the
	// installed CLI catalog.
	ErrCheck = errors.New("check Plystra Agent guidance")
	// ErrSync reports failure to synchronize Agent guidance transactionally.
	ErrSync = errors.New("synchronize Plystra Agent guidance")
	// ErrManifest reports malformed or unsafe prior guidance ownership data.
	ErrManifest = errors.New("invalid Plystra Agent guidance manifest")
	// ErrDrift reports guidance state that cannot be changed without explicit
	// authority or without first restoring a missing owned path.
	ErrDrift = errors.New("Plystra Agent guidance drift")
)

const maximumGuidanceDirectoryEntries = 4096

// ChangeKind classifies one manifest-bounded Agent-guidance difference.
type ChangeKind string

const (
	ChangeStale            ChangeKind = "stale"
	ChangeMissing          ChangeKind = "missing"
	ChangeUnlisted         ChangeKind = "unlisted"
	ChangeManuallyModified ChangeKind = "manually-modified"
)

// Change is one immutable deterministic guidance diagnostic.
type Change struct {
	kind ChangeKind
	path string
}

// Kind returns stale, missing, unlisted, or manually-modified.
func (c Change) Kind() ChangeKind { return c.kind }

// Path returns the slash-separated Project-relative path.
func (c Change) Path() string { return c.path }

// Report is a deterministic set of guidance differences grouped in stale,
// missing, unlisted, then manually-modified order.
type Report struct {
	changes []Change
}

// Clean reports whether the installed projection and ownership manifest match
// the installed CLI catalog.
func (r Report) Clean() bool { return len(r.changes) == 0 }

// Changes returns defensive ordered diagnostics.
func (r Report) Changes() []Change { return append([]Change(nil), r.changes...) }

// Stale returns unchanged prior-owned paths whose desired bytes or ownership
// have changed.
func (r Report) Stale() []string { return r.paths(ChangeStale) }

// Missing returns absent desired paths and absent prior-owned paths.
func (r Report) Missing() []string { return r.paths(ChangeMissing) }

// Unlisted returns desired paths that exist without prior manifest ownership.
func (r Report) Unlisted() []string { return r.paths(ChangeUnlisted) }

// ManuallyModified returns prior-owned paths whose current kind, size, or bytes
// no longer match the recorded digest.
func (r Report) ManuallyModified() []string { return r.paths(ChangeManuallyModified) }

func (r Report) paths(kind ChangeKind) []string {
	var paths []string
	for _, change := range r.changes {
		if change.kind == kind {
			paths = append(paths, change.path)
		}
	}
	return paths
}

// SyncOptions controls the explicit authority available to guidance sync.
type SyncOptions struct {
	ReplaceGenerated bool
}

// Result records the paths changed or removed by one successful sync.
type Result struct {
	changed []string
	removed []string
}

// Changed returns paths created or replaced by the sync, including the
// ownership manifest when it changed.
func (r Result) Changed() []string { return append([]string(nil), r.changed...) }

// Removed returns retired prior-owned paths removed by the sync.
func (r Result) Removed() []string { return append([]string(nil), r.removed...) }

// Source is one path-only Project-relative Agent-guidance diagnostic source.
type Source struct {
	modulePath string
	sourcePath string
}

func (s Source) ModulePath() string { return s.modulePath }
func (s Source) SourcePath() string { return s.sourcePath }
func (Source) SourceKind() string   { return "agent-guidance" }
func (Source) Line() int            { return 0 }
func (Source) Column() int          { return 0 }

// SourceError retains every known Agent-guidance path associated with a
// manifest, drift, or concurrent-change failure.
type SourceError struct {
	sources []Source
	cause   error
}

// Sources returns defensive, path-sorted source records.
func (e *SourceError) Sources() []Source {
	if e == nil {
		return nil
	}
	return append([]Source(nil), e.sources...)
}

func (e *SourceError) Error() string {
	if e == nil || e.cause == nil {
		return ErrCheck.Error()
	}
	return e.cause.Error()
}

func (e *SourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// DriftError retains every blocking guidance difference.
type DriftError struct {
	changes []Change
}

// Changes returns defensive ordered blocking differences.
func (e *DriftError) Changes() []Change {
	if e == nil {
		return nil
	}
	return append([]Change(nil), e.changes...)
}

func (e *DriftError) Error() string {
	if e == nil || len(e.changes) == 0 {
		return ErrDrift.Error()
	}
	parts := make([]string, len(e.changes))
	for index, change := range e.changes {
		parts[index] = fmt.Sprintf("%s %s", change.kind, change.path)
	}
	return fmt.Sprintf("%s: synchronization blocked by %s", ErrDrift, strings.Join(parts, ", "))
}

func (*DriftError) Unwrap() error { return ErrDrift }

type actualFile struct {
	exists   bool
	conflict bool
	mode     fs.FileMode
	size     int64
	data     []byte
	bounded  bool
}

type inspectActualFunc func(root *os.Root, filePath string) (actualFile, error)

type applyFilesFunc func(rootPath string, writes []atomicfs.Write, removes []atomicfs.Remove, validate func(root string) error) error

func (f actualFile) boundedRegular() bool {
	return f.exists && !f.conflict && f.mode.IsRegular() && f.mode&fs.ModeSymlink == 0 && f.bounded
}

type inspectedState struct {
	desired        map[string]File
	actual         map[string]actualFile
	previous       map[string]string
	manifest       actualFile
	manifestExists bool
	report         Report
}

// Check compares only the installed catalog paths, prior-manifest paths, and
// the manifest itself. It never scans or changes unrelated Project files.
func Check(rootPath string, projection Projection) (Report, error) {
	state, err := inspect(rootPath, projection)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrCheck, err)
	}
	return state.report, nil
}

// Sync transactionally installs the desired guidance projection. Ordinary sync
// changes or removes only unchanged prior-owned files and never expands an
// existing manifest's ownership. ReplaceGenerated also permits changed
// prior-owned bounded regular files, but never unlisted paths, missing owned
// paths, symbolic entries, directories, or oversized files.
func Sync(rootPath string, projection Projection, options SyncOptions) (Result, error) {
	return syncWithApply(rootPath, projection, options, atomicfs.ApplyFiles)
}

func syncWithApply(rootPath string, projection Projection, options SyncOptions, apply applyFilesFunc) (Result, error) {
	if apply == nil {
		return Result{}, fmt.Errorf("%w: apply function is nil", ErrSync)
	}
	state, err := inspect(rootPath, projection)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSync, err)
	}
	blocking := blockingChanges(state, options)
	if len(blocking) != 0 {
		cause := &DriftError{changes: append([]Change(nil), blocking...)}
		return Result{}, fmt.Errorf("%w: %w", ErrSync, sourceError(projection.modulePath, changePaths(blocking), cause))
	}

	manifestData, _ := projectionFile(projection, ManifestPath)
	writes := make([]atomicfs.Write, 0, len(state.desired)+1)
	changed := make([]string, 0, len(state.desired)+1)
	for filePath, desired := range state.desired {
		actual := state.actual[filePath]
		if actual.exists && bytes.Equal(actual.data, desired.data) {
			continue
		}
		write := atomicfs.Write{Path: filePath, Data: desired.data}
		if actual.exists {
			write.ExpectedData = actual.data
		} else {
			write.MustNotExist = true
		}
		writes = append(writes, write)
		changed = append(changed, filePath)
	}
	if !state.manifestExists || !bytes.Equal(state.manifest.data, manifestData) {
		write := atomicfs.Write{Path: ManifestPath, Data: manifestData}
		if state.manifestExists {
			write.ExpectedData = state.manifest.data
		} else {
			write.MustNotExist = true
		}
		writes = append(writes, write)
		changed = append(changed, ManifestPath)
	}

	removes := make([]atomicfs.Remove, 0)
	removed := make([]string, 0)
	for filePath := range state.previous {
		if _, retained := state.desired[filePath]; retained {
			continue
		}
		actual := state.actual[filePath]
		removes = append(removes, atomicfs.Remove{Path: filePath, ExpectedData: actual.data})
		removed = append(removed, filePath)
	}
	sort.Slice(writes, func(left, right int) bool { return writes[left].Path < writes[right].Path })
	sort.Slice(removes, func(left, right int) bool { return removes[left].Path < removes[right].Path })
	sort.Strings(changed)
	sort.Strings(removed)

	validateInstalled := func(root string) error {
		confirmed, inspectErr := inspect(root, projection)
		if inspectErr != nil {
			return concurrentVerificationError(projection, inspectErr)
		}
		if !confirmed.report.Clean() {
			return concurrentReportError(confirmed.report)
		}
		return nil
	}
	if len(writes) != 0 || len(removes) != 0 {
		if err := apply(rootPath, writes, removes, validateInstalled); err != nil {
			cause := fmt.Errorf("%w: %w", ErrSync, err)
			paths := atomicfs.ConcurrentChangePaths(err)
			changed, inspectErr := changedTransactionPaths(rootPath, state, writes, removes)
			if inspectErr != nil {
				cause = errors.Join(cause, fmt.Errorf("inspect failed Agent-guidance transaction targets: %w", inspectErr))
				paths = append(paths, atomicfs.ConcurrentChangePaths(inspectErr)...)
			}
			paths = append(paths, changed...)
			paths = canonicalPaths(paths)
			if len(paths) != 0 {
				if !errors.Is(err, atomicfs.ErrConcurrentChange) || len(changed) != 0 {
					cause = errors.Join(cause, atomicfs.NewConcurrentChangeError(
						paths,
						fmt.Errorf("%w: Agent-guidance transaction targets changed after inspection", atomicfs.ErrConcurrentChange),
					))
				}
				return Result{}, sourceError(projection.modulePath, paths, cause)
			}
			return Result{}, cause
		}
	}

	final, err := inspect(rootPath, projection)
	if err != nil {
		return Result{}, concurrentVerificationError(projection, fmt.Errorf("%w: inspect committed guidance: %w", ErrSync, err))
	}
	if !final.report.Clean() {
		return Result{}, sourceError(projection.modulePath, reportPaths(final.report), fmt.Errorf("%w: %w", ErrSync, concurrentReportError(final.report)))
	}
	return Result{changed: changed, removed: removed}, nil
}

func inspect(rootPath string, projection Projection) (result inspectedState, inspectErr error) {
	return inspectWith(rootPath, projection, inspectActual)
}

func inspectWith(rootPath string, projection Projection, inspectFile inspectActualFunc) (result inspectedState, inspectErr error) {
	if err := validateProjection(projection); err != nil {
		return inspectedState{}, err
	}
	if inspectFile == nil {
		return inspectedState{}, errors.New("inspect Agent-guidance file function is nil")
	}
	absoluteRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return inspectedState{}, fmt.Errorf("resolve Project root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return inspectedState{}, fmt.Errorf("inspect Project root: %w", err)
	}
	if !info.IsDir() {
		return inspectedState{}, errors.New("Project root is not a directory")
	}
	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return inspectedState{}, fmt.Errorf("open Project root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			inspectErr = errors.Join(inspectErr, fmt.Errorf("close Project root: %w", err))
		}
	}()

	manifest, err := inspectFile(root, ManifestPath)
	if err != nil {
		return inspectedState{}, sourceError(projection.modulePath, []string{ManifestPath}, err)
	}
	previous := make(map[string]string)
	manifestExists := manifest.exists
	if manifestExists {
		if !manifest.boundedRegular() {
			return inspectedState{}, sourceError(projection.modulePath, []string{ManifestPath}, fmt.Errorf("%w: %s must be a bounded regular non-symbolic file", ErrManifest, ManifestPath))
		}
		parsed, err := ParseManifest(manifest.data)
		if err != nil {
			return inspectedState{}, sourceError(projection.modulePath, []string{ManifestPath}, fmt.Errorf("%w: %v", ErrManifest, err))
		}
		for _, file := range parsed.Files {
			previous[file.Path] = file.SHA256
		}
	}

	desired := make(map[string]File, len(projection.files)-1)
	load := make(map[string]struct{}, len(projection.files)+len(previous))
	for _, file := range projection.files {
		if file.path == ManifestPath {
			continue
		}
		desired[file.path] = file
		load[file.path] = struct{}{}
	}
	desiredAliases := make(map[string]string, len(desired))
	for filePath := range desired {
		desiredAliases[guidancePathAlias(filePath)] = filePath
	}
	for filePath := range previous {
		if desiredPath, aliases := desiredAliases[guidancePathAlias(filePath)]; aliases && desiredPath != filePath {
			return inspectedState{}, sourceError(
				projection.modulePath,
				[]string{ManifestPath},
				fmt.Errorf("%w: prior-owned path %s aliases installed catalog path %s", ErrManifest, filePath, desiredPath),
			)
		}
		load[filePath] = struct{}{}
	}
	actual := make(map[string]actualFile, len(load))
	paths := make([]string, 0, len(load))
	for filePath := range load {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		file, err := inspectFile(root, filePath)
		if err != nil {
			return inspectedState{}, sourceError(projection.modulePath, []string{filePath}, err)
		}
		actual[filePath] = file
	}
	confirmedManifest, err := inspectFile(root, ManifestPath)
	if err != nil {
		return inspectedState{}, sourceError(projection.modulePath, []string{ManifestPath}, err)
	}
	if !sameActualFile(manifest, confirmedManifest) {
		concurrent := atomicfs.NewConcurrentChangeError(
			[]string{ManifestPath},
			fmt.Errorf("%w: %s changed while Agent-guidance paths were inspected", atomicfs.ErrConcurrentChange, ManifestPath),
		)
		return inspectedState{}, sourceError(projection.modulePath, []string{ManifestPath}, concurrent)
	}

	result = inspectedState{
		desired:        desired,
		actual:         actual,
		previous:       previous,
		manifest:       manifest,
		manifestExists: manifestExists,
	}
	result.report = classify(projection, result)
	return result, nil
}

func changedTransactionPaths(rootPath string, state inspectedState, writes []atomicfs.Write, removes []atomicfs.Remove) (changed []string, changeErr error) {
	absoluteRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve Project root: %w", err)
	}
	root, err := os.OpenRoot(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("open Project root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			changeErr = errors.Join(changeErr, fmt.Errorf("close Project root: %w", err))
		}
	}()

	paths := make([]string, 0, len(writes)+len(removes))
	for _, write := range writes {
		paths = append(paths, write.Path)
	}
	for _, remove := range removes {
		paths = append(paths, remove.Path)
	}
	paths = canonicalPaths(paths)
	for _, filePath := range paths {
		before := state.actual[filePath]
		if filePath == ManifestPath {
			before = state.manifest
		}
		after, err := inspectActual(root, filePath)
		if err != nil {
			changeErr = errors.Join(changeErr, fmt.Errorf("inspect %s after failed transaction: %w", filePath, err))
			continue
		}
		if !sameActualFile(before, after) {
			changed = append(changed, filePath)
		}
	}
	return changed, changeErr
}

func sameActualFile(left, right actualFile) bool {
	return left.exists == right.exists &&
		left.conflict == right.conflict &&
		left.mode == right.mode &&
		left.size == right.size &&
		left.bounded == right.bounded &&
		bytes.Equal(left.data, right.data)
}

func inspectActual(root *os.Root, filePath string) (actualFile, error) {
	components := strings.Split(filePath, "/")
	current := ""
	for index, component := range components {
		aliases, err := guidanceComponentAliases(root, current, component)
		if err != nil {
			return actualFile{}, err
		}
		if aliases {
			return actualFile{exists: true, conflict: true}, nil
		}

		current = path.Join(current, component)
		before, err := root.Lstat(filepath.FromSlash(current))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return actualFile{}, nil
		case err != nil:
			return actualFile{}, fmt.Errorf("inspect guidance path %s: %w", current, err)
		}
		if index != len(components)-1 {
			if !before.IsDir() || before.Mode()&fs.ModeSymlink != 0 {
				return actualFile{exists: true, conflict: true, mode: before.Mode()}, nil
			}
			continue
		}

		actual := actualFile{exists: true, mode: before.Mode(), size: before.Size()}
		if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 || before.Size() < 0 || before.Size() > maximumGuidanceFileBytes {
			return actual, nil
		}
		data, err := readStableFile(root, filePath, before)
		if err != nil {
			return actualFile{}, err
		}
		actual.data = data
		actual.bounded = true
		return actual, nil
	}
	return actualFile{}, errors.New("inspect empty Agent-guidance path")
}

func guidanceComponentAliases(root *os.Root, parent, component string) (aliases bool, aliasErr error) {
	directoryPath := "."
	if parent != "" {
		directoryPath = filepath.FromSlash(parent)
	}
	directory, err := root.Open(directoryPath)
	if err != nil {
		return false, fmt.Errorf("open guidance directory %s: %w", path.Clean(parent), err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			aliasErr = errors.Join(aliasErr, fmt.Errorf("close guidance directory %s: %w", path.Clean(parent), err))
		}
	}()

	entries := make([]fs.DirEntry, 0, maximumGuidanceDirectoryEntries+1)
readEntries:
	for len(entries) <= maximumGuidanceDirectoryEntries {
		remaining := maximumGuidanceDirectoryEntries + 1 - len(entries)
		batch, err := directory.ReadDir(remaining)
		entries = append(entries, batch...)
		switch {
		case errors.Is(err, io.EOF):
			break readEntries
		case err != nil:
			return false, fmt.Errorf("read guidance directory %s: %w", path.Clean(parent), err)
		}
		if len(entries) > maximumGuidanceDirectoryEntries {
			break
		}
	}
	if len(entries) > maximumGuidanceDirectoryEntries {
		// Without a bounded complete listing, a portable alias-free lookup
		// cannot be established.
		return true, nil
	}
	for _, entry := range entries {
		if entry.Name() != component && strings.EqualFold(entry.Name(), component) {
			return true, nil
		}
	}
	return false, nil
}

func readStableFile(root *os.Root, filePath string, before fs.FileInfo) ([]byte, error) {
	osPath := filepath.FromSlash(filePath)
	file, err := root.Open(osPath)
	if err != nil {
		return nil, fmt.Errorf("open guidance path %s: %w", filePath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened guidance path %s: %w", filePath, err)
	}
	if !sameFileSnapshot(before, opened) {
		_ = file.Close()
		return nil, atomicfs.NewConcurrentChangeError([]string{filePath}, fmt.Errorf("%w: %s was replaced before open", atomicfs.ErrConcurrentChange, filePath))
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumGuidanceFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read guidance path %s: %w", filePath, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close guidance path %s: %w", filePath, closeErr)
	}
	if len(data) > maximumGuidanceFileBytes {
		return nil, atomicfs.NewConcurrentChangeError([]string{filePath}, fmt.Errorf("%w: %s exceeded the bounded snapshot while being read", atomicfs.ErrConcurrentChange, filePath))
	}
	after, err := root.Lstat(osPath)
	if err != nil || !sameFileSnapshot(opened, after) {
		return nil, atomicfs.NewConcurrentChangeError([]string{filePath}, fmt.Errorf("%w: %s changed while it was read", atomicfs.ErrConcurrentChange, filePath))
	}
	return data, nil
}

func sameFileSnapshot(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func classify(projection Projection, state inspectedState) Report {
	changes := make(map[ChangeKind]map[string]struct{}, 4)
	add := func(kind ChangeKind, filePath string) {
		if changes[kind] == nil {
			changes[kind] = make(map[string]struct{})
		}
		changes[kind][filePath] = struct{}{}
	}
	manifestData, _ := projectionFile(projection, ManifestPath)
	if !state.manifestExists {
		add(ChangeMissing, ManifestPath)
	} else if !bytes.Equal(state.manifest.data, manifestData) {
		add(ChangeStale, ManifestPath)
	}

	paths := make(map[string]struct{}, len(state.desired)+len(state.previous))
	for filePath := range state.desired {
		paths[filePath] = struct{}{}
	}
	for filePath := range state.previous {
		paths[filePath] = struct{}{}
	}
	for filePath := range paths {
		desired, wanted := state.desired[filePath]
		previousDigest, previouslyOwned := state.previous[filePath]
		actual := state.actual[filePath]
		switch {
		case wanted && !previouslyOwned:
			if actual.exists {
				add(ChangeUnlisted, filePath)
			} else {
				add(ChangeMissing, filePath)
			}
		case previouslyOwned && !actual.exists:
			add(ChangeMissing, filePath)
		case previouslyOwned && !actual.boundedRegular():
			add(ChangeManuallyModified, filePath)
		case previouslyOwned && digest(actual.data) != previousDigest:
			add(ChangeManuallyModified, filePath)
		case wanted && !bytes.Equal(actual.data, desired.data):
			add(ChangeStale, filePath)
		case !wanted:
			add(ChangeStale, filePath)
		}
	}
	return orderedReport(changes)
}

func blockingChanges(state inspectedState, options SyncOptions) []Change {
	changes := make(map[ChangeKind]map[string]struct{}, 3)
	add := func(kind ChangeKind, filePath string) {
		if changes[kind] == nil {
			changes[kind] = make(map[string]struct{})
		}
		changes[kind][filePath] = struct{}{}
	}
	if !state.manifestExists {
		for filePath := range state.desired {
			if state.actual[filePath].exists {
				add(ChangeUnlisted, filePath)
			}
		}
		return orderedReport(changes).changes
	}
	for filePath, previousDigest := range state.previous {
		actual := state.actual[filePath]
		switch {
		case !actual.exists:
			add(ChangeMissing, filePath)
		case !actual.boundedRegular():
			add(ChangeManuallyModified, filePath)
		case digest(actual.data) != previousDigest && !options.ReplaceGenerated:
			add(ChangeManuallyModified, filePath)
		}
	}
	for filePath := range state.desired {
		if _, previouslyOwned := state.previous[filePath]; !previouslyOwned {
			if state.actual[filePath].exists {
				add(ChangeUnlisted, filePath)
			} else {
				add(ChangeMissing, filePath)
			}
		}
	}
	return orderedReport(changes).changes
}

func orderedReport(changes map[ChangeKind]map[string]struct{}) Report {
	order := [...]ChangeKind{ChangeStale, ChangeMissing, ChangeUnlisted, ChangeManuallyModified}
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

func validateProjection(projection Projection) error {
	if !validModuleLabel(projection.modulePath) || len(projection.files) != len(projection.manifest.Files)+1 {
		return errors.New("invalid Plystra Agent guidance projection")
	}
	if err := validateManifest(projection.manifest); err != nil {
		return fmt.Errorf("invalid Plystra Agent guidance projection: %v", err)
	}
	wanted := make(map[string]string, len(projection.manifest.Files))
	for _, file := range projection.manifest.Files {
		wanted[file.Path] = file.SHA256
	}
	seen := make(map[string]struct{}, len(projection.files))
	var manifestData []byte
	for _, file := range projection.files {
		if _, duplicate := seen[file.path]; duplicate || len(file.data) == 0 || len(file.data) > maximumGuidanceFileBytes {
			return errors.New("invalid Plystra Agent guidance projection")
		}
		seen[file.path] = struct{}{}
		if file.path == ManifestPath {
			manifestData = file.data
			continue
		}
		if !validOwnedPath(file.path) || wanted[file.path] != digest(file.data) {
			return errors.New("invalid Plystra Agent guidance projection")
		}
		delete(wanted, file.path)
	}
	if len(wanted) != 0 || len(manifestData) == 0 {
		return errors.New("invalid Plystra Agent guidance projection")
	}
	manifest, err := ParseManifest(manifestData)
	if err != nil || !sameManifest(manifest, projection.manifest) {
		return errors.New("invalid Plystra Agent guidance projection")
	}
	return nil
}

func sameManifest(left, right Manifest) bool {
	if left.Schema != right.Schema || left.CLIVersion != right.CLIVersion || left.KernelVersion != right.KernelVersion || left.SpecificationRevision != right.SpecificationRevision || left.CatalogDigest != right.CatalogDigest || len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func projectionFile(projection Projection, filePath string) ([]byte, bool) {
	for _, file := range projection.files {
		if file.path == filePath {
			return file.data, true
		}
	}
	return nil, false
}

func sourceError(modulePath string, paths []string, cause error) error {
	if cause == nil {
		return nil
	}
	ordered := canonicalPaths(paths)
	sources := make([]Source, len(ordered))
	for index, filePath := range ordered {
		sources[index] = Source{modulePath: modulePath, sourcePath: filePath}
	}
	return &SourceError{sources: sources, cause: cause}
}

func canonicalPaths(paths []string) []string {
	unique := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		filePath = path.Clean(strings.ReplaceAll(filePath, "\\", "/"))
		if filePath == "." || !fs.ValidPath(filePath) {
			continue
		}
		unique[filePath] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for filePath := range unique {
		ordered = append(ordered, filePath)
	}
	sort.Strings(ordered)
	return ordered
}

func changePaths(changes []Change) []string {
	paths := make([]string, len(changes))
	for index, change := range changes {
		paths[index] = change.path
	}
	return paths
}

func reportPaths(report Report) []string { return changePaths(report.changes) }

func concurrentReportError(report Report) error {
	parts := make([]string, len(report.changes))
	for index, change := range report.changes {
		parts[index] = fmt.Sprintf("%s %s", change.kind, change.path)
	}
	return atomicfs.NewConcurrentChangeError(reportPaths(report), fmt.Errorf("%w: installed Agent guidance changed during verification: %s", atomicfs.ErrConcurrentChange, strings.Join(parts, ", ")))
}

func concurrentVerificationError(projection Projection, cause error) error {
	paths := atomicfs.ConcurrentChangePaths(cause)
	var located *SourceError
	if len(paths) == 0 && errors.As(cause, &located) && located != nil {
		for _, source := range located.Sources() {
			paths = append(paths, source.SourcePath())
		}
	}
	if len(paths) == 0 {
		paths = []string{ManifestPath}
	}
	concurrent := atomicfs.NewConcurrentChangeError(paths, fmt.Errorf("%w: Agent guidance could not be verified after installation", atomicfs.ErrConcurrentChange))
	return sourceError(projection.modulePath, paths, errors.Join(cause, concurrent))
}
