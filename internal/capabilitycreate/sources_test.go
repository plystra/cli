package capabilitycreate_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitysource"
)

func TestResolveSourcesLoadsEveryProviderWithoutRequiringByteEquality(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	first := []byte("id: account.register/v1\nrequest:\n  email: {type: string}\n")
	second := []byte("request: {email: {type: string}}\nid: account.register/v1\n")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, first)
	writeCapabilitySource(t, filepath.Join(root, "profile"), id, second)

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: filepath.Join(root, "account"), Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	if len(resolved) != 2 || resolved[0].Provider().PluginID() != "acme.app.account" || resolved[1].Provider().PluginID() != "acme.app.profile" {
		t.Fatalf("resolved sources = %#v", resolved)
	}
	if resolved[0].Source().ID() != id || !bytes.Equal(resolved[0].Source().Data(), first) || !bytes.Equal(resolved[1].Source().Data(), second) || bytes.Equal(resolved[0].Source().Data(), resolved[1].Source().Data()) {
		t.Fatalf("resolved data = %q and %q", resolved[0].Source().Data(), resolved[1].Source().Data())
	}
}

func TestResolveSourcesReturnsNoPartialResult(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\n"))
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: filepath.Join(root, "account"), Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if !errors.Is(err, capabilitycreate.ErrResolveSources) || !errors.Is(err, capabilitysource.ErrLoad) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ResolveSources error = %v", err)
	}
	if resolved != nil {
		t.Fatalf("ResolveSources returned partial result %#v", resolved)
	}
}

func TestResolveSourcesHandlesFirstVersionAndRejectsEmptyPlan(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if err != nil || resolved != nil {
		t.Fatalf("ResolveSources(first version) = %#v, %v", resolved, err)
	}
	if resolved, err := capabilitycreate.ResolveSources(capabilitycreate.Plan{}); !errors.Is(err, capabilitycreate.ErrResolveSources) || resolved != nil {
		t.Fatalf("ResolveSources(empty) = %#v, %v", resolved, err)
	}
}

func writeCapabilitySource(t *testing.T, pluginRoot string, id capabilityid.Identifier, data []byte) {
	t.Helper()
	name := filepath.Join(pluginRoot, "capabilities", id.Name(), "v"+strconv.FormatUint(id.Major(), 10), "capability.yaml")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func mustCapabilityID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}
