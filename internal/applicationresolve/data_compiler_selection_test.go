package applicationresolve_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/testkernel"
	"golang.org/x/mod/module"
)

func TestDataCompilerSelectionUsesSelectedVerifiedModule(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	manifest := dataCompilerManifest(t)
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.1.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{"plystra-data-compiler.json": manifest})
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.2.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{"plystra-data-compiler.json": manifest})
	writeCompilerProxyModule(t, proxy, "example.com/bridge", "v0.1.0", "module example.com/bridge\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n", nil)
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire (\n github.com/plystra/data v0.1.0\n example.com/bridge v0.1.0\n)\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	options := applicationresolve.Options{Start: project, Environment: environment, Offline: true}
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
	if err != nil {
		t.Fatalf("DiscoverSelectionInputs: %v", err)
	}
	selected, err := inputs.DataCompilerSelection()
	if err != nil {
		t.Fatalf("DataCompilerSelection: %v", err)
	}
	sum := sha256.Sum256(manifest)
	if selected.ModulePath != datacompiler.ModulePath || selected.ModuleVersion != "v0.2.0" || !strings.HasPrefix(selected.ModuleChecksum, "h1:") || selected.ManifestDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("selected compiler = %#v", selected)
	}
	if selected.Root == "" || !filepath.IsAbs(selected.Root) {
		t.Fatalf("selected root = %q", selected.Root)
	}
	_, err = applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) || errors.Is(err, datacompiler.ErrSelection) {
		t.Fatalf("active selected compiler should remain uninstalled: %v", err)
	}
}

func TestResolveAcquiresSelectedCompilerBeforeAnalyzeBoundary(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	manifest := dataCompilerManifest(t)
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.2.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        manifest,
		"cmd/plystra-data-compiler/main.go": []byte("package main\nfunc main() {}\n"),
	})
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	cache := filepath.Join(root, "compiler-cache")
	options := applicationresolve.Options{Start: project, Environment: environment, DataCompilerCacheRoot: cache, CompileTimeout: 30 * time.Second}
	resolved, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationresolve.ErrDataCompilerAnalysisUnavailable) || !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) {
		t.Fatalf("Resolve error = %v", err)
	}
	var unavailable *applicationresolve.DataCompilerUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Resolve error omitted DataCompilerUnavailableError: %v", err)
	}
	acquisition, ok := unavailable.Acquisition()
	if !ok || !acquisition.Valid() || acquisition.CacheHit() || acquisition.Offline() || acquisition.ModuleVersion() != "v0.2.0" || acquisition.ModulePath() != datacompiler.ModulePath {
		t.Fatalf("cold acquisition = %#v, ok=%t", acquisition, ok)
	}
	if retained, ok := resolved.DataCompilerAcquisition(); !ok || retained != acquisition {
		t.Fatalf("partial resolution acquisition = %#v, ok=%t", retained, ok)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) == 0 {
		t.Fatalf("compiler cache entries = %v, error = %v", entries, err)
	}
	options.Offline = true
	resolved, err = applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationresolve.ErrDataCompilerAnalysisUnavailable) {
		t.Fatalf("offline warm Resolve error = %v", err)
	}
	if !errors.As(err, &unavailable) {
		t.Fatalf("offline Resolve error omitted DataCompilerUnavailableError: %v", err)
	}
	acquisition, ok = unavailable.Acquisition()
	if !ok || !acquisition.Valid() || !acquisition.CacheHit() || !acquisition.Offline() {
		t.Fatalf("warm offline acquisition = %#v, ok=%t", acquisition, ok)
	}
	if retained, ok := resolved.DataCompilerAcquisition(); !ok || retained != acquisition {
		t.Fatalf("offline partial resolution acquisition = %#v, ok=%t", retained, ok)
	}
}

