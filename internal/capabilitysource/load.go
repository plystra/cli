// Package capabilitysource loads local capability declarations without
// following symbolic paths or accepting inconsistent identities.
package capabilitysource

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
)

var (
	// ErrLoad reports that a local capability source could not be loaded.
	ErrLoad = errors.New("load local capability source")
	// ErrUnsafePath reports a symbolic or non-regular schema path component.
	ErrUnsafePath = errors.New("unsafe capability source path")
	// ErrConcurrentChange reports a schema path replaced while it was read.
	ErrConcurrentChange = errors.New("capability source changed during load")
	// ErrIdentityMismatch reports a source declaring a different exact ID.
	ErrIdentityMismatch = errors.New("capability source identity mismatch")
)

// Source is one immutable, identity-checked local capability declaration.
type Source struct {
	id           capabilityid.Identifier
	path         string
	relativePath string
	data         []byte
}

// ID returns the exact capability identity.
func (s Source) ID() capabilityid.Identifier { return s.id }

// Path returns the canonical absolute capability.yaml path.
func (s Source) Path() string { return s.path }

// RelativePath returns the slash-separated path below the plugin directory.
func (s Source) RelativePath() string { return s.relativePath }

// Data returns a defensive copy of the exact source bytes.
func (s Source) Data() []byte { return append([]byte(nil), s.data...) }

// Load reads the exact conventional capability source below pluginPath.
func Load(pluginPath string, expected capabilityid.Identifier) (result Source, loadErr error) {
	if pluginPath == "" {
		return Source{}, fmt.Errorf("%w: plugin path is empty", ErrLoad)
	}
	if expected.String() == "" {
		return Source{}, fmt.Errorf("%w: expected capability is empty", ErrLoad)
	}
	absoluteRoot, err := filepath.Abs(pluginPath)
	if err != nil {
		return Source{}, fmt.Errorf("%w: resolve plugin path: %w", ErrLoad, err)
	}
	rootBefore, err := os.Lstat(absoluteRoot)
	if err != nil {
		return Source{}, fmt.Errorf("%w: inspect plugin path: %w", ErrLoad, err)
	}
	if !rootBefore.IsDir() || rootBefore.Mode()&fs.ModeSymlink != 0 {
		return Source{}, fmt.Errorf("%w: %w: plugin path is not a regular directory", ErrLoad, ErrUnsafePath)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return Source{}, fmt.Errorf("%w: resolve plugin path links: %w", ErrLoad, err)
	}
	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return Source{}, fmt.Errorf("%w: open plugin root: %w", ErrLoad, err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("%w: close plugin root: %w", ErrLoad, err))
		}
	}()
	openedRoot, err := root.Lstat(".")
	if err != nil {
		return Source{}, fmt.Errorf("%w: inspect opened plugin root: %w", ErrLoad, err)
	}
	if !sameDirectory(rootBefore, openedRoot) {
		return Source{}, fmt.Errorf("%w: %w: plugin root was replaced before open", ErrLoad, ErrConcurrentChange)
	}

	relativePath := sourcePath(expected)
	before, err := inspectPath(root, relativePath)
	if err != nil {
		return Source{}, err
	}
	file, err := root.Open(relativePath)
	if err != nil {
		return Source{}, fmt.Errorf("%w: open %s: %w", ErrLoad, relativePath, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return Source{}, fmt.Errorf("%w: inspect opened %s: %w", ErrLoad, relativePath, err)
	}
	if !opened.Mode().IsRegular() || !sameFile(before[len(before)-1].info, opened) {
		_ = file.Close()
		return Source{}, fmt.Errorf("%w: %w: %s was replaced before open", ErrLoad, ErrConcurrentChange, relativePath)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, capabilitymeta.MaximumSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return Source{}, fmt.Errorf("%w: read %s: %w", ErrLoad, relativePath, readErr)
	}
	if closeErr != nil {
		return Source{}, fmt.Errorf("%w: close %s: %w", ErrLoad, relativePath, closeErr)
	}

	rootAfter, rootErr := root.Lstat(".")
	after, pathErr := inspectPath(root, relativePath)
	if rootErr != nil {
		return Source{}, fmt.Errorf("%w: %w: inspect plugin root after read: %w", ErrLoad, ErrConcurrentChange, rootErr)
	}
	if pathErr != nil {
		return Source{}, fmt.Errorf("%w: %w: inspect %s after read: %w", ErrLoad, ErrConcurrentChange, relativePath, pathErr)
	}
	if !sameDirectory(openedRoot, rootAfter) || !samePathStates(before, after) {
		return Source{}, fmt.Errorf("%w: %w: %s changed while it was read", ErrLoad, ErrConcurrentChange, relativePath)
	}
	if len(data) > capabilitymeta.MaximumSize {
		return Source{}, fmt.Errorf("%w: %s exceeds %d bytes", ErrLoad, relativePath, capabilitymeta.MaximumSize)
	}
	declared, err := capabilitymeta.ParseID(data)
	if err != nil {
		return Source{}, fmt.Errorf("%w: %s: %w", ErrLoad, relativePath, err)
	}
	if declared != expected {
		return Source{}, fmt.Errorf("%w: %w: %s declares %s, expected %s", ErrLoad, ErrIdentityMismatch, relativePath, declared, expected)
	}
	return Source{
		id:           expected,
		path:         filepath.Join(canonicalRoot, filepath.FromSlash(relativePath)),
		relativePath: relativePath,
		data:         append([]byte(nil), data...),
	}, nil
}

type pathState struct {
	name string
	info fs.FileInfo
}

func inspectPath(root *os.Root, relativePath string) ([]pathState, error) {
	components := strings.Split(relativePath, "/")
	states := make([]pathState, 0, len(components))
	current := ""
	for index, component := range components {
		current = path.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, fmt.Errorf("%w: inspect %s: %w", ErrLoad, current, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %w: %s is symbolic", ErrLoad, ErrUnsafePath, current)
		}
		if index < len(components)-1 {
			if !info.IsDir() {
				return nil, fmt.Errorf("%w: %w: %s is not a regular directory", ErrLoad, ErrUnsafePath, current)
			}
		} else {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%w: %w: %s is not a regular file", ErrLoad, ErrUnsafePath, current)
			}
			if info.Size() > capabilitymeta.MaximumSize {
				return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrLoad, current, capabilitymeta.MaximumSize)
			}
		}
		states = append(states, pathState{name: current, info: info})
	}
	return states, nil
}

func samePathStates(left, right []pathState) bool {
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

func sameDirectory(left, right fs.FileInfo) bool {
	return left != nil && right != nil && left.IsDir() && right.IsDir() && left.Mode() == right.Mode() && os.SameFile(left, right)
}

func sameFile(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func sourcePath(identifier capabilityid.Identifier) string {
	return path.Join(
		"capabilities",
		identifier.Name(),
		"v"+strconv.FormatUint(identifier.Major(), 10),
		"capability.yaml",
	)
}
