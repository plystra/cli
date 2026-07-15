package capabilitycreate_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/plugintarget"
)

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
	if provider.PluginID() != pluginID || provider.Directory() != directory || provider.Path() != path || provider.Capability().String() != identifier {
		t.Fatalf("Provider = ID %q, directory %q, path %q, capability %q", provider.PluginID(), provider.Directory(), provider.Path(), provider.Capability())
	}
}
