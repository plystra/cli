package datacompiler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/module"
)

var (
	// ErrBuild identifies compiler source, identity, cache, or build failures.
	ErrBuild = errors.New("build Data compiler")
	// ErrInvalidBuildOptions identifies incomplete compiler acquisition inputs.
	ErrInvalidBuildOptions = errors.New("invalid Data compiler build options")
)

// BuildOptions identifies one immutable compiler source and its CLI-owned
// cache. ModuleChecksum must be the verified Go checksum for ModuleVersion.
type BuildOptions struct {
	ModuleRoot     string
	ModuleVersion  string
	ModuleChecksum string
	CacheRoot      string
	GoCommand      string
	Environment    []string
}

// Artifact is one verified compiler executable and its immutable provenance.
type Artifact struct {
	Path           string
	ModulePath     string
	ModuleVersion  string
	ModuleChecksum string
	ManifestDigest string
	BinaryDigest   string
	GoToolchain    string
	GOOS           string
	GOARCH         string
	CacheHit       bool
}

type cacheRecord struct {
	Schema         string `json:"schema"`
	Key            string `json:"key"`
	ModulePath     string `json:"module_path"`
	ModuleVersion  string `json:"module_version"`
	ModuleChecksum string `json:"module_checksum"`
	ManifestDigest string `json:"manifest_digest"`
	BinaryDigest   string `json:"binary_digest"`
	GoToolchain    string `json:"go_toolchain"`
	GOOS           string `json:"goos"`
	GOARCH         string `json:"goarch"`
	Bytes          int64  `json:"bytes"`
}

const cacheRecordSchema = "plystra.data-compiler-cache/v1"

// Build validates the selected module distribution and builds its compiler
// into a deterministic private cache entry. The compiler receives no input
// from the Project and is never started by this function.
func Build(ctx context.Context, options BuildOptions) (Artifact, error) {
	if err := validateBuildOptions(options); err != nil {
		return Artifact{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	manifest, manifestDigest, err := Load(options.ModuleRoot)
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	goos, goarch := target(options.Environment)
	goToolchain := runtime.Version()
	key := cacheKey(manifestDigest, options.ModuleVersion, options.ModuleChecksum, goToolchain, goos, goarch)
	if artifact, ok := readCache(options.CacheRoot, key, manifestDigest, options, goToolchain, goos, goarch); ok {
		artifact.CacheHit = true
		return artifact, nil
	}
	if err := os.MkdirAll(options.CacheRoot, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("%w: create private cache: %v", ErrBuild, err)
	}
	goCache := filepath.Join(options.CacheRoot, "go-cache")
	goTemp := filepath.Join(options.CacheRoot, "go-tmp")
	if err := os.MkdirAll(goCache, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("%w: create Go build cache: %v", ErrBuild, err)
	}
	if err := os.MkdirAll(goTemp, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("%w: create Go temporary directory: %v", ErrBuild, err)
	}
	temporary, err := os.CreateTemp(options.CacheRoot, ".data-compiler-*")
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: create build output: %v", ErrBuild, err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return Artifact{}, fmt.Errorf("%w: prepare build output: %v", ErrBuild, err)
	}
	defer os.Remove(temporaryPath)
	environment := buildEnvironment(options.Environment)
	environment = replaceEnvironment(environment, "GOCACHE", goCache)
	environment = replaceEnvironment(environment, "GOTMPDIR", goTemp)
	if err := gocommand.Run(ctx, gocommand.Options{
		Command: options.GoCommand, Directory: options.ModuleRoot, Environment: environment,
	}, "build", "-mod=readonly", "-o", temporaryPath, manifest.CommandImportPath); err != nil {
		return Artifact{}, fmt.Errorf("%w: %v", ErrBuild, err)
	}
	binaryDigest, size, err := fileDigest(temporaryPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("%w: verify compiler output: %v", ErrBuild, err)
	}
	path := filepath.Join(options.CacheRoot, "data-compiler-"+key+executableSuffix(goos))
	recordPath := path + ".json"
	if err := replaceFile(temporaryPath, path); err != nil {
		return Artifact{}, fmt.Errorf("%w: install compiler output: %v", ErrBuild, err)
	}
	record := cacheRecord{
		Schema: cacheRecordSchema, Key: key, ModulePath: ModulePath,
		ModuleVersion: options.ModuleVersion, ModuleChecksum: options.ModuleChecksum,
		ManifestDigest: manifestDigest, BinaryDigest: binaryDigest,
		GoToolchain: goToolchain, GOOS: goos, GOARCH: goarch, Bytes: size,
	}
	if err := writeCacheRecord(recordPath, record); err != nil {
		_ = os.Remove(path)
		return Artifact{}, fmt.Errorf("%w: write compiler cache record: %v", ErrBuild, err)
	}
	return Artifact{
		Path: path, ModulePath: ModulePath, ModuleVersion: options.ModuleVersion,
		ModuleChecksum: options.ModuleChecksum, ManifestDigest: manifestDigest,
		BinaryDigest: binaryDigest, GoToolchain: goToolchain, GOOS: goos,
		GOARCH: goarch,
	}, nil
}

func validateBuildOptions(options BuildOptions) error {
	if options.ModuleRoot == "" || !filepath.IsAbs(options.ModuleRoot) || options.CacheRoot == "" || !filepath.IsAbs(options.CacheRoot) {
		return ErrInvalidBuildOptions
	}
	if module.Check(ModulePath, options.ModuleVersion) != nil || !validModuleChecksum(options.ModuleChecksum) {
		return ErrInvalidBuildOptions
	}
	return nil
}

func validModuleChecksum(value string) bool {
	if !strings.HasPrefix(value, "h1:") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "h1:"))
	return err == nil && len(decoded) == sha256.Size
}

