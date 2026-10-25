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
	"time"

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

func resolveSourceChecksums(ctx context.Context, applicationRoot string, goMod fileSnapshot, modules []Module, options Options, outputLimit int, workspace *preparationWorkspace) (resultErr error) {
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
	if workspace != nil {
		return workspace.resolveChecksums(ctx, modules, options)
	}
	checksums, err := sourceChecksums(before.data)
	if err != nil {
		return fmt.Errorf("%w: invalid source checksum inputs", ErrInvalidOutput)
	}
	queries := map[module.Version][]int{}
	var arguments []string
	var versions []module.Version
	for index := range modules {
		version := sourceVersion(modules[index])
		if version.Path == "" {
			continue
		}
		if reported := modules[index].goModChecksum; reported != "" && !validSourceChecksum(reported) {
			return fmt.Errorf("%w: invalid module manifest checksum", ErrInvalidOutput)
		}
		if expected, exists := checksums[version]; exists {
			if reported := modules[index].checksum; reported != "" && reported != expected {
				return fmt.Errorf("%w: source checksum disagrees with Project go.sum", ErrInvalidOutput)
			}
			modules[index].checksum = expected
			if modules[index].goModChecksum != "" {
				continue
			}
		}
		// Private acquisitions have already run exact Go verification under the
		// original sums. Their complete checksums are download evidence, not the
		// unauthenticated fields of an earlier graph listing.
		if modules[index].acquisitionCache != "" && validSourceChecksum(modules[index].checksum) && validSourceChecksum(modules[index].goModChecksum) {
			continue
		}
		if _, exists := queries[version]; !exists {
			arguments = append(arguments, version.String())
			versions = append(versions, version)
		}
		queries[version] = append(queries[version], index)
	}
	if len(arguments) == 0 {
		return nil
	}
	if options.Offline {
		return fmt.Errorf("%w: source or module manifest checksums are unavailable offline", ErrModuleUnavailable)
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
	retainArchives, err := preparationArchiveRetainer(ctx, root, versions, options, environmentWith(options.Environment, "GOWORK", "off"))
	if err != nil {
		return err
	}
	output, started, downloadErr := gocommand.OutputObserved(ctx, gocommand.Options{
		Command: options.GoCommand, Directory: root,
		Environment: environmentWith(options.Environment, "GOWORK", "off"), OutputLimit: outputLimit,
	}, append([]string{"mod", "download", "-json"}, arguments...)...)
	var retentionErr error
	if started && !errors.Is(downloadErr, gocommand.ErrProcessUnsettled) && retainArchives != nil {
		captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		retentionErr = retainArchives(captureCtx)
	}
	if err := errors.Join(downloadErr, retentionErr, ctx.Err()); err != nil {
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
		if !exists || !validSourceChecksum(downloaded.Checksum) || !validSourceChecksum(downloaded.GoModSum) {
			return fmt.Errorf("%w: inconsistent source checksum result", ErrInvalidOutput)
		}
		for _, index := range indices {
			if !sameDirectory(modules[index].root, downloaded.Dir) ||
				modules[index].checksum != "" && modules[index].checksum != downloaded.Checksum ||
				modules[index].goModChecksum != "" && modules[index].goModChecksum != downloaded.GoModSum {
				return fmt.Errorf("%w: source changed during checksum resolution", ErrConcurrentChange)
			}
			modules[index].checksum = downloaded.Checksum
			modules[index].goModChecksum = downloaded.GoModSum
		}
		delete(queries, version)
	}
	if len(queries) != 0 {
		return fmt.Errorf("%w: missing source checksum results", ErrInvalidOutput)
	}
	return nil
}
