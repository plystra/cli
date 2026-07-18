package applicationresolve_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestMain(main *testing.M) {
	if mode := os.Getenv("PLYSTRA_APPLICATION_RESOLVE_HELPER"); mode != "" {
		os.Exit(runResolveHelper(mode))
	}
	os.Exit(main.Run())
}

func TestResolveEmptyApplicationDeterministicallyWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/empty")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := snapshotTree(t, root)
	options := applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.Module().Path() != root || first.Module().ModulePath() != "example.com/empty" {
		t.Fatalf("Module = %#v", first.Module())
	}
	if _, exists := first.Manifest().HTTPAddress(); exists || len(first.Manifest().Requirements()) != 0 || len(first.Manifest().Aliases()) != 0 {
		t.Fatalf("Manifest is not empty: %#v", first.Manifest())
	}
	if len(first.Inventory().Plugins()) != 0 {
		t.Fatalf("Inventory = %#v", first.Inventory().Plugins())
	}
	if len(first.Dependencies().Modules()) != 0 {
		t.Fatalf("Dependencies = %#v", first.Dependencies().Modules())
	}
	resolved := first.Resolution()
	if resolved.Passes() != 1 || len(resolved.Context().Plugins()) != 0 || len(resolved.Context().Requirements()) != 0 || len(resolved.Context().Providers()) != 0 {
		t.Fatalf("empty resolution = passes %d, plugins %#v, requirements %#v, providers %#v", resolved.Passes(), resolved.Context().Plugins(), resolved.Context().Requirements(), resolved.Context().Providers())
	}
	capabilities := resolved.Context().Capabilities()
	if len(capabilities) != 2 || capabilities[0].ID().String() != "kernel.health/v1" || capabilities[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("intrinsic catalog = %#v", capabilities)
	}
	if len(resolved.AliasResolution().Aliases()) != 0 {
		t.Fatalf("Aliases = %#v", resolved.AliasResolution().Aliases())
	}
	if !first.Configurations().Valid() || len(first.Configurations().Bindings()) != 0 || first.Configurations().Digest() == "" {
		t.Fatalf("Configurations = %#v", first.Configurations())
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || resolved.Context().Digest() != second.Resolution().Context().Digest() || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() {
		t.Fatal("repeated empty resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveClosesLocalRequirementsThroughDependencyProvidersAndAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), `http:
  address: ":9090"
  expose: [email.send/v1]
timeouts: {startup: 1s}
capabilities:
  use: {email.send/v1: example.smtp}
  aliases:
    mail.send/v1: email.send/v1
config:
  example.smtp:
    host: private.smtp.example.com
    password: {env: PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET}
`)
	writeFile(t, filepath.Join(providerRoot, "plystra.production.yaml"), "not: [a valid dependency overlay\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig: {host: {type: string, required: true}, password: {type: secret, required: true}}\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/providers v1.2.3

replace example.com/providers => ../providers
`)
	writePlugin(t, appRoot, "local", "id: example.local\nrequires: [email.send/v1]\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "http: {address: \":8080\"}\n")
	before := snapshotTree(t, appRoot)
	dependencyBefore := snapshotTree(t, providerRoot)
	options := applicationresolve.Options{
		Start:       filepath.Join(appRoot, "local"),
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plugins := first.Inventory().Plugins()
	dependencies := first.Dependencies().Modules()
	if len(dependencies) != 1 || dependencies[0].Path() != "example.com/providers" || dependencies[0].SelectedVersion() != "v1.2.3" {
		t.Fatalf("Dependencies = %#v", dependencies)
	}
	if !first.Composition().Valid() || first.Composition().DependencyDigest() == "" || len(first.Composition().Provenance()) == 0 {
		t.Fatalf("Composition = %#v", first.Composition())
	}
	if address, exists := first.Manifest().HTTPAddress(); !exists || address != ":8080" || first.Manifest().StartupTimeout() != applicationmeta.DefaultStartupTimeout || len(first.CurrentManifest().HTTPExposures()) != 0 || len(first.Manifest().HTTPExposures()) != 1 {
		t.Fatalf("composed/current manifests = effective %#v, current %#v", first.Manifest(), first.CurrentManifest())
	}
	if got := pluginSummaries(plugins); !reflect.DeepEqual(got, []string{
		"example.local:example.com/app@local:local:true",
		"example.smtp:example.com/providers@v1.2.3:smtp:false",
	}) {
		t.Fatalf("Inventory = %v", got)
	}
	resolved := first.Resolution()
	capability := parseGenerationCapability(t, "email.send/v1")
	provider, exists := resolved.Context().SelectedProvider(capability)
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("SelectedProvider(email.send/v1) = %s, %t", provider, exists)
	}
	if requirements := resolved.Context().Requirements(); len(requirements) != 1 || requirements[0] != capability {
		t.Fatalf("Requirements = %v", requirements)
	}
	target, exists := resolved.Context().Capability(capability)
	if !exists || target.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
		t.Fatalf("target exposure = %#v, %t", target.Exposure(), exists)
	}
	aliases := resolved.AliasResolution().Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "mail.send/v1" || aliases[0].Target().String() != "email.send/v1" || aliases[0].Exposure() != target.Exposure() {
		t.Fatalf("Aliases = %#v", aliases)
	}
	if got := configurationBindingIDs(first.Configurations().Bindings()); !reflect.DeepEqual(got, []string{"example.local", "example.smtp"}) {
		t.Fatalf("configuration bindings = %v", got)
	}
	for _, forbidden := range []string{"private.smtp.example.com", "PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET"} {
		if bytes.Contains(resolved.Context().CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("generation context exposed private configuration %q: %s", forbidden, resolved.Context().CanonicalJSON())
		}
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() {
		t.Fatal("repeated dependency resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if after := snapshotTree(t, providerRoot); !reflect.DeepEqual(after, dependencyBefore) {
		t.Fatalf("Resolve mutated dependency Project:\nbefore: %#v\nafter:  %#v", dependencyBefore, after)
	}
}

func TestResolveComposesDirectAndTransitiveDependencyProjectDeclarations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	directRoot := filepath.Join(root, "direct")
	transitiveRoot := filepath.Join(root, "transitive")
	ordinaryRoot := filepath.Join(root, "ordinary")

	writeModule(t, transitiveRoot, "example.com/transitive")
	writeFile(t, filepath.Join(transitiveRoot, "plystra.yaml"), "capabilities: {require: [audit.write/v1]}\n")
	writePlugin(t, transitiveRoot, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, transitiveRoot, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeFile(t, filepath.Join(directRoot, "go.mod"), "module example.com/direct\n\ngo 1.26\n\nrequire example.com/transitive v1.4.0\n")
	writeFile(t, filepath.Join(directRoot, "plystra.yaml"), `http:
  expose: [email.send/v1]
capabilities:
  use: {email.send/v1: example.smtp}
`)
	writePlugin(t, directRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, directRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writeFile(t, filepath.Join(ordinaryRoot, "looks-like-plugin", "plugin.yaml"), "this is deliberately not a valid Plugin declaration\n")

	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/direct v1.2.0
	example.com/ordinary v1.0.0
)

replace example.com/direct => ../direct
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	projects := result.Dependencies().Projects()
	if len(projects) != 2 || projects[0].Path() != "example.com/direct" || projects[0].SelectedVersion() != "v1.2.0" || projects[1].Path() != "example.com/transitive" || projects[1].SelectedVersion() != "v1.4.0" || projects[1].Direct() {
		t.Fatalf("dependency Projects = %#v", projects)
	}
	if got := pluginSummaries(result.Inventory().Plugins()); !reflect.DeepEqual(got, []string{
		"example.audit:example.com/transitive@v1.4.0:audit:false",
		"example.smtp:example.com/direct@v1.2.0:smtp:false",
	}) {
		t.Fatalf("visible Plugins = %v", got)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"audit.write/v1"}) {
		t.Fatalf("composed requirements = %v", got)
	}
	if got := applicationExposureIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("composed exposures = %v", got)
	}
	contextRequirements := result.Resolution().Context().Requirements()
	if len(contextRequirements) != 2 || contextRequirements[0].String() != "audit.write/v1" || contextRequirements[1].String() != "email.send/v1" {
		t.Fatalf("resolved requirements = %v", contextRequirements)
	}
	if provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1")); !exists || provider.String() != "example.smtp" {
		t.Fatalf("email Provider = %s, %t", provider, exists)
	}
	provenance := result.Composition().Provenance()
	for path, source := range map[string]string{
		`http.expose["email.send/v1"]`:           "example.com/direct@v1.2.0/plystra.yaml",
		`capabilities.require["audit.write/v1"]`: "example.com/transitive@v1.4.0/plystra.yaml",
		`capabilities.use["email.send/v1"]`:      "example.com/direct@v1.2.0/plystra.yaml",
	} {
		records := compositionProvenance(provenance, path)
		if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.HasPrefix(records[0].Sources()[0], source) {
			t.Fatalf("provenance for %s = %#v", path, records)
		}
	}
}

