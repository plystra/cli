package applicationresolve

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/moduledependency"
)

const applicationManifestName = "plystra.yaml"

const (
	generatedApplicationManifestName = "generated/manifest.json"
	maximumGeneratedManifestSize     = 16 << 20
)

// ManifestSnapshot is one bounded, non-symbolic plystra.yaml read together
// with the filesystem identity needed to detect replacement during a longer
// operation.
type ManifestSnapshot struct {
	path       string
	root       fs.FileInfo
	components []manifestPathState
	file       fs.FileInfo
	data       []byte
}

// Path returns the stable Project-relative slash path that was read.
func (s ManifestSnapshot) Path() string { return s.path }

// Data returns defensive manifest bytes suitable for an ExpectedData write
// precondition.
func (s ManifestSnapshot) Data() []byte { return append([]byte(nil), s.data...) }

func loadConfiguration(moduleRoot, relativePath string) (ManifestSnapshot, applicationmeta.Manifest, error) {
	snapshot, err := readManifestSnapshot(moduleRoot, relativePath)
	if err != nil {
		return ManifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	manifest, err := applicationmeta.Parse(snapshot.data)
	if err != nil {
		return ManifestSnapshot{}, applicationmeta.Manifest{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	return snapshot, manifest, nil
}

type dependencyManifestSnapshot struct {
	identity string
	root     string
	snapshot ManifestSnapshot
}

func loadDependencyManifests(dependencies []moduledependency.Module) ([]dependencyManifestSnapshot, []applicationmeta.Dependency, error) {
	snapshots := make([]dependencyManifestSnapshot, 0, len(dependencies))
	manifests := make([]applicationmeta.Dependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		snapshot, err := ReadManifestSnapshot(dependency.Root())
		if err != nil {
			return nil, nil, fmt.Errorf("%w: dependency Project %s: %w", ErrManifest, dependencyIdentity(dependency), err)
		}
		manifest, err := applicationmeta.Parse(snapshot.data)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: dependency Project %s: %w", ErrManifest, dependencyIdentity(dependency), err)
		}
		snapshots = append(snapshots, dependencyManifestSnapshot{
			identity: dependencyIdentity(dependency),
			root:     dependency.Root(),
			snapshot: snapshot,
		})
		manifests = append(manifests, applicationmeta.Dependency{
			ModulePath:    dependency.Path(),
			ModuleVersion: dependency.SelectedVersion(),
			Manifest:      manifest,
		})
	}
	return snapshots, manifests, nil
}

func recheckDependencyManifests(snapshots []dependencyManifestSnapshot) error {
	for _, before := range snapshots {
		after, err := ReadManifestSnapshot(before.root)
		if err != nil {
			return fmt.Errorf("%w: dependency Project %s plystra.yaml: %v", ErrConcurrentChange, before.identity, err)
		}
		if !sameManifestSnapshot(before.snapshot, after) {
			return fmt.Errorf("%w: dependency Project %s plystra.yaml changed before resolution completed", ErrConcurrentChange, before.identity)
		}
	}
	return nil
}

func dependencyIdentity(dependency moduledependency.Module) string {
	version := dependency.SelectedVersion()
	if version == "" {
		version = "workspace"
	}
	return dependency.Path() + "@" + version
}

// ReadManifestSnapshot safely reads the root plystra.yaml without following a
// symbolic module root or manifest and rejects replacement during the read.
func ReadManifestSnapshot(moduleRoot string) (result ManifestSnapshot, snapshotErr error) {
	return readManifestSnapshot(moduleRoot, applicationManifestName)
}

// readManifestSnapshot safely reads one Project-relative configuration path
// without following symbolic path components and rejects replacement during
// the read.
func readManifestSnapshot(moduleRoot, relativePath string) (result ManifestSnapshot, snapshotErr error) {
	if moduleRoot == "" {
		return ManifestSnapshot{}, fmt.Errorf("%w: module root is empty", ErrUnsafeManifest)
	}
	relativePath = filepath.Clean(filepath.FromSlash(relativePath))
	if relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return ManifestSnapshot{}, fmt.Errorf("%w: configuration path %q is not a safe Project-relative file", ErrUnsafeManifest, filepath.ToSlash(relativePath))
	}
	displayPath := filepath.ToSlash(relativePath)
	absolute, err := filepath.Abs(moduleRoot)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("resolve module root: %w", err)
	}
	rootBefore, err := os.Lstat(absolute)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect module root: %w", err)
	}
	if !rootBefore.IsDir() || rootBefore.Mode()&fs.ModeSymlink != 0 {
		return ManifestSnapshot{}, fmt.Errorf("%w: module root is not a regular non-symbolic directory", ErrUnsafeManifest)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("open module root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			snapshotErr = errors.Join(snapshotErr, fmt.Errorf("close module root: %w", err))
		}
	}()
	openedRoot, err := root.Lstat(".")
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect opened module root: %w", err)
	}
	if !sameDirectory(rootBefore, openedRoot) {
		return ManifestSnapshot{}, fmt.Errorf("%w: %w: module root was replaced before open", ErrUnsafeManifest, ErrConcurrentChange)
	}

	pathBefore, err := inspectManifestPath(root, relativePath)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("inspect selected configuration %s: %w", displayPath, err)
	}
	before := pathBefore[len(pathBefore)-1].info
	if before.Size() > applicationmeta.MaximumSize {
		return ManifestSnapshot{}, fmt.Errorf("%w: selected configuration %s exceeds %d bytes", ErrUnsafeManifest, displayPath, applicationmeta.MaximumSize)
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return ManifestSnapshot{}, fmt.Errorf("open selected configuration %s: %w", displayPath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return ManifestSnapshot{}, fmt.Errorf("inspect opened selected configuration %s: %w", displayPath, err)
	}
	if !opened.Mode().IsRegular() || !sameFile(before, opened) {
		_ = file.Close()
		return ManifestSnapshot{}, fmt.Errorf("%w: selected configuration %s was replaced before open", ErrConcurrentChange, displayPath)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, applicationmeta.MaximumSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return ManifestSnapshot{}, fmt.Errorf("read selected configuration %s: %w", displayPath, readErr)
	}
	if closeErr != nil {
		return ManifestSnapshot{}, fmt.Errorf("close selected configuration %s: %w", displayPath, closeErr)
	}
	rootAfter, rootErr := root.Lstat(".")
	pathAfter, pathErr := inspectManifestPath(root, relativePath)
	if rootErr != nil || pathErr != nil || !sameDirectory(openedRoot, rootAfter) || !sameManifestPathStates(pathBefore, pathAfter) {
		return ManifestSnapshot{}, fmt.Errorf("%w: selected configuration %s or its Project root changed while it was read", ErrConcurrentChange, displayPath)
	}
	after := pathAfter[len(pathAfter)-1].info
	if len(data) > applicationmeta.MaximumSize {
		return ManifestSnapshot{}, fmt.Errorf("%w: selected configuration %s exceeds %d bytes", ErrUnsafeManifest, displayPath, applicationmeta.MaximumSize)
	}
	return ManifestSnapshot{
		path:       displayPath,
		root:       rootAfter,
		components: append([]manifestPathState(nil), pathAfter...),
		file:       after,
		data:       append([]byte(nil), data...),
	}, nil
}

