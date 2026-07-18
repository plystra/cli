package moduledependency_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"golang.org/x/mod/module"
)

func TestMain(main *testing.M) {
	if mode := os.Getenv("PLYSTRA_MODULE_DEPENDENCY_HELPER"); mode != "" {
		os.Exit(runHelper(mode))
	}
	os.Exit(main.Run())
}

func TestDiscoverUsesSortedEffectiveGraphAndRecognizesProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	aRoot := filepath.Join(root, "a")
	ordinaryRoot := filepath.Join(root, "ordinary")
	transitiveRoot := filepath.Join(root, "transitive")
	zRoot := filepath.Join(root, "z")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, aRoot, "example.com/a")
	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writeModule(t, transitiveRoot, "example.com/transitive")
	writeModule(t, zRoot, "example.com/z")
	for _, projectRoot := range []string{aRoot, transitiveRoot, zRoot} {
		writeFile(t, filepath.Join(projectRoot, "plystra.yaml"), "{}\n")
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire (\n\texample.com/z v1.4.0 // indirect\n\texample.com/a v1.2.0\n)\n")

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{
		GoCommand: os.Args[0],
		Environment: append(os.Environ(),
			"PLYSTRA_MODULE_DEPENDENCY_HELPER=valid",
			"PLYSTRA_MODULE_APP_ROOT="+appRoot,
			"PLYSTRA_MODULE_A_ROOT="+aRoot,
			"PLYSTRA_MODULE_ORDINARY_ROOT="+ordinaryRoot,
			"PLYSTRA_MODULE_TRANSITIVE_ROOT="+transitiveRoot,
			"PLYSTRA_MODULE_Z_ROOT="+zRoot,
		),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 4 || modules[0].Path() != "example.com/a" || modules[1].Path() != "example.com/ordinary" || modules[2].Path() != "example.com/transitive" || modules[3].Path() != "example.com/z" {
		t.Fatalf("Modules() = %#v", modules)
	}
	if modules[0].RequiredVersion() != "v1.2.0" || modules[0].SelectedVersion() != "v1.3.0" || !modules[0].Direct() || modules[0].Indirect() || modules[0].Workspace() || !modules[0].Project() {
		t.Fatalf("a module provenance = %#v", modules[0])
	}
	if modules[1].RequiredVersion() != "" || modules[1].Direct() || modules[1].Project() {
		t.Fatalf("ordinary transitive module provenance = %#v", modules[1])
	}
	if modules[2].RequiredVersion() != "" || modules[2].Direct() || !modules[2].Project() {
		t.Fatalf("transitive Project provenance = %#v", modules[2])
	}
	if modules[3].RequiredVersion() != "v1.4.0" || modules[3].SelectedVersion() != "v1.5.0" || !modules[3].Direct() || !modules[3].Indirect() || modules[3].Workspace() || !modules[3].Project() {
		t.Fatalf("z module provenance = %#v", modules[3])
	}
	if _, ok := modules[0].Replacement(); ok {
		t.Fatal("a module unexpectedly has replacement provenance")
	}
	if byPath, ok := index.ByPath("example.com/z"); !ok || byPath.Root() != canonicalPath(t, zRoot) {
		t.Fatalf("ByPath(z) = %#v, %t", byPath, ok)
	}
	if _, ok := index.ByPath("example.com/missing"); ok {
		t.Fatal("ByPath(missing) succeeded")
	}
	projects := index.Projects()
	if len(projects) != 3 || projects[0].Path() != "example.com/a" || projects[1].Path() != "example.com/transitive" || projects[2].Path() != "example.com/z" {
		t.Fatalf("Projects() = %#v", projects)
	}
	projects[0] = moduledependency.Module{}
	if index.Projects()[0].Path() != "example.com/a" {
		t.Fatal("Projects exposed mutable index storage")
	}
	modules[0] = moduledependency.Module{}
	if index.Modules()[0].Path() != "example.com/a" {
		t.Fatal("Modules exposed mutable index storage")
	}
}

func TestDiscoverAcceptsLocalReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	pluginRoot := filepath.Join(root, "plugin")
	writeModule(t, pluginRoot, "example.com/plugin")
	writeFile(t, filepath.Join(pluginRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(pluginRoot, "smtp", "plugin.yaml"), "id: example.smtp\n")
	goMod := "module example.com/app\n\ngo 1.26\n\nrequire example.com/plugin v1.2.3\n\nreplace example.com/plugin => ../plugin\n"
	writeFile(t, filepath.Join(appRoot, "go.mod"), goMod)

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: goEnvironment(map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || modules[0].Path() != "example.com/plugin" || modules[0].SelectedVersion() != "v1.2.3" || modules[0].Root() != canonicalPath(t, pluginRoot) || !modules[0].Direct() || !modules[0].Project() || len(index.Projects()) != 1 {
		t.Fatalf("Modules() = %#v", modules)
	}
	replacement, ok := modules[0].Replacement()
	if !ok || !replacement.Local() || replacement.Path() != "../plugin" || replacement.Version() != "" {
		t.Fatalf("Replacement() = %#v, %t", replacement, ok)
	}
	if data, err := os.ReadFile(filepath.Join(appRoot, "go.mod")); err != nil || string(data) != goMod {
		t.Fatalf("go.mod changed to %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "go.sum")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("go.sum was created: %v", err)
	}
}

func TestDiscoverAcceptsSyntheticGoModForOrdinaryLegacyModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	sourceRoot := filepath.Join(root, "legacy-source")
	metadataRoot := filepath.Join(root, "metadata")
	writeFile(t, filepath.Join(sourceRoot, "legacy.go"), "package legacy\n")
	writeModule(t, metadataRoot, "example.com/legacy")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/legacy v1.2.3\n")

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{
		GoCommand: os.Args[0],
		Environment: append(os.Environ(),
			"PLYSTRA_MODULE_DEPENDENCY_HELPER=synthetic",
			"PLYSTRA_MODULE_APP_ROOT="+appRoot,
			"PLYSTRA_MODULE_SOURCE_ROOT="+sourceRoot,
			"PLYSTRA_MODULE_METADATA_ROOT="+metadataRoot,
		),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || modules[0].Path() != "example.com/legacy" || modules[0].Root() != canonicalPath(t, sourceRoot) || !modules[0].Direct() || modules[0].Project() || len(index.Projects()) != 0 {
		t.Fatalf("Modules() = %#v, Projects() = %#v", modules, index.Projects())
	}
}

func TestDiscoverAcceptsImplicitGoWorkspaceModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	pluginRoot := filepath.Join(root, "plugin")
	writeModule(t, pluginRoot, "example.com/plugin")
	writeFile(t, filepath.Join(pluginRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	goWork := filepath.Join(root, "go.work")
	writeFile(t, goWork, "go 1.26\n\nuse (\n\t./app\n\t./plugin\n)\n")

	environment := goEnvironment(map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
	})
	environment = withoutEnvironmentKey(environment, "GOWORK")
	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || !modules[0].Workspace() || modules[0].SelectedVersion() != "" || modules[0].Root() != canonicalPath(t, pluginRoot) || modules[0].Direct() || !modules[0].Project() {
		t.Fatalf("Modules() = %#v", modules)
	}
	if _, ok := modules[0].Replacement(); ok {
		t.Fatal("workspace module unexpectedly has replacement provenance")
	}
}

func TestDiscoverAcceptsDownloadedSelectedVersionWithoutMutation(t *testing.T) {
	t.Parallel()

	const (
		modulePath = "example.com/plugin"
		version    = "v1.2.3"
	)
	root := t.TempDir()
	proxyRoot := writeModuleProxy(t, root, modulePath, version)
	appRoot := filepath.Join(root, "app")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire "+modulePath+" "+version+"\n")
	environment := isolatedGoEnvironment(t, proxyRoot)
	runGo(t, appRoot, environment, "mod", "download", modulePath+"@"+version)
	goModBefore := readFile(t, filepath.Join(appRoot, "go.mod"))
	goSumBefore := readFile(t, filepath.Join(appRoot, "go.sum"))

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || modules[0].Path() != modulePath || modules[0].RequiredVersion() != version || modules[0].SelectedVersion() != version || modules[0].Workspace() || !modules[0].Direct() || !modules[0].Project() || len(index.Projects()) != 1 {
		t.Fatalf("Modules() = %#v", modules)
	}
	if _, ok := modules[0].Replacement(); ok {
		t.Fatal("downloaded module unexpectedly has replacement provenance")
	}
	if _, err := os.Stat(filepath.Join(modules[0].Root(), "smtp", "plugin.yaml")); err != nil {
		t.Fatalf("selected module root is not inspectable: %v", err)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.mod")); !bytes.Equal(got, goModBefore) {
		t.Fatalf("Discover changed go.mod from %q to %q", goModBefore, got)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.sum")); !bytes.Equal(got, goSumBefore) {
		t.Fatalf("Discover changed go.sum from %q to %q", goSumBefore, got)
	}
}

func TestDiscoverMaterializesMissingSelectedSourceWithoutProjectMutation(t *testing.T) {
	t.Parallel()

	const (
		modulePath = "example.com/plugin"
		version    = "v1.2.3"
	)
	root := t.TempDir()
	proxyRoot := writeModuleProxy(t, root, modulePath, version)
	appRoot := filepath.Join(root, "app")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire "+modulePath+" "+version+"\n")
	environment := isolatedGoEnvironment(t, proxyRoot)
	runGo(t, appRoot, environment, "mod", "download", modulePath+"@"+version)

	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		t.Fatalf("EscapePath: %v", err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatalf("EscapeVersion: %v", err)
	}
	extractedRoot := filepath.Join(environmentValue(t, environment, "GOMODCACHE"), filepath.FromSlash(escapedPath)+"@"+escapedVersion)
	if err := os.RemoveAll(extractedRoot); err != nil {
		t.Fatalf("RemoveAll(extracted source): %v", err)
	}
	if _, err := os.Stat(extractedRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source remained before discovery: %v", err)
	}
	goModBefore := readFile(t, filepath.Join(appRoot, "go.mod"))
	goSumBefore := readFile(t, filepath.Join(appRoot, "go.sum"))

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	projects := index.Projects()
	if len(projects) != 1 || projects[0].Path() != modulePath || projects[0].Root() != canonicalPath(t, extractedRoot) {
		t.Fatalf("Projects() = %#v", projects)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.mod")); !bytes.Equal(got, goModBefore) {
		t.Fatalf("Discover changed go.mod from %q to %q", goModBefore, got)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.sum")); !bytes.Equal(got, goSumBefore) {
		t.Fatalf("Discover changed go.sum from %q to %q", goSumBefore, got)
	}
}

func TestDiscoverMaterializesVersionedReplacementSource(t *testing.T) {
	t.Parallel()

	const (
		modulePath         = "example.com/original"
		requiredVersion    = "v1.2.3"
		replacementPath    = "example.com/replacement"
		replacementVersion = "v1.4.0"
	)
	root := t.TempDir()
	proxyRoot := writeModuleProxy(t, root, replacementPath, replacementVersion)
	appRoot := filepath.Join(root, "app")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire "+modulePath+" "+requiredVersion+"\n\nreplace "+modulePath+" "+requiredVersion+" => "+replacementPath+" "+replacementVersion+"\n")
	environment := isolatedGoEnvironment(t, proxyRoot)
	runGo(t, appRoot, environment, "mod", "download", replacementPath+"@"+replacementVersion)

	escapedPath, err := module.EscapePath(replacementPath)
	if err != nil {
		t.Fatalf("EscapePath: %v", err)
	}
	escapedVersion, err := module.EscapeVersion(replacementVersion)
	if err != nil {
		t.Fatalf("EscapeVersion: %v", err)
	}
	extractedRoot := filepath.Join(environmentValue(t, environment, "GOMODCACHE"), filepath.FromSlash(escapedPath)+"@"+escapedVersion)
	if err := os.RemoveAll(extractedRoot); err != nil {
		t.Fatalf("RemoveAll(extracted source): %v", err)
	}
	goModBefore := readFile(t, filepath.Join(appRoot, "go.mod"))
	goSumBefore := readFile(t, filepath.Join(appRoot, "go.sum"))

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || modules[0].Path() != modulePath || modules[0].SelectedVersion() != requiredVersion || modules[0].Root() != canonicalPath(t, extractedRoot) || !modules[0].Project() {
		t.Fatalf("Modules() = %#v", modules)
	}
	replacement, exists := modules[0].Replacement()
	if !exists || replacement.Path() != replacementPath || replacement.Version() != replacementVersion || replacement.Local() {
		t.Fatalf("Replacement() = %#v, %t", replacement, exists)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.mod")); !bytes.Equal(got, goModBefore) {
		t.Fatalf("Discover changed go.mod from %q to %q", goModBefore, got)
	}
	if got := readFile(t, filepath.Join(appRoot, "go.sum")); !bytes.Equal(got, goSumBefore) {
		t.Fatalf("Discover changed go.sum from %q to %q", goSumBefore, got)
	}
}

func TestDiscoverRecognizesWorkspaceProjectWithoutDirectRequirement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	workspaceRoot := filepath.Join(root, "workspace-project")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, workspaceRoot, "example.com/workspace-project")
	writeFile(t, filepath.Join(workspaceRoot, "plystra.yaml"), "{}\n")
	goWork := filepath.Join(root, "go.work")
	writeFile(t, goWork, "go 1.26\n\nuse (\n\t./app\n\t./workspace-project\n)\n")
	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: goEnvironment(map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      goWork,
	})})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != 1 || modules[0].Path() != "example.com/workspace-project" || modules[0].Direct() || !modules[0].Workspace() || !modules[0].Project() {
		t.Fatalf("Modules() = %#v", modules)
	}
}