func target(environment []string) (string, string) {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch key {
		case "GOOS":
			if value != "" {
				goos = value
			}
		case "GOARCH":
			if value != "" {
				goarch = value
			}
		}
	}
	return goos, goarch
}

func buildEnvironment(environment []string) []string {
	result := append([]string(nil), os.Environ()...)
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		result = replaceEnvironment(result, name, strings.TrimPrefix(entry, name+"="))
	}
	result = replaceEnvironment(result, "GOWORK", "off")
	result = replaceEnvironment(result, "GOFLAGS", "-mod=readonly")
	return result
}

func replaceEnvironment(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, key) {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func cacheKey(manifestDigest, version, checksum, toolchain, goos, goarch string) string {
	payload, _ := json.Marshal(struct {
		ModulePath, ModuleVersion, ModuleChecksum, ManifestDigest, GoToolchain, GOOS, GOARCH string
	}{ModulePath, version, checksum, manifestDigest, toolchain, goos, goarch})
	sum := sha256.Sum256(append([]byte("plystra.data-compiler-cache-key/v1\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func readCache(root, key, manifestDigest string, options BuildOptions, toolchain, goos, goarch string) (Artifact, bool) {
	path := filepath.Join(root, "data-compiler-"+key+executableSuffix(goos))
	recordPath := path + ".json"
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return Artifact{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var record cacheRecord
	var extra any
	if decoder.Decode(&record) != nil || decoder.Decode(&extra) != io.EOF || record.Schema != cacheRecordSchema || record.Key != key || record.ModulePath != ModulePath || record.ModuleVersion != options.ModuleVersion || record.ModuleChecksum != options.ModuleChecksum || record.ManifestDigest != manifestDigest || record.GoToolchain != toolchain || record.GOOS != goos || record.GOARCH != goarch || record.Bytes <= 0 || !validDigest(record.BinaryDigest) {
		return Artifact{}, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.Bytes {
		return Artifact{}, false
	}
	digest, size, err := fileDigest(path)
	if err != nil || size != record.Bytes || digest != record.BinaryDigest {
		return Artifact{}, false
	}
	return Artifact{Path: path, ModulePath: ModulePath, ModuleVersion: options.ModuleVersion, ModuleChecksum: options.ModuleChecksum, ManifestDigest: manifestDigest, BinaryDigest: digest, GoToolchain: toolchain, GOOS: goos, GOARCH: goarch}, true
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func fileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", size, err
	}
	if size <= 0 {
		return "", size, errors.New("compiler output is empty")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func writeCacheRecord(path string, record cacheRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".data-compiler-record-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}

func executableSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}
