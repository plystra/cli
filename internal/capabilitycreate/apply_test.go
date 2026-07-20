package capabilitycreate_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/generatedfiles"
)

func TestCreateCommitsCapabilityImplementationAndGeneratedProject(t *testing.T) {
	root := createBuildableAuthoringModule(t, "example.com/acme/library", "")
	writePlugin(t, root, "records", "id: acme.library.records\n")
	writeAuthoringFile(t, filepath.Join(root, "records", "plugin.go"), "package records\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	environment := visiblePlanEnvironment()

	result, err := capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       filepath.Join(root, "records"),
			Reference:   "records.create",
			Intent:      capabilitycreate.IntentProfileQuery,
			Environment: environment,
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.Capability().String() != "records.create/v1" || result.PluginID() != "acme.library.records" || result.PluginPath() != filepath.Join(root, "records") || !result.DeclarationCreated() || !result.ImplementationCreated() {
		t.Fatalf("Result = capability %s, plugin %s at %s, declaration %t, implementation %t", result.Capability(), result.PluginID(), result.PluginPath(), result.DeclarationCreated(), result.ImplementationCreated())
	}
	wantCapabilityPath := filepath.Join(root, "records", "capabilities", "records.create", "v1", "capability.yaml")
	if result.CapabilityPath() != wantCapabilityPath {
		t.Fatalf("CapabilityPath = %q, want %q", result.CapabilityPath(), wantCapabilityPath)
	}
	for _, filePath := range []string{
		"records/capabilities/records.create/v1/capability.yaml",
		"records/capability_records.create_v1.go",
		"generated/.plystra-manifest.json",
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/configuration/records_gen.go",
		"generated/go/contracts/records/create/v1/contract_gen.go",
		"generated/go/providers/records/create/v1/provider_gen.go",
	} {
		assertAuthoringFile(t, root, filePath)
	}
	manifest := readAuthoringFile(t, root, "records/plugin.yaml")
	if !bytes.Contains(manifest, []byte("records.create/v1")) {
		t.Fatalf("plugin manifest omits Capability: %s", manifest)
	}
	implementation := readAuthoringFile(t, root, "records/capability_records.create_v1.go")
	for _, required := range [][]byte{
		[]byte("func (*Plugin) Create("),
		[]byte("example.com/acme/library/generated/go/contracts/records/create/v1"),
		[]byte("implementation.unavailable"),
	} {
		if !bytes.Contains(implementation, required) {
			t.Fatalf("implementation omits %q:\n%s", required, implementation)
		}
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("generated check = %#v, %v", checked.Report().Changes(), err)
	}

	repeated, err := capabilitycreate.Implement(t.Context(), capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       root,
			Reference:   "records.create/v1",
			Plugin:      "records",
			Environment: environment,
		},
	})
	if err != nil || repeated.DeclarationCreated() || repeated.ImplementationCreated() {
		t.Fatalf("repeated Implement = declaration %t, implementation %t, %v", repeated.DeclarationCreated(), repeated.ImplementationCreated(), err)
	}
	if got := readAuthoringFile(t, root, "records/capability_records.create_v1.go"); !bytes.Equal(got, implementation) {
		t.Fatalf("repeated implementation changed user source:\nbefore: %s\nafter:  %s", implementation, got)
	}
}

func TestImplementCopiesVisibleDependencyContract(t *testing.T) {
	catalogRoot := t.TempDir()
	writeAuthoringFile(t, filepath.Join(catalogRoot, "go.mod"), "module example.com/catalog\n\ngo 1.26\n")
	writeAuthoringFile(t, filepath.Join(catalogRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, catalogRoot, "email", "id: catalog.email\nprovides: [email.send/v1]\n")
	schema := "id: email.send/v1\nextensions: {delivery: {idempotent: true}}\nrequest: {to: {type: string, required: true}}\nresponse: {accepted: {type: boolean, required: true}}\nerrors: [invalid_recipient]\nsemantics:\n  kind: query\n  effects: none\n  idempotency: {mode: inherent}\n  retry: {safety: safe}\n  cancellation: {mode: best-effort}\n  completion: {mode: completed-before-return}\n  ordering: {mode: none}\n  data: {request: public, response: public}\n"
	writeAuthoringFile(t, filepath.Join(catalogRoot, "email", "capabilities", "email.send", "v1", "capability.yaml"), schema)

	root := createBuildableAuthoringModule(t, "example.com/acme/app", catalogRoot)
	writePlugin(t, root, "mailer", "id: acme.app.mailer\n")
	writeAuthoringFile(t, filepath.Join(root, "mailer", "plugin.go"), "package mailer\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	environment := visiblePlanEnvironment()
	result, err := capabilitycreate.Implement(t.Context(), capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       root,
			Reference:   "email.send/v1",
			Plugin:      "mailer",
			Environment: environment,
		},
	})
	if err != nil {
		t.Fatalf("Implement dependency Capability: %v", err)
	}
	if result.Capability().String() != "email.send/v1" || !result.DeclarationCreated() || !result.ImplementationCreated() {
		t.Fatalf("Result = capability %s, declaration %t, implementation %t", result.Capability(), result.DeclarationCreated(), result.ImplementationCreated())
	}
	if got := string(readAuthoringFile(t, root, "mailer/capabilities/email.send/v1/capability.yaml")); got != schema {
		t.Fatalf("copied schema = %q, want %q", got, schema)
	}
	for _, filePath := range []string{
		"mailer/capability_email.send_v1.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		assertAuthoringFile(t, root, filePath)
	}
}