func TestDiscoverHandlesLargeEffectiveGraphWithOneBoundedQuery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	sourceRoot := filepath.Join(root, "source")
	writeModule(t, sourceRoot, "example.com/source")
	writeFile(t, filepath.Join(sourceRoot, "plystra.yaml"), "{}\n")
	var goMod strings.Builder
	goMod.WriteString("module example.com/app\n\ngo 1.26\n\nrequire (\n")
	const count = 200
	for index := 0; index < count; index++ {
		_, _ = fmt.Fprintf(&goMod, "\texample.com/%03d/%s v1.0.0\n", index, strings.Repeat("segment", 12))
	}
	goMod.WriteString(")\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), goMod.String())

	index, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{
		GoCommand: os.Args[0],
		Environment: append(os.Environ(),
			"PLYSTRA_MODULE_DEPENDENCY_HELPER=batch",
			"PLYSTRA_MODULE_APP_ROOT="+appRoot,
			"PLYSTRA_MODULE_SOURCE_ROOT="+sourceRoot,
			"PLYSTRA_MODULE_COUNT=200",
		),
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	modules := index.Modules()
	if len(modules) != count {
		t.Fatalf("Modules() contains %d dependencies, want %d", len(modules), count)
	}
	if modules[0].Path() != "example.com/000/"+strings.Repeat("segment", 12) || modules[count-1].Path() != "example.com/199/"+strings.Repeat("segment", 12) {
		t.Fatalf("Modules() contains %d dependencies from %q through %q", len(modules), modules[0].Path(), modules[len(modules)-1].Path())
	}
	if len(index.Projects()) != count {
		t.Fatalf("Projects() contains %d dependencies, want %d", len(index.Projects()), count)
	}
}

func TestDiscoverRejectsInvalidGoListOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	sourceRoot := filepath.Join(root, "source")
	writeModule(t, sourceRoot, "example.com/plugin")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/plugin v1.2.3\n")
	application := locate(t, appRoot)
	tests := []struct {
		mode string
		want error
	}{
		{mode: "missing", want: moduledependency.ErrInvalidOutput},
		{mode: "missing-main", want: moduledependency.ErrInvalidOutput},
		{mode: "duplicate", want: moduledependency.ErrInvalidOutput},
		{mode: "malformed", want: moduledependency.ErrInvalidOutput},
		{mode: "older", want: moduledependency.ErrInvalidOutput},
		{mode: "unavailable", want: moduledependency.ErrModuleUnavailable},
		{mode: "oversized", want: gocommand.ErrOutputTooLarge},
	}
	for _, test := range tests {
		test := test
		t.Run(test.mode, func(t *testing.T) {
			t.Parallel()
			_, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{
				GoCommand: os.Args[0],
				Environment: append(os.Environ(),
					"PLYSTRA_MODULE_DEPENDENCY_HELPER="+test.mode,
					"PLYSTRA_MODULE_APP_ROOT="+appRoot,
					"PLYSTRA_MODULE_SOURCE_ROOT="+sourceRoot,
				),
				OutputLimit: 1024,
			})
			if !errors.Is(err, moduledependency.ErrDiscover) || !errors.Is(err, test.want) {
				t.Fatalf("Discover error = %v, want ErrDiscover and %v", err, test.want)
			}
		})
	}
}

