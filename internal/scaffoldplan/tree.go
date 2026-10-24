package scaffoldplan

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/runtimebaseline"
)

type entry struct {
	digest [sha256.Size]byte
	info   os.FileInfo
}

// This baseline is captured after relocation, so staging-only Go path rewrites
// cannot masquerade as requested application changes. Digests stay private.
func capture(ctx context.Context, directory string) (map[string]entry, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	result := map[string]entry{}
	count, total := 0, int64(0)
	err = fs.WalkDir(root.FS(), ".", func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > projectstage.MaximumEntries {
			return ErrPreview
		}
		if item.IsDir() {
			return nil
		}
		info, err := root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() > projectstage.MaximumFileBytes {
			return ErrPreview
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		opened, err := file.Stat()
		if err != nil || !os.SameFile(info, opened) {
			file.Close()
			return ErrPreview
		}
		hash := sha256.New()
		read, readErr := io.Copy(hash, io.LimitReader(file, min(projectstage.MaximumFileBytes, projectstage.MaximumTotalBytes-total)+1))
		closeErr := file.Close()
		total += read
		after, statErr := root.Lstat(name)
		if readErr != nil || closeErr != nil || statErr != nil || total > projectstage.MaximumTotalBytes || read != info.Size() || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return errors.Join(ErrPreview, readErr, closeErr, statErr)
		}
		value := entry{info: info}
		copy(value.digest[:], hash.Sum(nil))
		result[name] = value
		return nil
	})
	return result, err
}

func differences(before, after map[string]entry, configuration string, forceConfiguration bool) []commandschema.ChangePlanPathInput {
	paths := map[string]struct{}{runtimebaseline.Path: {}}
	for name := range before {
		paths[name] = struct{}{}
	}
	for name := range after {
		paths[name] = struct{}{}
	}
	var result []commandschema.ChangePlanPathInput
	for name := range paths {
		old, existed := before[name]
		current, exists := after[name]
		force := name == runtimebaseline.Path || name == configuration && forceConfiguration
		if existed == exists && old.digest == current.digest && !force || !existed && !exists {
			continue
		}
		result = append(result, commandschema.ChangePlanPathInput{Path: name, Kind: pathKind(name), BeforeExists: existed, AfterExists: exists})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func readCapturedFile(ctx context.Context, root *os.Root, name string, expected entry) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := root.Lstat(name)
	if err != nil || expected.info == nil || !info.Mode().IsRegular() || !os.SameFile(info, expected.info) || info.Mode() != expected.info.Mode() || info.Size() != expected.info.Size() || !info.ModTime().Equal(expected.info.ModTime()) {
		return nil, ErrPreview
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, ErrPreview
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, ErrPreview
	}
	data, readErr := io.ReadAll(io.LimitReader(file, projectstage.MaximumFileBytes+1))
	closeErr := file.Close()
	after, afterErr := root.Lstat(name)
	if ctx.Err() != nil || readErr != nil || closeErr != nil || afterErr != nil || int64(len(data)) != info.Size() || !os.SameFile(info, after) || info.Mode() != after.Mode() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || sha256.Sum256(data) != expected.digest {
		return nil, errors.Join(ErrPreview, ctx.Err(), readErr, closeErr, afterErr)
	}
	return data, nil
}

func pathKind(name string) string {
	switch {
	case name == runtimebaseline.Path:
		return "private-baseline"
	case name == "go.mod" || name == "go.sum":
		return "module"
	case strings.HasPrefix(name, "generated/"):
		return "generated"
	default:
		return "authored"
	}
}
