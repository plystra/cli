package datacompiler_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/datacompiler"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestBuildAnalyzeSourceSnapshotIncludesNoAccessAndSelectedDeclarationFiles(t *testing.T) {
	t.Parallel()
	compilerRoot := writeSnapshotCompilerModule(t)
	projectRoot := writeSnapshotProject(t, compilerRoot, "")
	packages := discoverSnapshotDataPackages(t, projectRoot, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}))
	selection := selectSnapshotCompiler(t, compilerRoot)

	snapshot, err := datacompiler.BuildAnalyzeSourceSnapshot(t.Context(), datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages: packages,
		Modules:      []datacompiler.SnapshotModule{{ModulePath: "example.com/app", Root: projectRoot}},
		Compiler:     selection,
		Environment:  goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	jsonData, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(jsonData), projectRoot) || strings.Contains(string(jsonData), compilerRoot) || snapshot.Digest() == "" {
		t.Fatalf("snapshot leaked a private root or digest: %q, %q", jsonData, snapshot.Digest())
	}
	packagesOut := snapshot.Packages()
	if len(packagesOut) != 2 || packagesOut[0].ImportPath() != "example.com/app/model" || packagesOut[0].RootEligibility() != "eligible" || packagesOut[1].ImportPath() != datacompiler.DeclarationImportPath || packagesOut[1].RootEligibility() != "support" {
		t.Fatalf("snapshot packages = %#v", packagesOut)
	}
	rootFiles := packagesOut[0].Files()
	if len(rootFiles) != 1 || !strings.Contains(string(rootFiles[0].Content()), "declaration.NoAccess") || rootFiles[0].Path() != "model/model.go" || rootFiles[0].Digest() == "" || rootFiles[0].Bytes() != len(rootFiles[0].Content()) {
		t.Fatalf("Data root files = %#v", rootFiles)
	}
	declarationFiles := packagesOut[1].Files()
	if len(declarationFiles) != 1 || declarationFiles[0].Path() != "declaration/declaration.go" {
		t.Fatalf("declaration files = %#v", declarationFiles)
	}
}

func TestBuildAnalyzeSourceSnapshotUsesGoSelectedBuildTags(t *testing.T) {
	t.Parallel()
	compilerRoot := writeSnapshotCompilerModule(t)
	projectRoot := writeSnapshotProject(t, compilerRoot, "snapshot_data_tag")
	packages := discoverSnapshotDataPackages(t, projectRoot, goEnvironment(map[string]string{
		"GOWORK": "off", "GOPROXY": "off", "GOFLAGS": "-tags=snapshot_data_tag",
	}))
	if len(packages) != 1 || len(packages[0].Files()) != 2 || packages[0].Files()[0].Path() != "model/model.go" || packages[0].Files()[1].Path() != "model/selected.go" {
		t.Fatalf("Go-selected Data files = %#v", packages)
	}
	selection := selectSnapshotCompiler(t, compilerRoot)
	snapshot, err := datacompiler.BuildAnalyzeSourceSnapshot(t.Context(), datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages: packages,
		Modules:      []datacompiler.SnapshotModule{{ModulePath: "example.com/app", Root: projectRoot}},
		Compiler:     selection,
		Environment: goEnvironment(map[string]string{
			"GOWORK": "off", "GOPROXY": "off", "GOFLAGS": "-tags=snapshot_declaration_tag",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range snapshot.Packages()[0].Files() {
		if file.Path() == "model/selected.go" && !strings.Contains(string(file.Content()), "snapshot_data_tag") {
			t.Fatalf("selected Data source content = %q", file.Content())
		}
	}
	declarationFiles := snapshot.Packages()[1].Files()
	if len(declarationFiles) != 2 || declarationFiles[1].Path() != "declaration/selected.go" {
		t.Fatalf("Go-selected declaration files = %#v", declarationFiles)
	}
}

func TestBuildAnalyzeSourceSnapshotRejectsSourceDrift(t *testing.T) {
	t.Parallel()
	compilerRoot := writeSnapshotCompilerModule(t)
	projectRoot := writeSnapshotProject(t, compilerRoot, "")
	packages := discoverSnapshotDataPackages(t, projectRoot, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}))
	selection := selectSnapshotCompiler(t, compilerRoot)
	writeSnapshotFile(t, filepath.Join(projectRoot, "model", "model.go"), snapshotDataSource("changed"))
	_, err := datacompiler.BuildAnalyzeSourceSnapshot(t.Context(), datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages: packages,
		Modules:      []datacompiler.SnapshotModule{{ModulePath: "example.com/app", Root: projectRoot}},
		Compiler:     selection,
		Environment:  goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if !errors.Is(err, datacompiler.ErrSourceSnapshotDrift) || strings.Contains(err.Error(), projectRoot) {
		t.Fatalf("source drift error = %v", err)
	}
}

func TestBuildAnalyzeSourceSnapshotRejectsCompilerDrift(t *testing.T) {
	t.Parallel()
	compilerRoot := writeSnapshotCompilerModule(t)
	projectRoot := writeSnapshotProject(t, compilerRoot, "")
	packages := discoverSnapshotDataPackages(t, projectRoot, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}))
	selection := selectSnapshotCompiler(t, compilerRoot)
	writeSnapshotFile(t, filepath.Join(compilerRoot, "declaration", "declaration.go"), "package declaration\n\ntype NoAccess struct { Changed bool }\ntype Member[Access any] struct { Namespace string; Access Access }\n")
	_, err := datacompiler.BuildAnalyzeSourceSnapshot(t.Context(), datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages: packages,
		Modules:      []datacompiler.SnapshotModule{{ModulePath: "example.com/app", Root: projectRoot}},
		Compiler:     selection,
		Environment:  goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if !errors.Is(err, datacompiler.ErrSourceSnapshotDrift) || strings.Contains(err.Error(), compilerRoot) {
		t.Fatalf("compiler drift error = %v", err)
	}
}

func TestBuildAnalyzeSourceSnapshotHonorsCompilerFrameBound(t *testing.T) {
	t.Parallel()
	compilerRoot := writeSnapshotCompilerModule(t)
	manifest := strings.Replace(validManifestJSON, `"max_frame_bytes": 16777216`, `"max_frame_bytes": 128`, 1)
	writeSnapshotFile(t, filepath.Join(compilerRoot, "plystra-data-compiler.json"), manifest)
	projectRoot := writeSnapshotProject(t, compilerRoot, "")
	packages := discoverSnapshotDataPackages(t, projectRoot, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}))
	_, err := datacompiler.BuildAnalyzeSourceSnapshot(t.Context(), datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages: packages,
		Modules:      []datacompiler.SnapshotModule{{ModulePath: "example.com/app", Root: projectRoot}},
		Compiler:     selectSnapshotCompiler(t, compilerRoot),
		Environment:  goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if !errors.Is(err, datacompiler.ErrSourceSnapshotBounds) {
		t.Fatalf("frame bound error = %v", err)
	}
}

func writeSnapshotCompilerModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSnapshotFile(t, filepath.Join(root, "go.mod"), "module github.com/plystra/data\n\ngo 1.26\n")
	writeSnapshotFile(t, filepath.Join(root, "plystra-data-compiler.json"), validManifestJSON)
	writeSnapshotFile(t, filepath.Join(root, "declaration", "declaration.go"), "package declaration\n\ntype NoAccess struct{}\ntype Member[Access any] struct { Namespace string; Access Access }\n")
	writeSnapshotFile(t, filepath.Join(root, "declaration", "selected.go"), "//go:build snapshot_declaration_tag\n\npackage declaration\n\nconst Selected = true\n")
	return root
}

func writeSnapshotProject(t *testing.T, compilerRoot, dataTag string) string {
	t.Helper()
	root := t.TempDir()
	relativeCompiler, err := filepath.Rel(root, compilerRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire github.com/plystra/data v0.2.0\n\nreplace github.com/plystra/data => "+filepath.ToSlash(relativeCompiler)+"\n")
	writeSnapshotFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeSnapshotFile(t, filepath.Join(root, "model", "model.go"), snapshotDataSource("base"))
	if dataTag != "" {
		writeSnapshotFile(t, filepath.Join(root, "model", "selected.go"), "//go:build "+dataTag+"\n\npackage model\n\nconst Selected = true\n")
	}
	return root
}

func snapshotDataSource(namespace string) string {
	return "package model\n\nimport \"github.com/plystra/data/declaration\"\n\n//plystra:data records.item/v1\nvar Records = declaration.Member[declaration.NoAccess]{Namespace: \"" + namespace + "\"}\n"
}

func selectSnapshotCompiler(t *testing.T, root string) datacompiler.Selection {
	t.Helper()
	checksum, err := dirhash.HashDir(root, datacompiler.ModulePath+"@v0.2.0", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := datacompiler.Resolve(datacompiler.Source{
		ModulePath: datacompiler.ModulePath, ModuleVersion: "v0.2.0", ModuleChecksum: checksum, Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func discoverSnapshotDataPackages(t *testing.T, root string, environment []string) []interfaceinventory.DataPackage {
	t.Helper()
	module, err := modulelocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), module, moduledependency.Index{}, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	return discovery.DataPackages()
}

func writeSnapshotFile(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, exists := strings.Cut(entry, "=")
		if exists {
			values[strings.ToUpper(name)] = name + "=" + value
		}
	}
	for name, value := range overrides {
		values[strings.ToUpper(name)] = name + "=" + value
	}
	result := make([]string, 0, len(values))
	for _, entry := range values {
		result = append(result, entry)
	}
	return result
}
