package command

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/testkernel"
)

func TestParseCapabilityArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      capabilityArguments
		ok        bool
	}{
		{
			name:      "create inferred target",
			arguments: []string{"capability", "create", "records.create", "--query"},
			want:      capabilityArguments{action: "create", reference: "records.create", query: true},
			ok:        true,
		},
		{
			name:      "create explicit target and confirmation",
			arguments: []string{"capability", "create", "records.create/v3", "--expose", "--query", "--confirm", "--interactive", "--plugin", "acme.records"},
			want:      capabilityArguments{action: "create", reference: "records.create/v3", plugin: "acme.records", interactive: true, confirm: true, expose: true, query: true},
			ok:        true,
		},
		{
			name:      "implement explicit target",
			arguments: []string{"capability", "implement", "email.send/v1", "--plugin", "mailer", "--interactive"},
			want:      capabilityArguments{action: "implement", reference: "email.send/v1", plugin: "mailer", interactive: true},
			ok:        true,
		},
		{
			name:      "expose exact capability",
			arguments: []string{"capability", "expose", "email.send/v1"},
			want:      capabilityArguments{action: "expose", reference: "email.send/v1"},
			ok:        true,
		},
		{
			name:      "expose environment capability",
			arguments: []string{"capability", "expose", "email.send/v1", "--env", "production"},
			want:      capabilityArguments{action: "expose", reference: "email.send/v1", environment: "production"},
			ok:        true,
		},
		{
			name:      "expose replacement capability",
			arguments: []string{"capability", "expose", "email.send/v1", "--config", "deploy/customer.yaml"},
			want:      capabilityArguments{action: "expose", reference: "email.send/v1", config: "deploy/customer.yaml"},
			ok:        true,
		},
		{
			name:      "expose conflicting selectors reaches the semantic boundary",
			arguments: []string{"capability", "expose", "email.send/v1", "--env", "test", "--config", "deploy.yaml"},
			want:      capabilityArguments{action: "expose", reference: "email.send/v1", config: "deploy.yaml", environment: "test"},
			ok:        true,
		},
		{name: "missing command", arguments: nil},
		{name: "wrong root", arguments: []string{"plugin", "create", "records.create"}},
		{name: "unknown action", arguments: []string{"capability", "remove", "records.create/v1"}},
		{name: "missing reference", arguments: []string{"capability", "create"}},
		{name: "option reference", arguments: []string{"capability", "create", "--confirm"}},
		{name: "duplicate confirmation", arguments: []string{"capability", "create", "records.create/v3", "--confirm", "--confirm"}},
		{name: "implement confirmation", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}},
		{name: "implement exposure", arguments: []string{"capability", "implement", "records.create/v1", "--expose"}},
		{name: "duplicate interaction", arguments: []string{"capability", "create", "records.create/v1", "--interactive", "--interactive"}},
		{name: "expose interaction", arguments: []string{"capability", "expose", "records.create/v1", "--interactive"}},
		{name: "duplicate exposure", arguments: []string{"capability", "create", "records.create/v1", "--expose", "--expose"}},
		{name: "duplicate query profile", arguments: []string{"capability", "create", "records.create/v1", "--query", "--query"}},
		{name: "implement query profile", arguments: []string{"capability", "implement", "records.create/v1", "--query"}},
		{name: "invalid profile", arguments: []string{"capability", "create", "records.create/v1", "--mutation"}},
		{name: "expose option", arguments: []string{"capability", "expose", "records.create/v1", "--confirm"}},
		{name: "expose missing environment", arguments: []string{"capability", "expose", "records.create/v1", "--env"}},
		{name: "expose duplicate environment", arguments: []string{"capability", "expose", "records.create/v1", "--env", "test", "--env", "production"}},
		{name: "expose missing configuration", arguments: []string{"capability", "expose", "records.create/v1", "--config"}},
		{name: "expose duplicate configuration", arguments: []string{"capability", "expose", "records.create/v1", "--config", "a.yaml", "--config", "b.yaml"}},
		{name: "missing plugin", arguments: []string{"capability", "create", "records.create", "--plugin"}},
		{name: "option plugin", arguments: []string{"capability", "create", "records.create", "--plugin", "--confirm"}},
		{name: "duplicate plugin", arguments: []string{"capability", "create", "records.create", "--plugin", "first", "--plugin", "second"}},
		{name: "unknown option", arguments: []string{"capability", "create", "records.create", "--force"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCapabilityArguments(test.arguments)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseCapabilityArguments(%q) = %#v, %t; want %#v, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestRunCapabilityPromptsForAmbiguousPluginTarget(t *testing.T) {
	root := writeInteractiveCapabilityModule(t)
	environment := interactiveCapabilityEnvironment()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runIn(
		[]string{"capability", "create", "profile.get", "--query", "--interactive"},
		&stdout,
		&stderr,
		root,
		environment,
		plugintarget.Prompt(strings.NewReader("2\n"), &stderr),
		nil,
	)
	wantPath := filepath.Join(root, "profile", "capabilities", "profile.get", "v1", "capability.yaml")
	wantOutput := "created capability profile.get/v1 in acme.app.profile at " + wantPath + "\n"
	if exitCode != 0 || stdout.String() != wantOutput {
		t.Fatalf("interactive capability create = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	wantPrompt := "Multiple local plugins:\n  1. acme.app.account (account)\n  2. acme.app.profile (profile)\nSelect plugin [1-2]: "
	if stderr.String() != wantPrompt {
		t.Fatalf("interactive prompt = %q, want %q", stderr.String(), wantPrompt)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("selected plugin Capability: %v", err)
	}
	unexpected := filepath.Join(root, "account", "capabilities", "profile.get", "v1", "capability.yaml")
	if _, err := os.Stat(unexpected); !os.IsNotExist(err) {
		t.Fatalf("unselected plugin Capability exists: %v", err)
	}
}

func TestRunCapabilityDoesNotUseAvailableSelectorWithoutInteractive(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
	}{
		{name: "create", arguments: []string{"capability", "create", "profile.get", "--query"}},
		{name: "implement", arguments: []string{"capability", "implement", "profile.get/v1"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeInteractiveCapabilityModule(t)
			selected := false
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runIn(
				test.arguments,
				&stdout,
				&stderr,
				root,
				interactiveCapabilityEnvironment(),
				func([]plugintarget.Target) (int, error) {
					selected = true
					return 0, nil
				},
				nil,
			)
			if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Diagnostic: "+diagnosticcode.PluginTargetAmbiguous+"\n") {
				t.Fatalf("non-interactive %s = exit %d, stdout %q, stderr %q", test.name, exitCode, stdout.String(), stderr.String())
			}
			if selected {
				t.Fatal("available selector was called without --interactive")
			}
		})
	}
}

func TestRunCapabilityRequestedInteractionFailsWithoutTerminalWhenAmbiguous(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
	}{
		{name: "create", arguments: []string{"capability", "create", "profile.get", "--query", "--interactive"}},
		{name: "implement", arguments: []string{"capability", "implement", "profile.get/v1", "--interactive"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeInteractiveCapabilityModule(t)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runIn(test.arguments, &stdout, &stderr, root, interactiveCapabilityEnvironment(), nil, nil)
			if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Diagnostic: "+diagnosticcode.PluginTargetInvalid+"\n") || strings.Contains(stderr.String(), "Source:") {
				t.Fatalf("unavailable interactive %s = exit %d, stdout %q, stderr %q", test.name, exitCode, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "input and output are required") {
				t.Fatalf("unavailable interactive %s omitted terminal failure: %q", test.name, stderr.String())
			}
		})
	}
}

