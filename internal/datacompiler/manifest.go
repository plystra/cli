// Package datacompiler validates the independent Data compiler distribution
// manifest without importing github.com/plystra/data.
package datacompiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	DistributionSchema   = "plystra.data-compiler-distribution/v1"
	ModulePath           = "github.com/plystra/data"
	CommandImportPath    = "github.com/plystra/data/cmd/plystra-data-compiler"
	AnalyzeSchema        = "plystra.data-analyze/v1"
	EmitSchema           = "plystra.data-emit/v1"
	DeclarationLanguage  = "plystra.data-declaration/v1"
	MaxRoots             = 1024
	MaxNodes             = 65536
	MaxImports           = 1024
	MaxNesting           = 64
	MaxSymbolBytes       = 128
	MaxDiagnostics       = 256
	MaxDiagnosticBytes   = 256 * 1024
	MaxFrameBytes        = 16 * 1024 * 1024
	MaxArtifacts         = 4096
	MaxArtifactBytes     = 4 * 1024 * 1024
	MaxArtifactPathBytes = 512
	maximumJSONDepth     = 16
	distributionManifest = "plystra-data-compiler.json"
)

// ErrInvalidManifest identifies an invalid or unsafe Data compiler
// distribution manifest. Its text is deliberately independent of input.
var ErrInvalidManifest = errors.New("invalid Data compiler distribution manifest")

// ErrManifestUnavailable identifies a selected compiler source without a
// readable regular distribution manifest.
var ErrManifestUnavailable = errors.New("Data compiler distribution manifest is unavailable")

// Bounds contains the advertised limits of the Data compiler protocol.
type Bounds struct {
	MaxRoots             int `json:"max_roots"`
	MaxNodes             int `json:"max_nodes"`
	MaxImports           int `json:"max_imports"`
	MaxNesting           int `json:"max_nesting"`
	MaxSymbolBytes       int `json:"max_symbol_bytes"`
	MaxDiagnostics       int `json:"max_diagnostics"`
	MaxDiagnosticBytes   int `json:"max_diagnostic_bytes"`
	MaxFrameBytes        int `json:"max_frame_bytes"`
	MaxArtifacts         int `json:"max_artifacts"`
	MaxArtifactBytes     int `json:"max_artifact_bytes"`
	MaxArtifactPathBytes int `json:"max_artifact_path_bytes"`
}

// Manifest is the validated identity and bounds of the independent Data
// compiler distribution.
type Manifest struct {
	Schema              string `json:"schema"`
	ModulePath          string `json:"module_path"`
	CommandImportPath   string `json:"command_import_path"`
	AnalyzeProtocol     string `json:"analyze_protocol"`
	EmitProtocol        string `json:"emit_protocol"`
	DeclarationLanguage string `json:"declaration_language"`
	Bounds              Bounds `json:"limits"`
}

// Parse validates one exact distribution manifest and returns its raw-byte
// SHA-256 digest. The digest is intentionally over data as received, rather
// than over a re-encoded or normalized JSON value.
func Parse(data []byte) (Manifest, string, error) {
	if len(data) == 0 || !hasUniqueJSONKeys(data) {
		return Manifest{}, "", ErrInvalidManifest
	}

	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, "", ErrInvalidManifest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Manifest{}, "", ErrInvalidManifest
	}
	if !validManifest(manifest) {
		return Manifest{}, "", ErrInvalidManifest
	}

	sum := sha256.Sum256(data)
	return manifest, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Load reads the exact distribution manifest from one selected module source
// root. It does not follow a manifest symlink and never includes the host path
// or source bytes in an error.
func Load(root string) (Manifest, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return Manifest{}, "", ErrManifestUnavailable
	}
	path := filepath.Join(root, distributionManifest)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > MaxFrameBytes {
		return Manifest{}, "", ErrManifestUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, "", ErrManifestUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxFrameBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxFrameBytes {
		return Manifest{}, "", ErrManifestUnavailable
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || after.Size() != before.Size() {
		return Manifest{}, "", ErrManifestUnavailable
	}
	manifest, digest, err := Parse(data)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("%w: %w", ErrManifestUnavailable, err)
	}
	return manifest, digest, nil
}

func validManifest(manifest Manifest) bool {
	return manifest.Schema == DistributionSchema &&
		manifest.ModulePath == ModulePath &&
		manifest.CommandImportPath == CommandImportPath &&
		manifest.AnalyzeProtocol == AnalyzeSchema &&
		manifest.EmitProtocol == EmitSchema &&
		manifest.DeclarationLanguage == DeclarationLanguage &&
		validBounds(manifest.Bounds)
}

func validBounds(bounds Bounds) bool {
	return validBound(bounds.MaxRoots, MaxRoots) &&
		validBound(bounds.MaxNodes, MaxNodes) &&
		validBound(bounds.MaxImports, MaxImports) &&
		validBound(bounds.MaxNesting, MaxNesting) &&
		validBound(bounds.MaxSymbolBytes, MaxSymbolBytes) &&
		validBound(bounds.MaxDiagnostics, MaxDiagnostics) &&
		validBound(bounds.MaxDiagnosticBytes, MaxDiagnosticBytes) &&
		validBound(bounds.MaxFrameBytes, MaxFrameBytes) &&
		validBound(bounds.MaxArtifacts, MaxArtifacts) &&
		validBound(bounds.MaxArtifactBytes, MaxArtifactBytes) &&
		validBound(bounds.MaxArtifactPathBytes, MaxArtifactPathBytes)
}

func validBound(value, maximum int) bool { return value > 0 && value <= maximum }

// hasUniqueJSONKeys performs the duplicate-key check that encoding/json does
// not provide. It walks all values so a future nested field cannot silently
// acquire last-key-wins behavior.
func hasUniqueJSONKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !scanJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > maximumJSONDepth {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return false
				}
				key, ok := keyToken.(string)
				if !ok {
					return false
				}
				if _, duplicate := seen[key]; duplicate {
					return false
				}
				seen[key] = struct{}{}
				if !scanJSONValue(decoder, depth+1) {
					return false
				}
			}
			closingToken, err := decoder.Token()
			if err != nil {
				return false
			}
			closing, ok := closingToken.(json.Delim)
			return ok && closing == '}'
		case '[':
			for decoder.More() {
				if !scanJSONValue(decoder, depth+1) {
					return false
				}
			}
			closingToken, err := decoder.Token()
			if err != nil {
				return false
			}
			closing, ok := closingToken.(json.Delim)
			return ok && closing == ']'
		default:
			return false
		}
	default:
		return true
	}
}
