package projectstage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/plystra/cli/internal/privatefile"
	"golang.org/x/mod/module"
)

// AcquisitionCache isolates exact downloads whose extracted sources are absent
// or partial. Complete extracted trees keep their existing acquisition path:
// rebuilding them from an archive would silently repair their authored bytes.
// The caller must retain ordinary Go checksum authority and use local toolchain
// selection with the checksum service disabled. The returned cache lives until
// Close. Only positive original inputs are retained; cache absence is not frozen.
func (s *Stage) AcquisitionCache(ctx context.Context, cache string, versions []module.Version) (string, error) {
	if err := s.Recheck(ctx); err != nil {
		return "", err
	}
	if !filepath.IsAbs(cache) || len(versions) == 0 || len(versions) > MaximumRoots {
		return "", ErrStage
	}
	var paths []string
	fallback := false
	seen := map[module.Version]bool{}
	for _, version := range versions {
		path, pathErr := module.EscapePath(version.Path)
		value, versionErr := module.EscapeVersion(version.Version)
		if pathErr != nil || versionErr != nil || module.Check(version.Path, version.Version) != nil || module.CanonicalVersion(version.Version) != version.Version || seen[version] {
			return "", ErrStage
		}
		seen[version] = true
		if version.Path == "golang.org/toolchain" || version.Path == "golang.org/fips140" {
			fallback = true
		}
		prefix := filepath.Join("cache", "download", filepath.FromSlash(path), "@v", value)
		directory := filepath.Join(cache, filepath.FromSlash(path)+"@"+value)
		info, err := os.Lstat(directory)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				fallback = true
				continue
			}
			if !info.IsDir() {
				return "", ErrStage
			}
			if _, err := os.Lstat(filepath.Join(cache, prefix+".partial")); errors.Is(err, os.ErrNotExist) {
				fallback = true
			} else if err != nil {
				return "", ErrStage
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", ErrStage
		}
		paths = append(paths, prefix)
	}
	if fallback {
		return cache, s.Recheck(ctx)
	}
	name, err := s.newAcquisitionCache(ctx)
	if err != nil {
		return "", err
	}
	for _, prefix := range paths {
		for _, suffix := range []string{".info", ".mod", ".ziphash", ".zip"} {
			relative := prefix + suffix
			original := filepath.Join(cache, relative)
			if _, err := os.Lstat(original); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return "", ErrStage
			}
			request := filepath.ToSlash(strings.TrimPrefix(relative, filepath.Join("cache", "download")+string(filepath.Separator)))
			data, err := s.retainAcquisitionFile(ctx, original, request, "cache")
			if err != nil {
				return "", err
			}
			if err := s.writeCacheSeed(ctx, name, relative, data); err != nil {
				return "", err
			}
		}
	}
	return filepath.Join(s.temporary, name), s.Recheck(ctx)
}

func (s *Stage) newAcquisitionCache(ctx context.Context) (string, error) {
	defer s.lock()()
	if err := s.recheck(ctx); err != nil {
		return "", err
	}
	if len(s.cacheRoots) >= MaximumRoots {
		return "", ErrStage
	}
	name, err := privatefile.MkdirTempRoot(s.files, "module-cache-")
	if err != nil {
		return "", err
	}
	s.cacheRoots = append(s.cacheRoots, filepath.Join(s.temporary, name))
	_, _, err = s.acquisitionDirectory(filepath.Join(s.temporary, name))
	return name, err
}

func (s *Stage) writeCacheSeed(ctx context.Context, name, relative string, data []byte) error {
	defer s.lock()()
	if err := s.recheck(ctx); err != nil {
		return err
	}
	if err := s.files.MkdirAll(filepath.Join(name, filepath.Dir(relative)), 0o700); err != nil {
		return err
	}
	return writePrivate(s.files, filepath.Join(name, relative), data)
}

// acquisitionDirectory admits only registered cache outputs, never arbitrary
// staged Project paths. Their provenance is live-stage evidence; cleanup must
// not turn generated cache outputs into missing original input files.
func (s *Stage) acquisitionDirectory(path string) (string, bool, error) {
	for _, root := range s.cacheRoots {
		if !within(root, path) {
			continue
		}
		canonical, err := canonicalDirectory(path)
		if err != nil || canonical != filepath.Clean(path) {
			return "", true, ErrStage
		}
		for current := canonical; within(root, current); current = filepath.Dir(current) {
			relative, err := filepath.Rel(s.temporary, current)
			if err != nil {
				return "", true, ErrStage
			}
			info, access, err := readAccessInfo(s.files, relative)
			if err != nil || !info.IsDir() {
				return "", true, ErrStage
			}
			found := false
			for _, before := range s.stagedPaths {
				if before.path == relative {
					if !sameFile(before.info, info) || before.access != access {
						return "", true, ErrConcurrentChange
					}
					found = true
					break
				}
			}
			if !found {
				if len(s.stagedPaths) >= MaximumEntries {
					return "", true, ErrStage
				}
				s.stagedPaths = append(s.stagedPaths, pathSnapshot{path: relative, info: info, access: access})
			}
			if current == root {
				break
			}
		}
		return canonical, true, nil
	}
	return "", false, nil
}

func (s *Stage) prepareCacheCleanup() error {
	if len(s.cacheRoots) == 0 || runtime.GOOS == "windows" {
		return nil
	}
	root, err := s.parent.OpenRoot(filepath.Base(s.temporary))
	if err != nil {
		return ErrStage
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(info, s.identity) {
		return ErrConcurrentChange
	}
	for _, cache := range s.cacheRoots {
		relative, err := filepath.Rel(s.temporary, cache)
		if err != nil || !filepath.IsLocal(relative) || relative == "." {
			return ErrStage
		}
		if err := writableCacheDirectories(root, relative); err != nil {
			return err
		}
	}
	return nil
}

// Go's default extracted directories are read-only on Unix. Change only
// directories inside this owned stage, using opened handles and refusing
// symbolic traversal; original cache permissions must never be repaired.
func writableCacheDirectories(root *os.Root, path string) error {
	before, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !before.IsDir() {
		return ErrConcurrentChange
	}
	directory, err := root.Open(path)
	if err != nil {
		return ErrStage
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return ErrConcurrentChange
	}
	if err := directory.Chmod(opened.Mode().Perm() | 0o700); err != nil {
		return ErrStage
	}
	for {
		children, readErr := directory.ReadDir(128)
		for _, child := range children {
			if child.IsDir() {
				if err := writableCacheDirectories(root, filepath.Join(path, child.Name())); err != nil {
					return err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return ErrStage
		}
	}
}
