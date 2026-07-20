package command_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/transportprovenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func TestRunGenerateAndCheckUsePublicApplicationSurface(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/app

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
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
		"  missing generated/go/application/main_gen.go\n" +
		"  missing generated/go/assembly/compatibility_gen.go\n" +
		"  missing generated/go/assembly/invocations_gen.go\n" +
		"  missing generated/go/assembly/providers_gen.go\n" +
		"  missing generated/go/bootstrap/bootstrap_gen.go\n" +
		"  missing generated/manifest.json\n" +
		"  missing generated/proto/descriptor-set.pb\n" +
		"  missing generated/proto/wire-map.json\n"
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
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/wire-map.json",
	} {
		assertCommandFile(t, root, name)
	}
	assertCommandBootstrapProvenanceMatchesManifest(t, root)

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

func TestRunGenerateInstallsConnectRuntimeRequirementsAndCheckIsReadOnly(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/connect-runtime

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
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	addLegacyProtobufReplacement(t, root)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `http:
  transports:
    connect: true
    rest: false
  expose:
    - kernel.health/v1
`)
	environment := commandGoEnvironment()

	beforeCheck := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 1 || stdout != "" {
		t.Fatalf("initial Connect check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, want := range []string{
		"invalid generated application runtime dependency",
		connectgen.ConnectModulePath,
		connectgen.ConnectModuleVersion,
		"run plystra generate",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("initial Connect check stderr %q omits %q", stderr, want)
		}
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatalf("generate --check changed the Project before runtime installation:\nbefore: %#v\nafter:  %#v", beforeCheck, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/connect-runtime in "+root+"\n" {
		t.Fatalf("Connect generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	assertCommandFile(t, root, "generated/go/adapters/connect/kernel/health/v1/handler_gen.go")

	modData := readCommandFile(t, root, "go.mod")
	parsed, err := modfile.Parse("go.mod", modData, nil)
	if err != nil {
		t.Fatalf("parse generated go.mod: %v", err)
	}
	requirements := make(map[string]*modfile.Require, len(parsed.Require))
	for _, requirement := range parsed.Require {
		requirements[requirement.Mod.Path] = requirement
	}
	for _, expected := range []struct {
		path    string
		version string
	}{
		{path: connectgen.ConnectModulePath, version: connectgen.ConnectModuleVersion},
		{path: connectgen.ProtobufModulePath, version: connectgen.ProtobufModuleVersion},
		{path: bootstrapgen.YAMLModulePath, version: bootstrapgen.YAMLModuleVersion},
	} {
		requirement, exists := requirements[expected.path]
		if !exists {
			t.Fatalf("generated go.mod omits %s", expected.path)
		}
		if requirement.Indirect {
			t.Fatalf("generated runtime requirement %s remains indirect", expected.path)
		}
		if semver.Compare(requirement.Mod.Version, expected.version) < 0 {
			t.Fatalf("generated runtime requirement %s = %s, want at least %s", expected.path, requirement.Mod.Version, expected.version)
		}
	}

	beforeCleanCheck := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/connect-runtime in "+root+"\n" {
		t.Fatalf("clean Connect check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCleanCheck) {
		t.Fatalf("clean generate --check changed the Project:\nbefore: %#v\nafter:  %#v", beforeCleanCheck, after)
	}
}

func TestRunGenerateRequiresConnectForJavaScriptSDKWithoutMutation(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/javascript-connect

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
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http: {transports: {connect: false, rest: true}, expose: [kernel.health/v1]}\n")
	before := commandTree(t, root)

	for _, arguments := range [][]string{{"generate", "--check"}, {"generate"}} {
		exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
		if exitCode != 1 || stdout != "" {
			t.Fatalf("%v = exit %d, stdout %q, stderr %q", arguments, exitCode, stdout, stderr)
		}
		for _, want := range []string{
			"invalid JavaScript SDK transport selection",
			`http.transports.connect is false for selected configuration "plystra.yaml"`,
			"official generated JavaScript SDK requires Connect for Capability kernel.health/v1",
			"enable http.transports.connect",
		} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v stderr %q does not contain %q", arguments, stderr, want)
			}
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("%v mutated rejected Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
		}
		assertNoCommandTransactions(t, root)
	}
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

func TestRunGenerateSelectsEnvironmentOverlayThroughPublicCommand(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/environment

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
	rootConfiguration := "# shared root\nhttp: {cors: {allowed_origins: [https://app.example.com], allow_credentials: true}}\ncapabilities: {require: [kernel.health/v1]}\n"
	overlayConfiguration := "# sparse production overlay\nhttp: {cors: {allow_credentials: null}}\ncapabilities:\n  require:\n    add: [kernel.info/v1]\n    remove: [kernel.health/v1]\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootConfiguration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)
	start := filepath.Join(root, "nested")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--env", "production"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/environment in "+root+"\n" {
		t.Fatalf("generate --env = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, root, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("environment generation changed root configuration:\n%s", got)
	}
	if got := string(readCommandFile(t, root, "plystra.production.yaml")); got != overlayConfiguration {
		t.Fatalf("environment generation changed overlay:\n%s", got)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil || provenance.Mode() != applicationgen.ConfigurationModeEnvironment || provenance.Environment() != "production" || provenance.SelectedPath() != "plystra.production.yaml" {
		t.Fatalf("environment provenance = mode %q environment %q path %q, %v", provenance.Mode(), provenance.Environment(), provenance.SelectedPath(), err)
	}
	assertCommandBootstrapProvenanceMatchesManifest(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production"}))
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/environment in "+root+"\n" {
		t.Fatalf("PLYSTRA_ENV check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	explicitEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "missing.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, start, explicitEnvironment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("explicit --env did not override ambient selectors = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	for _, runtime := range []struct {
		name        string
		arguments   []string
		environment []string
	}{
		{
			name:        "explicit environment overrides ambient",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--env", "production"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "missing", "PLYSTRA_CONFIG": "missing.yaml"}),
		},
		{
			name:        "ambient environment",
			arguments:   []string{"run", "./generated/go/application", "--smoke"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production"}),
		},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = root
			process.Env = runtime.environment
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("generated application %s: %v\n%s", runtime.name, err, output)
			}
		})
	}
	for _, runtime := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{name: "missing environment", arguments: []string{"run", "./generated/go/application", "--smoke", "--env", "missing"}, want: "requires plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"run", "./generated/go/application", "--smoke", "--env", "../production"}, want: "safe filename component"},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = root
			process.Env = environment
			output, err := process.CombinedOutput()
			if err == nil || !strings.Contains(string(output), runtime.want) {
				t.Fatalf("generated application %s = %v, %s", runtime.name, err, output)
			}
		})
	}

	beforeConflict := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "plystra.yaml"}))
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together") {
		t.Fatalf("ambient selector conflict = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeConflict) {
		t.Fatal("ambient selector conflict mutated the Project")
	}

	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"generate", "--check", "--env", "missing"}, want: "plystra.missing.yaml"},
		{arguments: []string{"generate", "--check", "--env", "../production"}, want: "safe filename component"},
	} {
		before := commandTree(t, root)
		exitCode, stdout, stderr = runCommand(t, test.arguments, start, environment)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
			t.Fatalf("generate %q = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("generate %q mutated the Project", test.arguments)
		}
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
	addLegacyProtobufReplacement(t, applicationRoot)
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
	assertCommandBootstrapProvenanceMatchesManifest(t, applicationRoot)
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

	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "root: [intentionally-invalid\n")
	for _, runtime := range []struct {
		name        string
		arguments   []string
		environment []string
	}{
		{
			name:        "explicit relative replacement overrides ambient selectors",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--config", "deploy/customer.yaml"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "missing.yaml", "PLYSTRA_ENV": "missing", "SELECTED_MAIL_TOKEN": "resolved-super-secret"}),
		},
		{
			name:        "explicit absolute replacement",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--config", selectedPath},
			environment: environment,
		},
		{
			name:        "ambient replacement",
			arguments:   []string{"run", "./generated/go/application", "--smoke"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml", "SELECTED_MAIL_TOKEN": "resolved-super-secret"}),
		},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = applicationRoot
			process.Env = runtime.environment
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("generated application %s: %v\n%s", runtime.name, err, output)
			}
		})
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)

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
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/configuration/email_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/wire-map.json",
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

