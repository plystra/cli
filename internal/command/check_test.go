package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunCheckUsesSelectedReadOnlyProjectWorkflow(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/check

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
	writeCommandFile(t, filepath.Join(root, "plystra.test.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "{}\n")
	start := filepath.Join(root, "nested")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"check"}, start, environment)
	if exitCode != 0 || stdout != "Project checks passed for example.com/acme/check in "+root+"\n" || stderr != "" {
		t.Fatalf("check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	drifted := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"check"}, start, environment)
	if exitCode != 1 || stdout != "" || stderr != "generated output is not current:\n  changed generated/manifest.json\n\nRecovery:\nRun `plystra generate` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.GeneratedDrift+"\n" {
		t.Fatalf("drifted check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, drifted) {
		t.Fatal("drifted check mutated the Project")
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--env", "test"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("generate --env = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	environmentSelection := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "test"})
	exitCode, stdout, stderr = runCommand(t, []string{"check"}, start, environmentSelection)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("PLYSTRA_ENV check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--config", "deploy/customer.yaml"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("generate --config = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	configSelection := commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"check"}, start, configSelection)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("PLYSTRA_CONFIG check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	explicitEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "missing.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"check", "--config", "deploy/customer.yaml"}, start, explicitEnvironment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("explicit --config check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	conflictBefore := commandTree(t, root)
	conflictEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "test", "PLYSTRA_CONFIG": "deploy/customer.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"check"}, start, conflictEnvironment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together") {
		t.Fatalf("selector conflict = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, conflictBefore) {
		t.Fatal("selector conflict mutated the Project")
	}

	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"check", "--env", "missing"}, want: "plystra.missing.yaml"},
		{arguments: []string{"check", "--env", "../test"}, want: "safe filename component"},
		{arguments: []string{"check", "--config", "../outside.yaml"}, want: "must identify a file within the Project root"},
	} {
		before := commandTree(t, root)
		exitCode, stdout, stderr = runCommand(t, test.arguments, start, environment)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
			t.Fatalf("check %q = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("check %q mutated the Project", test.arguments)
		}
	}
}

func TestRunCheckReportsGoTestFailureWithoutMutation(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf("module example.com/acme/failing-check\n\ngo 1.26\n\nrequire (\n\tgithub.com/plystra/kernel v0.0.0\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(root, "failure_test.go"), "package failingcheck\n\nimport \"testing\"\n\nfunc TestFailure(t *testing.T) { t.Fatal(\"project-check-sentinel\") }\n")
	before := commandTree(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"check"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "check Plystra Project: test Go packages") || !strings.Contains(stderr, "project-check-sentinel") {
		t.Fatalf("failing check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("failing check mutated the Project")
	}
}
