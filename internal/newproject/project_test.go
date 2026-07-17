package newproject_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_NEW_PLUGIN_ROLLBACK_HELPER") == "1" {
		switch {
		case len(os.Args) == 3 && os.Args[1] == "mod" && (os.Args[2] == "download" || os.Args[2] == "tidy"):
			os.Exit(0)
		case len(os.Args) == 4 && os.Args[1] == "test" && os.Args[2] == "-mod=readonly" && os.Args[3] == "./...":
			os.Exit(9)
		default:
			os.Exit(8)
		}
	}
	os.Exit(main.Run())
}

func TestCreateAndPublicCommandProduceDeterministicBuildableProjects(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	const modulePath = "example.com/acme/my-app"

	directParent := t.TempDir()
	direct, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      directParent,
		ModulePath:  modulePath,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if direct.ModulePath() != modulePath || direct.Path() != filepath.Join(directParent, "my-app") {
		t.Fatalf("Create result = module %q, path %q", direct.ModulePath(), direct.Path())
	}

	commandParent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"new", modulePath}, &stdout, &stderr, commandParent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	commandTarget := filepath.Join(commandParent, "my-app")
	wantOutput := fmt.Sprintf("created %s in %s\n", modulePath, commandTarget)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	directTree := snapshotTree(t, direct.Path())
	commandTree := snapshotTree(t, commandTarget)
	if !reflect.DeepEqual(directTree, commandTree) {
		t.Fatalf("repeated creation differed:\ndirect:  %#v\ncommand: %#v", directTree, commandTree)
	}
	wantFiles := []string{
		".gitattributes",
		".github/workflows/ci.yml",
		".gitignore",
		"README.md",
		"generated/.plystra-manifest.json",
		"generated/go/assembly/compatibility_gen.go",
		"generated/manifest.json",
		"go.mod",
		"go.sum",
		"plystra.yaml",
	}
	var gotFiles []string
	for name := range directTree {
		gotFiles = append(gotFiles, name)
	}
	sort.Strings(gotFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("project files = %v, want %v", gotFiles, wantFiles)
	}
	goldenTree := snapshotTree(t, "testdata/project")
	delete(directTree, "go.sum")
	if !reflect.DeepEqual(directTree, goldenTree) {
		t.Fatalf("project scaffold differs from golden files:\n got: %#v\nwant: %#v", directTree, goldenTree)
	}
	if bytes.Contains(directTree["plystra.yaml"], []byte("instance_id")) {
		t.Fatalf("project scaffold contains deprecated instance_id:\n%s", directTree["plystra.yaml"])
	}
	for _, obsolete := range [][]byte{[]byte("database:"), []byte("audit_write:")} {
		if bytes.Contains(directTree["plystra.yaml"], obsolete) {
			t.Fatalf("project scaffold contains obsolete configuration %q:\n%s", obsolete, directTree["plystra.yaml"])
		}
	}
	if !bytes.Contains(directTree["plystra.yaml"], []byte("  aliases: {}")) {
		t.Fatalf("project scaffold omits capabilities.aliases:\n%s", directTree["plystra.yaml"])
	}
	for name, content := range directTree {
		if bytes.Contains(content, []byte(directParent)) || bytes.Contains(content, []byte(commandParent)) {
			t.Fatalf("%s contains a local absolute path", name)
		}
	}
	assertModuleState(t, direct.Path(), modulePath)
	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       direct.Path(),
		Check:       true,
		Environment: environment,
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("initial generated output = %#v, %v", generated.Report().Changes(), err)
	}
}

