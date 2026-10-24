package projectstage

import (
	"bytes"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

type relocationImage struct {
	original []byte
	baseline []byte
}

type replacementToken struct {
	start int
	end   int
	text  string
}

// Relocation provenance retains the authored query; Go owns its resolution.
func originalModuleVersion(_ string, version string) (string, error) {
	if module.CanonicalVersion(version) == "" {
		return "", ErrStage
	}
	return version, nil
}

// Match an observed version without selecting or resolving one. Exact captured
// keys take precedence over resolved query spellings and vendor wildcards.
func replacementVersionMatch(original, current string, vendor bool) int {
	if original == current {
		return 3
	}
	if original == "" && vendor {
		return 1
	}
	if current == "" || module.CanonicalVersion(current) != current {
		return 0
	}
	if original == semver.Major(original) || original == semver.MajorMinor(original) {
		if strings.HasPrefix(current, original+".") && semver.Compare(current, original) >= 0 {
			return 2
		}
		return 0
	}
	if module.CanonicalVersion(original) == current {
		return 2
	}
	return 0
}

// Parse normalizes quoted tokens in place. Recover the authored target from
// its validated syntax span before relocation so harmless quoting survives too.
func originalReplacementToken(data []byte, replacement *modfile.Replace) (replacementToken, error) {
	start, end := replacement.Syntax.Span()
	target := slices.Index(replacement.Syntax.Token, "=>") + 1
	if target == 0 || start.Byte < 0 || end.Byte > len(data) || start.Byte >= end.Byte {
		return replacementToken{}, ErrStage
	}
	raw := string(data[start.Byte:end.Byte])
	offset := start.Byte
	for index := 0; ; index++ {
		trimmed := strings.TrimLeft(raw, " \t\r\n")
		offset += len(raw) - len(trimmed)
		raw = trimmed
		if raw == "" {
			return replacementToken{}, ErrStage
		}
		var token string
		if strings.HasPrefix(raw, `"`) {
			var err error
			token, err = strconv.QuotedPrefix(raw)
			if err != nil {
				return replacementToken{}, ErrStage
			}
		} else {
			length := strings.IndexAny(raw, " \t\r\n")
			if length < 0 {
				length = len(raw)
			}
			token = raw[:length]
		}
		if index == target {
			decoded := token
			if strings.HasPrefix(token, `"`) {
				var err error
				decoded, err = strconv.Unquote(token)
				if err != nil {
					return replacementToken{}, ErrStage
				}
			}
			if decoded != replacement.New.Path {
				return replacementToken{}, ErrStage
			}
			return replacementToken{start: offset, end: offset + len(token), text: token}, nil
		}
		raw = raw[len(token):]
		offset += len(token)
	}
}

// OriginalProjectBytes removes only this Project's staging-local module and
// vendor replacement rewrites. Dependency edits survive. An unchanged relocated
// baseline yields the exact authored bytes, including formatting and spelling.
// Other Project-relative slash paths yield a defensive copy without rewriting.
func (s *Stage) OriginalProjectBytes(path string, stagedData []byte) ([]byte, error) {
	if s == nil || !projectFilePath(path) || len(stagedData) > MaximumFileBytes {
		return nil, ErrStage
	}
	if inputs := s.inputs.captured(); inputs == nil || inputs.project == "" {
		return nil, ErrStage
	}
	if path != "go.mod" && path != "vendor/modules.txt" {
		return append([]byte(nil), stagedData...), nil
	}
	if len(stagedData) > maximumMetadataBytes {
		return nil, ErrStage
	}
	if image, found := s.projectImages[path]; found && bytes.Equal(image.baseline, stagedData) {
		return append([]byte(nil), image.original...), nil
	}
	var data []byte
	var err error
	if path == "go.mod" {
		data, err = s.originalModule(stagedData)
	} else {
		data, err = s.originalVendor(stagedData)
	}
	if err != nil || s.containsTemporaryPath(data) {
		return nil, ErrStage
	}
	return data, nil
}

func (s *Stage) originalModule(data []byte) ([]byte, error) {
	module, err := modfile.Parse("go.mod", data, originalModuleVersion)
	if err != nil {
		return nil, ErrStage
	}
	var edits []replacementToken
	for _, replacement := range module.Replace {
		if replacement.New.Version != "" {
			continue
		}
		var selected *replacementRelocation
		best, ambiguous := 0, false
		for _, relocation := range s.relocations {
			if relocation.workspace || relocation.owner != s.project || relocation.module != replacement.Old.Path ||
				!s.sameTarget(replacement.New.Path, relocation.mapped) {
				continue
			}
			match := replacementVersionMatch(relocation.version, replacement.Old.Version, false)
			if match == 0 || match < best {
				continue
			}
			ambiguous = match == best && (ambiguous || selected.originalToken != relocation.originalToken)
			selected = &relocation
			best = match
		}
		if ambiguous {
			return nil, ErrStage
		}
		if selected == nil {
			if !s.authoredTarget(replacement.Old.Path, replacement.Old.Version, replacement.New.Path, false) && s.temporaryTarget(replacement.New.Path) {
				return nil, ErrStage
			}
			continue
		}
		token, err := originalReplacementToken(data, replacement)
		if err != nil {
			return nil, ErrStage
		}
		token.text = selected.originalToken
		edits = append(edits, token)
	}
	return rewriteReplacementTokens(data, edits)
}

func rewriteReplacementTokens(data []byte, edits []replacementToken) ([]byte, error) {
	slices.SortFunc(edits, func(a, b replacementToken) int { return a.start - b.start })
	var result []byte
	offset := 0
	for _, edit := range edits {
		if edit.start < offset || edit.end < edit.start || edit.end > len(data) {
			return nil, ErrStage
		}
		result = append(result, data[offset:edit.start]...)
		result = append(result, edit.text...)
		offset = edit.end
	}
	return append(result, data[offset:]...), nil
}

func (s *Stage) originalVendor(data []byte) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
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
		var selected *replacementRelocation
		best, ambiguous := 0, false
		for _, relocation := range s.relocations {
			if relocation.workspace || relocation.owner != s.project || relocation.module != fields[1] ||
				!s.sameTarget(target, relocation.mapped) {
				continue
			}
			match := replacementVersionMatch(relocation.version, version, true)
			if match == 0 || match < best {
				continue
			}
			ambiguous = match == best && (ambiguous || selected.original != relocation.original)
			selected = &relocation
			best = match
		}
		if ambiguous {
			return nil, ErrStage
		}
		if selected != nil {
			lines[index] = prefix + " => " + selected.original + ending
		} else if !s.authoredTarget(fields[1], version, target, true) && s.temporaryTarget(target) {
			return nil, ErrStage
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (s *Stage) authoredTarget(modulePath, version, target string, vendor bool) bool {
	module, err := modfile.Parse("go.mod", s.projectImages["go.mod"].original, originalModuleVersion)
	if err != nil {
		return false
	}
	for _, replacement := range module.Replace {
		if replacement.Old.Path == modulePath && replacement.New.Version == "" && replacement.New.Path == target &&
			replacementVersionMatch(replacement.Old.Version, version, vendor) != 0 {
			return true
		}
	}
	return false
}

// Compare lexically against captured mappings, without consulting the candidate
// or following directories whose contents may already have been edited.
func (s *Stage) sameTarget(target, mapped string) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.root, target)
	}
	relative, err := filepath.Rel(mapped, filepath.Clean(target))
	return err == nil && relative == "."
}

func (s *Stage) temporaryTarget(target string) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(s.root, target)
	}
	return within(s.temporary, target)
}

func (s *Stage) containsTemporaryPath(data []byte) bool {
	// Go module tokens can quote and escape native Windows path separators.
	text := strings.ReplaceAll(string(data), `\\`, `\`)
	text = strings.ReplaceAll(text, `\`, "/")
	return strings.Contains(strings.ToLower(text), strings.ToLower(filepath.ToSlash(s.temporary)))
}