func TestGenerateForwardsOfflineCompilerAvailability(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.2.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        dataCompilerManifest(t),
		"cmd/plystra-data-compiler/main.go": []byte("package main\nfunc main() {}\n"),
	})
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	cache := filepath.Join(root, "compiler-cache")
	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Environment: environment, Offline: true, DataCompilerCacheRoot: cache,
	})
	if !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) || !errors.Is(err, datacompiler.ErrOfflineUnavailable) {
		t.Fatalf("offline generation error = %v", err)
	}
	var unavailable *applicationresolve.DataCompilerUnavailableError
	if errors.As(err, &unavailable) {
		if _, ok := unavailable.Acquisition(); ok {
			t.Fatal("cold offline failure reported compiler acquisition")
		}
	}
	if _, statErr := os.Stat(cache); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("offline generation materialized compiler cache: %v", statErr)
	}
	if _, ok := generated.DataCompilerAcquisition(); ok {
		t.Fatal("cold offline generation returned a compiler acquisition")
	}
}

func TestResolveInvokesAnalyzeWithResourceContractSourceClosure(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	manifest := dataCompilerManifest(t)
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.3.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        manifest,
		"declaration/declaration.go":        []byte("package declaration\n\ntype NoAccess struct{}\ntype Member[Access any] struct { Namespace string; Access Access }\n"),
		"plystra.yaml":                      []byte("{}\n"),
		"database/resource.go":              []byte("package database\n\n//plystra:resource data.database/v1\ntype Resource interface { Ping() error }\n"),
		"postgres/postgres.go":              []byte("package postgres\n\nimport \"github.com/plystra/data/database\"\n\ntype provider struct{}\nvar _ database.Resource = (*provider)(nil)\nfunc (*provider) Ping() error { return nil }\n\n//plystra:implements-resource data.database/v1\nfunc New() (*provider, error) { return &provider{}, nil }\n"),
		"cmd/plystra-data-compiler/main.go": []byte(snapshotCheckingCompilerSource),
	})
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.3.0\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.primary: {use: github.com/plystra/data/postgres.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: database.records}}}\n")
	writeFile(t, filepath.Join(project, "resource", "resource.go"), "package resource\n\n//plystra:resource example.records/v1\ntype Resource interface { Ping() error }\n")
	writeFile(t, filepath.Join(project, "model", "model.go"), "package model\n\nimport (\n \"github.com/plystra/data/declaration\"\n \"github.com/plystra/data/database\"\n)\n\n//plystra:data example.records/v1\nvar Records = declaration.Member[database.Resource]{Namespace: \"records\"}\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: project, Environment: environment, DataCompilerCacheRoot: filepath.Join(root, "compiler-cache"), CompileTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	accepted, ok := resolved.DataAnalysis()
	if !ok || !accepted.Valid() || accepted.ModelDigest("example.records/v1") == "" {
		t.Fatalf("accepted Data analysis = %#v, ok=%t", accepted, ok)
	}
	activation, ok := resolved.DataActivation()
	if !ok || len(activation.Assignments()) != 1 || activation.Assignments()[0].Access() != "database.records" || activation.Assignments()[0].Backend() != "postgres/v1" {
		t.Fatalf("Data activation = %#v, ok=%t", activation, ok)
	}
	graph := resolved.InterfaceResolution().Graph()
	graphGenerated := graph.GeneratedResourceConstructionOrder()
	if len(graphGenerated) != 1 || graphGenerated[0].Name() != "database.records" || graphGenerated[0].MemberID() != "example.records/v1" || graphGenerated[0].PackagePath() != "example.com/project/resource" || graphGenerated[0].ResourceID().String() != "example.records/v1" {
		t.Fatalf("generated Data access graph = %#v", graphGenerated)
	}
	accessDependencies := graph.ResourceDependencies(graphGenerated[0].Constructor())
	if len(accessDependencies) != 1 || accessDependencies[0].InstanceName() != "database.primary" || accessDependencies[0].Provider().String() != "github.com/plystra/data/postgres.New" || accessDependencies[0].Reason() != constructorgraph.SelectionUnique {
		t.Fatalf("generated Data access dependency = %#v", accessDependencies)
	}
	output, ok := resolved.DataAnalyzeOutput()
	if !ok || len(output) == 0 {
		t.Fatal("partial resolution omitted accepted analyze output")
	}
	output[0] = '!'
	second, _ := resolved.DataAnalyzeOutput()
	if len(second) == 0 || second[0] == '!' {
		t.Fatal("accepted analyze output accessor is not defensive")
	}
	artifact, ok := resolved.DataCompilerArtifact()
	if !ok || artifact.Path == "" || artifact.BinaryDigest == "" {
		t.Fatalf("partial resolution compiler = %#v, ok=%t", artifact, ok)
	}
	selectedManifest, ok := resolved.DataCompilerManifest()
	if !ok || selectedManifest.EmitProtocol != datacompiler.EmitSchema {
		t.Fatalf("partial resolution compiler manifest = %#v, ok=%t", selectedManifest, ok)
	}
	observations := resolved.DataCompilerObservations()
	if got := dataCompilerObservationIDs(observations); got != "data-compiler-build-temporary,data-compiler-build-execution,data-compiler-cache-materialization,data-compiler-analyze-execution" {
		t.Fatalf("cold resolution observations = %q", got)
	}
	observations[0].Verification[0] = "changed"
	if resolved.DataCompilerObservations()[0].Verification[0] == "changed" {
		t.Fatal("resolution observations accessor is not defensive")
	}
}

