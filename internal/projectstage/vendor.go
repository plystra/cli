package projectstage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type replacementRelocation struct {
	owner             string
	module            string
	version           string
	original          string
	originalToken     string
	workspaceOriginal string
	mapped            string
	workspace         bool
}

// modules.txt is Go's line-oriented vendor inventory. Only exact replacement
// headers change; vendored source and package membership remain authored input.
func (s *Stage) relocateVendor(path string, relocations []replacementRelocation, workspace bool) error {
	info, err := s.files.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumMetadataBytes {
		return ErrStage
	}
	data, err := s.files.ReadFile(path)
	if err != nil {
		return ErrStage
	}
	projectPath, err := filepath.Rel(s.temporary, filepath.Join(s.root, "vendor", "modules.txt"))
	if err != nil {
		return ErrStage
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	for index, line := range lines {
		ending := ""
		if strings.HasSuffix(line, "\r") {
			ending = "\r"
			line = strings.TrimSuffix(line, "\r")
		}
		prefix, target, replaced := strings.Cut(line, " => ")
		if !replaced {
			continue
		}
		fields := strings.Fields(prefix)
		if len(fields) < 2 || len(fields) > 3 || fields[0] != "#" {
			continue
		}
		version := ""
		if len(fields) == 3 {
			version = fields[2]
		}
		best := -1
		for candidate, relocation := range relocations {
			original := relocation.original
			if workspace {
				original = relocation.workspaceOriginal
			}
			if relocation.module != fields[1] || original != target || relocation.version != "" && relocation.version != version {
				continue
			}
			if best < 0 || relocation.workspace && !relocations[best].workspace || relocation.workspace == relocations[best].workspace && relocation.version == version {
				best = candidate
			}
		}
		if best >= 0 {
			lines[index] = prefix + " => " + relocations[best].mapped + ending
			changed = true
		}
	}
	if !workspace && path == projectPath {
		s.projectImages["vendor/modules.txt"] = relocationImage{original: data, baseline: []byte(strings.Join(lines, "\n"))}
	}
	if !changed {
		return nil
	}
	return s.files.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

func (s *Stage) workspaceVendorPath(root, path string) string {
	if s.workspaceBase == "" || filepath.IsAbs(path) {
		return path
	}
	absolute := filepath.Join(root, path)
	relative, err := filepath.Rel(s.workspaceBase, absolute)
	if err != nil {
		return absolute
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, "."+string(filepath.Separator)) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return relative
	}
	return "./" + filepath.ToSlash(relative)
}
