package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/modulepath"
)

// GenerateSchemaV1 identifies the payload returned by plystra generate.
const GenerateSchemaV1 = "plystra.generate/v1"

// ErrGeneratePayload reports an invalid generation payload.
var ErrGeneratePayload = errors.New("build plystra.generate result")

// GeneratePayloadInput is the construction-only input for one generation
// result. The compiler identity is public-safe and contains no executable or
// cache paths.
type GeneratePayloadInput struct {
	ModulePath   string
	Mode         string
	DataCompiler *GenerateDataCompilerInput
}

// GenerateDataCompilerInput is the public-safe identity of the selected Data
// compiler.
type GenerateDataCompilerInput struct {
	ModulePath     string
	ModuleVersion  string
	ModuleChecksum string
	ManifestDigest string
	BinaryDigest   string
	GoToolchain    string
	GOOS           string
	GOARCH         string
	CacheHit       bool
	Offline        bool
}

// GeneratePayload is one immutable plystra.generate/v1 payload.
type GeneratePayload struct {
	modulePath    string
	mode          string
	dataCompiler  *GenerateDataCompiler
	canonicalJSON []byte
	prepared      bool
}

type generatePayloadDocument struct {
	Schema       string                `json:"schema"`
	ModulePath   string                `json:"module_path"`
	Mode         string                `json:"mode"`
	DataCompiler *generateDataCompiler `json:"data_compiler"`
}

