package moduledependency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// RootManifest identifies an already-cached graph input and its independent
// checksum from the original Project or workspace checksum files.
type RootManifest struct {
	Path     string
	Checksum string
}

func retainRootManifests(ctx context.Context, root string, manifest fileSnapshot, options Options, inputs preparationInputs, workspace *preparationWorkspace) (func(context.Context) error, error) {
	var manifests []*modfile.File
	var primary []byte
	var memberSums [][]byte
	if workspace == nil {
		parsed, err := modfile.Parse("go.mod", manifest.data, nil)
		if err != nil {
			return nil, ErrInvalidGoMod
		}
		sums, err := inputs.read(filepath.Join(root, "go.sum"), true)
		if err != nil {
			return nil, err
		}
		manifests, primary = []*modfile.File{parsed}, sums.data
	} else {
		primary = workspace.sum
		for _, member := range workspace.members {
			manifests = append(manifests, member.mod)
			memberSums = append(memberSums, member.sum)
		}
	}
	for index := range manifests {
		owner := root
		if workspace != nil {
			owner = workspace.members[index].root
		}
		original, exists := inputs[filepath.Join(owner, "go.mod")]
		if !exists {
			return nil, ErrInvalidGoMod
		}
		// A prefix such as v1 is a Go query, not the v1.0.0 identity produced
		// by nil-fixer parsing. No early cache identity is established here.
		if _, err := modfile.Parse("go.mod", original.data, exactManifestVersion); err != nil {
			return nil, nil
		}
	}
	if workspace != nil {
		if err := workspace.validateRootManifestReplacements(); err != nil {
			return nil, err
		}
	}
	checksums, err := rootManifestChecksums(primary, memberSums)
	if err != nil {
		return nil, err
	}
	var archives map[module.Version]string
	if options.RetainSelectedSources != nil || options.RetainSourceTrees != nil {
		archives, err = rootChecksums(primary, memberSums, false)
		if err != nil {
			return nil, err
		}
	}
	excluded := map[module.Version]bool{}
	for _, parsed := range manifests {
		for _, exclusion := range parsed.Exclude {
			excluded[exclusion.Mod] = true
		}
	}
	seen := map[module.Version]bool{}
	var versions []module.Version
	for _, parsed := range manifests {
		for _, required := range parsed.Require {
			if required.Mod.Path == "go" || required.Mod.Path == "toolchain" || excluded[required.Mod] {
				continue
			}
			version := required.Mod
			if workspace != nil {
				version, err = workspace.rootManifestVersion(version)
				if err != nil {
					return nil, err
				}
			} else if replacement := rootManifestReplacement(version, parsed.Replace); replacement != nil {
				version = replacement.New
			}
			// Readonly single-module listing rejects absent h1 rows before reading
			// the manifest. Workspace listing can read it without an original sum.
			raw := options.RetainManifestFiles != nil && (workspace != nil || strings.HasPrefix(checksums[version], "h1:"))
			if version.Version == "" || seen[version] || !raw && !validSourceChecksum(checksums[version]) {
				continue
			}
			seen[version] = true
			versions = append(versions, version)
		}
	}
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: options.Environment, OutputLimit: maximumGoModSize}, "env", "-json", "GOMODCACHE", "GOVERSION")
	if err != nil {
		return nil, err
	}
	var selected struct{ GOMODCACHE, GOVERSION string }
	if json.Unmarshal(output, &selected) != nil || !filepath.IsAbs(selected.GOMODCACHE) {
		return nil, ErrInvalidOutput
	}
	var retained []RootManifest
	var manifestFiles []string
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, pathErr := module.EscapePath(version.Path)
		value, versionErr := module.EscapeVersion(version.Version)
		if pathErr != nil || versionErr != nil {
			return nil, ErrInvalidGoMod
		}
		name := filepath.Join(selected.GOMODCACHE, "cache", "download", filepath.FromSlash(path), "@v", value+".mod")
		if options.RetainManifestFiles != nil {
			manifestFiles = append(manifestFiles, name)
		}
		if !validSourceChecksum(checksums[version]) {
			continue
		}
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			continue // Go may materialize a missing manifest during resolution.
		} else if err != nil {
			return nil, err
		}
		retained = append(retained, RootManifest{Path: name, Checksum: checksums[version]})
	}
	retainFiles := rootManifestFileRetainer(manifestFiles, inputs, options.RetainManifestFiles)
	if err := retainFiles(ctx); err != nil {
		return nil, err
	}
	if len(retained) != 0 && options.RetainRootManifests != nil {
		if err := options.RetainRootManifests(ctx, append([]RootManifest(nil), retained...)); err != nil {
			return nil, err
		}
	}
	if err := inputs.recheck(); err != nil {
		return nil, err
	}
	graph := manifestGraph{
		root: root, mains: manifests, workspace: workspace, inputs: inputs,
		checksums: checksums, excluded: excluded, cache: selected.GOMODCACHE,
		toolchain: selected.GOVERSION, retain: options.RetainRootManifests,
		retainManifestFiles: options.RetainManifestFiles,
		retainInformation:   options.RetainModuleInformation,
		retainSources:       options.RetainSelectedSources, retainTrees: options.RetainSourceTrees, archives: archives,
		sourceBatch: options.SourceBatch,
		retained:    map[string]bool{}, parsed: map[string]*modfile.File{},
	}
	for _, manifest := range retained {
		graph.retained[manifest.Path] = true
	}
	if err := graph.capture(ctx); err != nil {
		return nil, err
	}
	if err := retainFiles(ctx); err != nil {
		return nil, err
	}
	// Settled listing may have filled a cold graph frontier. Recover only
	// manifests authorized by the original sums, retaining the same snapshots.
	graph.retainInformation, graph.retainSources, graph.retainTrees = nil, nil, nil
	graph.retainManifestFiles = nil
	return func(captureCtx context.Context) error {
		if err := retainFiles(captureCtx); err != nil {
			return err
		}
		if graph.retain == nil {
			return nil
		}
		return graph.capture(captureCtx)
	}, nil
}

