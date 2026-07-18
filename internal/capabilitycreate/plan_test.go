package capabilitycreate_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/plugintarget"
)

func TestPrepareVisibleUsesExplicitDependencyCapabilitySources(t *testing.T) {
	t.Parallel()

	dependencyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dependencyRoot, "go.mod"), []byte("module example.com/catalog\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dependency go.mod): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dependencyRoot, "plystra.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dependency plystra.yaml): %v", err)
	}
	writePlugin(t, dependencyRoot, "email", "id: catalog.email\nprovides: [email.send/v3]\n")
	identifier := mustCapabilityID(t, "email.send/v3")
	writeCapabilitySource(t, filepath.Join(dependencyRoot, "email"), identifier, []byte("id: email.send/v3\nrequest: {to: {type: string, required: true}}\nresponse: {}\nerrors: []\n"))

	root := createModule(t)
	goMod := fmt.Sprintf("module example.com/acme/app\n\ngo 1.26\n\nrequire example.com/catalog v0.0.0\n\nreplace example.com/catalog => %s\n", filepath.ToSlash(dependencyRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("WriteFile(application go.mod): %v", err)
	}
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	options := capabilitycreate.Options{
		Start:       root,
		Reference:   "email.send/v3",
		Plugin:      "profile",
		Environment: visiblePlanEnvironment(),
	}
	plan, err := capabilitycreate.PrepareVisible(t.Context(), options)
	if err != nil {
		t.Fatalf("PrepareVisible: %v", err)
	}
	if plan.Version().Action() != capabilityversion.ActionImplement || plan.Version().Target().String() != "email.send/v3" {
		t.Fatalf("Version = target %s, action %s", plan.Version().Target(), plan.Version().Action())
	}
	providers := plan.SourceProviders()
	if len(providers) != 1 {
		t.Fatalf("SourceProviders = %#v", providers)
	}
	provider := providers[0]
	if provider.PluginID() != "catalog.email" || provider.Directory() != "email" || provider.Path() != filepath.Join(dependencyRoot, "email") || provider.ModulePath() != "example.com/catalog" || provider.ModuleVersion() != "v0.0.0" || provider.ModuleRoot() != dependencyRoot || provider.Local() || provider.Capability() != identifier {
		t.Fatalf("dependency Provider = ID %q, directory %q, path %q, module %s@%s at %q, local %t, capability %s", provider.PluginID(), provider.Directory(), provider.Path(), provider.ModulePath(), provider.ModuleVersion(), provider.ModuleRoot(), provider.Local(), provider.Capability())
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if err != nil || len(resolved) != 1 || resolved[0].Source().ID() != identifier {
		t.Fatalf("ResolveSources = %#v, %v", resolved, err)
	}

	options.Reference = "email.send"
	next, err := capabilitycreate.PrepareVisible(t.Context(), options)
	if err != nil || next.Version().Target().String() != "email.send/v4" {
		t.Fatalf("PrepareVisible(next) = target %s, %v", next.Version().Target(), err)
	}
	if recommendations := next.Recommendations(); len(recommendations) != 0 {
		t.Fatalf("same-name version progression recommendations = %v", recommendations)
	}

	options.Reference = "email.sned"
	nearby, err := capabilitycreate.PrepareVisible(t.Context(), options)
	if err != nil || nearby.Version().Target().String() != "email.sned/v1" {
		t.Fatalf("PrepareVisible(nearby) = target %s, %v", nearby.Version().Target(), err)
	}
	recommendations := nearby.Recommendations()
	if len(recommendations) != 1 || recommendations[0].String() != "email.send/v3" {
		t.Fatalf("PrepareVisible(nearby) recommendations = %v", recommendations)
	}
}

func TestPrepareInfersTargetVersionAndAllLocalSources(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1, account.register/v3]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [profile.get/v1, account.register/v3]\n")
	start := filepath.Join(root, "account", "capabilities")
	if err := os.Mkdir(start, 0o755); err != nil {
		t.Fatalf("Mkdir(start): %v", err)
	}

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: start, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Target().ID() != "acme.app.account" || plan.Target().Directory() != "account" || plan.Target().Path() != filepath.Join(root, "account") {
		t.Fatalf("Target = ID %q, directory %q, path %q", plan.Target().ID(), plan.Target().Directory(), plan.Target().Path())
	}
	manifest := plan.Target().ManifestData()
	if string(manifest) != "id: acme.app.account\nprovides: [account.register/v1, account.register/v3]\n" {
		t.Fatalf("Target manifest = %q", manifest)
	}
	manifest[0] = 'x'
	if plan.Target().ManifestData()[0] != 'i' {
		t.Fatal("Target manifest exposed mutable plan storage")
	}
	version := plan.Version()
	source, hasSource := version.Source()
	highest, hasHighest := version.HighestVisible()
	if version.Target().String() != "account.register/v4" || !hasSource || source.String() != "account.register/v3" || !hasHighest || highest != source || version.Action() != capabilityversion.ActionCreate || version.RequiresConfirmation() {
		t.Fatalf("Version = target %q, source %q/%t, highest %q/%t, action %q, confirmation %t", version.Target(), source, hasSource, highest, hasHighest, version.Action(), version.RequiresConfirmation())
	}
	providers := plan.SourceProviders()
	if len(providers) != 2 {
		t.Fatalf("SourceProviders = %#v", providers)
	}
	assertProvider(t, providers[0], "acme.app.account", "account", filepath.Join(root, "account"), "account.register/v3")
	assertProvider(t, providers[1], "acme.app.profile", "profile", filepath.Join(root, "profile"), "account.register/v3")
	providers[0] = capabilitycreate.Provider{}
	if plan.SourceProviders()[0].PluginID() != "acme.app.account" {
		t.Fatal("SourceProviders exposed mutable plan storage")
	}
}