func TestDiscoverRejectsUnsafeDependencyProjectMarker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "dependency")
	writeModule(t, dependencyRoot, "example.com/dependency")
	if err := os.Mkdir(filepath.Join(dependencyRoot, "plystra.yaml"), 0o755); err != nil {
		t.Fatalf("Mkdir(plystra.yaml): %v", err)
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.0.0\n\nreplace example.com/dependency => ../dependency\n")
	_, err := moduledependency.Discover(context.Background(), locate(t, appRoot), moduledependency.Options{Environment: goEnvironment(map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})})
	if !errors.Is(err, moduledependency.ErrDiscover) || !strings.Contains(err.Error(), "regular non-symbolic") {
		t.Fatalf("Discover unsafe marker error = %v", err)
	}
}

func runHelper(mode string) int {
	want := []string{"list", "-m", "-json", "-mod=readonly", "all"}
	switch mode {
	case "valid", "batch", "synthetic", "missing", "missing-main", "duplicate", "malformed", "older", "unavailable", "oversized":
	default:
		return 9
	}
	if len(os.Args) != len(want)+1 {
		return 10
	}
	for index := range want {
		if os.Args[index+1] != want[index] {
			return 11
		}
	}
	switch mode {
	case "valid":
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		for _, value := range []struct {
			Path    string
			Version string
			Root    string
		}{
			{Path: "example.com/z", Version: "v1.5.0", Root: os.Getenv("PLYSTRA_MODULE_Z_ROOT")},
			{Path: "example.com/ordinary", Version: "v0.9.0", Root: os.Getenv("PLYSTRA_MODULE_ORDINARY_ROOT")},
			{Path: "example.com/a", Version: "v1.3.0", Root: os.Getenv("PLYSTRA_MODULE_A_ROOT")},
			{Path: "example.com/transitive", Version: "v1.8.0", Root: os.Getenv("PLYSTRA_MODULE_TRANSITIVE_ROOT")},
		} {
			if err := encoder.Encode(map[string]any{
				"Path":    value.Path,
				"Version": value.Version,
				"Dir":     value.Root,
				"GoMod":   filepath.Join(value.Root, "go.mod"),
			}); err != nil {
				return 12
			}
		}
	case "batch":
		root := os.Getenv("PLYSTRA_MODULE_SOURCE_ROOT")
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		count, err := strconv.Atoi(os.Getenv("PLYSTRA_MODULE_COUNT"))
		if err != nil || count <= 0 {
			return 13
		}
		for index := 0; index < count; index++ {
			modulePath := fmt.Sprintf("example.com/%03d/%s", index, strings.Repeat("segment", 12))
			if err := encoder.Encode(map[string]any{
				"Path":    modulePath,
				"Version": "v1.0.0",
				"Dir":     root,
				"GoMod":   filepath.Join(root, "go.mod"),
				"Replace": map[string]any{
					"Path":  "../source",
					"Dir":   root,
					"GoMod": filepath.Join(root, "go.mod"),
				},
			}); err != nil {
				return 12
			}
		}
	case "synthetic":
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		root := os.Getenv("PLYSTRA_MODULE_SOURCE_ROOT")
		metadataRoot := os.Getenv("PLYSTRA_MODULE_METADATA_ROOT")
		if err := encoder.Encode(map[string]any{
			"Path":    "example.com/legacy",
			"Version": "v1.2.3",
			"Dir":     root,
			"GoMod":   filepath.Join(metadataRoot, "go.mod"),
		}); err != nil {
			return 12
		}
	case "missing":
		return encodeMainExitCode()
	case "missing-main":
		root := os.Getenv("PLYSTRA_MODULE_SOURCE_ROOT")
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"Path": "example.com/plugin", "Version": "v1.2.3", "Dir": root, "GoMod": filepath.Join(root, "go.mod")})
	case "duplicate":
		root := os.Getenv("PLYSTRA_MODULE_SOURCE_ROOT")
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		_ = encoder.Encode(map[string]any{"Path": "example.com/plugin", "Version": "v1.2.3", "Dir": root, "GoMod": filepath.Join(root, "go.mod")})
		_ = encoder.Encode(map[string]any{"Path": "example.com/plugin", "Version": "v1.2.3", "Dir": root, "GoMod": filepath.Join(root, "go.mod")})
	case "malformed":
		_, _ = fmt.Fprint(os.Stdout, "{")
	case "older":
		root := os.Getenv("PLYSTRA_MODULE_SOURCE_ROOT")
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		_ = encoder.Encode(map[string]any{"Path": "example.com/plugin", "Version": "v1.0.0", "Dir": root, "GoMod": filepath.Join(root, "go.mod")})
	case "unavailable":
		encoder := json.NewEncoder(os.Stdout)
		if err := encodeMainModule(encoder); err != nil {
			return 12
		}
		_ = encoder.Encode(map[string]any{"Path": "example.com/plugin", "Version": "v1.2.3"})
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1025))
	}
	return 0
}

