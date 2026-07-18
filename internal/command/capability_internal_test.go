package command

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/plugintarget"
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
			arguments: []string{"capability", "create", "records.create"},
			want:      capabilityArguments{action: "create", reference: "records.create"},
			ok:        true,
		},
		{
			name:      "create explicit target and confirmation",
			arguments: []string{"capability", "create", "records.create/v3", "--confirm", "--plugin", "acme.records"},
			want:      capabilityArguments{action: "create", reference: "records.create/v3", plugin: "acme.records", confirm: true},
			ok:        true,
		},
		{
			name:      "implement explicit target",
			arguments: []string{"capability", "implement", "email.send/v1", "--plugin", "mailer"},
			want:      capabilityArguments{action: "implement", reference: "email.send/v1", plugin: "mailer"},
			ok:        true,
		},
		{name: "missing command", arguments: nil},
		{name: "wrong root", arguments: []string{"plugin", "create", "records.create"}},
		{name: "unknown action", arguments: []string{"capability", "remove", "records.create/v1"}},
		{name: "missing reference", arguments: []string{"capability", "create"}},
		{name: "option reference", arguments: []string{"capability", "create", "--confirm"}},
		{name: "duplicate confirmation", arguments: []string{"capability", "create", "records.create/v3", "--confirm", "--confirm"}},
		{name: "implement confirmation", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}},
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
		[]string{"capability", "create", "profile.get"},
		&stdout,
		&stderr,
		root,
		environment,
		plugintarget.Prompt(strings.NewReader("2\n"), &stderr),
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
	cliRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve CLI root: %v", err)
	}
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	root := t.TempDir()
	goMod := fmt.Sprintf("module example.com/acme/app\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(kernelRoot))
	writeInteractiveCapabilityFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeInteractiveCapabilityFile(t, filepath.Join(root, "go.sum"), string(goSum))
	for _, plugin := range []string{"account", "profile"} {
		writeInteractiveCapabilityFile(t, filepath.Join(root, plugin, "plugin.yaml"), "id: acme.app."+plugin+"\n")
		writeInteractiveCapabilityFile(t, filepath.Join(root, plugin, "plugin.go"), "package "+plugin+"\n\ntype Plugin struct{}\n")
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
