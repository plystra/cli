package moduledependency

import (
	"bytes"
	"context"
	"errors"
	"go/version"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/sumdb/dirhash"
)

type manifestPruning uint8

const (
	manifestPruned manifestPruning = iota
	manifestUnpruned
	manifestWorkspace
)

type manifestGraph struct {
	root                string
	mains               []*modfile.File
	workspace           *preparationWorkspace
	inputs              preparationInputs
	checksums           map[module.Version]string
	excluded            map[module.Version]bool
	cache               string
	loadCache           func(context.Context) (string, error)
	toolchain           string
	retain              func(context.Context, []RootManifest) error
	retainManifestFiles func(context.Context, []string) error
	retainInformation   func(context.Context, []string) error
	retainSources       func([]Module) error
	retainTrees         func(context.Context, []string) error
	archives            map[module.Version]string
	queryPaths          map[string]bool // Nil queries all selected modules; graph traversal remains unfiltered.
	retained            map[string]bool
	parsed              map[string]*modfile.File
	goVersions          map[module.Version]bool
	bytes               int64
}

func (graph *manifestGraph) capture(ctx context.Context) error {
	if !graph.initialized() {
		return nil // Inference requires a known toolchain and valid initialization.
	}
	graph.goVersions = map[module.Version]bool{}
	type load struct {
		module.Version
		pruning manifestPruning
	}
	var queue []load
	seen := map[load]bool{}
	enqueue := func(mod module.Version, pruning manifestPruning) error {
		if mod.Path == "go" || mod.Path == "toolchain" || mod.Version == "none" {
			return nil
		}
		key := load{mod, pruning}
		if seen[key] {
			return nil
		}
		if len(seen) >= 65_536 {
			return ErrInvalidOutput
		}
		seen[key] = true
		queue = append(queue, key)
		return nil
	}
	mainPaths := map[string]bool{}
	selected := map[string]string{}
	for _, main := range graph.mains {
		mainPaths[main.Module.Mod.Path] = true
		selected[main.Module.Mod.Path] = ""
	}
	selectVersion := func(mod module.Version) {
		if mainPaths[mod.Path] || mod.Path == "go" || mod.Path == "toolchain" {
			return
		}
		if previous, exists := selected[mod.Path]; !exists || semver.Compare(mod.Version, previous) > 0 {
			selected[mod.Path] = mod.Version
		}
	}
	if graph.workspace != nil {
		for _, main := range graph.mains {
			if err := enqueue(main.Module.Mod, manifestWorkspace); err != nil {
				return err
			}
		}
	} else {
		for _, required := range graph.mains[0].Require {
			if !graph.excluded[required.Mod] {
				selectVersion(required.Mod)
				if err := enqueue(required.Mod, pruningForManifest(graph.mains[0])); err != nil {
					return err
				}
			}
		}
	}
	complete := true
	requirements := map[module.Version][]module.Version{}
	loadQueue := func() error {
		for len(queue) != 0 {
			batch := queue
			queue = nil
			mods := make([]module.Version, len(batch))
			for index, current := range batch {
				mods[index] = current.Version
			}
			if err := graph.retainBatch(ctx, mods); err != nil {
				return err
			}
			for _, current := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				parsed, available, err := graph.read(ctx, current.Version, false)
				if err != nil {
					return err
				}
				if !available {
					complete = false
					continue
				}
				nextPruning := pruningForManifest(parsed)
				if current.pruning == manifestUnpruned {
					nextPruning = manifestUnpruned
				}
				var required []module.Version
				for _, dependency := range parsed.Require {
					if graph.excluded[dependency.Mod] {
						continue
					}
					required = append(required, dependency.Mod)
					selectVersion(dependency.Mod)
					if current.pruning != manifestPruned || pruningForManifest(parsed) == manifestUnpruned {
						if err := enqueue(dependency.Mod, nextPruning); err != nil {
							return err
						}
					}
				}
				requirements[current.Version] = required
			}
		}
		return nil
	}
	if err := loadQueue(); err != nil {
		return err
	}
	if graph.workspace != nil {
		tracked, expanded := map[string]bool{}, map[module.Version]bool{}
		for path := range mainPaths {
			tracked[path] = true
		}
		for complete {
			needed := map[module.Version]bool{}
			for path := range tracked {
				mod := module.Version{Path: path, Version: selected[path]}
				if !expanded[mod] {
					needed[mod] = true
					continue
				}
				for _, required := range requirements[mod] {
					selection := module.Version{Path: required.Path, Version: selected[required.Path]}
					if (mainPaths[selection.Path] || semver.Compare(selection.Version, required.Version) > 0) && !expanded[selection] {
						needed[selection] = true
					}
				}
			}
			if len(needed) == 0 {
				break
			}
			var ordered []module.Version
			for mod := range needed {
				ordered = append(ordered, mod)
			}
			module.Sort(ordered)
			for _, mod := range ordered {
				if err := enqueue(mod, manifestWorkspace); err != nil {
					return err
				}
				expanded[mod], tracked[mod.Path] = true, true
			}
			if err := loadQueue(); err != nil {
				return err
			}
		}
	}
	if complete {
		var selectedModules []module.Version
		for path, selectedVersion := range selected {
			if !mainPaths[path] && (graph.queryPaths == nil || graph.queryPaths[path]) {
				selectedModules = append(selectedModules, module.Version{Path: path, Version: selectedVersion})
			}
		}
		module.Sort(selectedModules)
		if err := graph.retainSelectedInformation(ctx, selectedModules); err != nil {
			return err
		}
		if err := graph.retainSelectedSources(ctx, selectedModules); err != nil {
			return err
		}
		var candidates []module.Version
		possibleVersions := map[module.Version]bool{}
		for _, mod := range selectedModules {
			if graph.goVersions[mod] {
				continue
			}
			effective, err := graph.effectiveVersion(mod)
			if err != nil {
				return err
			}
			candidates = append(candidates, mod)
			possibleVersions[effective] = true
		}
		module.Sort(candidates)
		var demanded []module.Version
		for _, mod := range candidates {
			effective, err := graph.effectiveVersion(mod)
			if err != nil {
				return err
			}
			// Concurrent info filling can learn this logical module's Go version
			// through another replacement and skip its own replacement summary.
			if effective != mod && possibleVersions[mod] {
				continue
			}
			demanded = append(demanded, mod)
		}
		if err := graph.retainBatch(ctx, demanded); err != nil {
			return err
		}
		for _, mod := range demanded {
			// Module information reads selected manifests even when their graph
			// requirements were pruned. It does not traverse those requirements.
			if _, _, err := graph.read(ctx, mod, true); err != nil {
				return err
			}
		}
	} else if graph.queryPaths == nil {
		if err := graph.retainFixedRootInputs(ctx); err != nil {
			return err
		}
	}
	return graph.inputs.recheck()
}

