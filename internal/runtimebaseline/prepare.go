package runtimebaseline

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/privatefile"
)

// Writes stages private build output and a local ignore rule in the generation transaction.
func Writes(rootPath string, data []byte) ([]atomicfs.Write, error) {
	if _, err := Decode(data); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, ErrBaseline
	}
	defer root.Close()
	if info, err := root.Lstat("dist"); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrBaseline
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrBaseline
	}
	read := func(name string) ([]byte, bool, error) {
		if info, err := root.Lstat(name); err == nil {
			if !info.Mode().IsRegular() {
				return nil, false, ErrBaseline
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, false, ErrBaseline
		}
		file, err := root.Open(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, ErrBaseline
		}
		defer file.Close()
		if name == Path && privatefile.Check(file) != nil {
			return nil, false, ErrBaseline
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaximumBytes {
			return nil, false, ErrBaseline
		}
		value, err := io.ReadAll(io.LimitReader(file, MaximumBytes+1))
		if err != nil || len(value) > MaximumBytes {
			return nil, false, ErrBaseline
		}
		return value, true, nil
	}
	previous, exists, err := read(Path)
	if err != nil {
		return nil, err
	}
	if exists {
		if _, err := Decode(previous); err != nil {
			return nil, err
		}
	}
	writes := []atomicfs.Write{{Path: Path, Data: data, OwnerPrivate: true, MustNotExist: !exists, ExpectedData: previous}}
	const ignorePath = "dist/.gitignore"
	ignore, exists, err := read(ignorePath)
	if err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(ignore, []byte("/runtime-baseline.json\n")) {
		updated := append([]byte(nil), ignore...)
		if len(updated) > 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, []byte("/runtime-baseline.json\n")...)
		writes = append(writes, atomicfs.Write{Path: ignorePath, Data: updated, MustNotExist: !exists, ExpectedData: ignore})
	}
	return writes, nil
}
