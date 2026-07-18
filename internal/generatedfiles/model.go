// Package generatedfiles owns deterministic tracking, comparison, and
// transactional installation of CLI-generated application files.
package generatedfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	// ManifestPath is the application-relative ownership manifest. Its own
	// path is implicit because a file cannot contain its own stable digest.
	ManifestPath            = "generated/.plystra-manifest.json"
	ApplicationManifestPath = "generated/manifest.json"
	manifestVersion         = 2
)

var (
	// ErrOutput reports an invalid desired managed-output model.
	ErrOutput = errors.New("invalid managed generated output")
	// ErrPath reports a generated path outside the CLI-owned source tree or in
	// an ignored build-output subtree.
	ErrPath = errors.New("invalid managed generated path")
)

var ignoredGeneratedDirectories = [...]string{
	"generated/sdk/javascript/dist",
	"generated/sdk/javascript/node_modules",
}

// File is one immutable desired generated file.
type File struct {
	path string
	data []byte
}

// NewFile validates one slash-separated application-relative generated path
// and defensively copies its complete contents.
func NewFile(filePath string, data []byte) (File, error) {
	if !validManagedPath(filePath) {
		return File{}, fmt.Errorf("%w: %w: %q", ErrOutput, ErrPath, filePath)
	}
	return File{path: filePath, data: append([]byte(nil), data...)}, nil
}

// Path returns the canonical slash-separated application-relative path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of the complete file contents.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Output is one immutable, path-sorted desired generated tree and its
// canonical ownership manifest.
type Output struct {
	files        []File
	manifestJSON []byte
	prepared     bool
}

// NewOutput validates uniqueness, sorts files by path, and renders the
// deterministic path and SHA-256 ownership manifest.
func NewOutput(files []File) (Output, error) {
	owned := make([]File, len(files))
	seen := make(map[string]struct{}, len(files))
	for index, file := range files {
		if !validManagedPath(file.path) {
			return Output{}, fmt.Errorf("%w: files[%d]: %w: %q", ErrOutput, index, ErrPath, file.path)
		}
		if _, duplicate := seen[file.path]; duplicate {
			return Output{}, fmt.Errorf("%w: files[%d] duplicates %q", ErrOutput, index, file.path)
		}
		seen[file.path] = struct{}{}
		owned[index] = File{path: file.path, data: append([]byte(nil), file.data...)}
	}
	sort.Slice(owned, func(left, right int) bool { return owned[left].path < owned[right].path })

	document := manifestDocument{Version: manifestVersion, Files: make([]manifestRecord, len(owned))}
	for index, file := range owned {
		document.Files[index] = manifestRecord{Path: file.path, SHA256: digest(file.data)}
		if file.path == ApplicationManifestPath {
			if !json.Valid(file.data) {
				return Output{}, fmt.Errorf("%w: %s is not valid JSON", ErrOutput, ApplicationManifestPath)
			}
			document.ApplicationManifest = append(json.RawMessage(nil), file.data...)
		}
	}
	manifestJSON, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Output{}, fmt.Errorf("%w: encode ownership manifest: %v", ErrOutput, err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if len(manifestJSON) > maximumManifestBytes {
		return Output{}, fmt.Errorf("%w: ownership manifest exceeds %d bytes", ErrOutput, maximumManifestBytes)
	}
	return Output{files: owned, manifestJSON: manifestJSON, prepared: true}, nil
}

// Files returns defensive desired files sorted by path. ManifestPath is
// implicit and is returned separately by ManifestJSON.
func (o Output) Files() []File {
	files := make([]File, len(o.files))
	for index, file := range o.files {
		files[index] = File{path: file.path, data: append([]byte(nil), file.data...)}
	}
	return files
}

// ManifestJSON returns defensive canonical ownership-manifest bytes.
func (o Output) ManifestJSON() []byte { return append([]byte(nil), o.manifestJSON...) }

func (o Output) valid() bool { return o.prepared && len(o.manifestJSON) != 0 }

type manifestDocument struct {
	Version             int              `json:"version"`
	Files               []manifestRecord `json:"files"`
	ApplicationManifest json.RawMessage  `json:"application_manifest,omitempty"`
}

type manifestRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func validManagedPath(value string) bool {
	if !fs.ValidPath(value) || value == "." || strings.ContainsRune(value, '\\') || path.Clean(value) != value {
		return false
	}
	if !strings.HasPrefix(value, "generated/") || value == ManifestPath {
		return false
	}
	return !ignoredGeneratedPath(value)
}

func ignoredGeneratedPath(value string) bool {
	for _, directory := range ignoredGeneratedDirectories {
		if value == directory || strings.HasPrefix(value, directory+"/") {
			return true
		}
	}
	return false
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