func TestAuthoringEnforcesActionAndExplicitVersionConfirmation(t *testing.T) {
	root := createBuildableAuthoringModule(t, "example.com/acme/library", "")
	writePlugin(t, root, "records", "id: acme.library.records\n")
	writeAuthoringFile(t, filepath.Join(root, "records", "plugin.go"), "package records\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	environment := visiblePlanEnvironment()
	base := capabilitycreate.Options{Start: root, Plugin: "records", Intent: capabilitycreate.IntentProfileQuery, Environment: environment}

	before := snapshotAuthoringTree(t, root)
	base.Reference = "records.create/v3"
	_, err := capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{Options: base})
	if !errors.Is(err, capabilitycreate.ErrCreate) || !errors.Is(err, capabilitycreate.ErrConfirmationRequired) {
		t.Fatalf("unconfirmed skipped version = %v", err)
	}
	if after := snapshotAuthoringTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unconfirmed create mutated module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if _, err := capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{Options: base, Confirm: true}); err != nil {
		t.Fatalf("confirmed skipped version: %v", err)
	}

	_, err = capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{Options: base, Confirm: true})
	if !errors.Is(err, capabilitycreate.ErrCreate) || !errors.Is(err, capabilitycreate.ErrActionMismatch) {
		t.Fatalf("create existing exact Capability = %v", err)
	}
	base.Reference = "records.missing/v1"
	_, err = capabilitycreate.Implement(t.Context(), capabilitycreate.AuthorOptions{Options: base})
	if !errors.Is(err, capabilitycreate.ErrImplement) || !errors.Is(err, capabilitycreate.ErrActionMismatch) {
		t.Fatalf("implement missing exact Capability = %v", err)
	}
}

func TestCreateRollsBackDeclarationsImplementationGenerationAndModuleMetadata(t *testing.T) {
	root := createBuildableAuthoringModule(t, "example.com/acme/library", "")
	writePlugin(t, root, "records", "id: acme.library.records\n")
	writeAuthoringFile(t, filepath.Join(root, "records", "plugin.go"), "package records\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	before := snapshotAuthoringTree(t, root)
	validationErr := errors.New("injected generated validation failure")
	_, err := capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       root,
			Reference:   "records.create",
			Intent:      capabilitycreate.IntentProfileQuery,
			Environment: visiblePlanEnvironment(),
		},
		Validate: func(context.Context, string) error { return validationErr },
	})
	if !errors.Is(err, capabilitycreate.ErrCreate) || !errors.Is(err, validationErr) {
		t.Fatalf("Create validation failure = %v", err)
	}
	if after := snapshotAuthoringTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed authoring changed module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCapabilityTransaction(t, root)
}

func TestCreateRejectsUnexpectedGeneratedOutputWithoutMutation(t *testing.T) {
	root := createBuildableAuthoringModule(t, "example.com/acme/library", "")
	writePlugin(t, root, "records", "id: acme.library.records\n")
	writeAuthoringFile(t, filepath.Join(root, "records", "plugin.go"), "package records\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	writeAuthoringFile(t, filepath.Join(root, "generated", "manual.txt"), "user-owned\n")
	before := snapshotAuthoringTree(t, root)

	_, err := capabilitycreate.Create(t.Context(), capabilitycreate.AuthorOptions{
		Options: capabilitycreate.Options{
			Start:       root,
			Reference:   "records.create",
			Intent:      capabilitycreate.IntentProfileQuery,
			Environment: visiblePlanEnvironment(),
		},
	})
	if !errors.Is(err, capabilitycreate.ErrCreate) || !errors.Is(err, generatedfiles.ErrUnexpected) || !strings.Contains(err.Error(), "generated/manual.txt") {
		t.Fatalf("Create unexpected-output error = %v", err)
	}
	if after := snapshotAuthoringTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("unexpected-output failure changed module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCapabilityTransaction(t, root)
}

func createBuildableAuthoringModule(t *testing.T, modulePath, catalogRoot string) string {
	t.Helper()
	root := t.TempDir()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	requireCatalog := ""
	replaceCatalog := ""
	if catalogRoot != "" {
		requireCatalog = "\texample.com/catalog v0.0.0\n"
		replaceCatalog = "\nreplace example.com/catalog => " + filepath.ToSlash(catalogRoot) + "\n"
	}
	goMod := fmt.Sprintf(`module %s

go 1.26

require (
%s	github.com/plystra/kernel v0.0.0
)

replace github.com/plystra/kernel => %s
%s`, modulePath, requireCatalog, filepath.ToSlash(kernelRoot), replaceCatalog)
	writeAuthoringFile(t, filepath.Join(root, "go.mod"), goMod)
	writeAuthoringFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeAuthoringFile(t, filepath.Join(root, "go.sum"), string(goSum))
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize module root: %v", err)
	}
	return canonical
}

func writeAuthoringFile(t *testing.T, filePath, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", filePath, err)
	}
	if err := os.WriteFile(filePath, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", filePath, err)
	}
}

func readAuthoringFile(t *testing.T, root, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return data
}

func assertAuthoringFile(t *testing.T, root, relative string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", relative, err)
	}
}

func snapshotAuthoringTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	if err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
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
	}); err != nil {
		t.Fatalf("snapshot authoring module: %v", err)
	}
	return result
}
