// Package generatedfiles owns deterministic tracking, comparison, and
// transactional installation of CLI-generated application files.
package generatedfiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// ManifestPath is the application-relative ownership manifest. Its own
	// path is implicit because a file cannot contain its own stable digest.
	ManifestPath            = "generated/.plystra-manifest.json"
	ApplicationManifestPath = "generated/manifest.json"
	manifestVersion         = 3

	maximumArtifactValueBytes = 4096
	maximumArtifactValues     = 4096
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

// ArtifactKind classifies one generated artifact without deriving semantics
// from its filename during later inspection.
type ArtifactKind string

const (
	ArtifactKindGoSource              ArtifactKind = "go-source"
	ArtifactKindProtobufSource        ArtifactKind = "protobuf-source"
	ArtifactKindProtobufDescriptor    ArtifactKind = "protobuf-descriptor"
	ArtifactKindJavaScriptSource      ArtifactKind = "javascript-source"
	ArtifactKindJavaScriptPackage     ArtifactKind = "javascript-package"
	ArtifactKindDocumentation         ArtifactKind = "documentation"
	ArtifactKindCompatibilityBaseline ArtifactKind = "compatibility-baseline"
	ArtifactKindWireMap               ArtifactKind = "wire-map"
	ArtifactKindApplicationManifest   ArtifactKind = "application-manifest"
	ArtifactKindOwnershipManifest     ArtifactKind = "ownership-manifest"
)

// CleanupOwnership is the closed authority used for stale-file removal.
type CleanupOwnership string

const CleanupOwnershipCLI CleanupOwnership = "cli-owned"

const ownershipManifestGenerator = "plystra.generated-ownership/v3"

// ArtifactInput is the complete stable explanation input for one generated
// file. InputRecordIDs identify normalized non-secret records; Sources contain
// stable declaration or selected-configuration references.
type ArtifactInput struct {
	Generator      string
	Kind           ArtifactKind
	InputRecordIDs []string
	Sources        []string
}

// Artifact is one immutable generated-file provenance record.
type Artifact struct {
	path             string
	sha256           string
	generator        string
	kind             ArtifactKind
	inputRecordIDs   []string
	sources          []string
	cleanupOwnership CleanupOwnership
	prepared         bool
}

// Path returns the canonical Project-relative generated path.
func (a Artifact) Path() string { return a.path }

// SHA256 returns the exact content digest recorded for this artifact.
func (a Artifact) SHA256() string { return a.sha256 }

// Generator returns the exact owning built-in generator and version.
func (a Artifact) Generator() string { return a.generator }

// Kind returns the closed output classification.
func (a Artifact) Kind() ArtifactKind { return a.kind }

// InputRecordIDs returns deterministic normalized input identities.
func (a Artifact) InputRecordIDs() []string {
	return append([]string(nil), a.inputRecordIDs...)
}

// Sources returns deterministic stable declaration and configuration sources.
func (a Artifact) Sources() []string { return append([]string(nil), a.sources...) }

// CleanupOwnership reports which boundary may remove this exact artifact.
func (a Artifact) CleanupOwnership() CleanupOwnership { return a.cleanupOwnership }

// Valid reports whether the artifact is complete and internally canonical.
func (a Artifact) Valid() bool { return validateArtifact(a) == nil }

// File is one immutable desired generated file.
type File struct {
	path     string
	data     []byte
	artifact Artifact
}

// NewFile validates one slash-separated application-relative generated path
// and its stable provenance, then defensively copies its complete contents.
func NewFile(filePath string, data []byte, input ArtifactInput) (File, error) {
	if !validManagedPath(filePath) {
		return File{}, fmt.Errorf("%w: %w: %q", ErrOutput, ErrPath, filePath)
	}
	artifact, err := newArtifact(filePath, digest(data), input)
	if err != nil {
		return File{}, fmt.Errorf("%w: artifact provenance for %s: %v", ErrOutput, filePath, err)
	}
	return File{path: filePath, data: append([]byte(nil), data...), artifact: artifact}, nil
}

