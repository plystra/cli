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
		"generated/go/application/main_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/contracts/records/list/v1/contract_gen.go",
		"generated/go/providers/records/list/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/proto/wire-map.json",
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

func TestRunCapabilityExposeSelectsEnvironmentAndReplacementConfiguration(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	pluginRoot := filepath.Join(root, "records")
	rootData := "# Shared defaults.\n{}\n"
	productionData := "# Production choices.\nhttp:\n  expose:\n    remove: [kernel.health/v1, kernel.info/v1]\n"
	stagingData := "# Staging choices.\n{}\n"
	customerData := "# Customer A.\n{}\n"
	automationData := "# Automation.\n{}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), productionData)
	writeCommandFile(t, filepath.Join(root, "plystra.staging.yaml"), stagingData)
	writeCommandFile(t, filepath.Join(root, "deploy", "customer-a.yaml"), customerData)
	writeCommandFile(t, filepath.Join(root, "deploy", "automation.yaml"), automationData)

	bothAmbient := commandGoEnvironmentWith(map[string]string{
		"PLYSTRA_CONFIG": "ignored.yaml",
		"PLYSTRA_ENV":    "ignored",
	})
	exitCode, stdout, stderr := runCommand(t, []string{"capability", "expose", "kernel.health/v1", "--env", "production"}, pluginRoot, bothAmbient)
	productionPath := filepath.Join(root, "plystra.production.yaml")
	wantOutput := "exposed capability kernel.health/v1 over HTTP in " + productionPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("explicit environment expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	production := string(readCommandFile(t, root, "plystra.production.yaml"))
	for _, retained := range []string{"# Production choices.", "add:\n      - kernel.health/v1", "remove: [kernel.info/v1]"} {
		if !strings.Contains(production, retained) {
			t.Fatalf("production overlay omits %q:\n%s", retained, production)
		}
	}
	for name, want := range map[string]string{
		"plystra.yaml":           rootData,
		"plystra.staging.yaml":   stagingData,
		"deploy/customer-a.yaml": customerData,
		"deploy/automation.yaml": automationData,
	} {
		if got := string(readCommandFile(t, root, name)); got != want {
			t.Fatalf("explicit environment changed %s:\n%s", name, got)
		}
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, pluginRoot, bothAmbient)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("environment post-exposure check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	idempotentBefore := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.health/v1", "--env", "production"}, root, bothAmbient)
	wantOutput = "capability kernel.health/v1 is already exposed over HTTP in " + productionPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("idempotent environment expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, idempotentBefore) {
		t.Fatalf("idempotent environment expose changed Project:\nbefore: %#v\nafter:  %#v", idempotentBefore, after)
	}

	stagingEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "staging"})
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.info/v1"}, pluginRoot, stagingEnvironment)
	stagingPath := filepath.Join(root, "plystra.staging.yaml")
	wantOutput = "exposed capability kernel.info/v1 over HTTP in " + stagingPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("ambient environment expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if staging := string(readCommandFile(t, root, "plystra.staging.yaml")); !strings.Contains(staging, "# Staging choices.") || !strings.Contains(staging, "kernel.info/v1") {
		t.Fatalf("ambient environment exposure = %q", staging)
	}
	if got := string(readCommandFile(t, root, "plystra.yaml")); got != rootData {
		t.Fatalf("ambient environment changed root configuration: %q", got)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.health/v1", "--config", "deploy/customer-a.yaml"}, pluginRoot, bothAmbient)
	customerPath := filepath.Join(root, "deploy", "customer-a.yaml")
	wantOutput = "exposed capability kernel.health/v1 over HTTP in " + customerPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("explicit replacement expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if customer := string(readCommandFile(t, root, "deploy/customer-a.yaml")); !strings.Contains(customer, "# Customer A.") || !strings.Contains(customer, "kernel.health/v1") {
		t.Fatalf("explicit replacement exposure = %q", customer)
	}
	if got := string(readCommandFile(t, root, "plystra.yaml")); got != rootData {
		t.Fatalf("explicit replacement changed root configuration: %q", got)
	}

	automationEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "deploy/automation.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.info/v1"}, pluginRoot, automationEnvironment)
	automationPath := filepath.Join(root, "deploy", "automation.yaml")
	wantOutput = "exposed capability kernel.info/v1 over HTTP in " + automationPath + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("ambient replacement expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if automation := string(readCommandFile(t, root, "deploy/automation.yaml")); !strings.Contains(automation, "# Automation.") || !strings.Contains(automation, "kernel.info/v1") {
		t.Fatalf("ambient replacement exposure = %q", automation)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunCapabilityExposeRejectsUnsafeOrMissingSelectionWithoutMutation(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "# Production.\n{}\n")

	tests := []struct {
		name        string
		arguments   []string
		environment []string
		wantError   string
	}{
		{name: "missing environment overlay", arguments: []string{"capability", "expose", "kernel.health/v1", "--env", "missing"}, environment: commandGoEnvironment(), wantError: "plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"capability", "expose", "kernel.health/v1", "--env", "../outside"}, environment: commandGoEnvironment(), wantError: "one safe filename component"},
		{name: "escaping replacement", arguments: []string{"capability", "expose", "kernel.health/v1", "--config", "../outside.yaml"}, environment: commandGoEnvironment(), wantError: "within the Project root"},
		{name: "ambient selector conflict", arguments: []string{"capability", "expose", "kernel.health/v1"}, environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "plystra.yaml"}), wantError: "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, test.arguments, filepath.Join(root, "records"), test.environment)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.wantError) {
				t.Fatalf("capability expose = exit %d, stdout %q, stderr %q; want error containing %q", exitCode, stdout, stderr, test.wantError)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected selector changed Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunCapabilityExposeRejectsMissingHTTPTransportAndRollsBack(t *testing.T) {
	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http: {transports: {connect: false, rest: false}}\n")
	before := commandTree(t, root)

	for _, test := range []struct {
		name       string
		arguments  []string
		capability string
	}{
		{name: "standalone exposure", arguments: []string{"capability", "expose", "kernel.health/v1"}, capability: "kernel.health/v1"},
		{name: "authored exposure", arguments: []string{"capability", "create", "records.disabled", "--expose", "--plugin", "records"}, capability: "records.disabled/v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, filepath.Join(root, "records"), commandGoEnvironment())
			if exitCode != 1 || stdout != "" {
				t.Fatalf("command = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			for _, want := range []string{
				"invalid HTTP transport selection",
				"http.expose is nonempty",
				"http.transports.connect and http.transports.rest are both false",
				"enable at least one transport in the selected current-project configuration",
				test.capability + ` at plystra.yaml http.expose["` + test.capability + `"]`,
			} {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr %q does not contain %q", stderr, want)
				}
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed command changed Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
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

	overlayData := "# Production-only exposure.\n{}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayData)
	overlayBefore := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"capability", "expose", "kernel.health/v1", "--env", "production"}, filepath.Join(root, "records"), environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "unexpected generated output") || !strings.Contains(stderr, "generated/manual.txt") {
		t.Fatalf("unexpected-output environment expose = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, overlayBefore) {
		t.Fatalf("failed environment expose changed Project:\nbefore: %#v\nafter:  %#v", overlayBefore, after)
	}
	if got := string(readCommandFile(t, root, "plystra.production.yaml")); got != overlayData {
		t.Fatalf("failed environment expose did not restore overlay: %q", got)
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
