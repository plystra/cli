package command_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
)

func TestRunGenerateAndCheckUsePublicApplicationSurface(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/app

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	start := filepath.Join(root, "docs", "work")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	environment := commandGoEnvironment()

	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 1 || stdout != "" {
		t.Fatalf("initial check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	wantMissing := "generated output is not current:\n" +
		"  missing generated/.plystra-manifest.json\n" +
		"  missing generated/go/assembly/compatibility_gen.go\n" +
		"  missing generated/go/assembly/invocations_gen.go\n" +
		"  missing generated/go/assembly/providers_gen.go\n" +
		"  missing generated/go/bootstrap/bootstrap_gen.go\n" +
		"  missing generated/manifest.json\n"
	if stderr != wantMissing {
		t.Fatalf("initial check stderr = %q, want %q", stderr, wantMissing)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("generate --check mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/app in "+root+"\n" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/.plystra-manifest.json",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/manifest.json",
	} {
		assertCommandFile(t, root, name)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/app in "+root+"\n" {
		t.Fatalf("clean check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	drifted := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 1 || stdout != "" || stderr != "generated output is not current:\n  changed generated/manifest.json\n" {
		t.Fatalf("drift check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, drifted) {
		t.Fatalf("drift check mutated application:\nbefore: %#v\nafter:  %#v", drifted, after)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repair generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(root, "generated", "manual.txt"), "preserve\n")
	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 1 || stdout != "" || stderr != "generated output remains inconsistent after installation:\n  unexpected generated/manual.txt\n" {
		t.Fatalf("unexpected output = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, root, "generated/manual.txt")); got != "preserve\n" {
		t.Fatalf("unexpected user file = %q", got)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunGenerateCheckReportsDependencyCompositionDriftWithoutMutation(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "platform")
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/platform\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.health/v1]\n")
	goMod := fmt.Sprintf(`module example.com/acme/composed

go 1.26

require (
	example.com/platform v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/platform => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "capabilities:\n  require: []\n")
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, applicationRoot, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/composed in "+applicationRoot+"\n" {
		t.Fatalf("initial generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.info/v1]\n")
	before := commandTree(t, applicationRoot)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, applicationRoot, environment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Project configuration or generated output is not current:\n  changed plystra.yaml (dependency composition)\n") {
		t.Fatalf("composition check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("composition check mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestRunGenerateSelectsCompleteConfigurationThroughPublicCommand(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "platform")
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/platform\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	goMod := fmt.Sprintf(`module example.com/acme/config-select

go 1.26

require (
	example.com/platform v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/platform => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "go.sum"), string(goSum))
	rootConfiguration := `# shared root configuration must be excluded in replacement mode
http:
  expose: [email.send/v1]
capabilities:
  require: [email.send/v1]
  use: {email.send/v1: acme.root-mail}
`
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)
	selectedConfiguration := `# complete customer configuration
http:
  expose: [reports.read/v1]
capabilities:
  require: [email.send/v1, reports.read/v1]
  use: {email.send/v1: acme.selected-mail}
config:
  acme.selected-mail:
    endpoint: https://selected-runtime.invalid
    token: {env: SELECTED_MAIL_TOKEN}
`
	selectedPath := filepath.Join(applicationRoot, "deploy", "customer.yaml")
	writeCommandFile(t, selectedPath, selectedConfiguration)
	ambientPath := filepath.Join(applicationRoot, "deploy", "ambient.yaml")
	writeCommandFile(t, ambientPath, rootConfiguration)

	emailContract := `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`
	for _, plugin := range []string{"root-mail", "selected-mail"} {
		pluginID := "acme." + plugin
		manifest := "id: " + pluginID + "\nprovides: [email.send/v1]\n"
		if plugin == "selected-mail" {
			manifest += "config:\n  endpoint: {type: string}\n  token: {type: secret}\n"
		}
		writeCommandFile(t, filepath.Join(applicationRoot, plugin, "plugin.yaml"), manifest)
		writeCommandFile(t, filepath.Join(applicationRoot, plugin, "capabilities", "email.send", "v1", "capability.yaml"), emailContract)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "selected-mail", "plugin.go"), `package selectedmail

import (
	"context"

	configuration "example.com/acme/config-select/generated/go/configuration"
	contract "example.com/acme/config-select/generated/go/contracts/email/send/v1"
)

type Config = configuration.SelectedMailConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Accepted: request.To != ""}, nil
}
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "root-mail", "plugin.go"), `package rootmail

import (
	"context"

	configuration "example.com/acme/config-select/generated/go/configuration"
	contract "example.com/acme/config-select/generated/go/contracts/email/send/v1"
)

type Config = configuration.RootMailConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Accepted: request.To != ""}, nil
}
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "reports", "plugin.yaml"), "id: acme.reports\nprovides: [reports.read/v1]\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "reports", "capabilities", "reports.read", "v1", "capability.yaml"), "id: reports.read/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "reports", "plugin.go"), `package reports

import (
	"context"

	configuration "example.com/acme/config-select/generated/go/configuration"
	contract "example.com/acme/config-select/generated/go/contracts/reports/read/v1"
)

type Config = configuration.ReportsConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Read(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)

	nestedStart := filepath.Join(applicationRoot, "selected-mail")
	environment := commandGoEnvironmentWith(map[string]string{"SELECTED_MAIL_TOKEN": "resolved-super-secret"})
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--config", selectedPath}, nestedStart, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/config-select in "+applicationRoot+"\n" {
		t.Fatalf("generate --config from nested Plugin = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, applicationRoot, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("replacement generation changed root configuration:\n%s", got)
	}
	selected := readCommandFile(t, applicationRoot, "deploy/customer.yaml")
	for _, required := range [][]byte{[]byte("# complete customer configuration"), []byte("kernel.health/v1"), []byte("SELECTED_MAIL_TOKEN"), []byte("https://selected-runtime.invalid")} {
		if !bytes.Contains(selected, required) {
			t.Fatalf("maintained selected configuration omits %q:\n%s", required, selected)
		}
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, applicationRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	if provenance.Mode() != applicationgen.ConfigurationModeExplicit || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "deploy/customer.yaml" {
		t.Fatalf("selected manifest provenance = mode %q root %q selected %q", provenance.Mode(), provenance.RootPath(), provenance.SelectedPath())
	}
	for _, name := range []string{
		"generated/go/adapters/http/reports/read/v1/handler_gen.go",
		"generated/go/clients/reports/read/v1/client_gen.go",
		"generated/go/invocation/reports/read/v1/invocation_gen.go",
		"generated/sdk/javascript/src/operations/reports/read/v1.ts",
		"generated/docs/api.md",
		"generated/docs/openapi.json",
	} {
		assertCommandFile(t, applicationRoot, name)
	}
	for _, name := range []string{
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
	} {
		if _, statErr := os.Lstat(filepath.Join(applicationRoot, filepath.FromSlash(name))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("root-only exposure generated %s: %v", name, statErr)
		}
	}
	invocations := readCommandFile(t, applicationRoot, "generated/go/assembly/invocations_gen.go")
	if !bytes.Contains(invocations, []byte(`ProviderPackage: "example.com/acme/config-select/selected-mail"`)) || bytes.Contains(invocations, []byte(`ProviderPackage: "example.com/acme/config-select/root-mail"`)) {
		t.Fatalf("selected invocation assembly retained the root-only Provider choice:\n%s", invocations)
	}
	apiDocumentation := readCommandFile(t, applicationRoot, "generated/docs/api.md")
	if !bytes.Contains(apiDocumentation, []byte("reports.read/v1")) || bytes.Contains(apiDocumentation, []byte("email.send/v1")) {
		t.Fatalf("selected API documentation does not match replacement exposure:\n%s", apiDocumentation)
	}
	for name, content := range commandTree(t, filepath.Join(applicationRoot, "generated")) {
		for _, forbidden := range []string{applicationRoot, "https://selected-runtime.invalid", "SELECTED_MAIL_TOKEN", "resolved-super-secret"} {
			if bytes.Contains(content, []byte(forbidden)) {
				t.Fatalf("generated/%s leaked %q", name, forbidden)
			}
		}
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/config-select in "+applicationRoot+"\n" {
		t.Fatalf("clean generate --check --config = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	ambientEnvironment := commandGoEnvironmentWith(map[string]string{
		"PLYSTRA_CONFIG":      "deploy/ambient.yaml",
		"SELECTED_MAIL_TOKEN": "resolved-super-secret",
	})
	beforeAmbientCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, nestedStart, ambientEnvironment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Project configuration or generated output is not current:\n  changed deploy/ambient.yaml (dependency composition)\n") {
		t.Fatalf("PLYSTRA_CONFIG check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeAmbientCheck) {
		t.Fatal("PLYSTRA_CONFIG generate --check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, ambientEnvironment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("explicit --config did not override PLYSTRA_CONFIG = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.info/v1]}\n")
	beforeCompositionCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Project configuration or generated output is not current:\n  changed deploy/customer.yaml (dependency composition)\n") {
		t.Fatalf("selected-path composition drift = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeCompositionCheck) {
		t.Fatal("selected-path drift check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repair selected composition = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	selected = readCommandFile(t, applicationRoot, "deploy/customer.yaml")
	if bytes.Contains(selected, []byte("kernel.health/v1")) || !bytes.Contains(selected, []byte("kernel.info/v1")) {
		t.Fatalf("selected composition was not updated independently:\n%s", selected)
	}
	if got := string(readCommandFile(t, applicationRoot, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("selected composition repair changed root configuration:\n%s", got)
	}

	outsidePath := filepath.Join(root, "outside.yaml")
	writeCommandFile(t, outsidePath, "{}\n")
	beforeOutsideSelection := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", outsidePath}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "within the Project root") {
		t.Fatalf("outside-Project configuration = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeOutsideSelection) {
		t.Fatal("outside-Project selector rejection mutated the Project")
	}
	linkedDeploy := filepath.Join(applicationRoot, "linked-deploy")
	beforeSymbolicSelection := commandTree(t, applicationRoot)
	if err := os.Symlink(filepath.Join(applicationRoot, "deploy"), linkedDeploy); err == nil {
		exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "linked-deploy/customer.yaml"}, nestedStart, environment)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "symbolic path component") {
			t.Fatalf("symbolic configuration path = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if err := os.Remove(linkedDeploy); err != nil {
			t.Fatalf("remove symbolic configuration directory: %v", err)
		}
		if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeSymbolicSelection) {
			t.Fatal("symbolic selector rejection mutated the Project")
		}
	}

	if err := os.Remove(filepath.Join(applicationRoot, "plystra.yaml")); err != nil {
		t.Fatalf("remove root Project marker: %v", err)
	}
	beforeMissingMarkerCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "plystra.yaml") {
		t.Fatalf("missing mandatory root marker = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeMissingMarkerCheck) {
		t.Fatal("missing-marker failure mutated the Project")
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)
}

func TestRunGenerateBuildsUnrequiredLocalCapabilityDeveloperSurfaces(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/authoring

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "business", "plugin.yaml"), "id: acme.business\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "business", "capabilities", "email.send", "v1", "capability.yaml"), `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeCommandFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "example.com/acme/authoring/generated/go/configuration"
	contract "example.com/acme/authoring/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Accepted: request.To != ""}, nil
}
`)
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, filepath.Join(root, "business"), environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/authoring in "+root+"\n" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/go/configuration/business_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		assertCommandFile(t, root, name)
	}
	generated := commandTree(t, filepath.Join(root, "generated"))
	for _, name := range []string{
		"docs/api.md",
		"go/adapters/http/email/send/v1/handler_gen.go",
		"go/clients/email/send/v1/client_gen.go",
		"go/invocation/email/send/v1/invocation_gen.go",
		"sdk/javascript/package.json",
	} {
		if _, exists := generated[name]; exists {
			t.Fatalf("unrequired local Capability received application surface generated/%s", name)
		}
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/authoring in "+root+"\n" {
		t.Fatalf("clean check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestRunGenerateBuildsCompleteProjectAssembly(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/library

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "email", "plugin.yaml"), "id: acme.library.email\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "email", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(root, "email", "plugin.go"), `package email

import (
	"context"

	configuration "example.com/acme/library/generated/go/configuration"
	contract "example.com/acme/library/generated/go/contracts/email/send/v1"
)

type Config = configuration.EmailConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, filepath.Join(root, "email"), environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/library in "+root+"\n" {
		t.Fatalf("generate Project = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/.plystra-manifest.json",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/configuration/email_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
	} {
		assertCommandFile(t, root, name)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("clean Project check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func runCommand(t testing.TB, arguments []string, start string, environment []string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn(arguments, &stdout, &stderr, start, environment)
	return exitCode, stdout.String(), stderr.String()
}

func commandGoEnvironment() []string {
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
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func commandGoEnvironmentWith(overrides map[string]string) []string {
	environment := commandGoEnvironment()
	replaced := make(map[string]string, len(overrides))
	for key, value := range overrides {
		replaced[strings.ToUpper(key)] = value
	}
	filtered := environment[:0]
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := replaced[strings.ToUpper(key)]; !exists {
			filtered = append(filtered, entry)
		}
	}
	keys := make([]string, 0, len(replaced))
	for key := range replaced {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filtered = append(filtered, key+"="+replaced[key])
	}
	return filtered
}

func writeCommandFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func readCommandFile(t testing.TB, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func assertCommandFile(t testing.TB, root, name string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", name, err)
	}
}

func commandTree(t testing.TB, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
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
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return result
}

func assertNoCommandTransactions(t testing.TB, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("transaction files = %v, %v", matches, err)
	}
}

func commandRepositoryRoot(t testing.TB) string {
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