func TestPrepareHonorsExplicitTargetForExistingCapability(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{
		Start:     filepath.Join(root, "account"),
		Reference: "account.register/v1",
		Plugin:    "profile",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Target().ID() != "acme.app.profile" || plan.Version().Action() != capabilityversion.ActionImplement || plan.Version().Caution() != capabilityversion.CautionExisting || !plan.Version().RequiresConfirmation() {
		t.Fatalf("Plan = target %q, action %q, caution %q", plan.Target().ID(), plan.Version().Action(), plan.Version().Caution())
	}
	providers := plan.SourceProviders()
	if len(providers) != 1 {
		t.Fatalf("SourceProviders = %#v", providers)
	}
	assertProvider(t, providers[0], "acme.app.account", "account", filepath.Join(root, "account"), "account.register/v1")
}

func TestPrepareCreatesFirstVersionWithoutSources(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.Version().Target().String() != "account.register/v1" || len(plan.SourceProviders()) != 0 {
		t.Fatalf("Plan = target %q, providers %#v", plan.Version().Target(), plan.SourceProviders())
	}
}

func TestPrepareRejectsInvalidReferenceBeforeFilesystemAccess(t *testing.T) {
	t.Parallel()

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: "missing", Reference: "Account.Register"})
	if !errors.Is(err, capabilitycreate.ErrPlan) || !errors.Is(err, capabilityid.ErrInvalid) {
		t.Fatalf("Prepare error = %v, want ErrPlan and ErrInvalid", err)
	}
	if plan.Target().ID() != "" || plan.Version().Target().String() != "" || len(plan.SourceProviders()) != 0 {
		t.Fatalf("invalid Prepare returned %#v", plan)
	}
}

func TestPrepareReportsMissingPlugin(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	if _, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"}); !errors.Is(err, capabilitycreate.ErrPlan) || !errors.Is(err, plugintarget.ErrNotFound) {
		t.Fatalf("Prepare error = %v, want ErrPlan and ErrNotFound", err)
	}
}

func createModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/acme/app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "plystra.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(plystra.yaml): %v", err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return canonical
}

func writePlugin(t *testing.T, root, name, declaration string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir(%s): %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(declaration), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertProvider(t *testing.T, provider capabilitycreate.Provider, pluginID, directory, path, identifier string) {
	t.Helper()
	if provider.PluginID() != pluginID || provider.Directory() != directory || provider.Path() != path || provider.ModulePath() != "example.com/acme/app" || provider.ModuleVersion() != "" || provider.ModuleRoot() != filepath.Dir(path) || !provider.Local() || provider.Capability().String() != identifier {
		t.Fatalf("Provider = ID %q, directory %q, path %q, module %s@%s at %q, local %t, capability %q", provider.PluginID(), provider.Directory(), provider.Path(), provider.ModulePath(), provider.ModuleVersion(), provider.ModuleRoot(), provider.Local(), provider.Capability())
	}
}

func visiblePlanEnvironment() []string {
	overrides := map[string]string{
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