func encodeMainModule(encoder *json.Encoder) error {
	root := os.Getenv("PLYSTRA_MODULE_APP_ROOT")
	return encoder.Encode(map[string]any{
		"Path":  "example.com/app",
		"Main":  true,
		"Dir":   root,
		"GoMod": filepath.Join(root, "go.mod"),
	})
}

func encodeMainExitCode() int {
	if err := encodeMainModule(json.NewEncoder(os.Stdout)); err != nil {
		return 12
	}
	return 0
}

func writeModule(t *testing.T, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writeModuleProxy(t *testing.T, root, modulePath, version string) string {
	t.Helper()
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		t.Fatalf("EscapePath: %v", err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatalf("EscapeVersion: %v", err)
	}
	proxyRoot := filepath.Join(root, "proxy")
	versionRoot := filepath.Join(proxyRoot, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(proxy): %v", err)
	}
	writeFile(t, filepath.Join(versionRoot, "list"), version+"\n")
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-07-17T00:00:00Z\"}\n", version))
	goMod := []byte("module " + modulePath + "\n\ngo 1.26\n")
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), string(goMod))

	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatalf("Create(zip): %v", err)
	}
	archive := zip.NewWriter(archiveFile)
	prefix := modulePath + "@" + version + "/"
	for _, file := range []struct {
		name string
		data []byte
	}{
		{name: "go.mod", data: goMod},
		{name: "plystra.yaml", data: []byte("{}\n")},
		{name: "smtp/plugin.yaml", data: []byte("id: example.smtp\n")},
	} {
		header := &zip.FileHeader{Name: prefix + file.name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := writer.Write(file.data); err != nil {
			t.Fatalf("Write(zip): %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("Close(zip): %v", err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatalf("Close(zip file): %v", err)
	}
	return proxyRoot
}

func isolatedGoEnvironment(t *testing.T, proxyRoot string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxyRoot)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	return goEnvironment(map[string]string{
		"GOCACHE":     filepath.Join(t.TempDir(), "build-cache"),
		"GOENV":       "off",
		"GOFLAGS":     "-modcacherw",
		"GOMODCACHE":  filepath.Join(t.TempDir(), "module-cache"),
		"GONOPROXY":   "none",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     (&url.URL{Scheme: "file", Path: proxyPath}).String(),
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
}

func goEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func withoutEnvironmentKey(environment []string, unwanted string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, unwanted) {
			result = append(result, entry)
		}
	}
	return result
}

func environmentValue(t *testing.T, environment []string, wanted string) string {
	t.Helper()
	for _, entry := range environment {
		key, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, wanted) {
			return value
		}
	}
	t.Fatalf("environment has no %s", wanted)
	return ""
}

func runGo(t *testing.T, root string, environment []string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func locate(t *testing.T, root string) modulelocate.Module {
	t.Helper()
	located, err := modulelocate.Find(root)
	if err != nil {
		t.Fatalf("modulelocate.Find: %v", err)
	}
	return located
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func canonicalPath(t *testing.T, name string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(name)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", name, err)
	}
	return canonical
}
