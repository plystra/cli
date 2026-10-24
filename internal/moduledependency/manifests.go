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

// RootManifest identifies an already-cached single-module graph input and its
// independent checksum from the original Project go.sum.
type RootManifest struct {
	Path     string
	Checksum string
}

func retainRootManifests(ctx context.Context, root string, manifest fileSnapshot, options Options, inputs preparationInputs) error {
	parsed, err := modfile.Parse("go.mod", manifest.data, nil)
	if err != nil {
		return ErrInvalidGoMod
	}
	sums, err := inputs.read(filepath.Join(root, "go.sum"), true)
	if err != nil {
		return err
	}
	checksums := map[module.Version]string{}
	// Go discards this historical checksum of an empty manifest before checking
	// recorded rows, even when it precedes the real checksum.
	const emptyGoModHash = "h1:G7mAYYxgmS0lVkHyy2hEOLQCFB0DlQFTMLWggykrydY="
	for _, line := range bytes.Split(sums.data, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) != 3 || !strings.HasSuffix(fields[1], "/go.mod") || !strings.HasPrefix(fields[2], "h1:") || fields[2] == emptyGoModHash {
			continue
		}
		version := module.Version{Path: fields[0], Version: strings.TrimSuffix(fields[1], "/go.mod")}
		if _, exists := checksums[version]; !exists {
			checksums[version] = fields[2] // Go checks the first h1 row before later matches.
		}
	}
	excluded := map[module.Version]bool{}
	for _, exclusion := range parsed.Exclude {
		excluded[exclusion.Mod] = true
	}
	seen := map[module.Version]bool{}
	var versions []module.Version
	for _, required := range parsed.Require {
		if excluded[required.Mod] {
			continue
		}
		version := required.Mod
		var replacement *modfile.Replace
		for _, candidate := range parsed.Replace {
			if candidate.Old.Path == version.Path && (candidate.Old.Version == version.Version || candidate.Old.Version == "" && replacement == nil) {
				replacement = candidate
			}
		}
		if replacement != nil {
			version = replacement.New
		}
		if version.Version == "" || !validSourceChecksum(checksums[version]) || seen[version] {
			continue
		}
		seen[version] = true
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil
	}
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: options.Environment, OutputLimit: maximumGoModSize}, "env", "-json", "GOMODCACHE")
	if err != nil {
		return err
	}
	var selected struct{ GOMODCACHE string }
	if json.Unmarshal(output, &selected) != nil || !filepath.IsAbs(selected.GOMODCACHE) {
		return ErrInvalidOutput
	}
	var manifests []RootManifest
	for _, version := range versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, pathErr := module.EscapePath(version.Path)
		value, versionErr := module.EscapeVersion(version.Version)
		if pathErr != nil || versionErr != nil {
			return ErrInvalidGoMod
		}
		name := filepath.Join(selected.GOMODCACHE, "cache", "download", filepath.FromSlash(path), "@v", value+".mod")
		if _, err := os.Lstat(name); errors.Is(err, os.ErrNotExist) {
			continue // Go may materialize a missing manifest during resolution.
		} else if err != nil {
			return err
		}
		manifests = append(manifests, RootManifest{Path: name, Checksum: checksums[version]})
	}
	if len(manifests) != 0 {
		if err := options.RetainRootManifests(manifests); err != nil {
			return err
		}
	}
	return inputs.recheck()
}
