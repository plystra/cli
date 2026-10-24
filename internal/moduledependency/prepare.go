package moduledependency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// PrepareSources materializes explicitly requested canonical versions without
// loading their packages or changing Project metadata. The caller must capture
// returned trees before executing resolution. Local replacements are omitted;
// their original sources must already be captured. This single-module boundary
// does not resolve mutable queries, workspaces, or transitive selections.
func PrepareSources(ctx context.Context, application modulelocate.Module, requested []module.Version, options Options) (result []Module, resultErr error) {
	if ctx == nil || application.Path() == "" || len(requested) > 1_024 {
		return nil, ErrInvalidOutput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	goModPath, sumPath := filepath.Join(application.Path(), "go.mod"), filepath.Join(application.Path(), "go.sum")
	before, err := readGoMod(goModPath)
	if err != nil {
		return nil, err
	}
	sums, err := optionalGoSum(sumPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		after, err := readGoMod(goModPath)
		if err != nil || !sameSnapshot(before, after) {
			resultErr = errors.Join(resultErr, ErrConcurrentChange)
		}
		after, err = optionalGoSum(sumPath)
		if err != nil || (sums.info != nil || after.info != nil) && !sameSnapshot(sums, after) {
			resultErr = errors.Join(resultErr, ErrConcurrentChange)
		}
		if resultErr != nil {
			result = nil
		}
	}()
	parsed, err := modfile.Parse("go.mod", before.data, nil)
	if err != nil {
		return nil, ErrInvalidGoMod
	}
	checksums, err := sourceChecksums(sums.data)
	if err != nil {
		return nil, err
	}
	queries := map[module.Version][]int{}
	seen := map[string]bool{}
	var arguments []string
	for _, version := range requested {
		if version.Version == "" || module.Check(version.Path, version.Version) != nil || module.CanonicalVersion(version.Version) != version.Version || seen[version.Path] {
			return nil, ErrInvalidOutput
		}
		seen[version.Path] = true
		effective := version
		var replacement *modfile.Replace
		for _, candidate := range parsed.Replace {
			if candidate.Old.Path == version.Path && (candidate.Old.Version == version.Version || candidate.Old.Version == "" && replacement == nil) {
				replacement = candidate
			}
		}
		if replacement != nil {
			if replacement.New.Version == "" {
				continue
			}
			effective = replacement.New
		}
		if options.Offline && checksums[effective] == "" {
			return nil, ErrSourceIntegrity
		}
		dependency := Module{path: version.Path, selectedVersion: version.Version, sourcePath: effective.Path}
		if replacement != nil {
			dependency.replacement = Replacement{path: effective.Path, version: effective.Version}
		}
		if _, exists := queries[effective]; !exists {
			arguments = append(arguments, effective.String())
		}
		queries[effective] = append(queries[effective], len(result))
		result = append(result, dependency)
	}
	if len(arguments) == 0 {
		return result, nil
	}
	root, err := os.MkdirTemp("", "plystra-source-preparation-")
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module plystra-source-preparation\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), sums.data, 0o600); err != nil {
		return nil, err
	}
	environment := environmentWith(options.Environment, "GOWORK", "off")
	if options.Offline {
		environment = environmentWith(environment, "GOPROXY", "off")
		environment = environmentWith(environment, "GONOPROXY", "none")
		environment = environmentWith(environment, "GOSUMDB", "off")
		environment = environmentWith(environment, "GOTOOLCHAIN", "local")
	}
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: environment, OutputLimit: options.OutputLimit}, append([]string{"mod", "download", "-json"}, arguments...)...)
	if err != nil {
		return nil, errors.Join(ErrModuleUnavailable, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var downloaded downloadedModule
		if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, ErrInvalidOutput
		}
		version := module.Version{Path: downloaded.Path, Version: downloaded.Version}
		indices, exists := queries[version]
		if !exists || !filepath.IsAbs(downloaded.Dir) || !validSourceChecksum(downloaded.Checksum) || checksums[version] != "" && checksums[version] != downloaded.Checksum {
			return nil, ErrSourceIntegrity
		}
		canonical, manifest, err := validateModuleRoot(version.Path, downloaded.Dir, downloaded.GoMod, version.Path, false)
		if err != nil {
			return nil, err
		}
		for _, index := range indices {
			result[index].root, result[index].goMod, result[index].goModChecksum = canonical, append([]byte(nil), manifest...), downloaded.GoModSum
			result[index].sourceDirectory, result[index].checksum = downloaded.Dir, downloaded.Checksum
		}
		delete(queries, version)
	}
	if len(queries) != 0 {
		return nil, ErrInvalidOutput
	}
	return result, nil
}