func (graph *manifestGraph) retainFixedRootInputs(ctx context.Context) error {
	if graph.retainTrees == nil && graph.retainInformation == nil && graph.retainManifestFiles == nil {
		return nil
	}
	mains := map[string]bool{}
	var replacements [][]*modfile.Replace
	if graph.workspace != nil {
		replacements = append(replacements, graph.workspace.work.Replace)
	}
	for _, main := range graph.mains {
		mains[main.Module.Mod.Path] = true
		replacements = append(replacements, main.Replace)
	}
	seenPaths := map[string]bool{}
	var roots []module.Version
	for _, main := range graph.mains {
		for _, required := range main.Require {
			path := required.Mod.Path
			if mains[path] || path == "go" || path == "toolchain" || graph.excluded[required.Mod] || seenPaths[path] {
				continue
			}
			seenPaths[path] = true
			roots = append(roots, module.Version{Path: path})
		}
	}
	module.Sort(roots)
	budget := 65_536
	seenSources := map[module.Version]bool{}
	var sources []module.Version
	for _, root := range roots {
		effective, fixed := uniformReplacementSource(ctx, root.Path, replacements, graph.effectiveVersion, &budget)
		if !fixed || effective.Version == "" || seenSources[effective] {
			continue
		}
		seenSources[effective] = true
		sources = append(sources, effective)
	}
	if err := graph.retainEffectiveManifestFiles(ctx, sources); err != nil {
		return err
	}
	if err := graph.retainEffectiveInformation(ctx, sources); err != nil {
		return err
	}
	if graph.retainTrees == nil {
		return errors.Join(ctx.Err(), graph.inputs.recheck())
	}
	for _, effective := range sources {
		cache, err := graph.moduleCache(ctx)
		if err != nil {
			return err
		}
		directory, manifest, err := cachedSourcePaths(cache, effective)
		if err != nil {
			return err
		}
		// The physical source is fixed, but the logical selected version is
		// still unknown. Retain only pending evidence, without source provenance.
		err = retainUnverifiedSourceTree(ctx, directory, manifest, graph.retainTrees)
		if err := errors.Join(err, ctx.Err(), graph.inputs.recheck()); err != nil {
			return err
		}
	}
	return errors.Join(ctx.Err(), graph.inputs.recheck())
}