func addLegacyProtobufReplacement(t testing.TB, moduleRoot string) {
	t.Helper()
	legacyRoot := filepath.Join(t.TempDir(), "legacy-protobuf")
	writeCommandFile(t, filepath.Join(legacyRoot, "go.mod"), "module github.com/golang/protobuf\n\ngo 1.26\n")
	goModPath := filepath.Join(moduleRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", goModPath, err)
	}
	data = append(data, []byte("\nreplace github.com/golang/protobuf => "+filepath.ToSlash(legacyRoot)+"\n")...)
	if err := os.WriteFile(goModPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", goModPath, err)
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

func assertCommandBootstrapProvenanceMatchesManifest(t testing.TB, root string) {
	t.Helper()
	manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	provenance, err := transportprovenance.New(transportprovenance.Input{
		Mode:                        generation.ConfigurationMode(manifest.Mode()),
		Environment:                 manifest.Environment(),
		RootPath:                    manifest.RootPath(),
		RootDigest:                  manifest.RootDigest(),
		SelectedPath:                manifest.SelectedPath(),
		SelectedDigest:              manifest.SelectedDigest(),
		DependencyCompositionDigest: manifest.DependencyBaseline().Digest(),
		ApplicationModelDigest:      manifest.ApplicationModelDigest(),
	})
	if err != nil {
		t.Fatalf("transportprovenance.New from generated manifest: %v", err)
	}
	bootstrap := readCommandFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	for _, required := range []string{
		strconv.Quote(string(provenance.CanonicalJSON())),
		strconv.Quote(provenance.Digest()),
	} {
		if !bytes.Contains(bootstrap, []byte(required)) {
			t.Fatalf("generated bootstrap provenance disagrees with generated manifest %q", required)
		}
	}
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
