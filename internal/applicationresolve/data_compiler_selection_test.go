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

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/datacompiler"
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
	options := applicationresolve.Options{Start: project, Environment: environment}
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
	return goEnvironment(map[string]string{
		"GOCACHE": filepath.Join(t.TempDir(), "go-build"), "GOENV": "off",
		"GOMODCACHE": filepath.Join(t.TempDir(), "go-mod"),
		"GONOPROXY":  "none", "GOPRIVATE": "", "GOPROXY": (&url.URL{Scheme: "file", Path: proxyPath}).String(),
		"GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
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