func (graph *manifestGraph) retainSelectedSources(ctx context.Context, selected []module.Version) error {
	if graph.retainSources == nil && graph.retainTrees == nil {
		return nil
	}
	groups := map[module.Version][]Module{}
	var versions []module.Version
	for _, mod := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		effective, err := graph.effectiveVersion(mod)
		if err != nil {
			return err
		}
		if effective.Version == "" {
			continue // Local sources were already captured with the Project.
		}
		source := Module{path: mod.Path, selectedVersion: mod.Version}
		replaced := effective != mod
		if !replaced {
			if graph.workspace != nil {
				replaced = rootManifestReplacement(mod, graph.workspace.work.Replace) != nil
			}
			for _, main := range graph.mains {
				replaced = replaced || rootManifestReplacement(mod, main.Replace) != nil
			}
		}
		if replaced {
			source.replacement = Replacement{path: effective.Path, version: effective.Version}
		}
		if _, exists := groups[effective]; !exists {
			versions = append(versions, effective)
		}
		groups[effective] = append(groups[effective], source)
	}
	checksums := preparationChecksums{archives: graph.archives, manifests: graph.checksums}
	for _, effective := range versions {
		cache, err := graph.moduleCache(ctx)
		if err != nil {
			return err
		}
		if err := retainWarmSourceBundle(ctx, cache, groups[effective], checksums, graph.inputs, graph.retainSources, graph.retainTrees); err != nil {
			return err
		}
	}
	return nil
}

func (graph *manifestGraph) retainEffectiveManifestFiles(ctx context.Context, versions []module.Version) error {
	if graph.retainManifestFiles == nil {
		return nil
	}
	var paths []string
	for _, effective := range versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := graph.manifestPath(ctx, effective)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	// This evidence never enters retained or parsed: observing an identity does
	// not supply the independent checksum needed to read a graph summary.
	return errors.Join(retainPresentModuleFiles(ctx, paths, graph.retainManifestFiles), ctx.Err(), graph.inputs.recheck())
}

func (graph *manifestGraph) initialized() bool {
	if !version.IsValid(graph.toolchain) {
		return false
	}
	if graph.workspace != nil && (len(graph.workspace.work.Godebug) != 0 || graph.workspace.work.Go != nil && version.Compare("go"+graph.workspace.work.Go.Version, graph.toolchain) > 0) {
		return false
	}
	paths := map[string]bool{}
	for _, main := range graph.mains {
		if main.Module == nil || main.Module.Mod.Path == "go" || main.Module.Mod.Path == "toolchain" || module.CheckImportPath(main.Module.Mod.Path) != nil || paths[main.Module.Mod.Path] || graph.workspace == nil && len(main.Godebug) != 0 {
			return false
		}
		for _, tool := range main.Tool {
			if module.CheckImportPath(tool.Path) != nil {
				return false
			}
		}
		paths[main.Module.Mod.Path] = true
		if main.Go != nil {
			goVersion := "go" + main.Go.Version
			if version.Compare(goVersion, graph.toolchain) > 0 {
				return false
			}
			if graph.workspace != nil && version.Compare(goVersion, "go1.21") >= 0 && (graph.workspace.work.Go == nil || version.Compare(goVersion, "go"+graph.workspace.work.Go.Version) > 0) {
				return false
			}
		}
	}
	if graph.workspace == nil {
		requiredPaths := map[string]bool{}
		for _, required := range graph.mains[0].Require {
			if graph.excluded[required.Mod] {
				continue
			}
			if paths[required.Mod.Path] || requiredPaths[required.Mod.Path] {
				return false
			}
			requiredPaths[required.Mod.Path] = true
		}
	}
	return true
}

func exactManifestVersion(path, value string) (string, error) {
	_, pathMajor, ok := module.SplitPathVersion(path)
	if !ok || module.CanonicalVersion(value) != value || module.CheckPathMajor(value, pathMajor) != nil {
		return "", ErrInvalidGoMod
	}
	return value, nil
}

func pruningForManifest(parsed *modfile.File) manifestPruning {
	if parsed.Go == nil || version.Compare("go"+parsed.Go.Version, "go1.17") < 0 {
		return manifestUnpruned
	}
	return manifestPruned
}

func (graph *manifestGraph) effectiveVersion(mod module.Version) (module.Version, error) {
	if graph.workspace != nil {
		return graph.workspace.rootManifestVersion(mod)
	}
	if replacement := rootManifestReplacement(mod, graph.mains[0].Replace); replacement != nil {
		effective := replacement.New
		if effective.Version == "" && !filepath.IsAbs(effective.Path) {
			effective.Path = filepath.Join(graph.root, effective.Path)
		}
		return effective, nil
	}
	return mod, nil
}

func (graph *manifestGraph) moduleCache(ctx context.Context) (string, error) {
	if graph.cache == "" {
		if graph.loadCache == nil {
			return "", ErrInvalidOutput
		}
		cache, err := graph.loadCache(ctx)
		if err != nil {
			return "", err
		}
		graph.cache = cache
	}
	return graph.cache, nil
}

