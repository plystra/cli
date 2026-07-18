package plugininventory_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestBuildIndexesLocalAndDependencyProjectPlugins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	aRoot := filepath.Join(root, "dependency-a")
	zRoot := filepath.Join(root, "dependency-z")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, aRoot, "example.com/a")
	writeModule(t, zRoot, "example.com/z")
	writePlugin(t, appRoot, "orders", "id: acme.orders\n")
	manifest := []byte("id: acme.smtp\nprovides: [email.send/v1]\nrequires: [audit.write/v1]\nconfig: {password: {type: secret, required: true}}\ngeneration: {api: v1, package: ./generation, activations: [{namespace: email, capability: email.send/v1}]}\n")
	writePlugin(t, aRoot, "smtp", string(manifest))
	if err := os.MkdirAll(filepath.Join(aRoot, "smtp", "generation"), 0o755); err != nil {
		t.Fatalf("MkdirAll(generation): %v", err)
	}
	writePlugin(t, zRoot, "profile", "id: acme.profile\nprovides: [customer.profile.get/v1]\n")
	application, dependencies := configureApplication(t, appRoot,
		dependency{path: "example.com/z", version: "v1.4.0", root: zRoot},
		dependency{path: "example.com/a", version: "v1.2.0", root: aRoot},
	)

	index, err := plugininventory.Build(application, dependencies)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	plugins := index.Plugins()
	if got := pluginIDs(plugins); !reflect.DeepEqual(got, []string{"acme.orders", "acme.profile", "acme.smtp"}) {
		t.Fatalf("Plugins IDs = %v", got)
	}
	orders := plugins[0]
	if !orders.Local() || orders.ModulePath() != "example.com/app" || orders.ModuleVersion() != "" || orders.ImportPath() != "example.com/app/orders" || orders.Source() != "example.com/app@local/orders/plugin.yaml" {
		t.Fatalf("orders provenance = %#v", orders)
	}
	profile := plugins[1]
	if profile.Local() || profile.ModulePath() != "example.com/z" || profile.ModuleVersion() != "v1.4.0" || profile.ImportPath() != "example.com/z/profile" || profile.Source() != "example.com/z@v1.4.0/profile/plugin.yaml" {
		t.Fatalf("profile provenance = %#v", profile)
	}
	smtp := plugins[2]
	if smtp.Local() || smtp.Name() != "smtp" || smtp.Path() != "smtp" || smtp.ModulePath() != "example.com/a" || smtp.ModuleVersion() != "v1.2.0" || smtp.ModuleRoot() != canonicalPath(t, aRoot) || smtp.PluginRoot() != filepath.Join(canonicalPath(t, aRoot), "smtp") {
		t.Fatalf("smtp provenance = %#v", smtp)
	}
	if got := identifierStrings(smtp.Provides()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("smtp Provides() = %v", got)
	}
	if got := identifierStrings(smtp.Requires()); !reflect.DeepEqual(got, []string{"audit.write/v1"}) {
		t.Fatalf("smtp Requires() = %v", got)
	}
	password, ok := smtp.Config().Lookup("password")
	if !ok || password.Type() != kernelmanifest.ConfigSecret || !password.Required() {
		t.Fatalf("smtp Config password = %#v, %t", password, ok)
	}
	generation, ok := smtp.Generation()
	if !ok || generation.API() != "v1" || generation.Package() != "./generation" {
		t.Fatalf("smtp Generation() = %#v, %t", generation, ok)
	}
	if packagePath, ok := smtp.GenerationPackagePath(); !ok || packagePath != "smtp/generation" {
		t.Fatalf("smtp GenerationPackagePath() = %q, %t", packagePath, ok)
	}
	if got := smtp.ManifestData(); !bytes.Equal(got, manifest) {
		t.Fatalf("smtp ManifestData() = %q", got)
	}
	if byID, ok := index.ByID("acme.smtp"); !ok || byID.Source() != smtp.Source() {
		t.Fatalf("ByID(acme.smtp) = %#v, %t", byID, ok)
	}
	if _, ok := index.ByID("acme.missing"); ok {
		t.Fatal("ByID(missing) succeeded")
	}

	plugins[0] = plugininventory.Plugin{}
	if index.Plugins()[0].ID() != "acme.orders" {
		t.Fatal("Plugins exposed mutable index storage")
	}
	provided := smtp.Provides()
	provided[0] = capabilityid.Identifier{}
	if smtp.Provides()[0].String() != "email.send/v1" {
		t.Fatal("Provides exposed mutable declaration storage")
	}
	manifestCopy := smtp.ManifestData()
	manifestCopy[0] = 'x'
	if !bytes.Equal(smtp.ManifestData(), manifest) {
		t.Fatal("Plugin accessors exposed mutable declaration storage")
	}
}

func TestBuildRejectsDuplicateIDsAcrossModules(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "dependency")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, dependencyRoot, "example.com/dependency")
	writePlugin(t, appRoot, "local", "id: acme.shared\n")
	writePlugin(t, dependencyRoot, "remote", "id: acme.shared\n")
	application, dependencies := configureApplication(t, appRoot, dependency{path: "example.com/dependency", version: "v1.0.0", root: dependencyRoot})

	index, err := plugininventory.Build(application, dependencies)
	if !errors.Is(err, plugininventory.ErrBuild) || !errors.Is(err, plugininventory.ErrDuplicateID) || len(index.Plugins()) != 0 {
		t.Fatalf("Build = %#v, %v; want duplicate failure", index.Plugins(), err)
	}
	var duplicate *plugininventory.DuplicateIDError
	if !errors.As(err, &duplicate) || duplicate.ID() != "acme.shared" {
		t.Fatalf("Build error = %v, want typed acme.shared duplicate", err)
	}
	wantSources := []string{
		"example.com/app@local/local/plugin.yaml",
		"example.com/dependency@v1.0.0/remote/plugin.yaml",
	}
	if !reflect.DeepEqual(duplicate.Sources(), wantSources) {
		t.Fatalf("duplicate Sources() = %v, want %v", duplicate.Sources(), wantSources)
	}
	returned := duplicate.Sources()
	returned[0] = "changed"
	if reflect.DeepEqual(returned, duplicate.Sources()) {
		t.Fatal("DuplicateIDError exposed mutable source storage")
	}
}

