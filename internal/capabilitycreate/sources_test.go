package capabilitycreate_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
)

func TestResolveSourcesLoadsEveryProviderWithoutRequiringByteEquality(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	first := []byte("id: account.register/v1\ndescription: Account provider wording.\nrequest:\n  email: {type: string, required: false, enum: [work, personal]}\nerrors: [unavailable, invalid_email]\nextensions:\n  authz: {space: request.space_id, permission: account.register}\n  authn: {authenticated: true}\n")
	second := []byte("extensions: {authn: {authenticated: true}, authz: {permission: account.register, space: request.space_id}}\nerrors: [invalid_email, unavailable]\nrequest: {email: {enum: [personal, work], type: string}}\ndescription: Profile provider wording.\nid: account.register/v1\n")
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

func TestResolveSourcesRejectsExtensionMetadataConflict(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\nextensions:\n  authn: {authenticated: true}\n  authz: {permission: account.register, space: request.space_id}\n"))
	writeCapabilitySource(t, filepath.Join(root, "profile"), id, []byte("id: account.register/v1\nextensions:\n  authz: {permission: account.update, space: request.space_id}\n"))

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: filepath.Join(root, "account"), Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if !errors.Is(err, capabilitycreate.ErrResolveSources) || !errors.Is(err, capabilitycreate.ErrSchemaConflict) || resolved != nil {
		t.Fatalf("ResolveSources = %#v, %v", resolved, err)
	}
	var conflict *capabilitycreate.SchemaConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("ResolveSources error type = %T", err)
	}
	differences := conflict.Differences()
	if len(differences) != 2 || differences[0].Path() != "extensions.authn" || differences[0].Baseline() != `{"authenticated":true}` || differences[0].Conflicting() != "<missing>" || differences[1].Path() != "extensions.authz.permission" || differences[1].Baseline() != `"account.register"` || differences[1].Conflicting() != `"account.update"` {
		t.Fatalf("extension differences = %#v", differences)
	}
	message := err.Error()
	for _, required := range []string{"extensions.authn", "extensions.authz.permission", "account.register", "account.update", "new capability version"} {
		if !strings.Contains(message, required) {
			t.Fatalf("conflict error %q does not contain %q", message, required)
		}
	}
}

func TestResolveSourcesRejectsSemanticSchemaConflict(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\nrequest:\n  email: {type: string, required: true}\nresponse:\n  state: {type: string, enum: [active, disabled]}\nerrors: [unavailable, invalid_email]\n"))
	writeCapabilitySource(t, filepath.Join(root, "profile"), id, []byte("id: account.register/v1\nrequest:\n  email: {type: integer}\nresponse:\n  state: {type: string, enum: [pending]}\nerrors: [unavailable]\n"))

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: filepath.Join(root, "account"), Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if !errors.Is(err, capabilitycreate.ErrResolveSources) || !errors.Is(err, capabilitycreate.ErrSchemaConflict) {
		t.Fatalf("ResolveSources error = %v", err)
	}
	if resolved != nil {
		t.Fatalf("ResolveSources returned conflicting result %#v", resolved)
	}
	var conflict *capabilitycreate.SchemaConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("ResolveSources error type = %T", err)
	}
	if conflict.Capability() != id || conflict.BaselineProvider().PluginID() != "acme.app.account" || conflict.ConflictingProvider().PluginID() != "acme.app.profile" {
		t.Fatalf("conflict = capability %s, providers %s and %s", conflict.Capability(), conflict.BaselineProvider().PluginID(), conflict.ConflictingProvider().PluginID())
	}
	wantPaths := []string{"errors", "request.email.required", "request.email.type", "response.state.enum"}
	gotPaths := make([]string, 0, len(conflict.Differences()))
	for _, difference := range conflict.Differences() {
		gotPaths = append(gotPaths, difference.Path())
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("difference paths = %v, want %v", gotPaths, wantPaths)
	}
	if differences := conflict.Differences(); differences[0].Baseline() != `["invalid_email","unavailable"]` || differences[0].Conflicting() != `["unavailable"]` || differences[1].Baseline() != "true" || differences[1].Conflicting() != "<missing>" {
		t.Fatalf("difference values = %#v", differences)
	}
	differences := conflict.Differences()
	differences[0] = capabilitycreate.SchemaDifference{}
	if conflict.Differences()[0].Path() != "errors" {
		t.Fatal("Differences exposed mutable conflict storage")
	}
	message := err.Error()
	for _, required := range []string{
		"account.register/v1",
		"acme.app.account",
		"acme.app.profile",
		conflict.BaselineSourcePath(),
		conflict.ConflictingSourcePath(),
		"request.email.type: \"string\" != \"integer\"",
		"correction: make every provider",
		"new capability version",
	} {
		if !strings.Contains(message, required) {
			t.Fatalf("conflict error %q does not contain %q", message, required)
		}
	}
}

func TestResolveSourcesRejectsInvalidSchemaWithoutPartialResult(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\nrequest:\n  email: {type: string}\n"))
	writeCapabilitySource(t, filepath.Join(root, "profile"), id, []byte("id: account.register/v1\nrequest:\n  email: {type: bytes}\n"))

	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: filepath.Join(root, "account"), Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	resolved, err := capabilitycreate.ResolveSources(plan)
	if !errors.Is(err, capabilitycreate.ErrResolveSources) || !errors.Is(err, capabilitymeta.ErrInvalidManifest) || resolved != nil {
		t.Fatalf("ResolveSources(invalid schema) = %#v, %v", resolved, err)
	}
	if !strings.Contains(err.Error(), "acme.app.profile") || !strings.Contains(err.Error(), filepath.Join(root, "profile")) {
		t.Fatalf("invalid schema error lacks provider location: %v", err)
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
