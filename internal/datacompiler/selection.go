package datacompiler

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

const maximumGoModBytes = 1 << 20

var (
	// ErrSelection identifies a compiler source that cannot be an exact
	// immutable Data module selection.
	ErrSelection = errors.New("resolve exact Data compiler distribution")
	// ErrInvalidSelection identifies incomplete or substituted module identity.
	ErrInvalidSelection = errors.New("invalid Data compiler module selection")
)

// Source is the verified module identity selected by Core's ordinary Go
// module graph. A workspace or replacement source is not an exact published
// compiler distribution.
type Source struct {
	ModulePath     string
	ModuleVersion  string
	ModuleChecksum string
	Root           string
	Workspace      bool
	Replacement    bool
}

// Selection is the exact Data compiler distribution input for acquisition.
type Selection struct {
	ModulePath     string
	ModuleVersion  string
	ModuleChecksum string
	Root           string
	Manifest       Manifest
	ManifestDigest string
}

// Resolve validates one verified module-graph selection and loads its
// distribution manifest without modifying the module source.
func Resolve(source Source) (Selection, error) {
	root, err := validateModuleRoot(source.Root)
	if source.ModulePath != ModulePath || source.ModuleVersion == "" || module.Check(ModulePath, source.ModuleVersion) != nil || !validModuleChecksum(source.ModuleChecksum) || root == "" || source.Workspace || source.Replacement {
		return Selection{}, fmt.Errorf("%w: %w", ErrSelection, ErrInvalidSelection)
	}
	manifest, digest, err := Load(root)
	if err != nil {
		return Selection{}, fmt.Errorf("%w: %w", ErrSelection, err)
	}
	return Selection{
		ModulePath: source.ModulePath, ModuleVersion: source.ModuleVersion,
		ModuleChecksum: source.ModuleChecksum, Root: root,
		Manifest: manifest, ManifestDigest: digest,
	}, nil
}

func validateModuleRoot(root string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", ErrInvalidSelection
	}
	canonicalRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", ErrInvalidSelection
	}
	rootInfo, err := os.Lstat(canonicalRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&fs.ModeSymlink != 0 {
		return "", ErrInvalidSelection
	}
	goModPath := filepath.Join(canonicalRoot, "go.mod")
	goModInfo, err := os.Lstat(goModPath)
	if err != nil || !goModInfo.Mode().IsRegular() || goModInfo.Mode()&fs.ModeSymlink != 0 || goModInfo.Size() <= 0 || goModInfo.Size() > maximumGoModBytes {
		return "", ErrInvalidSelection
	}
	goMod, err := os.ReadFile(goModPath)
	if err != nil || len(goMod) == 0 || len(goMod) > maximumGoModBytes {
		return "", ErrInvalidSelection
	}
	parsed, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil || parsed.Module == nil || parsed.Module.Mod.Path != ModulePath {
		return "", ErrInvalidSelection
	}
	return canonicalRoot, nil
}