type manifestPathState struct {
	name string
	info fs.FileInfo
}

func inspectManifestPath(root *os.Root, relativePath string) ([]manifestPathState, error) {
	components := strings.Split(relativePath, string(filepath.Separator))
	states := make([]manifestPathState, 0, len(components))
	current := ""
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s contains a symbolic path component", ErrUnsafeManifest, filepath.ToSlash(relativePath))
		}
		if index < len(components)-1 {
			if !info.IsDir() {
				return nil, fmt.Errorf("%w: %s has a non-directory path component", ErrUnsafeManifest, filepath.ToSlash(relativePath))
			}
		} else if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: %s must be a regular non-symbolic file", ErrUnsafeManifest, filepath.ToSlash(relativePath))
		}
		states = append(states, manifestPathState{name: current, info: info})
	}
	return states, nil
}

func sameManifestPathStates(left, right []manifestPathState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].name != right[index].name || left[index].info.Mode() != right[index].info.Mode() || !os.SameFile(left[index].info, right[index].info) {
			return false
		}
		if index == len(left)-1 && !sameFile(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}

func readGeneratedApplicationManifest(moduleRoot string) (result []byte, exists bool, readErr error) {
	absolute, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, false, fmt.Errorf("resolve module root: %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, false, fmt.Errorf("open module root: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			readErr = errors.Join(readErr, fmt.Errorf("close module root: %w", err))
		}
	}()
	generated, err := root.Lstat("generated")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect generated directory: %w", err)
	}
	if !generated.IsDir() || generated.Mode()&fs.ModeSymlink != 0 {
		return nil, false, errors.New("generated directory is not a regular non-symbolic directory")
	}
	before, err := root.Lstat(filepath.FromSlash(generatedApplicationManifestName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", generatedApplicationManifestName, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s is not a regular non-symbolic file", generatedApplicationManifestName)
	}
	if before.Size() > maximumGeneratedManifestSize {
		return nil, false, fmt.Errorf("%s exceeds %d bytes", generatedApplicationManifestName, maximumGeneratedManifestSize)
	}
	file, err := root.Open(filepath.FromSlash(generatedApplicationManifestName))
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", generatedApplicationManifestName, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("inspect opened %s: %w", generatedApplicationManifestName, err)
	}
	if !opened.Mode().IsRegular() || !sameFile(before, opened) {
		_ = file.Close()
		return nil, false, fmt.Errorf("%w: %s was replaced before open", ErrConcurrentChange, generatedApplicationManifestName)
	}
	data, dataErr := io.ReadAll(io.LimitReader(file, maximumGeneratedManifestSize+1))
	closeErr := file.Close()
	if dataErr != nil {
		return nil, false, fmt.Errorf("read %s: %w", generatedApplicationManifestName, dataErr)
	}
	if closeErr != nil {
		return nil, false, fmt.Errorf("close %s: %w", generatedApplicationManifestName, closeErr)
	}
	after, err := root.Lstat(filepath.FromSlash(generatedApplicationManifestName))
	if err != nil || !sameFile(opened, after) {
		return nil, false, fmt.Errorf("%w: %s changed while it was read", ErrConcurrentChange, generatedApplicationManifestName)
	}
	if len(data) > maximumGeneratedManifestSize {
		return nil, false, fmt.Errorf("%s exceeds %d bytes", generatedApplicationManifestName, maximumGeneratedManifestSize)
	}
	return append([]byte(nil), data...), true, nil
}

func sameManifestSnapshot(left, right ManifestSnapshot) bool {
	return sameDirectory(left.root, right.root) &&
		sameManifestPathStates(left.components, right.components) &&
		sameFile(left.file, right.file) &&
		bytes.Equal(left.data, right.data)
}

func sameDirectory(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() && left.Mode() == right.Mode() && os.SameFile(left, right)
}

func sameFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
