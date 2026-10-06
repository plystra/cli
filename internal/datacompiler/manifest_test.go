package datacompiler_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/plystra/cli/internal/datacompiler"
)

const validManifestJSON = `{
  "schema": "plystra.data-compiler-distribution/v1",
  "module_path": "github.com/plystra/data",
  "command_import_path": "github.com/plystra/data/cmd/plystra-data-compiler",
  "analyze_protocol": "plystra.data-analyze/v1",
  "emit_protocol": "plystra.data-emit/v1",
  "declaration_language": "plystra.data-declaration/v1",
  "limits": {
    "max_roots": 1024,
    "max_nodes": 65536,
    "max_imports": 1024,
    "max_nesting": 64,
    "max_symbol_bytes": 128,
    "max_diagnostics": 256,
    "max_diagnostic_bytes": 262144,
    "max_frame_bytes": 16777216,
    "max_artifacts": 4096,
    "max_artifact_bytes": 4194304,
    "max_artifact_path_bytes": 512
  }
}`

func TestParseValidManifestAndRawDigest(t *testing.T) {
	t.Parallel()

	data := []byte(validManifestJSON)
	manifest, digest, err := datacompiler.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if manifest.Schema != datacompiler.DistributionSchema || manifest.ModulePath != datacompiler.ModulePath || manifest.CommandImportPath != datacompiler.CommandImportPath || manifest.AnalyzeProtocol != datacompiler.AnalyzeSchema || manifest.EmitProtocol != datacompiler.EmitSchema || manifest.DeclarationLanguage != datacompiler.DeclarationLanguage {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if manifest.Bounds != (datacompiler.Bounds{
		MaxRoots: 1024, MaxNodes: 65536, MaxImports: 1024, MaxNesting: 64,
		MaxSymbolBytes: 128, MaxDiagnostics: 256, MaxDiagnosticBytes: 262144,
		MaxFrameBytes: 16777216, MaxArtifacts: 4096, MaxArtifactBytes: 4194304,
		MaxArtifactPathBytes: 512,
	}) {
		t.Fatalf("manifest bounds = %#v", manifest.Bounds)
	}
	sum := sha256.Sum256(data)
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if digest != wantDigest {
		t.Fatalf("digest = %q, want %q", digest, wantDigest)
	}
	padded := append([]byte("\n"), data...)
	_, paddedDigest, err := datacompiler.Parse(padded)
	if err != nil {
		t.Fatalf("Parse(padded) error = %v", err)
	}
	if paddedDigest == digest {
		t.Fatal("digest ignored raw leading whitespace")
	}
	paddedSum := sha256.Sum256(padded)
	if paddedDigest != "sha256:"+hex.EncodeToString(paddedSum[:]) {
		t.Fatalf("padded digest = %q", paddedDigest)
	}
}

func TestParseRejectsInvalidManifestWithoutInputLeak(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown field":             strings.Replace(validManifestJSON, `"limits": {`, `"private-secret-marker": "private-value", "limits": {`, 1),
		"duplicate top-level field": strings.Replace(validManifestJSON, `"module_path": "github.com/plystra/data",`, `"module_path": "github.com/plystra/data", "module_path": "private-value",`, 1),
		"duplicate nested field":    strings.Replace(validManifestJSON, `"max_roots": 1024,`, `"max_roots": 1024, "max_roots": 1024,`, 1),
		"missing identity":          strings.Replace(validManifestJSON, "  \"emit_protocol\": \"plystra.data-emit/v1\",\n", "", 1),
		"wrong module path":         strings.Replace(validManifestJSON, `"github.com/plystra/data"`, `"private-module-path"`, 1),
		"wrong command path":        strings.Replace(validManifestJSON, `"github.com/plystra/data/cmd/plystra-data-compiler"`, `"private-command-path"`, 1),
		"wrong analyze schema":      strings.Replace(validManifestJSON, `"plystra.data-analyze/v1"`, `"private-analyze-schema"`, 1),
		"wrong emit schema":         strings.Replace(validManifestJSON, `"plystra.data-emit/v1"`, `"private-emit-schema"`, 1),
		"wrong declaration schema":  strings.Replace(validManifestJSON, `"plystra.data-declaration/v1"`, `"private-declaration-schema"`, 1),
		"zero bound":                strings.Replace(validManifestJSON, `"max_roots": 1024`, `"max_roots": 0`, 1),
		"negative bound":            strings.Replace(validManifestJSON, `"max_nodes": 65536`, `"max_nodes": -1`, 1),
		"bound over safety limit":   strings.Replace(validManifestJSON, `"max_artifacts": 4096`, `"max_artifacts": 4097`, 1),
		"trailing JSON":             validManifestJSON + `{}`,
	}
	for name, data := range tests {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			manifest, digest, err := datacompiler.Parse([]byte(data))
			if !errors.Is(err, datacompiler.ErrInvalidManifest) || manifest != (datacompiler.Manifest{}) || digest != "" {
				t.Fatalf("Parse() = %#v, %q, %v", manifest, digest, err)
			}
			if strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "private_secret") {
				t.Fatalf("error exposed input content: %v", err)
			}
		})
	}
}

func TestParseAcceptsPositiveBoundsWithinSafetyLimits(t *testing.T) {
	t.Parallel()

	data := validManifestJSON
	for _, replacement := range []struct{ old, new string }{
		{`"max_roots": 1024`, `"max_roots": 1`},
		{`"max_nodes": 65536`, `"max_nodes": 1`},
		{`"max_imports": 1024`, `"max_imports": 1`},
		{`"max_nesting": 64`, `"max_nesting": 1`},
		{`"max_symbol_bytes": 128`, `"max_symbol_bytes": 1`},
		{`"max_diagnostics": 256`, `"max_diagnostics": 1`},
		{`"max_diagnostic_bytes": 262144`, `"max_diagnostic_bytes": 1`},
		{`"max_frame_bytes": 16777216`, `"max_frame_bytes": 1`},
		{`"max_artifacts": 4096`, `"max_artifacts": 1`},
		{`"max_artifact_bytes": 4194304`, `"max_artifact_bytes": 1`},
		{`"max_artifact_path_bytes": 512`, `"max_artifact_path_bytes": 1`},
	} {
		data = strings.Replace(data, replacement.old, replacement.new, 1)
	}
	if _, _, err := datacompiler.Parse([]byte(data)); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseConcurrent(t *testing.T) {
	t.Parallel()

	data := []byte(validManifestJSON)
	const workers = 8
	const iterations = 32
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				manifest, digest, err := datacompiler.Parse(data)
				if err != nil || manifest.ModulePath != datacompiler.ModulePath || !strings.HasPrefix(digest, "sha256:") {
					t.Errorf("Parse() = %#v, %q, %v", manifest, digest, err)
					return
				}
			}
		}()
	}
	group.Wait()
}
