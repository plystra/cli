package applicationresolve_test

import (
	"archive/zip"
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
	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/moduledependency"
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
	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationresolve.ErrDataCompilerAnalysisUnavailable) || !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) {
		t.Fatalf("Resolve error = %v", err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) == 0 {
		t.Fatalf("compiler cache entries = %v, error = %v", entries, err)
	}
	options.Offline = true
	_, err = applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationresolve.ErrDataCompilerAnalysisUnavailable) {
		t.Fatalf("offline warm Resolve error = %v", err)
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
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: project, Environment: environment, Offline: true, DataCompilerCacheRoot: cache,
	})
	if !errors.Is(err, applicationresolve.ErrDataCompilerUnavailable) || !errors.Is(err, datacompiler.ErrOfflineUnavailable) {
		t.Fatalf("offline generation error = %v", err)
	}
	if _, statErr := os.Stat(cache); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("offline generation materialized compiler cache: %v", statErr)
	}
}

func TestResolveInvokesAnalyzeWithResourceContractSourceClosure(t *testing.T) {
	root := t.TempDir()
	proxy := filepath.Join(root, "proxy")
	manifest := dataCompilerManifest(t)
	writeCompilerProxyModule(t, proxy, datacompiler.ModulePath, "v0.3.0", "module github.com/plystra/data\n\ngo 1.26\n", map[string][]byte{
		"plystra-data-compiler.json":        manifest,
		"declaration/declaration.go":        []byte("package declaration\n\ntype NoAccess struct{}\ntype Member[Access any] struct { Namespace string; Access Access }\n"),
		"cmd/plystra-data-compiler/main.go": []byte(snapshotCheckingCompilerSource),
	})
	project := filepath.Join(root, "project")
	writeFile(t, filepath.Join(project, "go.mod"), "module example.com/project\n\ngo 1.26\n\nrequire github.com/plystra/data v0.3.0\n")
	writeFile(t, filepath.Join(project, "plystra.yaml"), "resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: database.records}}}\n")
	writeFile(t, filepath.Join(project, "resource", "resource.go"), "package resource\n\n//plystra:resource data.database/v1\ntype Resource interface { Ping() error }\n")
	writeFile(t, filepath.Join(project, "model", "model.go"), "package model\n\nimport (\n \"github.com/plystra/data/declaration\"\n \"example.com/project/resource\"\n)\n\n//plystra:data example.records/v1\nvar Records = declaration.Member[resource.Resource]{Namespace: \"records\"}\n")
	environment := compilerProxyEnvironment(t, proxy)
	runCompilerGo(t, project, environment, "mod", "download", "all")
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: project, Environment: environment, DataCompilerCacheRoot: filepath.Join(root, "compiler-cache"), CompileTimeout: 30 * time.Second,
	})
	if !errors.Is(err, applicationresolve.ErrDataCompilerAnalysisUnavailable) || !strings.HasSuffix(err.Error(), applicationresolve.ErrDataCompilerAnalysisUnavailable.Error()) {
		t.Fatalf("Resolve error = %v", err)
	}
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
	initializer := "declaration.Member[resource.Resource]{Namespace: \"records\"}"
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
		"access_package": "example.com/project/resource", "access_type": "Resource", "access_id": "data.database/v1", "migration_only": false,
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