// Path returns the canonical slash-separated application-relative path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of the complete file contents.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Artifact returns defensive stable provenance for this file.
func (f File) Artifact() Artifact { return cloneArtifact(f.artifact) }

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
		if !validManagedPath(file.path) || !file.artifact.Valid() || file.artifact.path != file.path || file.artifact.sha256 != digest(file.data) {
			return Output{}, fmt.Errorf("%w: files[%d]: %w: %q", ErrOutput, index, ErrPath, file.path)
		}
		if _, duplicate := seen[file.path]; duplicate {
			return Output{}, fmt.Errorf("%w: files[%d] duplicates %q", ErrOutput, index, file.path)
		}
		seen[file.path] = struct{}{}
		owned[index] = File{path: file.path, data: append([]byte(nil), file.data...), artifact: cloneArtifact(file.artifact)}
	}
	sort.Slice(owned, func(left, right int) bool { return owned[left].path < owned[right].path })

	document := manifestDocument{Version: manifestVersion, Files: make([]manifestRecord, len(owned))}
	for index, file := range owned {
		document.Files[index] = manifestRecord{
			Path:             file.artifact.path,
			SHA256:           file.artifact.sha256,
			Generator:        file.artifact.generator,
			OutputKind:       file.artifact.kind,
			InputRecordIDs:   append([]string{}, file.artifact.inputRecordIDs...),
			Sources:          append([]string{}, file.artifact.sources...),
			CleanupOwnership: file.artifact.cleanupOwnership,
		}
		if file.path == ApplicationManifestPath {
			if !json.Valid(file.data) {
				return Output{}, fmt.Errorf("%w: %s is not valid JSON", ErrOutput, ApplicationManifestPath)
			}
			canonical, err := json.Marshal(json.RawMessage(file.data))
			if err != nil {
				return Output{}, fmt.Errorf("%w: canonicalize %s: %v", ErrOutput, ApplicationManifestPath, err)
			}
			canonical = append(canonical, '\n')
			if !bytes.Equal(file.data, canonical) {
				return Output{}, fmt.Errorf("%w: %s must use canonical compact JSON with one trailing newline", ErrOutput, ApplicationManifestPath)
			}
			document.ApplicationManifest = append(json.RawMessage(nil), canonical...)
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
		files[index] = File{path: file.path, data: append([]byte(nil), file.data...), artifact: cloneArtifact(file.artifact)}
	}
	return files
}

// Artifacts returns every desired file plus the implicit ownership-manifest
// artifact in canonical path order.
func (o Output) Artifacts() []Artifact {
	if !o.valid() {
		return nil
	}
	result := make([]Artifact, 0, len(o.files)+1)
	for _, file := range o.files {
		result = append(result, cloneArtifact(file.artifact))
	}
	result = append(result, ownershipManifestArtifact(o.manifestJSON, result))
	sort.Slice(result, func(left, right int) bool { return result[left].path < result[right].path })
	return result
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
	Path             string           `json:"path"`
	SHA256           string           `json:"sha256"`
	Generator        string           `json:"generator"`
	OutputKind       ArtifactKind     `json:"output_kind"`
	InputRecordIDs   []string         `json:"input_record_ids"`
	Sources          []string         `json:"sources"`
	CleanupOwnership CleanupOwnership `json:"cleanup_ownership"`
}

