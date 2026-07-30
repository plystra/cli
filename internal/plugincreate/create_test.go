package plugincreate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/pluginscan"
)

var updatePluginGolden = flag.Bool("update", false, "update generated plugin scaffold golden files")

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_PLUGIN_CREATE_ROLLBACK_HELPER") == "1" {
		os.Exit(runRollbackHelper())
	}
	os.Exit(main.Run())
}

func runRollbackHelper() int {
	switch {
	case len(os.Args) == 6 && os.Args[1] == "list" && os.Args[2] == "-m" && os.Args[3] == "-json" && os.Args[4] == "-mod=readonly" && os.Args[5] == "all":
		kernelRoot := os.Getenv("PLYSTRA_TEST_KERNEL_ROOT")
		if kernelRoot == "" {
			return 13
		}
		applicationRoot, err := os.Getwd()
		if err != nil {
			return 14
		}
		encoder := json.NewEncoder(os.Stdout)
		if err := encoder.Encode(map[string]any{
			"Path":  "example.com/acme/rollback",
			"Main":  true,
			"Dir":   applicationRoot,
			"GoMod": filepath.Join(applicationRoot, "go.mod"),
		}); err != nil {
			return 15
		}
		listed := map[string]any{
			"Path":    "github.com/plystra/kernel",
			"Version": "v0.0.0",
			"Dir":     kernelRoot,
			"GoMod":   filepath.Join(kernelRoot, "go.mod"),
			"Replace": map[string]any{
				"Path":  kernelRoot,
				"Dir":   kernelRoot,
				"GoMod": filepath.Join(kernelRoot, "go.mod"),
			},
		}
		if err := encoder.Encode(listed); err != nil {
			return 16
		}
		return 0
	case len(os.Args) >= 8 && os.Args[1] == "list" && os.Args[2] == "-deps" && os.Args[3] == "-export" && os.Args[4] == "-json" && os.Args[5] == "-e" && os.Args[6] == "-mod=readonly":
		goCommand, err := exec.LookPath("go")
		if err != nil {
			return 17
		}
		command := exec.Command(goCommand, os.Args[1:]...)
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.Env = os.Environ()
		if err := command.Run(); err != nil {
			return 18
		}
		return 0
	case len(os.Args) == 3 && os.Args[1] == "mod" && os.Args[2] == "tidy":
		data, err := os.ReadFile("go.mod")
		if err != nil {
			return 10
		}
		data = append(data, []byte("\nrequire example.com/temporary v1.0.0 // indirect\n")...)
		if err := os.WriteFile("go.mod", data, 0o644); err != nil {
			return 11
		}
		if err := os.WriteFile("go.sum", []byte("temporary module metadata\n"), 0o644); err != nil {
			return 12
		}
		return 0
	case len(os.Args) == 4 && os.Args[1] == "test" && os.Args[2] == "-mod=readonly" && os.Args[3] == "./...":
		return 9
	default:
		return 8
	}
}

