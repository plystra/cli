package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRunCapabilityCreateAndImplementUsePublicTransactionalSurface(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	environment := commandGoEnvironment()
	pluginRoot := filepath.Join(root, "records")

	exitCode, stdout, stderr := runCommand(t, []string{"capability", "create", "records.create"}, pluginRoot, environment)
	wantPath := filepath.Join(root, "records", "capabilities", "records.create", "v1", "capability.yaml")
	wantOutput := "created capability records.create/v1 in acme.library.records at " + wantPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, filePath := range []string{
		"records/capabilities/records.create/v1/capability.yaml",
		"records/capability_records.create_v1.go",
		"generated/.plystra-manifest.json",
		"generated/go/contracts/records/create/v1/contract_gen.go",
		"generated/go/providers/records/create/v1/provider_gen.go",
	} {
		assertCommandFile(t, root, filePath)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "implement", "records.create/v1", "--plugin", "records"}, root, environment)
	wantOutput = "implemented capability records.create/v1 in acme.library.records at " + wantPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("capability implement = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "records.create/v1", "--plugin", "records"}, root, environment)
	wantError := "create capability: capability authoring action does not match visible contracts: records.create/v1 is already visible; implement the existing exact contract instead\n"
	if exitCode != 1 || stdout != "" || stderr != wantError {
		t.Fatalf("duplicate capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	beforeConfirmation := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "records.archive/v3", "--plugin", "records"}, root, environment)
	wantError = "create capability: capability version requires confirmation: records.archive/v3 is skipped without visible version history; rerun with --confirm after reviewing visible Capability versions\n"
	if exitCode != 1 || stdout != "" || stderr != wantError {
		t.Fatalf("unconfirmed capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeConfirmation) {
		t.Fatalf("unconfirmed command mutated module:\nbefore: %#v\nafter:  %#v", beforeConfirmation, after)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "records.archive/v3", "--confirm", "--plugin", "records"}, root, environment)
	wantArchivePath := filepath.Join(root, "records", "capabilities", "records.archive", "v3", "capability.yaml")
	wantOutput = "created capability records.archive/v3 in acme.library.records at " + wantArchivePath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("confirmed capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "record.create", "--plugin", "records"}, root, environment)
	wantRelatedPath := filepath.Join(root, "records", "capabilities", "record.create", "v1", "capability.yaml")
	wantOutput = "created capability record.create/v1 in acme.library.records at " + wantRelatedPath + "\n"
	wantRecommendation := "recommendation: review visible Capability records.create/v1 before keeping custom record.create/v1\n"
	if exitCode != 0 || stdout != wantOutput || stderr != wantRecommendation {
		t.Fatalf("near-name capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("post-authoring generate check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunCapabilityCreateAndExposeRegenerateRunnableApplication(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"capability", "create", "records.list", "--plugin", "records"}, root, environment)
	wantPath := filepath.Join(root, "records", "capabilities", "records.list", "v1", "capability.yaml")
	wantOutput := "created capability records.list/v1 in acme.library.records at " + wantPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("runnable capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, filePath := range []string{
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/contracts/records/list/v1/contract_gen.go",
		"generated/go/providers/records/list/v1/provider_gen.go",
		"generated/manifest.json",
	} {
		assertCommandFile(t, root, filePath)
	}
	for _, filePath := range []string{
		"generated/go/clients/records/list/v1/client_gen.go",
		"generated/go/invocation/records/list/v1/invocation_gen.go",
		"generated/go/adapters/http/records/list/v1/handler_gen.go",
	} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(filePath))); !os.IsNotExist(err) {
			t.Fatalf("unrequired Capability emitted %s: %v", filePath, err)
		}
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "records.list/v1"}, filepath.Join(root, "records"), environment)
	wantManifestPath := filepath.Join(root, "plystra.yaml")
	wantOutput = "exposed capability records.list/v1 over HTTP in " + wantManifestPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("capability expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/go/adapters/http/records/list/v1/handler_gen.go",
		"generated/go/clients/records/list/v1/client_gen.go",
		"generated/go/invocation/records/list/v1/invocation_gen.go",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/operations/records/list/v1.ts",
	} {
		assertCommandFile(t, root, filePath)
	}
	idempotentBefore := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "records.list/v1"}, root, environment)
	wantOutput = "capability records.list/v1 is already exposed over HTTP in " + wantManifestPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("idempotent capability expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, idempotentBefore) {
		t.Fatalf("idempotent capability expose changed module:\nbefore: %#v\nafter:  %#v", idempotentBefore, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "records.write", "--expose", "--plugin", "records"}, root, environment)
	wantWritePath := filepath.Join(root, "records", "capabilities", "records.write", "v1", "capability.yaml")
	wantOutput = "created capability records.write/v1 in acme.library.records at " + wantWritePath + "\n" +
		"exposed capability records.write/v1 over HTTP in " + wantManifestPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("capability create --expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, filePath := range []string{
		"records/capabilities/records.write/v1/capability.yaml",
		"generated/go/adapters/http/records/write/v1/handler_gen.go",
		"generated/go/clients/records/write/v1/client_gen.go",
		"generated/go/invocation/records/write/v1/invocation_gen.go",
		"generated/sdk/javascript/src/operations/records/write/v1.ts",
	} {
		assertCommandFile(t, root, filePath)
	}
	manifestData := readCommandFile(t, root, "plystra.yaml")
	manifest, err := applicationmeta.Parse(manifestData)
	if err != nil || len(manifest.HTTPExposures()) != 2 || manifest.HTTPExposures()[0].ID().String() != "records.list/v1" || manifest.HTTPExposures()[1].ID().String() != "records.write/v1" {
		t.Fatalf("application manifest exposures are incomplete or unsorted: %#v, %v\n%s", manifest.HTTPExposures(), err, manifestData)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("runnable post-authoring check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestRunCapabilityRejectsUnexpectedGeneratedOutput(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	environment := commandGoEnvironment()
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	missingBefore := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"capability", "expose", "missing.operation/v1"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "missing.operation/v1") || !strings.Contains(stderr, "absent from the visible canonical catalog") {
		t.Fatalf("missing capability expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, missingBefore) {
		t.Fatalf("missing expose changed module:\nbefore: %#v\nafter:  %#v", missingBefore, after)
	}
	assertNoCommandTransactions(t, root)

	writeCommandFile(t, filepath.Join(root, "generated", "manual.txt"), "user-owned\n")
	before := commandTree(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "create", "records.create", "--expose", "--plugin", "records"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "unexpected generated output") || !strings.Contains(stderr, "generated/manual.txt") {
		t.Fatalf("unexpected-output capability create = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed command changed module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.health/v1"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "unexpected generated output") || !strings.Contains(stderr, "generated/manual.txt") {
		t.Fatalf("unexpected-output capability expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed expose changed module:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func writeCapabilityCommandModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/library

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "records", "plugin.yaml"), "id: acme.library.records\n")
	writeCommandFile(t, filepath.Join(root, "records", "plugin.go"), `package records

import configuration "example.com/acme/library/generated/go/configuration"

type Config = configuration.RecordsConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }
`)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize command module: %v", err)
	}
	return canonical
}
