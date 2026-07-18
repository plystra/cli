package capabilitycreate_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/pluginindex"
)

func TestWriteDeclarationsCommitsOneValidatedTransaction(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	manifestPath := filepath.Join(root, "account", "plugin.yaml")
	writePlugin(t, root, "account", "id: acme.app.account\nconfig: {}\n")
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil || sources != nil {
		t.Fatalf("ResolveSources = %#v, %v", sources, err)
	}
	if err := capabilitycreate.WriteDeclarations(plan, sources); err != nil {
		t.Fatalf("WriteDeclarations: %v", err)
	}

	wantManifest := "id: acme.app.account\nprovides:\n  - account.register/v1\nconfig: {}\n"
	if manifest, err := os.ReadFile(manifestPath); err != nil || string(manifest) != wantManifest {
		t.Fatalf("manifest = %q, %v", manifest, err)
	}
	wantSchema, err := os.ReadFile("testdata/account.register.v1.yaml")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	schemaPath := filepath.Join(root, "account", "capabilities", "account.register", "v1", "capability.yaml")
	if schema, err := os.ReadFile(schemaPath); err != nil || !bytes.Equal(schema, wantSchema) {
		t.Fatalf("schema = %q, %v", schema, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(manifestPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("manifest mode = %#v, %v", info, err)
		}
	}
	index, err := pluginindex.Scan(root)
	plugin, ok := index.ByName("account")
	if err != nil || !ok || len(plugin.Provides()) != 1 || plugin.Provides()[0] != plan.Version().Target() {
		t.Fatalf("updated index = %#v, %t, %v", plugin, ok, err)
	}
	assertNoCapabilityTransaction(t, root)
}

func TestWriteDeclarationsCommitsRetainedSourceCopy(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	id := mustCapabilityID(t, "account.register/v1")
	sourceData := []byte("# Account contract.\nid: account.register/v1\ndescription: Registers an account.\nrequest: {email: {type: string, required: true}}\nextensions: {authn: {authenticated: true}}\n")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, sourceData)
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Plugin: "profile"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	if err := capabilitycreate.WriteDeclarations(plan, sources); err != nil {
		t.Fatalf("WriteDeclarations: %v", err)
	}

	wantSchema, err := capabilitymeta.RetargetSchema(sourceData, plan.Version().Target())
	if err != nil {
		t.Fatalf("RetargetSchema: %v", err)
	}
	targetPath := filepath.Join(root, "profile", "capabilities", "account.register", "v2", "capability.yaml")
	if schema, err := os.ReadFile(targetPath); err != nil || !bytes.Equal(schema, wantSchema) {
		t.Fatalf("target schema = %q, %v", schema, err)
	}
	if manifest, err := os.ReadFile(filepath.Join(root, "profile", "plugin.yaml")); err != nil || string(manifest) != "id: acme.app.profile\nprovides:\n  - account.register/v2\n" {
		t.Fatalf("target manifest = %q, %v", manifest, err)
	}
	if source, err := os.ReadFile(filepath.Join(root, "account", "capabilities", "account.register", "v1", "capability.yaml")); err != nil || !bytes.Equal(source, sourceData) {
		t.Fatalf("source schema = %q, %v", source, err)
	}
	assertNoCapabilityTransaction(t, root)
}

func TestWriteDeclarationsRollsBackWhenDependencySourceChanges(t *testing.T) {
	t.Parallel()

	dependencyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(dependencyRoot, "go.mod"), []byte("module example.com/catalog\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dependency go.mod): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dependencyRoot, "plystra.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dependency plystra.yaml): %v", err)
	}
	writePlugin(t, dependencyRoot, "email", "id: catalog.email\nprovides: [email.send/v1]\n")
	identifier := mustCapabilityID(t, "email.send/v1")
	sourcePath := filepath.Join(dependencyRoot, "email")
	writeCapabilitySource(t, sourcePath, identifier, []byte("id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"))

	root := createModule(t)
	goMod := "module example.com/acme/app\n\ngo 1.26\n\nrequire example.com/catalog v0.0.0\n\nreplace example.com/catalog => " + filepath.ToSlash(dependencyRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("WriteFile(application go.mod): %v", err)
	}
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	plan, err := capabilitycreate.PrepareVisible(t.Context(), capabilitycreate.Options{
		Start:       root,
		Reference:   "email.send/v1",
		Plugin:      "profile",
		Environment: visiblePlanEnvironment(),
	})
	if err != nil {
		t.Fatalf("PrepareVisible: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	writeCapabilitySource(t, sourcePath, identifier, []byte("id: email.send/v1\nrequest: {to: {type: string}}\nresponse: {}\nerrors: []\n"))

	err = capabilitycreate.WriteDeclarations(plan, sources)
	if !errors.Is(err, capabilitycreate.ErrWriteDeclarations) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("WriteDeclarations error = %v", err)
	}
	if manifest, readErr := os.ReadFile(filepath.Join(root, "profile", "plugin.yaml")); readErr != nil || string(manifest) != "id: acme.app.profile\n" {
		t.Fatalf("target manifest after rollback = %q, %v", manifest, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "profile", "capabilities", "email.send", "v1")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target capability remains after rollback: %v", statErr)
	}
	assertNoCapabilityTransaction(t, root)
}