func TestResolveReportsInheritedProviderConflictAndAcceptsExactCurrentReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	moduleRoots := map[string]string{
		"example.com/a": filepath.Join(root, "a"),
		"example.com/b": filepath.Join(root, "b"),
	}
	providers := map[string]string{"example.com/a": "example.smtp-a", "example.com/b": "example.smtp-b"}
	for modulePath, moduleRoot := range moduleRoots {
		writeModule(t, moduleRoot, modulePath)
		provider := providers[modulePath]
		writeFile(t, filepath.Join(moduleRoot, "plystra.yaml"), fmt.Sprintf("capabilities: {use: {email.send/v1: %s}}\n", provider))
		writePlugin(t, moduleRoot, "smtp", fmt.Sprintf("id: %s\nprovides: [email.send/v1]\n", provider))
		writeCapability(t, moduleRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.0.0
)

replace example.com/a => ../a
replace example.com/b => ../b
`)
	manifestPath := filepath.Join(appRoot, "plystra.yaml")
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\n")
	options := applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}

	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("Resolve conflict error = %v", err)
	}
	for _, required := range []string{
		`capabilities.use["email.send/v1"]`,
		"example.smtp-a",
		"example.smtp-b",
		"example.com/a@v1.0.0/plystra.yaml",
		"example.com/b@v1.0.0/plystra.yaml",
	} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("conflict error omits %q: %v", required, err)
		}
	}

	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1], use: {email.send/v1: example.smtp-a}}\n")
	result, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve with current replacement: %v", err)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp-a" {
		t.Fatalf("selected replacement Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 2 {
		t.Fatalf("inherited conflict provenance = %#v", records)
	}
}

func TestResolveCurrentProviderRemovalRestoresUniqueProviderSelection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeModule(t, dependencyRoot, "example.com/smtp")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/smtp v1.0.0

replace example.com/smtp => ../smtp
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: null}}\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve with Provider removal: %v", err)
	}
	if len(result.Manifest().ProviderChoices()) != 0 {
		t.Fatalf("effective explicit Provider choices = %#v", result.Manifest().ProviderChoices())
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("automatic unique Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.Contains(records[0].Sources()[0], "example.com/smtp@v1.0.0/plystra.yaml") {
		t.Fatalf("inherited Provider provenance = %#v", records)
	}
}

func TestResolveRejectsMalformedAndUnsafeDependencyProjectManifest(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "unknown: true\n")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency@v1.2.3") || !strings.Contains(err.Error(), `unknown key "unknown"`) {
			t.Fatalf("Resolve malformed dependency error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		target := filepath.Join(root, "outside.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(dependencyRoot, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency") {
			t.Fatalf("Resolve unsafe dependency error = %v", err)
		}
	})
}

func TestResolveUsesActiveGoWorkspaceDependencySource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/workspace-app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n")
	goWork := filepath.Join(root, "go.work")
	writeFile(t, goWork, "go 1.26\n\nuse (\n\t./app\n\t./providers\n)\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": goWork, "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plugins := result.Inventory().Plugins()
	if len(plugins) != 1 || plugins[0].ID() != "example.smtp" || plugins[0].ModuleRoot() != providerRoot || plugins[0].ModuleVersion() != "" || plugins[0].Source() != "example.com/providers@local/smtp/plugin.yaml" {
		t.Fatalf("workspace plugin = %#v, summaries %v", plugins, pluginSummaries(plugins))
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("workspace provider = %s, %t", provider, exists)
	}
}

func TestResolveExecutesSelectedFilesystemGenerationExtension(t *testing.T) {
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	goMod := fmt.Sprintf(
		"module example.com/extension-app\n\ngo 1.26\n\nrequire github.com/plystra/cli v0.0.0\n\nrequire (\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/cli => %s\n",
		strconv.Quote(filepath.ToSlash(cliRoot)),
	)
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n")
	writePlugin(t, root, "business", "id: example.business\nprovides: [order.create/v1]\n")
	writePlugin(t, root, "authn", `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writePlugin(t, root, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "order.create/v1", `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCapability(t, root, "authn", "authn.session.verify/v1", "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), realExtensionSource)
	before := snapshotTree(t, root)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOWORK": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	generated := result.Resolution().GeneratedRequirements()
	if result.Resolution().Passes() != 3 || len(generated) != 1 || generated[0].PluginID() != "example.authn" || generated[0].Capability().String() != "audit.write/v1" {
		t.Fatalf("extension resolution = passes %d, generated %#v", result.Resolution().Passes(), generated)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "audit.write/v1"))
	if !exists || provider.String() != "example.audit" {
		t.Fatalf("generated audit provider = %s, %t", provider, exists)
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension artifacts = %v, %v", entries, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("extension resolution mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveRejectsMissingUnsafeAndChangingManifest(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/missing")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrNotFound) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/directory")
		if err := os.Mkdir(filepath.Join(root, "plystra.yaml"), 0o755); err != nil {
			t.Fatalf("Mkdir(plystra.yaml): %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/symbolic")
		target := filepath.Join(t.TempDir(), "application.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(root, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/oversized")
		writeFile(t, filepath.Join(root, "plystra.yaml"), strings.Repeat(" ", applicationmeta.MaximumSize+1))
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationresolve.ErrUnsafeManifest) || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("changed before completion", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/changing\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n")
		manifestPath := filepath.Join(appRoot, "plystra.yaml")
		writeFile(t, manifestPath, "{}\n")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start:     appRoot,
			GoCommand: os.Args[0],
			Environment: goEnvironment(map[string]string{
				"GOWORK":                             "off",
				"PLYSTRA_APPLICATION_RESOLVE_HELPER": "change-manifest",
				"PLYSTRA_APPLICATION_MANIFEST":       manifestPath,
				"PLYSTRA_APPLICATION_MODULE_ROOT":    dependencyRoot,
			}),
		})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrConcurrentChange) || !strings.Contains(err.Error(), "plystra.yaml") {
			t.Fatalf("Resolve error = %v", err)
		}
	})
}

func runResolveHelper(mode string) int {
	if mode != "change-manifest" {
		return 9
	}
	want := []string{"list", "-m", "-json", "-mod=readonly", "all"}
	if len(os.Args) != len(want)+1 {
		return 10
	}
	for index, value := range want {
		if os.Args[index+1] != value {
			return 11
		}
	}
	if err := os.WriteFile(os.Getenv("PLYSTRA_APPLICATION_MANIFEST"), []byte("timeouts: {}\n"), 0o644); err != nil {
		return 12
	}
	applicationRoot, err := os.Getwd()
	if err != nil {
		return 13
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{
		"Path":  "example.com/changing",
		"Main":  true,
		"Dir":   applicationRoot,
		"GoMod": filepath.Join(applicationRoot, "go.mod"),
	}); err != nil {
		return 14
	}
	root := os.Getenv("PLYSTRA_APPLICATION_MODULE_ROOT")
	if err := encoder.Encode(map[string]any{
		"Path":    "example.com/dependency",
		"Version": "v1.2.3",
		"Dir":     root,
		"GoMod":   filepath.Join(root, "go.mod"),
	}); err != nil {
		return 15
	}
	return 0
}

func writeModule(t testing.TB, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writePlugin(t testing.TB, moduleRoot, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(moduleRoot, name, "plugin.yaml"), manifest)
}

func writeCapability(t testing.TB, moduleRoot, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(moduleRoot, plugin, "capabilities", filepath.FromSlash(identifier.Name()), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml"), source)
}

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func parseGenerationCapability(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	identifier, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("generation.ParseCapabilityID(%s): %v", value, err)
	}
	return identifier
}

func configurationBindingIDs(bindings []configurationresolve.Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = binding.PluginID()
	}
	return result
}

func applicationRequirementIDs(manifest applicationmeta.Manifest) []string {
	requirements := manifest.Requirements()
	result := make([]string, len(requirements))
	for index, requirement := range requirements {
		result[index] = requirement.ID().String()
	}
	return result
}

func applicationExposureIDs(manifest applicationmeta.Manifest) []string {
	exposures := manifest.HTTPExposures()
	result := make([]string, len(exposures))
	for index, exposure := range exposures {
		result[index] = exposure.ID().String()
	}
	return result
}

func compositionProvenance(values []applicationmeta.Provenance, path string) []applicationmeta.Provenance {
	var result []applicationmeta.Provenance
	for _, value := range values {
		if value.Path() == path {
			result = append(result, value)
		}
	}
	return result
}

func pluginSummaries(plugins []plugininventory.Plugin) []string {
	result := make([]string, len(plugins))
	for index, plugin := range plugins {
		version := plugin.ModuleVersion()
		if version == "" {
			version = "local"
		}
		result[index] = fmt.Sprintf("%s:%s@%s:%s:%t", plugin.ID(), plugin.ModulePath(), version, plugin.Path(), plugin.Local())
	}
	return result
}

type treeEntry struct {
	path     string
	mode     fs.FileMode
	modified time.Time
	data     []byte
}

func snapshotTree(t testing.TB, root string) []treeEntry {
	t.Helper()
	var result []treeEntry
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		state := treeEntry{path: filepath.ToSlash(relative), mode: info.Mode()}
		if info.Mode().IsRegular() {
			state.modified = info.ModTime()
			state.data, err = os.ReadFile(name)
			if err != nil {
				return err
			}
		}
		result = append(result, state)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}

func goEnvironment(overrides map[string]string) []string {
	defaults := map[string]string{
		"GOENV":       "off",
		"GOTOOLCHAIN": "local",
	}
	for key, value := range overrides {
		defaults[strings.ToUpper(key)] = value
	}
	environment := make([]string, 0, len(os.Environ())+len(defaults))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := defaults[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+defaults[key])
	}
	return environment
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}

const realExtensionSource = `package extension

import generation "github.com/plystra/cli/generation/v1"

func Generate(context generation.GenerationContext) (generation.Output, error) {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{Requirements: []generation.Requirement{{
		RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit,
	}}}, nil
}
`
