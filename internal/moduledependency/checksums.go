package moduledependency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/module"
)

func sourceVersion(dependency Module) module.Version {
	if dependency.workspace || dependency.replacement.Local() {
		return module.Version{}
	}
	if dependency.replacement.path != "" {
		return module.Version{Path: dependency.replacement.path, Version: dependency.replacement.version}
	}
	return module.Version{Path: dependency.path, Version: dependency.selectedVersion}
}

func validSourceChecksum(value string) bool {
	if !strings.HasPrefix(value, "h1:") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value[3:])
	return err == nil && len(decoded) == sha256.Size && base64.StdEncoding.EncodeToString(decoded) == value[3:]
}

func sourceChecksums(data []byte) (map[module.Version]string, error) {
	result := map[module.Version]string{}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, ErrInvalidOutput
		}
		if strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		version := module.Version{Path: fields[0], Version: fields[1]}
		if module.Check(version.Path, version.Version) != nil || !validSourceChecksum(fields[2]) {
			return nil, ErrInvalidOutput
		}
		if previous, exists := result[version]; exists && previous != fields[2] {
			return nil, ErrInvalidOutput
		}
		result[version] = fields[2]
	}
	return result, nil
}

func optionalGoSum(path string) (fileSnapshot, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	return readGoMod(path)
}

func resolveSourceChecksums(ctx context.Context, applicationRoot string, goMod fileSnapshot, modules []Module, options Options, outputLimit int) (resultErr error) {
	sumPath := filepath.Join(applicationRoot, "go.sum")
	before, err := optionalGoSum(sumPath)
	if err != nil {
		return fmt.Errorf("%w: inspect source checksum inputs: %w", ErrInvalidOutput, err)
	}
	defer func() {
		after, err := optionalGoSum(sumPath)
		if err != nil || (before.info != nil || after.info != nil) && !sameSnapshot(before, after) {
			resultErr = errors.Join(resultErr, ErrConcurrentChange)
		}
		after, err = readGoMod(filepath.Join(applicationRoot, "go.mod"))
		if err != nil || !sameSnapshot(goMod, after) {
			resultErr = errors.Join(resultErr, ErrConcurrentChange)
		}
	}()
	checksums, err := sourceChecksums(before.data)
	if err != nil {
		return fmt.Errorf("%w: invalid source checksum inputs", ErrInvalidOutput)
	}
	queries := map[module.Version][]int{}
	var arguments []string
	for index := range modules {
		version := sourceVersion(modules[index])
		if version.Path == "" {
			continue
		}
		if expected, exists := checksums[version]; exists {
			if reported := modules[index].checksum; reported != "" && reported != expected {
				return fmt.Errorf("%w: source checksum disagrees with Project go.sum", ErrInvalidOutput)
			}
			modules[index].checksum = expected
			continue
		}
		if _, exists := queries[version]; !exists {
			arguments = append(arguments, version.String())
		}
		queries[version] = append(queries[version], index)
	}
	if len(arguments) == 0 {
		return nil
	}
	if options.Offline {
		return fmt.Errorf("%w: full source checksums are not recorded in Project go.sum", ErrModuleUnavailable)
	}

	root, err := os.MkdirTemp("", "plystra-module-checksums-")
	if err != nil {
		return fmt.Errorf("%w: prepare checksum verification: %w", ErrModuleUnavailable, err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	// A separate main module makes Go check cached zip hashes against the
	// Project's recorded sums without changing its module files or interpreting
	// relative replacements from a different filesystem base.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module plystra-checksums\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), before.data, 0o600); err != nil {
		return err
	}
	output, err := gocommand.Output(ctx, gocommand.Options{
		Command: options.GoCommand, Directory: root,
		Environment: environmentWith(options.Environment, "GOWORK", "off"), OutputLimit: outputLimit,
	}, append([]string{"mod", "download", "-json"}, arguments...)...)
	if err != nil {
		return fmt.Errorf("%w: resolve source checksums: %w", ErrModuleUnavailable, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var downloaded downloadedModule
		if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("%w: decode source checksum result", ErrInvalidOutput)
		}
		version := module.Version{Path: downloaded.Path, Version: downloaded.Version}
		indices, exists := queries[version]
		if !exists || !validSourceChecksum(downloaded.Checksum) {
			return fmt.Errorf("%w: inconsistent source checksum result", ErrInvalidOutput)
		}
		for _, index := range indices {
			if !sameDirectory(modules[index].root, downloaded.Dir) ||
				modules[index].checksum != "" && modules[index].checksum != downloaded.Checksum {
				return fmt.Errorf("%w: source changed during checksum resolution", ErrConcurrentChange)
			}
			modules[index].checksum = downloaded.Checksum
		}
		delete(queries, version)
	}
	if len(queries) != 0 {
		return fmt.Errorf("%w: missing source checksum results", ErrInvalidOutput)
	}
	return nil
}