func TestCreateLibraryAndPublicCommandProduceDeterministicBuildableModules(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	const modulePath = "example.com/acme/email"

	directParent := t.TempDir()
	direct, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      directParent,
		ModulePath:  modulePath,
		Library:     true,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create library: %v", err)
	}

	commandParent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"new", modulePath, "--library"}, &stdout, &stderr, commandParent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	commandTarget := filepath.Join(commandParent, "email")
	wantOutput := fmt.Sprintf("created %s in %s\n", modulePath, commandTarget)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	directTree := snapshotTree(t, direct.Path())
	commandTree := snapshotTree(t, commandTarget)
	if !reflect.DeepEqual(directTree, commandTree) {
		t.Fatalf("repeated library creation differed:\ndirect:  %#v\ncommand: %#v", directTree, commandTree)
	}
	wantFiles := []string{
		".gitattributes",
		".github/workflows/ci.yml",
		".gitignore",
		"README.md",
		"generated/.plystra-manifest.json",
		"generated/go/assembly/compatibility_gen.go",
		"go.mod",
		"go.sum",
	}
	var gotFiles []string
	for name := range directTree {
		gotFiles = append(gotFiles, name)
	}
	sort.Strings(gotFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("library files = %v, want %v", gotFiles, wantFiles)
	}
	delete(directTree, "go.sum")
	if goldenTree := snapshotTree(t, "testdata/library"); !reflect.DeepEqual(directTree, goldenTree) {
		t.Fatalf("library scaffold differs from golden files:\n got: %#v\nwant: %#v", directTree, goldenTree)
	}
	if _, err := os.Lstat(filepath.Join(direct.Path(), "plystra.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("library contains plystra.yaml: %v", err)
	}
	assertModuleState(t, direct.Path(), modulePath)
}

func TestCreateWithInitialPluginComposesRunnableAndLibraryTransactions(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	const modulePath = "example.com/acme/my-app/v2"
	const pluginName = "account-profile"

	runnableParent := t.TempDir()
	runnable, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      runnableParent,
		ModulePath:  modulePath,
		Plugin:      pluginName,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create with plugin: %v", err)
	}

	libraryParent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"new", modulePath, "--plugin", pluginName, "--library"}
	if exitCode := command.RunIn(arguments, &stdout, &stderr, libraryParent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	libraryRoot := filepath.Join(libraryParent, "my-app")
	wantOutput := fmt.Sprintf("created %s in %s\n", modulePath, libraryRoot)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	golden := snapshotTree(t, filepath.Join("..", "plugincreate", "testdata", "plugin"))
	for kind, root := range map[string]string{"runnable": runnable.Path(), "library": libraryRoot} {
		pluginTree := pluginScaffoldSnapshot(t, root, pluginName)
		if !reflect.DeepEqual(pluginTree, golden) {
			t.Fatalf("%s initial plugin differs from plugin-create golden:\n got: %#v\nwant: %#v", kind, pluginTree, golden)
		}
		assertModuleState(t, root, modulePath)
	}
	if info, err := os.Stat(filepath.Join(runnable.Path(), "plystra.yaml")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("runnable plystra.yaml = %#v, %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(libraryRoot, "plystra.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("library contains plystra.yaml: %v", err)
	}
}

func TestCreateRollsBackGoValidationFailure(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "my-app")
	_, err := newproject.Create(context.Background(), newproject.Options{
		Parent:     parent,
		ModulePath: "example.com/acme/my-app",
		GoCommand:  filepath.Join(parent, "missing-go-command"),
	})
	if !errors.Is(err, newproject.ErrCreate) {
		t.Fatalf("Create error = %v, want ErrCreate", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after failed validation: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateWithInitialPluginRollsBackOuterProjectOnPluginValidationFailure(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	environment := append(os.Environ(), "PLYSTRA_NEW_PLUGIN_ROLLBACK_HELPER=1")
	_, err = newproject.Create(context.Background(), newproject.Options{
		Parent:      parent,
		ModulePath:  "example.com/acme/my-app",
		Plugin:      "account",
		GoCommand:   command,
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, plugincreate.ErrCreate) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v, want project, plugin, and Go command errors", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after plugin validation failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRejectsInvalidInputsBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modulePath string
	}{
		{name: "empty", modulePath: ""},
		{name: "invalid path", modulePath: "example.com/acme/../app"},
		{name: "uppercase base", modulePath: "example.com/acme/MyApp"},
		{name: "double hyphen", modulePath: "example.com/acme/my--app"},
		{name: "trailing hyphen", modulePath: "example.com/acme/my-app-"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			_, err := newproject.Create(context.Background(), newproject.Options{Parent: parent, ModulePath: test.modulePath})
			if !errors.Is(err, newproject.ErrCreate) {
				t.Fatalf("Create error = %v, want ErrCreate", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("parent entries = %v, %v", entries, readErr)
			}
		})
	}
}

func TestCreateRejectsInvalidInitialPluginBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, pluginName := range []string{"Account", "generated", "account--profile"} {
		pluginName := pluginName
		t.Run(pluginName, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			_, err := newproject.Create(context.Background(), newproject.Options{
				Parent:     parent,
				ModulePath: "example.com/acme/my-app",
				Plugin:     pluginName,
			})
			if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, plugincreate.ErrInvalidName) {
				t.Fatalf("Create error = %v, want ErrCreate and ErrInvalidName", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("parent entries = %v, %v", entries, readErr)
			}
		})
	}
}

func TestCreatePreservesExistingProject(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "my-app")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	keep := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := newproject.Create(context.Background(), newproject.Options{
		Parent:     parent,
		ModulePath: "example.com/acme/my-app",
		GoCommand:  filepath.Join(parent, "must-not-run"),
	})
	if !errors.Is(err, newproject.ErrCreate) {
		t.Fatalf("Create error = %v, want ErrCreate", err)
	}
	content, readErr := os.ReadFile(keep)
	if readErr != nil || string(content) != "keep" {
		t.Fatalf("existing content = %q, %v", content, readErr)
	}
	assertNoTransactionFiles(t, parent)
}