func TestGenerateEmitsAndCleansDataArtifacts(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.99.99", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        dataCompilerManifest(t),
		"plystra.yaml":                      []byte("{}\n"),
		"declaration/declaration.go":        []byte("package declaration\n\ntype Member[Access any] struct { Namespace string; Access Access }\n"),
		"database/resource.go":              []byte("package database\n\n//plystra:resource data.database/v1\ntype Resource interface { Ping() error }\n"),
		"postgres/postgres.go":              []byte("package postgres\n\nimport \"github.com/plystra/data/database\"\n\ntype provider struct{}\nvar _ database.Resource = (*provider)(nil)\nfunc (*provider) Ping() error { return nil }\n\n//plystra:implements-resource data.database/v1\nfunc New() (*provider, error) { return &provider{}, nil }\n"),
		"cmd/plystra-data-compiler/main.go": []byte(snapshotCheckingCompilerSource),
	})
	project := filepath.Join(root, "project")
	kernelRoot := testkernel.Root(t)
	writeFile(t, filepath.Join(project, "go.mod"), fmt.Sprintf(`module example.com/project

go 1.26

require (
	github.com/plystra/data v0.99.99
    github.com/plystra/kernel v0.0.0
    go.yaml.in/yaml/v3 v3.0.5
    golang.org/x/mod v0.38.0
    golang.org/x/sys v0.47.0
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.primary: {use: github.com/plystra/data/postgres.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: database.records}}}\n")
	writeFile(t, filepath.Join(project, "resource", "resource.go"), "package resource\n\n//plystra:resource example.records/v1\ntype Resource interface { Ping() error }\n")
	writeFile(t, filepath.Join(project, "model", "model.go"), "package model\n\nimport (\n \"github.com/plystra/data/declaration\"\n \"github.com/plystra/data/database\"\n)\n\n//plystra:data example.records/v1\nvar Records = declaration.Member[database.Resource]{Namespace: \"records\"}\n")
	environment := compilerProxyEnvironmentWithPublicFallback(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	options := applicationgenerate.Options{
		Start: project, Environment: environment, DataCompilerCacheRoot: filepath.Join(root, "compiler-cache"), CompileTimeout: 30 * time.Second,
		Validate: func(context.Context, string) error { return nil },
	}
	generated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil {
		t.Fatalf("Generate with Data emit: %v", err)
	}
	if !generated.Installed() || !generated.Report().Clean() {
		t.Fatalf("generated Data result = installed %t, changes %#v", generated.Installed(), generated.Report().Changes())
	}
	for _, filePath := range []string{"generated/data/database.primary/manifest.json", "generated/data/database.primary/schema/schema.sql"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(filePath))); err != nil {
			t.Fatalf("emitted Data artifact %s: %v", filePath, err)
		}
		artifact, exists, err := generatedfiles.ReadArtifact(project, filePath)
		if err != nil || !exists || !artifact.Valid() || artifact.Generator() != "plystra.data/v1" {
			t.Fatalf("Data artifact provenance %s = %#v, exists=%t, err=%v", filePath, artifact, exists, err)
		}
		values := append(artifact.InputRecordIDs(), artifact.Sources()...)
		for _, expected := range []string{"data-compiler:github.com/plystra/data@v0.99.99", "data-resource:database.primary", "data-member:example.records/v1", "Data provider github.com/plystra/data/postgres.New"} {
			found := false
			for _, value := range values {
				if value == expected {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("Data artifact %s provenance omits %q: %#v", filePath, expected, values)
			}
		}
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Check: true, Environment: environment, DataCompilerCacheRoot: options.DataCompilerCacheRoot, CompileTimeout: options.CompileTimeout,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("Data generate check = %#v, %v", checked.Report().Changes(), err)
	}
	manifestBefore, err := os.ReadFile(filepath.Join(project, "generated/data/database.primary/manifest.json"))
	if err != nil {
		t.Fatalf("read generated Data manifest before malformed emit: %v", err)
	}
	malformedEnvironment := append([]string(nil), environment...)
	malformedEnvironment = append(malformedEnvironment, "PLYSTRA_EMIT_TEST_MODE=manifest-mismatch")
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Environment: malformedEnvironment, DataCompilerCacheRoot: options.DataCompilerCacheRoot, CompileTimeout: options.CompileTimeout,
	})
	if !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) || !strings.Contains(err.Error(), "model digest") {
		t.Fatalf("malformed staged Data manifest error = %v", err)
	}
	manifestAfter, err := os.ReadFile(filepath.Join(project, "generated/data/database.primary/manifest.json"))
	if err != nil {
		t.Fatalf("read generated Data manifest after malformed emit: %v", err)
	}
	if !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatal("malformed staged Data manifest changed installed output")
	}
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.replica: {use: github.com/plystra/data/postgres.New}}}\ndata: {members: {example.records/v1: {resource: database.replica, access: database.records}}}\n")
	replaced, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil {
		t.Fatalf("Generate after Resource selection change: %v", err)
	}
	for _, filePath := range []string{"generated/data/database.replica/manifest.json", "generated/data/database.replica/schema/schema.sql"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(filePath))); err != nil {
			t.Fatalf("selected Resource artifact %s: %v", filePath, err)
		}
	}
	for _, filePath := range []string{"generated/data/database.primary/manifest.json", "generated/data/database.primary/schema/schema.sql"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(filePath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unselected Resource artifact %s remains: %v", filePath, err)
		}
	}
	if !replaced.Report().Clean() {
		t.Fatalf("Resource selection replacement changes = %#v", replaced.Report().Changes())
	}
	checked, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Check: true, Environment: environment, DataCompilerCacheRoot: options.DataCompilerCacheRoot, CompileTimeout: options.CompileTimeout,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("selected Resource generate check = %#v, %v", checked.Report().Changes(), err)
	}
	writeFile(t, filepath.Join(project, "plystra.yaml"), "{}\n")
	cleaned, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil {
		t.Fatalf("Generate after Data removal: %v", err)
	}
	for _, filePath := range []string{"generated/data/database.primary/manifest.json", "generated/data/database.primary/schema/schema.sql"} {
		if _, err := os.Stat(filepath.Join(project, filepath.FromSlash(filePath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale generated Data artifact %s remains: %v", filePath, err)
		}
	}
	if !cleaned.Report().Clean() {
		t.Fatalf("Data cleanup result changes = %#v", cleaned.Report().Changes())
	}
	checked, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Check: true, Environment: environment, DataCompilerCacheRoot: options.DataCompilerCacheRoot, CompileTimeout: options.CompileTimeout,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("post-cleanup generate check = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestResolveRejectsNonPostgreSQLDataProviderAfterAnalysis(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.3.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        dataCompilerManifest(t),
		"declaration/declaration.go":        []byte("package declaration\n\ntype Member[Access any] struct { Namespace string; Access Access }\n"),
		"plystra.yaml":                      []byte("{}\n"),
		"database/resource.go":              []byte("package database\n\n//plystra:resource data.database/v1\ntype Resource interface { Ping() error }\n"),
		"postgres/postgres.go":              []byte("package postgres\n\nimport \"github.com/plystra/data/database\"\n\ntype provider struct{}\nvar _ database.Resource = (*provider)(nil)\nfunc (*provider) Ping() error { return nil }\n\n//plystra:implements-resource data.database/v1\nfunc New() (*provider, error) { return &provider{}, nil }\n"),
		"cmd/plystra-data-compiler/main.go": []byte(snapshotCheckingCompilerSource),
	})
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.3.0\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.primary: {use: example.com/project/backend.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: database.records}}}\n")
	writeFile(t, filepath.Join(project, "resource", "resource.go"), "package resource\n\n//plystra:resource example.records/v1\ntype Resource interface { Ping() error }\n")
	writeFile(t, filepath.Join(project, "model", "model.go"), "package model\n\nimport (\n \"github.com/plystra/data/declaration\"\n \"github.com/plystra/data/database\"\n)\n\n//plystra:data example.records/v1\nvar Records = declaration.Member[database.Resource]{Namespace: \"records\"}\n")
	writeFile(t, filepath.Join(project, "backend", "backend.go"), "package backend\n\nimport \"github.com/plystra/data/database\"\n\ntype provider struct{}\nvar _ database.Resource = (*provider)(nil)\nfunc (*provider) Ping() error { return nil }\n\n//plystra:implements-resource data.database/v1\nfunc New() (*provider, error) { return &provider{}, nil }\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: project, Environment: environment, DataCompilerCacheRoot: filepath.Join(root, "compiler-cache"), CompileTimeout: 30 * time.Second,
	})
	if !errors.Is(err, applicationresolve.ErrDataAssignment) || !strings.Contains(err.Error(), "official PostgreSQL provider") {
		t.Fatalf("unsupported Data provider error = %v", err)
	}
	if activation, ok := resolved.DataActivation(); !ok || !activation.Valid() || activation.Assignments()[0].Backend() != "" {
		t.Fatalf("provider rejection discarded accepted activation = %#v, ok=%t", activation, ok)
	}
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.primary: {use: example.com/project/missing.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: database.records}}}\n")
	resolved, err = applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: project, Environment: environment, DataCompilerCacheRoot: filepath.Join(root, "compiler-cache"), Offline: true,
	})
	if err == nil || !strings.Contains(err.Error(), "provider is not visible") {
		t.Fatalf("post-analysis Resource failure = %v", err)
	}
	if activation, ok := resolved.DataActivation(); !ok || !activation.Valid() {
		t.Fatalf("post-analysis failure discarded accepted activation = %#v, ok=%t", activation, ok)
	}
}

func dataCompilerObservationIDs(values []datacompiler.Observation) string {
	ids := make([]string, len(values))
	for index, value := range values {
		ids[index] = value.ID
	}
	return strings.Join(ids, ",")
}

func TestDataCompilerSelectionOfflineRejectsColdModuleGraph(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.2.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{"plystra-data-compiler.json": dataCompilerManifest(t)})
	project := filepath.Join(root, "project")
	goMod := "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n"
	writeFile(t, filepath.Join(project, "go.mod"), goMod)
	writeFile(t, filepath.Join(project, "plystra.yaml"), "{}\n")
	_, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{
		Start: project, Environment: compilerProxyEnvironment(t, proxy), Offline: true,
	})
	if !errors.Is(err, moduledependency.ErrDiscover) {
		t.Fatalf("cold offline discovery error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, "go.mod"))
	if err != nil || string(data) != goMod {
		t.Fatalf("offline discovery changed go.mod: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "go.sum")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("offline discovery created go.sum: %v", err)
	}
}

func TestDataCompilerSelectionRejectsAbsentAndInvalidDistribution(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		project := t.TempDir()
		writeModule(t, project, "example.com/project")
		writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
		options := applicationresolve.Options{Start: project, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
		_, err := applicationresolve.Resolve(t.Context(), options)
		if !errors.Is(err, datacompiler.ErrSelection) || !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) || !strings.Contains(err.Error(), datacompiler.ModulePath) {
			t.Fatalf("missing module error = %v", err)
		}
	})
	t.Run("invalid manifest", func(t *testing.T) {
		root := t.TempDir()
		proxy := filepath.Join(root, "proxy")
		writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.2.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{"plystra-data-compiler.json": []byte("{}\n")})
		project := filepath.Join(root, "project")
		writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n")
		writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
		environment := compilerProxyEnvironment(t, proxy)
		runCompilerGo(t, project, environment, "mod", "download", "all")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: project, Environment: environment})
		if !errors.Is(err, datacompiler.ErrInvalidManifest) || !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) {
			t.Fatalf("invalid distribution error = %v", err)
		}
	})
}

func TestDataCompilerSelectionRejectsReplacement(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	project := filepath.Join(root, "project")
	writeModule(t, dataRoot, datacompiler.ModulePath)
	writeFile(t, filepath.Join(dataRoot, "plystra-data-compiler.json"), string(dataCompilerManifest(t)))
	writeFile(t, filepath.Join(project, "go.mod"), fmt.Sprintf("module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n\nreplace github.com/plystra/data => %s\n", filepath.ToSlash(dataRoot)))
	writeFile(t, filepath.Join(project, "plystra.yaml"), "{}\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{Start: project, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
	if err != nil {
		t.Fatalf("DiscoverSelectionInputs: %v", err)
	}
	_, err = inputs.DataCompilerSelection()
	if !errors.Is(err, datacompiler.ErrInvalidSelection) {
		t.Fatalf("replacement selection error = %v", err)
	}
}

func TestDataCompilerSelectionRejectsWorkspaceSource(t *testing.T) {
	root := t.TempDir()
	dataRoot := filepath.Join(root, "data")
	project := filepath.Join(root, "project")
	writeModule(t, dataRoot, datacompiler.ModulePath)
	writeFile(t, filepath.Join(dataRoot, "plystra-data-compiler.json"), string(dataCompilerManifest(t)))
	writeModule(t, project, "example.com/project")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "{}\n")
	work := filepath.Join(root, "go.work")
	writeFile(t, work, "go 1.26\nuse (\n ./project\n ./data\n)\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{Start: project, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})})
	if err != nil {
		t.Fatalf("DiscoverSelectionInputs: %v", err)
	}
	_, err = inputs.DataCompilerSelection()
	if !errors.Is(err, datacompiler.ErrInvalidSelection) {
		t.Fatalf("workspace selection error = %v", err)
	}
}

func TestResolveIgnoresRemovedDataMemberWithoutCompiler(t *testing.T) {
	project := t.TempDir()
	writeModule(t, project, "example.com/project")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "data: {members: {example.records/v1: {resource: database.primary}}}\n")
	writeFile(t, filepath.Join(project, "plystra.production.yaml"), "data: {members: {example.records/v1: {$remove: true}}}\n")
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: project, EnvironmentName: "production", Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
	if err != nil {
		t.Fatalf("removed member should not select Data: %v", err)
	}
}

func dataCompilerManifest(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(datacompiler.Manifest{
		Schema: datacompiler.DistributionSchema, ModulePath: datacompiler.ModulePath,
		CommandImportPath: datacompiler.CommandImportPath, AnalyzeProtocol: datacompiler.AnalyzeSchema,
		EmitProtocol: datacompiler.EmitSchema, DeclarationLanguage: datacompiler.DeclarationLanguage,
		Bounds: datacompiler.Bounds{
			MaxRoots: datacompiler.MaxRoots, MaxNodes: datacompiler.MaxNodes,
			MaxImports: datacompiler.MaxImports, MaxNesting: datacompiler.MaxNesting,
			MaxSymbolBytes: datacompiler.MaxSymbolBytes, MaxDiagnostics: datacompiler.MaxDiagnostics,
			MaxDiagnosticBytes: datacompiler.MaxDiagnosticBytes, MaxFrameBytes: datacompiler.MaxFrameBytes,
			MaxArtifacts: datacompiler.MaxArtifacts, MaxArtifactBytes: datacompiler.MaxArtifactBytes,
			MaxArtifactPathBytes: datacompiler.MaxArtifactPathBytes,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

const snapshotCheckingCompilerSource = `package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
)

func main() {
    var header [4]byte
    if _, err := io.ReadFull(os.Stdin, header[:]); err != nil { os.Exit(2) }
    payload := make([]byte, binary.BigEndian.Uint32(header[:]))
    if _, err := io.ReadFull(os.Stdin, payload); err != nil { os.Exit(2) }
    var input map[string]any
    if json.Unmarshal(payload, &input) != nil { os.Exit(2) }
    if phase, _ := input["phase"].(string); phase == "emit" {
        emit(input, header)
        return
    }
    snapshot, ok := input["snapshot"].(map[string]any)
    if !ok { os.Exit(2) }
    packages, packagesOK := snapshot["packages"].([]any)
    resources, resourcesOK := snapshot["resources"].([]any)
	foundPackage, foundResource := false, false
	var source string
	if packagesOK {
		for _, value := range packages {
			packageValue, ok := value.(map[string]any)
			if !ok { continue }
			if packageValue["import_path"] == "example.com/project/resource" { foundPackage = true }
			if packageValue["import_path"] != "example.com/project/model" { continue }
			files, ok := packageValue["files"].([]any)
			if !ok { continue }
			for _, fileValue := range files {
				file, ok := fileValue.(map[string]any)
				if !ok || file["path"] != "model/model.go" { continue }
				encoded, ok := file["content"].(string)
				if !ok { continue }
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil { continue }
				source = string(decoded)
			}
		}
	}
    if resourcesOK {
        for _, value := range resources {
            resourceValue, ok := value.(map[string]any)
            if ok && resourceValue["import_path"] == "example.com/project/resource" { foundResource = true }
        }
    }
	if !foundPackage || !foundResource || source == "" { os.Exit(2) }
	initializer := "declaration.Member[database.Resource]{Namespace: \"records\"}"
	start := strings.Index(source, initializer)
	if start < 0 { os.Exit(2) }
	position := func(offset int) map[string]any {
		line, column := 1, 1
		for index := 0; index < offset; index++ {
			if source[index] == '\n' { line, column = line+1, 1 } else { column++ }
		}
		return map[string]any{"line": line, "column": column}
	}
	startPosition, endPosition := position(start), position(start+len(initializer))
	sum := sha256.Sum256([]byte(source))
	root := map[string]any{
		"member_id": "example.records/v1", "package_path": "example.com/project/model", "file_path": "model/model.go", "symbol": "Records",
		"model": map[string]any{"namespace": "records"},
		"access_package": "example.com/project/resource", "access_type": "Resource", "access_id": "example.records/v1", "migration_only": false,
		"source_digest": "sha256:" + hex.EncodeToString(sum[:]),
		"declaration_pos": map[string]any{"path": "model/model.go", "start_line": startPosition["line"], "start_column": startPosition["column"], "end_line": endPosition["line"], "end_column": endPosition["column"]},
	}
	roots, _ := json.Marshal([]any{root})
	digestPayload := []byte("{\"roots\":" + string(roots) + ",\"diagnostics\":[],\"truncated\":false}")
	digestSum := sha256.Sum256(append([]byte("plystra.data.analyze/v1\x00"), digestPayload...))
	digest := "sha256:" + hex.EncodeToString(digestSum[:])
	response := map[string]any{
		"schema": input["schema"], "phase": input["phase"], "request_id": input["request_id"], "input_digest": input["input_digest"],
		"status": "succeeded", "output_digest": digest, "output": map[string]any{"roots": []any{root}, "digest": digest, "valid": true, "truncated": false},
		"diagnostics": []any{}, "truncated": false,
	}
    data, _ := json.Marshal(response)
    binary.BigEndian.PutUint32(header[:], uint32(len(data)))
    _, _ = os.Stdout.Write(header[:])
    _, _ = os.Stdout.Write(data)
}

func emit(input map[string]any, header [4]byte) {
    assignments, ok := input["assignments"].([]any)
    if !ok || len(assignments) != 1 { os.Exit(2) }
    assignment, ok := assignments[0].(map[string]any)
    if !ok { os.Exit(2) }
    memberID, _ := assignment["member_id"].(string)
    resource, _ := assignment["resource"].(string)
    resourceContract, _ := assignment["resource_contract"].(string)
    provider, _ := assignment["provider"].(string)
    backend, _ := assignment["backend"].(string)
    compiler, ok := input["compiler"].(map[string]any)
    if !ok { os.Exit(2) }
    frozenModelDigest, _ := input["frozen_model_digest"].(string)
    artifact := func(path string, content []byte) map[string]any {
        sum := sha256.Sum256(content)
        return map[string]any{
            "path": path, "mode": 0644, "bytes": content, "digest": "sha256:" + hex.EncodeToString(sum[:]),
            "owning_members": []string{memberID}, "resource": resource, "resource_contract": resourceContract,
            "provider": provider, "backend": backend, "compiler": compiler, "frozen_model_digest": frozenModelDigest,
        }
    }
	schemaPath := "generated/data/" + resource + "/schema/schema.sql"
	schemaArtifact := artifact(schemaPath, []byte("CREATE TABLE records (id bigint NOT NULL);\n"))
	modelSum := sha256.Sum256(append([]byte("plystra.data.logical-model/v1\x00"), []byte("{\"namespace\":\"records\"}")...))
	manifest, _ := json.Marshal(map[string]any{
		"schema": "plystra.data-instance-manifest/v1", "resource": resource, "resource_contract": resourceContract,
		"provider": provider, "backend": backend, "compiler": compiler, "analyze_digest": input["analyze_digest"],
		"input_digest": input["input_digest"], "frozen_model_digest": frozenModelDigest,
		"member_models": map[string]string{memberID: "sha256:" + hex.EncodeToString(modelSum[:])},
		"artifacts": []map[string]any{{"path": schemaPath, "mode": 0644, "digest": schemaArtifact["digest"], "owning_members": []string{memberID}}},
	})
	if os.Getenv("PLYSTRA_EMIT_TEST_MODE") == "manifest-mismatch" {
		var value map[string]any
		if json.Unmarshal(manifest, &value) != nil {
			os.Exit(2)
		}
		value["member_models"] = map[string]string{memberID: "sha256:" + strings.Repeat("0", 64)}
		manifest, _ = json.Marshal(value)
	}
	artifacts := []map[string]any{artifact("generated/data/"+resource+"/manifest.json", manifest), schemaArtifact}
    artifactBytes, _ := json.Marshal(artifacts)
    outputSum := sha256.Sum256(append([]byte("plystra.data.emit-output/v1\x00"), artifactBytes...))
    response := map[string]any{
        "schema": input["schema"], "phase": input["phase"], "request_id": input["request_id"], "input_digest": input["input_digest"],
        "status": "succeeded", "output_digest": "sha256:" + hex.EncodeToString(outputSum[:]),
        "output": map[string]any{"artifacts": json.RawMessage(artifactBytes)}, "diagnostics": []any{}, "truncated": false,
    }
    data, _ := json.Marshal(response)
    binary.BigEndian.PutUint32(header[:], uint32(len(data)))
    _, _ = os.Stdout.Write(header[:])
    _, _ = os.Stdout.Write(data)
}
`

func writeCompilerProxyModule(t *testing.T, proxy, path, version, goMod string, files map[string][]byte) {
	t.Helper()
	escapedPath, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	versionRoot := filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v")
	writeFile(t, filepath.Join(versionRoot, "list"), version+"\n")
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-10-07T00:00:00Z\"}\n", version))
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), goMod)
	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	contents := map[string][]byte{"go.mod": []byte(goMod)}
	for name, data := range files {
		contents[name] = data
	}
	names := make([]string, 0, len(contents))
	for name := range contents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := contents[name]
		header := &zip.FileHeader{Name: path + "@" + version + "/" + name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func compilerProxyEnvironment(t *testing.T, proxy string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxy)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	moduleCache := filepath.Join(t.TempDir(), "go-mod")
	t.Cleanup(func() {
		if err := makeWritableTree(moduleCache); err != nil {
			t.Logf("restore module cache permissions: %v", err)
		}
	})
	return goEnvironment(map[string]string{
		"GOCACHE": filepath.Join(t.TempDir(), "go-build"), "GOENV": "off",
		"GOMODCACHE": moduleCache,
		"GONOPROXY":  "none", "GOPRIVATE": "", "GOPROXY": (&url.URL{Scheme: "file", Path: proxyPath}).String(),
		"GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
	})
}

func compilerProxyEnvironmentWithPublicFallback(t *testing.T, proxy string) []string {
	t.Helper()
	environment := compilerProxyEnvironment(t, proxy)
	for index, entry := range environment {
		if strings.HasPrefix(entry, "GOPROXY=") {
			environment[index] = "GOPROXY=" + strings.TrimPrefix(entry, "GOPROXY=") + "|https://proxy.golang.org"
			return environment
		}
	}
	t.Fatal("compiler proxy environment omitted GOPROXY")
	return nil
}

func makeWritableTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chmod(path, 0o700)
	})
}

func runCompilerGo(t *testing.T, directory string, environment []string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = directory
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