func newArtifact(filePath, fileDigest string, input ArtifactInput) (Artifact, error) {
	inputs, err := normalizeArtifactValues("input record IDs", input.InputRecordIDs, false)
	if err != nil {
		return Artifact{}, err
	}
	sources, err := normalizeArtifactValues("sources", input.Sources, false)
	if err != nil {
		return Artifact{}, err
	}
	artifact := Artifact{
		path:             filePath,
		sha256:           fileDigest,
		generator:        input.Generator,
		kind:             input.Kind,
		inputRecordIDs:   inputs,
		sources:          sources,
		cleanupOwnership: CleanupOwnershipCLI,
		prepared:         true,
	}
	if err := validateArtifact(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func restoreArtifact(record manifestRecord) (Artifact, error) {
	artifact := Artifact{
		path:             record.Path,
		sha256:           record.SHA256,
		generator:        record.Generator,
		kind:             record.OutputKind,
		inputRecordIDs:   append([]string(nil), record.InputRecordIDs...),
		sources:          append([]string(nil), record.Sources...),
		cleanupOwnership: record.CleanupOwnership,
		prepared:         true,
	}
	if err := validateArtifact(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func validateArtifact(artifact Artifact) error {
	if !artifact.prepared || !validArtifactPath(artifact) || !validDigest(artifact.sha256) {
		return errors.New("path or content digest is invalid")
	}
	if !validArtifactGenerator(artifact.generator) {
		return errors.New("generator identity is invalid")
	}
	if !validArtifactKind(artifact.kind) {
		return errors.New("output kind is invalid")
	}
	if artifact.path == ApplicationManifestPath && artifact.kind != ArtifactKindApplicationManifest {
		return errors.New("application manifest output kind is invalid")
	}
	if artifact.cleanupOwnership != CleanupOwnershipCLI {
		return errors.New("cleanup ownership must be cli-owned")
	}
	if !validArtifactValues(artifact.inputRecordIDs, false) {
		return errors.New("input record IDs are not canonical")
	}
	if !validArtifactValues(artifact.sources, false) {
		return errors.New("sources are not canonical")
	}
	return nil
}

func ownershipManifestArtifact(data []byte, artifacts []Artifact) Artifact {
	artifactSet := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifactSet = append(artifactSet, artifact.path+"\x00"+artifact.sha256)
	}
	sort.Strings(artifactSet)
	artifactSetDigest := digest([]byte(strings.Join(artifactSet, "\n")))
	return Artifact{
		path:      ManifestPath,
		sha256:    digest(data),
		generator: ownershipManifestGenerator,
		kind:      ArtifactKindOwnershipManifest,
		inputRecordIDs: []string{
			"artifact-set:" + artifactSetDigest,
			"ownership-schema:3",
		},
		sources:          []string{"generated output model"},
		cleanupOwnership: CleanupOwnershipCLI,
		prepared:         true,
	}
}

func validArtifactPath(artifact Artifact) bool {
	if artifact.path != ManifestPath {
		return validManagedPath(artifact.path)
	}
	return artifact.kind == ArtifactKindOwnershipManifest && artifact.generator == ownershipManifestGenerator
}

func cloneArtifact(value Artifact) Artifact {
	value.inputRecordIDs = append([]string(nil), value.inputRecordIDs...)
	value.sources = append([]string(nil), value.sources...)
	return value
}

func normalizeArtifactValues(name string, values []string, allowEmpty bool) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if !validArtifactValues(result, allowEmpty) {
		return nil, fmt.Errorf("%s must be bounded, nonempty, unique, and canonically ordered", name)
	}
	return result, nil
}

func validArtifactValues(values []string, allowEmpty bool) bool {
	if values == nil || !allowEmpty && len(values) == 0 || len(values) > maximumArtifactValues {
		return false
	}
	for index, value := range values {
		if !validArtifactValue(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func validArtifactValue(value string) bool {
	return value != "" && len(value) <= maximumArtifactValueBytes && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validArtifactGenerator(value string) bool {
	if !validArtifactValue(value) || !strings.HasPrefix(value, "plystra.") {
		return false
	}
	name, version, found := strings.Cut(value, "/v")
	if !found || name == "plystra." || version == "" || version[0] == '0' {
		return false
	}
	for _, character := range strings.TrimPrefix(name, "plystra.") {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '.' {
			return false
		}
	}
	for _, character := range version {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func validArtifactKind(value ArtifactKind) bool {
	switch value {
	case ArtifactKindGoSource,
		ArtifactKindProtobufSource,
		ArtifactKindProtobufDescriptor,
		ArtifactKindJavaScriptSource,
		ArtifactKindJavaScriptPackage,
		ArtifactKindDocumentation,
		ArtifactKindCompatibilityBaseline,
		ArtifactKindWireMap,
		ArtifactKindApplicationManifest,
		ArtifactKindOwnershipManifest:
		return true
	default:
		return false
	}
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