func createKernelProxy(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proxy")
	escapedPath, err := module.EscapePath("github.com/plystra/kernel")
	if err != nil {
		t.Fatalf("EscapePath: %v", err)
	}
	escapedVersion, err := module.EscapeVersion(newproject.KernelVersion)
	if err != nil {
		t.Fatalf("EscapeVersion: %v", err)
	}
	versionRoot := filepath.Join(root, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestFile(t, filepath.Join(versionRoot, "list"), []byte(newproject.KernelVersion+"\n"))
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".info"), []byte(fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-07-15T00:00:00Z\"}\n", newproject.KernelVersion)))
	moduleFile := []byte("module github.com/plystra/kernel\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), moduleFile)

	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatalf("Create zip: %v", err)
	}
	archive := zip.NewWriter(archiveFile)
	prefix := "github.com/plystra/kernel@" + newproject.KernelVersion + "/"
	files := []struct {
		name string
		data []byte
	}{
		{name: "assembly/version.go", data: []byte("package assembly\n\nimport \"fmt\"\n\ntype Version uint32\n\nconst V1 Version = 1\n\nfunc RequireVersion(version Version) error {\n\tif version != V1 { return fmt.Errorf(\"unsupported assembly API version %d\", version) }\n\treturn nil\n}\n")},
		{name: "go.mod", data: moduleFile},
	}
	for _, file := range files {
		header := &zip.FileHeader{Name: prefix + file.name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := writer.Write(file.data); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	return root
}

func isolatedGoEnvironment(t *testing.T, proxyRoot string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxyRoot)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	proxyURL := (&url.URL{Scheme: "file", Path: proxyPath}).String()
	overrides := map[string]string{
		"GOCACHE":     filepath.Join(t.TempDir(), "build-cache"),
		"GOENV":       "off",
		"GOFLAGS":     "-modcacherw",
		"GOMODCACHE":  filepath.Join(t.TempDir(), "module-cache"),
		"GONOPROXY":   "none",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     proxyURL,
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func assertModuleState(t *testing.T, root, modulePath string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("Parse(go.mod): %v", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != modulePath {
		t.Fatalf("module directive = %#v", parsed.Module)
	}
	if len(parsed.Require) != 1 || parsed.Require[0].Mod.Path != "github.com/plystra/kernel" || parsed.Require[0].Mod.Version != newproject.KernelVersion || parsed.Require[0].Indirect {
		t.Fatalf("requirements = %#v", parsed.Require)
	}
	if sum, err := os.ReadFile(filepath.Join(root, "go.sum")); err != nil || len(sum) == 0 {
		t.Fatalf("go.sum = %q, %v", sum, err)
	}
}

func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		result[filepath.ToSlash(name)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return result
}

func pluginScaffoldSnapshot(t *testing.T, root, pluginName string) map[string][]byte {
	t.Helper()
	tree := snapshotTree(t, root)
	result := make(map[string][]byte)
	pluginPrefix := pluginName + "/"
	generatedSuffix := "/" + pluginName + "_gen.go"
	for name, data := range tree {
		if strings.HasPrefix(name, pluginPrefix) || strings.HasPrefix(name, "generated/") && strings.HasSuffix(name, generatedSuffix) {
			result[name] = data
		}
	}
	return result
}

func writeTestFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertNoTransactionFiles(t *testing.T, parent string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(parent, ".*.plystra-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction files remain: %v", matches)
	}
}