func TestWriteDeclarationsRollsBackBothFilesWhenValidationFails(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	manifest := []byte("id: acme.app.account\nconfig: {}\n")
	writePlugin(t, root, "account", string(manifest))
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Plugin: "account"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	invalidOtherManifest := []byte("id: acme.app.profile\nunknown: true\n")
	otherPath := filepath.Join(root, "profile", "plugin.yaml")
	if err := os.WriteFile(otherPath, invalidOtherManifest, 0o644); err != nil {
		t.Fatalf("WriteFile(other manifest): %v", err)
	}

	err = capabilitycreate.WriteDeclarations(plan, nil)
	if !errors.Is(err, capabilitycreate.ErrWriteDeclarations) || !errors.Is(err, atomicfs.ErrWriteFiles) || !errors.Is(err, pluginindex.ErrIndex) {
		t.Fatalf("WriteDeclarations error = %v", err)
	}
	if current, err := os.ReadFile(filepath.Join(root, "account", "plugin.yaml")); err != nil || !bytes.Equal(current, manifest) {
		t.Fatalf("target manifest after rollback = %q, %v", current, err)
	}
	if current, err := os.ReadFile(otherPath); err != nil || !bytes.Equal(current, invalidOtherManifest) {
		t.Fatalf("other manifest after rollback = %q, %v", current, err)
	}
	assertCapabilityMissing(t, root, "account", "account.register", "v1")
	assertNoCapabilityTransaction(t, root)
}

func TestWriteDeclarationsPreservesConcurrentTargetManifestEdit(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	userEdit := []byte("id: acme.app.account\nconfig:\n  token: {type: secret}\n")
	manifestPath := filepath.Join(root, "account", "plugin.yaml")
	if err := os.WriteFile(manifestPath, userEdit, 0o644); err != nil {
		t.Fatalf("WriteFile(user edit): %v", err)
	}

	err = capabilitycreate.WriteDeclarations(plan, nil)
	if !errors.Is(err, capabilitycreate.ErrWriteDeclarations) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("WriteDeclarations error = %v", err)
	}
	if current, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(current, userEdit) {
		t.Fatalf("manifest after rejection = %q, %v", current, err)
	}
	assertCapabilityMissing(t, root, "account", "account.register", "v1")
	assertNoCapabilityTransaction(t, root)
}

func TestWriteDeclarationsRollsBackWhenResolvedSourceChanges(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\nrequest: {}\n"))
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Plugin: "profile"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	userEdit := []byte("id: account.register/v1\ndescription: User edit.\nrequest: {}\n")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, userEdit)
	targetManifest := []byte("id: acme.app.profile\n")

	err = capabilitycreate.WriteDeclarations(plan, sources)
	if !errors.Is(err, capabilitycreate.ErrWriteDeclarations) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("WriteDeclarations error = %v", err)
	}
	if current, err := os.ReadFile(filepath.Join(root, "profile", "plugin.yaml")); err != nil || !bytes.Equal(current, targetManifest) {
		t.Fatalf("target manifest after rollback = %q, %v", current, err)
	}
	if current, err := os.ReadFile(filepath.Join(root, "account", "capabilities", "account.register", "v1", "capability.yaml")); err != nil || !bytes.Equal(current, userEdit) {
		t.Fatalf("source after rollback = %q, %v", current, err)
	}
	assertCapabilityMissing(t, root, "profile", "account.register", "v2")
	assertNoCapabilityTransaction(t, root)
}

func assertCapabilityMissing(t *testing.T, root, plugin, name, version string) {
	t.Helper()
	path := filepath.Join(root, plugin, "capabilities", name, version)
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("capability path exists after rejected transaction: %v", err)
	}
}

func assertNoCapabilityTransaction(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction files remain: %v", matches)
	}
}