func TestRunCapabilityRequestedInteractionPreservesUnambiguousResolution(t *testing.T) {
	for _, test := range []struct {
		name       string
		plugins    []string
		start      func(string) string
		arguments  []string
		wantPlugin string
	}{
		{
			name:       "explicit Plugin",
			plugins:    []string{"account", "profile"},
			start:      func(root string) string { return root },
			arguments:  []string{"capability", "create", "profile.explicit", "--query", "--plugin", "profile", "--interactive"},
			wantPlugin: "profile",
		},
		{
			name:       "enclosing Plugin",
			plugins:    []string{"account", "profile"},
			start:      func(root string) string { return filepath.Join(root, "profile") },
			arguments:  []string{"capability", "create", "profile.enclosing", "--query", "--interactive"},
			wantPlugin: "profile",
		},
		{
			name:       "sole Plugin",
			plugins:    []string{"profile"},
			start:      func(root string) string { return root },
			arguments:  []string{"capability", "create", "profile.sole", "--query", "--interactive"},
			wantPlugin: "profile",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeInteractiveCapabilityModuleWithPlugins(t, test.plugins...)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runIn(test.arguments, &stdout, &stderr, test.start(root), interactiveCapabilityEnvironment(), nil, nil)
			if exitCode != 0 || stderr.Len() != 0 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", test.name, exitCode, stdout.String(), stderr.String())
			}
			wantPath := filepath.Join(root, test.wantPlugin, "capabilities", test.arguments[2], "v1", "capability.yaml")
			if _, err := os.Stat(wantPath); err != nil {
				t.Fatalf("%s target: %v", test.name, err)
			}
		})
	}
}

func TestTerminalPluginSelectorRejectsNonTerminalStreams(t *testing.T) {
	if selector := terminalPluginSelector(os.Stdin, &bytes.Buffer{}); selector != nil {
		t.Fatal("buffer output enabled interactive selection")
	}
	file, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer file.Close()
	if selector := terminalPluginSelector(file, file); selector != nil {
		t.Fatal("regular files enabled interactive selection")
	}
}

func writeInteractiveCapabilityModule(t *testing.T) string {
	t.Helper()
	return writeInteractiveCapabilityModuleWithPlugins(t, "account", "profile")
}

func writeInteractiveCapabilityModuleWithPlugins(t *testing.T, plugins ...string) string {
	t.Helper()
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := testkernel.Root(t)
	root := t.TempDir()
	goMod := fmt.Sprintf("module example.com/acme/app\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(kernelRoot))
	writeInteractiveCapabilityFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeInteractiveCapabilityFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeInteractiveCapabilityFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	for _, plugin := range plugins {
		writeInteractiveCapabilityFile(t, filepath.Join(root, plugin, "plugin.yaml"), "id: acme.app."+plugin+"\n")
		writeInteractiveCapabilityFile(t, filepath.Join(root, plugin, "plugin.go"), "package "+plugin+"\n\ntype Plugin struct{}\n\nfunc New(_ ...any) *Plugin { return &Plugin{} }\n")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize module: %v", err)
	}
	return canonical
}

func writeInteractiveCapabilityFile(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", name, err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func interactiveCapabilityEnvironment() []string {
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
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}
