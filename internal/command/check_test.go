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

func TestRunCheckReportsEveryInheritedConfigurationConflictSource(t *testing.T) {
	parent := t.TempDir()
	dependencies := []struct {
		module      string
		version     string
		directory   string
		constructor string
	}{
		{module: "example.com/a", version: "v1.0.0", directory: "a", constructor: "example.com/implementation/primary.New"},
		{module: "example.com/b", version: "v1.1.0", directory: "b", constructor: "example.com/implementation/primary.New"},
		{module: "example.com/c", version: "v1.2.0", directory: "c", constructor: "example.com/implementation/secondary.New"},
	}
	dependencyTrees := make(map[string]map[string][]byte, len(dependencies))
	for _, dependency := range dependencies {
		root := filepath.Join(parent, dependency.directory)
		writeCommandFile(t, filepath.Join(root, "go.mod"), "module "+dependency.module+"\n\ngo 1.26\n")
		writeCommandFile(t, filepath.Join(root, "package.go"), "package placeholder\n")
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+dependency.constructor+"}}\n")
		dependencyTrees[root] = commandTree(t, root)
	}

	orders := [][]int{{0, 1, 2}, {2, 1, 0}}
	outputs := make([]string, len(orders))
	for orderIndex, order := range orders {
		root := filepath.Join(parent, fmt.Sprintf("application-%d", orderIndex))
		var moduleFile strings.Builder
		moduleFile.WriteString("module example.com/application\n\ngo 1.26\n\nrequire (\n")
		for _, dependencyIndex := range order {
			dependency := dependencies[dependencyIndex]
			fmt.Fprintf(&moduleFile, "\t%s %s\n", dependency.module, dependency.version)
		}
		moduleFile.WriteString(")\n\n")
		for _, dependencyIndex := range order {
			dependency := dependencies[dependencyIndex]
			fmt.Fprintf(&moduleFile, "replace %s => ../%s\n", dependency.module, dependency.directory)
		}
		writeCommandFile(t, filepath.Join(root, "go.mod"), moduleFile.String())
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
		before := commandTree(t, root)

		exitCode, stdout, stderr := runCommand(t, []string{"check"}, root, commandGoEnvironment())
		if exitCode != 1 || stdout != "" {
			t.Fatalf("check order %d = exit %d, stdout %q, stderr %q", orderIndex, exitCode, stdout, stderr)
		}
		for _, fragment := range []string{
			`interfaces.use["email.send/v1"]`,
			"example.com/implementation/primary.New",
			"example.com/implementation/secondary.New",
			`example.com/a@v1.0.0/plystra.yaml interfaces.use["email.send/v1"]`,
			`example.com/b@v1.1.0/plystra.yaml interfaces.use["email.send/v1"]`,
			`example.com/c@v1.2.0/plystra.yaml interfaces.use["email.send/v1"]`,
			"Set or remove the conflicting field explicitly in plystra.yaml, then rerun the command.",
			"Diagnostic: " + diagnosticcode.ConfigurationInheritedConflict,
		} {
			if !strings.Contains(stderr, fragment) {
				t.Fatalf("check order %d omits %q: %s", orderIndex, fragment, stderr)
			}
		}
		if strings.Count(stderr, "example.com/implementation/primary.New") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
			t.Fatalf("check order %d has unstable deduplication or recovery: %s", orderIndex, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("conflicting check order %d mutated the Project", orderIndex)
		}
		outputs[orderIndex] = stderr
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("conflict diagnostic depends on module declaration order:\nfirst: %s\nsecond: %s", outputs[0], outputs[1])
	}
	for root, before := range dependencyTrees {
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("conflicting check mutated dependency Project %s", root)
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
