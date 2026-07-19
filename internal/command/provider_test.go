package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRunUseSelectsOnlyTheRequestedConfigurationLayer(t *testing.T) {
	tests := []struct {
		name        string
		selectors   []string
		environment map[string]string
		wantPath    string
		overlay     bool
	}{
		{name: "root", wantPath: "plystra.yaml"},
		{
			name:      "explicit environment overrides ambient selectors",
			selectors: []string{"--env", "production"},
			environment: map[string]string{
				"PLYSTRA_CONFIG": "deploy/ignored.yaml",
				"PLYSTRA_ENV":    "ignored",
			},
			wantPath: "plystra.production.yaml",
			overlay:  true,
		},
		{
			name:        "ambient environment",
			environment: map[string]string{"PLYSTRA_ENV": "staging"},
			wantPath:    "plystra.staging.yaml",
			overlay:     true,
		},
		{
			name:      "explicit replacement overrides ambient selectors",
			selectors: []string{"--config", "deploy/customer.yaml"},
			environment: map[string]string{
				"PLYSTRA_CONFIG": "deploy/ignored.yaml",
				"PLYSTRA_ENV":    "ignored",
			},
			wantPath: "deploy/customer.yaml",
		},
		{
			name:        "ambient replacement",
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/automation.yaml"},
			wantPath:    "deploy/automation.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeProviderCommandProject(t)
			configuration := map[string]string{
				"plystra.yaml":            "# Shared Provider choices.\ncapabilities:\n  require: [email.send/v1]\n",
				"plystra.production.yaml": "# Production Provider choices.\n{}\n",
				"plystra.staging.yaml":    "# Staging Provider choices.\n{}\n",
				"deploy/customer.yaml":    "# Customer Provider choices.\ncapabilities:\n  require: [email.send/v1]\n",
				"deploy/automation.yaml":  "# Automation Provider choices.\ncapabilities:\n  require: [email.send/v1]\n",
				"deploy/ignored.yaml":     "# Ignored replacement.\ncapabilities:\n  require: [email.send/v1]\n",
				"plystra.ignored.yaml":    "# Ignored overlay.\n{}\n",
			}
			for name, data := range configuration {
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(name)), data)
			}
			before := make(map[string]string, len(configuration))
			for name := range configuration {
				before[name] = string(readCommandFile(t, root, name))
			}

			environment := providerCommandEnvironment(test.environment)
			arguments := append([]string{"use", "email.send/v1", "acme.email.local"}, test.selectors...)
			exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "smtp"), environment)
			wantAbsolutePath := filepath.Join(root, filepath.FromSlash(test.wantPath))
			wantOutput := "selected Provider acme.email.local for email.send/v1 in " + wantAbsolutePath + "\n"
			if exitCode != 0 || stdout != wantOutput || stderr != "" {
				t.Fatalf("plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			selectedData := readCommandFile(t, root, test.wantPath)
			if !strings.Contains(string(selectedData), strings.Split(before[test.wantPath], "\n")[0]) {
				t.Fatalf("selected configuration lost its leading comment:\n%s", selectedData)
			}
			manifest, err := applicationmeta.Parse(selectedData)
			if test.overlay {
				manifest, err = applicationmeta.ParseOverlaySource(test.wantPath, selectedData)
			}
			if err != nil || !commandHasProviderChoice(manifest, "email.send/v1", "acme.email.local") {
				t.Fatalf("selected configuration Provider choices = %#v, %v\n%s", manifest.ProviderChoices(), err, selectedData)
			}
			for name, data := range before {
				if name == test.wantPath {
					continue
				}
				if got := string(readCommandFile(t, root, name)); got != data {
					t.Fatalf("plystra use changed unselected %s:\n%s", name, got)
				}
			}
			assembly := string(readCommandFile(t, root, "generated/go/assembly/invocations_gen.go"))
			if !strings.Contains(assembly, `"acme.email.local"`) || strings.Contains(assembly, `"acme.email.smtp"`) {
				t.Fatalf("generated invocation assembly does not use the explicit Provider:\n%s", assembly)
			}

			checkArguments := append([]string{"generate", "--check"}, test.selectors...)
			exitCode, stdout, stderr = runCommand(t, checkArguments, filepath.Join(root, "local"), environment)
			if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/provider-use in "+root+"\n" {
				t.Fatalf("post-selection generate --check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}

			if test.name == "root" {
				idempotentBefore := commandTree(t, root)
				exitCode, stdout, stderr = runCommand(t, arguments, filepath.Join(root, "local"), environment)
				wantOutput = "Provider acme.email.local is already selected for email.send/v1 in " + wantAbsolutePath + "\n"
				if exitCode != 0 || stdout != wantOutput || stderr != "" {
					t.Fatalf("idempotent plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, idempotentBefore) {
					t.Fatalf("idempotent plystra use changed the Project:\nbefore: %#v\nafter:  %#v", idempotentBefore, after)
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseRejectsInvalidChoicesAndRestoresTheProject(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		pluginID   string
		prepare    func(*testing.T, string)
		want       string
	}{
		{name: "intrinsic Capability", capability: "kernel.health/v1", pluginID: "acme.email.smtp", want: "intrinsic kernel.* Capability"},
		{name: "absent Capability", capability: "missing.operation/v1", pluginID: "acme.email.smtp", want: "no canonical requirement or visible provider declares this Capability"},
		{
			name:       "application Alias",
			capability: "mail.send/v1",
			pluginID:   "acme.email.smtp",
			prepare: func(t *testing.T, root string) {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n  aliases: {mail.send/v1: email.send/v1}\n")
			},
			want: "provider choices must name canonical Capabilities",
		},
		{name: "invalid Plugin ID", capability: "email.send/v1", pluginID: "Acme.Email", want: "parse Plugin ID"},
		{name: "unknown Plugin", capability: "email.send/v1", pluginID: "missing.email", want: "selected Plugin ID is not visible"},
		{name: "non-provider Plugin", capability: "email.send/v1", pluginID: "acme.other", want: "does not provide this exact Capability"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeProviderCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n  use: {email.send/v1: acme.email.smtp}\n")
			if test.prepare != nil {
				test.prepare(t, root)
			}
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"use", test.capability, test.pluginID}, filepath.Join(root, "smtp"), providerCommandEnvironment(nil))
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("rejected plystra use = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected plystra use changed the Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseRejectsAmbientSelectorConflictWithoutMutation(t *testing.T) {
	root := writeProviderCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "capabilities: {require: [email.send/v1]}\n")
	before := commandTree(t, root)
	environment := providerCommandEnvironment(map[string]string{
		"PLYSTRA_CONFIG": "deploy/customer.yaml",
		"PLYSTRA_ENV":    "production",
	})
	exitCode, stdout, stderr := runCommand(t, []string{"use", "email.send/v1", "acme.email.local"}, filepath.Join(root, "smtp"), environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together") {
		t.Fatalf("ambient selector conflict = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("ambient selector conflict changed the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func writeProviderCommandProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/provider-use

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "# Shared Provider choices.\ncapabilities:\n  require: [email.send/v1]\n")
	contract := "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"
	for _, provider := range []struct {
		directory string
		packageID string
		pluginID  string
		config    string
	}{
		{directory: "smtp", packageID: "smtp", pluginID: "acme.email.smtp", config: "SMTPConfig"},
		{directory: "local", packageID: "local", pluginID: "acme.email.local", config: "LocalConfig"},
	} {
		writeCommandFile(t, filepath.Join(root, provider.directory, "plugin.yaml"), "id: "+provider.pluginID+"\nprovides: [email.send/v1]\n")
		writeCommandFile(t, filepath.Join(root, provider.directory, "capabilities", "email.send", "v1", "capability.yaml"), contract)
		writeCommandFile(t, filepath.Join(root, provider.directory, "plugin.go"), fmt.Sprintf(`package %s

import (
	"context"

	configuration "example.com/acme/provider-use/generated/go/configuration"
	contract "example.com/acme/provider-use/generated/go/contracts/email/send/v1"
)

type Config = configuration.%s
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, provider.packageID, provider.config))
	}
	writeCommandFile(t, filepath.Join(root, "other", "plugin.yaml"), "id: acme.other\nprovides: [reports.read/v1]\n")
	writeCommandFile(t, filepath.Join(root, "other", "capabilities", "reports.read", "v1", "capability.yaml"), "id: reports.read/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(root, "other", "plugin.go"), `package other

import (
	"context"

	configuration "example.com/acme/provider-use/generated/go/configuration"
	contract "example.com/acme/provider-use/generated/go/contracts/reports/read/v1"
)

type Config = configuration.OtherConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Read(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize Provider command Project: %v", err)
	}
	return canonical
}

func commandHasProviderChoice(manifest applicationmeta.Manifest, capability, pluginID string) bool {
	for _, choice := range manifest.ProviderChoices() {
		if choice.Capability().String() == capability && choice.PluginID() == pluginID {
			return true
		}
	}
	return false
}

func providerCommandEnvironment(overrides map[string]string) []string {
	environment := commandGoEnvironment()
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "PLYSTRA_CONFIG") || strings.EqualFold(name, "PLYSTRA_ENV") {
			continue
		}
		filtered = append(filtered, entry)
	}
	keys := make([]string, 0, len(overrides))
	for name := range overrides {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		filtered = append(filtered, name+"="+overrides[name])
	}
	return filtered
}