func rootManifestFileRetainer(files []string, inputs preparationInputs, retain func(context.Context, []string) error) func(context.Context) error {
	captured := map[string]bool{}
	return func(ctx context.Context) error {
		if retain == nil || len(files) == 0 {
			return nil
		}
		for {
			previous := len(captured)
			err := retainPresentModuleFiles(ctx, files, func(ctx context.Context, paths []string) error {
				if err := retain(ctx, paths); err != nil {
					return err
				}
				for _, path := range paths {
					captured[path] = true
				}
				return nil
			})
			if err != nil {
				return err
			}
			// A callback may materialize another known root. Drain those paths
			// without refreshing evidence held by retainModuleFilesOnce.
			if len(captured) == previous {
				recheckErr := inputs.recheck()
				return errors.Join(ctx.Err(), recheckErr)
			}
		}
	}
}

func rootManifestChecksums(primary []byte, members [][]byte) (map[module.Version]string, error) {
	return rootChecksums(primary, members, true)
}

func rootChecksums(primary []byte, members [][]byte, manifests bool) (map[module.Version]string, error) {
	checksums := map[module.Version]string{}
	primaryKeys := map[module.Version]bool{}
	for index, sums := range append([][]byte{primary}, members...) {
		for _, line := range bytes.Split(sums, []byte{'\n'}) {
			fields := strings.Fields(string(line))
			if len(fields) == 0 {
				continue
			}
			if len(fields) != 3 {
				return nil, ErrInvalidOutput
			}
			// Go ignores the historical empty-manifest hash before checking rows.
			if strings.HasSuffix(fields[1], "/go.mod") != manifests || !strings.HasPrefix(fields[2], "h1:") || fields[2] == "h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY=" {
				continue
			}
			version := module.Version{Path: fields[0], Version: strings.TrimSuffix(fields[1], "/go.mod")}
			if primaryKeys[version] {
				continue // The first primary h1 row decides before any member rows.
			}
			if previous, exists := checksums[version]; exists && previous != fields[2] {
				checksums[version] = "" // A member conflict cannot regain authority.
			} else {
				checksums[version] = fields[2]
			}
			if index == 0 {
				primaryKeys[version] = true
			}
		}
	}
	return checksums, nil
}

func rootManifestReplacement(version module.Version, replacements []*modfile.Replace) *modfile.Replace {
	var selected *modfile.Replace
	for _, candidate := range replacements {
		if candidate.Old.Path == version.Path && (candidate.Old.Version == version.Version || candidate.Old.Version == "" && selected == nil) {
			selected = candidate
		}
	}
	return selected
}

func (workspace *preparationWorkspace) rootManifestVersion(version module.Version) (module.Version, error) {
	if replacement := rootManifestReplacement(version, workspace.work.Replace); replacement != nil {
		selected := replacement.New
		if selected.Version == "" && !filepath.IsAbs(selected.Path) {
			selected.Path = filepath.Join(filepath.Dir(workspace.path), selected.Path)
		}
		return selected, nil
	}
	var selected module.Version
	var selectedRoot string
	found := false
	for _, member := range workspace.members {
		replacement := rootManifestReplacement(version, member.mod.Replace)
		if replacement == nil {
			continue
		}
		next := replacement.New
		// Go visits members in workfile order. A later version-specific rule
		// conflicts with a different earlier match, rather than overriding it.
		if found && replacement.Old.Version != "" && selected != next {
			return module.Version{}, ErrInvalidGoMod
		}
		selected, found = next, true
		selectedRoot = member.root
	}
	if found {
		if selected.Version == "" && !filepath.IsAbs(selected.Path) {
			selected.Path = filepath.Join(selectedRoot, selected.Path)
		}
		return selected, nil
	}
	return version, nil
}

func (workspace *preparationWorkspace) validateRootManifestReplacements() error {
	mainPaths, overridden := map[string]bool{}, map[string]bool{}
	for _, member := range workspace.members {
		mainPaths[member.mod.Module.Mod.Path] = true
	}
	workReplacements := map[module.Version]module.Version{}
	for _, replacement := range workspace.work.Replace {
		if previous, exists := workReplacements[replacement.Old]; exists && previous != replacement.New {
			return ErrInvalidGoMod
		}
		if mainPaths[replacement.Old.Path] && replacement.Old.Version == "" {
			return ErrInvalidGoMod
		}
		workReplacements[replacement.Old] = replacement.New
		overridden[replacement.Old.Path] = true
	}
	replacements := map[module.Version]module.Version{}
	for _, member := range workspace.members {
		local := map[module.Version]module.Version{}
		for _, replacement := range member.mod.Replace {
			if previous, exists := local[replacement.Old]; exists && previous != replacement.New {
				return ErrInvalidGoMod
			}
			local[replacement.Old] = replacement.New
			if overridden[replacement.Old.Path] {
				continue
			}
			next := replacement.New
			// Initialization compares member-relative local paths after rebasing;
			// lookup separately compares their original spelling.
			if next.Version == "" && !filepath.IsAbs(next.Path) {
				next.Path = filepath.Join(member.root, next.Path)
			}
			if previous, exists := replacements[replacement.Old]; exists && previous != next {
				return ErrInvalidGoMod
			}
			replacements[replacement.Old] = next
		}
	}
	return nil
}