type generateDataCompiler struct {
	ModulePath     string `json:"module_path"`
	ModuleVersion  string `json:"module_version"`
	ModuleChecksum string `json:"module_checksum"`
	ManifestDigest string `json:"manifest_digest"`
	BinaryDigest   string `json:"binary_digest"`
	GoToolchain    string `json:"go_toolchain"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	CacheHit       bool   `json:"cache_hit"`
	Offline        bool   `json:"offline"`
}

// NewGeneratePayload validates and constructs one generation payload.
func NewGeneratePayload(input GeneratePayloadInput) (GeneratePayload, error) {
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return GeneratePayload{}, fmt.Errorf("%w: module path: %v", ErrGeneratePayload, err)
	}
	if input.Mode != "check" && input.Mode != "install" {
		return GeneratePayload{}, fmt.Errorf("%w: mode %q is not supported", ErrGeneratePayload, input.Mode)
	}
	var compiler *GenerateDataCompiler
	if input.DataCompiler != nil {
		if err := validateGenerateDataCompiler(*input.DataCompiler); err != nil {
			return GeneratePayload{}, fmt.Errorf("%w: Data compiler: %v", ErrGeneratePayload, err)
		}
		value := generateDataCompilerFrom(*input.DataCompiler)
		compiler = &value
	}
	payload := GeneratePayload{modulePath: input.ModulePath, mode: input.Mode, dataCompiler: compiler, prepared: true}
	canonical, err := json.Marshal(payload.document())
	if err != nil {
		return GeneratePayload{}, fmt.Errorf("%w: encode: %v", ErrGeneratePayload, err)
	}
	payload.canonicalJSON = canonical
	return payload, nil
}

// Valid reports whether NewGeneratePayload produced this payload.
func (p GeneratePayload) Valid() bool {
	if !p.prepared {
		return false
	}
	input := GeneratePayloadInput{ModulePath: p.modulePath, Mode: p.mode}
	if p.dataCompiler != nil {
		value := p.dataCompiler.input()
		input.DataCompiler = &value
	}
	rebuilt, err := NewGeneratePayload(input)
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

// Schema returns the immutable payload schema identity.
func (GeneratePayload) Schema() string { return GenerateSchemaV1 }

// ModulePath returns the generated Project module identity.
func (p GeneratePayload) ModulePath() string { return p.modulePath }

// Mode returns check or install.
func (p GeneratePayload) Mode() string { return p.mode }

// DataCompiler returns the selected public-safe compiler identity when one
// was acquired.
func (p GeneratePayload) DataCompiler() (GenerateDataCompiler, bool) {
	if p.dataCompiler == nil {
		return GenerateDataCompiler{}, false
	}
	return *p.dataCompiler, true
}

// CanonicalJSON returns a defensive copy of the payload document.
func (p GeneratePayload) CanonicalJSON() []byte { return append([]byte(nil), p.canonicalJSON...) }

func (GeneratePayload) commandPayload() {}

func (p GeneratePayload) document() generatePayloadDocument {
	var compiler *generateDataCompiler
	if p.dataCompiler != nil {
		copy := generateDataCompilerDocumentFrom(p.dataCompiler.input())
		compiler = &copy
	}
	return generatePayloadDocument{Schema: GenerateSchemaV1, ModulePath: p.modulePath, Mode: p.mode, DataCompiler: compiler}
}

// GenerateDataCompiler is one immutable public-safe compiler identity.
type GenerateDataCompiler struct {
	modulePath     string
	moduleVersion  string
	moduleChecksum string
	manifestDigest string
	binaryDigest   string
	goToolchain    string
	goos           string
	goarch         string
	cacheHit       bool
	offline        bool
}

func (c GenerateDataCompiler) input() GenerateDataCompilerInput {
	return GenerateDataCompilerInput{
		ModulePath: c.modulePath, ModuleVersion: c.moduleVersion, ModuleChecksum: c.moduleChecksum,
		ManifestDigest: c.manifestDigest, BinaryDigest: c.binaryDigest, GoToolchain: c.goToolchain,
		GOOS: c.goos, GOARCH: c.goarch, CacheHit: c.cacheHit, Offline: c.offline,
	}
}

func (c GenerateDataCompiler) ModulePath() string     { return c.modulePath }
func (c GenerateDataCompiler) ModuleVersion() string  { return c.moduleVersion }
func (c GenerateDataCompiler) ModuleChecksum() string { return c.moduleChecksum }
func (c GenerateDataCompiler) ManifestDigest() string { return c.manifestDigest }
func (c GenerateDataCompiler) BinaryDigest() string   { return c.binaryDigest }
func (c GenerateDataCompiler) GoToolchain() string    { return c.goToolchain }
func (c GenerateDataCompiler) GOOS() string           { return c.goos }
func (c GenerateDataCompiler) GOARCH() string         { return c.goarch }
func (c GenerateDataCompiler) CacheHit() bool         { return c.cacheHit }
func (c GenerateDataCompiler) Offline() bool          { return c.offline }

func generateDataCompilerFrom(input GenerateDataCompilerInput) GenerateDataCompiler {
	return GenerateDataCompiler{
		modulePath: input.ModulePath, moduleVersion: input.ModuleVersion, moduleChecksum: input.ModuleChecksum,
		manifestDigest: input.ManifestDigest, binaryDigest: input.BinaryDigest, goToolchain: input.GoToolchain,
		goos: input.GOOS, goarch: input.GOARCH, cacheHit: input.CacheHit, offline: input.Offline,
	}
}

func generateDataCompilerDocumentFrom(input GenerateDataCompilerInput) generateDataCompiler {
	return generateDataCompiler{
		ModulePath: input.ModulePath, ModuleVersion: input.ModuleVersion, ModuleChecksum: input.ModuleChecksum,
		ManifestDigest: input.ManifestDigest, BinaryDigest: input.BinaryDigest, GoToolchain: input.GoToolchain,
		GOOS: input.GOOS, GOARCH: input.GOARCH, CacheHit: input.CacheHit, Offline: input.Offline,
	}
}

func validateGenerateDataCompiler(input GenerateDataCompilerInput) error {
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return fmt.Errorf("module path: %v", err)
	}
	for _, value := range []struct {
		name  string
		value string
	}{
		{name: "module version", value: input.ModuleVersion},
		{name: "module checksum", value: input.ModuleChecksum},
		{name: "manifest digest", value: input.ManifestDigest},
		{name: "binary digest", value: input.BinaryDigest},
		{name: "Go toolchain", value: input.GoToolchain},
		{name: "GOOS", value: input.GOOS},
		{name: "GOARCH", value: input.GOARCH},
	} {
		if !validSafeText(value.value, maximumIdentityLength) {
			return fmt.Errorf("%s is invalid", value.name)
		}
	}
	return nil
}
