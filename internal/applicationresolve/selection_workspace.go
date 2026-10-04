package applicationresolve

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/moduledependency"
)

type selectionWorkspaceSnapshot struct {
	root     string
	snapshot ManifestSnapshot
}

// Capture workspace-selection inputs without changing gocommand's policy.
// Absent nearer candidates matter too: creating one can redirect Go tooling.
// An excluded enclosing workspace is retained because an edit can activate it.
func captureSelectionWorkspace(root string, environment []string) ([]selectionWorkspaceSnapshot, error) {
	selection := ""
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "GOWORK") {
			selection = value
		}
	}
	if selection == "off" {
		return nil, nil
	}
	if selection != "" && selection != "auto" {
		if !filepath.IsAbs(selection) {
			return nil, ErrConcurrentChange
		}
		snapshot, err := readManifestSnapshot(filepath.Dir(selection), filepath.Base(selection))
		if err != nil {
			return nil, err
		}
		return []selectionWorkspaceSnapshot{{root: filepath.Dir(selection), snapshot: snapshot}}, nil
	}
	var result []selectionWorkspaceSnapshot
	for directory := root; ; directory = filepath.Dir(directory) {
		snapshot, err := readSelectionWorkspace(directory, "go.work")
		if err != nil {
			return nil, err
		}
		result = append(result, selectionWorkspaceSnapshot{root: directory, snapshot: snapshot})
		if snapshot.file != nil || filepath.Dir(directory) == directory {
			return result, nil
		}
	}
}

func readSelectionWorkspace(root, name string) (ManifestSnapshot, error) {
	snapshot, err := readManifestSnapshot(root, name)
	if !errors.Is(err, fs.ErrNotExist) {
		return snapshot, err
	}
	info, rootErr := os.Lstat(root)
	_, fileErr := os.Lstat(filepath.Join(root, name))
	if rootErr != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 || !errors.Is(fileErr, fs.ErrNotExist) {
		return ManifestSnapshot{}, ErrConcurrentChange
	}
	return ManifestSnapshot{path: name, root: info}, nil
}

func (s SelectionInputs) recheckWorkspace() error {
	for _, before := range s.workspaceSnapshots {
		after, err := readSelectionWorkspace(before.root, before.snapshot.path)
		if err != nil {
			return selectionWorkspaceError(s.module.ModulePath())
		}
		equal := sameManifestSnapshot(before.snapshot, after)
		if before.snapshot.file == nil && after.file == nil {
			equal = sameDirectory(before.snapshot.root, after.root)
		}
		if !equal {
			return selectionWorkspaceError(s.module.ModulePath())
		}
	}
	return nil
}

func selectionWorkspaceError(modulePath string) error {
	return newManifestSourceError(modulePath, "go.work", "module-dependency", 0, 0,
		fmt.Errorf("%w: %w: workspace selection input changed or cannot be captured", ErrConcurrentChange, moduledependency.ErrConcurrentChange))
}