func (graph *manifestGraph) manifestPath(ctx context.Context, effective module.Version) (string, error) {
	if effective.Version == "" {
		return filepath.Join(effective.Path, "go.mod"), nil
	}
	escaped, pathErr := module.EscapePath(effective.Path)
	value, versionErr := module.EscapeVersion(effective.Version)
	if pathErr != nil || versionErr != nil {
		return "", ErrInvalidGoMod
	}
	cache, err := graph.moduleCache(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "cache", "download", filepath.FromSlash(escaped), "@v", value+".mod"), nil
}

func (graph *manifestGraph) retainBatch(ctx context.Context, mods []module.Version) error {
	var effectiveVersions []module.Version
	for _, mod := range mods {
		if err := ctx.Err(); err != nil {
			return err
		}
		if graph.workspace != nil && mod.Version == "" {
			continue
		}
		effective, err := graph.effectiveVersion(mod)
		if err != nil {
			return err
		}
		effectiveVersions = append(effectiveVersions, effective)
	}
	return graph.retainEffectiveBatch(ctx, effectiveVersions)
}

func (graph *manifestGraph) retainEffectiveBatch(ctx context.Context, versions []module.Version) error {
	var manifests []RootManifest
	seen := map[string]bool{}
	for _, effective := range versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if effective.Version == "" || !validSourceChecksum(graph.checksums[effective]) {
			continue
		}
		path, err := graph.manifestPath(ctx, effective)
		if err != nil {
			return err
		}
		if graph.retained[path] || seen[path] {
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		seen[path] = true
		manifests = append(manifests, RootManifest{Path: path, Checksum: graph.checksums[effective]})
	}
	if len(manifests) != 0 {
		if graph.retain != nil {
			if err := graph.retain(ctx, manifests); err != nil {
				return err
			}
		}
		for _, manifest := range manifests {
			graph.retained[manifest.Path] = true
		}
	}
	return graph.inputs.recheck()
}

func (graph *manifestGraph) retainSelectedInformation(ctx context.Context, mods []module.Version) error {
	if graph.retainInformation == nil {
		return nil
	}
	var effective []module.Version
	for _, mod := range mods {
		version, err := graph.effectiveVersion(mod)
		if err != nil {
			return err
		}
		effective = append(effective, version)
	}
	return graph.retainEffectiveInformation(ctx, effective)
}

func (graph *manifestGraph) retainEffectiveInformation(ctx context.Context, mods []module.Version) error {
	if graph.retainInformation == nil {
		return nil
	}
	var paths []string
	seen := map[string]bool{}
	for _, mod := range mods {
		if mod.Version == "" {
			continue
		}
		path, err := graph.manifestPath(ctx, mod)
		if err != nil {
			return err
		}
		path = strings.TrimSuffix(path, ".mod") + ".info"
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return errors.Join(retainPresentModuleFiles(ctx, paths, graph.retainInformation), graph.inputs.recheck())
}

func (graph *manifestGraph) read(ctx context.Context, mod module.Version, infoOnly bool) (*modfile.File, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if mod.Version == "" && graph.workspace != nil {
		for _, main := range graph.mains {
			if main.Module.Mod.Path == mod.Path {
				return main, true, nil
			}
		}
	}
	effective, err := graph.effectiveVersion(mod)
	if err != nil {
		return nil, false, err
	}
	path, err := graph.manifestPath(ctx, effective)
	if err != nil {
		return nil, false, err
	}
	if effective.Version != "" && !graph.retained[path] {
		return nil, false, nil
	}
	parsed, summarized := graph.parsed[path]
	if infoOnly && summarized {
		return nil, true, nil
	}
	if parsed == nil {
		snapshot, err := graph.inputs.read(path, false)
		if err != nil {
			return nil, false, err
		}
		graph.bytes += int64(len(snapshot.data))
		if graph.bytes > 512<<20 {
			return nil, false, ErrInvalidOutput
		}
		if effective.Version != "" {
			checksum, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(snapshot.data)), nil
			})
			if err != nil || checksum != graph.checksums[effective] {
				return nil, false, ErrSourceIntegrity
			}
		}
		if infoOnly {
			graph.parsed[path] = nil
			return nil, true, nil // Go ignores summary syntax and identity errors here.
		}
		parsed, err = modfile.ParseLax("go.mod", snapshot.data, nil)
		if err != nil {
			return nil, false, ErrInvalidGoMod
		}
		graph.parsed[path] = parsed
	}
	if parsed.Go != nil {
		graph.goVersions[effective] = true
	}
	if effective.Version != "" && (parsed.Module == nil || parsed.Module.Mod.Path != mod.Path && parsed.Module.Mod.Path != effective.Path) {
		return nil, false, ErrInvalidGoMod
	}
	if parsed.Go != nil && version.Compare("go"+parsed.Go.Version, graph.toolchain) > 0 {
		return nil, false, nil
	}
	return parsed, true, nil
}
