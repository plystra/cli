package moduledependency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// PrepareSources materializes explicitly requested canonical versions without
// loading their packages or changing Project metadata. Retain captures each
// eligible warm source before acquisition and each completed effective source
// before the next acquisition or resolution. Early retention provides source
// provenance and original checksums, not parsed declarations. The same source
// may be retained again after acquisition to recheck the original evidence;
// metadata remains guarded through the last callback. Local sources must
// already be captured. Mutable queries and transitive selections remain outside
// this boundary.
func PrepareSources(ctx context.Context, application modulelocate.Module, requested []module.Version, options Options, retain func([]Module) error) (resultErr error) {
	if ctx == nil || application.Path() == "" || len(requested) > 1_024 || retain == nil {
		return ErrInvalidOutput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	options.Environment = append([]string{}, options.Environment...)
	inputs := preparationInputs{}
	defer func() { resultErr = errors.Join(resultErr, inputs.recheck()) }()
	goModPath, sumPath := filepath.Join(application.Path(), "go.mod"), filepath.Join(application.Path(), "go.sum")
	before, err := inputs.read(goModPath, false)
	if err != nil {
		return err
	}
	sums, err := inputs.read(sumPath, true)
	if err != nil {
		return err
	}
	parsed, err := modfile.Parse("go.mod", before.data, nil)
	if err != nil {
		return ErrInvalidGoMod
	}
	checksums, err := sourceChecksums(sums.data)
	if err != nil {
		return err
	}
	manifestChecksums, err := rootManifestChecksums(sums.data, nil)
	if err != nil {
		return err
	}
	workspace, err := capturePreparationWorkspace(ctx, application.Path(), options, inputs)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var sources []Module
	for _, version := range requested {
		if version.Version == "" || module.Check(version.Path, version.Version) != nil || module.CanonicalVersion(version.Version) != version.Version || seen[version.Path] {
			return ErrInvalidOutput
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
			return ErrSourceIntegrity
		}
		dependency := Module{path: version.Path, selectedVersion: version.Version, sourcePath: effective.Path}
		if replacement != nil {
			dependency.replacement = Replacement{path: effective.Path, version: effective.Version}
		}
		sources = append(sources, dependency)
	}
	root, err := os.MkdirTemp("", "plystra-source-preparation-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module plystra-source-preparation\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), sums.data, 0o600); err != nil {
		return err
	}
	environment := preparationEnvironment(options)
	if options.RetainRootManifests != nil {
		versions := make([]module.Version, len(sources))
		for index, source := range sources {
			versions[index] = sourceVersion(source)
		}
		if err := retainPreparationManifests(ctx, root, versions, manifestChecksums, options, environment, inputs); err != nil {
			return err
		}
	}
	if err := downloadAndRetainSources(ctx, root, sources, preparationChecksums{archives: checksums, manifests: manifestChecksums}, options, environment, inputs, retain); err != nil {
		return err
	}
	if workspace != nil {
		return workspace.prepare(ctx, root, requested, options, environment, inputs, retain)
	}
	return nil
}

func preparationEnvironment(options Options) []string {
	environment := environmentWith(options.Environment, "GOWORK", "off")
	if options.Offline {
		environment = environmentWith(environment, "GOPROXY", "off")
		environment = environmentWith(environment, "GONOPROXY", "none")
		environment = environmentWith(environment, "GOSUMDB", "off")
		environment = environmentWith(environment, "GOTOOLCHAIN", "local")
	}
	return environment
}

func downloadAndRetainSources(ctx context.Context, root string, sources []Module, checksums preparationChecksums, options Options, environment []string, inputs preparationInputs, retain func([]Module) error) error {
	if callback := options.RetainModuleInformation; callback != nil {
		options.RetainModuleInformation = func(paths []string) error {
			return errors.Join(callback(paths), inputs.recheck())
		}
	}
	if len(sources) == 0 {
		return retain(sources)
	}
	indices := map[module.Version]int{}
	var groups [][]Module
	for _, source := range sources {
		version := sourceVersion(source)
		index, exists := indices[version]
		if !exists {
			index = len(groups)
			indices[version] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], source)
	}
	for _, group := range groups {
		if err := retainWarmPreparedSources(ctx, root, group, checksums, options, environment, inputs, retain); err != nil {
			return err
		}
		// Retaining a completed source before another Go process starts keeps
		// later acquisition failures from discarding or refreshing its evidence.
		if err := downloadPreparedSources(ctx, root, group, checksums.archives, options, environment, nil); err != nil {
			return err
		}
		if err := retain(group); err != nil {
			return err
		}
	}
	return nil
}

func downloadPreparedSources(ctx context.Context, root string, result []Module, checksums map[module.Version]string, options Options, environment []string, retain func([]Module) error) (resultErr error) {
	queries := map[module.Version][]int{}
	var arguments []string
	var versions []module.Version
	for index, dependency := range result {
		version := sourceVersion(dependency)
		if _, exists := queries[version]; !exists {
			arguments = append(arguments, version.String())
			versions = append(versions, version)
		}
		queries[version] = append(queries[version], index)
	}
	if len(arguments) == 0 {
		return nil
	}
	information, err := preparationInformationPaths(ctx, root, versions, options, environment)
	if err != nil {
		return err
	}
	if err := retainModuleInformation(ctx, information, options); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, retainModuleInformation(ctx, information, options)) }()
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: environment, OutputLimit: options.OutputLimit}, append([]string{"mod", "download", "-json"}, arguments...)...)
	if err != nil {
		return errors.Join(ErrModuleUnavailable, err)
	}
	acquired := append([]Module(nil), result...)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var downloaded downloadedModule
		if err := decoder.Decode(&downloaded); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return ErrInvalidOutput
		}
		version := module.Version{Path: downloaded.Path, Version: downloaded.Version}
		indices, exists := queries[version]
		if !exists || downloaded.Error != "" || !filepath.IsAbs(downloaded.Dir) || !filepath.IsAbs(downloaded.GoMod) || !validSourceChecksum(downloaded.Checksum) || !validSourceChecksum(downloaded.GoModSum) || checksums[version] != "" && checksums[version] != downloaded.Checksum {
			return ErrSourceIntegrity
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(downloaded.Dir))
		if err != nil {
			return fmt.Errorf("%w: %w: resolve source for module %q: %v", ErrInvalidOutput, ErrModuleUnavailable, version.Path, err)
		}
		for _, index := range indices {
			if result[index].checksum != "" && result[index].checksum != downloaded.Checksum || result[index].goModChecksum != "" && result[index].goModChecksum != downloaded.GoModSum {
				return errors.Join(ErrSourceIntegrity, ErrConcurrentChange)
			}
			acquired[index].root, acquired[index].goMod, acquired[index].goModChecksum = canonical, nil, downloaded.GoModSum
			acquired[index].sourceDirectory, acquired[index].checksum = downloaded.Dir, downloaded.Checksum
			acquired[index].goModFile = downloaded.GoMod
		}
		delete(queries, version)
	}
	if len(queries) != 0 {
		return ErrInvalidOutput
	}
	// Decode the entire acquisition before retaining it, but do not read an
	// extracted manifest until its source evidence has been retained.
	if retain != nil {
		if err := retain(append([]Module(nil), acquired...)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	for index, dependency := range acquired {
		version := sourceVersion(dependency)
		canonical, manifest, err := validateModuleRoot(dependency.path, dependency.sourceDirectory, dependency.goModFile, version.Path, false)
		if err != nil {
			return err
		}
		acquired[index].root, acquired[index].goMod = canonical, manifest
	}
	copy(result, acquired)
	return nil
}