func TestCreateAndPublicCommandProduceDeterministicBuildablePlugins(t *testing.T) {
	const modulePath = "example.com/acme/my-app/v2"
	const name = "account-profile"
	environment := isolatedGoEnvironment(t)

	directRoot := createModule(t, modulePath)
	directStart := filepath.Join(directRoot, "docs", "work")
	if err := os.MkdirAll(directStart, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	direct, err := plugincreate.Create(context.Background(), plugincreate.Options{
		Start:       directStart,
		Name:        name,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if direct.ID() != "acme.my-app.account-profile" || direct.ModuleRoot() != directRoot || direct.Path() != filepath.Join(directRoot, name) {
		t.Fatalf("Create result = ID %q, module %q, path %q", direct.ID(), direct.ModuleRoot(), direct.Path())
	}

	commandRoot := createModule(t, modulePath)
	commandStart := filepath.Join(commandRoot, "docs", "work")
	if err := os.MkdirAll(commandStart, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"plugin", "create", name}, &stdout, &stderr, commandStart, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	wantOutput := fmt.Sprintf("created plugin acme.my-app.account-profile in %s\n", filepath.Join(commandRoot, name))
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	directTree := scaffoldSnapshot(t, directRoot)
	commandTree := scaffoldSnapshot(t, commandRoot)
	if !reflect.DeepEqual(directTree, commandTree) {
		t.Fatalf("repeated creation differed:\ndirect:  %#v\ncommand: %#v", directTree, commandTree)
	}
	wantFiles := []string{
		"account-profile/README.md",
		"account-profile/plugin.go",
		"account-profile/plugin.yaml",
		"account-profile/plugin_test.go",
		"generated/.plystra-manifest.json",
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/configuration/account-profile_gen.go",
	}
	gotFiles := make([]string, 0, len(directTree))
	for name := range directTree {
		gotFiles = append(gotFiles, name)
	}
	sort.Strings(gotFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("plugin files = %v, want %v", gotFiles, wantFiles)
	}
	golden := snapshotTree(t, "testdata/plugin")
	if *updatePluginGolden {
		writePluginGoldenTree(t, "testdata/plugin", directTree)
		golden = snapshotTree(t, "testdata/plugin")
	}
	if !reflect.DeepEqual(directTree, golden) {
		t.Fatalf("plugin scaffold differs from golden files:\n got: %#v\nwant: %#v", directTree, golden)
	}
	for path, content := range directTree {
		if bytes.Contains(content, []byte(directRoot)) || bytes.Contains(content, []byte(commandRoot)) {
			t.Fatalf("%s contains a local absolute path", path)
		}
	}
	scan, err := pluginscan.ScanRoot(directRoot)
	if err != nil || len(scan.Directories()) != 1 || scan.Directories()[0].Name() != name {
		t.Fatalf("ScanRoot = %#v, %v", scan.Directories(), err)
	}
}

func TestCreateRollsBackValidationFailure(t *testing.T) {
	t.Parallel()

	root := createModule(t, "example.com/acme/my-app")
	before := snapshotTree(t, root)
	_, err := plugincreate.Create(context.Background(), plugincreate.Options{
		Start:     root,
		Name:      "account",
		GoCommand: filepath.Join(root, "missing-go-command"),
	})
	if !errors.Is(err, plugincreate.ErrCreate) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v, want ErrCreate and ErrRun", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("tree changed after rollback:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoTransactionFiles(t, root)
}

func TestCreateRejectsUnexpectedGeneratedOutputWithoutMutation(t *testing.T) {
	t.Parallel()

	root := createModule(t, "example.com/acme/my-app")
	if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatalf("create generated directory: %v", err)
	}
	writeFile(t, filepath.Join(root, "generated", "manual.txt"), "user-owned")
	before := snapshotTree(t, root)
	_, err := plugincreate.Create(t.Context(), plugincreate.Options{
		Start:       root,
		Name:        "account",
		Environment: isolatedGoEnvironment(t),
	})
	if !errors.Is(err, plugincreate.ErrCreate) || !errors.Is(err, generatedfiles.ErrUnexpected) || !strings.Contains(err.Error(), "generated/manual.txt") {
		t.Fatalf("Create unexpected-output error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unexpected-output failure changed module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoTransactionFiles(t, root)
}

func TestCreateTidiesModuleForGeneratedConfiguration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	catalogRoot := filepath.Join(t.TempDir(), "catalog")
	if err := os.MkdirAll(catalogRoot, 0o755); err != nil {
		t.Fatalf("create catalog root: %v", err)
	}
	writeFile(t, filepath.Join(catalogRoot, "go.mod"), "module example.com/catalog\n\ngo 1.26\n")
	goMod := fmt.Sprintf(`module example.com/acme/untidy

go 1.26

require (
	example.com/catalog v0.0.0
	github.com/plystra/kernel v0.0.0
)

replace example.com/catalog => %s

replace github.com/plystra/kernel => %s
	`, filepath.ToSlash(catalogRoot), filepath.ToSlash(kernelRoot))
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	retainedSum := "golang.org/x/mod v0.38.0 h1:MECBjubtXD7yj4HrhIUcywNaGeNVUdfVnxmPajOk4yk=\n"
	writeFile(t, filepath.Join(root, "go.sum"), retainedSum)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")

	result, err := plugincreate.Create(t.Context(), plugincreate.Options{
		Start:       root,
		Name:        "account",
		Environment: isolatedGoEnvironment(t),
	})
	if err != nil {
		t.Fatalf("Create in untidy module: %v", err)
	}
	if result.ID() != "acme.untidy.account" {
		t.Fatalf("created Plugin ID = %q", result.ID())
	}
	normalizedMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read normalized go.mod: %v", err)
	}
	for _, requirement := range [][]byte{
		[]byte("example.com/catalog v0.0.0"),
		[]byte("go.yaml.in/yaml/v3 v3.0.4"),
	} {
		if !bytes.Contains(normalizedMod, requirement) {
			t.Fatalf("normalized go.mod omits %q:\n%s", requirement, normalizedMod)
		}
	}
	if sum, err := os.ReadFile(filepath.Join(root, "go.sum")); err != nil || !bytes.Contains(sum, []byte(retainedSum)) {
		t.Fatalf("normalized go.sum = %q, %v", sum, err)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: isolatedGoEnvironment(t),
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("generated Project check = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestCreateRestoresModuleMetadataWhenGeneratedValidationFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/rollback

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := snapshotTree(t, root)
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	environment := append(os.Environ(), "PLYSTRA_PLUGIN_CREATE_ROLLBACK_HELPER=1", "PLYSTRA_TEST_KERNEL_ROOT="+kernelRoot)
	_, err = plugincreate.Create(t.Context(), plugincreate.Options{
		Start:       root,
		Name:        "account",
		GoCommand:   command,
		Environment: environment,
	})
	if !errors.Is(err, plugincreate.ErrCreate) || !errors.Is(err, plugincreate.ErrModuleTidy) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v, want create, module-tidy, and Go command errors", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("tree changed after module-metadata rollback:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoTransactionFiles(t, root)
}

func TestCreateRegeneratesProjectAssembly(t *testing.T) {
	t.Parallel()

	root := createModule(t, "example.com/acme/my-app")
	environment := isolatedGoEnvironment(t)
	result, err := plugincreate.Create(t.Context(), plugincreate.Options{
		Start:       root,
		Name:        "account",
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create Project plugin: %v", err)
	}
	if result.ID() != "acme.my-app.account" {
		t.Fatalf("created Plugin ID = %q", result.ID())
	}
	providers, err := os.ReadFile(filepath.Join(root, "generated", "go", "assembly", "providers_gen.go"))
	if err != nil {
		t.Fatalf("read generated providers: %v", err)
	}
	for _, required := range [][]byte{[]byte(`"example.com/acme/my-app/account"`), []byte("DecodeAccount"), []byte("provider0.New(configuration)")} {
		if !bytes.Contains(providers, required) {
			t.Fatalf("generated providers omit %q:\n%s", required, providers)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "generated", "assembly")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("obsolete generated/assembly remains: %v", err)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("generated application check = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestCreatePreservesExistingDirectoriesAndGeneratedTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "plugin directory",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "account"), 0o755); err != nil {
					t.Fatalf("Mkdir: %v", err)
				}
				writeFile(t, filepath.Join(root, "account", "keep.txt"), "keep")
			},
		},
		{
			name: "generated target",
			setup: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "generated", "go", "configuration", "account_gen.go")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				writeFile(t, path, "keep")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := createModule(t, "example.com/acme/my-app")
			test.setup(t, root)
			before := snapshotTree(t, root)
			_, err := plugincreate.Create(context.Background(), plugincreate.Options{Start: root, Name: "account"})
			if !errors.Is(err, plugincreate.ErrCreate) {
				t.Fatalf("Create error = %v, want ErrCreate", err)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("existing tree changed:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoTransactionFiles(t, root)
		})
	}
}

func TestCreateRejectsInvalidNamesBeforeFilesystemInspection(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Account", "account--profile", "account-", "account_profile", "generated", ".hidden", strings.Repeat("a", 65)} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := plugincreate.Create(context.Background(), plugincreate.Options{Start: "missing", Name: name})
			if !errors.Is(err, plugincreate.ErrInvalidName) {
				t.Fatalf("Create(%q) error = %v, want ErrInvalidName", name, err)
			}
		})
	}
}

func TestCreateRejectsOrdinaryGoModuleWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/ordinary\n\ngo 1.26\n")
	before := snapshotTree(t, root)
	_, err := plugincreate.Create(t.Context(), plugincreate.Options{Start: root, Name: "account"})
	if !errors.Is(err, plugincreate.ErrCreate) || !strings.Contains(err.Error(), "has no root plystra.yaml") {
		t.Fatalf("Create error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("ordinary module changed:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func createModule(t *testing.T, modulePath string) string {
	t.Helper()
	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module %s

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, modulePath, filepath.ToSlash(kernelRoot))
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), goSum, 0o644); err != nil {
		t.Fatalf("write go.sum: %v", err)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return canonical
}

func isolatedGoEnvironment(t *testing.T) []string {
	t.Helper()
	overrides := map[string]string{
		"GOCACHE":     filepath.Join(t.TempDir(), "build-cache"),
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOPROXY":     "off",
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

func scaffoldSnapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	tree := snapshotTree(t, root)
	result := make(map[string][]byte)
	for name, data := range tree {
		if strings.HasPrefix(name, "account-profile/") || name == "generated/.plystra-manifest.json" || name == "generated/go/application/main_gen.go" || name == "generated/go/assembly/compatibility_gen.go" || name == "generated/go/configuration/account-profile_gen.go" {
			result[name] = data
		}
	}
	return result
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

func writePluginGoldenTree(t *testing.T, root string, tree map[string][]byte) {
	t.Helper()
	for name, data := range tree {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertNoTransactionFiles(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction files remain: %v", matches)
	}
}
