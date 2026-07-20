package capabilitycreate_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilitymeta"
)

func TestRenderSchemaWriteCreatesCompleteFirstVersionWithoutMutation(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\n")
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Intent: capabilitycreate.IntentProfileQuery})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil || sources != nil {
		t.Fatalf("ResolveSources = %#v, %v", sources, err)
	}
	write, err := capabilitycreate.RenderSchemaWrite(plan, sources)
	if err != nil {
		t.Fatalf("RenderSchemaWrite: %v", err)
	}
	want, err := os.ReadFile("testdata/account.register.v1.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if write.Path != "account/capabilities/account.register/v1/capability.yaml" || !bytes.Equal(write.Data, want) || write.Mode.Perm() != 0o644 || !write.MustNotExist || !write.ParentMustNotExist {
		t.Fatalf("schema write = %#v", write)
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(write.Path))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("RenderSchemaWrite changed filesystem: %v", err)
	}
	declared, err := capabilitymeta.ParseID(write.Data)
	if err != nil || declared != plan.Version().Target() {
		t.Fatalf("rendered ID = %s, %v", declared, err)
	}
}

func TestRenderSchemaWriteCopiesAndRetargetsDeterministicSource(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	id := mustCapabilityID(t, "account.register/v1")
	sourceData := []byte("# Account contract.\r\nid: account.register/v1\r\ndescription: Registers an account.\r\nrequest: {email: {type: string, required: true}}\r\nsemantics:\r\n  kind: query\r\n  effects: none\r\n  idempotency: {mode: inherent}\r\n  retry: {safety: safe}\r\n  cancellation: {mode: best-effort}\r\n  completion: {mode: completed-before-return}\r\n  ordering: {mode: none}\r\n  data: {request: public, response: public}\r\nextensions: {authn: {authenticated: true}}\r\n")
	sourcePath := filepath.Join(root, "account")
	writeCapabilitySource(t, sourcePath, id, sourceData)
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Plugin: "profile"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	write, err := capabilitycreate.RenderSchemaWrite(plan, sources)
	if err != nil {
		t.Fatalf("RenderSchemaWrite: %v", err)
	}
	want, err := capabilitymeta.RetargetSchema(sourceData, plan.Version().Target())
	if err != nil {
		t.Fatalf("RetargetSchema: %v", err)
	}
	if write.Path != "profile/capabilities/account.register/v2/capability.yaml" || !bytes.Equal(write.Data, want) {
		t.Fatalf("copied schema write = %#v", write)
	}
	write.Data[0] = 'x'
	repeated, err := capabilitycreate.RenderSchemaWrite(plan, sources)
	if err != nil || !bytes.Equal(repeated.Data, want) {
		t.Fatalf("repeated RenderSchemaWrite = %#v, %v", repeated, err)
	}
	loaded, err := os.ReadFile(filepath.Join(sourcePath, "capabilities", "account.register", "v1", "capability.yaml"))
	if err != nil || !bytes.Equal(loaded, sourceData) {
		t.Fatalf("source changed = %q, %v", loaded, err)
	}
}

func TestRenderSchemaWritePreservesExactExistingVersionBytes(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "profile", "id: acme.app.profile\n")
	id := mustCapabilityID(t, "account.register/v1")
	sourceData := []byte("id: account.register/v1\r\nrequest: {}\r\nsemantics:\r\n  kind: query\r\n  effects: none\r\n  idempotency: {mode: inherent}\r\n  retry: {safety: safe}\r\n  cancellation: {mode: best-effort}\r\n  completion: {mode: completed-before-return}\r\n  ordering: {mode: none}\r\n  data: {request: public, response: public}\r\n")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, sourceData)
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register/v1", Plugin: "profile"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	write, err := capabilitycreate.RenderSchemaWrite(plan, sources)
	if err != nil || write.Path != "profile/capabilities/account.register/v1/capability.yaml" || !bytes.Equal(write.Data, sourceData) {
		t.Fatalf("RenderSchemaWrite(existing) = %#v, %v", write, err)
	}
}

func TestRenderSchemaWriteRejectsEmptyOrMismatchedSnapshots(t *testing.T) {
	t.Parallel()

	if write, err := capabilitycreate.RenderSchemaWrite(capabilitycreate.Plan{}, nil); !errors.Is(err, capabilitycreate.ErrRenderSchema) || write.Path != "" || write.Data != nil {
		t.Fatalf("RenderSchemaWrite(empty) = %#v, %v", write, err)
	}

	root := createModule(t)
	writePlugin(t, root, "account", "id: acme.app.account\nprovides: [account.register/v1]\n")
	writePlugin(t, root, "audit", "id: acme.app.audit\nprovides: [account.register/v1]\n")
	id := mustCapabilityID(t, "account.register/v1")
	writeCapabilitySource(t, filepath.Join(root, "account"), id, []byte("id: account.register/v1\n"+querySemanticsYAML))
	writeCapabilitySource(t, filepath.Join(root, "audit"), id, []byte("id: account.register/v1\n"+querySemanticsYAML))
	plan, err := capabilitycreate.Prepare(capabilitycreate.Options{Start: root, Reference: "account.register", Plugin: "account"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if write, err := capabilitycreate.RenderSchemaWrite(plan, nil); !errors.Is(err, capabilitycreate.ErrRenderSchema) || write.Path != "" {
		t.Fatalf("RenderSchemaWrite(missing sources) = %#v, %v", write, err)
	}
	sources, err := capabilitycreate.ResolveSources(plan)
	if err != nil {
		t.Fatalf("ResolveSources: %v", err)
	}
	if write, err := capabilitycreate.RenderSchemaWrite(plan, append(sources, sources[0])); !errors.Is(err, capabilitycreate.ErrRenderSchema) || write.Path != "" {
		t.Fatalf("RenderSchemaWrite(extra source) = %#v, %v", write, err)
	}
	sources[0], sources[1] = sources[1], sources[0]
	if write, err := capabilitycreate.RenderSchemaWrite(plan, sources); !errors.Is(err, capabilitycreate.ErrRenderSchema) || write.Path != "" {
		t.Fatalf("RenderSchemaWrite(reordered sources) = %#v, %v", write, err)
	}
}
