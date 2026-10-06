package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicInertDependencyConfigurationDoesNotPublishValueHashes(t *testing.T) {
	for _, mode := range []struct {
		name     string
		selected string
		selector []string
	}{
		{name: "default", selected: "plystra.yaml"},
		{name: "environment", selected: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selected: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			if mode.selected != "plystra.yaml" {
				writeCommandFile(t, filepath.Join(root, mode.selected), "{}\n")
			}
			dependency := filepath.Join(t.TempDir(), "dependency")
			const module = "example.com/inert-dependency"
			writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module "+module+"\n\ngo 1.26\n")
			writeCommandFile(t, filepath.Join(dependency, "package.go"), "package dependency\n")
			document := func(marker string) string {
				return "config: {example.com/unavailable/service.New: {password: {env: " + marker + "}, " + marker + ": [1, 2]}}\n"
			}
			writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), document("PRIVATE_FIRST"))
			goMod := string(readCommandFile(t, root, "go.mod"))
			writeCommandFile(t, filepath.Join(root, "go.mod"), goMod+"\nrequire "+module+" v1.0.0\nreplace "+module+" => "+filepath.ToSlash(dependency)+"\n")

			invoke := func(arguments ...string) string {
				t.Helper()
				arguments = append(arguments, mode.selector...)
				code, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
					t.Fatalf("%v = %d, %q, %q", arguments, code, stdout, stderr)
				}
				return stdout
			}
			invoke("generate")
			before := commandTree(t, filepath.Join(root, "generated"))
			inspection := invoke("inspect", "configuration", "--format", "json")
			writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), document("PRIVATE_SECOND"))
			unchanged := commandTree(t, root)
			invoke("generate", "--check")
			invoke("check")
			if got := invoke("inspect", "configuration", "--format", "json"); got != inspection {
				t.Fatal("private dependency values changed public inspection")
			}
			if !reflect.DeepEqual(commandTree(t, root), unchanged) {
				t.Fatal("read-only checks mutated the Project")
			}
			invoke("generate")
			if !reflect.DeepEqual(commandTree(t, filepath.Join(root, "generated")), before) {
				t.Fatal("private dependency values changed public generated artifacts")
			}
			if got := string(readCommandFile(t, dependency, "plystra.yaml")); got != document("PRIVATE_SECOND") {
				t.Fatal("generation rewrote inert dependency values")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
