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
	"strings"
	"sync"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// PrepareSources materializes explicitly requested canonical versions without
// loading their packages or changing Project metadata. Retain captures each
// eligible warm source before acquisition and each completed effective source
// before extracted-manifest validation, the next acquisition, or resolution.
// Retention provides source provenance and checksums, not parsed declarations.
// The same source may be retained again after acquisition to recheck the
// original evidence; metadata remains guarded through the last callback. Local
// sources must already be captured. Before workspace selection, eligible warm
// sources are also retained when wildcard and exact replacements fix the same
// effective source for every possible selected version, or checked cached
// manifests completely infer the synthetic graph's selected requested sources.
// RetainSourceTrees additionally preserves existing trees without original
// archive authority as private pending evidence before selection and acquisition;
// consumers authenticate that same evidence using the completed source callback.
// Retain must honor its supplied context, including the independent bounded
// context used to retain evidence after a settled download.
// Mutable queries and cold resolution reads remain outside this boundary.
func PrepareSources(ctx context.Context, application modulelocate.Module, requested []module.Version, options Options, retain func(context.Context, []Module) error) (resultErr error) {
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
	if callback := options.AcquisitionCache; callback != nil {
		options.AcquisitionCache = func(ctx context.Context, cache string, versions []module.Version) (string, error) {
			selected, err := callback(ctx, cache, versions)
			return selected, errors.Join(err, ctx.Err(), inputs.recheck())
		}
	}
	if callback := options.RetainModuleArchives; callback != nil {
		options.RetainModuleArchives = func(ctx context.Context, archives []ModuleArchive) error {
			return errors.Join(callback(ctx, archives), ctx.Err(), inputs.recheck())
		}
	}
	if options.RetainManifestFiles != nil {
		options.RetainManifestFiles = retainModuleFilesOnce(inputs, options.RetainManifestFiles)
	}
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

func downloadAndRetainSources(ctx context.Context, root string, sources []Module, checksums preparationChecksums, options Options, environment []string, inputs preparationInputs, retain func(context.Context, []Module) error) error {
	if callback := options.RetainSourceTrees; callback != nil {
		options.RetainSourceTrees = func(retainCtx context.Context, directories []string) error {
			return errors.Join(callback(retainCtx, directories), retainCtx.Err(), inputs.recheck())
		}
	}
	if callback := options.RetainModuleInformation; callback != nil {
		options.RetainModuleInformation = func(retainCtx context.Context, paths []string) error {
			return errors.Join(callback(retainCtx, paths), inputs.recheck())
		}
	}
	if len(sources) == 0 {
		return errors.Join(retain(ctx, sources), ctx.Err(), inputs.recheck())
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
		retainMetadata, err := preparationManifestRetainer(ctx, root, []module.Version{sourceVersion(group[0])}, checksums.manifests, options, environment, inputs)
		if err != nil {
			return err
		}
		if err := downloadPreparedSources(ctx, root, group, checksums.archives, options, environment, func(retainCtx context.Context, acquired []Module) error {
			return errors.Join(retain(retainCtx, acquired), ctx.Err(), inputs.recheck())
		}, retainMetadata); err != nil {
			return err
		}
	}
	return nil
}

func downloadPreparedSources(ctx context.Context, root string, result []Module, checksums map[module.Version]string, options Options, environment []string, retain func(context.Context, []Module) error, retainMetadata func(context.Context) error) error {
	cache := ""
	if len(result) != 0 {
		cache = result[0].acquisitionCache
		for _, source := range result {
			if source.acquisitionCache != cache {
				return ErrInvalidOutput
			}
		}
		if cache != "" {
			environment = environmentWith(environment, "GOMODCACHE", cache)
		}
	}
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
	if err := retainUnverifiedPreparedSources(ctx, root, versions, checksums, options, environment); err != nil {
		return err
	}
	var manifests []string
	if options.RetainManifestFiles != nil || options.MetadataBatch != nil && options.RetainRootManifests != nil {
		var err error
		manifests, err = preparationModuleFilePaths(ctx, root, versions, options, environment, ".mod")
		if err != nil {
			return err
		}
	}
	information, err := preparationInformationPaths(ctx, root, versions, options, environment)
	if err != nil {
		return err
	}
	batch := options.MetadataBatch
	if batch != nil {
		present := false
		for _, paths := range [][]string{information, manifests} {
			for _, path := range paths {
				if err := ctx.Err(); err != nil {
					return err
				}
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					present = true
				}
			}
		}
		if !present {
			// Empty cold-cache captures need no additional complete tree walks.
			// A later materialized file still uses ordinary individual checks.
			batch = nil
		}
	}
	// Paths and manifest authority are resolved before this closed local phase.
	// Share complete checks without spanning cache selection or Go work.
	if err := captureSourceBatch(ctx, batch, func(captureCtx context.Context) error {
		if err := retainPresentModuleFiles(captureCtx, information, options.RetainModuleInformation); err != nil {
			return err
		}
		if retainMetadata != nil {
			if err := retainMetadata(captureCtx); err != nil {
				return err
			}
		}
		return retainPresentModuleFiles(captureCtx, manifests, options.RetainManifestFiles)
	}); err != nil {
		return err
	}
	retainArchives, err := preparationArchiveRetainer(ctx, root, versions, options, environment)
	if err != nil {
		return err
	}
	if options.AcquisitionCache != nil {
		output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: environment, OutputLimit: maximumGoModSize}, "env", "-json", "GOMODCACHE", "GOSUMDB", "GONOSUMDB", "GOTOOLCHAIN")
		if err != nil {
			return err
		}
		var selected struct{ GOMODCACHE, GOSUMDB, GONOSUMDB, GOTOOLCHAIN string }
		if json.Unmarshal(output, &selected) != nil || !filepath.IsAbs(selected.GOMODCACHE) {
			return ErrInvalidOutput
		}
		isolated := selected.GOMODCACHE
		withoutChecksumService := selected.GOSUMDB == "off"
		if !withoutChecksumService {
			withoutChecksumService = true
			for _, version := range versions {
				// Distribution modules have stronger verification rules than
				// ordinary modules, even when a private-path pattern matches.
				if version.Path == "golang.org/toolchain" || version.Path == "golang.org/fips140" || !module.MatchPrefixPatterns(selected.GONOSUMDB, version.Path) {
					withoutChecksumService = false
					break
				}
			}
		}
		if withoutChecksumService && selected.GOTOOLCHAIN == "local" {
			isolated, err = options.AcquisitionCache(ctx, selected.GOMODCACHE, append([]module.Version(nil), versions...))
			if err != nil {
				return err
			}
			if !filepath.IsAbs(isolated) {
				return ErrInvalidOutput
			}
		}
		environment = environmentWith(environment, "GOMODCACHE", isolated)
		if isolated != selected.GOMODCACHE {
			cache = isolated
			for _, version := range versions {
				_, manifest, err := cachedSourcePaths(isolated, version)
				if err != nil {
					return err
				}
				manifests = append(manifests, manifest)
				information = append(information, strings.TrimSuffix(manifest, ".mod")+".info")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	output, started, downloadErr := gocommand.OutputObserved(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: environment, OutputLimit: options.OutputLimit}, append([]string{"mod", "download", "-json"}, arguments...)...)
	var acquired []Module
	retainAcquired := func(retainCtx context.Context) error {
		acquired = append([]Module(nil), result...)
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			if err := retainCtx.Err(); err != nil {
				return err
			}
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
				acquired[index].acquisitionCache = cache
				acquired[index].goModFile = downloaded.GoMod
				acquired[index].incompleteSourceMetadata = false
			}
			delete(queries, version)
		}
		if len(queries) != 0 {
			return ErrInvalidOutput
		}
		// Decode the entire acquisition before retaining it, but do not read an
		// extracted manifest until its source evidence has been retained.
		if retain != nil {
			if err := retain(retainCtx, append([]Module(nil), acquired...)); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	var retentionErr error
	if started && !errors.Is(downloadErr, gocommand.ErrProcessUnsettled) {
		// Capture only local evidence after the process has stopped. Cancellation
		// must not discard consumed metadata or start new Go work during cleanup.
		captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		capture := func(captureCtx context.Context) error {
			var captureErr error
			if retainMetadata != nil {
				captureErr = retainMetadata(captureCtx)
			}
			if retainArchives != nil {
				captureErr = errors.Join(captureErr, retainArchives(captureCtx))
			}
			return errors.Join(captureErr,
				retainPresentModuleFiles(captureCtx, manifests, options.RetainManifestFiles),
				retainPresentModuleFiles(captureCtx, information, options.RetainModuleInformation), captureCtx.Err())
		}
		batch := options.MetadataBatch
		if options.SourceBatch != nil {
			batch = options.SourceBatch
			metadataCapture := capture
			capture = func(retainCtx context.Context) error {
				err := metadataCapture(retainCtx)
				if err != nil || downloadErr != nil || ctx.Err() != nil {
					return errors.Join(err, ctx.Err())
				}
				return retainAcquired(retainCtx)
			}
		}
		retentionErr = captureMetadataBatch(captureCtx, batch, capture)
		retentionErr = errors.Join(retentionErr, captureCtx.Err())
	}
	retentionErr = errors.Join(retentionErr, ctx.Err())
	if downloadErr != nil {
		return errors.Join(ErrModuleUnavailable, downloadErr, retentionErr)
	}
	if retentionErr != nil {
		return retentionErr
	}
	if options.SourceBatch == nil {
		if err := retainAcquired(ctx); err != nil {
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

func captureMetadataBatch(ctx context.Context, batch func(context.Context, func(context.Context) error) error, capture func(context.Context) error) error {
	if batch == nil {
		return capture(ctx)
	}
	var mu sync.Mutex
	active, called := true, false
	var captureErr error
	batchErr := batch(ctx, func(derived context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if !active || called {
			captureErr = errors.Join(captureErr, ErrSourceIntegrity)
			return ErrSourceIntegrity
		}
		called = true
		deadline, _ := ctx.Deadline()
		if derived == nil {
			captureErr = ErrSourceIntegrity
		} else if bounded, ok := derived.Deadline(); !ok || bounded.After(deadline) {
			captureErr = ErrSourceIntegrity
		} else {
			captureErr = capture(derived)
		}
		return captureErr
	})
	mu.Lock()
	defer mu.Unlock()
	active = false
	if !called && batchErr == nil {
		batchErr = ErrSourceIntegrity
	}
	return errors.Join(batchErr, captureErr)
}

// captureSourceBatch never spans Go work. Ordinary captures keep caller
// cancellation; settled-download cleanup supplies its own independent context.
func captureSourceBatch(ctx context.Context, batch func(context.Context, func(context.Context) error) error, capture func(context.Context) error) error {
	if batch == nil {
		return capture(ctx)
	}
	captureCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return errors.Join(captureMetadataBatch(captureCtx, batch, capture), captureCtx.Err(), ctx.Err())
}