func TestBuildAllowsModulesWithoutPlugins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "dependency")
	writeModule(t, appRoot, "example.com/app")
	writeModule(t, dependencyRoot, "example.com/dependency")
	application, dependencies := configureApplication(t, appRoot, dependency{path: "example.com/dependency", version: "v1.0.0", root: dependencyRoot})
	index, err := plugininventory.Build(application, dependencies)
	if err != nil || len(index.Plugins()) != 0 {
		t.Fatalf("Build = %#v, %v", index.Plugins(), err)
	}
}

func TestBuildIgnoresOrdinaryGraphModulePlugins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	ordinaryRoot := filepath.Join(root, "ordinary")
	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writePlugin(t, ordinaryRoot, "hidden", "id: acme.hidden\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/ordinary v1.0.0\n\nreplace example.com/ordinary => ../ordinary\n")
	application, err := modulelocate.Find(appRoot)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), application, moduledependency.Options{Environment: isolatedGoEnvironment()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dependencies.Modules()) != 1 || dependencies.Modules()[0].Project() || len(dependencies.Projects()) != 0 {
		t.Fatalf("dependency graph = %#v, Projects %#v", dependencies.Modules(), dependencies.Projects())
	}
	index, err := plugininventory.Build(application, dependencies)
	if err != nil || len(index.Plugins()) != 0 {
		t.Fatalf("Build = %#v, %v", index.Plugins(), err)
	}
}

func TestBuildIncludesTransitiveDependencyProjectPlugins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	directRoot := filepath.Join(root, "direct")
	transitiveRoot := filepath.Join(root, "transitive")
	writeFile(t, filepath.Join(directRoot, "go.mod"), "module example.com/direct\n\ngo 1.26\n\nrequire example.com/transitive v1.0.0\n")
	writeFile(t, filepath.Join(directRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, directRoot, "direct", "id: acme.direct\n")
	writeModule(t, transitiveRoot, "example.com/transitive")
	writeFile(t, filepath.Join(transitiveRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, transitiveRoot, "transitive", "id: acme.transitive\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/direct v1.0.0\n\nreplace example.com/direct => ../direct\n\nreplace example.com/transitive => ../transitive\n")
	application, err := modulelocate.Find(appRoot)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), application, moduledependency.Options{Environment: isolatedGoEnvironment()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(dependencies.Projects()) != 2 || !dependencies.Projects()[0].Direct() || dependencies.Projects()[1].Direct() {
		t.Fatalf("Projects = %#v", dependencies.Projects())
	}
	index, err := plugininventory.Build(application, dependencies)
	if err != nil || !reflect.DeepEqual(pluginIDs(index.Plugins()), []string{"acme.direct", "acme.transitive"}) {
		t.Fatalf("Build = %#v, %v", pluginIDs(index.Plugins()), err)
	}
}

func TestBuildRejectsPluginWithInvalidImportPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writePlugin(t, root, "bad path", "id: acme.bad-path\n")
	application, err := modulelocate.Find(root)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	index, err := plugininventory.Build(application, moduledependency.Index{})
	if !errors.Is(err, plugininventory.ErrBuild) || len(index.Plugins()) != 0 || !strings.Contains(err.Error(), "acme.bad-path") || !strings.Contains(err.Error(), "example.com/app/bad path") {
		t.Fatalf("Build = %#v, %v; want invalid import path", index.Plugins(), err)
	}
}

type dependency struct {
	path    string
	version string
	root    string
}

func configureApplication(t *testing.T, appRoot string, dependencies ...dependency) (modulelocate.Module, moduledependency.Index) {
	t.Helper()
	sort.Slice(dependencies, func(left, right int) bool { return dependencies[left].path < dependencies[right].path })
	var goMod strings.Builder
	goMod.WriteString("module example.com/app\n\ngo 1.26\n")
	if len(dependencies) != 0 {
		goMod.WriteString("\nrequire (\n")
		for _, dependency := range dependencies {
			goMod.WriteString("\t" + dependency.path + " " + dependency.version + "\n")
		}
		goMod.WriteString(")\n")
		for _, dependency := range dependencies {
			writeFile(t, filepath.Join(dependency.root, "plystra.yaml"), "{}\n")
			relative, err := filepath.Rel(appRoot, dependency.root)
			if err != nil {
				t.Fatalf("Rel: %v", err)
			}
			goMod.WriteString("\nreplace " + dependency.path + " => " + filepath.ToSlash(relative) + "\n")
		}
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), goMod.String())
	application, err := modulelocate.Find(appRoot)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	resolved, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{Environment: isolatedGoEnvironment()})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return application, resolved
}

func isolatedGoEnvironment() []string {
	overrides := map[string]string{
		"GOENV":       "off",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
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

func writeModule(t *testing.T, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writePlugin(t *testing.T, root, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "plugin.yaml"), manifest)
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

func pluginIDs(plugins []plugininventory.Plugin) []string {
	result := make([]string, len(plugins))
	for index, plugin := range plugins {
		result[index] = plugin.ID()
	}
	return result
}

func identifierStrings[T interface{ String() string }](identifiers []T) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}

func canonicalPath(t *testing.T, name string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(name)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", name, err)
	}
	return canonical
}
